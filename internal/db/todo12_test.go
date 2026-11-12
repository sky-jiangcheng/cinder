package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A handoff-tagged hit carries the previous session's exit record; bm25 alone
// can rank it past the LIMIT window when many ordinary notes match the query.
func TestSearchNotes_HandoffTaggedHitStaysInTopN(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()
	pid := createTestProject(t, database, "handoff-rank")

	// More ordinary matches than the limit, each repeating the term so bm25
	// ranks them above the single-mention handoff note.
	for i := 0; i < searchResultLimit+6; i++ {
		title := "ordinary " + strings.Repeat("x", i+1)
		if _, err := CreateNoteEx(database, pid, title,
			"deploy deploy deploy deploy deploy", "", "knowledge", "manual"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := CreateNoteEx(database, pid, "session handoff",
		"a single deploy mention in a long exit record", "handoff", "knowledge", "manual"); err != nil {
		t.Fatal(err)
	}

	hits, err := SearchNotes(database, "deploy")
	if err != nil {
		t.Fatalf("SearchNotes: %v", err)
	}
	if len(hits) != searchResultLimit {
		t.Fatalf("got %d hits, want the %d-row limit", len(hits), searchResultLimit)
	}
	if hits[0].Title != "session handoff" {
		t.Fatalf("handoff hit ranked first as %q; boost did not pull it to the top: %+v", hits[0].Title, hits)
	}
}

// A single gotcha bullet can reach ~1000 bytes. The old 100-byte window cut
// it in half; the enlarged window must return the whole bullet.
func TestMakeSnippet_LongBulletNotHalfCut(t *testing.T) {
	bullet := "- gotcha: " + strings.Repeat("migration dry-run and rollback ", 28) + "END"
	snippet := makeSnippet(bullet, "rollback")
	if !strings.Contains(snippet, "- gotcha:") {
		t.Errorf("bullet head missing from snippet: %q", snippet)
	}
	if !strings.Contains(snippet, "END") {
		t.Errorf("bullet tail missing (bullet was cut): %q", snippet)
	}
	if !strings.Contains(snippet, "<mark>rollback</mark>") {
		t.Errorf("query not highlighted: %q", snippet)
	}
}

// RestoreBackup copies a verified snapshot to a caller-chosen path without
// touching the source, the live db, or any pre-existing file.
func TestRestoreBackupCopiesToExplicitTarget(t *testing.T) {
	database := openSourceDB(t)
	snap, err := CreateBackup(database, t.TempDir(), time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC), 14)
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	before, err := os.Stat(snap)
	if err != nil {
		t.Fatal(err)
	}

	dstDir := filepath.Join(t.TempDir(), "restore", "nested")
	dst := filepath.Join(dstDir, "copy.db")
	written, err := RestoreBackup(snap, dst)
	if err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}
	if written == 0 {
		t.Fatal("RestoreBackup reported zero bytes copied")
	}
	conn, err := sql.Open("sqlite", dst)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var v string
	if err := conn.QueryRow("SELECT v FROM t WHERE id = 1").Scan(&v); err != nil || v != "hello" {
		t.Fatalf("restored copy lost its data: v=%q err=%v", v, err)
	}
	if err := VerifySnapshot(dst); err != nil {
		t.Fatalf("restored copy does not verify: %v", err)
	}
	after, err := os.Stat(snap)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != before.Size() {
		t.Fatal("source snapshot size changed")
	}
	if err := VerifySnapshot(snap); err != nil {
		t.Fatalf("source snapshot no longer verifies: %v", err)
	}
	entries, err := os.ReadDir(dstDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("destination dir has stray files: %v", entries)
	}
}

// RestoreBackup must be conservative: no overwrite, no bad snapshot, no
// self-copy, and no partial file left behind on failure.
func TestRestoreBackupRefusesToOverwriteOrAcceptBadSnapshot(t *testing.T) {
	database := openSourceDB(t)
	snap, err := CreateBackup(database, t.TempDir(), time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC), 14)
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	dir := t.TempDir()

	dst := filepath.Join(dir, "keep.db")
	if err := os.WriteFile(dst, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreBackup(snap, dst); err == nil {
		t.Fatal("RestoreBackup overwrote an existing file")
	}
	if got, _ := os.ReadFile(dst); string(got) != "precious" {
		t.Fatal("existing destination file was modified")
	}

	bad := filepath.Join(dir, "bad.db")
	if err := os.WriteFile(bad, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.db")
	if _, err := RestoreBackup(bad, out); err == nil {
		t.Fatal("RestoreBackup accepted a corrupt snapshot")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("RestoreBackup published a copy of a bad snapshot")
	}
	if _, err := RestoreBackup(snap, snap); err == nil {
		t.Fatal("RestoreBackup accepted destination == source")
	}
	// A failed run leaves no temp file in the destination directory.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".repotory-restore-") {
			t.Fatalf("failed restore left a temp file: %s", e.Name())
		}
	}
}
