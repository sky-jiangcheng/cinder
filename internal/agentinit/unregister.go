package agentinit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// StatusRemoved reports a file or entry the uninstall took away. It exists
// only on the uninstall path — Register never removes anything.
const StatusRemoved Status = "removed"

// Unregister removes exactly what Register added, and nothing else: the
// repotory server entry from each client config (every other server stays),
// the two hook entries from .claude/settings.json (every other hook stays),
// the hook scripts (only when their bytes still match what was installed — a
// user-modified script is left in place with a warning), and the managed
// instruction block (everything outside the markers stays). This is the
// precise-uninstall half of the install-all CLI (ADR-0067).
//
// Idempotent: a second run reports unchanged everywhere. A file that never
// existed is unchanged, not an error.
func Unregister(opts Options) (*Report, error) {
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
	home := opts.HomeDir
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cannot resolve the home directory: %w", err)
		}
		home = h
	}

	report := &Report{}
	clients := opts.Clients
	if len(clients) == 0 {
		clients = AllClients
	}
	for _, c := range clients {
		res := unregisterClient(c, abs, home)
		if res.Status == StatusFailed {
			report.Warnings = append(report.Warnings, res.Note)
		}
		report.Files = append(report.Files, res)
	}

	hook, brief, settings, err := uninstallHook(abs)
	if err != nil {
		report.Warnings = append(report.Warnings, err.Error())
	}
	if hook.Path != "" {
		report.Hook = &hook
	}
	if brief.Path != "" {
		report.BriefHook = &brief
	}
	if settings.Path != "" {
		report.HookSettings = &settings
	}

	instr := uninstallInstructions(abs)
	report.Instructions = instr
	for _, f := range instr {
		if f.Status == StatusFailed {
			report.Warnings = append(report.Warnings, f.Note)
		}
	}
	return report, nil
}

// unregisterClient drops the repotory entry from one client config file.
func unregisterClient(c Client, targetDir, homeDir string) FileResult {
	switch c {
	case ClientClaude:
		return removeMCPEntry(targetDir, ".mcp.json", "mcpServers", c)
	case ClientCursor:
		return removeMCPEntry(targetDir, filepath.Join(".cursor", "mcp.json"), "mcpServers", c)
	case ClientVSCode:
		return removeMCPEntry(targetDir, filepath.Join(".vscode", "mcp.json"), "servers", c)
	case ClientWindsurf:
		return removeMCPEntry(homeDir, filepath.Join(".codeium", "windsurf", "mcp_config.json"), "mcpServers", c)
	default:
		return FileResult{Client: c, Status: StatusFailed, Note: fmt.Sprintf("unknown AI client %q", c)}
	}
}

func removeMCPEntry(dir, rel, key string, c Client) FileResult {
	file := filepath.Join(dir, rel)
	res := FileResult{Client: c, Path: file}

	raw, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		res.Status = StatusUnchanged
		return res
	}
	if err != nil {
		res.Status = StatusFailed
		res.Note = fmt.Sprintf("cannot read %s: %v", file, err)
		return res
	}
	top := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &top); err != nil {
		res.Status = StatusFailed
		res.Note = fmt.Sprintf("%s is not valid JSON (%v); fix or remove it first", file, err)
		return res
	}
	servers := map[string]json.RawMessage{}
	if existing, ok := top[key]; ok {
		if err := json.Unmarshal(existing, &servers); err != nil {
			res.Status = StatusUnchanged
			return res
		}
	}
	if _, ok := servers[ServerName]; !ok {
		res.Status = StatusUnchanged
		return res
	}
	delete(servers, ServerName)
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
	if err := writeWithBackup(file, raw, out); err != nil {
		res.Status = StatusFailed
		res.Note = err.Error()
		return res
	}
	res.Status = StatusRemoved
	res.Note = "backup: " + file + BackupSuffix
	return res
}

// uninstallHook removes the two hook entries and scripts. A script whose
// bytes differ from what was installed is the user's edit, not ours to
// delete: it stays, with a warning.
func uninstallHook(targetDir string) (FileResult, FileResult, FileResult, error) {
	settingsRes, err := removeHookSettings(targetDir)
	if err != nil {
		return FileResult{}, FileResult{}, settingsRes, err
	}
	hookRes := removeHookScriptFile(targetDir, HookScriptRel, hookScript)
	briefRes := removeHookScriptFile(targetDir, BriefHookScriptRel, briefHookScript)
	var warn []string
	for _, r := range []FileResult{hookRes, briefRes} {
		if r.Status == StatusFailed || (r.Status == StatusUnchanged && r.Note != "") {
			warn = append(warn, r.Note)
		}
	}
	if len(warn) > 0 {
		return hookRes, briefRes, settingsRes, errors.New(strings.Join(warn, "; "))
	}
	return hookRes, briefRes, settingsRes, nil
}

func removeHookScriptFile(targetDir, rel, installed string) FileResult {
	scriptPath := filepath.Join(targetDir, filepath.FromSlash(rel))
	res := FileResult{Client: ClientClaude, Path: scriptPath}
	existing, err := os.ReadFile(scriptPath)
	if os.IsNotExist(err) {
		res.Status = StatusUnchanged
		return res
	}
	if err != nil {
		res.Status = StatusFailed
		res.Note = fmt.Sprintf("cannot read %s: %v", scriptPath, err)
		return res
	}
	if string(existing) != installed {
		res.Status = StatusUnchanged
		res.Note = fmt.Sprintf("%s was modified after install; left in place (remove it manually if intended)", scriptPath)
		return res
	}
	if err := os.Remove(scriptPath); err != nil {
		res.Status = StatusFailed
		res.Note = fmt.Sprintf("cannot remove %s: %v", scriptPath, err)
		return res
	}
	res.Status = StatusRemoved
	return res
}

