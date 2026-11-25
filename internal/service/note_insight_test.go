package service

import (
	"strconv"
	"testing"

	"repotory/internal/db"
)

// seedNote is a tiny helper for the insight tests: create a note in a project
// and return its id.
func seedNote(t *testing.T, svc *Service, projectID int64, title, content, tags string) int64 {
	t.Helper()
	n, err := db.CreateNoteEx(svc.db, projectID, title, content, tags, "knowledge", "manual")
	if err != nil {
		t.Fatalf("seed note %q: %v", title, err)
	}
	return n.ID
}

// insightFixture seeds one project with two notes about the same subject and
// one about a different one — the shape the related-note layer must get right.
func insightFixture(t *testing.T, svc *Service) (projectID, sqliteA, sqliteB, cssID int64) {
	t.Helper()
	projectID = seedProject(t, svc.db, "insight", "/tmp/insight")
	sqliteA = seedNote(t, svc, projectID, "SQLite 迁移失败复盘",
		"在生产环境执行 schema 迁移时，ALTER TABLE 添加列失败，原因是旧库已存在同名列。修复：先检测 pragma table_info，再决定是否 ALTER。", "sqlite,migration")
	sqliteB = seedNote(t, svc, projectID, "SQLite 迁移的列检测",
		"迁移脚本必须先查 pragma table_info，确认列不存在再执行 ALTER TABLE，否则重复添加列会失败。线上库已存在同名列时直接跳过。", "sqlite")
	cssID = seedNote(t, svc, projectID, "CSS 网格布局调整",
		"知识卡片改成 auto-fill 网格，固定高度，正文超出用渐变遮罩。标题字号降到 13px，卡片头部只留图标按钮。", "css")
	return projectID, sqliteA, sqliteB, cssID
}

func TestNoteInsightsRelatedUsesSharedTerms(t *testing.T) {
	svc, _ := setupService(t)
	projectID, a, b, css := insightFixture(t, svc)

	insights := svc.NoteInsights(projectID)
	if len(insights) != 3 {
		t.Fatalf("insights = %d, want 3", len(insights))
	}
	byID := map[int64]int{}
	for i, ins := range insights {
		byID[ins.NoteID] = i
	}

	relA := insights[byID[a]].Related
	if len(relA) == 0 {
		t.Fatalf("note %d has no related notes", a)
	}
	if relA[0].ID != b {
		t.Fatalf("related[0] = %d, want the SQLite sibling %d (got %+v)", relA[0].ID, b, relA[0])
	}
	if len(relA[0].Shared) == 0 {
		t.Error("related note carries no shared terms, so the UI cannot say why")
	}
	for _, r := range relA {
		if r.ID == css {
			t.Errorf("CSS note %d was offered as related to the SQLite note", css)
		}
	}

	relCSS := insights[byID[css]].Related
	for _, r := range relCSS {
		if r.ID == a || r.ID == b {
			t.Errorf("SQLite note %d was offered as related to the CSS note", r.ID)
		}
	}
}

func TestNoteInsightsSingleNoteHasNoRelated(t *testing.T) {
	svc, _ := setupService(t)
	projectID := seedProject(t, svc.db, "solo", "/tmp/solo")
	id := seedNote(t, svc, projectID, "只有一条", "这条笔记没有邻居，但仍然应该拿到自己的洞察结构。", "")

	insights := svc.NoteInsights(projectID)
	if len(insights) != 1 {
		t.Fatalf("insights = %d, want 1", len(insights))
	}
	if insights[0].NoteID != id || len(insights[0].Related) != 0 {
		t.Fatalf("single note insight = %+v, want that note with no related", insights[0])
	}
}

func TestNoteInsightsScopeMatchesProject(t *testing.T) {
	svc, _ := setupService(t)
	pa, a, _, _ := insightFixture(t, svc)
	pb := seedProject(t, svc.db, "other", "/tmp/other")
	other := seedNote(t, svc, pb, "SQLite 迁移失败复盘",
		"在生产环境执行 schema 迁移时，ALTER TABLE 添加列失败，原因是旧库已存在同名列。", "sqlite")

	scoped := svc.NoteInsights(pa)
	for _, ins := range scoped {
		if ins.NoteID == other {
			t.Fatal("project scope leaked a note from another project")
		}
	}
	for _, ins := range scoped {
		if ins.NoteID != a {
			continue
		}
		for _, r := range ins.Related {
			if !r.SameProject {
				t.Fatalf("related %d is in another project inside a project-scoped call", r.ID)
			}
		}
	}

	// The knowledge hub (projectID 0) sees both, which is the only place a
	// cross-project relation is allowed to appear.
	all := svc.NoteInsights(0)
	if len(all) < 4 {
		t.Fatalf("global insights = %d, want every note", len(all))
	}
}

