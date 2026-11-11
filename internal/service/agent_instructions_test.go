package service

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"repotory/internal/agentinit"
)

// The collected-project instruction checklist: the audit behind ADR-0054's
// successor flow found that the handoff protocol idled for two weeks because
// nobody ever ran the setup that writes the ADR-0043 instruction block. The
// checklist asks "does this project's directory carry the block", and offers a
// one-click top-up that writes the block and NOTHING else.

// seedCollectedProject seeds a plain (non-container) collected project: the
// checklist only ever speaks about projects a user actually works in, while
// the generic seedProject helper creates scan-time containers
// (is_auto_grouped), which refusalForProject rightly refuses.
func seedCollectedProject(t *testing.T, database *sql.DB, name, root string) int64 {
	t.Helper()
	pid := seedProject(t, database, name, root)
	if _, err := database.Exec("UPDATE projects SET is_auto_grouped = 0, collected = 1, collected_at = datetime('now') WHERE id = ?", pid); err != nil {
		t.Fatal(err)
	}
	return pid
}

// successor flow found that the handoff protocol idled for two weeks because
// nobody ever ran the setup that writes the ADR-0043 instruction block. The
// checklist asks "does this project's directory carry the block", and offers a
// one-click top-up that writes the block and NOTHING else.

// A project directory with no instruction file answers "missing", with both
// standard names listed so the UI can say which file is absent.
func TestAgentInstructionsStatusMissing(t *testing.T) {
	s, _ := setupService(t)
	dir := t.TempDir()
	pid := seedCollectedProject(t, s.db, "bare", dir)

	st, err := s.AgentInstructionsStatusFor(pid)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != AgentInstrStateMissing {
		t.Errorf("state = %q, want %q", st.State, AgentInstrStateMissing)
	}
	if len(st.Files) != 0 {
		t.Errorf("Files = %v, want none", st.Files)
	}
	if len(st.Missing) != len(agentinit.InstructionFiles) {
		t.Errorf("Missing = %v, want both standard names", st.Missing)
	}
}

// An instruction file that exists but carries no managed block answers
// "no_block": the file is the user's, so Cinder offers the top-up instead of
// claiming everything is fine (or appending silently).
func TestAgentInstructionsStatusNoBlock(t *testing.T) {
	s, _ := setupService(t)
	dir := t.TempDir()
	pid := seedCollectedProject(t, s.db, "handwritten", dir)

	if err := writeFile(filepath.Join(dir, "AGENTS.md"), "# My project\n\nMy own instructions.\n"); err != nil {
		t.Fatal(err)
	}

	st, err := s.AgentInstructionsStatusFor(pid)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != AgentInstrStateNoBlock {
		t.Errorf("state = %q, want %q", st.State, AgentInstrStateNoBlock)
	}
	if len(st.Files) != 1 || st.Files[0] != "AGENTS.md" {
		t.Errorf("Files = %v, want [AGENTS.md]", st.Files)
	}
}

// A file carrying the managed marker answers "ok". The marker check is what
// makes the checklist mean "the memory layer is wired", not just "a file with
// that name happens to exist".
func TestAgentInstructionsStatusOK(t *testing.T) {
	s, _ := setupService(t)
	dir := t.TempDir()
	pid := seedCollectedProject(t, s.db, "wired", dir)

	content := "# My project\n\n" + agentinit.InstructionsBeginMark + "\nblock\n" + agentinit.InstructionsEndMark + "\n"
	if err := writeFile(filepath.Join(dir, "CLAUDE.md"), content); err != nil {
		t.Fatal(err)
	}

	st, err := s.AgentInstructionsStatusFor(pid)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != AgentInstrStateOK {
		t.Errorf("state = %q, want %q", st.State, AgentInstrStateOK)
	}
}

// Containers and the synthetic default row answer "skipped": the checklist
// must not nag about directories nobody works in.
func TestAgentInstructionsStatusSkipsContainers(t *testing.T) {
	s, _ := setupService(t)
	pid := seedProject(t, s.db, "container", t.TempDir())
	if _, err := s.db.Exec("UPDATE projects SET is_auto_grouped = 1 WHERE id = ?", pid); err != nil {
		t.Fatal(err)
	}

	st, err := s.AgentInstructionsStatusFor(pid)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != AgentInstrStateSkipped {
		t.Errorf("state = %q, want %q", st.State, AgentInstrStateSkipped)
	}
}

