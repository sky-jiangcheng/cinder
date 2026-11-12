package db

import (
	"database/sql"
	"testing"
)

// The whole incremental-embedding design rests on one claim: internal/db is where
// every note writer converges, so trigger-maintained dirtying also covers the
// plugin runtime's direct upserts (the five agent-memory importers never call
// service.*Note). These tests write through db on purpose — that IS the importer
// path. Same rationale as TestNoteWriteBoundsAtDBLayer.
func TestNoteTriggersMarkDirtyOnContentWrites(t *testing.T) {
	db := setupTestDB(t)
	pid := createTestProject(t, db, "trig")

	note, err := CreateNoteEx(db, pid, "title a", "body a", "t1", "knowledge", "manual")
	if err != nil {
		t.Fatal(err)
	}
	assertDirty(t, db, note.ID, true, "after INSERT")

	if err := UpdateNote(db, note.ID, "body b"); err != nil {
		t.Fatal(err)
	}
	assertDirty(t, db, note.ID, true, "after content UPDATE")

	if err := DeleteNote(db, note.ID); err != nil {
		t.Fatal(err)
	}
	assertDirty(t, db, note.ID, true, "after DELETE (its vector still has to be removed)")
}

// Pinning, reordering, retagging or moving a note does not change the text handed
// to the embedding endpoint, so none of it may cost an embedding request. Without
// the `AFTER UPDATE OF title, content` restriction, every pin click would queue a
// re-embed.
func TestNoteTriggersIgnoreMetadataOnlyWrites(t *testing.T) {
	db := setupTestDB(t)
	pid := createTestProject(t, db, "meta")

	note, err := CreateNoteEx(db, pid, "t", "c", "", "other", "manual")
	if err != nil {
		t.Fatal(err)
	}
	// Consume the INSERT's mark so only the metadata writes are under test.
	if err := TruncateDirtyNoteIDs(db); err != nil {
		t.Fatal(err)
	}

	if err := PinNote(db, note.ID, true); err != nil {
		t.Fatal(err)
	}
	assertDirty(t, db, note.ID, false, "after pin")

	if err := UpdateNoteMeta(db, note.ID, "t", "new,tags", "log", true); err != nil {
		t.Fatal(err)
	}
	assertDirty(t, db, note.ID, false, "after tags/kind-only update (title unchanged)")

	if err := MoveNote(db, note.ID, pid); err != nil {
		t.Fatal(err)
	}
	assertDirty(t, db, note.ID, false, "after move")

	// Title IS part of the embedding input, so a title change must dirty.
	if err := UpdateNoteFull(db, note.ID, "c", "renamed", "new,tags", "log", true, 0); err != nil {
		t.Fatal(err)
	}
	assertDirty(t, db, note.ID, true, "after a title change (title is in NoteEmbeddingText)")
}

// A delete must stay distinguishable from an edit: the queue holds ids, and the
// drainer decides "re-embed" vs "un-index" by whether the note still exists.
func TestNextDirtyEmbedWorkSplitsLiveAndGone(t *testing.T) {
	db := setupTestDB(t)
	pid := createTestProject(t, db, "split")

	live, err := CreateNoteEx(db, pid, "hello", "world", "", "other", "manual")
	if err != nil {
		t.Fatal(err)
	}
	doomed, err := CreateNoteEx(db, pid, "gone", "soon", "", "other", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := DeleteNote(db, doomed.ID); err != nil {
		t.Fatal(err)
	}

	work, err := NextDirtyEmbedWork(db, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(work.ToEmbed) != 1 || work.ToEmbed[0].ID != live.ID {
		t.Fatalf("ToEmbed = %+v, want exactly the live note %d", work.ToEmbed, live.ID)
	}
	// Built by the same function the full rebuild uses, or the two paths would
	// write vectors that mean different things and then get RRF-fused together.
	if want := NoteEmbeddingText("hello", "world"); work.ToEmbed[0].Text != want {
		t.Errorf("Text = %q, want %q (shared builder)", work.ToEmbed[0].Text, want)
	}
	if len(work.Gone) != 1 || work.Gone[0] != doomed.ID {
		t.Errorf("Gone = %v, want [%d]", work.Gone, doomed.ID)
	}

	// Reading the queue must not consume it: a failed embed has to be retried
	// rather than silently lost.
	again, err := NextDirtyEmbedWork(db, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.ToEmbed)+len(again.Gone) != 2 {
		t.Fatalf("queue shrank by being read: %+v", again)
	}

	var resolved []int64
	for _, w := range again.ToEmbed {
		resolved = append(resolved, w.ID)
	}
	resolved = append(resolved, again.Gone...)
	if err := ClearDirtyNoteIDs(db, resolved); err != nil {
		t.Fatal(err)
	}
	empty, err := NextDirtyEmbedWork(db, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.ToEmbed) != 0 || len(empty.Gone) != 0 {
		t.Fatalf("ClearDirtyNoteIDs left %+v", empty)
	}
}

// The cap is what stops a bulk import from being handed to the endpoint at once.
func TestNextDirtyEmbedWorkHonoursLimit(t *testing.T) {
	db := setupTestDB(t)
	pid := createTestProject(t, db, "cap")
	for i := 0; i < 5; i++ {
		if _, err := CreateNoteEx(db, pid, "t", "c", "", "other", "manual"); err != nil {
			t.Fatal(err)
		}
	}
	work, err := NextDirtyEmbedWork(db, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(work.ToEmbed) != 2 {
		t.Fatalf("limit 2 returned %d rows", len(work.ToEmbed))
	}
}

// v14 must be replay-safe: EnsureNoteEmbedDirty runs from the migration and the
// statements are IF NOT EXISTS, so an existing queue keeps its rows.
func TestEnsureNoteEmbedDirtyIsIdempotent(t *testing.T) {
	db := setupTestDB(t)
	pid := createTestProject(t, db, "idemp")
	note, err := CreateNoteEx(db, pid, "t", "c", "", "other", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureNoteEmbedDirty(db); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	assertDirty(t, db, note.ID, true, "after re-applying the DDL (queue must survive)")
}

func assertDirty(t *testing.T, db *sql.DB, id int64, want bool, when string) {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM note_embed_dirty WHERE note_id = ?", id).Scan(&n); err != nil {
		t.Fatalf("%s: query dirty queue: %v", when, err)
	}
	if got := n > 0; got != want {
		t.Errorf("%s: note %d dirty = %v, want %v", when, id, got, want)
	}
}
