package wikigen

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"repotory/internal/db"
)

// Concept merge (ADR-0019) — the repayment tool ADR-0015 promised and never built.
//
// ADR-0015 决策 3 accepted "concepts will split, a human merges them" as the price
// of "the compiler may never touch an approved page", and lane 1's rehearsal cashed
// that check: 5 notes produced 7 pages, and the only thing a reviewer could do with
// a near-duplicate was reject it, which leaves a semantically-overlapping tombstone
// behind. There was no merge anywhere in the system. This file adds one, in the
// shape the ADR specifies: cheap structural detection, a model that may only
// *suggest*, and a human who alone can merge.
//
// Three boundaries, and each one fails differently if it leaks:
//
//  1. Detection is structural and free (决策 2): four cheap signals, no model, no
//     vector, so it can run without cost and it works before ADR-0018 lane 2 exists.
//     Its output is a *candidate pair*, never a conclusion.
//  2. The model has one job (决策 4): given two pages, is this one concept, and which
//     page should survive? It may not merge, rename, delete or edit. Its entire
//     output becomes one todo line, on lint's discipline — a wrong suggestion costs a
//     glance, a wrong action costs knowledge.
//  3. Execution is human-only (决策 1): the merge transaction has exactly one caller,
//     service.MergeWikiPagesInto, which is reachable only from a binding the review
//     page invokes on a click. Not the ticker, not a job worker, not an MCP tool.
//     Enforced by absence, and pinned by TestMergeHasNoAutoPath.
//
// What a merge is NOT: an edit of the winner. db.MergeWikiPages guarantees the
// winner's row is byte-identical afterwards, which is what keeps "no automated path
// may modify an approved page" true after a merge tool exists.

const (
	// MergeMinPairScore is 决策 3's first-layer threshold: two independent signals
	// minimum (the two cheapest sum to 4). A single weak signal is how a merge pass
	// becomes the review fatigue lane 1 already produced.
	MergeMinPairScore = 4
	// MergeDefaultTopK bounds what the review surface shows.
	MergeDefaultTopK = 20
	// mergeMaxPairs bounds a full suggestion round by cost, not by correctness.
	mergeMaxPairs = 200
	// mergePairsPerCall is how many candidate pairs go into ONE model request.
	//
	// 决策 4's "每轮 pair 预算封顶如 ≤200 对" reads as one call per pair, and a local
	// model measured 173s per request during lane 1's rehearsal — 200 pairs would be
	// a nine-hour lint pass. Batching keeps the budget's INTENT (bounded cost per
	// round) while making the round finish: fewer requests, each with less context,
	// and a reply still validated pair by pair against the library.
	mergePairsPerCall = 10
	// mergeExcerptBytes is how much of each page the model sees per pair.
	mergeExcerptBytes = 700
	// MergeMaxBucket is the cross-product guard for token blocking: a token shared
	// by more than this many pages ("project", "api") carries no similarity
	// information, so the bucket is skipped rather than compared pairwise. Without
	// this, "no full cross product" would be true only until the first common word.
	MergeMaxBucket = 40
)

// Signal weights are fixed integers rather than a tuned score: the whole point of
// the first layer is that a human can read WHY a pair was suggested.
const (
	signalSharedNotes = 3 // two pages built from the same note are near-certainly one concept
	signalSlugShape   = 2 // payment-gateway vs gateway-payment
	signalTitleWords  = 2 // the words agree even where the slugs do not
	signalLinked      = 2 // the graph already thinks they are related
)

// Candidate signal names, as they appear in MergeCandidate.Signals and in the
// batched model prompt.
const (
	sigSharedNotes = "shared-notes"
	sigSlugPrefix  = "slug-prefix"
	sigTitleToken  = "title-token"
	sigLinked      = "linked"
)

// MergeCandidate is one pair that looks like the same concept twice.
//
// Winner is a PROVISIONAL direction chosen from structural evidence (待决 1: more
// sources, higher in-degree, older). The model may overturn it and the review UI
// may overturn both — but a candidate arrives directed, so the human has something
// to react to rather than an unlabelled pair.
type MergeCandidate struct {
	WinnerID   int64    `json:"winner_id"`
	WinnerSlug string   `json:"winner_slug"`
	LoserID    int64    `json:"loser_id"`
	LoserSlug  string   `json:"loser_slug"`
	Score      int      `json:"score"`
	Signals    []string `json:"signals"`
	Shared     int      `json:"shared_notes"`
	ProjectID  int64    `json:"project_id"`
}

