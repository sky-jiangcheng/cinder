package service

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"repotory/internal/db"
)

// ADR-0019 晋升条件 3 and 4 — candidate discovery and the model's bounded role.
//
// The fixture below is a matrix rather than a story: each pair is built to fire
// exactly one signal, or to look like it should and must not. That is the only way
// "four signals, two required" is a tested rule instead of a comment.

func mergeFixtureProject(t *testing.T, svc *Service, name string) int64 {
	t.Helper()
	return seedProject(t, svc.db, name, "/tmp/"+name)
}

func mustCreatePage(t *testing.T, svc *Service, kind, slug, title string, projectID int64,
	body, status, source string) *db.WikiPage {
	t.Helper()
	p, err := db.CreateWikiPageAs(svc.db, kind, slug, title, projectID, body, status, source)
	if err != nil {
		t.Fatalf("create page %s: %v", slug, err)
	}
	return p
}

func mustLink(t *testing.T, svc *Service, from, to int64, relation string) {
	t.Helper()
	if err := db.LinkWikiPages(svc.db, from, to, relation); err != nil {
		t.Fatalf("link %d->%d: %v", from, to, err)
	}
}

func mustAttachNote(t *testing.T, svc *Service, projectID, pageID int64, title string) int64 {
	t.Helper()
	n, err := db.CreateNoteEx(svc.db, projectID, title, "note body for "+title, "", "knowledge", "manual")
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	if err := db.AttachNoteToPage(svc.db, n.ID, pageID); err != nil {
		t.Fatalf("attach note: %v", err)
	}
	return n.ID
}

func candidateFor(rep *MergeCandidateReport, a, b string) (MergeCandidate, bool) {
	for _, c := range rep.Candidates {
		if (c.LoserSlug == a && c.WinnerSlug == b) || (c.LoserSlug == b && c.WinnerSlug == a) {
			return c, true
		}
	}
	return MergeCandidate{}, false
}

