package service

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"repotory/internal/platform"
)

// bigSection builds a section at the handoff write bounds so one handoff
// can push the assembled context past contextMaxChars.
func bigSection(prefix string) []string {
	out := make([]string, 20)
	for i := range out {
		out[i] = fmt.Sprintf("%s-%02d-%s", prefix, i, strings.Repeat("x", 950))
	}
	return out
}

func TestBuildProjectContextFitsTotalBudget(t *testing.T) {
	s, _ := setupService(t)
	pid := seedProject(t, s.db, "demo", "/home/me/demo")
	for i := 0; i < 12; i++ {
		if _, err := s.CreateNoteWithMeta(pid, fmt.Sprintf("note-%02d", i), strings.Repeat("n", 1200), "", "knowledge", "manual"); err != nil {
			t.Fatal(err)
		}
	}
	gotchas := bigSection("G")
	gotchas[0] = "GOTCHA-FIRST-MARK"
	if _, err := s.CreateHandoffNote(HandoffInput{
		ProjectID: pid,
		Summary:   strings.Repeat("S", 3800),
		Changes:   bigSection("C"),
		Decisions: bigSection("D"),
		Gotchas:   gotchas,
		NextSteps: bigSection("N"),
	}); err != nil {
		t.Fatal(err)
	}

	doc := s.BuildProjectContext(s.ResolveProject("demo"))
	if len(doc) > contextMaxChars+256 {
		t.Errorf("context doc is %d bytes, budget is %d", len(doc), contextMaxChars)
	}
	if !strings.Contains(doc, "notes /") || !strings.Contains(doc, "handoffs omitted") {
		t.Errorf("missing omission marker:\n%s", doc)
	}
	if !strings.Contains(doc, "GOTCHA-FIRST-MARK") {
		t.Error("priority Gotchas section was cut")
	}
	if !strings.Contains(doc, "\n\n...") {
		t.Error("missing truncation marker")
	}
}

// A long summary used to push Gotchas / Next Steps past the 4 KB cap; the
// section-priority ordering now keeps them.
func TestBuildProjectContextKeepsGotchasAndNextSteps(t *testing.T) {
	s, _ := setupService(t)
	pid := seedProject(t, s.db, "demo", "/home/me/demo")
	if _, err := s.CreateHandoffNote(HandoffInput{
		ProjectID: pid,
		Summary:   strings.Repeat("S", 3800),
		Gotchas:   []string{"GOTCHA-MARK"},
		NextSteps: []string{"NEXTSTEP-MARK"},
	}); err != nil {
		t.Fatal(err)
	}

	doc := s.BuildProjectContext(s.ResolveProject("demo"))
	for _, want := range []string{"GOTCHA-MARK", "NEXTSTEP-MARK"} {
		if !strings.Contains(doc, want) {
			t.Errorf("%q was cut from the handoff:\n%s", want, doc)
		}
	}
	if gotcha, summary := strings.Index(doc, "GOTCHA-MARK"), strings.Index(doc, strings.Repeat("S", 20)); gotcha == -1 || summary == -1 || gotcha > summary {
		t.Errorf("Gotchas should lead the long Summary:\n%s", doc)
	}
}

