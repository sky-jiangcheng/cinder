//go:build aelive

// Opt-in FULL end-to-end test of M3-A against real local services:
// Ollama embeds notes (axis A) → vectors stored in a REAL Qdrant (axis B, the
// "remote" path simulated on localhost) → SearchNotes fuses vector recall that
// lexical FTS5 misses. Run:
//
//	ollama serve & ; ollama pull nomic-embed-text
//	docker run -d -p 6333:6333 qdrant/qdrant
//	go test -tags aelive ./internal/service/ -run SemanticLive -v
package service

import (
	"os"
	"testing"

	"repotory/internal/db"
	"repotory/internal/search/vectordb"
)

func TestSemanticLiveEndToEnd(t *testing.T) {
	ollama := os.Getenv("OLLAMA_URL")
	if ollama == "" {
		ollama = "http://localhost:11434/v1"
	}
	qdrant := os.Getenv("QDRANT_URL")
	if qdrant == "" {
		qdrant = "http://localhost:6333"
	}
	if probe, err := vectordb.NewQdrant(qdrant, "", "repotory_aelive_smoke"); err != nil || !probe.Reachable() {
		t.Skipf("Qdrant not reachable at %s", qdrant)
	}

	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "liveproj", "/tmp/liveproj")
	if _, err := db.CreateNoteEx(svc.db, pid, "greeting note", "says hello and greets the user", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateNoteEx(svc.db, pid, "unrelated", "zzz quux foo", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}

	for k, v := range map[string]string{
		"semantic_search":         "1",
		"embedding_base_url":      ollama,
		"embedding_model":         "nomic-embed-text",
		"embedding_dim":           "768",
		"vector_store":            "qdrant",
		"vector_store_url":        qdrant,
		"vector_store_collection": "repotory_aelive_smoke",
	} {
		if err := svc.UpdateConfig(k, v); err != nil {
			t.Fatalf("config %s: %v", k, err)
		}
	}

	n, err := svc.RebuildEmbeddings() // Ollama embeds both notes -> Qdrant
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if n != 2 {
		t.Fatalf("embedded %d, want 2", n)
	}

	// "bonjour salutation" shares no token with the notes -> lexical FTS5 finds none.
	query := "bonjour salutation"
	if lex, _ := db.SearchNotes(svc.db, query); len(lex) != 0 {
		t.Fatalf("expected lexical to miss, got %d (bad fixture)", len(lex))
	}

	hits := svc.mustSearchNotes(query) // vector recall via real Qdrant
	if len(hits) == 0 {
		t.Fatal("semantic search via Qdrant returned nothing")
	}
	var got []string
	for _, h := range hits {
		got = append(got, h.Title)
	}
	t.Logf("semantic hits (Ollama+Qdrant): %v", got)

	// The greeting note must surface, and (semantically) rank first.
	if got[0] != "greeting note" {
		t.Errorf("expected 'greeting note' ranked first via real embeddings, got %v", got)
	}
}