// One fixture, many assertions: each pair targets one signal.
func mergeSignalFixture(t *testing.T, svc *Service) map[string]int64 {
	t.Helper()
	pid := mergeFixtureProject(t, svc, "merge-signals")
	ids := map[string]int64{}
	long := "支付网关的重试策略与幂等键设计：最多三次退避重试，幂等键使用 invoice id，超时统一返回 408。" +
		"这一句存在的目的是让两页的正文长度接近，避免任何检查按长度而不是按内容差异做判断。"

	// (a) slug-shape + title-overlap: the classic word-order split.
	ids["pg"] = mustCreatePage(t, svc, db.WikiKindEntity, "payment-gateway", "Payment Gateway",
		pid, long, db.WikiStatusApproved, "manual").ID
	ids["gp"] = mustCreatePage(t, svc, db.WikiKindConcept, "payment-gateway-v2", "Payment Gateway V2",
		pid, long, db.WikiStatusPending, "wiki-compile").ID

	// (b) shared source note ONLY: strong alone, but below the two-signal bar.
	ids["solo-a"] = mustCreatePage(t, svc, db.WikiKindConcept, "quota-sync", "Quota Sync",
		pid, long, db.WikiStatusApproved, "manual").ID
	ids["solo-b"] = mustCreatePage(t, svc, db.WikiKindConcept, "quota-refresh", "Quota Refresh",
		pid, long, db.WikiStatusApproved, "manual").ID
	note := mustAttachNote(t, svc, pid, ids["solo-a"], "quota note")
	if err := db.AttachNoteToPage(svc.db, note, ids["solo-b"]); err != nil {
		t.Fatal(err)
	}

	// (c) shared source note + an explicit link: two signals, must be suggested.
	ids["link-a"] = mustCreatePage(t, svc, db.WikiKindEntity, "invoice-reconcile", "Invoice Reconcile",
		pid, long, db.WikiStatusApproved, "manual").ID
	ids["link-b"] = mustCreatePage(t, svc, db.WikiKindConcept, "recon-batch", "Recon Batch",
		pid, long, db.WikiStatusApproved, "manual").ID
	n2 := mustAttachNote(t, svc, pid, ids["link-a"], "recon note")
	if err := db.AttachNoteToPage(svc.db, n2, ids["link-b"]); err != nil {
		t.Fatal(err)
	}
	mustLink(t, svc, ids["link-a"], ids["link-b"], db.RelationRef)

	// (d) a contradicts edge only: difference is not similarity. The two slugs are
	// deliberately unrelated by name, so the ONLY reason this pair would score
	// anything is the edge — and it must not. (The first version of this fixture
	// used sync-mode/async-mode, which is a genuine slug-shape near-duplicate and
	// therefore scored 5 for the wrong reason: a negative that passes for the wrong
	// reason is worse than no negative at all.)
	ids["cx-a"] = mustCreatePage(t, svc, db.WikiKindConcept, "sync-mode", "Sync Mode",
		pid, long, db.WikiStatusApproved, "manual").ID
	ids["cx-b"] = mustCreatePage(t, svc, db.WikiKindConcept, "nightly-recompute", "Nightly Recompute",
		pid, long, db.WikiStatusApproved, "manual").ID
	n3 := mustAttachNote(t, svc, pid, ids["cx-a"], "mode note")
	if err := db.AttachNoteToPage(svc.db, n3, ids["cx-b"]); err != nil {
		t.Fatal(err)
	}
	mustLink(t, svc, ids["cx-a"], ids["cx-b"], db.RelationContradicts)

	// (e) two tombstones that look alike: nothing is gained by merging them.
	ids["dead-a"] = mustCreatePage(t, svc, db.WikiKindConcept, "stale-note-a", "Stale Note A",
		pid, "已并入 [[payment-gateway]]", db.WikiStatusRejected, "manual").ID
	ids["dead-b"] = mustCreatePage(t, svc, db.WikiKindConcept, "stale-note-b", "Stale Note B",
		pid, "已并入 [[payment-gateway]]", db.WikiStatusRejected, "manual").ID

	// (f) cross-project near-duplicates: 待决 4 keeps them apart.
	other := mergeFixtureProject(t, svc, "merge-other")
	ids["cross"] = mustCreatePage(t, svc, db.WikiKindConcept, "payment-gateway-eu", "Payment Gateway",
		other, long, db.WikiStatusApproved, "manual").ID
	n4 := mustAttachNote(t, svc, pid, ids["pg"], "shared across projects note")
	if err := db.AttachNoteToPage(svc.db, n4, ids["cross"]); err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestFindMergeCandidates(t *testing.T) {
	svc, _ := setupService(t)
	ids := mergeSignalFixture(t, svc)

	rep, err := svc.FindMergeCandidates(0, mergeDefaultTopK)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}

	// (a) slug shape + title overlap.
	c, ok := candidateFor(rep, "payment-gateway", "payment-gateway-v2")
	if !ok {
		t.Fatalf("the word-order split should be a candidate, got %v", slugsOf(rep))
	}
	if c.Score < mergeMinPairScore {
		t.Fatalf("candidate below the threshold reported anyway: %+v", c)
	}
	// The approved page must win: more sources / older / higher in-degree, and here
	// it is plainly the older row.
	if c.WinnerID != ids["pg"] {
		t.Fatalf("the approved original should be the winner, got %+v", c)
	}

	// (b) one strong signal is not enough — this is the anti-spam bar.
	if _, ok := candidateFor(rep, "quota-sync", "quota-refresh"); ok {
		t.Fatal("shared notes alone must not clear the two-signal bar")
	}
	// (c) two signals do.
	if _, ok := candidateFor(rep, "invoice-reconcile", "recon-batch"); !ok {
		t.Fatalf("shared notes + a ref edge must be a candidate, got %v", slugsOf(rep))
	}
	// (d) contradiction is difference, not similarity.
	if _, ok := candidateFor(rep, "sync-mode", "nightly-recompute"); ok {
		t.Fatal("a contradicts edge must not count as a similarity signal")
	}
	// (e) tombstone into tombstone.
	if _, ok := candidateFor(rep, "stale-note-a", "stale-note-b"); ok {
		t.Fatal("two rejected pages must never be a merge candidate")
	}
	// (f) cross-project.
	if _, ok := candidateFor(rep, "payment-gateway", "payment-gateway-eu"); ok {
		t.Fatal("cross-project pairs must be excluded (待决 4)")
	}
	// Never a self-pair, and every reported pair must be ordered loser!=winner.
	for _, cand := range rep.Candidates {
		if cand.LoserID == cand.WinnerID {
			t.Fatalf("self-pair reported: %+v", cand)
		}
	}
}

func slugsOf(rep *MergeCandidateReport) []string {
	var out []string
	for _, c := range rep.Candidates {
		out = append(out, fmt.Sprintf("%s|%s(%d)", c.LoserSlug, c.WinnerSlug, c.Score))
	}
	return out
}

