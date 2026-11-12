package db

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"repotory/internal/errcode"
)

const openAttempts = 6

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
		if !errcode.IsLockContention(initErr) {
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
