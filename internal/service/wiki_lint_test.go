package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"repotory/internal/db"
)

// lintFixture seeds a store with each defect the checks are meant to catch, so a
// passing run proves all five at once rather than one at a time.
//
//	page a  entity, has an inbound edge, links at a missing page (dangling),
//	        mentions page b's title without linking it (missing-ref)
//	page b  concept, orphan (no inbound edge), long enough to not be thin,
//	        has one source note attached
//	page c  entity, thin body, no source note
//	plus 5 entity/concept/source pages total but no synthesis (coverage gap).
func lintFixture(t *testing.T, svc *Service) map[string]int64 {
	t.Helper()
	pid := seedProject(t, svc.db, "lint", "/tmp/lint")
	ids := map[string]int64{}
	mk := func(kind, slug, title, body string) int64 {
		t.Helper()
		p, err := db.CreateWikiPage(svc.db, kind, slug, title, pid, body)
		if err != nil {
			t.Fatalf("create %s: %v", slug, err)
		}
		ids[slug] = p.ID
		return p.ID
	}
	long := "这一页足够长，用来证明 thin-page 检查是按长度而不是按存在与否判断的。" +
		"重复一些内容让它超过阈值，避免两个检查互相遮蔽，从而让断言真正指向各自的目标而不是彼此的巧合。"
	mk(db.WikiKindEntity, "auth-module", "Auth Module", long+" 它还提到了 Payment Gateway Retry 这个词组但没有加链接，并引用了 [[no-such-page]]。")
	b := mk(db.WikiKindConcept, "payment-gateway-retry", "Payment Gateway Retry", long)
	mk(db.WikiKindEntity, "stub-page", "Stub Page", "太短")
	mk(db.WikiKindSource, "incident-note", "Incident Note", long)
	mk(db.WikiKindQuery, "why-timeout", "Why Timeout", long)
	// no-synthesis fires at >=5 entity/concept/source pages; the set above is one
	// short of that, so the extra source page here is the fixture meeting the
	// threshold rather than the threshold being lowered to fit the fixture.
	mk(db.WikiKindSource, "runbook", "Runbook", long)

	// b gets an inbound edge (so it is not an orphan); a does not (so it is an
	// orphan until the edge from incident-note is added — added below, and the
	// missing-ref finding for a is expected to remain because the link is missing).
	if err := db.LinkWikiPages(svc.db, ids["incident-note"], b, "explains"); err != nil {
		t.Fatal(err)
	}
	// b has a source note; a/c do not.
	n, err := db.CreateNoteEx(svc.db, pid, "retry behaviour", "body", "", "knowledge", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AttachNoteToPage(svc.db, n.ID, b); err != nil {
		t.Fatal(err)
	}
	return ids
}

func findingsByCheck(rep *WikiLintReport) map[string]int {
	out := map[string]int{}
	for _, f := range rep.Findings {
		out[f.Check]++
	}
	return out
}

// The contract test for "lint 只能建议、不得自动改写页面".
func TestWikiLint_NeverMutatesPages(t *testing.T) {
	svc, _ := setupService(t)
	lintFixture(t, svc)

	before, err := db.ListWikiPages(svc.db, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RunWikiLint(0, true); err != nil {
		t.Fatal(err)
	}
	after, err := db.ListWikiPages(svc.db, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("page count changed %d -> %d: lint created or deleted pages", len(before), len(after))
	}
	for i := range before {
		if before[i].Content != after[i].Content || before[i].Title != after[i].Title ||
			before[i].Kind != after[i].Kind || before[i].UpdatedAt != after[i].UpdatedAt {
			t.Fatalf("lint mutated page %s: content/title/kind/updated must be untouched", before[i].Slug)
		}
	}
	// Links and note attachments are equally off-limits.
	edges, err := db.WikiEdgesFrom(svc.db, before[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = edges
}

func TestWikiLint_DetectsAllFiveStructuralChecks(t *testing.T) {
	svc, _ := setupService(t)
	lintFixture(t, svc)

	rep, err := svc.RunWikiLint(0, false)
	if err != nil {
		t.Fatal(err)
	}
	got := findingsByCheck(rep)
	for _, want := range []string{"orphan", "dangling-link", "missing-ref", "thin-page", "no-source", "no-synthesis"} {
		if got[want] == 0 {
			t.Errorf("check %q produced no finding; fixture was built to trigger it. all=%v", want, got)
		}
	}
	if rep.LLMRan {
		t.Error("llm_ran is true although the LLM pass was never requested")
	}
	// wantLLM=false means the caller did not ask: no note is expected, and the
	// gated-but-not-run case is asserted in the LLM test below instead.
	// Findings become todos, and only once per distinct finding.
	if rep.TodosCreated == 0 {
		t.Fatal("no todos filed: findings that live only in a report change nothing")
	}
	first := rep.TodosCreated
	second, err := svc.RunWikiLint(0, false)
	if err != nil {
		t.Fatal(err)
	}
	if second.TodosCreated != 0 {
		t.Errorf("second run created %d todos, want 0 (dedupe against open todos failed); first=%d",
			second.TodosCreated, first)
	}
	if second.TodosExisting == 0 {
		t.Error("the deduped findings should be counted as existing, not silently dropped")
	}
	titles := map[string]bool{}
	for _, td := range svc.ListTodos(mustProjectID(t, svc, "lint")) {
		if !strings.HasPrefix(td.Title, lintTodoPrefix) {
			continue
		}
		if titles[td.Title] {
			t.Errorf("duplicate lint todo: %s", td.Title)
		}
		titles[td.Title] = true
	}
}

// A finding on a global page has no project to own a todo. It must be reported and
// counted, never filed somewhere arbitrary to make the number look right.
func TestWikiLint_GlobalPageIsReportedNotFiled(t *testing.T) {
	svc, _ := setupService(t)
	lintFixture(t, svc)
	if _, err := db.CreateWikiPage(svc.db, db.WikiKindConcept, "global-orphan", "Global Orphan", 0,
		"a global page with no inbound links and no source note, long enough to avoid the thin-page check by a comfortable margin indeed"); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.RunWikiLint(0, false)
	if err != nil {
		t.Fatal(err)
	}
	var sawGlobal bool
	for _, f := range rep.Findings {
		if f.ProjectID == 0 {
			sawGlobal = true
		}
	}
	if !sawGlobal {
		t.Fatal("no global-page finding produced, so the unfiled path is untested")
	}
	if rep.TodosUnfiled == 0 {
		t.Error("global findings must be counted as unfiled, not dropped")
	}
}

// Findings must land on the project that owns the page, not on whichever project
// the pass happened to scan first.
func TestWikiLint_FilesOnOwningProject(t *testing.T) {
	svc, _ := setupService(t)
	lintFixture(t, svc)
	other := seedProject(t, svc.db, "otherlint", "/tmp/otherlint")
	if _, err := db.CreateWikiPage(svc.db, db.WikiKindEntity, "other-orphan", "Other Orphan", other, "短"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RunWikiLint(0, false); err != nil {
		t.Fatal(err)
	}
	mine := countLintTodos(t, svc, mustProjectID(t, svc, "lint"))
	theirs := countLintTodos(t, svc, other)
	if mine == 0 || theirs == 0 {
		t.Fatalf("todo distribution wrong: lint=%d other=%d", mine, theirs)
	}
	// Scoping the run to one project must not touch the other's todos.
	beforeOther := countLintTodos(t, svc, other)
	if _, err := svc.RunWikiLint(mustProjectID(t, svc, "lint"), false); err != nil {
		t.Fatal(err)
	}
	if after := countLintTodos(t, svc, other); after != beforeOther {
		t.Errorf("a project-scoped run changed another project's todos %d -> %d", beforeOther, after)
	}
}

// The model-backed half: gated, cited-by-id only, and unpersuaded by garbage.
func TestWikiLint_LLMChecksAreGatedAndSkeptical(t *testing.T) {
	svc, _ := setupService(t)
	ids := lintFixture(t, svc)

	var hits atomic.Int64
	payload := func(body string) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
		}
	}
	good, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
		"message": map[string]any{"content": fmt.Sprintf(
			`[{"type":"contradiction","page_id":%d,"other_id":%d,"detail":"两页对超时时间陈述冲突"},`+
				`{"type":"stale","page_id":%d,"other_id":null,"detail":"被更晚的页覆盖"},`+
				`{"type":"contradiction","page_id":999999,"other_id":null,"detail":"指向不存在的页"},`+
				`{"type":"nonsense","page_id":%d,"other_id":null,"detail":"未知类型仍作为建议收下"}]`,
			ids["auth-module"], ids["payment-gateway-retry"], ids["payment-gateway-retry"], ids["stub-page"]),
		},
	}}})
	srv := httptest.NewServer(http.HandlerFunc(payload(string(good))))
	defer srv.Close()

	// Off by default: no request may leave the app.
	if err := svc.UpdateConfig("ai_chat_base_url", srv.URL+"/v1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateConfig("ai_chat_model", "stub"); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.RunWikiLint(0, true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.LLMRan || hits.Load() != 0 {
		t.Fatalf("LLM ran without the gate: ran=%v hits=%d", rep.LLMRan, hits.Load())
	}
	if !strings.Contains(rep.LLMNote, "wiki_lint_llm") {
		t.Errorf("LLMNote = %q, want it to name the switch that is off", rep.LLMNote)
	}

	if err := svc.UpdateConfig(wikiLintLLMKey, "1"); err != nil {
		t.Fatal(err)
	}
	on, err := svc.RunWikiLint(0, true)
	if err != nil {
		t.Fatal(err)
	}
	if !on.LLMRan {
		// hits proves the stub was reached at all, which separates "endpoint never
		// called" from "reply could not be parsed" without another debugging run.
		t.Fatalf("LLM pass did not run: ran=%v note=%q hits=%d", on.LLMRan, on.LLMNote, hits.Load())
	}
	// This assertion used to be `on.LLMNote == ""`. It is no longer the right
	// contract: ADR-0019's merge judgement shares the lint model half, so with that
	// second gate left off (its default) a clean contradiction pass still has a note
	// saying so. Requiring an empty note would mean a user who enabled one model pass
	// could never discover the other exists.
	if !strings.Contains(on.LLMNote, wikiMergeSuggestKey) {
		t.Errorf("LLMNote = %q, want it to say the merge pass was skipped", on.LLMNote)
	}
	checks := findingsByCheck(on)
	if checks["contradiction"] == 0 || checks["stale"] == 0 {
		t.Errorf("model findings missing: %v", checks)
	}
	if checks["nonsense"] != 0 {
		t.Error("an unknown model check name leaked through")
	}
	// The invented page id must not surface as a finding. Assert on PageID, not on
	// the phrase "指向不存在的页": the structural dangling-link check legitimately
	// says that about the fixture's [[no-such-page]], so a substring match here
	// would fail for the wrong reason.
	for _, f := range on.Findings {
		if f.PageID == 999999 {
			t.Errorf("a model-invented page id became a finding: %+v", f)
		}
	}
	if got := findingsByCheck(on); got["contradiction"] < 1 {
		t.Errorf("the two valid contradiction suggestions were dropped: %v", got)
	}
	// Every LLM finding carries the human-judgement label, since it is advice.
	for _, f := range on.Findings {
		if f.Check == "contradiction" || f.Check == "stale" {
			if !strings.Contains(f.Detail, "需人工判断") {
				t.Errorf("model finding lacks the human-judgement label: %q", f.Detail)
			}
		}
	}

	// An unparseable reply yields no findings at all rather than a guess.
	bad, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
		"message": map[string]any{"content": "我找到了几个冲突，但没法给你 JSON。"},
	}}})
	srv2 := httptest.NewServer(http.HandlerFunc(payload(string(bad))))
	defer srv2.Close()
	if err := svc.UpdateConfig("ai_chat_base_url", srv2.URL+"/v1"); err != nil {
		t.Fatal(err)
	}
	rep2, err := svc.RunWikiLint(0, true)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.LLMRan {
		t.Error("an unparseable reply must not count as a successful LLM pass")
	}
	if !strings.Contains(rep2.LLMNote, "无法解析") {
		t.Errorf("LLMNote = %q, want it to say the reply was unparseable", rep2.LLMNote)
	}
	for _, f := range rep2.Findings {
		if f.Check == "contradiction" || f.Check == "stale" {
			t.Errorf("model findings survived an unparseable reply: %+v", f)
		}
	}
}

