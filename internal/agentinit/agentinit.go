// Package agentinit registers Repotory's MCP server with the AI clients a
// user actually runs — Claude Code, Cursor, VS Code, Windsurf — and installs
// the Claude Code SessionEnd hook that writes the handoff note when a session
// ends.
//
// Why there is a Go twin of ide/vscode/repotory-init/index.mjs. That script is
// the terminal path: it runs where node already is (the VS Code extension
// shells out to it). The desktop app has no node — it is one self-contained
// binary — so the button that makes "click once, the agent sees the memory"
// possible needs the same wiring in Go.
//
// Two implementations of one wire format drift, and the drift shows up as
// "the button said it registered, but Claude Code does not see repotory".
// So both are pinned to the same golden fixture
// (testdata/contract.json): the Go test compares its own output against it,
// and scripts/check-agent-init-contract.mjs runs the mjs and compares the
// same fixture. Change one side and the other side's check goes red.
package agentinit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// ServerName is the key the MCP server is registered under. Frozen: it
	// matches the tool prefix every client already knows.
	ServerName = "repotory"
	// BackupSuffix is appended to a config file before it is overwritten, so a
	// hand-written config is never lost to a registration.
	BackupSuffix = ".repotory-init.bak"
	// HookScriptRel is the hook script path relative to the project directory.
	// Forward slashes on every platform: the path ends up inside a shell
	// command string, and the mjs used to build it with path.join, which
	// produced backslashes on Windows — the one place the two
	// implementations could disagree about the same file.
	HookScriptRel = ".claude/hooks/repotory-handoff.sh"
	// HookSettingsRel is Claude Code's settings file, relative to the project
	// directory.
	HookSettingsRel = ".claude/settings.json"
)

// Client is one AI client that can host the MCP server.
type Client string

const (
	ClientClaude   Client = "claude"
	ClientCursor   Client = "cursor"
	ClientVSCode   Client = "vscode"
	ClientWindsurf Client = "windsurf"
)

// AllClients is the set the CLI registers by default. The desktop app lets the
// user pick, because each of these files lands in the user's project (or, for
// Windsurf, in their home directory) and that is not a decision to make for
// them.
var AllClients = []Client{ClientClaude, ClientCursor, ClientVSCode, ClientWindsurf}

// ParseClient validates one client name. The frontend sends strings, so the
// set is checked here rather than trusted.
func ParseClient(name string) (Client, error) {
	c := Client(strings.ToLower(strings.TrimSpace(name)))
	switch c {
	case ClientClaude, ClientCursor, ClientVSCode, ClientWindsurf:
		return c, nil
	}
	return "", fmt.Errorf("unknown AI client %q (known: claude, cursor, vscode, windsurf)", name)
}

// Status is what happened to one file.
type Status string

const (
	StatusCreated   Status = "created"
	StatusUpdated   Status = "updated"
	StatusUnchanged Status = "unchanged"
	StatusFailed    Status = "failed"
)

// FileResult reports one file the registration touched.
type FileResult struct {
	Client Client `json:"client"`
	Path   string `json:"path"`
	Status Status `json:"status"`
	// Note carries the detail a status cannot: the backup path, or the reason
	// a file failed.
	Note string `json:"note,omitempty"`
}

// Report is the machine-readable outcome of a registration. The frontend
// renders it; it never assembles sentences about what happened, because the
// backend does not know the user's language.
type Report struct {
	// Binary is the resolved repotory-mcp path that was registered.
	Binary string `json:"binary"`
	// Files are the client config files, one per registered client.
	Files []FileResult `json:"files"`
	// Hook is the installed SessionEnd hook script (nil when not requested).
	Hook *FileResult `json:"hook,omitempty"`
	// HookSettings is the .claude/settings.json entry that points at the hook.
	HookSettings *FileResult `json:"hook_settings,omitempty"`
	// Instructions lists the project instruction files the managed block was
	// written to, one entry per file (ADR-0043). nil when not requested.
	Instructions []FileResult `json:"instructions,omitempty"`
	// Warnings collects per-file failures. A registration that half-succeeded
	// is reported, not hidden behind one boolean.
	Warnings []string `json:"warnings,omitempty"`
}

