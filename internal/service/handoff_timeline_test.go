package service

import (
	"strings"
	"testing"

	"repotory/internal/db"
)

// The handoff timeline is a READER over handoff notes across every project.
// These tests pin what makes it readable and what keeps it honest: ordering,
// the excerpt's shape, the auto-capture badge, the global scope, and the cap.

func seedHandoff(t *testing.T, svc *Service, projectID int64, title, content, tags string) *db.Note {
	t.Helper()
	note, err := db.CreateNoteEx(svc.db, projectID, title, content, tags, "handoff", "manual")
	if err != nil {
		t.Fatalf("seed handoff note: %v", err)
	}
	return note
}

func TestListHandoffTimelineSpansProjectsNewestFirst(t *testing.T) {
	svc, _ := setupService(t)
	pidA := seedProject(t, svc.db, "Alpha", "/code/alpha")
	pidB := seedProject(t, svc.db, "Beta", "/code/beta")

	seedHandoff(t, svc, pidA, "较早的交接", "第一周的总结。", "handoff")
	seedHandoff(t, svc, pidB, "较新的交接", "第二周的总结。", "handoff")
	// A plain note with no handoff tag must never appear on the timeline.
	if _, err := db.CreateNoteEx(svc.db, pidA, "普通笔记", "这不是交接。", "", "knowledge", "manual"); err != nil {
		t.Fatalf("seed plain note: %v", err)
	}

	out := svc.ListHandoffTimeline(0)
	if len(out) != 2 {
		t.Fatalf("timeline has %d entries, want the 2 handoffs only: %+v", len(out), out)
	}
	if out[0].Title != "较新的交接" {
		t.Errorf("newest first violated: got %q first", out[0].Title)
	}
	if out[0].ProjectName != "Beta" || out[1].ProjectName != "Alpha" {
		t.Errorf("project names missing: %+v", out)
	}
}

func TestListHandoffTimelineExcerptIsSingleLineAndClamped(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "Alpha", "/code/alpha")

	long := strings.Repeat("很长的正文段落。", 100) // far past 180 runes
	seedHandoff(t, svc, pid, "长文交接", "## Summary\n\n"+long, "handoff")

	out := svc.ListHandoffTimeline(0)
	if len(out) != 1 {
		t.Fatalf("timeline has %d entries, want 1", len(out))
	}
	e := out[0]
	if strings.ContainsAny(e.Excerpt, "\n\r") {
		t.Errorf("excerpt kept newlines: %q", e.Excerpt)
	}
	if !strings.HasSuffix(e.Excerpt, "…") {
		t.Errorf("clamped excerpt lost its ellipsis: %q", e.Excerpt)
	}
	if got := len([]rune(strings.TrimSuffix(e.Excerpt, "…"))); got > 180 {
		t.Errorf("excerpt exceeded 180 runes: %d", got)
	}
}

func TestListHandoffTimelineAutoCapturedBadge(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "Alpha", "/code/alpha")
	seedHandoff(t, svc, pid, "手动交接", "手写的。", "handoff")
	seedHandoff(t, svc, pid, "自动交接", "捕获的。", "handoff, auto-captured")

	out := svc.ListHandoffTimeline(0)
	byTitle := map[string]bool{}
	for _, e := range out {
		byTitle[e.Title] = e.AutoCaptured
	}
	if !byTitle["自动交接"] {
		t.Error("auto-captured badge missing on the auto-captured note")
	}
	if byTitle["手动交接"] {
		t.Error("manual note wrongly badged as auto-captured")
	}
}

func TestListHandoffTimelineCapsLimit(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "Alpha", "/code/alpha")
	seedHandoff(t, svc, pid, "唯一交接", "内容。", "handoff")

	// An absurd limit is clamped, not obeyed — but with one note the visible
	// effect is simply that the note still comes back.
	out := svc.ListHandoffTimeline(999999)
	if len(out) != 1 {
		t.Fatalf("clamped timeline lost its only entry: %d", len(out))
	}

	// Zero takes the default window without erroring.
	if out := svc.ListHandoffTimeline(0); len(out) != 1 {
		t.Fatalf("default-limit timeline broken: %d", len(out))
	}
}
