package db

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"repotory/internal/errcode"
)

// schema v31 (ADR-0057) adds the columns the write paths reason about. Each one
// is asserted from the database, not from the Go type, because the point of the
// migration is that the guarantee holds for every process that opens the file.
func TestSchemaV31_NoteWriteContractColumns(t *testing.T) {
	db := setupTestDB(t)

	if !hasColumn(t, db, "project_notes", "idempotency_key") {
		t.Error("project_notes.idempotency_key is missing after migration v31")
	}
	if !hasColumn(t, db, "project_notes", "version") {
		t.Error("project_notes.version is missing after migration v31")
	}
	if !hasColumn(t, db, "note_versions", "version") {
		t.Error("note_versions.version is missing after migration v31")
	}

	var indexSQL string
	err := db.QueryRow(
		"SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'idx_project_notes_idempotency'").Scan(&indexSQL)
	if err != nil {
		t.Fatalf("idempotency index missing: %v", err)
	}
	// Partial, so the many writers that pass no key stay out of the index
	// entirely instead of all colliding on (project_id, '').
	if !strings.Contains(indexSQL, "WHERE idempotency_key <> ''") {
		t.Errorf("idempotency index must be partial, got:\n%s", indexSQL)
	}
}

// The v31 semantic flip: a snapshot is the state BEFORE the edit. Asserting it
// from the row (not from the trigger's SQL) is what makes a regression in
// either direction visible: new.* would give "body v2" here, no snapshot would
// give zero rows.
func TestNoteVersions_SnapshotHoldsPreEditState(t *testing.T) {
	db := setupTestDB(t)
	pid := createTestProject(t, db, "snapshot-pre-edit")
	note, err := CreateNoteEx(db, pid, "title", "body v1", "tag", "knowledge", "manual")
	if err != nil {
		t.Fatalf("CreateNoteEx: %v", err)
	}
	if note.Version != 1 {
		t.Fatalf("a new note starts at version 1, got %d", note.Version)
	}

	if err := UpdateNote(db, note.ID, "body v2"); err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}

	versions, err := ListNoteVersions(db, note.ID)
	if err != nil {
		t.Fatalf("ListNoteVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("one edit must leave exactly one snapshot, got %d", len(versions))
	}
	if versions[0].Content != "body v1" {
		t.Errorf("snapshot content = %q, want the pre-edit state %q", versions[0].Content, "body v1")
	}
	if versions[0].Version != 1 {
		t.Errorf("snapshot version = %d, want 1 (the version it is the state of)", versions[0].Version)
	}

	current, err := GetNoteByID(db, note.ID)
	if err != nil {
		t.Fatalf("GetNoteByID: %v", err)
	}
	if current.Version != 2 {
		t.Errorf("current version = %d, want 2 after one edit", current.Version)
	}

	// A second edit appends the state before THAT edit, so history is the
	// complete v1..vN-1 sequence rather than a duplicate of the live row.
	if err := UpdateNote(db, note.ID, "body v3"); err != nil {
		t.Fatalf("UpdateNote (second): %v", err)
	}
	versions, err = ListNoteVersions(db, note.ID)
	if err != nil {
		t.Fatalf("ListNoteVersions (second): %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("two edits must leave two snapshots, got %d", len(versions))
	}
	if versions[0].Content != "body v2" || versions[0].Version != 2 {
		t.Errorf("newest snapshot = %q v%d, want \"body v2\" v2", versions[0].Content, versions[0].Version)
	}
}

// A repeat write with the same server-generated key must collapse onto the
// existing note: that is what makes a SessionEnd-hook retry and the
// auto-capture path safe to run twice.
func TestCreateNoteIdempotent_CollapsesRepeat(t *testing.T) {
	db := setupTestDB(t)
	pid := createTestProject(t, db, "idem")

	first, deduped, err := CreateNoteIdempotent(db, pid, "Session Handoff", "body", "handoff", "knowledge", "mcp", "handoff:1:sess-abc")
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	if deduped {
		t.Fatal("the first write is not a duplicate")
	}

	second, deduped, err := CreateNoteIdempotent(db, pid, "Session Handoff", "body", "handoff", "knowledge", "mcp", "handoff:1:sess-abc")
	if err != nil {
		t.Fatalf("repeat write: %v", err)
	}
	if !deduped {
		t.Error("the repeat must be reported as deduped, not as a new note")
	}
	if second.ID != first.ID {
		t.Errorf("repeat returned note %d, want the existing %d", second.ID, first.ID)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM project_notes WHERE idempotency_key = ?", "handoff:1:sess-abc").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("repeat created %d rows, want 1", count)
	}

	// Same key, different project: two records, not a duplicate. A session id
	// is only unique inside the project it was captured for.
	other := createTestProject(t, db, "idem-other")
	if _, deduped, err := CreateNoteIdempotent(db, other, "Session Handoff", "body", "handoff", "knowledge", "mcp", "handoff:1:sess-abc"); err != nil {
		t.Fatalf("other project: %v", err)
	} else if deduped {
		t.Error("the same key in another project must create a note")
	}

	// No key: no deduplication, even with identical content. The desktop
	// editor deliberately writes the same text twice.
	if _, err := CreateNoteEx(db, pid, "dup", "same body", "", "knowledge", "manual"); err != nil {
		t.Fatalf("first keyless write: %v", err)
	}
	if _, deduped, err := CreateNoteIdempotent(db, pid, "dup", "same body", "", "knowledge", "manual", ""); err != nil {
		t.Fatalf("second keyless write: %v", err)
	} else if deduped {
		t.Error("a write without a key is never deduped")
	}
}

// The optimistic lock: a stale version is refused rather than silently
// overwriting, and the sentinel classifies as a non-retryable conflict.
func TestUpdateNoteFull_VersionConflict(t *testing.T) {
	db := setupTestDB(t)
	pid := createTestProject(t, db, "optimistic-lock")
	note, err := CreateNoteEx(db, pid, "t", "v1", "", "knowledge", "manual")
	if err != nil {
		t.Fatalf("CreateNoteEx: %v", err)
	}

	if err := UpdateNoteFull(db, note.ID, "v2", "t", "", "knowledge", false, note.Version); err != nil {
		t.Fatalf("write with the current version must succeed: %v", err)
	}

	// The editor still holds version 1.
	err = UpdateNoteFull(db, note.ID, "stale", "t", "", "knowledge", false, note.Version)
	if !errors.Is(err, errcode.ErrVersionConflict) {
		t.Fatalf("stale write error = %v, want ErrVersionConflict", err)
	}
	if code, retryable := errcode.Classify(err); code != errcode.Conflict || retryable {
		t.Errorf("classify = (%q, %v), want (%q, false)", code, retryable, errcode.Conflict)
	}
	current, err := GetNoteByID(db, note.ID)
	if err != nil {
		t.Fatalf("GetNoteByID: %v", err)
	}
	if current.Content != "v2" {
		t.Errorf("the refused write changed content to %q; a conflict must leave the row untouched", current.Content)
	}

	// 0 means "no check" and always wins — that is what importers pass.
	if err := UpdateNoteFull(db, note.ID, "forced", "t", "", "knowledge", false, 0); err != nil {
		t.Fatalf("unconditional write: %v", err)
	}

	// An absent note is not a conflict: there is nothing to re-read.
	if err := UpdateNoteFull(db, 999999, "x", "t", "", "knowledge", false, 1); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("absent note error = %v, want sql.ErrNoRows", err)
	}
}

