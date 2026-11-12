package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"repotory/internal/db"
	"repotory/internal/service"
)

// The handoff gate is the repeatable 口径 behind TODO 2.1: "抽 N 条历史
// handoff，问「当时记的是什么」". These tests pin the three things that could
// quietly turn it into a green lie: a probe that does not describe what was
// recorded, a context check that measures something other than what an agent
// receives, and a verdict that does not notice an unreachable memory.

func TestParseHandoffSectionsSplitsTheCanonicalTemplate(t *testing.T) {
	content := "# Session Handoff\n\n> Written by `claude-code`\n\n" +
		"## Summary\n\n做完了幂等键改造。\n\n## Changes\n\n- 新增唯一索引\n- 补了回归测试\n\n" +
		"## Gotchas\n\n- 幂等键必须带网关流水号\n\n## Next Steps\n\n- 上线观察\n"

	sections := parseHandoffSections(content)
	if got := sections["Summary"]; got != "做完了幂等键改造。" {
		t.Errorf("Summary = %q", got)
	}
	if got := sections["Changes"]; got != "- 新增唯一索引\n- 补了回归测试" {
		t.Errorf("Changes = %q", got)
	}
	if got := sections["Gotchas"]; got != "- 幂等键必须带网关流水号" {
		t.Errorf("Gotchas = %q", got)
	}
	if _, ok := sections["Session Handoff"]; ok {
		t.Error("the '#' level heading must not be treated as a section")
	}
}

func TestDeriveHandoffProbePrefersGotchas(t *testing.T) {
	note := db.NoteWithProject{Note: db.Note{ID: 7, Content: "# Session Handoff\n\n" +
		"## Summary\n\n总结句\n\n## Changes\n\n- 改了配置\n\n## Gotchas\n\n- 重试会重复入账\n"}}

	probe, ok := deriveHandoffProbe(note)
	if !ok {
		t.Fatal("expected a probe")
	}
	if probe.Section != "Gotchas" {
		t.Errorf("section = %q, want Gotchas — a gotcha is what the next session most needs", probe.Section)
	}
	if probe.Query != "重试会重复入账" {
		t.Errorf("query = %q", probe.Query)
	}
	if probe.NoteID != 7 {
		t.Errorf("note id = %d", probe.NoteID)
	}
}

func TestDeriveHandoffProbeFallsBackToSummary(t *testing.T) {
	note := db.NoteWithProject{Note: db.Note{ID: 8, Content: "## Summary\n\n只有一句总结也足够找回上下文\n"}}

	probe, ok := deriveHandoffProbe(note)
	if !ok {
		t.Fatal("expected a probe")
	}
	if probe.Section != "Summary" {
		t.Errorf("section = %q, want Summary", probe.Section)
	}
}

// A note with nothing in it yields no probe. An empty probe would query the
// retriever for nothing and score a miss that says nothing about the memory
// layer — the gate would report a broken handoff where there is simply no
// content to remember.
func TestDeriveHandoffProbeSkipsNotesWithoutContent(t *testing.T) {
	for _, content := range []string{"", "# Session Handoff\n\n## Summary\n\n\n## Gotchas\n\n"} {
		if _, ok := deriveHandoffProbe(db.NoteWithProject{Note: db.Note{ID: 1, Content: content}}); ok {
			t.Errorf("content %q should not yield a probe", content)
		}
	}
}

// A probe shorter than a few runes cannot produce a trigram, so it would fall
// back to a LIKE scan of the whole corpus — a measurement of the fallback
// path, not of the handoff.
func TestDeriveHandoffProbeSkipsFragmentsTooShortToIndex(t *testing.T) {
	note := db.NoteWithProject{Note: db.Note{ID: 9, Content: "## Gotchas\n\n- 短\n"}}
	if _, ok := deriveHandoffProbe(note); ok {
		t.Error("a 1-rune fragment must not become a probe")
	}
}

func TestTruncateProbeCutsLatinAtAWordBoundary(t *testing.T) {
	got := truncateProbe("the idempotency key must carry the gateway serial number on every retry", 30)
	if strings.HasSuffix(got, "the") || strings.HasSuffix(got, "gat") {
		t.Errorf("probe %q ends mid-word", got)
	}
	if len([]rune(got)) > 30 {
		t.Errorf("probe %q is longer than the bound", got)
	}
}

func TestTruncateProbeCutsCJKAtARuneBoundary(t *testing.T) {
	in := strings.Repeat("幂等键", 30)
	got := truncateProbe(in, 20)
	if len([]rune(got)) != 20 {
		t.Errorf("probe %q has %d runes, want 20", got, len([]rune(got)))
	}
	for _, r := range got {
		if r == '�' {
			t.Errorf("probe %q was cut mid-character", got)
		}
	}
}

