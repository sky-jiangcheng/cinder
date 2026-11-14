package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"sync"
	"time"

	"modernc.org/sqlite"
)

// The reader open path (ADR-0058, TODO 13.7).
//
// The writer owns the shared database. Every other Cinder process opens the
// same file, and the old open path wrote to it: the WAL pragma, the schema
// re-assertions, the migrations, the FTS rebuild. Those writes are what made
// `database is locked` reach an agent tool call — a fresh cinder-mcp process
// opened the database while the writer was inside a write transaction, and
// both wanted the lock.
//
// A reader opens the file without writing to it: no WAL switch, no schema
// work, no writes of any kind. The handle still refuses writes at the SQLite
// level (query_only), so a reader that breaks the contract fails loudly
// instead of quietly writing to the shared file.
//
// The handle is not a dead end: when the writer dies, this process takes the
// writer role, and PromoteToWriter turns the handle writable again. The
// election lock — not the handle — decides who writes.

const (
	// schemaWaitBudget bounds how long a reader waits for the writer's schema.
	schemaWaitBudget = 10 * time.Second
	// schemaWaitPoll is the gap between two checks.
	schemaWaitPoll = 100 * time.Millisecond
)

// writableDSNs holds the reader DSNs whose connections must be writable.
//
// Why a set of DSNs and not a flag on the handle: the DSN is what a
// REPLACEMENT connection is built from, and a promoted writer keeps its handle
// for the rest of its life. The pool replaces the connection whenever the
// previous one was interrupted — a cancelled tool call poisons it, and
// database/sql then discards it and opens a new one. Without this set, that
// replacement would come back read-only and the new writer would fail every
// write with "attempt to write a readonly database".
var writableDSNs sync.Map // string -> struct{}

func init() {
	// The hook runs for every connection the driver opens, right after the
	// DSN's own pragmas have been applied. A reader's DSN asks for query_only,
	// so this is the one place that lifts it — for a promoted reader only.
	sqlite.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, dsn string) error {
		if _, ok := writableDSNs.Load(dsn); !ok {
			return nil
		}
		_, err := conn.ExecContext(context.Background(), "PRAGMA query_only=OFF", nil)
		return err
	})
}

// OpenReader opens the shared knowledge base for a process that is not the
// writer: read-only, and without writing a single byte to the database.
//
// The wait for the writer's schema is bounded. The writer owns the schema
// now, so a reader can arrive while the writer is still creating it (first
// run) or applying a migration — reading then fails with "no such table".
func OpenReader(path string) (*sql.DB, error) {
	d, err := sql.Open("sqlite", readerDSN(path))
	if err != nil {
		return nil, fmt.Errorf("failed to open database for reading: %w", err)
	}
	d.SetMaxOpenConns(1)
	d.SetConnMaxLifetime(0)
	if err := waitForSchema(d); err != nil {
		d.Close() //nolint:errcheck // close on init failure, error irrelevant
		return nil, err
	}
	return d, nil
}

// readerDSN builds the read-only DSN.
//
// query_only is a per-connection pragma that refuses every write ("attempt to
// write a readonly database"). It is applied when the connection is created,
// so it survives the pool replacing the connection — and PromoteToWriter can
// turn it off again when this process takes the writer role.
//
// The busy timeout and the foreign-key pragma come from the DSN for the same
// reason: they are connection settings, and a connection that missed them
// would behave differently from the writer's.
func readerDSN(path string) string {
	q := url.Values{}
	q.Set("_query_only", "1")
	q.Set("_busy_timeout", "5000")
	q.Set("_pragma", "foreign_keys(1)")
	return path + "?" + q.Encode()
}

// waitForSchema waits, bounded, until the writer has created the schema.
//
// "Created" means the app_config table answers a query: that is the first
// table createTables makes, and it exists in every schema version, so the
// check does not have to know which version the writer is migrating to.
func waitForSchema(d *sql.DB) error {
	deadline := time.Now().Add(schemaWaitBudget)
	var lastErr error
	for {
		_, err := GetConfig(d, "schema_version")
		if err == nil {
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return fmt.Errorf("database schema still not ready after %s (the writer is busy or stuck): %w",
				schemaWaitBudget, lastErr)
		}
		time.Sleep(schemaWaitPoll)
	}
}

// OpenExisting opens the shared database without touching the schema.
//
// The desktop's client role. A human is clicking in the app, and the
// human-gate writes (approve / merge / config) have no catalogue method to
// forward to — they are the desktop's own acts. So the handle stays writable.
//
// The schema is still the writer's (ADR-0058, TODO 13.7): this path creates
// nothing, migrates nothing and rebuilds nothing, so opening it cannot
// collide with the writer's lock the way the old open path did.
func OpenExisting(path string) (*sql.DB, error) {
	d, err := sql.Open("sqlite", existingDSN(path))
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	d.SetMaxOpenConns(1)
	d.SetConnMaxLifetime(0)
	// No journal_mode switch: the file is already in WAL mode (the writer
	// converted it), and switching the journal mode writes to the header.
	if err := waitForSchema(d); err != nil {
		d.Close() //nolint:errcheck // close on init failure, error irrelevant
		return nil, err
	}
	return d, nil
}

// existingDSN builds the writable DSN: the same connection settings as the
// reader, without query_only.
func existingDSN(path string) string {
	q := url.Values{}
	q.Set("_busy_timeout", "5000")
	q.Set("_pragma", "foreign_keys(1)")
	return path + "?" + q.Encode()
}

// ReadOnly reports whether the handle refuses writes — the reader open path
// sets query_only, and the writer path does not.
func ReadOnly(d *sql.DB) bool {
	var q int
	if err := d.QueryRow("PRAGMA query_only").Scan(&q); err != nil {
		return false
	}
	return q != 0
}

// PromoteToWriter makes a read-only handle writable again, and reports whether
// it had to.
//
// Why it exists: a client opens the shared database read-only, but when the
// writer dies this process takes the writer role — and the role must be able
// to write. The election lock, not the handle, decides who writes.
//
// The schema work is NOT here: that belongs to the writer role, and the role
// is the writer's business (the writerapi manager runs EnsureSchema when this
// process takes over).
//
// dbPath is the file this handle was opened from: the promotion is recorded
// for that DSN, so a connection the pool replaces later comes back writable
// too.
func PromoteToWriter(d *sql.DB, dbPath string) (bool, error) {
	if !ReadOnly(d) {
		return false, nil
	}
	// Register the DSN BEFORE lifting the pragma: a connection replaced from
	// now on must come back writable.
	writableDSNs.Store(readerDSN(dbPath), struct{}{})
	if _, err := d.Exec("PRAGMA query_only=OFF"); err != nil {
		return false, fmt.Errorf("failed to make the database writable: %w", err)
	}
	return true, nil
}
