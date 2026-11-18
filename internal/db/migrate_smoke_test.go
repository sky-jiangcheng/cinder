package db

import (
	"context"
	"path/filepath"
	"testing"
)

// Smoke test: the v7 migration must apply through InitDB on a real file DB
// (WAL + foreign keys), creating the FTS tables/triggers and backfilling
// existing rows so search works end-to-end.
func TestInitDB_FTSMigrationSmoke(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db1, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB (fresh): %v", err)
	}
	// Insert a project + note, then confirm the FTS index (created by the v7
	// migration) serves search end-to-end.
	res, err := db1.Exec("INSERT INTO projects (name, root_path) VALUES (?, ?)", "smoke", "/tmp/smoke")
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	projectID, _ := res.LastInsertId()
	if _, err := CreateNoteEx(db1, projectID, "登录模块", "修复登录模块的若干Bug", "", "other", "manual"); err != nil {
		t.Fatalf("CreateNoteEx: %v", err)
	}
	// Search should work via the FTS index created by the migration.
	hits, err := SearchNotes(context.Background(), db1, "登录模块")
	if err != nil {
		t.Fatalf("SearchNotes after fresh init: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	db1.Close()

	// Re-open the same DB: upgradeSchema must re-run idempotently (v7 already
	// applied) without error, and the index must remain consistent.
	db2, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB (reopen): %v", err)
	}
	defer db2.Close()
	hits, err = SearchNotes(context.Background(), db2, "登录模块")
	if err != nil {
		t.Fatalf("SearchNotes after reopen: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("expected 1 hit after reopen, got %d", len(hits))
	}

	// Backfill must be idempotent: force-set schema_version below v7 and
	// re-run upgrade so the backfill re-executes; no duplicate FTS rows.
	if _, err := db2.Exec("INSERT OR REPLACE INTO app_config (key, value) VALUES (?, '6')", migrationVersionKey); err != nil {
		t.Fatalf("reset schema version: %v", err)
	}
	if err := upgradeSchema(db2); err != nil {
		t.Fatalf("upgradeSchema re-run: %v", err)
	}
	hits, err = SearchNotes(context.Background(), db2, "登录模块")
	if err != nil {
		t.Fatalf("SearchNotes after backfill re-run: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("backfill should be idempotent; expected 1 hit, got %d (duplicate FTS rows?)", len(hits))
	}
}

func TestReadSchemaVersion_InvalidValue(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	if _, err := db.Exec("INSERT OR REPLACE INTO app_config (key, value) VALUES (?, ?)", migrationVersionKey, "not-a-number"); err != nil {
		t.Fatal(err)
	}
	if _, err := readSchemaVersion(db); err == nil {
		t.Fatalf("expected error for invalid schema version, got nil")
	}
}

// A multi-statement migration must be atomic: a statement that hard-fails
// rolls back the statements before it, instead of leaving a half-applied
// batch that the version stamp (never written on failure) would retry
// against an inconsistent schema.
func TestMigrationApplyRollsBackBatchOnFailure(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	m := migration{id: 999, sql: []string{
		"CREATE TABLE mig_tx_probe (id INTEGER PRIMARY KEY)",
		"INSERT INTO no_such_table_for_migration_test VALUES (1)",
	}}
	if err := m.apply(database); err == nil {
		t.Fatal("apply should fail on the nonexistent table")
	}
	var n int
	if err := database.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE name = 'mig_tx_probe'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("statement before the failure should have been rolled back")
	}
}

// PRAGMA statements run outside the batch transaction (foreign_keys cannot
// change inside one), so a migration mixing PRAGMAs and DDL still applies.
func TestMigrationApplyWithPragmas(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	m := migration{id: 998, sql: []string{
		"PRAGMA foreign_keys = OFF",
		"CREATE TABLE mig_pragma_probe (id INTEGER PRIMARY KEY)",
		"PRAGMA foreign_keys = ON",
	}}
	if err := m.apply(database); err != nil {
		t.Fatalf("apply with PRAGMAs: %v", err)
	}
	var n int
	if err := database.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE name = 'mig_pragma_probe'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("probe table should exist after the migration")
	}
}