func TestDuplicateNotesFlagsNearIdenticalPair(t *testing.T) {
	svc, _ := setupService(t)
	projectID := seedProject(t, svc.db, "dup", "/tmp/dup")
	body := "Clash 代理端口与实际监听不一致时，git fetch 会报 502。用 lsof 查实际端口，再写进 http.proxy。"
	first := seedNote(t, svc, projectID, "代理端口排查", body, "proxy")
	second := seedNote(t, svc, projectID, "代理端口排查（副本）", body+" 另外记一下 lsof 的参数。", "proxy")
	seedNote(t, svc, projectID, "周报模板", "本周提交集中在知识库检索与渲染链路，下周补上离线和评测。", "weekly")

	pairs := svc.DuplicateNotes(projectID)
	if len(pairs) != 1 {
		t.Fatalf("pairs = %d, want exactly the duplicate pair (%+v)", len(pairs), pairs)
	}
	got := []int64{pairs[0].A.ID, pairs[0].B.ID}
	if !(got[0] == first && got[1] == second) && !(got[0] == second && got[1] == first) {
		t.Fatalf("pair = %v, want %d and %d", got, first, second)
	}
	if pairs[0].Score < insightDuplicateMinScore {
		t.Fatalf("score = %.2f, below the duplicate threshold", pairs[0].Score)
	}
	if len(pairs[0].Shared) == 0 {
		t.Error("duplicate pair carries no shared terms")
	}
}

func TestGenerateNoteInsightsCachesAndMarksStale(t *testing.T) {
	svc, _ := setupService(t)
	projectID, a, _, _ := insightFixture(t, svc)

	srv, _ := chatStubCapture(t, `[{"id":`+strconv.FormatInt(a, 10)+`,"summary":"迁移前先查 pragma table_info，避免重复加列。","keywords":["sqlite","迁移","pragma"]}]`, "")
	defer srv.Close()
	configureOrganize(t, svc, srv.URL)

	gen, err := svc.GenerateNoteInsights(projectID, []int64{a})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(gen.Insights) != 1 || gen.Insights[0].Summary == "" {
		t.Fatalf("generated = %+v, want one summary", gen.Insights)
	}

	// A second read is served from the cache: Cached true, not stale, and the
	// keywords survived the round trip through the comma-joined column.
	var cached bool
	for _, ins := range svc.NoteInsights(projectID) {
		if ins.NoteID != a {
			continue
		}
		cached = ins.Cached && !ins.Stale && ins.Summary != "" && len(ins.Keywords) == 3
	}
	if !cached {
		t.Fatal("summary was not cached with its keywords")
	}

	// "Fill the gaps" on an already-summarized note returns nothing to do.
	gen2, err := svc.GenerateNoteInsights(projectID, nil)
	if err != nil {
		t.Fatalf("generate gaps: %v", err)
	}
	for _, ins := range gen2.Insights {
		if ins.NoteID == a {
			t.Fatal("a note with a fresh summary was summarized again")
		}
	}

	// Editing the note makes the stored summary stale instead of silently
	// quoting text that no longer exists.
	if err := db.UpdateNote(svc.db, a, "完全换掉的内容：这篇讲的是渲染管线的 SVG 尺寸计算。"); err != nil {
		t.Fatal(err)
	}
	for _, ins := range svc.NoteInsights(projectID) {
		if ins.NoteID == a && !ins.Stale {
			t.Fatal("edited note's summary is not marked stale")
		}
	}
}

func TestGenerateNoteInsightsRequiresModel(t *testing.T) {
	svc, _ := setupService(t)
	projectID, a, _, _ := insightFixture(t, svc)
	if _, err := svc.GenerateNoteInsights(projectID, []int64{a}); err == nil {
		t.Fatal("generation without a configured model should fail with a setup error")
	}
}

func TestParseInsightReplyToleratesFencesAndChatter(t *testing.T) {
	raw := "好的，结果如下：\n```json\n[{\"id\":7,\"summary\":\"  “迁移前先查列。”  \",\"keywords\":[\"sqlite\",\" \",\"迁移\"]}]\n```\n"
	parsed, warning := parseInsightReply(raw)
	if warning != "" {
		t.Fatalf("warning = %q, want none", warning)
	}
	item, ok := parsed[7]
	if !ok {
		t.Fatal("id 7 was not parsed out of a fenced reply")
	}
	if item.Summary != "迁移前先查列。" {
		t.Fatalf("summary = %q, want the quotes and padding stripped", item.Summary)
	}
	if len(item.Keywords) != 2 {
		t.Fatalf("keywords = %v, want the blank entry dropped", item.Keywords)
	}

	if _, w := parseInsightReply("抱歉，我无法完成。"); w == "" {
		t.Fatal("a reply with no array must warn instead of silently producing nothing")
	}
}

func TestInsightTermsShape(t *testing.T) {
	terms := insightTerms("ALTER TABLE 迁移迁移 schema 2024")
	for _, want := range []string{"alter", "table", "schema", "迁移"} {
		if terms[want] == 0 {
			t.Errorf("term %q missing from %v", want, terms)
		}
	}
	if terms["2024"] != 0 {
		t.Error("a bare number is not a subject term")
	}
}
