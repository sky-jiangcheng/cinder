package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"repotory/internal/diff"
)

// Page version snapshots (ADR-0021, schema v22).
//
// The page layer had the asymmetry that notes do not: `project_notes` carries
// `note_versions` (migration v8/v11, storing the pre-edit state since v31) so
// "what did this note say before I saved" is answerable, while
// `wiki_pages.content` is overwritten in place — so once a
// page is edited or superseded there is no way to see what a human actually
// approved. For hand-written notes that is a mild loss; for content a model
// drafted and a person waved through, "the approved version is reproducible" is
// the whole audit.
//
// Four properties, and the reason each is not the obvious alternative:
//
//  1. Snapshots only ever hold APPROVED content. A pending page is a draft and a
//     rejected page is a tombstone (M6-W3-UI); neither is a conclusion, so neither
//     has a "conclusion history" worth keeping. This is what the WHEN clause
//     enforces, not a caller convention.
//  2. The writer is a trigger, not service code. ADR-0021 决策 2 lists two write
//     points (approve, and "any future path that mutates approved content"). One
//     trigger covers both plus every path that does not exist yet, for the same
//     reason migration v14 put the embedding queue on triggers: internal/db is
//     where all writers converge — importers, the compiler, a future revise_page,
//     and anything a later commit adds all write through here. A Go-side helper
//     is a helper someone forgets to call.
//  3. Append-only is a constraint, not a policy: BEFORE UPDATE / BEFORE DELETE
//     both RAISE(ABORT). 决策 3 asks for exactly that, so "clean up the history
//     table" becomes a migration exercise (drop the triggers, edit, re-add) rather
//     than a quiet DELETE.
//  4. NO foreign key on page_id. This one is forced by (3) and is the trap worth
//     writing down: SQLite fires row triggers for foreign-key CASCADE actions. The
//     experiment is in wiki_versions_test.go, and the result is that
//     `REFERENCES wiki_pages(id) ON DELETE CASCADE` combined with the BEFORE DELETE
//     trigger makes `DELETE FROM wiki_pages` — and therefore deleting an entire
//     project, which cascades through wiki_pages — fail with "append-only".
//     Choosing between "project deletion breaks" and "snapshots outlive their page"
//     is not close for an audit table: an audit row that vanishes when the thing it
//     records is deleted is not an audit row. So page_id is an ordinary INTEGER,
//     orphan rows are expected, and `ListPageVersions` can still read the history
//     of a page that is gone.
//
// Like the rest of the wiki layer this is derived, discardable structure:
// DropWikiSchema removes it and the reversibility proof in wiki_test.go still
// holds byte-for-byte.

// pageVersionColumns is the projection shared by every read of page_versions.
const pageVersionColumns = "id, page_id, COALESCE(title, ''), content, COALESCE(kind, ''), " +
	"COALESCE(source, 'manual'), COALESCE(created_at, '')"