// The one-click top-up writes the block into both instruction files and
// nothing else: no client configs, no hook. After it, the status is "ok", and
// a second install is a no-op (idempotent, byte-identical).
func TestInstallAgentInstructionsTopsUpAndStaysIdempotent(t *testing.T) {
	s, _ := setupService(t)
	dir := t.TempDir()
	pid := seedCollectedProject(t, s.db, "topup", dir)

	report, err := s.InstallAgentInstructions(pid)
	if err != nil {
		t.Fatal(err)
	}
	if report.Code != AgentSetupOK || report.Blocking {
		t.Fatalf("install refused: code=%q blocking=%v warnings=%v", report.Code, report.Blocking, report.Warnings)
	}
	if len(report.Instructions) != len(agentinit.InstructionFiles) {
		t.Fatalf("Instructions = %d entries, want %d", len(report.Instructions), len(agentinit.InstructionFiles))
	}

	st, err := s.AgentInstructionsStatusFor(pid)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != AgentInstrStateOK {
		t.Errorf("state after install = %q, want %q", st.State, AgentInstrStateOK)
	}

	report2, err := s.InstallAgentInstructions(pid)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range report2.Instructions {
		if f.Status != "unchanged" {
			t.Errorf("second install: %s = %s, want unchanged", f.Path, f.Status)
		}
	}
}

// A container refuses the top-up with the same code SetupAgentClients uses,
// and nothing is written.
func TestInstallAgentInstructionsRefusesContainers(t *testing.T) {
	s, _ := setupService(t)
	dir := t.TempDir()
	pid := seedProject(t, s.db, "container", dir)
	if _, err := s.db.Exec("UPDATE projects SET is_auto_grouped = 1 WHERE id = ?", pid); err != nil {
		t.Fatal(err)
	}

	report, err := s.InstallAgentInstructions(pid)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Blocking || report.Code != AgentSetupAutoGroupedProject {
		t.Fatalf("report = code=%q blocking=%v, want blocking %q", report.Code, report.Blocking, AgentSetupAutoGroupedProject)
	}
	st, err := s.AgentInstructionsStatusFor(pid)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != AgentInstrStateSkipped {
		t.Errorf("state = %q, want %q (nothing written)", st.State, AgentInstrStateSkipped)
	}
}

// The dashboard badge counts only collected projects that lack the block:
// un-collected directories and already-wired projects must not move it.
func TestMissingAgentInstructionsCount(t *testing.T) {
	s, _ := setupService(t)
	bare := t.TempDir()
	wired := t.TempDir()
	seedCollectedProject(t, s.db, "bare", bare)
	seedCollectedProject(t, s.db, "wired", wired)
	content := agentinit.InstructionsBeginMark + "\nblock\n" + agentinit.InstructionsEndMark + "\n"
	if err := writeFile(filepath.Join(wired, "AGENTS.md"), content); err != nil {
		t.Fatal(err)
	}
	// An un-collected project with no block: outside the checklist's scope.
	seedProject(t, s.db, "uncollected", t.TempDir())
	if _, err := s.db.Exec("UPDATE projects SET collected = 0 WHERE name = 'uncollected'"); err != nil {
		t.Fatal(err)
	}

	if got := s.MissingAgentInstructionsCount(); got != 1 {
		t.Errorf("count = %d, want 1 (only the bare collected project)", got)
	}

	// Topping the bare project up drops the count to zero.
	var bareID int64
	if err := s.db.QueryRow("SELECT id FROM projects WHERE name = 'bare'").Scan(&bareID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InstallAgentInstructions(bareID); err != nil {
		t.Fatal(err)
	}
	if got := s.MissingAgentInstructionsCount(); got != 0 {
		t.Errorf("count after top-up = %d, want 0", got)
	}
}

// writeFile is the test-local helper for instruction files; it keeps the
// permission bits explicit so the checklist's read path is exercised the same
// way on every platform.
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