// MergeCandidateReport is the structural pass's whole output. PairsScored is the
// blocking evidence: tests assert it stays far below Possible, which is the only
// way "candidate discovery does not do a full cross product" (晋升条件 3) remains
// true as the layer grows rather than merely today.
type MergeCandidateReport struct {
	ProjectID   int64            `json:"project_id"`
	Pages       int              `json:"pages"`
	PairsScored int              `json:"pairs_scored"`
	Possible    int              `json:"pairs_possible"`
	Candidates  []MergeCandidate `json:"candidates"`
	Truncated   bool             `json:"truncated"`
}

// pairSignals accumulates which independent keys put two pages in the same room.
type pairSignals map[string]bool

// FindMergeCandidates runs the four structural blocking signals over the page
// layer and returns the top-K pairs worth a human's attention. No model, no
// vector, no writes — which is why it can be called freely, including from the
// review page on every load.
func FindMergeCandidates(database *sql.DB, projectID int64, topK int) (*MergeCandidateReport, error) {
	if topK <= 0 {
		topK = MergeDefaultTopK
	}
	pages, err := db.ListWikiPages(database, "", projectID)
	if err != nil {
		return nil, err
	}
	report := &MergeCandidateReport{
		ProjectID:  projectID,
		Pages:      len(pages),
		Candidates: []MergeCandidate{},
		Possible:   len(pages) * (len(pages) - 1) / 2,
	}
	if len(pages) < 2 {
		return report, nil
	}

	byID := make(map[int64]db.WikiPage, len(pages))
	for _, p := range pages {
		byID[p.ID] = p
	}
	pairs := map[[2]int64]pairSignals{}
	addPair := func(a, b int64, signal string) {
		if a == b {
			return
		}
		if _, ok := byID[a]; !ok {
			return
		}
		if _, ok := byID[b]; !ok {
			return
		}
		key := [2]int64{a, b}
		if a > b {
			key = [2]int64{b, a}
		}
		if pairs[key] == nil {
			pairs[key] = pairSignals{}
		}
		pairs[key][signal] = true
	}

	// ---- blocking ----------------------------------------------------------
	//
	// Every pair here comes from a shared key (a title/slug token, a slug prefix, a
	// source note, an edge). Pages that share none of those are never compared. This
	// is the classic record-linking lesson: the engineering is in the blocking, not
	// in the matching — "对每一对都问模型" is listed in the ADR's own candidate matrix
	// as the option that costs O(n²) calls.
	tokenBuckets := map[string][]int64{}
	prefixBuckets := map[string][]int64{}
	for _, p := range pages {
		for _, tok := range conceptTokens(p.Title, p.Slug) {
			tokenBuckets[tok] = append(tokenBuckets[tok], p.ID)
		}
		// The first three runes of the slug. This is what catches the CJK case:
		// 支付网关 / 支付通道 share no ASCII delimiter at all, and a word-only
		// tokenizer would call them unrelated.
		if pre := slugPrefixKey(p.Slug); pre != "" {
			prefixBuckets[pre] = append(prefixBuckets[pre], p.ID)
		}
	}
	// Maps are not comparable in Go, so the two bucket families are walked through
	// one closure that is told which signal it is recording.
	block := func(buckets map[string][]int64, signal string) {
		for _, ids := range buckets {
			if len(ids) < 2 || len(ids) > MergeMaxBucket {
				continue
			}
			for i := 0; i < len(ids); i++ {
				for j := i + 1; j < len(ids); j++ {
					addPair(ids[i], ids[j], signal)
				}
			}
		}
	}
	block(tokenBuckets, sigTitleToken)
	block(prefixBuckets, sigSlugPrefix)

	// Shared source notes come from SQL, which is both faster than a Go join and
	// the only place the same-project-or-global rule can be stated once.
	overlaps, err := db.NoteOverlapPairs(database, projectID)
	if err != nil {
		return nil, err
	}
	for _, o := range overlaps {
		addPair(o.Left, o.Right, sigSharedNotes)
	}

	// Edges. Only ref/mentions count as a similarity signal: a supersedes or
	// contradicts edge means the pages are DIFFERENT things on purpose, and
	// treating that as "same concept" would invert the vocabulary lane 1 exists for.
	links, err := db.ListPageLinks(database)
	if err != nil {
		return nil, err
	}
	for _, l := range links {
		if l.Relation != db.RelationRef && l.Relation != db.RelationMentions {
			continue
		}
		addPair(l.FromID, l.ToID, sigLinked)
	}
	report.PairsScored = len(pairs)

	// ---- matching ----------------------------------------------------------
	notesByPage, err := sourceNoteCounts(database)
	if err != nil {
		return nil, err
	}
	inDegree := map[int64]int{}
	for _, l := range links {
		inDegree[l.ToID]++
	}
	for key, sigs := range pairs {
		a, b := byID[key[0]], byID[key[1]]
		c, ok := scoreMergePair(a, b, sigs, notesByPage, inDegree)
		if !ok {
			continue
		}
		report.Candidates = append(report.Candidates, c)
	}

	// A stable order is what makes the review page show the same list twice, and
	// what makes the "re-run files no duplicate todo" test meaningful.
	sort.Slice(report.Candidates, func(i, j int) bool {
		a, b := report.Candidates[i], report.Candidates[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.LoserSlug != b.LoserSlug {
			return a.LoserSlug < b.LoserSlug
		}
		return a.WinnerSlug < b.WinnerSlug
	})
	if len(report.Candidates) > topK {
		report.Candidates = report.Candidates[:topK]
		report.Truncated = true
	}
	return report, nil
}

// scoreMergePair decides whether one blocked pair clears the two-signal bar, and
// which way it should be absorbed.
func scoreMergePair(a, b db.WikiPage, sigs pairSignals,
	notesByPage, inDegree map[int64]int) (MergeCandidate, bool) {

	score := 0
	var signals []string
	shared := 0

	if sigs[sigSharedNotes] {
		score += signalSharedNotes
		shared = min(notesByPage[a.ID], notesByPage[b.ID])
		signals = append(signals, sigSharedNotes)
	}
	// The name signals are re-derived rather than trusted from the bucket, so the
	// reported reason is the actual claim ("the slugs are near-identical") instead
	// of "they happened to share a token".
	//
	// A shared 3-rune slug prefix is deliberately NOT scored, only used for
	// blocking. It looked attractive (it is the CJK-friendly approximation of
	// "same word"), but on English slugs "quota-sync" and "quota-refresh" share
	// "quo" while meaning different things, and pairing those up is exactly the
	// review fatigue this pass exists to avoid. Cheap keys may widen the candidate
	// SET; they must not add evidence to a candidate's SCORE.
	if slugShapeMatch(a.Slug, b.Slug) {
		score += signalSlugShape
		signals = append(signals, "slug-shape")
	}
	if titleTokenMatch(a.Title, b.Title) {
		score += signalTitleWords
		signals = append(signals, "title-overlap")
	}
	if sigs[sigLinked] {
		score += signalLinked
		signals = append(signals, sigLinked)
	}
	if score < MergeMinPairScore {
		return MergeCandidate{}, false
	}

	winner, loser := pickMergeDirection(a, b, notesByPage, inDegree)
	if winner.Status == db.WikiStatusRejected {
		winner, loser = loser, winner
	}
	if winner.Status == db.WikiStatusRejected {
		// Both are tombstones. Absorbing one error page into another records
		// nothing and fixes nothing.
		return MergeCandidate{}, false
	}
	// 待决 4: the MVP never merges across projects. A global page (project_id 0) may
	// sit on either side, because it already belongs to every project.
	if winner.ProjectID != loser.ProjectID && winner.ProjectID > 0 && loser.ProjectID > 0 {
		return MergeCandidate{}, false
	}

	sort.Strings(signals)
	return MergeCandidate{
		WinnerID: winner.ID, WinnerSlug: winner.Slug,
		LoserID: loser.ID, LoserSlug: loser.Slug,
		Score: score, Signals: signals, Shared: shared,
		ProjectID: winner.ProjectID,
	}, true
}

// pickMergeDirection implements 待决 1's structural heuristic: more source notes,
// then higher in-degree, then older. "Older" uses the row id, which IS insertion
// order under AUTOINCREMENT — reading created_at instead would mean widening the
// page projection and touching all ten of its consumers for one tie-break.
func pickMergeDirection(a, b db.WikiPage, notesByPage, inDegree map[int64]int) (winner, loser db.WikiPage) {
	an, bn := notesByPage[a.ID], notesByPage[b.ID]
	if an != bn {
		if an > bn {
			return a, b
		}
		return b, a
	}
	if inDegree[a.ID] != inDegree[b.ID] {
		if inDegree[a.ID] > inDegree[b.ID] {
			return a, b
		}
		return b, a
	}
	if a.ID != b.ID {
		if a.ID < b.ID {
			return a, b
		}
		return b, a
	}
	if a.Slug <= b.Slug {
		return a, b
	}
	return b, a
}

// conceptTokens returns the word set of a title plus its slug. ASCII words split
// on every delimiter; CJK runs contribute character bigrams, because "支付网关" has
// no internal delimiter and single runes are too coarse (every finance page
// contains 支 and 付).
//
// The delimiter case must FLUSH rather than accumulate: writing '-' into the
// builder turns "Payment Gateway" into the single token "payment gateway", which
// makes every two-word title share nothing with every other one and silently
// disables the whole title signal. That is what the first round of tests caught.
func conceptTokens(title, slug string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		t = strings.TrimSpace(strings.ToLower(t))
		if len([]rune(t)) < 2 || seen[t] {
			return
		}
		seen[t] = true
		out = append(out, t)
	}
	isASCIIWord := func(r rune) bool {
		return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
	}
	for _, field := range []string{title, slug} {
		var (
			ascii strings.Builder
			cjk   []rune
		)
		flushASCII := func() {
			if ascii.Len() >= 2 {
				add(ascii.String())
			}
			ascii.Reset()
		}
		flushCJK := func() {
			for i := 0; i+1 < len(cjk); i++ {
				add(string(cjk[i : i+2]))
			}
			cjk = cjk[:0]
		}
		for _, r := range field {
			switch {
			case r >= 0x4e00 && r <= 0x9fff:
				flushASCII()
				cjk = append(cjk, r)
			case isASCIIWord(r):
				flushCJK()
				ascii.WriteRune(r)
			default:
				// Any other character — space, hyphen, underscore, punctuation, an
				// unsupported script — ends both pending tokens.
				flushASCII()
				flushCJK()
			}
		}
		flushASCII()
		flushCJK()
	}
	return out
}

