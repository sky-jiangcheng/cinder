package db

import (
	"database/sql"
	"time"

	"repotory/internal/errcode"
)

// execDDLRetryingBusy runs one idempotent DDL statement, retrying while another
// connection holds the schema lock.
//
// busy_timeout is per connection and makes a statement WAIT; it does not make a
// crowd of them fair. Every open re-asserts the same DDL (DROP TRIGGER IF
// EXISTS / CREATE TRIGGER), so N processes starting together queue N times on
// the schema lock, and a slow runner can outlast the 5s pragma — the losing
// process then dies at startup with "database is locked" even though nothing is
// wrong with the database.
//
// Retrying the single statement keeps the cost bounded: the work is idempotent,
// so a retry after the winner commits simply succeeds. Eight attempts with
// linear backoff cap the added wait at 3.6s, and the final error is returned
// unchanged so a real failure still reports itself.
func execDDLRetryingBusy(db *sql.DB, stmt string) error {
	const attempts = 8
	var err error
	for i := 0; i < attempts; i++ {
		if _, err = db.Exec(stmt); err == nil || !errcode.IsLockContention(err) {
			return err
		}
		time.Sleep(time.Duration(i+1) * 100 * time.Millisecond)
	}
	return err
}