// Restoring an old version is a NEW version, not a rewind: the counter moves
// forward so a client holding the newer version cannot look up to date.
func TestRestoreNoteVersion_BumpsVersion(t *testing.T) {
	db := setupTestDB(t)
	pid := createTestProject(t, db, "restore-version")
	note, err := CreateNoteEx(db, pid, "t", "v1", "", "knowledge", "manual")
	if err != nil {
		t.Fatalf("CreateNoteEx: %v", err)
	}
	if err := UpdateNote(db, note.ID, "v2"); err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	versions, err := ListNoteVersions(db, note.ID)
	if err != nil || len(versions) == 0 {
		t.Fatalf("ListNoteVersions: %v (%d rows)", err, len(versions))
	}

	if err := RestoreNoteVersion(db, note.ID, versions[0].ID); err != nil {
		t.Fatalf("RestoreNoteVersion: %v", err)
	}
	restored, err := GetNoteByID(db, note.ID)
	if err != nil {
		t.Fatalf("GetNoteByID: %v", err)
	}
	if restored.Content != "v1" {
		t.Errorf("restored content = %q, want v1", restored.Content)
	}
	if restored.Version != 3 {
		t.Errorf("version after restore = %d, want 3 (forward, not back to 1)", restored.Version)
	}
}

func hasColumn(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query("SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		t.Fatalf("pragma_table_info(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan column name: %v", err)
		}
		if name == column {
			return true
		}
	}
	return false
}