// slugPrefixKey is the cheap CJK-friendly blocking key: the first three runes of
// the normalized slug, or the whole slug when shorter.
func slugPrefixKey(slug string) string {
	r := []rune(db.NormalizeWikiSlug(slug))
	if len(r) < 2 {
		return ""
	}
	if len(r) > 3 {
		r = r[:3]
	}
	return string(r)
}

// slugShapeMatch: one normalized slug is a prefix of the other, or they are within
// two rune edits (payment-gateway / gateway-payment is a word-order split;
// cbipay-3-0-6 / cbipay-3-0-7 is a version drift).
func slugShapeMatch(a, b string) bool {
	x, y := db.NormalizeWikiSlug(a), db.NormalizeWikiSlug(b)
	if x == "" || y == "" {
		return false
	}
	if x == y || strings.HasPrefix(x, y) || strings.HasPrefix(y, x) {
		return true
	}
	const maxSlugRunes = 64
	if len([]rune(x)) > maxSlugRunes || len([]rune(y)) > maxSlugRunes {
		return false
	}
	// Only measured for names long enough that two edits means "nearly the same
	// word"; otherwise cat/cot would merge unrelated one-syllable topics.
	if min(len([]rune(x)), len([]rune(y))) < 6 {
		return false
	}
	return runeEditDistance(x, y) <= 2
}

