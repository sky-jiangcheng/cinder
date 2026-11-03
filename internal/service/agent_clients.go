package service

import (
	"database/sql"
	"errors"
	"os"
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

// SetupAgentClients registers Repotory's MCP server with the AI clients the
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
