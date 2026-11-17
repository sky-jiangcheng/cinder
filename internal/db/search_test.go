package db

import (
	"strings"
	"testing"
)

// -- FTS5 search tests (issue #18) --

func TestSearchNotes_FTS_MatchesContent(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	pid := createTestProject(t, db, "search-project")

	// Create via CreateNoteEx so title is set (FTS indexes title + content).
	if _, err := CreateNoteEx(db, pid, "登录模块修复", "修复登录模块的若干Bug，提升稳定性", "", "other", "manual"); err != nil {
		t.Fatalf("CreateNoteEx: %v", err)
	}
	if _, err := CreateNoteEx(db, pid, "无关内容", "完全不同的主题，关于性能优化", "", "other", "manual"); err != nil {
		t.Fatalf("CreateNoteEx: %v", err)
	}

	// 3-char CJK term is FTS-usable: should match only the first note.
	hits, err := SearchNotes(db, "登录模块")
	if err != nil {
		t.Fatalf("SearchNotes: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit for '登录模块', got %d: %+v", len(hits), hits)
	}
	if hits[0].Title != "登录模块修复" {
		t.Errorf("unexpected hit title: %s", hits[0].Title)
	}
}

func TestSearchNotes_FTS_MultiTermIsAND(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	pid := createTestProject(t, db, "and-project")

	// Doc A contains both terms; Doc B contains only one.
	if _, err := CreateNoteEx(db, pid, "A", "the quick brown fox login flow", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateNoteEx(db, pid, "B", "just the login screen", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}

	hits, err := SearchNotes(db, "login flow")
	if err != nil {
		t.Fatalf("SearchNotes: %v", err)
	}
	// FTS combines phrase terms with implicit AND: only the doc with both
	// 'login' and 'flow' matches.
	if len(hits) != 1 || hits[0].Title != "A" {
		t.Errorf("expected only doc A (both terms), got %d: %+v", len(hits), hits)
	}
}

func TestSearchNotes_FTS_RanksByRelevance(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	pid := createTestProject(t, db, "rank-project")

	// Doc A: term in both title and content (denser match). Doc B: term only in
	// a longer content body. bm25 should rank A above B.
	if _, err := CreateNoteEx(db, pid, "login guide", "login flow details here", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateNoteEx(db, pid, "misc", "some notes about login among many other words", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}

	hits, err := SearchNotes(db, "login")
	if err != nil {
		t.Fatalf("SearchNotes: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits for 'login', got %d", len(hits))
	}
	if hits[0].Title != "login guide" {
		t.Errorf("expected 'login guide' ranked first by bm25, got %s", hits[0].Title)
	}
}

func TestSearchNotes_FTS_SpecialCharsNoError(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	pid := createTestProject(t, db, "special-project")

	if _, err := CreateNoteEx(db, pid, "x", `has "quotes" (and) :stars *`, "", "other", "manual"); err != nil {
		t.Fatal(err)
	}
	// A query with FTS special characters must not error; it is escaped to a
	// literal phrase. 'quotes' is >=3 chars so the FTS path is taken.
	hits, err := SearchNotes(db, `"quotes"`)
	if err != nil {
		t.Fatalf("SearchNotes with special chars errored: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("expected 1 hit for '\"quotes\"', got %d", len(hits))
	}
}

func TestSearchNotes_LIKEFallbackForShortQuery(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	pid := createTestProject(t, db, "short-project")

	// '登录' is 2 chars: trigram cannot match, so search falls back to LIKE.
	if _, err := CreateNoteEx(db, pid, "登录模块修复", "修复登录模块的若干Bug", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}
	hits, err := SearchNotes(db, "登录")
	if err != nil {
		t.Fatalf("SearchNotes: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("expected 1 hit for 2-char '登录' via LIKE fallback, got %d", len(hits))
	}
}

func TestSearchNotes_FTS_IndexStaysInSync(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	pid := createTestProject(t, db, "sync-project")

	n, err := CreateNoteEx(db, pid, "draft", "initial content about networking", "", "other", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := SearchNotes(db, "networking"); err != nil || len(got) != 1 {
		t.Fatalf("before update: expected 1 hit, got %d err=%v", len(got), err)
	}

	// Update content: old term should disappear, new term should appear.
	if err := UpdateNote(db, n.ID, "rewritten content about database"); err != nil {
		t.Fatal(err)
	}
	if got, err := SearchNotes(db, "networking"); err != nil {
		t.Fatalf("after update: %v", err)
	} else if len(got) != 0 {
		t.Errorf("old term 'networking' should no longer match after update, got %d", len(got))
	}
	if got, err := SearchNotes(db, "database"); err != nil {
		t.Fatalf("after update (new term): %v", err)
	} else if len(got) != 1 {
		t.Errorf("new term 'database' should match after update, got %d", len(got))
	}

	// Delete: no hits remain.
	if err := DeleteNote(db, n.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := SearchNotes(db, "database"); err != nil {
		t.Fatalf("after delete: %v", err)
	} else if len(got) != 0 {
		t.Errorf("term should not match after delete, got %d", len(got))
	}
}

func TestSearchAll_NotesAndTodos(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	pid := createTestProject(t, db, "mixed-project")

	if _, err := CreateNoteEx(db, pid, "login note", "describes the login architecture", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateTodo(db, pid, "Implement login screen"); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateTodo(db, pid, "Unrelated task"); err != nil {
		t.Fatal(err)
	}

	hits, err := SearchAll(db, "login")
	if err != nil {
		t.Fatalf("SearchAll: %v", err)
	}
	// One note + one todo should match.
	var noteCount, todoCount int
	for _, h := range hits {
		switch h.Type {
		case "note":
			noteCount++
		case "todo":
			todoCount++
		}
	}
	if noteCount != 1 || todoCount != 1 {
		t.Errorf("expected 1 note + 1 todo, got %d notes + %d todos (all=%+v)", noteCount, todoCount, hits)
	}
}

func TestFTSUsable(t *testing.T) {
	cases := map[string]bool{
		"login":      true,  // 5 chars
		"登录模块":       true,  // 4 runes
		"login flow": true,  // both terms >=3
		"登录":         false, // 2 runes
		"ab":         false, // 2 chars
		"login ab":   false, // one term <3
		"":           false, // empty
		"   ":        false, // whitespace only
	}
	for q, want := range cases {
		if got := ftsUsable(q); got != want {
			t.Errorf("ftsUsable(%q) = %v, want %v", q, got, want)
		}
	}
}

func TestMakeSnippet_NearEndNoPanic(t *testing.T) {
	// content longer than the snippet window with the query near the end:
	// previously panicked with "index out of range" when end == len(escaped).
	content := strings.Repeat("a", 120) + " hello "
	snippet := makeSnippet(content, "hello")
	if !strings.Contains(snippet, "<mark>hello</mark>") {
		t.Errorf("expected query highlighted in snippet, got %q", snippet)
	}
}

// -- M3-C lexical query relaxation (ADR-0012) --

func TestEscapeFTSOR(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"login and flow", `"login" OR "flow"`}, // stopword "and" dropped
		{"kubernetes terraform", `"kubernetes" OR "terraform"`},
		{"login", ""},                      // single term: OR == AND, skip
		{"the and for", ""},                // all stopwords -> nothing to relax
		{`quote a"b`, `"quote" OR "a""b"`}, // double quotes escaped
	}
	for _, c := range cases {
		if got := escapeFTSOR(c.in); got != c.want {
			t.Errorf("escapeFTSOR(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSearchNotes_ORRelaxationWhenANDEmpty(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	pid := createTestProject(t, db, "relax-project")
	// No single doc contains BOTH terms, so the strict AND yields zero.
	if _, err := CreateNoteEx(db, pid, "Deploy", "kubernetes ingress setup guide", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateNoteEx(db, pid, "IaC", "terraform module registry", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}

	// Sanity: AND of both terms alone would be empty.
	if h, _ := searchNotesFTS(db, "kubernetes terraform", escapeFTS("kubernetes terraform")); len(h) != 0 {
		t.Fatalf("expected strict AND to yield 0, got %d", len(h))
	}

	hits, err := SearchNotes(db, "kubernetes terraform")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("OR relaxation expected 2 hits, got %d: %+v", len(hits), hits)
	}
	got := map[string]bool{}
	for _, h := range hits {
		got[h.Title] = true
	}
	if !got["Deploy"] || !got["IaC"] {
		t.Errorf("expected Deploy and IaC via relaxation, got %v", got)
	}
}

func TestSearchNotes_NoRelaxationWhenANDMatches(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	pid := createTestProject(t, db, "precise-project")
	if _, err := CreateNoteEx(db, pid, "both", "the login flow module", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateNoteEx(db, pid, "partial", "only login screen here", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}
	// AND matches "both" -> relaxation must NOT fire (would wrongly add "partial").
	hits, err := SearchNotes(db, "login flow")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Title != "both" {
		t.Fatalf("expected only 'both' (strict AND), got %d: %+v", len(hits), hits)
	}
}

// -- CJK lexical reachability --
// Both regressions below are the same bug seen from two code paths: a Chinese
// question reached db.SearchNotes and returned ZERO rows, so the whole M3-A
// delta measured "CJK is unreachable lexically" rather than "semantics help".

func TestEscapeFTSOR_CJKRunBecomesTrigrams(t *testing.T) {
	// A single CJK term must NOT collapse to "" — that was the dead end.
	got := escapeFTSOR("中英文切换按钮")
	want := `"中英文" OR "英文切" OR "文切换" OR "切换按" OR "换按钮"`
	if got != want {
		t.Errorf("escapeFTSOR(CJK run) = %q, want %q", got, want)
	}

	// Stopword filtering still applies.
	if got := escapeFTSOR("the and for"); got != "" {
		t.Errorf("all-stopword query should still yield \"\", got %q", got)
	}
	// A 2-rune CJK term is below trigram width and cannot match, so it is the
	// only part and relaxation correctly declines.
	if got := escapeFTSOR("登录"); got != "" {
		t.Errorf("sub-trigram CJK term should yield \"\", got %q", got)
	}
	// Mixed terms: the ASCII half is untouched, the CJK half is expanded.
	got = escapeFTSOR("FastAPI 登录模块")
	if got != `"FastAPI" OR "登录模" OR "录模块"` {
		t.Errorf("escapeFTSOR(mixed) = %q", got)
	}
}

// A punctuated Chinese question must still reach the trigram index.
//
// The question mark is not a CJK rune, so the whole term used to fail the CJK
// test and be emitted as a single phrase — a run no page contains. The
// relaxation then had one term to OR, declined, and the answer to a question
// the library could plainly answer was "no knowledge matched".
func TestEscapeFTSOR_PunctuatedCJKQuestionExpandsPerSegment(t *testing.T) {
	got := escapeFTSOR("时态图是什么？为什么需要它？")
	want := `"时态图" OR "态图是" OR "图是什" OR "是什么" OR "为什么" OR "什么需" OR "么需要" OR "需要它"`
	if got != want {
		t.Errorf("escapeFTSOR(punctuated CJK) = %q, want %q", got, want)
	}
}

// The punctuation itself is dropped, and a segment that is not CJK keeps its old
// shape — the mixed-script rule ("do not guess at the boundary") is unchanged.
// The double quote stays inside its segment: FTS5 uses it as the phrase
// delimiter and the callers escape it by doubling.
func TestExpandForFTS_PunctuationSplitsButDoesNotGuessScripts(t *testing.T) {
	got := expandForFTS("FastAPI登录？")
	want := []string{"FastAPI登录"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("expandForFTS = %v, want %v", got, want)
	}
	// The CJK segment expanded into trigrams; the mixed one did not.
	got = expandForFTS("时态图？")
	want = []string{"时态图"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("expandForFTS = %v, want %v", got, want)
	}
	// A term that is only punctuation has no segment to keep.
	if got := expandForFTS("？？"); len(got) != 1 {
		t.Errorf("expandForFTS(punctuation only) = %v", got)
	}
}

func TestIsCJKRun(t *testing.T) {
	cases := map[string]bool{
		"中英文切换":     true,
		"ログイン":      true, // kana
		"한국어":       true, // hangul
		"FastAPI":   false,
		"FastAPI登录": false, // mixed: not guessed at
		"":          false,
	}
	for in, want := range cases {
		if got := isCJKRun([]rune(in)); got != want {
			t.Errorf("isCJKRun(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSearchNotes_LongCJKQuestionFindsNote(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	pid := createTestProject(t, db, "cjk-project")

	if _, err := CreateNoteEx(db, pid, "国际化", "中英文切换按钮已经加好了，页面 lang 属性跟着切换", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateNoteEx(db, pid, "无关", "完全不同的主题，关于性能优化", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}

	// A realistic Chinese question: no spaces, far longer than any note's
	// contiguous run. Pre-fix this returned 0 rows via BOTH the strict AND and
	// the escapeFTSOR bail-out.
	hits, err := SearchNotes(db, "看板页面的中英文切换按钮会切换哪些文案")
	if err != nil {
		t.Fatalf("SearchNotes: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("long CJK question returned no hits — lexical CJK is unreachable again")
	}
	if hits[0].Title != "国际化" {
		t.Errorf("expected '国际化' ranked first, got %s", hits[0].Title)
	}
}

// The same reachability, one character class further: a question that ends in
// punctuation. "？" is not a CJK rune, so the whole term used to be emitted as
// one unmatchable phrase and the answer was "nothing matched".
func TestSearchNotes_PunctuatedCJKQuestionFindsNote(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	pid := createTestProject(t, db, "punctuated-cjk-project")

	if _, err := CreateNoteEx(db, pid, "时态图", "时态图机制在 wiki_pages 旁记录每次审批的变更", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}

	hits, err := SearchNotes(db, "时态图是什么？")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("a punctuated Chinese question returned no hits")
	}
	if hits[0].Title != "时态图" {
		t.Errorf("expected '时态图' ranked first, got %s", hits[0].Title)
	}
}

func TestSearchNotes_MixedQueryWithShortTermFindsNote(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	pid := createTestProject(t, db, "mixed-cjk-project")

	if _, err := CreateNoteEx(db, pid, "health", "app.mount 根路径会遮蔽 health 路由", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}
	// '里' is 1 rune -> ftsUsable false -> the LIKE fallback, which pre-fix
	// matched the whole query as ONE substring and so never matched anything.
	hits, err := SearchNotes(db, "FastAPI 里 health 探测不到")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("mixed query with a 1-rune term returned no hits")
	}
	if hits[0].Title != "health" {
		t.Errorf("expected 'health' ranked first, got %s", hits[0].Title)
	}
}

func TestSearchNotes_LikeFallbackUnchangedWhenMatchExists(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	pid := createTestProject(t, db, "no-regress-project")

	if _, err := CreateNoteEx(db, pid, "exact", "prefix suffix", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateNoteEx(db, pid, "prefix only", "unrelated body", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}
	// The whole query matches doc 1, so OR-relaxation must NOT fire and pull
	// in doc 2 — same "never displace a working answer" rule as the FTS path.
	hits, err := SearchNotes(db, "prefix suffix")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Title != "exact" {
		t.Errorf("expected only the exact match, got %d: %+v", len(hits), hits)
	}
}

func TestSearchNotes_EmptyTimestampDoesNotKillQuery(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	pid := createTestProject(t, db, "bad-ts-project")

	// A note whose updated_at is an empty STRING (not NULL) — the shape an
	// import can produce. julianday('') is NULL, which used to make the whole
	// rank expression NULL; scanning that into float64 failed the query, and
	// SearchNotes turns an error into ZERO results, so this one bad row hid
	// every other match in the corpus.
	if _, err := CreateNoteEx(db, pid, "健康检查", "health 探测不到时的排查", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateNoteEx(db, pid, "另一个", "health 相关的其他说明", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE project_notes SET updated_at = '' WHERE title = '另一个'"); err != nil {
		t.Fatal(err)
	}

	// '里' forces the LIKE path, whose OR-relaxation surfaces BOTH notes — so
	// the bad-timestamp row is actually scanned. Pre-fix that scan failed and
	// SearchNotes reported ZERO results for the whole query.
	hits, err := SearchNotes(db, "里 health 探测不到")
	if err != nil {
		t.Fatalf("SearchNotes must not error on an empty timestamp: %v", err)
	}
	if len(hits) < 2 {
		t.Fatalf("expected both notes despite the empty timestamp, got %d: %+v", len(hits), hits)
	}
}

func TestEscapeFTSOR_CappedForPathologicalInput(t *testing.T) {
	// A pasted paragraph expands to one phrase per trigram, so without a cap
	// 600 distinct runes become 598 OR'd phrases — a MATCH string nobody wants
	// evaluated. A real question stays far below the cap.
	var b strings.Builder
	for r := rune(0x4E00); r < 0x4E00+600; r++ {
		b.WriteRune(r)
	}
	expr := escapeFTSOR(b.String())
	if got := strings.Count(expr, " OR ") + 1; got > maxFTSORTerms {
		t.Errorf("pathological CJK input produced %d OR terms, cap is %d", got, maxFTSORTerms)
	}
	if got := strings.Count(escapeFTSOR("看板页面的中英文切换按钮会切换哪些文案"), " OR ") + 1; got > maxFTSORTerms {
		t.Errorf("a real question produced %d OR terms, over cap %d — cap is too tight", got, maxFTSORTerms)
	}
}

func TestLikeTerms(t *testing.T) {
	cases := map[string][]string{
		"FastAPI 里 health 探测不到": {"FastAPI", "里", "health", "探测不到"},
		"login  login":          {"login"}, // deduped
		"one":                   {"one"},
		"health,":               {"health"}, // punctuation trimmed
	}
	for in, want := range cases {
		got := likeTerms(in)
		if len(got) != len(want) {
			t.Errorf("likeTerms(%q) = %v, want %v", in, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("likeTerms(%q) = %v, want %v", in, got, want)
				break
			}
		}
	}
}

// The evidence channel must not accept a coincidence as a citation.
//
// Reproduced from the live library (2026-11-17): asking "昨日代码提交量" put an
// unrelated ADR under "本次依据" because the ADR mentions "每日代码量标准" — one
// shared trigram ("日代码"). The relaxed OR path exists for the SEARCH surface,
// where the user scans a ranked list and judges it; the evidence channel renders
// every hit as a source, so it must not relax at all. A real CJK match can also
// share a single trigram, which is why no ratio threshold works here — the
// difference is the surface, not the score.
func TestSearchNotesStrictRejectsSingleTrigramCoincidence(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	pid := createTestProject(t, db, "strict-evidence-project")

	// The coincidental note: shares exactly one trigram ("日代码").
	coincidence, err := CreateNoteEx(db, pid, "ADR-0008:", "仪表盘「生产力门面」——目标环 / 每日代码量标准 / 工作日告警。", "", "knowledge", "manual")
	if err != nil {
		t.Fatal(err)
	}
	// The genuinely relevant note. It must NOT contain the whole question:
	// if it did, strict AND would match it and the relaxed branch would never
	// run — the test would pass while probing nothing (verified: the first
	// version of this test did exactly that). A real user question rephrases
	// the note it is looking for.
	relevant, err := CreateNoteEx(db, pid, "提交统计", "代码提交量按仓库聚合，合并提交不计入行数。", "", "knowledge", "manual")
	if err != nil {
		t.Fatal(err)
	}

	question := "昨日代码提交量"

	// The search surface may still surface the coincidence (a hint the user
	// can dismiss); strict must not.
	strict, err := SearchNotesStrict(db, question)
	if err != nil {
		t.Fatalf("SearchNotesStrict: %v", err)
	}
	for _, h := range strict {
		if h.ID == coincidence.ID {
			t.Errorf("a single shared trigram became a citation: %q", h.Title)
		}
	}
	// The relaxed surface still offers both — the hint behaviour is unchanged.
	relaxed, err := SearchNotes(db, question)
	if err != nil {
		t.Fatalf("SearchNotes: %v", err)
	}
	var sawCoincidence bool
	for _, h := range relaxed {
		if h.ID == coincidence.ID {
			sawCoincidence = true
		}
	}
	if !sawCoincidence {
		t.Skip("relaxed path no longer returns the coincidence; the strict assertion still holds")
	}
	_ = relevant
}
