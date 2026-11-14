package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"modernc.org/sqlite"
)

// The reader open path (ADR-0058, TODO 13.7 / 13.8).
//
// The writer owns the shared database. Every other Cinder process opens the
// same file, and the old open path wrote to it: the WAL pragma, the schema
// re-assertions, the migrations, the FTS rebuild. Those writes are what made
// `database is locked` reach an agent tool call — a fresh cinder-mcp process
// opened the database while the writer was inside a write transaction, and
// both wanted the lock.
//
// A reader opens the file without writing to it: no WAL switch, no schema
// work, no writes of any kind — and it does not create the file when it is
// missing. The restriction is at the FILE level (SQLite's mode=ro), not a
// per-connection pragma: a reader that breaks the contract fails loudly at
// the SQLite level, and the file it was handed stays untouched.
//
// The handle is not a dead end: when the writer dies, this process takes the
// writer role. SQLite cannot upgrade a file handle it opened read-only, so
// the promotion opens the same file writable and lets the pool use it from
// then on. The election lock — not the handle — decides who writes.

const (
	// readerWaitBudget bounds how long a reader waits for the writer's
	// database: the file has to exist, and the schema has to be ready.
	readerWaitBudget = 10 * time.Second
	// readerWaitPoll is the gap between two checks.
	readerWaitPoll = 100 * time.Millisecond
)

// readerSwitches maps an open reader handle to the switch that can turn it
// writable.
//
// Why a map and not a field on the handle: the *sql.DB is what every layer
// holds (the service, the plugin runtime, the review runner), and it must
// not change when the role changes. The switch is found through the handle
// when the writer role is taken.
var readerSwitches sync.Map // *sql.DB -> *readerSwitch

// readerSwitch opens the connections of a read-only handle, and switches the
// DSN when this process takes the writer role.
//
// Why a connector and not a second handle: the *sql.DB that the service layer
// holds must not change — the plugin runtime and the review runner captured
// it — and SQLite cannot upgrade a file handle it opened read-only. So the
// handle stays what it is and the CONNECTIONS behind it change: the pool
// asks the connector for a connection, and the connector answers with the DSN
// of the current role.
//
// The connectors are built on modernc's package-level driver (through
// sqlite.NewConnector), so the functions, collations and connection hooks
// registered on it still apply — this is not a second, diverging driver.
type readerSwitch struct {
	ro driver.Connector
	rw driver.Connector
	// writable flips when this process takes the writer role.
	writable atomic.Bool
}

// newReaderSwitch builds the switch for a database file.
func newReaderSwitch(path string) (*readerSwitch, error) {
	roDSN, err := readOnlyDSN(path)
	if err != nil {
		return nil, err
	}
	rwDSN, err := readWriteDSN(path)
	if err != nil {
		return nil, err
	}
	ro, err := sqlite.NewConnector(roDSN)
	if err != nil {
		return nil, fmt.Errorf("failed to build the read-only connector: %w", err)
	}
	rw, err := sqlite.NewConnector(rwDSN)
	if err != nil {
		return nil, fmt.Errorf("failed to build the writable connector: %w", err)
	}
	return &readerSwitch{ro: ro, rw: rw}, nil
}

// Connect implements driver.Connector.
func (s *readerSwitch) Connect(ctx context.Context) (driver.Conn, error) {
	base, readOnly := s.ro, true
	if s.writable.Load() {
		base, readOnly = s.rw, false
	}
	conn, err := base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &readerConn{Conn: conn, readOnly: readOnly, sw: s}, nil
}

// Driver implements driver.Connector. It reports the same driver as
// sql.Open("sqlite", ...) does, because the connectors are built on the
// package-level driver.
func (s *readerSwitch) Driver() driver.Driver { return s.ro.Driver() }

// promote switches the handle to the writable DSN, and reports whether it
// had to.
func (s *readerSwitch) promote() (bool, error) {
	if s.writable.Load() {
		return false, nil
	}
	s.writable.Store(true)
	return true, nil
}

// readerConn is one connection of a reader handle.
//
// It refuses to serve a connection that was opened read-only once the handle
// became writable: SQLite opened the file read-only for that connection, and
// it cannot be upgraded. The pool asks IsValid when the connection goes back
// and ResetSession when it comes out again, so a stale connection is dropped
// and the next statement gets a fresh writable connection (database/sql
// retries on driver.ErrBadConn).
type readerConn struct {
	driver.Conn
	readOnly bool
	sw       *readerSwitch
}

