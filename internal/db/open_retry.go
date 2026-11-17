package db

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"repotory/internal/errcode"
)

const openAttempts = 6

// initRetryable reports whether a failed InitDB is worth a whole retry.
//
// "database is locked" is the documented case. "database schema has changed"
// (SQLITE_SCHEMA) belongs here too: every open re-asserts the same DDL, so when
// two processes start together one can have its FTS vtable invalidated between
// statements by the other's commit — the schema settles as soon as that
// transaction ends, so a retry converges. A genuinely broken schema fails the
// same way every time and still exhausts the attempts with its error intact.
func initRetryable(err error) bool {
	if errcode.IsLockContention(err) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "database schema has changed")
}

// OpenWithRetry opens the knowledge base, retrying while another Cinder
// process holds the write lock. Opening the shared SQLite file writes (WAL
// pragma, schema re-assertions, migrations), so a desktop app mid-import can
// hold the lock longer than busy_timeout; a cold MCP start used to exit at
// once with "database is locked". Every other failure is returned on the
// first try — a missing file or a broken schema must not be retried into a
// timeout.
func OpenWithRetry(path string) (*sql.DB, error) {
	var err error
	for attempt := 1; attempt <= openAttempts; attempt++ {
		handle, initErr := InitDB(path)
		if initErr == nil {
			return handle, nil
		}
		err = initErr
		if !initRetryable(initErr) {
			return nil, err
		}
		if attempt == openAttempts {
			break
		}
		wait := time.Duration(attempt) * time.Second
		log.Printf("database is locked by another Cinder process (attempt %d/%d); retrying in %s", attempt, openAttempts, wait)
		time.Sleep(wait)
	}
	return nil, fmt.Errorf("database stayed locked after %d attempts: %w", openAttempts, err)
}