// Options is one registration request.
type Options struct {
	// TargetDir is the project directory the client-local config files land
	// in. Required.
	TargetDir string
	// HomeDir overrides the user's home directory, where the Windsurf config
	// lives. Empty means the real home; tests set it to a temp dir.
	HomeDir string
	// BinPath is the MCP server binary to register. Empty means "detect".
	BinPath string
	// Clients selects which clients to register. Empty means all of them.
	Clients []Client
	// WithHook installs the Claude Code SessionEnd handoff hook.
	WithHook bool
	// WithInstructions writes the managed repotory_* instruction block into the
	// project's instruction files (AGENTS.md, CLAUDE.md) — ADR-0043. The zero
	// value is deliberately NOT the default here: this field is set explicitly
	// by every caller (service.SetupAgentClients passes the user's checkbox),
	// because writing into the user's own tracked files is not a decision to
	// make for them.
	WithInstructions bool
}

// ErrBinaryNotFound means the repotory-mcp binary is not on this machine. The
// caller shows the install guidance; the registration itself is not possible.
var ErrBinaryNotFound = errors.New("repotory-mcp binary not found")

// Register writes the client configuration for one project directory and
// returns what it did.
//
// A failure on one file does not abort the others: a project directory the
// user cannot write to should not stop the global Windsurf registration. The
// failure is reported as a warning so the UI can say which client did not
// make it.
func Register(opts Options) (*Report, error) {
	target := strings.TrimSpace(opts.TargetDir)
	if target == "" {
		return nil, errors.New("target directory is required")
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("target directory %s: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("target path %s is not a directory", abs)
	}

	bin, err := FindBinary(opts.BinPath)
	if err != nil {
		return nil, err
	}

	home := opts.HomeDir
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cannot resolve the home directory: %w", err)
		}
		home = h
	}

	clients := opts.Clients
	if len(clients) == 0 {
		clients = AllClients
	}

	report := &Report{Binary: bin}
	for _, c := range clients {
		res := registerClient(c, abs, home, bin)
		if res.Status == StatusFailed {
			report.Warnings = append(report.Warnings, res.Note)
		}
		report.Files = append(report.Files, res)
	}

	if opts.WithHook {
		hook, settings, err := installHook(abs)
		if err != nil {
			report.Warnings = append(report.Warnings, err.Error())
		} else {
			report.Hook = &hook
			report.HookSettings = &settings
		}
	}

	if opts.WithInstructions {
		instr := installInstructions(abs)
		report.Instructions = instr
		for _, f := range instr {
			if f.Status == StatusFailed {
				report.Warnings = append(report.Warnings, f.Note)
			}
		}
	}
	return report, nil
}

// registerClient writes one client's config file.
func registerClient(c Client, targetDir, homeDir, bin string) FileResult {
	switch c {
	case ClientClaude:
		return writeMCPConfig(targetDir, ".mcp.json", "mcpServers", c, bin)
	case ClientCursor:
		return writeMCPConfig(targetDir, filepath.Join(".cursor", "mcp.json"), "mcpServers", c, bin)
	case ClientVSCode:
		return writeMCPConfig(targetDir, filepath.Join(".vscode", "mcp.json"), "servers", c, bin)
	case ClientWindsurf:
		return writeMCPConfig(homeDir, filepath.Join(".codeium", "windsurf", "mcp_config.json"), "mcpServers", c, bin)
	default:
		return FileResult{Client: c, Status: StatusFailed, Note: fmt.Sprintf("unknown AI client %q", c)}
	}
}

