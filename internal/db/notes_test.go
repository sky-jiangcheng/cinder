package db

import (
	"strings"
	"testing"
)

// The bounds are enforced at the DB layer because this package is the
// chokepoint every writer converges on — the service layer (desktop UI, MCP
// tools) AND the plugin runtime's import upserts, which call these functions
// directly. These tests prove the second path is bounded too.
func TestNoteWriteBoundsAtDBLayer(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	pid := createTestProject(t, db, "proj-a")

	// Sanity: normal-size writes must succeed, or the bounds are too tight.
	note, err := CreateNoteEx(db, pid, "title", "body", "tag", "other", "manual")
	if err != nil {
		t.Fatalf("CreateNoteEx: %v", err)
	}

	cases := []struct {
		name string
		fn   func() error
	}{
		{"CreateNoteEx oversized content", func() error {
			_, err := CreateNoteEx(db, pid, "t", strings.Repeat("x", MaxNoteContentLen+1), "", "knowledge", "manual")
			return err
		}},
		{"CreateNoteEx oversized title", func() error {
			_, err := CreateNoteEx(db, pid, strings.Repeat("t", MaxNoteTitleLen+1), "body", "", "knowledge", "manual")
			return err
		}},
		{"CreateNoteEx too many tags", func() error {
			_, err := CreateNoteEx(db, pid, "t", "body", strings.Repeat("a,", MaxNoteTagCount+1), "knowledge", "manual")
			return err
		}},
		{"UpdateNote oversized content", func() error {
			return UpdateNote(db, note.ID, strings.Repeat("x", MaxNoteContentLen+1))
		}},
		{"UpdateNoteFull oversized title", func() error {
			return UpdateNoteFull(db, note.ID, "body", strings.Repeat("t", MaxNoteTitleLen+1), "tag", "knowledge", false, 0)
		}},
		{"UpdateNoteMeta oversized title", func() error {
			return UpdateNoteMeta(db, note.ID, strings.Repeat("t", MaxNoteTitleLen+1), "tag", "knowledge", false)
		}},
		{"UpdateNoteMeta too many tags", func() error {
			return UpdateNoteMeta(db, note.ID, "title", strings.Repeat("a,", MaxNoteTagCount+1), "knowledge", false)
		}},
	}
	for _, c := range cases {
		if err := c.fn(); err == nil {
			t.Errorf("%s: expected rejection", c.name)
		}
	}

	// The note must be untouched by the rejected writes.
	got, err := GetNoteByID(db, note.ID)
	if err != nil {
		t.Fatalf("GetNoteByID: %v", err)
	}
	if got.Title != "title" || got.Content != "body" || got.Tags != "tag" {
		t.Errorf("note mutated by rejected writes: %+v", got)
	}
}

// Deleting a note must not leave its embedding behind: orphaned vectors
// consume KNN's k budget and are invisible to the inner-join materialisation,
// so semantic recall degrades silently until a full rebuild.
// TestListRecentHandoffNotes pins the memory-ring query (D25): the handoff
// tag predicate, newest-first ordering (updated_at, then id as the tiebreak
// for same-second writes), the limit, the project scope, and the join that
// carries the project name. Decoys with lookalike tags ("handoff-ish") must
// never match.
func TestListRecentHandoffNotes(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()
	pidA := createTestProject(t, database, "proj-a")
	pidB := createTestProject(t, database, "proj-b")

	mk := func(pid int64, title, tags string) int64 {
		t.Helper()
		n, err := CreateNoteEx(database, pid, title, "body of "+title, tags, "knowledge", "mcp")
		if err != nil {
			t.Fatalf("CreateNoteEx %s: %v", title, err)
		}
		return n.ID
	}
	mk(pidA, "handoff one", "handoff")
	mk(pidA, "handoff auto", "handoff, auto-captured")
	mk(pidB, "handoff two", "handoff, extra")
	mk(pidA, "plain note", "knowledge")
	mk(pidA, "handoff-ish decoy", "handoff-ish")

	all, err := ListRecentHandoffNotes(database, 0, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 handoff notes, got %d: %+v", len(all), all)
	}
	// Same-second writes order by id DESC: the newest-created handoff first.
	if all[0].Title != "handoff two" || all[0].ProjectID != pidB {
		t.Fatalf("newest-first violated: %+v", all)
	}
	for _, n := range all {
		if n.ProjectName == "" {
			t.Errorf("project name not joined: %+v", n)
		}
	}

	scoped, err := ListRecentHandoffNotes(database, pidB, 0)
	if err != nil {
		t.Fatalf("scoped list: %v", err)
	}
	if len(scoped) != 1 || scoped[0].Title != "handoff two" {
		t.Fatalf("project scope broken: %+v", scoped)
	}

	capped, err := ListRecentHandoffNotes(database, 0, 2)
	if err != nil {
		t.Fatalf("capped list: %v", err)
	}
	if len(capped) != 2 {
		t.Fatalf("limit ignored: got %d", len(capped))
	}
}

func TestDeleteNoteRemovesEmbedding(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	pid := createTestProject(t, database, "vec")
	note, err := CreateNote(database, pid, "to be deleted")
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureVectorIndex(database, 2); err != nil {
		t.Fatal(err)
	}
	if err := PutNoteEmbedding(database, note.ID, []float32{1, 0}); err != nil {
		t.Fatal(err)
	}

	if err := DeleteNote(database, note.ID); err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}
	ids, err := KnnNoteIDs(database, []float32{1, 0}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("embedding for deleted note %d survived: %v", note.ID, ids)
	}

	// Cascade path: prune removes vectors for notes deleted outside DeleteNote.
	other, err := CreateNote(database, pid, "cascade deleted")
	if err != nil {
		t.Fatal(err)
	}
	if err := PutNoteEmbedding(database, other.ID, []float32{0, 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("DELETE FROM project_notes WHERE id = ?", other.ID); err != nil {
		t.Fatal(err)
	}
	n, err := PruneNoteEmbeddings(database)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("prune should remove 1 orphan, removed %d", n)
	}
	ids, _ = KnnNoteIDs(database, []float32{0, 1}, 5)
	if len(ids) != 0 {
		t.Fatalf("orphaned embedding survived prune: %v", ids)
	}
}