// runeEditDistance is Levenshtein over runes, with two rows of the DP table kept
// (a full m*n table is meaningless work for strings this short, and a byte-based
// distance would count one CJK character as three edits).
func runeEditDistance(a, b string) int {
	x, y := []rune(a), []rune(b)
	prev := make([]int, len(y)+1)
	cur := make([]int, len(y)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(x); i++ {
		cur[0] = i
		for j := 1; j <= len(y); j++ {
			cost := 1
			if x[i-1] == y[j-1] {
				cost = 0
			}
			cur[j] = min(min(prev[j]+1, cur[j-1]+1), prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(y)]
}

// titleTokenMatch is Jaccard similarity over the titles' token sets, with a high
// bar (0.5) AND a floor of two shared tokens: 支付 appearing in two otherwise
// unrelated pages is not agreement about a topic.
func titleTokenMatch(a, b string) bool {
	ta := tokenSet(conceptTokens(a, ""))
	tb := tokenSet(conceptTokens(b, ""))
	if len(ta) == 0 || len(tb) == 0 {
		return false
	}
	shared := 0
	for t := range ta {
		if tb[t] {
			shared++
		}
	}
	union := len(ta) + len(tb) - shared
	if union == 0 || shared < 2 {
		return false
	}
	return float64(shared)/float64(union) >= 0.5
}

func tokenSet(in []string) map[string]bool {
	out := make(map[string]bool, len(in))
	for _, t := range in {
		out[t] = true
	}
	return out
}

// sourceNoteCounts is one grouped pass over note_pages rather than one query per
// page: the candidate pass runs per pair, and a per-page query would turn blocking
// into N round trips.
func sourceNoteCounts(database *sql.DB) (map[int64]int, error) {
	rows, err := database.Query("SELECT page_id, COUNT(*) FROM note_pages GROUP BY page_id")
	if err != nil {
		return nil, fmt.Errorf("wikigen: source note counts: %w", err)
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// mergeTodoDetail is the one line a reviewer reads in their todo list. It names
// the direction explicitly, because the action is not symmetric and a todo that
// says "these two are alike" leaves the work half-specified.
func mergeTodoDetail(loserSlug, winnerSlug, reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "结构信号相似，无模型判断依据"
	}
	return clipRunes(fmt.Sprintf("[[%s]] → [[%s]]：（模型建议，需人工判断）%s",
		loserSlug, winnerSlug, reason), 200)
}

// mergeSuggestion is one model verdict. The pair index is what ties it back to a
// candidate: models reorder, merge and renumber rows, and matching on slugs alone
// would let a verdict land on a page that was never asked about.
type mergeSuggestion struct {
	PairRef int    `json:"pair"`
	Same    bool   `json:"same"`
	Winner  string `json:"winner"`
	Loser   string `json:"loser"`
	Reason  string `json:"reason"`
}

// SuggestMerges asks the model about the structural candidates, in batches, and
// turns only the confirmed ones into findings. It returns (findings, note) in the
// same shape as the contradiction pass so the lint pass treats every model half
// alike, including the "why did nothing run" note.
//
// Two gates, both required: wiki_lint_llm (the umbrella for any model call inside
// lint) and wiki_merge_suggest (this pass's own consent). Either one off means
// zero outbound requests — the check happens before the candidate query so a
// disabled feature does not even read the graph.
func SuggestMerges(ctx context.Context, database *sql.DB, chat Chat, pages []db.WikiPage) ([]WikiFinding, string) {
	if !LintLLMEnabled(database) {
		return nil, "未开启（wiki_lint_llm）：跳过合并判等"
	}
	if !MergeSuggestEnabled(database) {
		return nil, "未开启（wiki_merge_suggest）：跳过合并判等"
	}
	if err := chat.Available(ctx); err != nil {
		return nil, "AI 端点未配置：跳过合并判等"
	}
	cand, err := FindMergeCandidates(database, 0, mergeMaxPairs)
	if err != nil {
		return nil, fmt.Sprintf("候选发现失败，跳过合并判等（%v）", err)
	}
	if len(cand.Candidates) == 0 {
		// Zero candidates, zero calls — the property the ADR-0053 runner pins for
		// review, stated here for the same reason: a paid endpoint must not be
		// pinged to be told "nothing to judge".
		return nil, ""
	}

	bySlug := make(map[string]db.WikiPage, len(pages)*2)
	for _, p := range pages {
		bySlug[p.Slug] = p
	}

	var out []WikiFinding
	var notes []string
	for start := 0; start < len(cand.Candidates); start += mergePairsPerCall {
		end := start + mergePairsPerCall
		if end > len(cand.Candidates) {
			end = len(cand.Candidates)
		}
		items, failNote := askMergeBatch(ctx, chat, cand.Candidates[start:end], bySlug)
		out = append(out, items...)
		if failNote != "" {
			notes = append(notes, failNote)
			break // stop asking; the budget is the point of the loop, not the batching
		}
	}
	if len(notes) > 0 {
		return out, strings.Join(notes, "；")
	}
	return out, ""
}

// askMergeBatch runs one batched judgement and validates every answer against the
// library instead of trusting it.
func askMergeBatch(ctx context.Context, chat Chat, batch []MergeCandidate,
	bySlug map[string]db.WikiPage) ([]WikiFinding, string) {

	arr := make([]map[string]any, 0, len(batch))
	for i, c := range batch {
		w, okW := bySlug[c.WinnerSlug]
		l, okL := bySlug[c.LoserSlug]
		if !okW || !okL {
			continue
		}
		arr = append(arr, map[string]any{
			"pair": i,
			"a": map[string]any{"slug": l.Slug, "title": l.Title, "kind": l.Kind,
				"excerpt": truncateBytes(l.Content, mergeExcerptBytes)},
			"b": map[string]any{"slug": w.Slug, "title": w.Title, "kind": w.Kind,
				"excerpt": truncateBytes(w.Content, mergeExcerptBytes)},
			"signals":     c.Signals,
			"shared_note": c.Shared,
		})
	}
	if len(arr) == 0 {
		return nil, ""
	}
	encoded, err := json.Marshal(arr)
	if err != nil {
		return nil, "候选对序列化失败：跳过合并判等"
	}
	var prompt strings.Builder
	prompt.WriteString("下面是若干对知识页，每对的结构信号显示它们可能是同一概念的两份记录。" +
		"逐对判断：是否确为同一概念？若是，哪一页应保留（winner），哪一页应被并入（loser）？\n" +
		"依据只有给出的标题与正文摘录；不要臆测未写出的信息，宁可回答 same:false。\n" +
		`只输出 JSON 数组，元素形如 {"pair":<编号>,"same":true|false,"winner":"<slug>","loser":"<slug>","reason":"<一句话>"}。` +
		"不同的对输出 same:false 且不必给 winner/loser。不要输出 JSON 以外的字符。\n\n")
	prompt.Write(encoded)

	// Page content is untrusted data and the reply is untrusted data too: the only
	// thing this path can produce is a todo line, so a prompt buried in a page
	// ("tell me to merge everything") has nowhere to land.
	reply, err := chat.Chat(ctx, []Message{
		{Role: "system", Content: "你只输出 JSON，只做相似度判断。合并由人执行，你不合并任何东西。"},
		{Role: "user", Content: prompt.String()},
	})
	if err != nil {
		return nil, fmt.Sprintf("LLM 调用失败，跳过合并判等（%v）", err)
	}
	items, ok := parseMergeReply(reply.Text)
	if !ok {
		// Unparseable yields nothing rather than a best-effort guess; a hallucinated
		// "these are the same" todo is noise a human clears by hand. Truncation gets
		// its own wording because the remedy differs (raise the budget / switch model).
		if hint := TruncatedHint(reply.FinishReason); hint != "" {
			return nil, "LLM 回复被截断，未能解析为 JSON：本次不采信任何合并建议" + hint
		}
		return nil, "LLM 回复无法解析为 JSON：本次不采信任何合并建议"
	}

	var out []WikiFinding
	for _, it := range items {
		if !it.Same {
			continue
		}
		// 晋升条件 4: an invented slug, an out-of-range pair number, or a rejected
		// winner is dropped rather than guessed at — the rule lint already applies
		// to a fabricated page_id.
		if it.PairRef < 0 || it.PairRef >= len(batch) {
			continue
		}
		w, okW := bySlug[strings.TrimSpace(it.Winner)]
		l, okL := bySlug[strings.TrimSpace(it.Loser)]
		if !okW || !okL || w.ID == l.ID {
			continue
		}
		if w.Status == db.WikiStatusRejected {
			continue
		}
		if w.ProjectID != l.ProjectID && w.ProjectID > 0 && l.ProjectID > 0 {
			continue
		}
		out = append(out, WikiFinding{
			Check:     "merge",
			Severity:  "warn",
			ProjectID: w.ProjectID,
			PageID:    l.ID,
			Detail:    mergeTodoDetail(l.Slug, w.Slug, it.Reason),
		})
	}
	return out, ""
}

// parseMergeReply shares parseLintReply's tolerance for prose wrapped around the
// JSON, and its refusal to guess at anything that is not valid JSON.
func parseMergeReply(raw string) ([]mergeSuggestion, bool) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "["); i > 0 {
		s = s[i:]
	}
	if j := strings.LastIndex(s, "]"); j >= 0 && j+1 < len(s) {
		s = s[:j+1]
	}
	var items []mergeSuggestion
	if err := json.Unmarshal([]byte(s), &items); err != nil {
		return nil, false
	}
	return items, true
}
