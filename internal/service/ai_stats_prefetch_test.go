package service

import (
	"context"
	"strings"
	"testing"

	"repotory/internal/stats"
)

// The stats pre-fetch exists because the configured model may never call the
// stats tools (free reasoning models on OpenRouter answer "which repo?" with
// list_projects unused, or paste a stale metrics note as the answer — both
// observed live, 2026-11-11 and 2026-11-24). The block itself must therefore
// be deterministic: these tests pin what the model will actually read.

func TestLooksLikeStatsQuestion(t *testing.T) {
	cases := []struct {
		question string
		want     bool
	}{
		{"昨日代码提交量", true},
		{"今天代码提交记录是多少", true},
		{"帮我看下这个月的代码量", true},
		{"how many commits yesterday", true},
		{"昨天的提交数", true},
		{"代码提交活跃度怎么样", true},
		// Questions ABOUT commits are not questions about commit NUMBERS.
		{"提交记录怎么写比较好", false},
		{"commit message 规范是什么", false},
		{"如何提交一个 PR", false},
		// And ordinary chat never prefetches.
		{"介绍一下这个项目的架构", false},
		{"你好", false},
		{"", false},
	}
	for _, c := range cases {
		if got := looksLikeStatsQuestion(c.question); got != c.want {
			t.Errorf("looksLikeStatsQuestion(%q) = %v, want %v", c.question, got, c.want)
		}
	}
}

func TestPrefetchStatsBlockRendersLiveMeasurements(t *testing.T) {
	svc, fg := setupService(t)
	database := svc.db

	pid := seedProject(t, database, "Cinder", "/code/cinder")
	seedRepo(t, database, "/code/cinder", pid)
	seedRepo(t, database, "/code/cinder/web", pid)

	fg.ranges["/code/cinder|"] = []stats.DailyEntry{
		{Date: "2026-08-15", Commits: 3, LinesAdded: 120, LinesDeleted: 40},
		{Date: "2026-08-16", Commits: 1, LinesAdded: 15, LinesDeleted: 2},
	}
	fg.ranges["/code/cinder/web|"] = []stats.DailyEntry{
		{Date: "2026-08-15", Commits: 2, LinesAdded: 30, LinesDeleted: 10},
	}

	block := svc.prefetchStatsBlock(context.Background(), 0, "昨日代码提交量")
	if block == "" {
		t.Fatal("prefetch returned empty block for a stats question")
	}
	for _, want := range []string{
		"Live measurements",
		"2026-08-15", // yesterday, from fakeGit
		"2026-08-16", // today
		"/code/cinder",
		"3 commits, 120 lines added, 40 lines deleted",
		"TOTAL",
		"5 commits", // 3 + 2 across both repos for yesterday
	} {
		if !strings.Contains(block, want) {
			t.Errorf("block missing %q:\n%s", want, block)
		}
	}
	// The framing matters as much as the numbers: without "authoritative" the
	// model may still prefer the stale memory snapshots further down the prompt.
	if !strings.Contains(block, "authoritative") || !strings.Contains(block, "do not recompute") {
		t.Errorf("block lost its authority framing:\n%s", block)
	}
}

func TestPrefetchSkipsNonStatsQuestions(t *testing.T) {
	svc, _ := setupService(t)
	if got := svc.prefetchStatsBlock(context.Background(), 0, "介绍一下这个项目的架构"); got != "" {
		t.Errorf("non-stats question got a prefetch block: %q", got)
	}
}

func TestPrefetchScopedToProject(t *testing.T) {
	svc, fg := setupService(t)
	database := svc.db

	pidA := seedProject(t, database, "Alpha", "/code/alpha")
	pidB := seedProject(t, database, "Beta", "/code/beta")
	seedRepo(t, database, "/code/alpha", pidA)
	seedRepo(t, database, "/code/beta", pidB)
	fg.ranges["/code/alpha|"] = []stats.DailyEntry{
		{Date: "2026-08-15", Commits: 7, LinesAdded: 700, LinesDeleted: 70},
	}
	fg.ranges["/code/cinder/web|"] = []stats.DailyEntry{
		{Date: "2026-08-15", Commits: 9, LinesAdded: 900, LinesDeleted: 90},
	}

	block := svc.prefetchStatsBlock(context.Background(), pidA, "昨天提交了多少")
	if !strings.Contains(block, "/code/alpha") {
		t.Errorf("scoped block missing the scoped repo:\n%s", block)
	}
	if strings.Contains(block, "/code/beta") {
		t.Errorf("scoped block leaked the other project's repo:\n%s", block)
	}
	if strings.Contains(block, "9 commits") {
		t.Errorf("scoped block leaked the other project's numbers:\n%s", block)
	}
}

// The prefetch hooks into evidenceMessages so BOTH transports (sync ask and
// the console's streaming ask) carry it — the "same answer, different
// transport" claim would be broken if only one path measured.
func TestEvidenceMessagesCarryPrefetchedStats(t *testing.T) {
	svc, fg := setupService(t)
	database := svc.db

	pid := seedProject(t, database, "Cinder", "/code/cinder")
	seedRepo(t, database, "/code/cinder", pid)
	fg.ranges["/code/cinder|"] = []stats.DailyEntry{
		{Date: "2026-08-15", Commits: 4, LinesAdded: 400, LinesDeleted: 40},
	}

	// Assert on the block's distinctive header line, not the bare phrase: the
	// tool guide legitimately mentions "Live measurements" when it explains
	// what the block is, and a bare-phrase assert would trip on that.
	const blockHeader = "Live measurements (queried from git"

	statsMsgs := svc.evidenceMessages(context.Background(), pid, "昨日代码提交量", "")
	if len(statsMsgs) == 0 || statsMsgs[0].Role != "system" {
		t.Fatal("evidenceMessages produced no system message")
	}
	if !strings.Contains(statsMsgs[0].Content, blockHeader) {
		t.Errorf("stats question's system prompt has no measurement block:\n%s", statsMsgs[0].Content)
	}

	plainMsgs := svc.evidenceMessages(context.Background(), pid, "介绍一下这个项目的架构", "")
	if strings.Contains(plainMsgs[0].Content, blockHeader) {
		t.Errorf("non-stats question got a measurement block:\n%s", plainMsgs[0].Content)
	}
}