func TestBuildProjectContextTimelineCappedAndSorted(t *testing.T) {
	s, _ := setupService(t)
	pid := seedProject(t, s.db, "demo", "/home/me/demo")
	for i := 0; i < 12; i++ {
		res, err := s.CreateHandoffNote(HandoffInput{
			ProjectID: pid,
			Summary:   fmt.Sprintf("UNIQUE-SESSION-%02d-END", i),
			NextSteps: []string{"step"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec("UPDATE project_notes SET updated_at = ? WHERE id = ?", fmt.Sprintf("2026-09-%02d 10:00:00.000", i+1), res.NoteID); err != nil {
			t.Fatal(err)
		}
	}

	doc := s.BuildProjectContext(s.ResolveProject("demo"))
	// Newest (11) is the full handoff; the timeline keeps the next 10 (10..1).
	if !strings.Contains(doc, "UNIQUE-SESSION-10-END") {
		t.Error("newest timeline entry missing")
	}
	if strings.Contains(doc, "UNIQUE-SESSION-00-END") {
		t.Error("timeline exceeded its cap")
	}
	if newest, older := strings.Index(doc, "UNIQUE-SESSION-10-END"), strings.Index(doc, "UNIQUE-SESSION-01-END"); newest == -1 || older == -1 || newest > older {
		t.Error("timeline is not newest-first")
	}
}

// A newer auto-captured record must not compress a human handoff into the
// one-line timeline.
func TestBuildProjectContextHumanHandoffBeatsAutoCapture(t *testing.T) {
	s, _ := setupService(t)
	pid := seedProject(t, s.db, "demo", "/home/me/demo")
	human, err := s.CreateHandoffNote(HandoffInput{
		ProjectID: pid, Summary: "HUMAN-SUMMARY", Gotchas: []string{"HUMAN-GOTCHA"},
	})
	if err != nil {
		t.Fatal(err)
	}
	auto, err := s.CreateHandoffNote(HandoffInput{
		ProjectID: pid, Summary: "AUTO-SUMMARY-MARK", Tags: "auto-captured", NextSteps: []string{"auto step"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE project_notes SET updated_at = '2026-09-01 10:00:00.000' WHERE id = ?", human.NoteID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE project_notes SET updated_at = '2026-09-02 10:00:00.000' WHERE id = ?", auto.NoteID); err != nil {
		t.Fatal(err)
	}

	doc := s.BuildProjectContext(s.ResolveProject("demo"))
	if !strings.Contains(doc, "HUMAN-GOTCHA") {
		t.Errorf("human handoff should render in full:\n%s", doc)
	}
	timelineIdx := strings.Index(doc, "### Earlier Handoffs")
	autoIdx := strings.Index(doc, "AUTO-SUMMARY-MARK")
	if timelineIdx == -1 || autoIdx == -1 || autoIdx < timelineIdx {
		t.Errorf("auto-captured record should be demoted to the timeline:\n%s", doc)
	}
	if fullIdx := strings.Index(doc, "HUMAN-GOTCHA"); timelineIdx == -1 || fullIdx > timelineIdx {
		t.Errorf("human handoff should lead the timeline:\n%s", doc)
	}
}

// With no human handoff available the newest auto-captured record still
// leads, but is labelled so the reader knows it is not a session summary.
func TestBuildProjectContextLabelsAllAutoCaptured(t *testing.T) {
	s, _ := setupService(t)
	pid := seedProject(t, s.db, "demo", "/home/me/demo")
	if _, err := s.CreateHandoffNote(HandoffInput{
		ProjectID: pid, Summary: "AUTO-ONLY-SUMMARY", Tags: "auto-captured", NextSteps: []string{"step"},
	}); err != nil {
		t.Fatal(err)
	}
	doc := s.BuildProjectContext(s.ResolveProject("demo"))
	if !strings.Contains(doc, "AUTO-ONLY-SUMMARY") || !strings.Contains(doc, "Auto-captured record") {
		t.Errorf("auto-captured handoff should lead but be labelled:\n%s", doc)
	}
}

// A failed SessionEnd capture leaves a pending file in the data dir (never
// the knowledge layer); the next context call must hint at it.
func TestBuildProjectContextReportsPendingCapture(t *testing.T) {
	s, _ := setupService(t)
	pid := seedProject(t, s.db, "demo", "/home/me/demo")
	t.Setenv("HOME", t.TempDir())
	if err := platform.AppendCapturePending("/work/demo", "capture failed"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(platform.ClearCapturePending)

	doc := s.BuildProjectContext(s.ResolveProject("demo"))
	if !strings.Contains(doc, "auto-capture failure") {
		t.Errorf("missing pending hint:\n%s", doc)
	}
	if !strings.Contains(doc, platform.CapturePendingPath()) {
		t.Error("hint should point at the pending file")
	}
	if notes := s.ListNotes(pid); len(notes) != 0 {
		t.Errorf("pending record leaked into the knowledge layer: %d notes", len(notes))
	}
}

// Plain-note bodies are clipped with truncateBytes, so a CJK cut never
// leaves a broken rune in the context doc.
func TestBuildProjectContextNoteBodyUsesRuneTruncation(t *testing.T) {
	s, _ := setupService(t)
	pid := seedProject(t, s.db, "demo", "/home/me/demo")
	if _, err := s.CreateNoteWithMeta(pid, "cjk", strings.Repeat("汉", 500), "", "knowledge", "manual"); err != nil {
		t.Fatal(err)
	}
	doc := s.BuildProjectContext(s.ResolveProject("demo"))
	if !utf8.ValidString(doc) {
		t.Error("context doc contains invalid UTF-8")
	}
	if !strings.Contains(doc, "### cjk") || !strings.Contains(doc, "\n\n...") {
		t.Error("note body should be truncated with a marker")
	}
}
