package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Page retrieval for the wiki layer (ADR-0014 决策 3 / TODO M6-W2).
//
// W1 shipped structure with no search, deliberately: search is the first thing a
// consumer needs, and W2 is that consumer. So page FTS arrives with W2 rather
// than being dead index in W1.
//
// Same shape as the notes/todos indexes: external-content FTS5 with the trigram
// tokenizer (case-insensitive substring matching for ASCII and CJK alike,
// ADR-0003) plus three sync triggers, so a page written through ANY path is
// immediately findable without a rebuild step. That "any path" property is the
// whole reason it is a trigger and not service code — W3's compiler will write
// pages through the same layer as everything else.
var wikiFTSSchemaStatements = []string{
	`CREATE VIRTUAL TABLE IF NOT EXISTS wiki_pages_fts USING fts5(` +
		`title, content, content='wiki_pages', content_rowid='id', tokenize='trigram')`,
	`CREATE TRIGGER IF NOT EXISTS wiki_pages_fts_ai AFTER INSERT ON wiki_pages BEGIN ` +
		`INSERT INTO wiki_pages_fts(rowid, title, content) VALUES (new.id, new.title, new.content); END`,
	`CREATE TRIGGER IF NOT EXISTS wiki_pages_fts_ad AFTER DELETE ON wiki_pages BEGIN ` +
		`INSERT INTO wiki_pages_fts(wiki_pages_fts, rowid, title, content) VALUES ('delete', old.id, old.title, old.content); END`,
	`CREATE TRIGGER IF NOT EXISTS wiki_pages_fts_au AFTER UPDATE ON wiki_pages BEGIN ` +
		`INSERT INTO wiki_pages_fts(wiki_pages_fts, rowid, title, content) VALUES ('delete', old.id, old.title, old.content); ` +
		`INSERT INTO wiki_pages_fts(rowid, title, content) VALUES (new.id, new.title, new.content); END`,
}

// wikiFTSObjects are the derived-index additions, listed separately from
// wikiObjects so DropWikiSchema's reversibility proof covers them too.
var wikiFTSObjects = []struct{ kind, name string }{
	{"TRIGGER", "wiki_pages_fts_ai"},
	{"TRIGGER", "wiki_pages_fts_au"},
	{"TRIGGER", "wiki_pages_fts_ad"},
	{"TABLE", "wiki_pages_fts"},
}

// EnsureWikiFTS creates the page index and its triggers. Idempotent, safe to
// call on any database that has wiki_pages (same contract as EnsureFTSIndex).
//
// The update trigger is AFTER UPDATE, not AFTER UPDATE OF ...: unlike the
// embedding queue (where a metadata-only write must not cost an HTTP request),
// an FTS row costs nothing, and an OF-restricted trigger would drift if a page
// column is ever added. Value comparison happens in the embedding queue precisely
// because that one has a price.
func EnsureWikiFTS(db *sql.DB) error {
	for _, stmt := range wikiFTSSchemaStatements {
		if _, err := db.Exec(stmt); err != nil {
			if isAlreadyExistsErr(err) {
				continue
			}
			return fmt.Errorf("db: ensure wiki FTS: %w", err)
		}
	}
	return nil
}

func dropWikiFTS(db *sql.DB) error {
	for _, o := range wikiFTSObjects {
		if _, err := db.Exec("DROP " + o.kind + " IF EXISTS " + o.name); err != nil {
			return fmt.Errorf("db: drop wiki FTS %s %s: %w", o.kind, o.name, err)
		}
	}
	return nil
}

// RebuildWikiFTS re-populates the page index from wiki_pages. Needed when the
// index was created after pages already existed (the triggers only cover writes
// from here on) and after the v7-style empty-index failure this repo has already
// lived through once (see migration v12).
//
// A schema change under a concurrent opener is retried rather than fatal.
// `INSERT ...('rebuild')` runs against the FTS5 virtual table, and SQLite
// invalidates a compiled virtual-table statement when any connection changes
// the schema ("database schema has changed: vtable constructor failed",
// SQLITE_SCHEMA). Several processes open this file — the desktop app, one
// cinder-mcp per IDE, cinder-server — and each one re-asserts its schema on
// open, so a page compile racing a peer's DDL is ordinary, not exceptional.
// The rebuild is idempotent, so re-running it is the whole fix; a bounded
// number of attempts keeps a genuinely broken index from spinning forever.
func RebuildWikiFTS(db *sql.DB) error {
	// Bounded: enough attempts to outlast a peer's concurrent DDL burst,
	// few enough that a real corruption still surfaces promptly.
	const attempts = 5
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		if _, err = db.Exec(`INSERT INTO wiki_pages_fts(wiki_pages_fts) VALUES('rebuild')`); err == nil {
			return nil
		}
		if !isSchemaChangedErr(err) {
			// Not a race: a missing table or a corrupt index is the caller's
			// to see on the first answer, not something to retry.
			return fmt.Errorf("db: rebuild wiki FTS: %w", err)
		}
		// A short, growing pause lets the peer's transaction land before the
		// statement is compiled again.
		time.Sleep(time.Duration(attempt+1) * 20 * time.Millisecond)
	}
	return fmt.Errorf("db: rebuild wiki FTS: %w", err)
}

