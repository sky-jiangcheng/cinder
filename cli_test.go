package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newSnapshotForCLI writes a readable one-table database to return as a
// snapshot: VerifySnapshot only asks "can SQLite open this and read the
// schema", so this is the smallest honest fixture.
func newSnapshotForCLI(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "repotory-2026-11-12.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	if _, err := database.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT); INSERT INTO t (v) VALUES ('kept')"); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}
	return path
}

// The CLI exists so a corruption can be recovered without the app: it restores
// to a path the user names, and says so.
func TestRestoreCommand_WritesVerifiedCopy(t *testing.T) {
	src := newSnapshotForCLI(t)
	dst := filepath.Join(t.TempDir(), "restored.db")
	var out, errOut bytes.Buffer

	code := restoreCommand([]string{"--from", src, "--to", dst}, &out, &errOut, filepath.Join(t.TempDir(), "live.db"))
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), dst) {
		t.Errorf("output does not name the target: %q", out.String())
	}
	restored, err := sql.Open("sqlite", dst)
	if err != nil {
		t.Fatalf("open restored file: %v", err)
	}
	defer restored.Close()
	var v string
	if err := restored.QueryRow("SELECT v FROM t WHERE id = 1").Scan(&v); err != nil || v != "kept" {
		t.Fatalf("restored row = %q (err=%v)", v, err)
	}
}

// Writing over the live database is the one thing this command must never do:
// the premise of a restore is that the live file is already suspect, so the
// replacement has to be a separate, deliberate act.
func TestRestoreCommand_RefusesLiveDatabase(t *testing.T) {
	src := newSnapshotForCLI(t)
	live := filepath.Join(t.TempDir(), "repotory.db")
	var out, errOut bytes.Buffer

	code := restoreCommand([]string{"--from", src, "--to", live}, &out, &errOut, live)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "refusing") {
		t.Errorf("the refusal must say why, got: %q", errOut.String())
	}
	if _, err := os.Stat(live); !os.IsNotExist(err) {
		t.Errorf("the live database was written: %v", err)
	}
}

func TestRestoreCommand_RequiresBothPaths(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := restoreCommand([]string{"--from", "x.db"}, &out, &errOut, "live.db"); code != 2 {
		t.Fatalf("missing --to: exit = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "usage:") {
		t.Errorf("usage not printed: %q", errOut.String())
	}
}

// A destination that already exists is refused by RestoreBackup, and the CLI
// surfaces that instead of overwriting somebody's file.
func TestRestoreCommand_DoesNotOverwriteExistingFile(t *testing.T) {
	src := newSnapshotForCLI(t)
	dst := filepath.Join(t.TempDir(), "already-there.db")
	if err := os.WriteFile(dst, []byte("user data"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := restoreCommand([]string{"--from", src, "--to", dst}, &out, &errOut, filepath.Join(t.TempDir(), "live.db")); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	body, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "user data" {
		t.Errorf("the existing file was modified: %q", body)
	}
}