// PageVersion is one frozen snapshot of a page's approved content.
type PageVersion struct {
	ID        int64  `json:"id"`
	PageID    int64  `json:"page_id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Kind      string `json:"kind"`
	Source    string `json:"source"`
	CreatedAt string `json:"created_at"`
}

func scanPageVersion(rows interface{ Scan(dest ...any) error }, v *PageVersion) error {
	return rows.Scan(&v.ID, &v.PageID, &v.Title, &v.Content, &v.Kind, &v.Source, &v.CreatedAt)
}

// pageVersionObjects are the objects this file owns, in drop order (triggers
// before their table). wiki_versions_test.go asserts this list matches
// sqlite_master so a new trigger cannot silently escape DropWikiSchema.
var pageVersionObjects = []struct{ kind, name string }{
	{"TRIGGER", "page_versions_no_update"},
	{"TRIGGER", "page_versions_no_delete"},
	{"TRIGGER", "page_versions_snap"},
	{"INDEX", "idx_page_versions_page"},
	{"TABLE", "page_versions"},
}

// pageVersionSchemaStatements is the DDL, defined once so the v22 migration, the
// InitDB re-assert and a test harness cannot disagree (same trick as
// pageLinksTableDDL in wiki.go).
var pageVersionSchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS page_versions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		page_id INTEGER NOT NULL,
		title TEXT NOT NULL DEFAULT '',
		content TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL DEFAULT '',
		source TEXT NOT NULL DEFAULT 'manual',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`,
	// Newest first, which is the only order any consumer of a history list wants.
	`CREATE INDEX IF NOT EXISTS idx_page_versions_page ON page_versions(page_id, id DESC)`,
	// 决策 3: the snapshot table is append-only as a structural fact.
	`CREATE TRIGGER IF NOT EXISTS page_versions_no_update BEFORE UPDATE ON page_versions BEGIN ` +
		`SELECT RAISE(ABORT, 'page_versions is append-only: snapshots cannot be modified'); END`,
	`CREATE TRIGGER IF NOT EXISTS page_versions_no_delete BEFORE DELETE ON page_versions BEGIN ` +
		`SELECT RAISE(ABORT, 'page_versions is append-only: snapshots cannot be deleted'); END`,
	// 决策 2, the only writer. Fires when a page BECOMES approved (freeze what the
	// human is signing off on) or when an already-approved page's content, title or
	// kind CHANGES (freeze the outgoing state before it is gone).
	//
	// It stores the NEW row. note_versions used to do the same and no longer does
	// (schema v31 flips it to old.*, ADR-0057 D3), so the two tables now answer
	// different questions and that divergence is intentional: a page snapshot is
	// the record of what a HUMAN APPROVED ("what did I sign off on"), which only
	// exists after the write; a note snapshot is the history of what was there
	// before, because notes have no approval gate and the pre-edit state is the
	// thing that would otherwise be lost. Keeping new.* here also means the newest
	// entry's `DiffPageVersion` against the live page degrades to empty, which is
	// truthful; comparing with the previous row would leave the current content
	// with no snapshot at all.
	//
	// status/rejected transitions deliberately do NOT fire: turning an approved page
	// into a merge tombstone (ADR-0019 决策 5) changes new.status away from
	// 'approved', so the WHEN clause is false and no junk version is written.
	`CREATE TRIGGER IF NOT EXISTS page_versions_snap AFTER UPDATE ON wiki_pages
		 FOR EACH ROW WHEN (new.status = 'approved' AND (
			old.status <> 'approved'
			OR new.content <> old.content
			OR new.title <> old.title
			OR new.kind <> old.kind)) BEGIN
		INSERT INTO page_versions(page_id, title, content, kind, source)
		VALUES (new.id, COALESCE(new.title, ''), new.content, new.kind, COALESCE(new.source, 'manual'));
	END`,
}

// EnsurePageVersions is migration v22. Idempotent, and exported so tests and the
// InitDB re-assert share one definition.
//
// The statements are IF NOT EXISTS throughout, so replaying after a partial crash
// is a no-op. The backfill is separate because a trigger only covers writes from
// now on: every page that was already approved when this migration runs must get
// exactly one baseline snapshot, otherwise "approved content is reproducible" would
// be true only for pages approved after v22 — the same class of half-applied state
// migration v18 was split out to avoid.
func EnsurePageVersions(db *sql.DB) error {
	for _, stmt := range pageVersionSchemaStatements {
		if _, err := db.Exec(stmt); err != nil {
			if isAlreadyExistsErr(err) {
				continue
			}
			return fmt.Errorf("db: ensure page versions: %w", err)
		}
	}
	return backfillPageVersions(db)
}

