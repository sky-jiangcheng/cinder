package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"repotory/internal/db"
	"repotory/internal/version"
)

// The three original fields are frozen: the updater compares "version".
func TestHealthKeepsVersionFields(t *testing.T) {
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	h := New(database, "tester").Health()
	if h["status"] != "ok" {
		t.Errorf("status = %v, want ok", h["status"])
	}
	if h["version"] != version.Version {
		t.Errorf("version changed: %v", h["version"])
	}
	if h["core_version"] != version.CoreVersion {
		t.Errorf("core_version changed: %v", h["core_version"])
	}
}

func TestHealthRuntimeMetricsAreReal(t *testing.T) {
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	h := New(database, "tester").Health()

	if v, ok := h["db_size_bytes"].(int64); !ok || v <= 0 {
		t.Errorf("db_size_bytes = %v (%T), want a positive int64 from PRAGMA", h["db_size_bytes"], h["db_size_bytes"])
	}
	if _, ok := h["compile_jobs_failed_24h"].(int64); !ok {
		t.Errorf("compile_jobs_failed_24h missing/wrong type: %T", h["compile_jobs_failed_24h"])
	}
	v, present := h["last_backup_at"]
	if !present {
		t.Error("last_backup_at key missing")
	} else if v != nil {
		// :memory: has no live DB file, so no snapshot directory is readable.
		t.Errorf("in-memory db should report nil last_backup_at, got %v", v)
	}
}

func TestHealthCountsRecentFailedCompileJobs(t *testing.T) {
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	s := New(database, "tester")
	if got := s.Health()["compile_jobs_failed_24h"]; got != int64(0) {
		t.Fatalf("fresh db: got %v (%T), want 0", got, got)
	}

	// compile_jobs.project_id is a FK with foreign_keys=ON, so seed a project.
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := db.SyncProjectTx(tx, "p", "/tmp/p", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	insert := func(status string, when time.Time) {
		t.Helper()
		if _, err := database.Exec(
			`INSERT INTO compile_jobs (project_id, requested_notes, status, created_at) VALUES (?, 1, ?, ?)`,
			pid, status, when.UTC().Format("2006-01-02 15:04:05")); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	insert("failed", now.Add(-1*time.Hour))
	insert("failed", now.Add(-2*time.Hour))
	insert("failed", now.Add(-48*time.Hour)) // outside the 24h window
	insert("succeeded", now.Add(-1*time.Hour))

	if got := s.Health()["compile_jobs_failed_24h"]; got != int64(2) {
		t.Errorf("compile_jobs_failed_24h = %v, want 2", got)
	}
}

func TestHealthLastBackupAtReadsSnapshots(t *testing.T) {
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	s := New(database, "tester")
	dir := t.TempDir()
	s.backupDir = dir // keep the read off the real user data directory

	if got := s.Health()["last_backup_at"]; got != nil {
		t.Errorf("empty backup dir should report nil, got %v", got)
	}

	snapshot := filepath.Join(dir, db.BackupSnapshotPrefix+"2026-11-12.db")
	if err := os.WriteFile(snapshot, []byte("snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 11, 12, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(snapshot, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	// A newer unrelated file must not be mistaken for a snapshot.
	noise := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(noise, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	later := stamp.Add(48 * time.Hour)
	if err := os.Chtimes(noise, later, later); err != nil {
		t.Fatal(err)
	}

	got, ok := s.Health()["last_backup_at"].(string)
	if !ok {
		t.Fatalf("last_backup_at is not a string: %T", s.Health()["last_backup_at"])
	}
	parsed, err := time.Parse(time.RFC3339, got)
	if err != nil {
		t.Fatalf("last_backup_at is not RFC3339: %q", got)
	}
	if !parsed.Equal(stamp) {
		t.Errorf("last_backup_at = %v, want the snapshot mtime %v (noise must be ignored)", parsed, stamp)
	}
}
