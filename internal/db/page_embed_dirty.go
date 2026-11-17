package db

import (
	"database/sql"
	"fmt"
	"strings"
)

// Incremental page-embedding work queue — the page twin of note_embed_dirty
// (embed_dirty.go), the second half of ADR-0018 lane 2's vector space (schema v23).
//
// Same chokepoint argument as the note queue: wiki_pages is written through
// internal/db from every path — the compiler, approve/reject, merge absorption
// (ADR-0019), a future revise_page, and the CLI — none of which would call a
// service-layer hook reliably. A trigger-maintained queue covers all of them at
// once, exactly as FTS5 and the note-embed queue already do.
//
// The queue is a work queue, not data: it is derived from writes and always safe
// to truncate (a full re-embed covers everything). The drainer resolves each id
// against wiki_pages to tell "re-embed this approved page" from "remove its
// vector (page gone or no longer approved)". So the WHEN clauses mark a page dirty
// on any change to its embeddable text or its approved-ness, and the drainer —
// not the trigger — decides the fate, because it is the only place that can read
// the current status alongside the id.
//
// Value comparison (OLD.x IS NOT NEW.x), not column mention (AFTER UPDATE OF ...):
// the note queue learned this the hard way — an OF trigger is a SYNTACTIC test that
// fires whenever a statement names the column, so every status/reorder/metadata
// save that resends title would queue a wasted embedding request. Only a real text
// change or an approved-ness change may dirty a page.
var pageEmbedDirtyStatements = []string{
	`CREATE TABLE IF NOT EXISTS page_embed_dirty (
		page_id INTEGER PRIMARY KEY,
		marked_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`,
	// A page is created already-approved (CreateWikiPage) far more often than
	// pending, so an INSERT of an approved page must queue its first embed. The
	// WHEN guard keeps pending pages out until a human approves them.
	`DROP TRIGGER IF EXISTS wiki_pages_embed_ai`,
	`CREATE TRIGGER wiki_pages_embed_ai AFTER INSERT ON wiki_pages
		WHEN new.status = 'approved' BEGIN
		INSERT OR REPLACE INTO page_embed_dirty(page_id) VALUES (new.id);
	END`,
	// Any change to the embeddable text (title/content), or any transition of the
	// approved flag, dirties the page. Moving OUT of approved also dirties it, so
	// the drainer sees it and removes the vector.
	`DROP TRIGGER IF EXISTS wiki_pages_embed_au`,
	`CREATE TRIGGER wiki_pages_embed_au AFTER UPDATE ON wiki_pages
		WHEN new.title IS NOT old.title
			OR new.content IS NOT old.content
			OR new.status IS NOT old.status BEGIN
		INSERT OR REPLACE INTO page_embed_dirty(page_id) VALUES (new.id);
	END`,
	// A deleted page's vector must be removed, or it keeps being recalled forever.
	`DROP TRIGGER IF EXISTS wiki_pages_embed_ad`,
	`CREATE TRIGGER wiki_pages_embed_ad AFTER DELETE ON wiki_pages BEGIN
		INSERT OR REPLACE INTO page_embed_dirty(page_id) VALUES (old.id);
	END`,
}

// EnsurePageEmbedDirty creates the page dirty table and its triggers. Idempotent,
// exported so tests (and InitDB's re-assert) can set it up without the full
// migration machinery — same contract as EnsureNoteEmbedDirty.
//
// "Already exists" is tolerated rather than fatal, and that tolerance is load
// bearing: this runs on EVERY database open (InitDB re-asserts it so a dropped
// trigger heals), and several processes open the same file — the desktop app,
// one cinder-mcp per IDE, cinder-server. Two of them starting together both run
// `DROP TRIGGER IF EXISTS` and then both run `CREATE TRIGGER`; the second CREATE
// arrives after the first one committed, and without this guard it takes the
// whole process down with "trigger ... already exists" before the handshake.
// The sibling EnsureNoteEmbedDirty has always carried the same guard; this one
// was missing it, which is why only the page side failed under concurrent
// startup. The statements are not wrapped in one transaction because each is
// independently idempotent: a peer that loses the race ends in the same state.
func EnsurePageEmbedDirty(db *sql.DB) error {
	for _, stmt := range pageEmbedDirtyStatements {
		if _, err := db.Exec(stmt); err != nil {
			if isAlreadyExistsErr(err) {
				continue
			}
			return fmt.Errorf("db: ensure page embed dirty queue: %w", err)
		}
	}
	return nil
}

