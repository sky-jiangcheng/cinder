package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"repotory/internal/agentinit"
	"repotory/internal/db"
	"repotory/internal/domain"
)

// The desktop "connect an AI client in one click" path.
//
// The wiring is internal/agentinit's job and is pinned there. What these
// tests cover is the layer above it: which project may receive client
// configuration at all, and how a refusal reaches the UI — as a code, never
// as a sentence the backend would have to speak the user's language.

// setupAgentService returns a service plus a fake repotory-mcp binary and an
// isolated HOME, so a test never registers anything into the real user's
// home directory or depends on a real install.
func setupAgentService(t *testing.T) (*Service, string) {
	t.Helper()
	s, _ := setupService(t)

	bin := filepath.Join(t.TempDir(), "repotory-mcp")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REPOTORY_MCP_BIN", bin)
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome) // os.UserHomeDir reads this on Windows
	return s, bin
}

// seedManualProject inserts a project that is NOT auto-grouped — the shape a
// real scanned repository has.
func seedManualProject(t *testing.T, s *Service, name, root string) int64 {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := db.SyncProjectTx(tx, name, root, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return pid
}

func TestSetupAgentClientsWritesTheClientConfig(t *testing.T) {
	s, bin := setupAgentService(t)
	projectDir := t.TempDir()
	pid := seedManualProject(t, s, "demo", projectDir)

	report, err := s.SetupAgentClients(pid, []string{"claude"}, false, false)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if report.Code != AgentSetupOK {
		t.Fatalf("code = %q, want empty", report.Code)
	}
	if report.Blocking {
		t.Fatal("a successful setup must not be blocking")
	}
	if len(report.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(report.Files))
	}
	if report.Files[0].Status != "created" {
		t.Fatalf("status = %q, want created", report.Files[0].Status)
	}

	raw, err := os.ReadFile(filepath.Join(projectDir, ".mcp.json"))
	if err != nil {
		t.Fatalf("read .mcp.json: %v", err)
	}
	var parsed struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse .mcp.json: %v", err)
	}
	entry, ok := parsed.Servers["repotory"]
	if !ok {
		t.Fatalf(".mcp.json has no repotory server: %s", raw)
	}
	if entry.Command != bin {
		t.Fatalf("command = %q, want %q", entry.Command, bin)
	}
}

// Registering twice must be a no-op the second time, not a rewrite: the
// button is idempotent so a user can press it twice without thinking.
func TestSetupAgentClientsIsIdempotent(t *testing.T) {
	s, _ := setupAgentService(t)
	projectDir := t.TempDir()
	pid := seedManualProject(t, s, "demo", projectDir)

	if _, err := s.SetupAgentClients(pid, []string{"claude"}, false, false); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	report, err := s.SetupAgentClients(pid, []string{"claude"}, false, false)
	if err != nil {
		t.Fatalf("second setup: %v", err)
	}
	if report.Files[0].Status != "unchanged" {
		t.Fatalf("status = %q, want unchanged", report.Files[0].Status)
	}
}