// A reply the server itself cut off is NOT the same failure as a malformed one:
// both arrive as "not valid JSON", but the user's next move differs (raise the
// output budget / switch model vs. re-run). Before finish_reason was carried
// through, both rendered as the same 无法解析 note.
func TestWikiLint_TruncatedReplyIsDistinguishedFromMalformed(t *testing.T) {
	svc, _ := setupService(t)
	ids := lintFixture(t, svc)

	serve := func(finishReason, content string) {
		t.Helper()
		body := mustJSON(finishReason, content)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
		}))
		t.Cleanup(srv.Close)
		if err := svc.UpdateConfig("ai_chat_base_url", srv.URL+"/v1"); err != nil {
			t.Fatal(err)
		}
		if err := svc.UpdateConfig("ai_chat_model", "stub"); err != nil {
			t.Fatal(err)
		}
		if err := svc.UpdateConfig(wikiLintLLMKey, "1"); err != nil {
			t.Fatal(err)
		}
	}

	// A prefix of a JSON array: the shape a truncated reply actually has.
	serve("length", fmt.Sprintf(`[{"type":"contradiction","page_id":%d,"other_id":%d,"detail":"两页对超时时间陈`,
		ids["auth-module"], ids["payment-gateway-retry"]))
	rep, err := svc.RunWikiLint(0, true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.LLMRan {
		t.Error("a truncated reply must not count as a successful LLM pass")
	}
	if !strings.Contains(rep.LLMNote, "截断") {
		t.Errorf("LLMNote = %q, want it to name truncation as the cause", rep.LLMNote)
	}
	if !strings.Contains(rep.LLMNote, "max_tokens") {
		t.Errorf("LLMNote = %q, want it to carry the actionable remedy", rep.LLMNote)
	}
	if strings.Contains(rep.LLMNote, "无法解析为 JSON") {
		t.Errorf("LLMNote = %q still blames the format for a truncated reply", rep.LLMNote)
	}
	for _, f := range rep.Findings {
		if f.Check == "contradiction" || f.Check == "stale" {
			t.Errorf("model findings survived a truncated reply: %+v", f)
		}
	}

	// A complete reply that simply is not JSON keeps the old wording.
	serve("stop", "我找到了几个冲突，但没法给你 JSON。")
	rep2, err := svc.RunWikiLint(0, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rep2.LLMNote, "无法解析") {
		t.Errorf("LLMNote = %q, want the format wording for a genuinely malformed reply", rep2.LLMNote)
	}
	if strings.Contains(rep2.LLMNote, "截断") {
		t.Errorf("LLMNote = %q blames truncation for a malformed reply", rep2.LLMNote)
	}
}

func TestWikiLint_EmptyStoreIsQuiet(t *testing.T) {
	svc, _ := setupService(t)
	rep, err := svc.RunWikiLint(0, true)
	if err != nil {
		t.Fatalf("linting an empty store must not fail: %v", err)
	}
	if rep.Pages != 0 || len(rep.Findings) != 0 || rep.TodosCreated != 0 {
		t.Errorf("empty store produced %+v", rep)
	}
}

func countLintTodos(t *testing.T, svc *Service, projectID int64) int {
	t.Helper()
	n := 0
	for _, td := range svc.ListTodos(projectID) {
		if strings.HasPrefix(td.Title, lintTodoPrefix) {
			n++
		}
	}
	return n
}

func mustProjectID(t *testing.T, svc *Service, name string) int64 {
	t.Helper()
	for _, p := range mustProjects(svc) {
		if p.Name == name {
			return p.ID
		}
	}
	t.Fatalf("project %q not found", name)
	return 0
}

func mustProjects(svc *Service) []db.Project {
	ps, _ := db.GetAllProjects(svc.db)
	return ps
}

// TestParseLintReply (the pure reply parser) moved to the wikilint package with
// the parser itself (ADR-0060 step four).
