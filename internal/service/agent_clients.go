package service

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"repotory/internal/agentinit"
	"repotory/internal/db"
	"repotory/internal/domain"
)

// The desktop "connect an AI client in one click" path.
//
// The wiring itself lives in internal/agentinit (the Go twin of the CLI's
// repotory-init). This layer owns the two things the twin must not decide on
// its own:
//
//   - WHICH project the client files land in. The twin takes a directory; this
//     layer turns a project id into that directory, and refuses the projects
//     that have no meaningful one.
//   - HOW a refusal is reported. The backend does not speak the user's
//     language, so every refusal leaves here as a code, and the UI turns it
//     into a sentence in the user's locale.
//
// Why the desktop app needs this at all: the terminal path (repotory-init)
// assumes node and a shell. The desktop app is one self-contained binary, so
// the button that makes "click once and Claude Code sees the memory" possible
// needs the same wiring in Go — see internal/agentinit for the drift guard
// that keeps the two implementations writing the same bytes.

// The machine-readable reasons a registration can be refused.
//
// A refusal is data, not an error: the report IS the answer. The UI renders
// the code in the user's language (see web/src/pages/settings/ActionsTab.tsx).
const (
	// AgentSetupOK — the registration ran; the report says what it wrote.
	AgentSetupOK = ""
	// AgentSetupNoClient — the request selected no client. Refused rather than
	// defaulted: agentinit.Register treats an empty list as "all four", and
	// silently writing into ~/.codeium because the user unchecked everything is
	// not a decision this button may make for them.
	AgentSetupNoClient = "no_client"
	// AgentSetupBadClient — one of the requested client names is not known.
	AgentSetupBadClient = "bad_client"
	// AgentSetupProjectNotFound — no project carries that id.
	AgentSetupProjectNotFound = "project_not_found"
	// AgentSetupVirtualProject — the project is the synthetic "未分类" row
	// (repotory://memory/default). It has no directory to write into.
	AgentSetupVirtualProject = "virtual_project"
	// AgentSetupAutoGroupedProject — the project is a scan-time container
	// (is_auto_grouped), typically the user's home directory. A container is
	// not a project the user works in; its .claude/settings.json would apply
	// the SessionEnd hook to every project underneath it.
	AgentSetupAutoGroupedProject = "auto_grouped_project"
	// AgentSetupMissingDirectory — the project's root path is not a directory
	// on disk (deleted, renamed, or on an unmounted volume).
	AgentSetupMissingDirectory = "missing_directory"
	// AgentSetupBinaryMissing — repotory-mcp is not on this machine, so there
	// is nothing to register. The UI points at the install guide.
	AgentSetupBinaryMissing = "binary_missing"
)

// AgentSetupReport is what one registration attempt did, or why it did not.
//
// It mirrors agentinit.Report and adds the refusal code, so the UI can tell
// "nothing happened because you asked for a client that does not exist" apart
// from "the files were written, and here is what changed".
type AgentSetupReport struct {
	// Code is the machine-readable reason the setup was refused. Empty means
	// the registration ran.
	Code string `json:"code"`
	// Blocking marks a refusal: nothing was written.
	Blocking bool `json:"blocking"`
	// Binary is the repotory-mcp path that was registered.
	Binary string `json:"binary"`
	// Files lists the client config files, one entry per client.
	Files []agentinit.FileResult `json:"files"`
	// Hook is the installed SessionEnd hook script, when the hook was asked for.
	Hook *agentinit.FileResult `json:"hook,omitempty"`
	// HookSettings is the .claude/settings.json entry pointing at the hook.
	HookSettings *agentinit.FileResult `json:"hook_settings,omitempty"`
	// Instructions lists the project instruction files that received the managed
	// block, one entry per file (ADR-0043). nil when not requested.
	Instructions []agentinit.FileResult `json:"instructions,omitempty"`
	// Warnings collects the per-file failures. A registration that half
	// succeeded is reported, not hidden behind one boolean.
	Warnings []string `json:"warnings,omitempty"`
}

// AgentClientNames returns the AI clients this build can register.
//
// The UI asks for the list instead of hardcoding it: a client the backend
// cannot register must never be offered as a checkbox, and a client the
// backend gains later must show up without a frontend release.
func AgentClientNames() []string {
	out := make([]string, 0, len(agentinit.AllClients))
	for _, c := range agentinit.AllClients {
		out = append(out, string(c))
	}
	return out
}