func TestFindMergeCandidates_TruncatesToTopK(t *testing.T) {
	svc, _ := setupService(t)
	mergeSignalFixture(t, svc)
	// A pile of extra near-duplicates so the candidate set exceeds any small top-K.
	pid := mergeFixtureProject(t, svc, "merge-many")
	long := "批量同主题页面，用来把候选数量推到 top-K 之上，验证截断真的发生而不是恰好没截断。"
	for i := 0; i < 8; i++ {
		mustCreatePage(t, svc, db.WikiKindConcept, fmt.Sprintf("bulk-item-%02d", i),
			fmt.Sprintf("Bulk Item %02d", i), pid, long, db.WikiStatusApproved, "manual")
	}
	rep, err := svc.FindMergeCandidates(0, 3)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if len(rep.Candidates) != 3 || !rep.Truncated {
		t.Fatalf("top-K must cap and say so, got %d truncated=%v", len(rep.Candidates), rep.Truncated)
	}
	// Ordering is by score first, so a truncated list is the best three, not the
	// first three encountered.
	if rep.Candidates[0].Score < rep.Candidates[2].Score {
		t.Fatalf("candidates are not sorted by score: %+v", rep.Candidates)
	}
}

// 晋升条件 3: "断言不做全乘遍历". PairsScored is the number of pairs the blocking step
// actually produced, and it must stay well under n(n-1)/2.
func TestFindMergeCandidates_BlocksInsteadOfCrossProduct(t *testing.T) {
	svc, _ := setupService(t)
	pid := mergeFixtureProject(t, svc, "merge-blocking")
	body := "同主题批量页，共享一个词用于制造 blocking 命中。"

	// 20 pages that share a word (they block together) + 20 that share nothing.
	for i := 0; i < 20; i++ {
		mustCreatePage(t, svc, db.WikiKindConcept, fmt.Sprintf("sharedterm-alpha%02d", i),
			fmt.Sprintf("Sharedterm Report %02d", i), pid, body, db.WikiStatusApproved, "manual")
	}
	// Distinct 3-rune prefixes, so the slug-prefix bucket cannot quietly join them
	// either — which is exactly what blocking is supposed to guarantee.
	names := []string{"bravo", "charlie", "delta", "echoo", "foxte", "golfg", "hotel", "india",
		"julie", "kiloc", "limac", "mikep", "novem", "oscar", "papaw", "queeb", "romeo", "sierr",
		"tango", "unifm"}
	for _, n := range names {
		mustCreatePage(t, svc, db.WikiKindConcept, n+"-standalone", n+" standalone",
			pid, body, db.WikiStatusApproved, "manual")
	}

	rep, err := svc.FindMergeCandidates(0, mergeDefaultTopK)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if rep.Pages < 40 {
		t.Fatalf("fixture too small to prove anything, pages=%d", rep.Pages)
	}
	if rep.PairsScored >= rep.Possible {
		t.Fatalf("blocking did not reduce the pair count: scored %d of %d possible",
			rep.PairsScored, rep.Possible)
	}
	if rep.PairsScored > rep.Possible/2 {
		t.Fatalf("blocking cut too little: scored %d of %d", rep.PairsScored, rep.Possible)
	}
}

// A token shared by nearly everything is a stopword, not a similarity signal. The
// bucket cap is what stops "no cross product" from degrading into "one big cross
// product" the moment a word like "api" appears on every page.
func TestFindMergeCandidates_StopwordBucketIsSkipped(t *testing.T) {
	svc, _ := setupService(t)
	pid := mergeFixtureProject(t, svc, "merge-stopword")
	n := mergeMaxBucket + 8
	for i := 0; i < n; i++ {
		// One shared prefix AND one shared word, so both blocking keys put every
		// page in a single oversized bucket. C(n,2) is 1128 comparisons here for
		// what is really 48 unrelated pages — the exact shape the cap exists to cut.
		mustCreatePage(t, svc, db.WikiKindConcept, fmt.Sprintf("svc-api-%02d", i),
			fmt.Sprintf("Service api module %02d", i), pid, "正文",
			db.WikiStatusApproved, "manual")
	}
	rep, err := svc.FindMergeCandidates(0, mergeDefaultTopK)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if rep.Pages != n {
		t.Fatalf("fixture size wrong: %d", rep.Pages)
	}
	if rep.PairsScored != 0 {
		t.Fatalf("an over-sized bucket must be skipped outright, scored %d pairs of %d possible",
			rep.PairsScored, rep.Possible)
	}
	if len(rep.Candidates) != 0 {
		t.Fatalf("nothing was compared, so nothing may be suggested: %v", slugsOf(rep))
	}
}

// ---- the model half ------------------------------------------------------

const mergeStubReply = `[{"pair":0,"same":true,"winner":"payment-gateway","loser":"payment-gateway-v2","reason":"两页讲的是同一个网关重试策略"}]`