// serverEntry is the MCP server stanza. Field order is fixed by the struct so
// the written file reads command-first, like the mjs's object literal.
type serverEntry struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// writeMCPConfig merges the repotory entry into a client config file.
//
// The merge keeps every other key the file already had — these are the user's
// config files, and a registration that dropped their other MCP servers would
// be a destructive side effect of a convenience action.
func writeMCPConfig(dir, rel, key string, c Client, bin string) FileResult {
	file := filepath.Join(dir, rel)
	res := FileResult{Client: c, Path: file}

	raw, err := os.ReadFile(file)
	existed := err == nil
	if err != nil && !os.IsNotExist(err) {
		res.Status = StatusFailed
		res.Note = fmt.Sprintf("cannot read %s: %v", file, err)
		return res
	}

	top := map[string]json.RawMessage{}
	if existed {
		if err := json.Unmarshal(raw, &top); err != nil {
			res.Status = StatusFailed
			res.Note = fmt.Sprintf("%s is not valid JSON (%v); fix or remove it first", file, err)
			return res
		}
	}

	servers := map[string]json.RawMessage{}
	if existing, ok := top[key]; ok {
		// A non-object value under the key is replaced rather than merged
		// into; the mjs does the same (it only merges plain objects).
		if err := json.Unmarshal(existing, &servers); err != nil {
			servers = map[string]json.RawMessage{}
		}
	}

	entry, err := json.Marshal(serverEntry{Command: bin, Args: []string{}})
	if err != nil {
		res.Status = StatusFailed
		res.Note = err.Error()
		return res
	}
	if prev, ok := servers[ServerName]; ok && jsonEqual(prev, entry) {
		res.Status = StatusUnchanged
		return res
	}
	servers[ServerName] = entry

	encoded, err := json.Marshal(servers)
	if err != nil {
		res.Status = StatusFailed
		res.Note = err.Error()
		return res
	}
	top[key] = encoded

	out, err := marshalJSON(top)
	if err != nil {
		res.Status = StatusFailed
		res.Note = err.Error()
		return res
	}
	if existed && bytes.Equal(raw, out) {
		res.Status = StatusUnchanged
		return res
	}

	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		res.Status = StatusFailed
		res.Note = fmt.Sprintf("cannot create %s: %v", filepath.Dir(file), err)
		return res
	}
	if existed {
		backup := file + BackupSuffix
		if err := os.WriteFile(backup, raw, 0o600); err != nil {
			res.Status = StatusFailed
			res.Note = fmt.Sprintf("cannot back up %s: %v", file, err)
			return res
		}
		res.Note = "backup: " + backup
	}
	if err := os.WriteFile(file, out, 0o644); err != nil {
		res.Status = StatusFailed
		res.Note = fmt.Sprintf("cannot write %s: %v", file, err)
		return res
	}
	if existed {
		res.Status = StatusUpdated
	} else {
		res.Status = StatusCreated
	}
	return res
}

// hookCommand is one command entry inside a Claude Code hook.
type hookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// hookEntry is one SessionEnd entry: a list of commands to run.
type hookEntry struct {
	Hooks []hookCommand `json:"hooks"`
}

// hookScript is the SessionEnd hook. It asks Claude Code, at the end of a
// session, to write the handoff note — the "next agent can read what this one
// learned" step that makes the memory layer worth having.
//
// The bytes must match the mjs's HOOK_SCRIPT exactly; the contract check
// compares the sha256 of the two files.
const hookScript = `#!/bin/sh
# Repotory SessionEnd hook: write the handoff note when a session ends.
# Installed by repotory-init. Delete this file and the matching
# .claude/settings.json entry to uninstall.
command -v claude >/dev/null 2>&1 || exit 0
cd "${CLAUDE_PROJECT_DIR:-$(dirname "$0")/../..}" || exit 0
[ -f .mcp.json ] || exit 0
claude -p --mcp-config .mcp.json \
  'This session ended. Call repotory_handoff for the current project: a concise summary plus next_steps. If the project cannot be resolved, call repotory_context once first. Do nothing else.' \
  >/dev/null 2>&1 || true
`

