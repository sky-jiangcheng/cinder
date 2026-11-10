package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// ErrNoScannedPaths is returned by CleanupStaleDataTx when it is asked to
// reconcile against an empty set of scanned paths.
//
// The empty case is not "the user deleted every repository" — it is
// indistinguishable from "the scan found nothing because the walk failed, the
// roots are unreadable, or the disk was unmounted". Treating it as
// authoritative deleted every repository and stat in the knowledge base, and
// that data cannot be reconstructed without re-reading git history from every
// project.
//
// Callers must treat this as a hard error, not as a no-op: silently skipping
// the cleanup would let genuinely-removed repos linger forever, which is the
// milder and recoverable failure.
var ErrNoScannedPaths = errors.New("refusing to clean up: scan produced no repository paths")

// CleanupStaleDataTx removes repositories (and their stats) whose paths are not
// in scannedPaths, then deletes orphaned projects that have no repositories,
// notes, or todos.
//
// scannedPaths must be non-empty; an empty slice returns ErrNoScannedPaths
// rather than deleting every repository and stat. See that error for why.
// It is a compatibility wrapper over CleanupStaleDataTxContext.
func CleanupStaleDataTx(tx *sql.Tx, scannedPaths []string) error {
	return CleanupStaleDataTxContext(context.Background(), tx, scannedPaths)
}

// CleanupStaleDataTxContext is CleanupStaleDataTx with a caller context, so a
// cancelled scan interrupts the long DELETEs instead of holding the single
// database connection for the whole transaction.
func CleanupStaleDataTxContext(ctx context.Context, tx *sql.Tx, scannedPaths []string) error {
	if len(scannedPaths) == 0 {
		return ErrNoScannedPaths
	}
	placeholders := strings.Repeat("?,", len(scannedPaths))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(scannedPaths))
	for i, p := range scannedPaths {
		args[i] = p
	}
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM daily_stats WHERE repository_id IN (SELECT id FROM repositories WHERE path NOT IN ("+placeholders+"))",
		args...); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM repositories WHERE path NOT IN ("+placeholders+")",
		args...); err != nil {
		return err
	}
	// The default ("Uncategorized") landing project is exempt: it has no repos by
	// design and is empty until an import uses it, so the "no repos/notes/todos"
	// test below would otherwise delete the very row imports depend on
	// (ADR-0023). Import placeholders are NOT exempt — an emptied placeholder
	// (all notes organized away) should be cleaned, while one that still holds
	// notes is kept by the notes predicate regardless of kind.
	_, err := tx.ExecContext(ctx,
		"DELETE FROM projects WHERE project_kind != 'default' "+
			"AND NOT EXISTS (SELECT 1 FROM repositories WHERE repositories.project_id = projects.id) "+
			"AND NOT EXISTS (SELECT 1 FROM project_notes WHERE project_notes.project_id = projects.id) "+
			"AND NOT EXISTS (SELECT 1 FROM project_todos WHERE project_todos.project_id = projects.id)")
	return err
}