// pageEmbedDirtyMigration is the v23 schema change (page vector space). Kept as
// its own helper so migrate.go stays a list of ids and this file owns the concept,
// matching noteEmbedDirtyMigration.
func pageEmbedDirtyMigration(db *sql.DB) error {
	// The vec0 index itself is created lazily by the service layer's rebuild, once
	// it has learned the embedding dimension — exactly as note_embeddings is. The
	// dirty QUEUE is created here because it costs nothing and a page written
	// before the first rebuild must be queued so that rebuild covers it.
	return EnsurePageEmbedDirty(db)
}

// pageEmbedDirtyObjects are the objects this file owns, in drop order (triggers
// before their table). Like pageVersionObjects, an explicit list means a new
// trigger cannot silently escape DropWikiSchema, and a typo in either direction
// fails loudly in the reversibility test.
var pageEmbedDirtyObjects = []struct{ kind, name string }{
	{"TRIGGER", "wiki_pages_embed_ai"},
	{"TRIGGER", "wiki_pages_embed_au"},
	{"TRIGGER", "wiki_pages_embed_ad"},
	{"TABLE", "page_embed_dirty"},
}

// dropPageEmbedDirty removes this file's objects. Called from DropWikiSchema: the
// three triggers are ON wiki_pages, so they must be dropped BEFORE wiki_pages
// itself (a trigger whose table vanishes is the broken-index state this repo has
// already paid for once, see DropWikiSchema's note on the FTS table). The queue
// table is pure derived state and safe to drop with the rest of the layer.
func dropPageEmbedDirty(db *sql.DB) error {
	for _, o := range pageEmbedDirtyObjects {
		if _, err := db.Exec("DROP " + o.kind + " IF EXISTS " + o.name); err != nil {
			return fmt.Errorf("db: drop page embed dirty %s %s: %w", o.kind, o.name, err)
		}
	}
	return nil
}

// DirtyPageEmbedWork is one drain round's worth of page work, split by what the
// page's current state demands.
type DirtyPageEmbedWork struct {
	// ToEmbed are live APPROVED pages needing a (re)computed vector.
	ToEmbed []PageEmbeddingInput
	// Gone are dirty ids whose page is gone or no longer approved: their vectors
	// must be removed, or a pending/deleted page keeps being recalled.
	Gone []int64
}

// NextDirtyPageEmbedWork pops up to `limit` queued page ids (lowest id first, so
// the order is stable and testable) and resolves each against wiki_pages. One
// round trip: the LEFT JOIN carries the page text only when the row still exists
// and is approved; anything else is "Gone" (remove the vector).
func NextDirtyPageEmbedWork(db *sql.DB, limit int) (*DirtyPageEmbedWork, error) {
	if limit <= 0 {
		limit = 64
	}
	rows, err := db.Query(
		`SELECT d.page_id, n.title, n.content
		   FROM page_embed_dirty d
		   LEFT JOIN wiki_pages n ON n.id = d.page_id AND n.status = 'approved'
		  ORDER BY d.page_id LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("db: next dirty page embed work: %w", err)
	}
	defer rows.Close()

	work := &DirtyPageEmbedWork{}
	for rows.Next() {
		var (
			id             int64
			title, content sql.NullString
		)
		if err := rows.Scan(&id, &title, &content); err != nil {
			return nil, err
		}
		// A NULL join means the page is gone OR no longer approved (the JOIN's
		// status guard drops the latter), so both collapse to "remove the vector".
		if !title.Valid && !content.Valid {
			work.Gone = append(work.Gone, id)
			continue
		}
		work.ToEmbed = append(work.ToEmbed, PageEmbeddingInput{
			ID:   id,
			Text: PageEmbeddingText(title.String, content.String),
		})
	}
	return work, rows.Err()
}

// ClearDirtyPageIDs removes resolved page ids from the queue. Only called for work
// that actually landed: if an embedding request fails, the rows stay queued and
// the next drain retries them.
func ClearDirtyPageIDs(db *sql.DB, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	if _, err := db.Exec(
		"DELETE FROM page_embed_dirty WHERE page_id IN ("+placeholders+")", args...); err != nil {
		return fmt.Errorf("db: clear dirty page ids: %w", err)
	}
	return nil
}

// TruncateDirtyPageIDs empties the page queue (called after a successful full
// page rebuild, which has already covered everything still queued).
func TruncateDirtyPageIDs(db *sql.DB) error {
	if _, err := db.Exec("DELETE FROM page_embed_dirty"); err != nil {
		return fmt.Errorf("db: truncate dirty page queue: %w", err)
	}
	return nil
}
