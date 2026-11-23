package service

import (
	"strings"
	"testing"
	"unicode/utf8"

	"repotory/internal/db"
)

// The brief is the SessionStart injection document (ADR-0066): it must carry
// the previous session's exit record and the approved knowledge, stay inside
// its budget, and never leak a pending page — injection is a read surface
// under the same human approval gate as every other one (ADR-0015).
func TestBuildSessionBriefContentBudgetAndApprovalGate(t *testing.T) {
	s, _ := setupService(t)
	pid := seedProject(t, s.db, "demo", "/home/me/demo")
	if _, err := s.CreateHandoffNote(HandoffInput{
		ProjectID: pid,
		Summary:   strings.Repeat("S", 3800),
		Gotchas:   []string{"BRIEF-GOTCHA-MARK"},
		NextSteps: []string{"BRIEF-NEXTSTEP-MARK"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTodo(pid, "BRIEF-TODO-MARK"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateWikiPage(s.db, db.WikiKindEntity, "approved-page", "BRIEF-APPROVED-PAGE", pid, "approved body"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateWikiPageAs(s.db, db.WikiKindEntity, "pending-page", "BRIEF-PENDING-PAGE", pid, "PENDING-SECRET-BODY", db.WikiStatusPending, "wiki-compile"); err != nil {
		t.Fatal(err)
	}

	doc := s.BuildSessionBrief(s.ResolveProject("demo"))
	if len(doc) > briefMaxChars {
		t.Errorf("brief is %d bytes, budget is %d", len(doc), briefMaxChars)
	}
	for _, want := range []string{"demo", "BRIEF-GOTCHA-MARK", "BRIEF-NEXTSTEP-MARK", "BRIEF-TODO-MARK", "BRIEF-APPROVED-PAGE"} {
		if !strings.Contains(doc, want) {
			t.Errorf("%q missing from brief:\n%s", want, doc)
		}
	}
	for _, banned := range []string{"BRIEF-PENDING-PAGE", "PENDING-SECRET-BODY"} {
		if strings.Contains(doc, banned) {
			t.Errorf("pending page leaked into the brief: %q", banned)
		}
	}
	// Gotchas / Next Steps lead the long Summary, so the budget cut drops the
	// Summary tail, not the warnings.
	if gotcha, summary := strings.Index(doc, "BRIEF-GOTCHA-MARK"), strings.Index(doc, strings.Repeat("S", 20)); gotcha == -1 || summary == -1 || gotcha > summary {
		t.Errorf("Gotchas should lead the long Summary:\n%s", doc)
	}
}

// An uncollected folder is a silent no-op, not a catalog or an error message:
// the hook injects whatever this returns into EVERY session start.
func TestBuildSessionBriefUnresolvedIsSilent(t *testing.T) {
	s, _ := setupService(t)
	seedProject(t, s.db, "one", "/home/me/one")
	seedProject(t, s.db, "two", "/home/me/two")

	if doc := s.BuildSessionBrief(nil); doc != "" {
		t.Errorf("nil resolution should be silent, got:\n%s", doc)
	}
	if doc := s.BuildSessionBrief(s.ResolveWorkspace("", "/definitely/not/collected")); doc != "" {
		t.Errorf("unresolved workspace should be silent, got:\n%s", doc)
	}
}

// Same rule as the full context: a newer auto-captured record must not push a
// human handoff out of the lead position.
func TestBuildSessionBriefHumanHandoffLeads(t *testing.T) {
	s, _ := setupService(t)
	pid := seedProject(t, s.db, "demo", "/home/me/demo")
	human, err := s.CreateHandoffNote(HandoffInput{
		ProjectID: pid, Summary: "HUMAN-BRIEF-SUMMARY", Gotchas: []string{"HUMAN-BRIEF-GOTCHA"},
	})
	if err != nil {
		t.Fatal(err)
	}
	auto, err := s.CreateHandoffNote(HandoffInput{
		ProjectID: pid, Summary: "AUTO-BRIEF-SUMMARY", Tags: "auto-captured", NextSteps: []string{"step"},
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

	doc := s.BuildSessionBrief(s.ResolveProject("demo"))
	if !strings.Contains(doc, "HUMAN-BRIEF-GOTCHA") {
		t.Errorf("human handoff should lead:\n%s", doc)
	}
	leadIdx := strings.Index(doc, "HUMAN-BRIEF-GOTCHA")
	autoIdx := strings.Index(doc, "AUTO-BRIEF-SUMMARY")
	if autoIdx != -1 && autoIdx < leadIdx {
		t.Errorf("auto-captured record precedes the human handoff:\n%s", doc)
	}
}

// A CJK handoff cut at the byte budget must not leave a broken rune in the
// injected document.
func TestBuildSessionBriefRuneSafeTruncation(t *testing.T) {
	s, _ := setupService(t)
	pid := seedProject(t, s.db, "demo", "/home/me/demo")
	if _, err := s.CreateHandoffNote(HandoffInput{
		ProjectID: pid, Summary: strings.Repeat("汉", 1300), NextSteps: []string{"下一步"},
	}); err != nil {
		t.Fatal(err)
	}
	doc := s.BuildSessionBrief(s.ResolveProject("demo"))
	if !utf8.ValidString(doc) {
		t.Error("brief contains invalid UTF-8")
	}
	if len(doc) > briefMaxChars {
		t.Errorf("brief is %d bytes, budget is %d", len(doc), briefMaxChars)
	}
}
