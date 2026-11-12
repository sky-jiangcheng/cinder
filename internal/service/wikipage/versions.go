package wikipage

import (
	"database/sql"
	"repotory/internal/db"
)

// Page version reads (ADR-0021 决策 1's consumer side).
//
// The snapshot itself is written by a trigger (see internal/db/wiki_versions.go),
// so there is deliberately no "save a version" entry point here: a Go-side writer
// would be a second, forgettable path to the same fact, and the one that can be
// skipped is the one that gets skipped.
//
// What lives here is only what the review surface needs to LOOK: the list, and a
// diff of one snapshot against the live page. Neither can change anything, which is
// the whole reason the history view is safe to expose without a gate on it.

// ListWikiPageVersions returns a page's snapshots, newest first. A page that has
// never had approved content returns an empty list rather than an error — "no
// history yet" is a fact about the page, not a failure.
func ListWikiPageVersions(database *sql.DB, pageID int64) ([]db.PageVersion, error) {
	vers, err := db.ListPageVersions(database, pageID)
	if err != nil {
		return nil, err
	}
	if vers == nil {
		// A nil slice serialises to JSON null, and the review panel maps over it
		// directly. Emit [] instead.
		vers = []db.PageVersion{}
	}
	return vers, nil
}

// DiffWikiPageVersion compares one snapshot against the page's current content, as
// the same line-diff text note versions use so the review page renders both with one
// component.
func DiffWikiPageVersion(database *sql.DB, pageID, versionID int64) (string, error) {
	return db.DiffPageVersion(database, pageID, versionID)
}
