package service

import (
	"context"
	"testing"
	"time"

	"repotory/internal/db"
	"repotory/internal/domain"
	"repotory/internal/search/temporal"
)

// ADR-0072: every search hit carries the channels that surfaced it (Recall)
// and note hits carry the derived source bucket (SourceType). These tests pin
// the provenance on the real service paths, not on the fusion helpers, so a
// refactor cannot silently drop the annotation the frontend badges rely on.

func hitByTitle(hits []domain.SearchHit, title string) (domain.SearchHit, bool) {
	for _, h := range hits {
		if h.Title == title {
			return h, true
		}
	}
	return domain.SearchHit{}, false
}

func recallEquals(h domain.SearchHit, want ...string) bool {
	if len(h.Recall) != len(want) {
		return false
	}
	for i, c := range want {
		if h.Recall[i] != c {
			return false
		}
	}
	return true
}

func TestSearchRecallLexicalOnly(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "recallproj", "/tmp/recallproj")
	if _, err := db.CreateNoteEx(svc.db, pid, "recallnote", "zebra body", "", "knowledge", "manual"); err != nil {
		t.Fatal(err)
	}
	hits := svc.mustSearchNotes("zebra")
	h, ok := hitByTitle(hits, "recallnote")
	if !ok {
		t.Fatal("lexical hit missing")
	}
	if !recallEquals(h, "lexical") {
		t.Errorf("Recall = %v, want [lexical]", h.Recall)
	}
	if h.SourceType != "manual" {
		t.Errorf("SourceType = %q, want manual", h.SourceType)
	}
}

func TestSearchRecallSemanticChannel(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "semrecall", "/tmp/semrecall")
	for _, title := range []string{"alpha", "beta"} {
		if _, err := db.CreateNoteEx(svc.db, pid, title, "", "", "other", "manual"); err != nil {
			t.Fatal(err)
		}
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
			t.Fatalf("config %s: %v", k, v)
		}
	}
	if _, err := svc.RebuildEmbeddings(); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	// Lexically empty query: whatever comes back was surfaced by the vector
	// arm alone, so its provenance must be exactly [semantic].
	hits := svc.mustSearchAll("totally unrelated zzz")
	if len(hits) == 0 {
		t.Fatal("expected semantic recall, got 0 hits")
	}
	for _, h := range hits {
		if !recallEquals(h, "semantic") {
			t.Errorf("hit %q Recall = %v, want [semantic]", h.Title, h.Recall)
		}
		if h.SourceType != "manual" {
			t.Errorf("hit %q SourceType = %q, want manual", h.Title, h.SourceType)
		}
	}

	// A lexically matching query keeps lexical FIRST in the provenance even
	// when the vector arm also recalls the same note.
	hits = svc.mustSearchNotes("alpha")
	h, ok := hitByTitle(hits, "alpha")
	if !ok {
		t.Fatal("lexical hit alpha missing")
	}
	if len(h.Recall) == 0 || h.Recall[0] != "lexical" {
		t.Errorf("Recall = %v, want lexical first", h.Recall)
	}
}

func TestSearchRecallTemporalChannel(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "temprecall", "/tmp/temprecall")
	if _, err := db.CreateNoteEx(svc.db, pid, "tempnote", "quokka", "", "log", "manual"); err != nil {
		t.Fatal(err)
	}
	ctx := temporal.WithAnchor(context.Background(), time.Now())
	hits, err := svc.SearchNotes(ctx, "今天记了什么")
	if err != nil {
		t.Fatal(err)
	}
	h, ok := hitByTitle(hits, "tempnote")
	if !ok {
		t.Fatal("temporal hit missing")
	}
	if !recallEquals(h, "temporal") {
		t.Errorf("Recall = %v, want [temporal] (no lexical match for the query)", h.Recall)
	}
}

func TestSearchRecallTodoStaysLexical(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "todoproj", "/tmp/todoproj")
	if _, err := db.CreateNoteEx(svc.db, pid, "sharedword", "zebra body", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateTodo(svc.db, pid, "zebra todo"); err != nil {
		t.Fatal(err)
	}
	hits := svc.mustSearchAll("zebra")
	var sawTodo bool
	for _, h := range hits {
		if h.Type != "todo" {
			continue
		}
		sawTodo = true
		if !recallEquals(h, "lexical") {
			t.Errorf("todo Recall = %v, want [lexical]", h.Recall)
		}
		if h.SourceType != "" {
			t.Errorf("todo SourceType = %q, want empty", h.SourceType)
		}
	}
	if !sawTodo {
		t.Fatal("todo hit missing")
	}
}