// isSchemaChangedErr reports SQLITE_SCHEMA, the "a peer changed the schema
// while this statement was compiled" answer.
//
// Matched on the message because the driver surfaces it as a plain string:
// FTS5 reports it as "database schema has changed", and the vtable layer
// prefixes it with "vtable constructor failed". Both spellings mean the same
// retryable condition, and neither is a data problem.
func isSchemaChangedErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "database schema has changed") ||
		strings.Contains(msg, "vtable constructor failed")
}

// SearchWikiPages ranks APPROVED pages by bm25 over title+content — the status
// filter is ADR-0015's whole point: a page a human has not reviewed must never
// answer a question, appear in an evidence block, or surface in the knowledge
// search. The inventory paths (ListWikiPages, the export bypass, lint) still see
// every status, because pending rows are exactly what review and lint must look at.
//
// Empty query returns
// nothing rather than everything: callers use it as a retrieval stage, and an
// "everything" result would silently flood the evidence budget.
//
// projectID narrows the visible set to that project's pages plus the global ones
// (ADR-0032 决策 3): the same visibility the evidence path grants, applied in
// SQL so another project's page cannot take a LIMIT slot before the visible ones
// are reached. 0 means no narrowing.
func SearchWikiPages(db *sql.DB, query string, projectID int64, limit int) ([]WikiPage, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	// Reuse the notes path's escapers verbatim: two independent FTS quoting rules
	// in one package is how a quote in a user query eventually breaks one path and
	// not the other.
	if !ftsUsable(query) {
		return searchWikiPagesLike(db, query, projectID, limit)
	}
	phrase := escapeFTS(query)

	scope, scopeArgs := wikiScopeClause(projectID, "p")
	q := "SELECT " + pageColumnsP + ", bm25(wiki_pages_fts, 3.0, 1.0) AS rank\n" +
		"   FROM wiki_pages_fts\n" +
		"   JOIN wiki_pages p ON p.id = wiki_pages_fts.rowid\n" +
		"  WHERE wiki_pages_fts MATCH ? AND p.status = 'approved'"
	args := []any{phrase}
	if scope != "" {
		q += " AND " + scope
		args = append(args, scopeArgs...)
	}
	q += "\n  ORDER BY rank\n  LIMIT ?"
	args = append(args, limit)

	rows, err := db.Query(q, args...)
	if err != nil {
		// A rejected MATCH (unbalanced quotes and friends) must not be silent.
		return nil, fmt.Errorf("db: wiki page search: %w", err)
	}
	defer rows.Close()

	var out []WikiPage
	for rows.Next() {
		var (
			p    WikiPage
			rank float64
		)
		if err := scanPageFieldsWithRank(rows, &p, &rank); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) > 0 {
		return out, nil
	}

	// Same relaxation the notes path uses (ADR-0003 review): a multi-term AND
	// that matches nothing falls back to an OR of the terms, staying inside FTS so
	// a damaged index still returns nothing rather than pretending.
	loose := escapeFTSOR(query)
	if strings.TrimSpace(loose) == "" || loose == phrase {
		return out, nil
	}
	return SearchWikiPagesMatch(db, loose, projectID, limit)
}