// SetupAgentClients registers Cinder's MCP server with the AI clients the
// user picked, for one project.
//
// projectID names the project whose directory receives the client-local
// configuration. clients are the client names ("claude", "cursor", "vscode",
// "windsurf"); withHook additionally installs the Claude Code SessionEnd hook
// that writes the handoff note when a session ends; withInstructions writes the
// managed repotory_* block into the project's instruction files (ADR-0043) —
// the protocol-neutral half, since the hook only exists for Claude Code.
//
// The two are independent switches and neither replaces the other: the hook
// catches "the user forgot to wrap up", the instruction file catches "the user
// is not in Claude Code". Both land in the same CreateHandoffNote path, so a
// handoff never comes in two shapes.
//
// A refusal is returned as a code in the report, not as an error: "the project
// is a container, not a project" is an answer the UI can render, and it must
// not look like a crash. An error is reserved for the service failing to look
// at all (an unreadable database row).
func (s *Service) SetupAgentClients(projectID int64, clients []string, withHook, withInstructions bool) (AgentSetupReport, error) {
	var report AgentSetupReport

	selected, err := parseAgentClients(clients)
	if err != nil {
		report.Code = AgentSetupBadClient
		report.Blocking = true
		return report, nil
	}
	if len(selected) == 0 {
		report.Code = AgentSetupNoClient
		report.Blocking = true
		return report, nil
	}

	project, err := db.GetProjectByID(s.db, projectID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			report.Code = AgentSetupProjectNotFound
			report.Blocking = true
			return report, nil
		}
		return report, err
	}

	if code := refusalForProject(project); code != AgentSetupOK {
		report.Code = code
		report.Blocking = true
		return report, nil
	}

	dir := strings.TrimSpace(project.RootPath)
	if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
		report.Code = AgentSetupMissingDirectory
		report.Blocking = true
		return report, nil
	}

	res, err := agentinit.Register(agentinit.Options{
		TargetDir:        dir,
		Clients:          selected,
		WithHook:         withHook,
		WithInstructions: withInstructions,
	})
	if err != nil {
		if errors.Is(err, agentinit.ErrBinaryNotFound) {
			report.Code = AgentSetupBinaryMissing
			report.Blocking = true
			return report, nil
		}
		return report, err
	}

	report.Binary = res.Binary
	report.Files = res.Files
	report.Hook = res.Hook
	report.HookSettings = res.HookSettings
	report.Instructions = res.Instructions
	report.Warnings = res.Warnings
	return report, nil
}

// refusalForProject answers "can this project receive client configuration at
// all". Empty means yes.
func refusalForProject(p *domain.Project) string {
	if p.RootPath == domain.DefaultProjectRootPath {
		return AgentSetupVirtualProject
	}
	if p.IsAutoGrouped {
		return AgentSetupAutoGroupedProject
	}
	return AgentSetupOK
}

