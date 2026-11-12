package db

import (
	"database/sql"
	"html"
	"strings"
	"unicode"
	"unicode/utf8"
)

// searchSnippetWindow holds one whole gotcha bullet: a single item can reach
// 1000 bytes, so the old 100-byte window cut it in half.
// handoffRankBoost keeps a handoff-tagged hit inside the LIMIT window.
const (
	searchResultLimit   = 20
	searchSnippetWindow = 1024
	handoffRankBoost    = 10.0

	// maxFTSORTerms caps the relaxed OR expression. CJK expansion turns one
	// long run into N-2 phrases, so a pasted paragraph (600 distinct runes ->
	// 598 phrases) would otherwise build a MATCH string no one wants to
	// evaluate. The cap keeps a pathological paste from turning a search into a
	// long OR scan; a real question stays far below it.
	maxFTSORTerms = 32
)

// SearchNotes searches note title and content, returning ranked hits. It uses
// the FTS5 trigram index (issue #18) for relevance-ranked substring matching
// when every query term is at least 3 characters; otherwise it falls back to a
// LIKE scan (which also covers 2-character CJK queries the trigram tokenizer
// cannot match). If the FTS index is unavailable or yields no rows, it falls
// back to LIKE so search never regresses.
func SearchNotes(db *sql.DB, query string) ([]SearchHit, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	if ftsUsable(q) {
		hits, err := searchNotesFTS(db, q, escapeFTS(q))
		if err == nil {
			if len(hits) > 0 {
				return hits, nil // strict AND matched: behaviour unchanged
			}
			// M3-C (ADR-0012): strict AND yielded ZERO rows — relax by dropping
			// English stopwords and OR-ing the remaining terms. This only ever
			// adds results in cases that returned empty before, so it cannot
			// degrade a currently-good result set (zero dictionary, zero CGO).
			// It stays WITHIN FTS (no new LIKE fallback), so a broken/empty
			// index still yields nothing exactly as before.
			if orExpr := escapeFTSOR(q); orExpr != "" {
				if relaxed, rerr := searchNotesFTS(db, q, orExpr); rerr == nil {
					return relaxed, nil
				}
			}
			return hits, nil // empty AND result: preserve original behaviour
		}
		// FTS index missing or query rejected: fall through to LIKE.
	}
	return searchNotesLike(db, q)
}