// searchWikiPagesLike is the LIKE fallback for a query the trigram index
// cannot serve.
//
// The tokenizer stores 3-rune windows, so a 2-character CJK term ("时态") is
// unmatchable even though the page carrying it exists — the index holds no
// window of that width. The notes path has had this fallback for a long time
// (searchNotesLike); the wiki path did not, so a short Chinese query on the
// browse surface returned zero hits and the reader was told the knowledge did
// not exist. On a corpus written in Chinese, most queries are short.
//
// Ranking is synthetic rather than bm25 — bm25 lives in the FTS index we just
// gave up on: a title match outranks a content-only match, and a recently
// updated page outranks an older one.
func searchWikiPagesLike(db *sql.DB, query string, projectID int64, limit int) ([]WikiPage, error) {
	pages, err := searchWikiPagesLikePatterns(db, projectID, limit, []string{query})
	if err != nil || len(pages) > 0 {
		return pages, err
	}
	// The whole query as one contiguous substring can never match when the query
	// is several words: no page contains that exact run including the spaces.
	// The notes path relaxes the same way, and it only ever runs on an EMPTY
	// result, so it cannot displace a working answer.
	terms := likeTerms(query)
	if len(terms) < 2 {
		return pages, nil
	}
	return searchWikiPagesLikePatterns(db, projectID, limit, terms)
}

// searchWikiPagesLikePatterns returns the approved pages in scope whose title
// or content matches ANY of the patterns.
//
// The approval filter and the scope clause are the same two the FTS path
// applies: this fallback must not widen what a reader may see, only how the
// matches are found.
func searchWikiPagesLikePatterns(db *sql.DB, projectID int64, limit int, patterns []string) ([]WikiPage, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	// Argument order must mirror the SQL text: the rank expression is written
	// before the WHERE, so its title probes come first.
	titleProbes := make([]string, 0, len(patterns))
	where := make([]string, 0, len(patterns))
	rankArgs := make([]any, 0, len(patterns))
	whereArgs := make([]any, 0, 2*len(patterns))
	for _, p := range patterns {
		pat := "%" + escapeLike(p) + "%"
		titleProbes = append(titleProbes, "p.title LIKE ? ESCAPE '\\'")
		rankArgs = append(rankArgs, pat)
		where = append(where, "(p.title LIKE ? ESCAPE '\\' OR p.content LIKE ? ESCAPE '\\')")
		whereArgs = append(whereArgs, pat, pat)
	}
	args := append(rankArgs, whereArgs...)

	rankExpr := "CASE WHEN " + strings.Join(titleProbes, " OR ") + " THEN 0 ELSE 1 END"
	q := "SELECT " + pageColumnsP + ", " + rankExpr + " AS rank\n" +
		"   FROM wiki_pages p\n" +
		"  WHERE p.status = 'approved'\n" +
		"    AND (" + strings.Join(where, " OR ") + ")"
	if scope, scopeArgs := wikiScopeClause(projectID, "p"); scope != "" {
		q += "\n    AND " + scope
		args = append(args, scopeArgs...)
	}
	q += "\n  ORDER BY rank, COALESCE(p.updated_at, '') DESC, p.id DESC\n  LIMIT ?"
	args = append(args, limit)

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("db: wiki page like search: %w", err)
	}
	defer rows.Close()

	var out []WikiPage
	for rows.Next() {
		var (
			p    WikiPage
			rank float64
		)
		if err := scanPageFieldsWithRank(rows, &p, &rank); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SearchWikiPagesMatch runs one raw MATCH expression; exported for the A/B gate
// so it can measure the strict and relaxed paths separately.
//
// It carries the same project scope as SearchWikiPages: the relaxed path is the
// fallback for the strict one, so the two must agree on which pages exist —
// otherwise "searchable but unsearchable" would drift with the word count.
func SearchWikiPagesMatch(db *sql.DB, match string, projectID int64, limit int) ([]WikiPage, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	scope, scopeArgs := wikiScopeClause(projectID, "p")
	q := "SELECT " + pageColumnsP + ", bm25(wiki_pages_fts, 3.0, 1.0) AS rank\n" +
		"   FROM wiki_pages_fts\n" +
		"   JOIN wiki_pages p ON p.id = wiki_pages_fts.rowid\n" +
		"  WHERE wiki_pages_fts MATCH ? AND p.status = 'approved'"
	args := []any{match}
	if scope != "" {
		q += " AND " + scope
		args = append(args, scopeArgs...)
	}
	q += "\n  ORDER BY rank\n  LIMIT ?"
	args = append(args, limit)

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("db: wiki page match: %w", err)
	}
	defer rows.Close()
	var out []WikiPage
	for rows.Next() {
		var (
			p    WikiPage
			rank float64
		)
		if err := scanPageFieldsWithRank(rows, &p, &rank); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