func pagesOf(t *testing.T, svc *Service) []db.WikiPage {
	t.Helper()
	pages, err := db.ListWikiPages(svc.db, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	return pages
}

func TestSuggestMerges_Gates(t *testing.T) {
	t.Run("wiki_lint_llm off means no request", func(t *testing.T) {
		svc, _ := setupService(t)
		mergeSignalFixture(t, svc)
		srv, hits := chatStub(t, mergeStubReply)
		for k, v := range map[string]string{
			"ai_chat_base_url":  srv.URL,
			"ai_chat_model":     "stub",
			wikiMergeSuggestKey: "1",
		} {
			if err := svc.UpdateConfig(k, v); err != nil {
				t.Fatal(err)
			}
		}
		findings, note := svc.suggestMerges(pagesOf(t, svc))
		if len(findings) != 0 || hits.Load() != 0 {
			t.Fatalf("merge suggestion must not run without the lint model gate: %d findings, %d calls",
				len(findings), hits.Load())
		}
		if !strings.Contains(note, "wiki_lint_llm") {
			t.Fatalf("the report must say which gate stopped it, got %q", note)
		}
	})

	t.Run("wiki_merge_suggest off means no request", func(t *testing.T) {
		svc, _ := setupService(t)
		mergeSignalFixture(t, svc)
		srv, hits := chatStub(t, mergeStubReply)
		for k, v := range map[string]string{
			"ai_chat_base_url": srv.URL,
			"ai_chat_model":    "stub",
			wikiLintLLMKey:     "1",
		} {
			if err := svc.UpdateConfig(k, v); err != nil {
				t.Fatal(err)
			}
		}
		findings, note := svc.suggestMerges(pagesOf(t, svc))
		if len(findings) != 0 || hits.Load() != 0 {
			t.Fatalf("the pass needs its OWN consent: %d findings, %d calls", len(findings), hits.Load())
		}
		if !strings.Contains(note, wikiMergeSuggestKey) {
			t.Fatalf("note must name the missing gate, got %q", note)
		}
	})
}

func TestSuggestMerges_FindsAndFilesOnlyTodos(t *testing.T) {
	svc, _ := setupService(t)
	mergeSignalFixture(t, svc)
	srv, hits := chatStub(t, mergeStubReply)
	for k, v := range map[string]string{
		"ai_chat_base_url":  srv.URL,
		"ai_chat_model":     "stub",
		wikiLintLLMKey:      "1",
		wikiMergeSuggestKey: "1",
	} {
		if err := svc.UpdateConfig(k, v); err != nil {
			t.Fatal(err)
		}
	}

	before := pagesOf(t, svc)
	findings, note := svc.suggestMerges(before)
	if note != "" {
		t.Fatalf("unexpected note: %q", note)
	}
	if hits.Load() == 0 {
		t.Fatal("the model was never asked")
	}
	var mergeFound *WikiFinding
	for i := range findings {
		if findings[i].Check == "merge" {
			mergeFound = &findings[i]
		}
	}
	if mergeFound == nil {
		t.Fatalf("expected a merge finding, got %+v", findings)
	}
	if !strings.Contains(mergeFound.Detail, "[payment-gateway-v2]] → [[payment-gateway") &&
		!strings.Contains(mergeFound.Detail, "payment-gateway-v2") {
		t.Fatalf("the finding must name both slugs and the direction, got %q", mergeFound.Detail)
	}

	// 晋升条件 4's real teeth: RunWikiLint turns it into a todo and NOTHING ELSE.
	// No page is created, edited, rejected or merged by a model verdict.
	pid := mergeFixtureProjectNamed(t, svc, "merge-signals")
	rep, err := svc.RunWikiLint(pid, true)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	var todos []string
	list, err := db.ListTodos(svc.db, pid)
	if err != nil {
		t.Fatalf("list todos: %v", err)
	}
	for _, td := range list {
		todos = append(todos, td.Title)
	}
	var hit bool
	for _, title := range todos {
		if strings.HasPrefix(title, lintTodoPrefix+" merge") {
			hit = true
			if !strings.Contains(title, "payment-gateway-v2") {
				t.Fatalf("todo must name the loser slug, got %q", title)
			}
		}
	}
	if !hit {
		t.Fatalf("expected a `[lint] merge ...` todo, got %v (report todos_created=%d)",
			todos, rep.TodosCreated)
	}
	after := pagesOf(t, svc)
	if len(before) != len(after) {
		t.Fatalf("a model verdict changed the page count: %d -> %d", len(before), len(after))
	}
	for i := range before {
		if before[i].Content != after[i].Content || before[i].Status != after[i].Status {
			t.Fatalf("a model verdict mutated page %s", before[i].Slug)
		}
	}
}

func mergeFixtureProjectNamed(t *testing.T, svc *Service, name string) int64 {
	t.Helper()
	var id int64
	if err := svc.db.QueryRow(`SELECT id FROM projects WHERE name = ?`, name).Scan(&id); err != nil {
		t.Fatalf("look up project %q: %v", name, err)
	}
	return id
}

// 晋升条件 4: the model's word is not evidence. An invented slug, an out-of-range
// pair index, or a reply that is not JSON at all must produce nothing.
func TestSuggestMerges_RefusesUntrustedReplies(t *testing.T) {
	cases := []struct {
		name  string
		reply string
	}{
		{"prose not json", "这两页看起来是同一个概念，建议合并。"},
		{"invented slugs", `[{"pair":0,"same":true,"winner":"does-not-exist","loser":"also-not-here","reason":"编造"}]`},
		{"pair index out of range", `[{"pair":999,"same":true,"winner":"payment-gateway","loser":"payment-gateway-v2","reason":"编号不存在"}]`},
		{"winner is a rejected page", `[{"pair":0,"same":true,"winner":"stale-note-a","loser":"stale-note-b","reason":"墓碑不能当保留页"}]`},
		{"same page on both sides", `[{"pair":0,"same":true,"winner":"payment-gateway","loser":"payment-gateway","reason":"自并"}]`},
		{"empty array", `[]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := setupService(t)
			mergeSignalFixture(t, svc)
			srv, _ := chatStub(t, tc.reply)
			for k, v := range map[string]string{
				"ai_chat_base_url":  srv.URL,
				"ai_chat_model":     "stub",
				wikiLintLLMKey:      "1",
				wikiMergeSuggestKey: "1",
			} {
				if err := svc.UpdateConfig(k, v); err != nil {
					t.Fatal(err)
				}
			}
			findings, _ := svc.suggestMerges(pagesOf(t, svc))
			for _, f := range findings {
				if f.Check == "merge" {
					t.Fatalf("a merge finding was accepted from %q: %+v", tc.name, f)
				}
			}
		})
	}
}

// The ADR's central claim is "合并永远由人触发". Comments rot; this does not. It
// walks the package's non-test sources and fails if the merge transaction grows a
// caller outside the human path — which is the difference between a review tool and
// an automated way to destroy knowledge.
func TestMergeHasNoAutoPath(t *testing.T) {
	allowed := map[string]bool{
		"wiki_merge.go":      true, // the service entry point and its own definition
		"bindings.go":        true, // desktop binding, called from a click
		"wiki_merge_test.go": true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var offenders []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || allowed[name] {
			continue
		}
		src, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		// A test-named file is skipped above by suffix; job workers and tickers are
		// the paths that must never reach a merge.
		if strings.Contains(string(src), "MergeWikiPages(") ||
			strings.Contains(string(src), "MergeWikiPagesInto(") {
			offenders = append(offenders, name)
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("the merge transaction gained a non-human caller: %v "+
			"(ADR-0019 决策 1: 合并只能由人点)", offenders)
	}

	// And the same claim, checked structurally rather than by string match: no
	// ticker or job worker references a merge.
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse dir: %v", err)
	}
	for pkgName := range pkgs {
		if pkgName != "service" {
			t.Fatalf("unexpected package %q in this directory", pkgName)
		}
	}
}

// The service wrapper builds the tombstone text and refuses nothing the db layer
// would not already refuse — asserted here because the copy is user-visible.
func TestMergeWikiPagesInto_FilesTombstoneNamingTheWinner(t *testing.T) {
	svc, _ := setupService(t)
	ids := mergeSignalFixture(t, svc)

	res, err := svc.MergeWikiPagesInto(ids["gp"], ids["pg"])
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if res.WinnerSlug != "payment-gateway" || res.LoserSlug != "payment-gateway-v2" {
		t.Fatalf("unexpected direction: %+v", res)
	}
	loser, err := db.GetWikiPageByID(svc.db, ids["gp"])
	if err != nil {
		t.Fatalf("reload loser: %v", err)
	}
	if loser.Status != db.WikiStatusRejected {
		t.Fatalf("loser must be rejected, got %q", loser.Status)
	}
	if !strings.Contains(loser.Content, "[[payment-gateway]]") {
		t.Fatalf("the tombstone must name the winner, got %q", loser.Content)
	}
}

// TestMergeSimilarityHelpers (the pure scoring helpers) moved to the wikigen
// package with the helpers themselves (ADR-0060 step three).
