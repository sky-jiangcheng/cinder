package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"repotory/internal/db"
)

// gatedEmbedServer is stubEmbedServer with a release gate: every embedding
// request blocks until the test closes `release`. That is what makes the
// "refining" state observable — with an instant stub the job could reach done
// before the assertion runs, and the test would pass for the wrong reason.
type gatedEmbedServer struct {
	*httptest.Server
	release chan struct{}
	once    sync.Once
	// hold is off while the index is being built (RebuildEmbeddings embeds the
	// seeded notes and would deadlock on a gate that is not open yet), and on
	// from the moment the test wants the query embed to hang.
	hold atomic.Bool
}

func newGatedEmbedServer(t *testing.T) *gatedEmbedServer {
	t.Helper()
	g := &gatedEmbedServer{release: make(chan struct{})}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.hold.Load() {
			<-g.release
		}
		b, _ := io.ReadAll(r.Body)
		var req hybridEmbedReq
		_ = json.Unmarshal(b, &req)
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
	return g
}

// holdQuery makes the next embedding request hang until open().
func (g *gatedEmbedServer) holdQuery() { g.hold.Store(true) }

func (g *gatedEmbedServer) open() {
	g.once.Do(func() { close(g.release) })
}

// armSemantic points the service at a stub embedder and builds the index, the
// same configuration TestSemanticSearchEndToEnd uses.
func armSemantic(t *testing.T, svc *Service, url string) {
	t.Helper()
	pid := seedProject(t, svc.db, "jobproj", "/tmp/jobproj")
	for _, title := range []string{"alpha", "beta"} {
		if _, err := db.CreateNoteEx(svc.db, pid, title, "", "", "other", "manual"); err != nil {
			t.Fatal(err)
		}
	}
	for k, v := range map[string]string{
		"embedding_base_url": url + "/v1",
		"embedding_model":    "stub",
		"embedding_dim":      "2",
		"semantic_search":    "1",
	} {
		if err := svc.UpdateConfig(k, v); err != nil {
			t.Fatalf("config %s: %v", k, err)
		}
	}
	if n, err := svc.RebuildEmbeddings(); err != nil || n != 2 {
		t.Fatalf("rebuild: n=%d err=%v", n, err)
	}
}

// With semantic search off — the default — there is no remote stage, so the
// job must come back finished. A job that said "refining" here would make
// every default install poll for nothing.
func TestStartSearchJobLocalOnlyIsImmediate(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "localproj", "/tmp/localproj")
	if _, err := db.CreateNoteEx(svc.db, pid, "handoff notes", "body about zsh", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}

	job, err := svc.StartSearchJob(context.Background(), "handoff")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != SearchJobDone {
		t.Fatalf("state = %q, want %q (no embedder configured)", job.State, SearchJobDone)
	}
	if job.ID != "" {
		t.Fatalf("id = %q, want empty: a finished job needs no handle", job.ID)
	}
	if len(job.Hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(job.Hits))
	}
	if job.Hits[0].Recall == nil || job.Hits[0].Recall[0] != "lexical" {
		t.Fatalf("recall provenance missing: %+v", job.Hits[0].Recall)
	}

	// Same answer as the synchronous path by construction, but pin it: the
	// frontend replaces one with the other and a divergence would be invisible.
	want, err := svc.SearchAll(context.Background(), "handoff")
	if err != nil {
		t.Fatal(err)
	}
	if len(want) != len(job.Hits) {
		t.Fatalf("job hits %d != SearchAll hits %d", len(job.Hits), len(want))
	}
}

func TestStartSearchJobEmptyQuery(t *testing.T) {
	svc, _ := setupService(t)
	job, err := svc.StartSearchJob(context.Background(), "   ")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != SearchJobDone || len(job.Hits) != 0 || job.ID != "" {
		t.Fatalf("empty query job = %+v", job)
	}
	if _, err := svc.GetSearchJob("nope"); err == nil {
		t.Fatal("unknown job id must be an error, not an empty result")
	}
}

// The whole point: the first answer arrives without waiting for the remote
// embedding, and the refined answer arrives through polling.
func TestStartSearchJobRefinesInBackground(t *testing.T) {
	svc, _ := setupService(t)
	srv := newGatedEmbedServer(t)
	defer srv.Close()
	armSemantic(t, svc, srv.URL)
	srv.holdQuery()

	// The query matches nothing lexically; only the semantic arm can find
	// "alpha" — so any hit at all proves the refinement ran.
	job, err := svc.StartSearchJob(context.Background(), "totally unrelated zzz")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != SearchJobRefining {
		t.Fatalf("state = %q, want %q", job.State, SearchJobRefining)
	}
	if job.ID == "" {
		t.Fatal("a refining job needs an id to poll")
	}
	if len(job.Hits) != 0 {
		t.Fatalf("lexical hits = %d, want 0 (query matches nothing locally)", len(job.Hits))
	}

	// Polling before the embed returns must still say "refining" — the query
	// embed is what the job is waiting on, so nothing can have finished yet.
	if again, err := svc.GetSearchJob(job.ID); err != nil || again.State != SearchJobRefining {
		t.Fatalf("early poll = %+v, err=%v", again, err)
	}

	srv.open()

	deadline := time.Now().Add(10 * time.Second)
	var final *SearchJob
	for time.Now().Before(deadline) {
		got, err := svc.GetSearchJob(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State == SearchJobDone {
			final = got
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if final == nil {
		t.Fatal("job never reached done")
	}
	if len(final.Hits) == 0 || final.Hits[0].Title != "alpha" {
		t.Fatalf("refined hits = %+v, want the semantic-only note first", final.Hits)
	}
}

// Two submissions of the same query while one is still refining share a job.
// Without this, a debounce storm starts one remote embed per keystroke.
func TestStartSearchJobSharesInFlightJob(t *testing.T) {
	svc, _ := setupService(t)
	srv := newGatedEmbedServer(t)
	defer srv.Close()
	armSemantic(t, svc, srv.URL)
	srv.holdQuery()

	first, err := svc.StartSearchJob(context.Background(), "unrelated zzz")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.StartSearchJob(context.Background(), "unrelated zzz")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("ids differ: %q vs %q", first.ID, second.ID)
	}
	// Drain before the test ends: the refine goroutine belongs to the service,
	// and letting it outlive setupService's cleanup would race the closed DB.
	srv.open()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got, err := svc.GetSearchJob(first.ID); err == nil && got.State == SearchJobDone {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("shared job never reached done")
}

// The registry must not grow without bound: abandoned jobs pin their hit
// slices, and a user scrolling a long result list can start plenty.
func TestSearchJobStoreEvictsBeyondCap(t *testing.T) {
	var st searchJobStore
	for i := 0; i < searchJobCap+5; i++ {
		st.put(&SearchJob{ID: st.nextID(), Query: "q", State: SearchJobRefining})
	}
	st.mu.Lock()
	live := len(st.jobs)
	order := len(st.order)
	st.mu.Unlock()
	if live != searchJobCap || order != searchJobCap {
		t.Fatalf("retained %d jobs / %d order entries, want %d", live, order, searchJobCap)
	}
}

// A finish published after eviction is dropped, not a panic and not a
// resurrected entry.
func TestSearchJobStoreFinishAfterEviction(t *testing.T) {
	var st searchJobStore
	st.put(&SearchJob{ID: "gone", Query: "q", State: SearchJobRefining})
	st.mu.Lock()
	delete(st.jobs, "gone")
	st.mu.Unlock()
	st.finish("gone", nil)
	if _, ok := st.get("gone"); ok {
		t.Fatal("evicted job came back")
	}
}