func TestSetupAgentClientsInstallsTheHook(t *testing.T) {
	s, _ := setupAgentService(t)
	projectDir := t.TempDir()
	pid := seedManualProject(t, s, "demo", projectDir)

	report, err := s.SetupAgentClients(pid, []string{"claude"}, true, false)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if report.Hook == nil || report.HookSettings == nil {
		t.Fatalf("the hook was requested but not installed: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".claude", "hooks", "repotory-handoff.sh")); err != nil {
		t.Fatalf("hook script missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".claude", "settings.json")); err != nil {
		t.Fatalf("settings.json missing: %v", err)
	}
}

// An auto-grouped project is a scan-time container (usually the home
// directory), not a project the user works in. Its .claude/settings.json
// would apply the SessionEnd hook to every project underneath it, so the
// registration must be refused.
func TestSetupAgentClientsRefusesAutoGroupedProject(t *testing.T) {
	s, _ := setupAgentService(t)
	containerDir := t.TempDir()
	pid := seedProject(t, s.db, "container", containerDir)

	report, err := s.SetupAgentClients(pid, []string{"claude"}, false, false)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if report.Code != AgentSetupAutoGroupedProject {
		t.Fatalf("code = %q, want %q", report.Code, AgentSetupAutoGroupedProject)
	}
	if !report.Blocking {
		t.Fatal("a refused setup must be blocking")
	}
	if len(report.Files) != 0 {
		t.Fatalf("a refused setup must not write anything: %+v", report.Files)
	}
	if _, err := os.Stat(filepath.Join(containerDir, ".mcp.json")); !os.IsNotExist(err) {
		t.Fatal("a refused setup must not have written .mcp.json")
	}
}

// The synthetic "未分类" row has no directory at all: its root_path is a
// sentinel, not a path.
func TestSetupAgentClientsRefusesTheVirtualProject(t *testing.T) {
	s, _ := setupAgentService(t)

	var pid int64
	if err := s.db.QueryRow("SELECT id FROM projects WHERE root_path = ?", domain.DefaultProjectRootPath).Scan(&pid); err != nil {
		t.Skipf("no default project in this database: %v", err)
	}

	report, err := s.SetupAgentClients(pid, []string{"claude"}, false, false)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if report.Code != AgentSetupVirtualProject {
		t.Fatalf("code = %q, want %q", report.Code, AgentSetupVirtualProject)
	}
}

func TestSetupAgentClientsRefusesAnUnknownClient(t *testing.T) {
	s, _ := setupAgentService(t)
	projectDir := t.TempDir()
	pid := seedManualProject(t, s, "demo", projectDir)

	report, err := s.SetupAgentClients(pid, []string{"emacs"}, false, false)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if report.Code != AgentSetupBadClient {
		t.Fatalf("code = %q, want %q", report.Code, AgentSetupBadClient)
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".mcp.json")); !os.IsNotExist(err) {
		t.Fatal("a refused setup must not have written .mcp.json")
	}
}

// Unchecking every client must not be read as "do all of them": the empty
// selection is a refusal, because writing into ~/.codeium because the user
// unchecked the box is not a decision the button may take for them.
func TestSetupAgentClientsRefusesAnEmptySelection(t *testing.T) {
	s, _ := setupAgentService(t)
	projectDir := t.TempDir()
	pid := seedManualProject(t, s, "demo", projectDir)

	report, err := s.SetupAgentClients(pid, nil, false, false)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if report.Code != AgentSetupNoClient {
		t.Fatalf("code = %q, want %q", report.Code, AgentSetupNoClient)
	}
	if len(report.Files) != 0 {
		t.Fatalf("an empty selection must write nothing: %+v", report.Files)
	}
}

func TestSetupAgentClientsReportsAMissingDirectory(t *testing.T) {
	s, _ := setupAgentService(t)
	pid := seedManualProject(t, s, "gone", filepath.Join(t.TempDir(), "deleted"))

	report, err := s.SetupAgentClients(pid, []string{"claude"}, false, false)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if report.Code != AgentSetupMissingDirectory {
		t.Fatalf("code = %q, want %q", report.Code, AgentSetupMissingDirectory)
	}
	if !report.Blocking {
		t.Fatal("a missing directory must be blocking")
	}
}

func TestSetupAgentClientsReportsAnUnknownProject(t *testing.T) {
	s, _ := setupAgentService(t)

	report, err := s.SetupAgentClients(99999, []string{"claude"}, false, false)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if report.Code != AgentSetupProjectNotFound {
		t.Fatalf("code = %q, want %q", report.Code, AgentSetupProjectNotFound)
	}
}

// The client list the UI renders must be the backend's list, so a client the
// backend cannot register is never offered as a checkbox.
func TestAgentClientNamesMatchesTheRegistry(t *testing.T) {
	names := AgentClientNames()
	want := make([]string, 0, len(agentinit.AllClients))
	for _, c := range agentinit.AllClients {
		want = append(want, string(c))
	}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for i, n := range names {
		if n != want[i] {
			t.Fatalf("names = %v, want %v", names, want)
		}
	}
}

// ADR-0043: the instruction block is the protocol-neutral half of the fix for
// "zero handoffs while the tools were registered". The hook only exists for
// Claude Code; AGENTS.md is read by every agent that reads it. This test goes
// through the real SetupAgentClients path — the binding, not the package —
// because the earlier defect in this area (UpdateConfig blocking free-text
// config keys) lived exactly at that seam.
func TestSetupAgentClientsWritesInstructionFiles(t *testing.T) {
	s, _ := setupAgentService(t)
	projectDir := t.TempDir()
	pid := seedManualProject(t, s, "demo", projectDir)

	report, err := s.SetupAgentClients(pid, []string{"claude"}, false, true)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if report.Code != AgentSetupOK {
		t.Fatalf("code = %q, want empty", report.Code)
	}
	if len(report.Instructions) != len(agentinit.InstructionFiles) {
		t.Fatalf("instructions = %d, want %d", len(report.Instructions), len(agentinit.InstructionFiles))
	}
	for i, rel := range agentinit.InstructionFiles {
		got := report.Instructions[i]
		if got.Status != "created" {
			t.Errorf("%s: status = %q, want created (%s)", rel, got.Status, got.Note)
		}
		raw, err := os.ReadFile(filepath.Join(projectDir, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if !strings.Contains(string(raw), "repotory_handoff") {
			t.Errorf("%s: the block must name the tool the agent is being asked to call", rel)
		}
	}

	// Clicking the button twice must not stack two blocks: an agent reading the
	// same instruction twice every session is a bug the user cannot see.
	again, err := s.SetupAgentClients(pid, []string{"claude"}, false, true)
	if err != nil {
		t.Fatalf("second setup: %v", err)
	}
	for i, rel := range agentinit.InstructionFiles {
		if got := again.Instructions[i]; got.Status != "unchanged" {
			t.Errorf("%s: second run status = %q, want unchanged", rel, got.Status)
		}
	}
}

// The two switches stay independent (ADR-0043 decision 5): a user who installs
// the hook must not silently get their repository's AGENTS.md rewritten, and a
// user who only wants the instruction file must not get a Claude Code hook.
// Writing into tracked files is a setup act on the user's machine.
func TestSetupAgentClientsInstructionAndHookAreIndependent(t *testing.T) {
	s, _ := setupAgentService(t)
	projectDir := t.TempDir()
	pid := seedManualProject(t, s, "demo", projectDir)

	hookOnly, err := s.SetupAgentClients(pid, []string{"claude"}, true, false)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if hookOnly.Hook == nil {
		t.Fatal("the hook was asked for and must be reported")
	}
	if hookOnly.Instructions != nil {
		t.Errorf("instructions requested off, got %v", hookOnly.Instructions)
	}
	for _, rel := range agentinit.InstructionFiles {
		if _, err := os.Stat(filepath.Join(projectDir, rel)); err == nil {
			t.Errorf("%s was written although withInstructions was false", rel)
		}
	}

	instrOnly, err := s.SetupAgentClients(pid, []string{"claude"}, false, true)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if instrOnly.Hook != nil {
		t.Error("the hook was not asked for and must not be reported")
	}
	if len(instrOnly.Instructions) != len(agentinit.InstructionFiles) {
		t.Errorf("instructions = %d, want %d", len(instrOnly.Instructions), len(agentinit.InstructionFiles))
	}
}
