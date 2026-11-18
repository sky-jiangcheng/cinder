package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// Regression test for the v12 migration.
//
// The damage being reproduced: migration v7 backfilled the FTS5 indexes with
//
//	INSERT INTO project_notes_fts(rowid, title, content)
//	SELECT ... FROM project_notes WHERE id NOT IN (SELECT rowid FROM project_notes_fts)
//
// which never matched a row. project_notes_fts is an external-content table,
// so the subquery is answered by project_notes itself, making the predicate
// "id NOT IN project_notes" — always false. Any database that had notes when
// v7 ran ended up with an empty index and no way to repair it, because
// SearchNotes only falls back to LIKE when the FTS query *errors*: a query
// that matches nothing returns no error and no results, so search silently
// reported a subset of the notes.
//
// TestInitDB_FTSMigrationSmoke re-runs v7's backfill but never caught this,
// because the index was already populated in that scenario. This test starts
// from the damaged state instead.
func TestUpgradeSchema_V12RepairsDriftedFTSIndex(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "drift.db")

	database, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer database.Close()

	res, err := database.Exec("INSERT INTO projects (name, root_path) VALUES (?, ?)", "drift", "/tmp/drift")
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	projectID, _ := res.LastInsertId()
	for _, note := range []struct{ title, body string }{
		{"登录模块", "修复登录模块的若干Bug"},
		{"缓存穿透", "缓存穿透与 trigram 分词"},
	} {
		if _, err := CreateNoteEx(database, projectID, note.title, note.body, "", "other", "manual"); err != nil {
			t.Fatalf("CreateNoteEx %q: %v", note.title, err)
		}
	}

	// Reproduce the damage: wipe the index while leaving the content table
	// intact. This is exactly the state v7's broken backfill produced.
	// The external-content delete command is the supported way to do it.
	if _, err := database.Exec("INSERT INTO project_notes_fts(project_notes_fts) VALUES('delete-all')"); err != nil {
		t.Fatalf("clear FTS index: %v", err)
	}
	assertIndexedDocs(t, database, 0, "index should start empty after the simulated v7 damage")

	// Search is now silently incomplete rather than broken: no error, fewer
	// results. This is the symptom users would report.
	hits, err := SearchNotes(context.Background(), database, "登录模块")
	if err != nil {
		t.Fatalf("expected a silent under-report, not an error; got %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("precondition failed: expected 0 hits on the damaged index, got %d", len(hits))
	}

	// Roll the version back to just before the repair and upgrade for real.
	if _, err := database.Exec("INSERT OR REPLACE INTO app_config (key, value) VALUES (?, '11')", migrationVersionKey); err != nil {
		t.Fatalf("reset schema version: %v", err)
	}
	if err := upgradeSchema(database); err != nil {
		t.Fatalf("upgradeSchema: %v", err)
	}

	version, err := readSchemaVersion(database)
	if err != nil {
		t.Fatalf("readSchemaVersion: %v", err)
	}
	// 12 is where the FTS repair lives; later migrations (v13...) keep
	// stacking on top, so assert "at least repaired", not a frozen number.
	if version < 12 {
		t.Fatalf("expected schema version >= 12 after upgrade, got %d", version)
	}

	assertIndexedDocs(t, database, 2, "v12 rebuild should have reindexed every note")

	// The repair has to be observable through the public search path, not just
	// the docsize shadow table - including for CJK, which is what the trigram
	// tokenizer exists for.
	for _, query := range []string{"登录模块", "缓存穿透", "trigram"} {
		hits, err := SearchNotes(context.Background(), database, query)
		if err != nil {
			t.Fatalf("SearchNotes(context.Background(), %q) after repair: %v", query, err)
		}
		if len(hits) == 0 {
			t.Errorf("query %q still returns nothing after the v12 rebuild", query)
		}
	}

	// Rebuilding an already-correct index must not duplicate rows, or a
	// database opened repeatedly would inflate its index on every launch.
	if err := upgradeSchema(database); err != nil {
		t.Fatalf("second upgradeSchema: %v", err)
	}
	assertIndexedDocs(t, database, 2, "rebuild must be idempotent across repeated opens")
}

// TestUpgradeSchema_V12RepairsTodoIndex covers the same drift on the todo side,
// which v7 damaged through an identical statement.
func TestUpgradeSchema_V12RepairsTodoIndexDrift(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	if _, err := database.Exec("INSERT INTO projects (name, root_path) VALUES (?, ?)", "todo", "/tmp/todo"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("INSERT INTO project_todos (project_id, title) VALUES (1, '重构搜索层')"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("INSERT INTO project_todos_fts(project_todos_fts) VALUES('delete-all')"); err != nil {
		t.Fatalf("clear todo FTS index: %v", err)
	}
	if _, err := database.Exec("INSERT OR REPLACE INTO app_config (key, value) VALUES (?, '11')", migrationVersionKey); err != nil {
		t.Fatal(err)
	}
	if err := upgradeSchema(database); err != nil {
		t.Fatalf("upgradeSchema: %v", err)
	}

	var n int
	if err := database.QueryRow("SELECT COUNT(*) FROM project_todos_fts_docsize").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected the todo index rebuilt to 1 document, got %d", n)
	}
}

// assertIndexedDocs reads the FTS5 docsize shadow table, which holds one row
// per document actually present in the index. It is the only place the real
// index size is visible: `SELECT COUNT(*) FROM project_notes_fts` on an
// external-content table is answered by the content table and would always
// agree with it, hiding the drift this whole migration exists to fix.
func assertIndexedDocs(t *testing.T, database *sql.DB, want int, msg string) {
	t.Helper()
	var n int
	if err := database.QueryRow("SELECT COUNT(*) FROM project_notes_fts_docsize").Scan(&n); err != nil {
		t.Fatalf("read docsize: %v", err)
	}
	if n != want {
		t.Fatalf("%s: want %d indexed document(s), got %d", msg, want, n)
	}
}
