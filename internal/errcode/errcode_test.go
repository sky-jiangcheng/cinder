package errcode

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
)

func TestClassifySentinelAndText(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantCode  string
		wantRetry bool
	}{
		{"nil", nil, Internal, false},
		{"no rows", sql.ErrNoRows, NotFound, false},
		{"wrapped no rows", fmt.Errorf("note lookup: %w", sql.ErrNoRows), NotFound, false},
		{"locked", errors.New("database is locked"), Retryable, true},
		{"busy", errors.New("SQLITE_BUSY: database is busy"), Retryable, true},
		{"timeout", errors.New("connection timeout"), Retryable, true},
		{"connection refused", errors.New("dial tcp: connection refused"), Retryable, true},
		{"missing table", errors.New("no such table: project_notes"), Internal, false},
		{"missing column", errors.New("no such column: dependencies"), Internal, false},
		// The optimistic-lock miss (ADR-0057 D2). It wraps the sentinel, so a
		// caller that adds context still classifies as a conflict — and it is
		// deliberately NOT retryable: the same stale write fails forever.
		{"version conflict", ErrVersionConflict, Conflict, false},
		{"wrapped version conflict", fmt.Errorf("note 42: expected version 3: %w", ErrVersionConflict), Conflict, false},
		{"unknown", errors.New("unexpected nil pointer"), Internal, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, retry := Classify(c.err)
			if code != c.wantCode || retry != c.wantRetry {
				t.Errorf("Classify(%v) = (%q,%v), want (%q,%v)", c.err, code, retry, c.wantCode, c.wantRetry)
			}
			if got := Code(c.err); got != c.wantCode {
				t.Errorf("Code = %q, want %q", got, c.wantCode)
			}
			if got := IsRetryable(c.err); got != c.wantRetry {
				t.Errorf("IsRetryable = %v, want %v", got, c.wantRetry)
			}
		})
	}
}

func TestNarrowPredicates(t *testing.T) {
	if !IsLockContention(errors.New("database is locked")) {
		t.Error("database is locked must be lock contention")
	}
	if !IsLockContention(errors.New("database table is locked")) {
		t.Error("a table lock must be lock contention")
	}
	if !IsLockContention(errors.New("sqlite_busy")) {
		t.Error("sqlite_busy must be lock contention")
	}
	// A timeout is retryable on the wire but is NOT a write-lock conflict:
	// OpenWithRetry waits only on the latter.
	if IsLockContention(errors.New("connection timeout")) {
		t.Error("a timeout is not lock contention")
	}
	if IsLockContention(nil) {
		t.Error("nil is not lock contention")
	}
	if !IsMissingTable(errors.New("no such table: wiki_pages")) {
		t.Error("missing table not recognised")
	}
	if IsMissingTable(errors.New("no such column: x")) {
		t.Error("a missing column is not a missing table")
	}
	if !IsMissingColumn(errors.New("no such column: x")) {
		t.Error("missing column not recognised")
	}
	if IsMissingColumn(nil) || IsMissingTable(nil) {
		t.Error("nil must not match structural predicates")
	}
}
