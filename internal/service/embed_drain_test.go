package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"repotory/internal/db"
)

// countedEmbedServer is stubEmbedServer with a hit counter, because several
// guarantees here are "no request must ever leave the app" (switch off, unknown
// dimension, index not built) and the only way to prove that is to count.
func countedEmbedServer(t *testing.T, failing bool) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if failing {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"backend down"}`))
			return
		}
		// Same mapping as stubEmbedServer: "beta"->[0,1], anything else->[1,0].
		var req hybridEmbedReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		type datum struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		data := make([]datum, len(req.Input))
		for i, s := range req.Input {
			v := []float32{1, 0}
			if s == "beta" {
				v = []float32{0, 1}
			}
			data[i] = datum{Index: i, Embedding: v}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// The point of the feature: a note written after the index was built becomes
// findable without another full rebuild — including when it arrives through the
// importer path (a direct db write that never touches this package).
func TestDrainIndexesNewNotesAndUnIndexesDeletedOnes(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "drain", "/tmp/drain")
	srv, hits := countedEmbedServer(t, false)
	configureEmbedding(t, svc, srv.URL, "2")

	if _, err := db.CreateNoteEx(svc.db, pid, "alpha", "", "", "knowledge", "manual"); err != nil {
		t.Fatal(err)
	}
	// First rebuild creates the index and persists the learned dim; after this
	// point nothing may ever require a rebuild again.
	n, err := svc.RebuildEmbeddings()
	if err != nil || n != 1 {
		t.Fatalf("rebuild: n=%d err=%v", n, err)
	}
	// Direct db write = the plugin runtime / importer path. service.CreateNote is
	// deliberately NOT used: it is the path the triggers cover anyway.
	if _, err := db.CreateNoteEx(svc.db, pid, "beta", "", "", "knowledge", "manual"); err != nil {
		t.Fatal(err)
	}

	before := hits.Load()
	got, err := svc.drainDirtyNotes(10)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if got != 1 {
		t.Fatalf("drain resolved %d, want 1 (the newly imported note)", got)
	}
	if hits.Load() <= before {
		t.Fatal("drain resolved the note without calling the embedding endpoint")
	}

	ids, err := db.KnnNoteIDs(svc.db, []float32{0, 1}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) == 0 {
		t.Fatal("KNN after drain returned nothing; the incremental vector never landed")
	}

	// Deleting must remove the vector, or search keeps recalling text the user
	// deleted.
	if err := db.DeleteNote(svc.db, ids[0]); err != nil {
		t.Fatal(err)
	}
	deletesBefore := hits.Load()
	if got, err := svc.drainDirtyNotes(10); err != nil || got != 1 {
		t.Fatalf("drain after delete: got=%d err=%v, want 1 resolved", got, err)
	}
	if hits.Load() != deletesBefore {
		t.Error("draining a deletion must not cost an embedding request (no text to embed)")
	}
	remaining, err := db.KnnNoteIDs(svc.db, []float32{0, 1}, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range remaining {
		if id == ids[0] {
			t.Fatalf("deleted note %d is still recallable after the drain", id)
		}
	}
}

// Everything here sits behind semantic_search, which is the user's explicit
// consent to send note text to the configured endpoint. With the switch off, not
// one request may leave the app — and the queue must keep the work for later.
func TestDrainIsSilentUntilSemanticSearchIsOn(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "gate", "/tmp/gate")
	srv, hits := countedEmbedServer(t, false)
	configureEmbedding(t, svc, srv.URL, "2")
	if _, err := db.CreateNoteEx(svc.db, pid, "alpha", "", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}
	// Build the index once so the only thing gating later rounds is the switch
	// itself (an unbuilt index is a separate gate, tested elsewhere).
	if _, err := svc.RebuildEmbeddings(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateNoteEx(svc.db, pid, "beta", "", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateConfig("semantic_search", "0"); err != nil {
		t.Fatal(err)
	}

	if got, err := svc.drainDirtyNotes(10); err != nil || got != 0 {
		t.Fatalf("drain with semantic off: got=%d err=%v, want 0/no error", got, err)
	}
	if hits.Load() != 1 {
		// One request so far: the rebuild above. Nothing may have been added while
		// the switch was off.
		t.Fatalf("expected exactly the rebuild's request before the flip, got %d", hits.Load())
	}
	assertStillQueued(t, svc)

	if err := svc.UpdateConfig("semantic_search", "1"); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.drainDirtyNotes(10); err != nil || got != 1 {
		t.Fatalf("drain after switching on: got=%d err=%v, want the retained work processed", got, err)
	}
}

// Two independent gates, both required: a known dimension (guessing one means
// calling Ensure with the wrong width, which drops and rebuilds the whole index)
// and an index that exists at all (writing into a never-created vec0 table only
// errors). Neither may consume the queue.
func TestDrainWaitsForKnownDimAndExistingIndex(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "gates2", "/tmp/gates2")
	srv, hits := countedEmbedServer(t, false)
	for k, v := range map[string]string{
		"embedding_base_url": srv.URL + "/v1",
		"embedding_model":    "stub",
		"semantic_search":    "1",
	} {
		if err := svc.UpdateConfig(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.CreateNoteEx(svc.db, pid, "alpha", "", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}

	// Gate 1: no dimension known (no embedding_dim, no index meta).
	if got, err := svc.drainDirtyNotes(10); err != nil || got != 0 {
		t.Fatalf("drain with unknown dim: got=%d err=%v, want 0", got, err)
	}
	if hits.Load() != 0 {
		t.Fatalf("unknown dim still made %d request(s)", hits.Load())
	}
	assertStillQueued(t, svc)

	// Gate 2: dimension known, but the index was never built.
	if err := svc.UpdateConfig("embedding_dim", "2"); err != nil {
		t.Fatal(err)
	}
	if db.VectorIndexReady(svc.db) {
		t.Fatal("pre-condition: index should not exist yet")
	}
	if got, err := svc.drainDirtyNotes(10); err != nil || got != 0 {
		t.Fatalf("drain with no index: got=%d err=%v, want 0", got, err)
	}
	if hits.Load() != 0 {
		t.Fatalf("a missing index still made %d request(s)", hits.Load())
	}
	assertStillQueued(t, svc)
}

// A failed endpoint must lose nothing. The queue is the only reason an offline
// laptop is safe: it retries on a later tick instead of dropping the write.
func TestDrainRetainsQueueWhenEndpointIsDown(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "down", "/tmp/down")
	srv, _ := countedEmbedServer(t, false)
	configureEmbedding(t, svc, srv.URL, "2")
	if _, err := db.CreateNoteEx(svc.db, pid, "alpha", "", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.RebuildEmbeddings(); err != nil || n != 1 {
		t.Fatalf("rebuild: n=%d err=%v", n, err)
	}

	dead, _ := countedEmbedServer(t, true) // always 502
	if err := svc.UpdateConfig("embedding_base_url", dead.URL+"/v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateNoteEx(svc.db, pid, "beta", "", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.drainDirtyNotes(10); err != nil || got != 0 {
		t.Fatalf("drain against a dead endpoint: got=%d err=%v, want 0 resolved and no panic", got, err)
	}
	assertStillQueued(t, svc)

	// Recovery: point it back and the same tick's worth of work lands.
	if err := svc.UpdateConfig("embedding_base_url", srv.URL+"/v1"); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.drainDirtyNotes(10); err != nil || got != 1 {
		t.Fatalf("drain after recovery: got=%d err=%v, want 1", got, err)
	}
}

// A completed rebuild embeds everything, so it may empty the queue. A rebuild
// that bailed halfway must NOT — those rows are the only proof the notes were
// never indexed.
func TestRebuildTruncatesQueueOnlyOnSuccess(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "trunc", "/tmp/trunc")
	srv, _ := countedEmbedServer(t, false)
	configureEmbedding(t, svc, srv.URL, "2")
	if _, err := db.CreateNoteEx(svc.db, pid, "alpha", "", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RebuildEmbeddings(); err != nil {
		t.Fatal(err)
	}
	assertQueueEmpty(t, svc, "after the first successful rebuild")

	// A new write queues work again.
	if _, err := db.CreateNoteEx(svc.db, pid, "beta", "", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}
	assertStillQueued(t, svc)

	// A rebuild whose endpoint dies must leave that row behind AND surface the
	// failure: returning (partial, nil) let callers report a half-finished
	// rebuild as success. The durable queue is still what keeps the notes safe.
	dead, _ := countedEmbedServer(t, true)
	if err := svc.UpdateConfig("embedding_base_url", dead.URL+"/v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RebuildEmbeddings(); err == nil {
		t.Fatal("bailed rebuild returned nil error; the endpoint failure was swallowed")
	}
	assertStillQueued(t, svc)

	// With the endpoint restored, a successful rebuild clears it.
	if err := svc.UpdateConfig("embedding_base_url", srv.URL+"/v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RebuildEmbeddings(); err != nil {
		t.Fatal(err)
	}
	assertQueueEmpty(t, svc, "after the rebuild that completed")
}

// A store that refuses a deletion must not silently consume the queue row: the
// deleted note's vector would otherwise stay recallable forever with no record of
// why, and the memo would keep pointing at a handle that just failed.
func TestDrainRetainsDeletionWhenStoreRejectsIt(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "delerr", "/tmp/delerr")
	srv, _ := countedEmbedServer(t, false)
	configureEmbedding(t, svc, srv.URL, "2")
	note, err := db.CreateNoteEx(svc.db, pid, "alpha", "", "", "other", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RebuildEmbeddings(); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteNote(svc.db, note.ID); err != nil {
		t.Fatal(err)
	}

	svc.vecStore.store = downStore{}
	if got, err := svc.drainDirtyNotes(10); err != nil || got != 0 {
		t.Fatalf("drain with a store that rejects Delete: got=%d err=%v, want 0 resolved", got, err)
	}
	if svc.vecStore.store != nil {
		t.Error("a store that rejected Delete kept its memo slot")
	}
	assertStillQueued(t, svc)
}

// configureEmbedding sets the endpoint, model and dimension and turns the feature
// on, the same four keys the settings page writes.
func configureEmbedding(t *testing.T, svc *Service, url, dim string) {
	t.Helper()
	for k, v := range map[string]string{
		"embedding_base_url": url + "/v1",
		"embedding_model":    "stub",
		"embedding_dim":      dim,
		"semantic_search":    "1",
	} {
		if err := svc.UpdateConfig(k, v); err != nil {
			t.Fatalf("config %s: %v", k, err)
		}
	}
}

func assertStillQueued(t *testing.T, svc *Service) {
	t.Helper()
	var n int
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM note_embed_dirty").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("dirty queue emptied although the work never landed")
	}
}

func assertQueueEmpty(t *testing.T, svc *Service, when string) {
	t.Helper()
	var n int
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM note_embed_dirty").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%s: %d row(s) left in the dirty queue, want 0", when, n)
	}
}
