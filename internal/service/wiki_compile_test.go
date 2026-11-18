package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"repotory/internal/db"
)

// chatStub answers one OpenAI-style /chat/completions request with `content`,
// counting calls so "gated off means nothing leaves the app" is provable.
func chatStub(t *testing.T, content string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%s}}]}`, jsonQuote(content))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func jsonQuote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func configureCompile(t *testing.T, svc *Service, url string) {
	t.Helper()
	for k, v := range map[string]string{
		"ai_chat_base_url": url + "/v1",
		"ai_chat_model":    "stub",
		wikiCompileKey:     "1",
	} {
		if err := svc.UpdateConfig(k, v); err != nil {
			t.Fatalf("config %s: %v", k, err)
		}
	}
}

// A note plus the vocabulary the compiler is shown.
func compileNoteFixture(t *testing.T, svc *Service) (projectID int64, noteID int64) {
	t.Helper()
	projectID = seedProject(t, svc.db, "compile", "/tmp/compile")
	n, err := db.CreateNoteEx(svc.db, projectID, "网关重试与幂等",
		"支付网关最多重试三次，幂等键使用 invoice id；超时后返回 408。", "", "knowledge", "manual")
	if err != nil {
		t.Fatal(err)
	}
	return projectID, n.ID
}

func TestWikiCompile_GatedOffSendsNothing(t *testing.T) {
	svc, _ := setupService(t)
	_, noteID := compileNoteFixture(t, svc)
	srv, hits := chatStub(t, "[]")
	if err := svc.UpdateConfig("ai_chat_base_url", srv.URL+"/v1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateConfig("ai_chat_model", "stub"); err != nil {
		t.Fatal(err)
	}
	// wiki_compile is deliberately NOT set.
	rep, err := svc.CompileNote(noteID)
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 0 {
		t.Fatalf("%d request(s) left the app with the gate off", hits.Load())
	}
	if !strings.Contains(rep.LLMNote, wikiCompileKey) {
		t.Errorf("LLMNote = %q, want it to name the switch that is off", rep.LLMNote)
	}
}

// The happy path, and the retrieval firewall that comes with it: a compiled page
// exists, is listed, is reviewable — and does not answer a single query until a
// human approves it.
func TestWikiCompile_PendingPagesAreInvisibleUntilApproved(t *testing.T) {
	svc, _ := setupService(t)
	projectID, noteID := compileNoteFixture(t, svc)
	ops := fmt.Sprintf(`[
	 {"op":"create_page","slug":"payment-gateway","title":"Payment Gateway","kind":"entity","body":"支付网关承担重试与幂等处理，重试上限三次，幂等键为 invoice id。这条描述刻意写长一点，以便越过 thin-page 阈值，从而不会让两个检查互相遮蔽。"},
	 {"op":"create_page","slug":"idempotency-key","title":"Idempotency Key","kind":"concept","body":"幂等键使用 invoice id，使得重复提交在网关侧被折叠为同一次扣款。这条也刻意写长一点，理由同上，不解释。"},
	 {"op":"add_link","slug":"payment-gateway","link_to":"idempotency-key","relation":"depends-on"},
	 {"op":"attach_note","slug":"payment-gateway","note_id":%d}
	]`, noteID)
	srv, hits := chatStub(t, ops)
	configureCompile(t, svc, srv.URL)

	rep, err := svc.CompileNote(noteID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.PagesCreated != 2 {
		t.Fatalf("PagesCreated = %d, want 2 (report=%+v)", rep.PagesCreated, rep)
	}
	if rep.LinksCreated != 1 {
		t.Errorf("LinksCreated = %d, want 1", rep.LinksCreated)
	}
	if hits.Load() == 0 {
		t.Fatal("no model call happened")
	}
	// Provenance is automatic for every produced page, not model-elected.
	for _, slug := range []string{"payment-gateway", "idempotency-key"} {
		p, err := db.GetWikiPageBySlug(svc.db, slug)
		if err != nil {
			t.Fatalf("%s missing: %v", slug, err)
		}
		if p.Status != db.WikiStatusPending {
			t.Errorf("%s status = %q, want pending", slug, p.Status)
		}
		if p.Source != compileSourceTag {
			t.Errorf("%s source = %q, want %q", slug, p.Source, compileSourceTag)
		}
		notes, _ := db.NotesForPage(svc.db, p.ID)
		if len(notes) == 0 {
			t.Errorf("%s has no source note: the compiler must not be able to skip provenance", slug)
		}
	}

	// The firewall proper.
	query := "支付网关 幂等 invoice id 重试"
	if hits := mustSearchPages(t, svc, query); len(hits) != 0 {
		t.Fatalf("pending pages answered a query before review: %v", hits)
	}
	ev := svc.GatherEvidence(context.Background(), projectID, query, EvidenceBudget{})
	for _, it := range ev.Items {
		if it.Type == "page" && it.Kind != "" {
			if it.Slug == "payment-gateway" || it.Slug == "idempotency-key" {
				t.Fatalf("AskAI evidence contained an unreviewed page: %+v", it)
			}
		}
	}
	// Inventory paths do see them — that is how review is possible at all.
	pending, err := svc.ListPendingWikiPages(projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 {
		t.Fatalf("review queue has %d pages, want 2", len(pending))
	}
	if len(pending[0].Sources) == 0 {
		t.Error("a review row lacks its source notes; a reviewer cannot compare")
	}

	// Approving publishes them. Approve is reachable only from here (a human
	// action), and the queries below each use terms that live in ONE page: asking
	// for words spread across both would test the AND semantics of the tokenizer
	// rather than the status filter, and would have looked like a bug in the
	// filter.
	for _, p := range pending {
		if err := svc.ApproveWikiPage(p.WikiPage.ID); err != nil {
			t.Fatal(err)
		}
	}
	if got := mustSearchPages(t, svc, "支付网关"); len(got) == 0 {
		t.Error("the approved entity page still does not answer queries; the status filter is over-tight")
	}
	if got := mustSearchPages(t, svc, "幂等键"); len(got) == 0 {
		t.Error("the approved concept page still does not answer queries")
	}
}

func TestWikiCompile_UnparseableReplyWritesNothing(t *testing.T) {
	svc, _ := setupService(t)
	_, noteID := compileNoteFixture(t, svc)
	srv, _ := chatStub(t, "我觉得应该建两张页，一张讲网关一张讲幂等。")
	configureCompile(t, svc, srv.URL)

	before := countPages(t, svc)
	rep, err := svc.CompileNote(noteID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.PagesCreated != 0 || countPages(t, svc) != before {
		t.Errorf("pages written from a non-JSON reply: %+v", rep)
	}
	if !strings.Contains(rep.LLMNote, "无法解析") {
		t.Errorf("LLMNote = %q, want the parse failure stated", rep.LLMNote)
	}
}

// Decision 3: the compiler may not edit an approved page. The intent survives as a
// todo, and the page is byte-for-byte unchanged.
func TestWikiCompile_NeverEditsApprovedPages(t *testing.T) {
	svc, _ := setupService(t)
	projectID, noteID := compileNoteFixture(t, svc)
	existing, err := db.CreateWikiPage(svc.db, db.WikiKindEntity, "payment-gateway",
		"Payment Gateway", projectID, "人工写下的原始描述，一个字都不许被模型改掉。这是一段足够长的文本用来做前后比对基线。")
	if err != nil {
		t.Fatal(err)
	}
	ops := fmt.Sprintf(`[{"op":"create_page","slug":"payment-gateway","title":"Hijacked","kind":"entity","body":"模型想把它整个换掉，这属于修订，必须降级为待办。这里再补一点长度让两个检查都不至于互相遮蔽，够了。"}]`)
	srv, _ := chatStub(t, ops)
	configureCompile(t, svc, srv.URL)

	rep, err := svc.CompileNote(noteID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.PagesCreated != 0 {
		t.Errorf("the compiler created/overwrote a page over an approved one: %+v", rep)
	}
	after, err := db.GetWikiPageByID(svc.db, existing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Content != existing.Content || after.Title != existing.Title || after.UpdatedAt != existing.UpdatedAt {
		t.Errorf("approved page mutated by compile:\n before=%+v\n after =%+v", existing, after)
	}
	var sawRevision bool
	for _, td := range svc.ListTodos(projectID) {
		if strings.Contains(td.Title, "compile-revision") {
			sawRevision = true
		}
	}
	if !sawRevision {
		t.Error("the revision intent was dropped instead of surfaced to a human")
	}
}

// Re-compiling the same note refreshes its own pending output rather than
// piling up duplicates — otherwise the review queue grows every time someone
// presses the button twice.
func TestWikiCompile_IsIdempotentOnPendingOutput(t *testing.T) {
	svc, _ := setupService(t)
	_, noteID := compileNoteFixture(t, svc)
	ops := `[{"op":"create_page","slug":"idempotency-key","title":"Idempotency Key","kind":"concept","body":"第一版描述，长度足够越过阈值，不再解释为什么每版都要写这么长。"}]`
	srv, _ := chatStub(t, ops)
	configureCompile(t, svc, srv.URL)

	first, err := svc.CompileNote(noteID)
	if err != nil {
		t.Fatal(err)
	}
	if first.PagesCreated != 1 {
		t.Fatalf("first run: %+v", first)
	}
	secondOps := `[{"op":"create_page","slug":"idempotency-key","title":"Idempotency Key v2","kind":"concept","body":"第二版描述，替换上一版未审核的内容，长度同样要越过那条阈值所以写满一点。"}]`
	srv2, _ := chatStub(t, secondOps)
	configureCompile(t, svc, srv2.URL)

	second, err := svc.CompileNote(noteID)
	if err != nil {
		t.Fatal(err)
	}
	if second.PagesCreated != 0 || second.PagesUpdated != 1 {
		t.Errorf("re-compile duplicated instead of refreshing: %+v", second)
	}
	all, _ := db.ListWikiPagesByStatus(svc.db, db.WikiStatusPending, 0)
	if len(all) != 1 {
		t.Fatalf("%d pending pages after two compiles of one note, want 1", len(all))
	}
	if all[0].Title != "Idempotency Key v2" {
		t.Errorf("pending page not refreshed: %q", all[0].Title)
	}
}

// The budget has to bite, and it has to say it bit: a model that keeps proposing
// pages must not be able to produce an unbounded review queue in one click.
func TestWikiCompile_StopsAtPerNoteBudget(t *testing.T) {
	svc, _ := setupService(t)
	_, noteID := compileNoteFixture(t, svc)
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < compileMaxPagesPerNote+3; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"op":"create_page","slug":"page-%d","title":"Page %d","kind":"concept","body":"一段足够长的正文，确保它不会被薄页检查顺带拦下，从而预算是这里唯一生效的约束条件。"}`, i, i)
	}
	b.WriteString("]")
	srv, _ := chatStub(t, b.String())
	configureCompile(t, svc, srv.URL)

	rep, err := svc.CompileNote(noteID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.PagesCreated+rep.PagesUpdated > compileMaxPagesPerNote {
		t.Errorf("%d pages from one note, over the %d budget", rep.PagesCreated, compileMaxPagesPerNote)
	}
	if rep.Stopped == "" {
		t.Error("hitting the budget must be reported, not silently truncated")
	}
}

