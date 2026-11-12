package db

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// rollingFiles lists the intraday snapshots present in dir.
func rollingFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && rollingSnapshotName.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	return names
}

// The daily snapshot answers "before the first open today"; this answers "an
// hour ago". Without it, a database broken at noon can only be restored to
// this morning (ADR-0057 D4).
func TestCreateRollingBackup_WritesIntradaySnapshots(t *testing.T) {
	database := setupTestDB(t)
	dir := t.TempDir()
	pid := createTestProject(t, database, "rolling")
	start := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)

	if _, err := CreateNoteEx(database, pid, "n1", "body", "", "knowledge", "manual"); err != nil {
		t.Fatalf("seed note: %v", err)
	}
	first, err := CreateRollingBackup(database, dir, start, time.Minute, 6)
	if err != nil {
		t.Fatalf("first rolling snapshot: %v", err)
	}
	if got := filepath.Base(first); got != "repotory-20261112-0900.db" {
		t.Fatalf("rolling snapshot name = %s", got)
	}
	if err := VerifySnapshot(first); err != nil {
		t.Fatalf("the snapshot it just wrote is not restorable: %v", err)
	}
	// The gap check reads the snapshot's real mtime, not the injected now, so the
	// fixture stamps the logical time onto the file (the same fixed-up mtime the
	// two sibling tests below arrange). Without this the second call at start+1h
	// sees a negative gap against the fresh real mtime and declines to write.
	if err := os.Chtimes(first, start, start); err != nil {
		t.Fatalf("chtimes first snapshot: %v", err)
	}

	// A write after the snapshot, an hour later: a second snapshot lands.
	if err := UpdateNote(database, 1, "body v2"); err != nil {
		t.Fatalf("edit note: %v", err)
	}
	second, err := CreateRollingBackup(database, dir, start.Add(time.Hour), time.Minute, 6)
	if err != nil {
		t.Fatalf("second rolling snapshot: %v", err)
	}
	if second == first {
		t.Fatal("a second snapshot was due but the first path came back")
	}
	if err := os.Chtimes(second, start.Add(time.Hour), start.Add(time.Hour)); err != nil {
		t.Fatalf("chtimes second snapshot: %v", err)
	}

	// Retention is its own budget: two snapshots, retain 1 → only the newest
	// survives, and the daily file next to them is untouched.
	daily, err := CreateBackup(database, dir, start, 14)
	if err != nil {
		t.Fatalf("daily snapshot: %v", err)
	}
	// A snapshot is only taken when something was written since the last one,
	// so the retention step needs a write of its own — that rule is the point,
	// not an accident of the fixture.
	if err := UpdateNote(database, 1, "body v3"); err != nil {
		t.Fatalf("edit note before the retention step: %v", err)
	}
	if _, err := CreateRollingBackup(database, dir, start.Add(2*time.Hour), time.Minute, 1); err != nil {
		t.Fatalf("third rolling snapshot: %v", err)
	}
	if files := rollingFiles(t, dir); len(files) != 1 {
		t.Fatalf("rolling retention 1 left %d files: %v", len(files), files)
	}
	if _, err := os.Stat(daily); err != nil {
		t.Errorf("rolling rotation removed the daily snapshot: %v", err)
	}
}

// Two launches five minutes apart must not write two full copies.
func TestCreateRollingBackup_RespectsMinGap(t *testing.T) {
	database := setupTestDB(t)
	dir := t.TempDir()
	start := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)

	first, err := CreateRollingBackup(database, dir, start, 30*time.Minute, 6)
	if err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	// Rewrite the mtime so the gap check sees a snapshot taken "now" — the
	// filename is minute-granular, which is not enough for a five-minute gap.
	now := time.Now()
	if err := os.Chtimes(first, now, now); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	again, err := CreateRollingBackup(database, dir, now.Add(5*time.Minute), 30*time.Minute, 6)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if again != first {
		t.Errorf("a snapshot inside the gap was written: %s (want %s)", again, first)
	}
	if files := rollingFiles(t, dir); len(files) != 1 {
		t.Errorf("min gap produced %d files, want 1: %v", len(files), files)
	}
}

// A tick with nothing written must cost zero disk. This is the difference
// between "six restore points of real work" and "six copies of the same
// database".
func TestCreateRollingBackup_SkipsWhenNothingWasWritten(t *testing.T) {
	database := setupTestDB(t)
	dir := t.TempDir()
	start := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)

	first, err := CreateRollingBackup(database, dir, start, time.Minute, 6)
	if err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	// The snapshot's mtime is "now"; age it so only the write check can stop a
	// second snapshot.
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(first, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	again, err := CreateRollingBackup(database, dir, time.Now(), time.Minute, 6)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if again != first {
		t.Errorf("an unchanged database produced a new snapshot: %s", again)
	}
	if files := rollingFiles(t, dir); len(files) != 1 {
		t.Errorf("got %d files, want 1: %v", len(files), files)
	}

	// One write and the same call does produce one — the write check must not
	// become a permanent off switch.
	pid := createTestProject(t, database, "rolling-writes")
	if _, err := CreateNoteEx(database, pid, "n", "body", "", "knowledge", "manual"); err != nil {
		t.Fatalf("seed note: %v", err)
	}
	third, err := CreateRollingBackup(database, dir, time.Now(), time.Minute, 6)
	if err != nil {
		t.Fatalf("third call: %v", err)
	}
	if third == first {
		t.Error("a write after the last snapshot must produce a new one")
	}
}