func TestFirstContextNoteTitleReadsTheFirstEntry(t *testing.T) {
	ctx := "# Project Context: demo\n\n## Tech Stack\n\n- Go\n\n" +
		"## Knowledge Notes\n\n### Session Handoff 2026-10-28 10:00\n\n- Tags: handoff\n\n" +
		"### Earlier Handoffs\n\n- older\n\n### Some plain note\n\nbody\n"

	if got := firstContextNoteTitle(ctx); got != "Session Handoff 2026-10-28 10:00" {
		t.Errorf("lead title = %q", got)
	}
	if got := firstContextNoteTitle("no notes section here"); got != "" {
		t.Errorf("a context without a notes section should have no lead title, got %q", got)
	}
}

// --- integration ------------------------------------------------------------

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	_ = w.Close()
	os.Stdout = orig
	return <-done
}

// seedGateCorpus writes a small but real corpus: one scanner-backed project,
// one handoff, and one unrelated plain note.
func seedGateCorpus(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "repotory.db")
	database, err := db.InitDB(dbPath)
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := db.SyncProjectTx(tx, "demo", filepath.Join(dir, "demo"), 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	svc := service.New(database, "abeval")
	if _, err := svc.CreateHandoffNote(service.HandoffInput{
		ProjectID: pid,
		Summary:   "支付回调的幂等键改造完成了",
		Gotchas:   []string{"幂等键必须包含网关流水号，否则重试会重复入账"},
		Changes:   []string{"新增 idempotency_key 唯一索引"},
	}); err != nil {
		t.Fatalf("create handoff: %v", err)
	}
	if _, err := svc.CreateNoteWithMeta(pid, "部署说明", "发布窗口在每周三晚上", "ops", "knowledge", "manual"); err != nil {
		t.Fatalf("create note: %v", err)
	}
	return dbPath
}

// The healthy path: every recorded handoff is reachable, and the context
// opens with the newest one.
func TestHandoffGatePassesOnAHealthyCorpus(t *testing.T) {
	dbPath := seedGateCorpus(t)

	var code int
	out := captureStdout(t, func() { code = runHandoffGate(dbPath, 5) })

	if code != 0 {
		t.Fatalf("gate exited %d, want 0\n%s", code, out)
	}
	for _, want := range []string{
		"GATE PASS",
		"handoff corpus: 1 handoff note(s)",
		"context lead rate: 1/1 project(s) open with their newest handoff",
		"hit@1=",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing %q\n%s", want, out)
		}
	}
}

// The failure path: a handoff that the production search path cannot find
// must fail the gate.
//
// Ranking can no longer be used to build this case. search.go boosts a
// handoff-tagged hit over the LIMIT window (12.12), so the older trick of
// flooding the corpus with byte-identical distractors and ageing the handoff
// no longer pushes it out — by design. The honest failure mode left is index
// drift: the note row exists (so the gate has something to measure) but the
// FTS entry is gone, which is what a broken rebuild or a partially applied
// migration looks like.
func TestHandoffGateFailsWhenARecordedHandoffIsUnreachable(t *testing.T) {
	dbPath := seedGateCorpus(t)

	database, err := db.InitDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()

	var id, pid int64
	var title, content string
	if err := database.QueryRow(
		"SELECT id, project_id, title, content FROM project_notes WHERE tags LIKE '%handoff%' LIMIT 1").
		Scan(&id, &pid, &title, &content); err != nil {
		t.Fatal(err)
	}

	svc := service.New(database, "abeval")
	for i := 0; i < 25; i++ {
		if _, err := svc.CreateNoteWithMeta(pid, title, content, "distractor", "knowledge", "manual"); err != nil {
			t.Fatal(err)
		}
	}
	// Drop the handoff's index entry only. project_notes_fts is an external
	// content table, so this is the same 'delete' command the triggers issue.
	if _, err := database.Exec(
		"INSERT INTO project_notes_fts(project_notes_fts, rowid, title, content) VALUES('delete', ?, ?, ?)",
		id, title, content); err != nil {
		t.Fatal(err)
	}

	var code int
	out := captureStdout(t, func() { code = runHandoffGate(dbPath, 5) })

	if code == 0 {
		t.Fatalf("gate passed with an unreachable handoff\n%s", out)
	}
	if !strings.Contains(out, "GATE FAIL") {
		t.Errorf("report does not say why it failed\n%s", out)
	}
}