// Reject replaces a never-published page with an error-page tombstone; approve stays safe.
func TestWikiCompile_RejectCannotTouchApproved(t *testing.T) {
	svc, _ := setupService(t)
	_, noteID := compileNoteFixture(t, svc)
	ops := `[{"op":"create_page","slug":"to-reject","title":"To Reject","kind":"concept","body":"一段足够长的正文，用来确认这条不是被薄页检查顺手拦下的，之后由人拒绝掉它。"},{"op":"create_page","slug":"to-approve","title":"To Approve","kind":"concept","body":"另一段足够长的正文，这张会被批准，然后拒绝它必须失败。"}]`
	srv, _ := chatStub(t, ops)
	configureCompile(t, svc, srv.URL)
	if _, err := svc.CompileNote(noteID); err != nil {
		t.Fatal(err)
	}
	rej, err := db.GetWikiPageBySlug(svc.db, "to-reject")
	if err != nil {
		t.Fatal(err)
	}
	apr, err := db.GetWikiPageBySlug(svc.db, "to-approve")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ApproveWikiPage(apr.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.RejectWikiPage(rej.ID); err != nil {
		t.Fatalf("rejecting a pending page failed: %v", err)
	}
	// Reject is now a tombstone, not a delete: the row survives as an error page
	// so anything that linked to it does not dangle.
	after, err := db.GetWikiPageByID(svc.db, rej.ID)
	if err != nil {
		t.Fatalf("rejected page vanished: %v", err)
	}
	if after.Status != db.WikiStatusRejected {
		t.Errorf("rejected page status = %q, want %q", after.Status, db.WikiStatusRejected)
	}
	if after.Content == rej.Content || after.Content == "" {
		t.Error("rejected page kept its original body; it must be replaced by the error placeholder")
	}
	// Gone from the pending feed, present in the rejected feed.
	if pending, _ := svc.ListPendingWikiPages(0); len(pending) != 0 {
		t.Errorf("rejected page still in pending feed: %+v", pending)
	}
	rejected, err := svc.ListRejectedWikiPages(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 1 || rejected[0].WikiPage.ID != rej.ID {
		t.Errorf("rejected feed = %+v, want the one tombstone", rejected)
	}
	// The escape hatch: permanent delete works on a rejected page.
	if err := svc.DeleteCompiledPage(rej.ID); err != nil {
		t.Fatalf("delete compiled page: %v", err)
	}
	if _, err := db.GetWikiPageByID(svc.db, rej.ID); err == nil {
		t.Error("DeleteCompiledPage did not remove the tombstone")
	}
	if err := svc.RejectWikiPage(apr.ID); err == nil {
		t.Error("RejectWikiPage accepted an approved page: rejection only applies to pending")
	}
	if err := svc.DeleteCompiledPage(apr.ID); err == nil {
		t.Error("DeleteCompiledPage removed an approved page: cleaning the queue must not destroy published knowledge")
	}
	if _, err := db.GetWikiPageByID(svc.db, apr.ID); err != nil {
		t.Errorf("approved page vanished anyway: %v", err)
	}
}

// Rejecting must not sever the graph: a page that linked to the rejected one
// keeps its edge, and the tombstone still appears in that link's target list.
func TestWikiCompile_RejectPreservesInboundLinks(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "tomb", "/tmp/tomb")
	src, err := db.CreateWikiPageAs(svc.db, db.WikiKindEntity, "src-page", "Src Page", pid, "source content long enough to survive the thin-page threshold in this check", db.WikiStatusApproved, "manual")
	if err != nil {
		t.Fatal(err)
	}
	target, err := db.CreateWikiPageAs(svc.db, db.WikiKindConcept, "target-page", "Target Page", pid, "target content long enough to survive the thin-page threshold in this check", db.WikiStatusPending, compileSourceTag)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.LinkWikiPages(svc.db, src.ID, target.ID, db.RelationRef); err != nil {
		t.Fatal(err)
	}
	if err := svc.RejectWikiPage(target.ID); err != nil {
		t.Fatal(err)
	}
	out, err := db.WikiEdgesFrom(svc.db, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].PageID != target.ID {
		t.Fatalf("inbound link was dropped when the target was rejected: %+v", out)
	}
}

// ADR-0015 open q2: pending pages are exported (tagged), and index.md stays
// approved-only, so the retrieval artifact never routes into unreviewed content.
func TestWikiExport_StatusSplit(t *testing.T) {
	svc, _ := setupService(t)
	pid, noteID := compileNoteFixture(t, svc)
	ops := `[{"op":"create_page","slug":"pending-page","title":"Pending Page","kind":"concept","body":"待审页面的正文，长度足够越过薄页阈值，因此下面的断言只关于状态而不是关于长度。"}]`
	srv, _ := chatStub(t, ops)
	configureCompile(t, svc, srv.URL)
	if _, err := svc.CompileNote(noteID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateWikiPage(svc.db, db.WikiKindEntity, "approved-page", "Approved Page", pid,
		"已批准页面的正文，长度同样足够越过阈值，这样两页在导出里的差别只可能来自状态。"); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	rep, err := svc.ExportWikiTree(out, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pages != 2 {
		t.Fatalf("export saw %d pages, want both statuses", rep.Pages)
	}
	pendingFile, err := readFile(filepath.Join(out, "wiki", "concepts", "pending-page.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pendingFile, "status: pending") || !strings.Contains(pendingFile, "source: wiki-compile") {
		t.Errorf("exported pending page lacks status/source tags:\n%s", pendingFile)
	}
	idx, err := readFile(filepath.Join(out, "wiki", "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(idx, "approved-page") {
		t.Error("index.md dropped the approved page")
	}
	if strings.Contains(idx, "pending-page") {
		t.Error("index.md listed an unreviewed page; it is a retrieval artifact")
	}
}

// v18 at rest: the columns exist, the CHECK is real, and pre-existing rows read as
// approved (otherwise an upgrade silently empties retrieval).
func TestWikiReviewColumns_SchemaAndDefaults(t *testing.T) {
	svc, _ := setupService(t)
	human, err := db.CreateWikiPage(svc.db, db.WikiKindEntity, "human-page", "Human Page", 0, "人写的页面")
	if err != nil {
		t.Fatal(err)
	}
	if human.Status != db.WikiStatusApproved || human.Source != "manual" {
		t.Errorf("legacy-shaped write got status=%q source=%q, want approved/manual", human.Status, human.Source)
	}
	if _, err := svc.db.Exec(`INSERT INTO wiki_pages(slug,title,kind,status) VALUES('bad','Bad','entity','published')`); err == nil {
		t.Error("the status CHECK constraint accepted an unknown state")
	}
	if err := db.SetWikiPageStatus(svc.db, human.ID, "published"); err == nil {
		t.Error("SetWikiPageStatus accepted an unknown state")
	}
	counts, err := db.CountWikiPagesByStatus(svc.db)
	if err != nil {
		t.Fatal(err)
	}
	if counts[db.WikiStatusApproved] != 1 {
		t.Errorf("status counts = %v, want one approved", counts)
	}
	// A page that predates the review columns must still be retrievable after the
	// upgrade — the whole reason status defaults to 'approved'.
	if got := mustSearchPages(t, svc, "人写的页面"); len(got) == 0 {
		t.Error("an existing page became invisible after v18")
	}
}

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

func mustSearchPages(t *testing.T, svc *Service, q string) []db.WikiPage {
	t.Helper()
	got, err := db.SearchWikiPages(svc.db, q, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func countPages(t *testing.T, svc *Service) int {
	t.Helper()
	all, err := db.ListWikiPages(svc.db, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	return len(all)
}