// backfillPageVersions files one snapshot per approved page that has none.
// created_at comes from the page's own updated_at, which is the closest thing to
// "when did this content become the conclusion" that pre-v22 databases record.
// The NOT EXISTS guard is what makes it safe to run on every open: a page with
// history is left completely alone, so the backfill can never overwrite or
// duplicate a real snapshot.
func backfillPageVersions(db *sql.DB) error {
	_, err := db.Exec(
		`INSERT INTO page_versions(page_id, title, content, kind, source, created_at)
		 SELECT w.id, COALESCE(w.title, ''), w.content, w.kind, COALESCE(w.source, 'manual'),
		        COALESCE(w.updated_at, CURRENT_TIMESTAMP)
		   FROM wiki_pages w
		  WHERE w.status = 'approved'
		    AND NOT EXISTS (SELECT 1 FROM page_versions pv WHERE pv.page_id = w.id)`)
	if err != nil {
		return fmt.Errorf("db: backfill page versions: %w", err)
	}
	return nil
}

// dropPageVersions removes this file's objects. Called from DropWikiSchema.
func dropPageVersions(db *sql.DB) error {
	for _, o := range pageVersionObjects {
		if _, err := db.Exec("DROP " + o.kind + " IF EXISTS " + o.name); err != nil {
			return fmt.Errorf("db: drop page_versions %s %s: %w", o.kind, o.name, err)
		}
	}
	return nil
}

// ListPageVersions returns a page's snapshots, newest first. It deliberately reads
// a page that no longer exists as easily as one that does (no FK, so an orphaned
// history stays queryable) — that is the point of the table, not an accident.
func ListPageVersions(db *sql.DB, pageID int64) ([]PageVersion, error) {
	rows, err := db.Query(
		"SELECT "+pageVersionColumns+" FROM page_versions WHERE page_id = ? ORDER BY id DESC", pageID)
	if err != nil {
		return nil, fmt.Errorf("db: list page versions: %w", err)
	}
	defer rows.Close()
	var out []PageVersion
	for rows.Next() {
		var v PageVersion
		if err := scanPageVersion(rows, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetPageVersion loads one snapshot by id, sql.ErrNoRows when absent.
func GetPageVersion(db *sql.DB, versionID int64) (*PageVersion, error) {
	var v PageVersion
	err := scanPageVersion(db.QueryRow(
		"SELECT "+pageVersionColumns+" FROM page_versions WHERE id = ?", versionID), &v)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, sql.ErrNoRows
		}
		return nil, err
	}
	return &v, nil
}

// CountPageVersions is the growth signal ADR-0021 待决 3 says to watch: the table
// has no retention policy on purpose, so the number has to be readable before a
// limit gets argued for.
func CountPageVersions(db *sql.DB) (int, error) {
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM page_versions").Scan(&n); err != nil {
		return 0, fmt.Errorf("db: count page versions: %w", err)
	}
	return n, nil
}

// DiffPageVersion compares one snapshot against the page's CURRENT content, as
// unified line diff text. It is the page-layer twin of DiffNoteVersions, and it
// deliberately keeps the same shape (version → current, not current → version) so
// a reviewer reading both screens sees the same +/- convention.
//
// The version must belong to the page it is asked about: without that check, an
// id from another page would render a confident diff of two unrelated documents.
func DiffPageVersion(db *sql.DB, pageID, versionID int64) (string, error) {
	var v PageVersion
	err := scanPageVersion(db.QueryRow(
		"SELECT "+pageVersionColumns+" FROM page_versions WHERE id = ? AND page_id = ?",
		versionID, pageID), &v)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("db: page version %d not found for page %d: %w", versionID, pageID, sql.ErrNoRows)
		}
		return "", fmt.Errorf("db: load page version: %w", err)
	}
	var current string
	if err := db.QueryRow("SELECT content FROM wiki_pages WHERE id = ?", pageID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// The page is gone but its history is not (no FK, by design): diffing
			// against nothing would read as "everything was deleted", which is a
			// claim this function is not entitled to make.
			return "", fmt.Errorf("db: page %d no longer exists: %w", pageID, sql.ErrNoRows)
		}
		return "", fmt.Errorf("db: load current page: %w", err)
	}
	return diff.Lines(v.Content, current), nil
}