// stale reports whether the connection can no longer serve the handle: it was
// opened read-only and the handle has become writable since.
func (c *readerConn) stale() bool {
	return c.readOnly && c.sw.writable.Load()
}

// IsValid implements driver.Validator.
func (c *readerConn) IsValid() bool {
	if c.stale() {
		return false
	}
	if v, ok := c.Conn.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

// ResetSession implements driver.SessionResetter.
func (c *readerConn) ResetSession(ctx context.Context) error {
	if c.stale() {
		return driver.ErrBadConn
	}
	if r, ok := c.Conn.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

// PrepareContext implements driver.ConnPrepareContext.
func (c *readerConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if p, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return p.PrepareContext(ctx, query)
	}
	return c.Conn.Prepare(query)
}

// BeginTx implements driver.ConnBeginTx.
func (c *readerConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if b, ok := c.Conn.(driver.ConnBeginTx); ok {
		return b.BeginTx(ctx, opts)
	}
	return c.Conn.Begin()
}

// ExecContext implements driver.ExecerContext.
func (c *readerConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if e, ok := c.Conn.(driver.ExecerContext); ok {
		return e.ExecContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

// QueryContext implements driver.QueryerContext.
func (c *readerConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if q, ok := c.Conn.(driver.QueryerContext); ok {
		return q.QueryContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

// Ping implements driver.Pinger.
func (c *readerConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

// OpenReader opens the shared knowledge base for a process that is not the
// writer: read-only at the file level, and without writing a single byte to
// the database.
//
// The file is not created when it is missing: the writer creates the
// database, and a reader that arrives before the writer does waits (bounded)
// for it to appear rather than becoming the creator itself.
func OpenReader(path string) (*sql.DB, error) {
	return openReader(path, readerWaitBudget)
}

// openReader is OpenReader with an explicit wait budget: the budget is a
// parameter so the gate for "the reader does not create the database" can
// run in milliseconds instead of waiting out the production budget.
func openReader(path string, budget time.Duration) (*sql.DB, error) {
	sw, err := newReaderSwitch(path)
	if err != nil {
		return nil, err
	}
	d := sql.OpenDB(sw)
	d.SetMaxOpenConns(1)
	d.SetConnMaxLifetime(0)
	if err := waitForReader(d, path, budget); err != nil {
		d.Close() //nolint:errcheck // close on init failure, error irrelevant
		return nil, err
	}
	readerSwitches.Store(d, sw)
	return d, nil
}

// readOnlyDSN builds the file-level read-only DSN.
//
// mode=ro is a SQLite URI parameter, so it only takes effect in a DSN that
// starts with "file:" — a plain file name has its query stripped by the
// driver before SQLite ever sees it (modernc's own driver documentation says
// so). mode=ro opens the file read-only: every write fails with "attempt to
// write a readonly database", and a MISSING file is not created — the open
// fails with SQLITE_CANTOPEN.
//
// _query_only is the second line of defence: a per-connection pragma that
// any later statement could turn off again. It belongs to the read-only DSN
// only — the writable DSN must not carry it.
func readOnlyDSN(path string) (string, error) {
	q := url.Values{}
	q.Set("mode", "ro")
	q.Set("_query_only", "1")
	q.Set("_busy_timeout", "5000")
	q.Set("_pragma", "foreign_keys(1)")
	return sqliteURI(path, q)
}

// readWriteDSN builds the writable DSN: read-write, but never creating the
// file.
//
// The desktop's client role is a writer by necessity (the human-gate writes
// have no forward path), and it must still not bring a database into
// existence behind the writer's back.
func readWriteDSN(path string) (string, error) {
	q := url.Values{}
	q.Set("mode", "rw")
	q.Set("_busy_timeout", "5000")
	q.Set("_pragma", "foreign_keys(1)")
	return sqliteURI(path, q)
}

// sqliteURI builds a SQLite URI for a database file.
//
// The path is made absolute first: a relative path in a "file:" URI would be
// read as a URI authority rather than as a path. The URI form also
// percent-encodes the path, so a database file whose name contains a space
// or a question mark is still opened.
func sqliteURI(path string, q url.Values) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to resolve the database path %q: %w", path, err)
	}
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		// A Windows drive letter ("C:/...") needs the leading slash to be
		// read as a path and not as an authority.
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p, RawQuery: q.Encode()}
	return u.String(), nil
}

// waitForReader waits, bounded, until the reader can actually read the
// database: the file must exist (the writer creates it) and the schema must
// be ready (the writer owns it).
//
// The two failures are told apart, because they call for different actions:
// a missing file means "no writer has created the database yet", a missing
// table means "the writer is still creating the schema".
func waitForReader(d *sql.DB, path string, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	var lastErr error
	for {
		_, err := GetConfig(d, "schema_version")
		if err == nil {
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return readerNotReady(path, budget, lastErr)
		}
		time.Sleep(readerWaitPoll)
	}
}

// readerNotReady explains why the reader could not read the database, and
// what to do about it.
func readerNotReady(path string, budget time.Duration, lastErr error) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf(
			"the knowledge base %s does not exist after waiting %s: no writer has created it — start the writer process (Cinder Desktop, or an agent) first: %w",
			path, budget, lastErr)
	}
	return fmt.Errorf(
		"the knowledge base %s has no usable schema after waiting %s: the writer is busy creating it or is stuck: %w",
		path, budget, lastErr)
}

// OpenExisting opens the shared database without touching the schema.
//
// The desktop's client role. A human is clicking in the app, and the
// human-gate writes (approve / merge / config) have no catalogue method to
// forward to — they are the desktop's own acts. So the handle stays writable.
//
// The schema is still the writer's (ADR-0058, TODO 13.7): this path creates
// nothing, migrates nothing and rebuilds nothing, so opening it cannot
// collide with the writer's lock the way the old open path did. It does not
// create the file either: the writer creates the database.
func OpenExisting(path string) (*sql.DB, error) {
	return openExisting(path, readerWaitBudget)
}

// openExisting is OpenExisting with an explicit wait budget, for the same
// reason as openReader.
func openExisting(path string, budget time.Duration) (*sql.DB, error) {
	dsn, err := readWriteDSN(path)
	if err != nil {
		return nil, err
	}
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	d.SetMaxOpenConns(1)
	d.SetConnMaxLifetime(0)
	// No journal_mode switch: the file is already in WAL mode (the writer
	// converted it), and switching the journal mode writes to the header.
	if err := waitForReader(d, path, budget); err != nil {
		d.Close() //nolint:errcheck // close on init failure, error irrelevant
		return nil, err
	}
	return d, nil
}

// ReadOnly reports whether the handle refuses writes.
//
// For a reader handle the answer comes from the role it was opened in: the
// file is open read-only, and no pragma can change that. For any other
// handle the per-connection pragma is the answer.
func ReadOnly(d *sql.DB) bool {
	if sw, ok := readerSwitches.Load(d); ok {
		return !sw.(*readerSwitch).writable.Load()
	}
	var q int
	if err := d.QueryRow("PRAGMA query_only").Scan(&q); err != nil {
		return false
	}
	return q != 0
}

// PromoteToWriter makes the handle writable again, and reports whether it
// had to.
//
// Why it exists: a client opens the shared database read-only, but when the
// writer dies this process takes the writer role — and the role must be able
// to write. The election lock, not the handle, decides who writes.
//
// The promotion is a switch, not a pragma: the file was opened read-only,
// and SQLite cannot upgrade an open file handle. So the handle starts
// handing out connections built from the writable DSN, and the read-only
// connections the pool still holds are dropped (they cannot write) — the
// next statement gets a fresh writable connection.
//
// The schema work is NOT here: that belongs to the writer role, and the role
// is the writer's business (the writerapi manager runs EnsureSchema when this
// process takes over).
func PromoteToWriter(d *sql.DB) (bool, error) {
	if sw, ok := readerSwitches.Load(d); ok {
		return sw.(*readerSwitch).promote()
	}
	// A handle that was never read-only needs no promotion.
	if !ReadOnly(d) {
		return false, nil
	}
	return false, fmt.Errorf(
		"failed to make the database writable: the handle is read-only but was not opened as a reader")
}