// searchNotesFTS matches a prepared FTS5 expression against the index, ranked
// by bm25. `matchExpr` is produced by escapeFTS (strict AND) or escapeFTSOR
// (relaxed); `q` is the original query, used only to render the snippet.
func searchNotesFTS(db *sql.DB, q, matchExpr string) ([]SearchHit, error) {
	rows, err := db.Query(
		"SELECT n.id, n.project_id, n.title, n.content, bm25(project_notes_fts) "+
			"FROM project_notes_fts f "+
			"JOIN project_notes n ON n.id = f.rowid "+
			"WHERE project_notes_fts MATCH ? "+
			// A handoff-tagged hit carries the previous session's exit record;
			// bm25 alone can rank it past the 20-row window. The tag predicate
			// mirrors notes.go: comma-delimited exact match.
			"ORDER BY bm25(project_notes_fts) "+
			"- CASE WHEN ',' || REPLACE(LOWER(n.tags), ' ', '') || ',' LIKE '%,handoff,%' THEN ? ELSE 0 END, "+
			"n.pinned DESC, n.updated_at DESC LIMIT ?",
		matchExpr, handoffRankBoost, searchResultLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []SearchHit
	for rows.Next() {
		var h SearchHit
		var content string
		var rank float64
		if err := rows.Scan(&h.ID, &h.ProjectID, &h.Title, &content, &rank); err != nil {
			return nil, err
		}
		h.Type = "note"
		h.Rank = rank
		h.Snippet = makeSnippet(content, q)
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// searchNotesLike is the LIKE-based fallback used for short or special queries.
// It assigns a synthetic rank: title matches score higher than content-only
// matches, pinned notes get a bonus, and recent updates rank higher.
//
// When the whole query as one contiguous substring matches nothing, it retries
// OR-ing the individual terms. This is the same AND→OR relaxation the FTS path
// does, and it is needed for the same reason: a query like
// `FastAPI 里 /health 探测不到` contains a 1-rune term, so ftsUsable sends the
// whole query here — and as a single substring it can never match, because no
// note contains that exact run including the spaces. The retry only ever runs
// on an EMPTY result, so it cannot displace a working answer.
func searchNotesLike(db *sql.DB, q string) ([]SearchHit, error) {
	hits, err := likeQuery(db, q, []string{q})
	if err != nil || len(hits) > 0 {
		return hits, err
	}
	terms := likeTerms(q)
	if len(terms) < 2 {
		return hits, nil
	}
	return likeQuery(db, q, terms)
}

// likeTerms splits a query into the units worth OR-ing: whitespace-separated
// terms, each stripped of leading/trailing punctuation that would only prevent
// a match (`/health` alone is findable; `/health,` is not). Returns fewer than
// two entries when the query is a single unit (OR == AND).
func likeTerms(q string) []string {
	fields := strings.FieldsFunc(q, func(r rune) bool {
		return unicode.IsSpace(r)
	})
	var out []string
	seen := map[string]struct{}{}
	for _, f := range fields {
		f = strings.TrimFunc(f, func(r rune) bool {
			return unicode.IsPunct(r) || unicode.IsSymbol(r)
		})
		if f == "" {
			continue
		}
		if _, dup := seen[f]; dup {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	return out
}

// likeQuery returns notes whose title or content matches ANY of the patterns,
// ranked by the same synthetic rank searchNotesLike has always used: title
// match -10, content-only -5, pinned -20, recency bonus. `q` (the whole query)
// is passed separately because it renders the snippet — the snippet should
// highlight what the user typed, not which OR-branch happened to match.
//
// An empty patterns slice yields no rows rather than every row, which is what
// `LIKE '%%'` would do.
func likeQuery(db *sql.DB, q string, patterns []string) ([]SearchHit, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	// Weighted rank: title match = -10, content match = -5, pinned = -20,
	// recency bonus via julian day diff (lower is better).
	// COALESCE(pinned, 0) handles the NULL case that occurs during migration
	// before the ALTER TABLE ADD COLUMN DEFAULT 0 is applied to all rows.
	//
	// COALESCE(julianday(updated_at), julianday('now')) is not cosmetic: an
	// imported note can carry updated_at = '' (empty string, not NULL), and
	// julianday('') is NULL. That made the whole rank expression NULL, and
	// scanning NULL into float64 failed the ENTIRE query — SearchNotes swallows
	// the error and returns no results at all, so one malformed timestamp hid
	// every other match. Substituting 'now' keeps such rows rankable instead of
	// fatal.
	rankExpr := "CASE WHEN title LIKE ? ESCAPE '\\' THEN -10 ELSE -5 END " +
		"+ CASE WHEN COALESCE(pinned, 0) = 1 THEN -20 ELSE 0 END " +
		"+ (julianday('now') - COALESCE(julianday(updated_at), julianday('now'))) * -0.01"

	// Argument order must mirror the SQL text exactly: the rank expression's
	// single title probe comes first (it is written before WHERE), then the
	// title/content probe pair per pattern, then the LIMIT.
	where := make([]string, 0, len(patterns))
	args := make([]any, 0, 1+2*len(patterns)+1)
	args = append(args, "%"+escapeLike(patterns[0])+"%")
	for _, p := range patterns {
		where = append(where, "title LIKE ? ESCAPE '\\' OR content LIKE ? ESCAPE '\\'")
		pat := "%" + escapeLike(p) + "%"
		args = append(args, pat, pat)
	}
	args = append(args, searchResultLimit)

	rows, err := db.Query(
		"SELECT id, project_id, title, content, "+rankExpr+" AS rank "+
			"FROM project_notes WHERE "+strings.Join(where, " OR ")+
			" ORDER BY rank LIMIT ?",
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []SearchHit
	for rows.Next() {
		var h SearchHit
		var content string
		if err := rows.Scan(&h.ID, &h.ProjectID, &h.Title, &content, &h.Rank); err != nil {
			return nil, err
		}
		h.Type = "note"
		h.Snippet = makeSnippet(content, q)
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// SearchAll searches notes and todos, returning unified ranked hits. Notes use
// the FTS5 path (with LIKE fallback) via SearchNotes; todos use the FTS index
// when usable and fall back to LIKE otherwise.
func SearchAll(db *sql.DB, query string) ([]SearchHit, error) {
	hits, err := SearchNotes(db, query)
	if err != nil {
		return nil, err
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return hits, nil
	}
	todoHits, err := searchTodos(db, q)
	if err != nil {
		return nil, err
	}
	return append(hits, todoHits...), nil
}

// searchTodos searches todo titles via FTS5 when usable, else LIKE.
func searchTodos(db *sql.DB, q string) ([]SearchHit, error) {
	if ftsUsable(q) {
		hits, err := searchTodosFTS(db, q)
		if err == nil {
			return hits, nil
		}
	}
	return searchTodosLike(db, q)
}

func searchTodosFTS(db *sql.DB, q string) ([]SearchHit, error) {
	rows, err := db.Query(
		"SELECT t.id, t.project_id, t.title FROM project_todos_fts f "+
			"JOIN project_todos t ON t.id = f.rowid "+
			"WHERE project_todos_fts MATCH ? "+
			"ORDER BY bm25(project_todos_fts), t.sort_order ASC, t.id ASC LIMIT ?",
		escapeFTS(q), searchResultLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []SearchHit
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.ID, &h.ProjectID, &h.Title); err != nil {
			return nil, err
		}
		h.Type = "todo"
		h.Snippet = h.Title
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

func searchTodosLike(db *sql.DB, q string) ([]SearchHit, error) {
	pattern := "%" + escapeLike(q) + "%"
	rows, err := db.Query(
		"SELECT id, project_id, title FROM project_todos WHERE title LIKE ? ESCAPE '\\' ORDER BY sort_order ASC, id ASC LIMIT ?",
		pattern, searchResultLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []SearchHit
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.ID, &h.ProjectID, &h.Title); err != nil {
			return nil, err
		}
		h.Type = "todo"
		h.Snippet = h.Title
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// ftsUsable reports whether the query can be served by the trigram FTS index.
// The trigram tokenizer extracts 3-character sequences, so a term shorter than
// 3 characters cannot match; in that case the caller falls back to LIKE.
func ftsUsable(query string) bool {
	for _, term := range strings.Fields(query) {
		if utf8.RuneCountInString(term) < 3 {
			return false
		}
	}
	return len(strings.Fields(query)) > 0
}

// escapeFTS builds an FTS5 MATCH expression from the query: each
// whitespace-separated term is wrapped as a phrase (double quotes escaped) and
// terms are combined with implicit AND. Wrapping as a phrase neutralises FTS
// special characters (*, :, AND, parentheses, etc.) so user input is treated
// as literal substring terms, not query syntax.
func escapeFTS(query string) string {
	var parts []string
	for _, term := range strings.Fields(query) {
		term = strings.ReplaceAll(term, `"`, `""`)
		parts = append(parts, `"`+term+`"`)
	}
	return strings.Join(parts, " ")
}

// escapeFTSOR builds a relaxed FTS5 expression: English stopwords are dropped,
// terms the trigram tokenizer can never match (<3 chars) are dropped, and CJK
// runs are expanded into overlapping trigrams. Returns "" when fewer than two
// matchable terms remain (a single term's OR is identical to its AND, so
// relaxation would be pointless and would only risk noise).
//
// The CJK expansion is what makes this function reachable for the language the
// corpus is actually written in. Chinese has no spaces, so a whole question
// arrives as ONE term: `看板页面的中英文切换按钮会切换哪些文案`. Wrapped as a phrase
// that demands a verbatim copy of the entire sentence, which no note contains —
// so strict AND returns zero. Before this, the relaxation below then bailed out
// too (`len(parts) < 2`), leaving the query with no path to any result at all.
// Emitting the trigrams the tokenizer actually indexed turns that one dead
// phrase into ten matchable ones; bm25 ranks a note containing five of them
// above a note containing one, which is the ranking we want without shipping a
// dictionary or a CGO segmenter.
func escapeFTSOR(query string) string {
	seen := map[string]struct{}{}
	var parts []string
	for _, term := range strings.Fields(query) {
		for _, t := range expandForFTS(term) {
			if len(parts) >= maxFTSORTerms {
				break
			}
			if searchStopwords[strings.ToLower(t)] {
				continue
			}
			if _, dup := seen[t]; dup {
				continue
			}
			seen[t] = struct{}{}
			parts = append(parts, `"`+strings.ReplaceAll(t, `"`, `""`)+`"`)
		}
	}
	if len(parts) < 2 {
		return ""
	}
	return strings.Join(parts, " OR ")
}

// expandForFTS turns one whitespace-separated term into the units the trigram
// tokenizer can actually match. A non-CJK term is returned unchanged (the
// caller's length and stopword filters still apply to it). A CJK run is
// returned as its overlapping 3-rune windows, because that is precisely the
// granularity trigram indexing stores — so these phrases match exactly when the
// character sequence occurs, and nothing coarser or finer.
//
// Segments shorter than 3 runes are returned unchanged: emitting 1- and 2-rune
// windows would only add phrases the trigram tokenizer can never match.
//
// A term is cut at punctuation first. A Chinese question is mostly punctuation
// ("时态图是什么？为什么需要它？"), and punctuation is not CJK — so the whole
// term failed the CJK test and was emitted as ONE phrase, a run no page
// contains. The relaxation then had a single term to OR and gave up, and the
// answer to a question the library could plainly answer was "no knowledge
// matched". Each punctuation-free segment is judged on its own now: the CJK
// parts expand into trigrams, the rest keeps the old shape.
func expandForFTS(term string) []string {
	segments := splitOnPunctuation(term)
	out := make([]string, 0, len(segments))
	for _, seg := range segments {
		runes := []rune(seg)
		if !isCJKRun(runes) || len(runes) < 3 {
			out = append(out, seg)
			continue
		}
		for i := 0; i+3 <= len(runes); i++ {
			out = append(out, string(runes[i:i+3]))
		}
	}
	if len(out) == 0 {
		return []string{term}
	}
	return out
}

// splitOnPunctuation cuts a term at punctuation and drops the punctuation
// itself.
//
// Punctuation separates words, so keeping it inside a segment only ever makes
// that segment unmatchable. It is dropped rather than indexed: no page stores
// "什么？" without the question mark, so a segment that still contains one
// matches nothing.
//
// The double quote is NOT a separator: FTS5 uses it as the phrase delimiter and
// the callers escape it by doubling, so splitting there would change what the
// escaping means.
//
// Symbols are not separators either: "+" in "C++" is part of the word, and
// splitting it off would leave a fragment that matches everything else in the
// corpus.
func splitOnPunctuation(term string) []string {
	var out []string
	var cur []rune
	for _, r := range term {
		if unicode.IsPunct(r) && r != '"' {
			if len(cur) > 0 {
				out = append(out, string(cur))
				cur = nil
			}
			continue
		}
		cur = append(cur, r)
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}

// isCJKRun reports whether every rune in the slice belongs to a CJK script.
// Mixed terms (e.g. `FastAPI里`) are treated as non-CJK: the ASCII part is
// already tokenised normally, and guessing where the script boundary sits would
// be a segmentation decision this package deliberately does not make.
func isCJKRun(runes []rune) bool {
	if len(runes) == 0 {
		return false
	}
	for _, r := range runes {
		switch {
		case r >= 0x4E00 && r <= 0x9FFF, // CJK Unified Ideographs
			r >= 0x3400 && r <= 0x4DBF, // Extension A
			r >= 0x3040 && r <= 0x30FF, // Kana
			r >= 0xAC00 && r <= 0xD7AF: // Hangul
		default:
			return false
		}
	}
	return true
}

// searchStopwords is a tiny, deterministic English function-word set used ONLY
// for M3-C query relaxation (never to reject a strict AND match, only to avoid
// OR-ing pure noise terms). Deliberately small and not a full thesaurus — the
// goal is recall, not semantics.
var searchStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true,
	"this": true, "from": true, "they": true, "have": true, "were": true,
	"what": true, "when": true, "your": true, "about": true, "into": true,
	"there": true, "these": true, "some": true, "then": true, "than": true,
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

func makeSnippet(content, query string) string {
	// HTML-escape the query for safe insertion into <mark> tags.
	escQuery := html.EscapeString(query)

	// Find the match position in the original (unescaped) content first.
	// We lowercase both for case-insensitive matching, but position is in original text.
	lowerContent := strings.ToLower(content)
	lowerQuery := strings.ToLower(query)
	idx := strings.Index(lowerContent, lowerQuery)

	start := 0
	end := searchSnippetWindow

	if idx >= 0 {
		// Window centered on the match in the original content.
		start = idx - searchSnippetWindow/2
		if start < 0 {
			start = 0
		}
		end = start + searchSnippetWindow
		if end > len(content) {
			end = len(content)
		}
		// Clamp both ends to valid UTF-8 boundaries so we don't split a
		// multi-byte character (common with CJK text where each rune is 3 bytes).
		for end > start && end < len(content) && !utf8.RuneStart(content[end]) {
			end--
		}
		for start < end && !utf8.RuneStart(content[start]) {
			start++
		}
		// Only HTML-escape the window slice, not the full content.
		window := content[start:end]
		escaped := html.EscapeString(window)
		return highlightQuery(escaped, escQuery)
	}

	// No match found: fall back to centering on the start of the content,
	// using only a window and escaping that.
	if len(content) <= searchSnippetWindow {
		escaped := html.EscapeString(content)
		return highlightQuery(escaped, escQuery)
	}
	start = 0
	end = searchSnippetWindow
	for end > start && end < len(content) && !utf8.RuneStart(content[end]) {
		end--
	}
	for start < end && !utf8.RuneStart(content[start]) {
		start++
	}
	window := content[start:end]
	escaped := html.EscapeString(window)
	return highlightQuery(escaped, escQuery)
}

// clampToRuneStart backs an end offset up to the start of the rune containing
// it, so escaped[:n] never ends mid-rune.
func clampToRuneStart(s string, n int) int {
	for n > 0 && n < len(s) && !utf8.RuneStart(s[n]) {
		n--
	}
	return n
}

// highlightQuery wraps all occurrences of query in the snippet with <mark> tags.
// Both snippet and query must already be HTML-escaped.
func highlightQuery(snippet, query string) string {
	if query == "" {
		return snippet
	}
	return strings.ReplaceAll(snippet, query, "<mark>"+query+"</mark>")
}
