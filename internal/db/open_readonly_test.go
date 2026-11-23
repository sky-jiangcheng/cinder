package db

import (
	"os"
	"path/filepath"
	"testing"
)

// mode=ro must be enforced by SQLite, not by convention: the session-brief
// path runs on every session start against the live database, and a write
// there would contend with the writer role (ADR-0058) or migrate the file.
func TestOpenReadOnlyRejectsWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ro-test.db")
	d, err := InitDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("INSERT INTO projects(name, root_path) VALUES ('x', '/x')"); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer ro.Close()
	var n int
	if err := ro.QueryRow("SELECT COUNT(*) FROM projects").Scan(&n); err != nil {
		t.Fatalf("read failed: %v", err)
	}
	// InitDB seeds the default project, so the inserted row is the second.
	if n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
	if _, err := ro.Exec("INSERT INTO projects(name, root_path) VALUES ('y', '/y')"); err == nil {
		t.Fatal("a write through the read-only handle succeeded; mode=ro is not doing its job")
	}
}

// A missing database is an error, not an invitation to create one: the
// SessionStart hook on a fresh machine must stay a silent no-op.
func TestOpenReadOnlyMissingFileDoesNotCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	if _, err := OpenReadOnly(path); err == nil {
		t.Fatal("OpenReadOnly accepted a missing file")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("OpenReadOnly created the file (stat err = %v)", err)
	}
}
