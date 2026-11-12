package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"repotory/internal/db"
)

// stubEmbedServer returns a fake OpenAI-compatible /v1/embeddings endpoint:
// "alpha"->[1,0], "beta"->[0,1], anything else->[1,0] (so an unrelated query
// vector is nearest to alpha). Exercises the whole A path without a real model.
func stubEmbedServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req hybridEmbedReq
		_ = json.Unmarshal(b, &req)
		type datum struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		data := make([]datum, len(req.Input))
		for i, s := range req.Input {
			var v []float32
			switch s {
			case "beta":
				v = []float32{0, 1}
			default: // "alpha" and any query text
				v = []float32{1, 0}
			}
			data[i] = datum{Index: i, Embedding: v}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}

type hybridEmbedReq struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

func TestSemanticSearchEndToEnd(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "semproj", "/tmp/semproj")
	if _, err := db.CreateNoteEx(svc.db, pid, "alpha", "", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateNoteEx(svc.db, pid, "beta", "", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}

	srv := stubEmbedServer(t)
	defer srv.Close()
	for k, v := range map[string]string{
		"embedding_base_url": srv.URL + "/v1",
		"embedding_model":    "stub",
		"embedding_dim":      "2",
		"semantic_search":    "1",
	} {
		if err := svc.UpdateConfig(k, v); err != nil {
			t.Fatalf("config %s: %v", k, err)
		}
	}

	// Index not built yet -> fuse is a graceful no-op (vector index missing).
	query := "totally unrelated zzz"
	if hits := svc.mustSearchNotes(query); len(hits) != 0 {
		t.Fatalf("before rebuild, expected lexical-only empty, got %d", len(hits))
	}

	n, err := svc.RebuildEmbeddings()
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if n != 2 {
		t.Fatalf("embedded %d, want 2", n)
	}

	// Now the query (lexically matching nothing) recalls "alpha" via vectors.
	hits := svc.mustSearchNotes(query)
	if len(hits) == 0 {
		t.Fatal("expected semantic recall of alpha, got 0 hits")
	}
	if hits[0].Title != "alpha" {
		t.Errorf("top hit = %q, want alpha", hits[0].Title)
	}

	// Turning semantic off restores pure-lexical behaviour (alpha no longer recalled).
	if err := svc.UpdateConfig("semantic_search", "0"); err != nil {
		t.Fatal(err)
	}
	if hits := svc.mustSearchNotes(query); len(hits) != 0 {
		t.Errorf("with semantic off, expected 0 hits, got %d", len(hits))
	}
}

func TestRebuildEmbeddingsRequiresEndpoint(t *testing.T) {
	svc, _ := setupService(t)
	if _, err := svc.RebuildEmbeddings(); err == nil {
		t.Error("expected error when embedding endpoint unconfigured")
	}
}
