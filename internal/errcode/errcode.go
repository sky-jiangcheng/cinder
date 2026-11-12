// Package errcode classifies storage and transport errors into one small,
// shared set of codes. The MCP surface, the headless server and the integrity
// report each used to carry their own strings.Contains copy of "is this a
// lock / a missing table / a transient failure"; keeping the matching in one
// place is what stops those verdicts from drifting apart.
package errcode

import (
	"database/sql"
	"errors"
	"strings"
)

// Wire-facing codes returned by Code and Classify.
const (
	NotFound  = "not_found"
	Retryable = "retryable"
	Internal  = "internal"
	// Conflict means "the write was refused because the caller's copy is
	// stale". It is deliberately NOT retryable: retrying the same stale write
	// fails again forever. The caller has to re-read and decide (re-apply /
	// merge / discard), which is a different action, not a backoff.
	Conflict = "conflict"
)

// ErrVersionConflict is the sentinel for an optimistic-lock miss (ADR-0057 D2):
// the row was written by another process between the caller's read and its
// write, so the write carried an expectedVersion that no longer holds.
//
// The sentinel lives here rather than in internal/db so that error
// classification, transport mapping and tests can recognise it without
// importing the storage package.
var ErrVersionConflict = errors.New("modified by another writer")

// lower returns a lowercased error message, "" for a nil error.
func lower(err error) string {
	if err == nil {
		return ""
	}
	return strings.ToLower(err.Error())
}

// hasAny reports whether msg contains any of the substrings.
func hasAny(msg string, subs ...string) bool {
	for _, s := range subs {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// IsMissingTable recognises the driver's "no such table" error.
func IsMissingTable(err error) bool {
	return hasAny(lower(err), "no such table")
}

// IsMissingColumn recognises the driver's "no such column" error.
func IsMissingColumn(err error) bool {
	return hasAny(lower(err), "no such column")
}

// IsLockContention is the narrow subset of retryable errors that is a SQLite
// write-lock conflict. An open-database retry waits only on this: a timeout or
// a refused connection is a different failure and must not be retried into a
// startup hang.
func IsLockContention(err error) bool {
	return hasAny(lower(err), "database is locked", "database table is locked", "sqlite_busy")
}

// isRetryableText is the broad transient-failure test the MCP wire code uses.
func isRetryableText(err error) bool {
	return hasAny(lower(err),
		"locked", "busy", "timeout", "deadline exceeded",
		"connection refused", "temporarily unavailable")
}

// Classify maps an error to a stable wire code and whether a retry may help.
// The sentinel wins first (the pure-Go driver has no typed "database is
// locked"); text matching is only a fallback. Structural schema errors and
// unknown errors are not transient, so they stay internal.
func Classify(err error) (string, bool) {
	if err == nil {
		return Internal, false
	}
	if errors.Is(err, ErrVersionConflict) {
		return Conflict, false
	}
	if errors.Is(err, sql.ErrNoRows) {
		return NotFound, false
	}
	if IsMissingTable(err) || IsMissingColumn(err) {
		return Internal, false
	}
	if isRetryableText(err) {
		return Retryable, true
	}
	return Internal, false
}

// Code returns just the classification code.
func Code(err error) string {
	code, _ := Classify(err)
	return code
}

// IsRetryable reports whether the failure is transient.
func IsRetryable(err error) bool {
	_, retryable := Classify(err)
	return retryable
}
