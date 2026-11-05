package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// A second process that opens the same fresh database while the first one is
// still creating it gets SQLITE_BUSY on the journal-mode change, and no
// busy_timeout helps. enableWAL has to survive that window.
func TestEnableWALWaitsForFirstCreator(t *testing.T) {
	path := filepath.Join(t.TempDir(), "race.db")
	ctx := context.Background()

	seed, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Close() //nolint:errcheck // test teardown
	if _, err := seed.ExecContext(ctx, "CREATE TABLE seed (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}

	holder, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close() //nolint:errcheck // test teardown
	conn, err := holder.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck // test teardown
	if _, err := conn.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	// Longer than the 250ms wait of a single attempt, so a one-shot call
	// cannot pass this test.
	const hold = 800 * time.Millisecond
	released := make(chan struct{})
	go func() {
		defer close(released)
		time.Sleep(hold)
		_, _ = conn.ExecContext(ctx, "COMMIT")
	}()

	target, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close() //nolint:errcheck // test teardown
	target.SetMaxOpenConns(1)
	target.SetConnMaxLifetime(0)

	start := time.Now()
	if err := enableWAL(target); err != nil {
		t.Fatalf("enableWAL gave up while the first creator was still working: %v", err)
	}
	waited := time.Since(start)
	<-released

	var mode string
	if err := target.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
	if waited < hold {
		t.Fatalf("enableWAL returned in %v — it never waited for the lock", waited.Round(time.Millisecond))
	}
}

// The premise of the retry: one attempt really does lose the race, so a test
// of enableWAL alone could not tell waiting from getting lucky.
func TestJournalModeChangeFailsWhileLocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locked.db")
	ctx := context.Background()

	seed, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Close() //nolint:errcheck // test teardown
	if _, err := seed.ExecContext(ctx, "CREATE TABLE seed (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}

	holder, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close() //nolint:errcheck // test teardown
	conn, err := holder.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck // test teardown
	if _, err := conn.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.ExecContext(ctx, "COMMIT") }()

	target, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close() //nolint:errcheck // test teardown
	target.SetMaxOpenConns(1)

	if _, err := target.Exec("PRAGMA busy_timeout=250; PRAGMA journal_mode=WAL;"); err == nil {
		t.Fatal("the journal-mode change succeeded while another connection held the exclusive lock")
	}
}