// installHook writes the hook script and registers it in .claude/settings.json.
func installHook(targetDir string) (FileResult, FileResult, error) {
	scriptPath := filepath.Join(targetDir, filepath.FromSlash(HookScriptRel))
	scriptRes := FileResult{Client: ClientClaude, Path: scriptPath}

	if existing, err := os.ReadFile(scriptPath); err == nil {
		if string(existing) == hookScript {
			scriptRes.Status = StatusUnchanged
		} else {
			if err := os.WriteFile(scriptPath+BackupSuffix, existing, 0o600); err != nil {
				scriptRes.Status = StatusFailed
				scriptRes.Note = fmt.Sprintf("cannot back up %s: %v", scriptPath, err)
			} else {
				if err := writeExecutable(scriptPath, []byte(hookScript)); err != nil {
					scriptRes.Status = StatusFailed
					scriptRes.Note = fmt.Sprintf("cannot write %s: %v", scriptPath, err)
				} else {
					scriptRes.Status = StatusUpdated
					scriptRes.Note = "backup: " + scriptPath + BackupSuffix
				}
			}
		}
	} else if !os.IsNotExist(err) {
		scriptRes.Status = StatusFailed
		scriptRes.Note = fmt.Sprintf("cannot read %s: %v", scriptPath, err)
	} else {
		if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
			scriptRes.Status = StatusFailed
			scriptRes.Note = fmt.Sprintf("cannot create %s: %v", filepath.Dir(scriptPath), err)
		} else if err := writeExecutable(scriptPath, []byte(hookScript)); err != nil {
			scriptRes.Status = StatusFailed
			scriptRes.Note = fmt.Sprintf("cannot write %s: %v", scriptPath, err)
		} else {
			scriptRes.Status = StatusCreated
		}
	}
	if scriptRes.Status == StatusFailed {
		return scriptRes, FileResult{}, errors.New(scriptRes.Note)
	}

	settingsRes, err := writeHookSettings(targetDir)
	if err != nil {
		return scriptRes, settingsRes, err
	}
	return scriptRes, settingsRes, nil
}

// writeHookSettings appends the SessionEnd entry to .claude/settings.json,
// unless it is already there.
func writeHookSettings(targetDir string) (FileResult, error) {
	file := filepath.Join(targetDir, filepath.FromSlash(HookSettingsRel))
	res := FileResult{Client: ClientClaude, Path: file}

	raw, err := os.ReadFile(file)
	existed := err == nil
	if err != nil && !os.IsNotExist(err) {
		res.Status = StatusFailed
		res.Note = fmt.Sprintf("cannot read %s: %v", file, err)
		return res, errors.New(res.Note)
	}

	settings := map[string]json.RawMessage{}
	if existed {
		if err := json.Unmarshal(raw, &settings); err != nil {
			res.Status = StatusFailed
			res.Note = fmt.Sprintf("%s is not valid JSON (%v); fix or remove it first", file, err)
			return res, errors.New(res.Note)
		}
	}

	command := `"$CLAUDE_PROJECT_DIR"/` + HookScriptRel
	hooks := map[string]json.RawMessage{}
	if existing, ok := settings["hooks"]; ok {
		if err := json.Unmarshal(existing, &hooks); err != nil {
			hooks = map[string]json.RawMessage{}
		}
	}

	var entries []json.RawMessage
	if existing, ok := hooks["SessionEnd"]; ok {
		if err := json.Unmarshal(existing, &entries); err != nil {
			entries = nil
		}
	}
	for _, e := range entries {
		var entry hookEntry
		if err := json.Unmarshal(e, &entry); err != nil {
			continue
		}
		for _, h := range entry.Hooks {
			if strings.Contains(h.Command, HookScriptRel) {
				res.Status = StatusUnchanged
				return res, nil
			}
		}
	}

	added, err := json.Marshal(hookEntry{Hooks: []hookCommand{{Type: "command", Command: command}}})
	if err != nil {
		res.Status = StatusFailed
		res.Note = err.Error()
		return res, err
	}
	entries = append(entries, added)
	encodedEntries, err := json.Marshal(entries)
	if err != nil {
		res.Status = StatusFailed
		res.Note = err.Error()
		return res, err
	}
	hooks["SessionEnd"] = encodedEntries
	encodedHooks, err := json.Marshal(hooks)
	if err != nil {
		res.Status = StatusFailed
		res.Note = err.Error()
		return res, err
	}
	settings["hooks"] = encodedHooks

	out, err := marshalJSON(settings)
	if err != nil {
		res.Status = StatusFailed
		res.Note = err.Error()
		return res, err
	}
	if existed && bytes.Equal(raw, out) {
		res.Status = StatusUnchanged
		return res, nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		res.Status = StatusFailed
		res.Note = fmt.Sprintf("cannot create %s: %v", filepath.Dir(file), err)
		return res, errors.New(res.Note)
	}
	if existed {
		backup := file + BackupSuffix
		if err := os.WriteFile(backup, raw, 0o600); err != nil {
			res.Status = StatusFailed
			res.Note = fmt.Sprintf("cannot back up %s: %v", file, err)
			return res, errors.New(res.Note)
		}
		res.Note = "backup: " + backup
	}
	if err := os.WriteFile(file, out, 0o644); err != nil {
		res.Status = StatusFailed
		res.Note = fmt.Sprintf("cannot write %s: %v", file, err)
		return res, errors.New(res.Note)
	}
	if existed {
		res.Status = StatusUpdated
	} else {
		res.Status = StatusCreated
	}
	return res, nil
}

