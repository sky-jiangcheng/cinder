package service

import (
	"testing"

	"repotory/internal/db"
	"repotory/internal/domain"
)

// ADR-0055 D2: SearchAll / SearchNotes now return an error. These helpers keep
// the old single-value shape for assertion-heavy retrieval tests, but a storage
// fault fails loudly instead of being read as "zero hits".
func (s *Service) mustSearchAll(q string) []domain.SearchHit {
	hits, err := s.SearchAll(q)
	if err != nil {
		panic(err)
	}
	return hits
}

func (s *Service) mustSearchNotes(q string) []domain.SearchHit {
	hits, err := s.SearchNotes(q)
	if err != nil {
		panic(err)
	}
	return hits
}

// TestSearchAllAppliesSemanticRecall pins the fix for the "evaluation gate
// measures a path the product does not use" defect.
//
// SearchAll is what every real user-facing search goes through: the five
// frontend entry points, the MCP repotory_notes_search / repotory_ask tools,
// and the HTTP /api/search?all=1 endpoint. It used to skip fuseSemantic
// entirely, so the ADR-0012 / ADR-0018 gains measured by cmd/abeval (which
// drives svc.SearchNotes) never reached a single real query.
//
// The regression is pinned on SearchAll itself rather than on a proxy, so a
// future refactor cannot re-open the gap by moving the fusion around.
func TestSearchAllAppliesSemanticRecall(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "allproj", "/tmp/allproj")
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
			t.Fatalf("config %s: %v", k, v)
		}
	}
	// Lexically this matches nothing, so a lexical-only SearchAll returns
	// zero hits — which is exactly what it did before the fix. The check runs
	// BEFORE the rebuild because the vector index does not exist yet: with
	// semantic enabled but no index, the fusion is a graceful no-op.
	query := "totally unrelated zzz"
	if hits := svc.mustSearchAll(query); len(hits) != 0 {
		t.Fatalf("before fusion, expected lexical-only empty, got %d", len(hits))
	}

	if n, err := svc.RebuildEmbeddings(); err != nil {
		t.Fatalf("rebuild: %v", err)
	} else if n != 2 {
		t.Fatalf("embedded %d, want 2", n)
	}

	hits := svc.mustSearchAll(query)
	if len(hits) == 0 {
		t.Fatal("expected semantic recall of alpha through SearchAll, got 0 hits")
	}
	if hits[0].Title != "alpha" {
		t.Errorf("top hit = %q, want alpha", hits[0].Title)
	}

	// Turning semantic off restores pure-lexical behaviour.
	if err := svc.UpdateConfig("semantic_search", "0"); err != nil {
		t.Fatal(err)
	}
	if hits := svc.mustSearchAll(query); len(hits) != 0 {
		t.Errorf("with semantic off, expected 0 hits, got %d", len(hits))
	}
}

// TestSearchAllKeepsNoteAndTodoIDSpacesApart pins the reason the fusion in
// SearchAll runs on the notes subset only.
//
// Note IDs come from project_notes and todo IDs from project_todos — both
// AUTOINCREMENT, so id 1 exists in both tables and means different rows. The
// fusion's byID map is keyed by a bare int64, so fusing the mixed list would
// let a todo and a note collide and overwrite each other. The test creates
// BOTH a note and a todo with the SAME id and asserts both survive a
// semantic-enabled SearchAll.
func TestSearchAllKeepsNoteAndTodoIDSpacesApart(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "idproj", "/tmp/idproj")

	// One note, one todo — both will land on id 1 in their own tables. The
	// todo title carries the same body term so that BOTH are lexically
	// matched: the point is that both are in the base list and both must
	// survive the fusion, not that one was never a candidate.
	note, err := db.CreateNoteEx(svc.db, pid, "sharedid", "", "zebra body", "other", "manual")
	if err != nil {
		t.Fatal(err)
	}
	todo, err := db.CreateTodo(svc.db, pid, "zebra sharedid todo")
	if err != nil {
		t.Fatal(err)
	}
	if note.ID != todo.ID {
		t.Fatalf("test premise broken: note id %d, todo id %d", note.ID, todo.ID)
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

	// "zebra" matches the note body lexically and the todo title lexically,
	// so both are in the base list and both must survive the fusion.
	hits := svc.mustSearchAll("zebra")
	var sawNote, sawTodo bool
	for _, h := range hits {
		switch h.Type {
		case "note":
			if h.Title == "sharedid" {
				sawNote = true
			}
		case "todo":
			if h.Title == "zebra sharedid todo" {
				sawTodo = true
			}
		}
	}
	if !sawNote {
		t.Error("note hit was lost — the todo overwrote it in the fused ID map")
	}
	if !sawTodo {
		t.Error("todo hit was lost — the note overwrote it in the fused ID map")
	}
}