// removeHookSettings drops our entries from SessionEnd / SessionStart,
// pruning an event key whose list went empty and the hooks key whose map
// went empty — the file returns to the shape it had before install.
func removeHookSettings(targetDir string) (FileResult, error) {
	file := filepath.Join(targetDir, filepath.FromSlash(HookSettingsRel))
	res := FileResult{Client: ClientClaude, Path: file}

	raw, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		res.Status = StatusUnchanged
		return res, nil
	}
	if err != nil {
		res.Status = StatusFailed
		res.Note = fmt.Sprintf("cannot read %s: %v", file, err)
		return res, errors.New(res.Note)
	}
	settings := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &settings); err != nil {
		res.Status = StatusFailed
		res.Note = fmt.Sprintf("%s is not valid JSON (%v); fix or remove it first", file, err)
		return res, errors.New(res.Note)
	}
	hooks := map[string]json.RawMessage{}
	if existing, ok := settings["hooks"]; ok {
		if err := json.Unmarshal(existing, &hooks); err != nil {
			res.Status = StatusUnchanged
			return res, nil
		}
	}

	changed := false
	for _, ev := range hookEvents {
		var entries []json.RawMessage
		if existing, ok := hooks[ev.event]; ok {
			if err := json.Unmarshal(existing, &entries); err != nil {
				continue
			}
		}
		var kept []json.RawMessage
		for _, e := range entries {
			var entry hookEntry
			if err := json.Unmarshal(e, &entry); err != nil {
				kept = append(kept, e)
				continue
			}
			ours := false
			for _, h := range entry.Hooks {
				if strings.Contains(h.Command, ev.scriptRel) {
					ours = true
				}
			}
			if !ours {
				kept = append(kept, e)
			}
		}
		if len(kept) == len(entries) {
			continue
		}
		changed = true
		if len(kept) == 0 {
			delete(hooks, ev.event)
			continue
		}
		encoded, err := json.Marshal(kept)
		if err != nil {
			res.Status = StatusFailed
			res.Note = err.Error()
			return res, err
		}
		hooks[ev.event] = encoded
	}
	if !changed {
		res.Status = StatusUnchanged
		return res, nil
	}
	if len(hooks) == 0 {
		delete(settings, "hooks")
	} else {
		encodedHooks, err := json.Marshal(hooks)
		if err != nil {
			res.Status = StatusFailed
			res.Note = err.Error()
			return res, err
		}
		settings["hooks"] = encodedHooks
	}
	out, err := marshalJSON(settings)
	if err != nil {
		res.Status = StatusFailed
		res.Note = err.Error()
		return res, err
	}
	if bytes.Equal(raw, out) {
		res.Status = StatusUnchanged
		return res, nil
	}
	if err := writeWithBackup(file, raw, out); err != nil {
		res.Status = StatusFailed
		res.Note = err.Error()
		return res, errors.New(res.Note)
	}
	res.Status = StatusRemoved
	res.Note = "backup: " + file + BackupSuffix
	return res, nil
}

// uninstallInstructions strips the managed block. A file that held nothing
// but the block is deleted (the pre-install state was "no file"); a file with
// other content keeps every byte outside the markers.
func uninstallInstructions(targetDir string) []FileResult {
	var out []FileResult
	for _, rel := range InstructionFiles {
		file := filepath.Join(targetDir, rel)
		res := FileResult{Client: ClientClaude, Path: file}
		raw, err := os.ReadFile(file)
		if os.IsNotExist(err) {
			res.Status = StatusUnchanged
			out = append(out, res)
			continue
		}
		if err != nil {
			res.Status = StatusFailed
			res.Note = fmt.Sprintf("cannot read %s: %v", file, err)
			out = append(out, res)
			continue
		}
		content := string(raw)
		begin := strings.Index(content, InstructionsBeginMark)
		end := strings.Index(content, InstructionsEndMark)
		if begin < 0 || end <= begin {
			res.Status = StatusUnchanged
			out = append(out, res)
			continue
		}
		end += len(InstructionsEndMark)
		tail := content[end:]
		nl := 0
		for nl < len(tail) && (tail[nl] == '\n' || tail[nl] == '\r') {
			nl++
		}
		next := content[:begin] + tail[nl:]
		if strings.TrimSpace(next) == "" {
			if err := os.Remove(file); err != nil {
				res.Status = StatusFailed
				res.Note = fmt.Sprintf("cannot remove %s: %v", file, err)
			} else {
				res.Status = StatusRemoved
			}
			out = append(out, res)
			continue
		}
		// Trim the blank line the install appended before the block, so a
		// file that ends with its own content returns to its exact old bytes.
		next = strings.TrimRight(next, "\n") + "\n"
		if err := writeWithBackup(file, raw, []byte(next)); err != nil {
			res.Status = StatusFailed
			res.Note = err.Error()
		} else {
			res.Status = StatusRemoved
			res.Note = "backup: " + file + BackupSuffix
		}
		out = append(out, res)
	}
	return out
}

// writeWithBackup backs up the previous bytes before overwriting, the same
// convention Register uses: an uninstall is still a write to the user's file.
func writeWithBackup(file string, raw, out []byte) error {
	if err := os.WriteFile(file+BackupSuffix, raw, 0o600); err != nil {
		return fmt.Errorf("cannot back up %s: %v", file, err)
	}
	if err := os.WriteFile(file, out, 0o644); err != nil {
		return fmt.Errorf("cannot write %s: %v", file, err)
	}
	return nil
}
