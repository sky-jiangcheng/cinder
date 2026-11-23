package agentinit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installEverything runs the fullest Register shape (all file-writing clients,
// both hooks, the managed block) so an uninstall test starts from exactly what
// a real `init --all` leaves behind.
func installEverything(t *testing.T, dir string) {
	t.Helper()
	bin := filepath.Join(dir, "repotory-mcp")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Register(Options{
		TargetDir:        dir,
		BinPath:          bin,
		Clients:          []Client{ClientClaude, ClientCursor, ClientVSCode},
		WithHook:         true,
		WithInstructions: true,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
}

func readJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s is not valid JSON: %v", path, err)
	}
	return doc
}

// The uninstall contract: our entries are gone, everything else survives, and
// files that held only our content return to not existing.
func TestUnregisterRemovesOnlyOurs(t *testing.T) {
	dir := t.TempDir()

	// Seed a foreign server and a foreign instruction paragraph BEFORE install,
	// so the test proves preservation rather than just absence.
	mcpPath := filepath.Join(dir, ".mcp.json")
	if err := os.WriteFile(mcpPath, []byte(`{"mcpServers":{"other":{"command":"other-server","args":[]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	agentsPath := filepath.Join(dir, "AGENTS.md")
	ownContent := "# My own rules\n\nKeep commits small.\n"
	if err := os.WriteFile(agentsPath, []byte(ownContent), 0o644); err != nil {
		t.Fatal(err)
	}

	installEverything(t, dir)

	report, err := Unregister(Options{TargetDir: dir, Clients: []Client{ClientClaude, ClientCursor, ClientVSCode}})
	if err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if len(report.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", report.Warnings)
	}
	for _, f := range report.Files {
		if f.Status != StatusRemoved {
			t.Errorf("%s: status = %s (%s), want removed", f.Path, f.Status, f.Note)
		}
	}
	if report.Hook == nil || report.Hook.Status != StatusRemoved {
		t.Errorf("SessionEnd script not removed: %+v", report.Hook)
	}
	if report.BriefHook == nil || report.BriefHook.Status != StatusRemoved {
		t.Errorf("SessionStart script not removed: %+v", report.BriefHook)
	}
	if report.HookSettings == nil || report.HookSettings.Status != StatusRemoved {
		t.Errorf("hook settings not cleaned: %+v", report.HookSettings)
	}

	// The foreign server survives; ours is gone.
	doc := readJSONFile(t, mcpPath)
	servers, _ := doc["mcpServers"].(map[string]any)
	if _, ok := servers[ServerName]; ok {
		t.Errorf("repotory entry survived uninstall: %v", servers)
	}
	if _, ok := servers["other"]; !ok {
		t.Errorf("the other server was dropped: %v", servers)
	}

	// settings.json returns to the pre-install shape: no hooks key at all.
	settings := readJSONFile(t, filepath.Join(dir, filepath.FromSlash(HookSettingsRel)))
	if _, ok := settings["hooks"]; ok {
		t.Errorf("empty hooks map was not pruned: %v", settings)
	}

	// Both scripts are gone.
	for _, rel := range []string{HookScriptRel, BriefHookScriptRel} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Errorf("hook script %s still exists (err = %v)", rel, err)
		}
	}

	// AGENTS.md returns to its exact old bytes; block-only CLAUDE.md is deleted.
	raw, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != ownContent {
		t.Errorf("AGENTS.md = %q, want the pre-install bytes %q", raw, ownContent)
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Errorf("block-only CLAUDE.md was not deleted (err = %v)", err)
	}

	// A backup exists for every file that was overwritten.
	for _, f := range []string{mcpPath, agentsPath} {
		if _, err := os.Stat(f + BackupSuffix); err != nil {
			t.Errorf("no backup for %s: %v", f, err)
		}
	}
}

// A second run must be a silent no-op: everything unchanged, no errors, no
// warnings — the same idempotency Register has.
func TestUnregisterIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	installEverything(t, dir)
	if _, err := Unregister(Options{TargetDir: dir, Clients: []Client{ClientClaude}}); err != nil {
		t.Fatalf("first Unregister: %v", err)
	}

	report, err := Unregister(Options{TargetDir: dir, Clients: []Client{ClientClaude}})
	if err != nil {
		t.Fatalf("second Unregister: %v", err)
	}
	if len(report.Warnings) != 0 {
		t.Errorf("second run warnings: %v", report.Warnings)
	}
	for _, f := range report.Files {
		if f.Status != StatusUnchanged {
			t.Errorf("%s: second run status = %s, want unchanged", f.Path, f.Status)
		}
	}
	for _, f := range []*FileResult{report.Hook, report.BriefHook, report.HookSettings} {
		if f != nil && f.Status != StatusUnchanged {
			t.Errorf("%s: second run status = %s, want unchanged", f.Path, f.Status)
		}
	}
	for _, f := range report.Instructions {
		if f.Status != StatusUnchanged {
			t.Errorf("%s: second run status = %s, want unchanged", f.Path, f.Status)
		}
	}
}

// A hook script the user edited after install is theirs, not ours to delete:
// it must stay in place and the report must say why.
func TestUnregisterLeavesModifiedHookScript(t *testing.T) {
	dir := t.TempDir()
	installEverything(t, dir)
	scriptPath := filepath.Join(dir, filepath.FromSlash(HookScriptRel))
	edited := hookScript + "\n# my local tweak\n"
	if err := os.WriteFile(scriptPath, []byte(edited), 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := Unregister(Options{TargetDir: dir, Clients: []Client{ClientClaude}})
	if err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if report.Hook == nil || report.Hook.Status != StatusUnchanged {
		t.Fatalf("modified script status = %+v, want unchanged (left in place)", report.Hook)
	}
	raw, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("the edited script was deleted: %v", err)
	}
	if string(raw) != edited {
		t.Error("the edited script's bytes were changed")
	}
	joined := strings.Join(report.Warnings, "\n")
	if !strings.Contains(joined, "modified after install") {
		t.Errorf("no warning about the modified script: %v", report.Warnings)
	}
	// The untouched brief script is still removed normally.
	if report.BriefHook == nil || report.BriefHook.Status != StatusRemoved {
		t.Errorf("brief script status = %+v, want removed", report.BriefHook)
	}
}

// A foreign hook on the same event survives; only our entry is dropped.
func TestUnregisterKeepsForeignHookEntry(t *testing.T) {
	dir := t.TempDir()
	installEverything(t, dir)
	settingsPath := filepath.Join(dir, filepath.FromSlash(HookSettingsRel))
	doc := readJSONFile(t, settingsPath)
	hooks, _ := doc["hooks"].(map[string]any)
	endEntries, _ := hooks["SessionEnd"].([]any)
	hooks["SessionEnd"] = append(endEntries, map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": "echo foreign"}},
	})
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Unregister(Options{TargetDir: dir, Clients: []Client{ClientClaude}}); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	doc = readJSONFile(t, settingsPath)
	hooks, _ = doc["hooks"].(map[string]any)
	endEntries, _ = hooks["SessionEnd"].([]any)
	if len(endEntries) != 1 {
		t.Fatalf("SessionEnd has %d entries, want just the foreign one: %v", len(endEntries), hooks)
	}
	entry, _ := endEntries[0].(map[string]any)
	inner, _ := entry["hooks"].([]any)
	first, _ := inner[0].(map[string]any)
	if first["command"] != "echo foreign" {
		t.Errorf("the foreign hook was dropped: %v", endEntries)
	}
	if _, ok := hooks["SessionStart"]; ok {
		t.Errorf("our empty SessionStart key was not pruned: %v", hooks)
	}
}

// A directory that never had an install is not an error: everything reports
// unchanged, nothing is created.
func TestUnregisterOnNeverInstalledDir(t *testing.T) {
	dir := t.TempDir()
	report, err := Unregister(Options{TargetDir: dir, Clients: []Client{ClientClaude, ClientCursor, ClientVSCode}})
	if err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if len(report.Warnings) != 0 {
		t.Errorf("warnings on a never-installed dir: %v", report.Warnings)
	}
	for _, f := range report.Files {
		if f.Status != StatusUnchanged {
			t.Errorf("%s: status = %s, want unchanged", f.Path, f.Status)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("uninstall created files in an empty dir: %v", entries)
	}
}

// An invalid client config must be reported, not overwritten — the same rule
// Register follows for a file it cannot parse.
func TestUnregisterReportsInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	broken := filepath.Join(dir, ".mcp.json")
	if err := os.WriteFile(broken, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := Unregister(Options{TargetDir: dir, Clients: []Client{ClientClaude}})
	if err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if report.Files[0].Status != StatusFailed {
		t.Errorf("status = %s, want failed", report.Files[0].Status)
	}
	if len(report.Warnings) != 1 {
		t.Errorf("warnings = %v, want one", report.Warnings)
	}
	raw, err := os.ReadFile(broken)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{not json" {
		t.Errorf("the broken file was overwritten: %q", raw)
	}
}