// writeExecutable writes a file and makes it executable. Windows has no
// executable bit, so the mode is set to best effort there.
func writeExecutable(path string, content []byte) error {
	if err := os.WriteFile(path, content, 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	return os.Chmod(path, 0o755)
}

// marshalJSON renders a value the way the mjs does: two-space indent, no HTML
// escaping, trailing newline.
func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// jsonEqual compares two JSON values by meaning, not by bytes: a config the
// user hand-wrote with the keys in another order is the same registration.
func jsonEqual(a, b json.RawMessage) bool {
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}
	return jsonValueEqual(av, bv)
}

func jsonValueEqual(a, b any) bool {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			other, ok := bv[k]
			if !ok || !jsonValueEqual(v, other) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !jsonValueEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

// binaryName is the MCP server executable name on this platform.
func binaryName() string {
	if runtime.GOOS == "windows" {
		return "repotory-mcp.exe"
	}
	return "repotory-mcp"
}

// FindBinary resolves the repotory-mcp executable to register.
//
// The order matters: an explicit path wins, then the env override, then PATH,
// then the directory the running executable lives in (the app and the MCP
// server ship in the same release, so this is where it is on a normal
// install), then the usual package-manager locations.
func FindBinary(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("mcp binary %q does not exist", explicit)
		}
		return filepath.Abs(explicit)
	}
	if env := strings.TrimSpace(os.Getenv("REPOTORY_MCP_BIN")); env != "" {
		if _, err := os.Stat(env); err == nil {
			return filepath.Abs(env)
		}
	}
	if p, err := exec.LookPath(binaryName()); err == nil {
		return p, nil
	}
	if exe, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(exe), binaryName())
		if isExecutable(sibling) {
			return sibling, nil
		}
	}
	for _, dir := range commonBinaryDirs() {
		candidate := filepath.Join(dir, binaryName())
		if isExecutable(candidate) {
			return candidate, nil
		}
	}
	return "", ErrBinaryNotFound
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

// commonBinaryDirs are the package-manager install locations, in the order
// the CLI checks them.
func commonBinaryDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	if runtime.GOOS == "windows" {
		return []string{
			filepath.Join(home, "scoop", "shims"),
			filepath.Join(home, "scoop", "apps", "repotory-mcp", "current"),
		}
	}
	return []string{
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/usr/local/opt/repotory-mcp/bin",
	}
}
