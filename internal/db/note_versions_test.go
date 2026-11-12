package db

import (
	"database/sql"
	"strings"
	"testing"
)

// countVersions returns the number of snapshots stored for a note.
func countVersions(t *testing.T, db *sql.DB, noteID int64) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM note_versions WHERE note_id = ?", noteID).Scan(&n); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	return n
}

// The v11 migration narrows note_versions_snap so only updates that touch a
// snapshotted column (content/title/tags/kind) create a version. Structural
// updates — pinning, moving between projects — must be silent, otherwise every
// pin click writes a duplicate version with identical content.
func TestNoteVersionsSnap_IgnoresStructuralUpdates(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	p1 := createTestProject(t, db, "proj-a")
	p2 := createTestProject(t, db, "proj-b")
	note, err := CreateNoteEx(db, p1, "title", "body", "tag", "other", "manual")
	if err != nil {
		t.Fatalf("CreateNoteEx: %v", err)
	}
	if got := countVersions(t, db, note.ID); got != 0 {
		t.Fatalf("fresh note should have 0 versions, got %d", got)
	}

	// Structural updates: no snapshot expected.
	if err := PinNote(db, note.ID, true); err != nil {
		t.Fatalf("PinNote: %v", err)
	}
	if got := countVersions(t, db, note.ID); got != 0 {
		t.Errorf("pin must not snapshot, versions = %d", got)
	}
	if err := MoveNote(db, note.ID, p2); err != nil {
		t.Fatalf("MoveNote: %v", err)
	}
	if got := countVersions(t, db, note.ID); got != 0 {
		t.Errorf("move must not snapshot, versions = %d", got)
	}
	// Metadata-only update where title/tags/kind are unchanged (only pinned
	// differs): still no snapshot.
	if err := UpdateNoteMeta(db, note.ID, "title", "tag", "other", false); err != nil {
		t.Fatalf("UpdateNoteMeta: %v", err)
	}
	if got := countVersions(t, db, note.ID); got != 0 {
		t.Errorf("no-op metadata update must not snapshot, versions = %d", got)
	}

	// Content update: one snapshot.
	if err := UpdateNote(db, note.ID, "body v2"); err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	if got := countVersions(t, db, note.ID); got != 1 {
		t.Fatalf("content update should snapshot once, versions = %d", got)
	}

	// Title-only update: another snapshot.
	if err := UpdateNoteMeta(db, note.ID, "title v2", "tag", "other", false); err != nil {
		t.Fatalf("UpdateNoteMeta (title): %v", err)
	}
	if got := countVersions(t, db, note.ID); got != 2 {
		t.Fatalf("title update should snapshot once, versions = %d", got)
	}
}

// The narrowed trigger must survive a fresh init (v8 creates the old trigger,
// v11 narrows it, v31 replaces it with a writer that stores the PRE-update
// state) — assert the final SQL carries both properties.
func TestNoteVersionsSnap_TriggerHasWhenClause(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	var sql string
	err := db.QueryRow("SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = 'note_versions_snap_upd'").Scan(&sql)
	if err != nil {
		t.Fatalf("note_versions_snap_upd trigger not found: %v", err)
	}
	if !strings.Contains(sql, "WHEN") {
		t.Errorf("trigger should carry a WHEN clause, got:\n%s", sql)
	}
	if !strings.Contains(sql, "new.content IS NOT old.content") {
		t.Errorf("trigger should compare content, got:\n%s", sql)
	}
	// v31 flipped the snapshot from "the state after the edit" to "the state
	// before it". A trigger that still inserts new.* silently makes every
	// version list a duplicate of the current note.
	if !strings.Contains(sql, "old.id") || strings.Contains(sql, "new.id") {
		t.Errorf("trigger must snapshot old.*, got:\n%s", sql)
	}

	// The v8/v11 trigger wrote new.*; leaving it in place would double every
	// edit (once with the state before, once with the state after).
	var legacy int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = 'note_versions_snap'").Scan(&legacy); err != nil {
		t.Fatalf("count legacy trigger: %v", err)
	}
	if legacy != 0 {
		t.Errorf("the v11 trigger must be dropped by v31, found %d", legacy)
	}
}
