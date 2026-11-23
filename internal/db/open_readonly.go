package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
)

// OpenReadOnly opens an existing database with SQLite-enforced read-only
// access (mode=ro): no migrations, no table creation, no writes of any kind.
// It is the session-brief path's opener (ADR-0066) — a SessionStart hook runs
// on every session, possibly while the desktop app holds the writer role, so
// it must neither migrate the live database nor contend for the write lock.
// A missing file is an error, not an invitation to create one.
func OpenReadOnly(dbPath string) (*sql.DB, error) {
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("db: read-only open: %w", err)
	}
	conn, err := sql.Open("sqlite", "file:"+filepath.ToSlash(abs)+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("db: read-only open: %w", err)
	}
	conn.SetMaxOpenConns(1)
	conn.SetConnMaxLifetime(0)
	// Connection-level pragma, no file write: waits out a concurrent writer's
	// checkpoint instead of failing the brief on SQLITE_BUSY.
	if _, err := conn.Exec("PRAGMA busy_timeout=5000"); err != nil {
		conn.Close() //nolint:errcheck // close on open failure, error irrelevant
		return nil, fmt.Errorf("db: read-only open: %w", err)
	}
	if err := conn.Ping(); err != nil {
		conn.Close() //nolint:errcheck
		return nil, fmt.Errorf("db: read-only open: %w", err)
	}
	return conn, nil
}