// parseAgentClients validates the requested client names and drops
// duplicates, keeping the order the user picked.
func parseAgentClients(names []string) ([]agentinit.Client, error) {
	out := make([]agentinit.Client, 0, len(names))
	seen := map[agentinit.Client]bool{}
	for _, name := range names {
		c, err := agentinit.ParseClient(name)
		if err != nil {
			return nil, err
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out, nil
}

// --- Collected-project instruction checklist -------------------------------
//
// The 2026-11-10 audit found the handoff feature idle for 15 days, and the
// first cause was boring: the projects the agents worked in never received the
// ADR-0043 instruction block ("call repotory_context at the start, write a
// handoff at the end"), so every session started blind. The block is written
// by SetupAgentClients / repotory-init — both opt-in setup acts nobody ran.
//
// This checklist closes that gap at the moment the user actually does something
// (collecting projects in a scan) instead of hoping they run a setup wizard:
// the desktop surfaces ask "does this project's directory carry the managed
// block in AGENTS.md or CLAUDE.md", and offer a one-click top-up that writes
// ONLY the instruction block — no client configs, no hook, no MCP binary
// dependency (agentinit.Options.InstructionsOnly). Writing into the user's
// tracked files stays a click the user makes, never a background side effect.

// The three states a project's instruction checklist can report, plus the
// not-applicable one for containers.
const (
	// AgentInstrStateOK — at least one instruction file exists AND carries the
	// managed repotory block.
	AgentInstrStateOK = "ok"
	// AgentInstrStateNoBlock — an instruction file exists but none of them
	// carries the block. The file is the user's; Cinder must not assume it may
	// append without asking, so this surfaces as the same one-click offer.
	AgentInstrStateNoBlock = "no_block"
	// AgentInstrStateMissing — no instruction file exists at all.
	AgentInstrStateMissing = "missing"
	// AgentInstrStateSkipped — the project has no writable project directory
	// (the synthetic default row, or a scan-time container): the checklist does
	// not apply, and the UI must not nag about it.
	AgentInstrStateSkipped = "skipped"
)

// AgentInstructionsStatus is the checklist answer for one project. Files lists
// the instruction files that exist on disk, Missing the standard names that do
// not — the UI can show "AGENTS.md missing" instead of a bare boolean.
type AgentInstructionsStatus struct {
	State   string   `json:"state"`
	Files   []string `json:"files"`
	Missing []string `json:"missing"`
}

// instructionFilesState does the two stat() calls and the block-marker scan
// for one directory. Kept free of the service so the scan pipeline can call it
// without a receiver.
func instructionFilesState(dir string) *AgentInstructionsStatus {
	st := &AgentInstructionsStatus{State: AgentInstrStateMissing}
	hasBlock := false
	for _, rel := range agentinit.InstructionFiles {
		path := filepath.Join(dir, rel)
		raw, err := os.ReadFile(path)
		if err != nil {
			st.Missing = append(st.Missing, rel)
			continue
		}
		st.Files = append(st.Files, rel)
		if strings.Contains(string(raw), agentinit.InstructionsBeginMark) {
			hasBlock = true
		}
	}
	switch {
	case len(st.Files) == 0:
		st.State = AgentInstrStateMissing
	case hasBlock:
		st.State = AgentInstrStateOK
	default:
		st.State = AgentInstrStateNoBlock
	}
	return st
}

// AgentInstructionsStatusFor reports the checklist for one project. A container
// or the synthetic default row answers "skipped" rather than an error: the UI
// renders the answer, and "not applicable" is the honest one.
func (s *Service) AgentInstructionsStatusFor(projectID int64) (*AgentInstructionsStatus, error) {
	project, err := db.GetProjectByID(s.db, projectID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("project %d not found", projectID)
		}
		return nil, err
	}
	if refusalForProject(project) != AgentSetupOK {
		return &AgentInstructionsStatus{State: AgentInstrStateSkipped}, nil
	}
	return instructionFilesState(strings.TrimSpace(project.RootPath)), nil
}

// MissingAgentInstructionsCount returns how many collected projects are
// missing the managed block, for the dashboard/rail badge. Read-only and
// deliberately recomputed on every call: storing the verdict would go stale
// the moment the user creates the file by hand, and a handful of stat() calls
// per refresh cost nothing.
func (s *Service) MissingAgentInstructionsCount() int {
	projects, err := db.GetAllProjects(s.db)
	if err != nil {
		log.Printf("agent instructions count: %v", err)
		return 0
	}
	n := 0
	for i := range projects {
		p := &projects[i]
		if !p.Collected || refusalForProject(p) != AgentSetupOK {
			continue
		}
		if st := instructionFilesState(strings.TrimSpace(p.RootPath)); st.State != AgentInstrStateOK {
			n++
		}
	}
	return n
}

// InstallAgentInstructions tops up ONE project's instruction files with the
// managed block, and nothing else. It exists so the checklist's one-click
// action never needs the MCP binary, never touches client configs, and never
// installs the Claude hook — those remain the full setup dialog's decisions.
//
// The refusals mirror SetupAgentClients (same codes, same reasoning): a
// container is not a project, and the desktop UI renders the code instead of
// the backend guessing at a language.
func (s *Service) InstallAgentInstructions(projectID int64) (AgentSetupReport, error) {
	var report AgentSetupReport

	project, err := db.GetProjectByID(s.db, projectID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			report.Code = AgentSetupProjectNotFound
			report.Blocking = true
			return report, nil
		}
		return report, err
	}
	if code := refusalForProject(project); code != AgentSetupOK {
		report.Code = code
		report.Blocking = true
		return report, nil
	}
	dir := strings.TrimSpace(project.RootPath)
	if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
		report.Code = AgentSetupMissingDirectory
		report.Blocking = true
		return report, nil
	}

	res, err := agentinit.Register(agentinit.Options{
		TargetDir:        dir,
		InstructionsOnly: true,
		WithInstructions: true,
	})
	if err != nil {
		return report, err
	}
	report.Instructions = res.Instructions
	report.Warnings = res.Warnings
	return report, nil
}
