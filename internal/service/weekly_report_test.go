package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"repotory/internal/db"
)

func weeklyFixture(t *testing.T, svc *Service) int64 {
	t.Helper()
	projectID := seedProject(t, svc.db, "weekly", "/tmp/weekly")
	if _, err := db.CreateNoteEx(svc.db, projectID, "重试与幂等",
		"支付网关最多重试三次，幂等键用 invoice id。", "gateway", "knowledge", "manual"); err != nil {
		t.Fatal(err)
	}
	return projectID
}

// A week that has not happened has no material, and a report about it would be
// invented. This is refused before any model call.
func TestGenerateWeeklyReport_RefusesAFutureWeek(t *testing.T) {
	svc, _ := setupService(t)
	projectID := weeklyFixture(t, svc)
	if _, err := svc.GenerateWeeklyReport(context.Background(), projectID, 1); err == nil {
		t.Fatal("a future week must be refused")
	}
}

// "Nothing happened" is information; a page of plausible filler is not. With
// no notes touched and no repository activity, this is an error.
func TestGenerateWeeklyReport_EmptyWeekIsAnError(t *testing.T) {
	svc, _ := setupService(t)
	projectID := seedProject(t, svc.db, "quiet", "/tmp/quiet")
	_, err := svc.GenerateWeeklyReport(context.Background(), projectID, 0)
	if err == nil {
		t.Fatal("an empty week must not produce a report")
	}
	if !strings.Contains(err.Error(), "没有可汇总的活动") {
		t.Fatalf("the refusal must say why: %v", err)
	}
}

// The proposal is OrganizeNote-shaped: title, tags and body parsed from the
// three markers, and nothing written to the store.
func TestGenerateWeeklyReport_Proposal(t *testing.T) {
	svc, _ := setupService(t)
	projectID := weeklyFixture(t, svc)
	stub, prompt := chatStubCapture(t, strings.Join([]string{
		organizeMarkerTitle,
		"weekly 周报 · 11-23 ~ 11-29",
		organizeMarkerTags,
		"周报",
		organizeMarkerContent,
		"## 本周做了什么\n\n重试与幂等的边界被写清楚了。",
	}, "\n"), "")
	configureOrganize(t, svc, stub.URL)

	got, err := svc.GenerateWeeklyReport(context.Background(), projectID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "weekly 周报 · 11-23 ~ 11-29" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Tags != "周报" {
		t.Errorf("tags = %q", got.Tags)
	}
	if !strings.Contains(got.Content, "重试与幂等的边界") {
		t.Errorf("content = %q", got.Content)
	}
	// A new note, not a rewrite: note_id 0 tells the caller there is nothing
	// to update.
	if got.NoteID != 0 {
		t.Errorf("note id = %d, want 0 for a new note", got.NoteID)
	}
	// The week's material reaches the model, and the prompt says it is data.
	if !strings.Contains(*prompt, "重试与幂等") {
		t.Errorf("the note is missing from the prompt:\n%s", *prompt)
	}
	if !strings.Contains(*prompt, "不可信数据") {
		t.Errorf("the prompt does not mark its material as untrusted:\n%s", *prompt)
	}
}

// Sunday belongs to the week that started on Monday. Getting this backwards
// shifts the whole window by a day and silently drops Sunday's work.
func TestWeeklyWindow_SundayClosesItsOwnWeek(t *testing.T) {
	loc := time.UTC
	sunday := time.Date(2026, 11, 29, 15, 0, 0, 0, loc) // a Sunday
	from, to := weeklyWindow(sunday, 0)
	if from.Day() != 23 {
		t.Fatalf("week start = %v, want the 23rd", from)
	}
	if !to.Equal(time.Date(2026, 11, 30, 0, 0, 0, 0, loc)) {
		t.Fatalf("week end = %v", to)
	}
	prevFrom, _ := weeklyWindow(sunday, -1)
	if prevFrom.Day() != 16 {
		t.Fatalf("previous week start = %v, want the 16th", prevFrom)
	}
}

// A window past the last scan is stated as missing data. Reporting "0 commits"
// for days nobody looked is the failure the metrics contract warns about.
func TestRenderWeekActivity_StaleSaysSo(t *testing.T) {
	zero, add, del := 0, 0, 0
	stale := &Metrics{Stale: true, Commits: &zero, Additions: &add, Deletions: &del}
	text := renderWeekActivity(stale, "2026-11-16", "2026-11-22")
	if !strings.Contains(text, "超出最后一次扫描") || !strings.Contains(text, "不是零活动") {
		t.Fatalf("a stale window must say it is stale: %s", text)
	}
}

// A nil number is "no trustworthy figure", never 0.
func TestRenderWeekActivity_NilNumbersAreNotZero(t *testing.T) {
	text := renderWeekActivity(&Metrics{}, "2026-11-16", "2026-11-22")
	if strings.Contains(text, "提交 0 次") {
		t.Fatalf("absent numbers rendered as zero: %s", text)
	}
	if !strings.Contains(text, "没有可信统计") {
		t.Fatalf("the absent-number case must say so: %s", text)
	}
}
