package db

import (
	"testing"
)

// apply must record its own version, inside the same transaction as its last
// DDL statement. The version used to be stamped by upgradeSchema AFTER apply
// returned; this pins the ownership so it cannot quietly move back out.
func TestMigrationApplyStampsItsOwnVersion(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	m := migration{id: 996, sql: []string{
		"CREATE TABLE mig_stamp_probe (id INTEGER PRIMARY KEY)",
	}}
	if err := m.apply(database); err != nil {
		t.Fatalf("apply: %v", err)
	}
	v, err := readSchemaVersion(database)
	if err != nil {
		t.Fatalf("readSchemaVersion: %v", err)
	}
	if v != 996 {
		t.Fatalf("apply should stamp its own version; schema_version = %d", v)
	}
}

// The point of stamping inside the transaction: a migration that changes the
// schema but cannot record the version must leave NO trace, not a migrated
// database claiming to be older than it is. Dropping app_config makes the stamp
// itself fail, which is the only way to observe the atomicity from outside.
func TestMigrationApplyRollsBackDDLWhenStampFails(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	before, err := readSchemaVersion(database)
	if err != nil {
		t.Fatalf("readSchemaVersion: %v", err)
	}

	m := migration{id: 997, sql: []string{"DROP TABLE app_config"}}
	if err := m.apply(database); err == nil {
		t.Fatal("apply should fail: the batch dropped the table the stamp writes to")
	}
	// If the DROP had been committed before the stamp was attempted, the table
	// would be gone here and every later InitDB would be broken.
	var n int
	if err := database.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'app_config'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("app_config should have been restored: the DDL and the stamp must commit together")
	}
	after, err := readSchemaVersion(database)
	if err != nil {
		t.Fatalf("readSchemaVersion: %v", err)
	}
	if after != before {
		t.Fatalf("a failed migration must not change the stamp; got %d, want %d", after, before)
	}
}

// The remaining window: a fn migration opens its own transactions, so a crash
// between it and its stamp leaves the schema at v31 with the stamp still at v30.
// The next open re-runs it — every Ensure* is idempotent — and re-stamps. This
// test simulates exactly that state by rewinding only the stamp.
func TestUpgradeSchemaRestampsAnUnstampedDatabase(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	want, err := readSchemaVersion(database)
	if err != nil {
		t.Fatalf("readSchemaVersion: %v", err)
	}
	if want < 31 {
		t.Fatalf("fixture should be at v31+, got %d", want)
	}
	if _, err := database.Exec(
		"INSERT OR REPLACE INTO app_config (key, value) VALUES (?, ?)",
		migrationVersionKey, "30",
	); err != nil {
		t.Fatalf("rewind schema version: %v", err)
	}
	if err := upgradeSchema(database); err != nil {
		t.Fatalf("upgradeSchema on an applied-but-unstamped database: %v", err)
	}
	got, err := readSchemaVersion(database)
	if err != nil {
		t.Fatalf("readSchemaVersion: %v", err)
	}
	if got != want {
		t.Fatalf("schema_version = %d, want %d", got, want)
	}
	// The re-run must not have disturbed the v31 contract it just re-applied.
	var n int
	if err := database.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = 'note_versions_snap_upd'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("the v31 trigger should still exist after the re-run")
	}
}
