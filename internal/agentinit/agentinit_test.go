package agentinit

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// update rewrites the contract fixture instead of comparing against it. Use it
// only together with the matching change in
// ide/vscode/repotory-init/index.mjs — the two implementations are one wire
// format, and the fixture is where that fact is pinned.
var update = flag.Bool("update", false, "rewrite the contract fixture")

func TestRegisterWritesClientConfigs(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "repotory-mcp")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := Register(Options{
		TargetDir: dir,
		BinPath:   bin,
		Clients:   []Client{ClientClaude, ClientCursor, ClientVSCode},
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(report.Files) != 3 {
		t.Fatalf("expected 3 file results, got %d", len(report.Files))
	}
	for _, f := range report.Files {
		if f.Status != StatusCreated {
			t.Errorf("%s: status = %s (%s), want created", f.Path, f.Status, f.Note)
		}
	}

	// The MCP entry is the same everywhere, only the key differs.
	for _, tc := range []struct{ rel, key string }{
		{".mcp.json", "mcpServers"},
		{filepath.Join(".cursor", "mcp.json"), "mcpServers"},
		{filepath.Join(".vscode", "mcp.json"), "servers"},
	} {
		raw, err := os.ReadFile(filepath.Join(dir, tc.rel))
		if err != nil {
			t.Fatalf("read %s: %v", tc.rel, err)
		}
		var doc map[string]map[string]map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s is not valid JSON: %v", tc.rel, err)
		}
		entry, ok := doc[tc.key]["repotory"]
		if !ok {
			t.Fatalf("%s: no repotory entry under %q: %s", tc.rel, tc.key, raw)
		}
		if entry["command"] != bin {
			t.Errorf("%s: command = %v, want %s", tc.rel, entry["command"], bin)
		}
		if args, ok := entry["args"].([]any); !ok || len(args) != 0 {
			t.Errorf("%s: args = %v, want an empty array", tc.rel, entry["args"])
		}
	}

	// A second run changes nothing and says so.
	again, err := Register(Options{
		TargetDir: dir,
		BinPath:   bin,
		Clients:   []Client{ClientClaude, ClientCursor, ClientVSCode},
	})
	if err != nil {
		t.Fatalf("second Register: %v", err)
	}
	for _, f := range again.Files {
		if f.Status != StatusUnchanged {
			t.Errorf("%s: second run status = %s, want unchanged", f.Path, f.Status)
		}
	}
}

func TestRegisterKeepsOtherServersAndBacksUp(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "repotory-mcp")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	existing := `{
  "mcpServers": {
    "other": {
      "command": "other-server",
      "args": ["--flag"]
    }
  }
}
`
	mcpPath := filepath.Join(dir, ".mcp.json")
	if err := os.WriteFile(mcpPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := Register(Options{TargetDir: dir, BinPath: bin, Clients: []Client{ClientClaude}})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if report.Files[0].Status != StatusUpdated {
		t.Fatalf("status = %s, want updated", report.Files[0].Status)
	}

	raw, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["mcpServers"]["other"]; !ok {
		t.Errorf("the other server was dropped: %s", raw)
	}
	if _, ok := doc["mcpServers"]["repotory"]; !ok {
		t.Errorf("repotory was not registered: %s", raw)
	}

	backup, err := os.ReadFile(mcpPath + BackupSuffix)
	if err != nil {
		t.Fatalf("no backup written: %v", err)
	}
	if string(backup) != existing {
		t.Errorf("backup holds %q, want the previous content %q", backup, existing)
	}
}

func TestRegisterInstallsHookOnce(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "repotory-mcp")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := Register(Options{
		TargetDir: dir,
		BinPath:   bin,
		Clients:   []Client{ClientClaude},
		WithHook:  true,
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if report.Hook == nil || report.Hook.Status != StatusCreated {
		t.Fatalf("hook was not installed: %+v", report.Hook)
	}
	if report.HookSettings == nil || report.HookSettings.Status != StatusCreated {
		t.Fatalf("hook settings were not written: %+v", report.HookSettings)
	}

	scriptPath := filepath.Join(dir, filepath.FromSlash(HookScriptRel))
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("no hook script: %v", err)
	}
	if string(content) != hookScript {
		t.Errorf("hook script content differs:\n%s", content)
	}
	// The exec bit is how the hook is made runnable on POSIX; on Windows the
	// script runs through the shell configured in settings.json, and the mode
	// argument to WriteFile is ignored, so there is no bit to assert.
	if info, err := os.Stat(scriptPath); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
		t.Errorf("hook script is not executable (mode %v)", info.Mode().Perm())
	}

	settingsRaw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(HookSettingsRel)))
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(settingsRaw, &settings); err != nil {
		t.Fatal(err)
	}
	hooks, _ := settings["hooks"].(map[string]any)
	entries, _ := hooks["SessionEnd"].([]any)
	if len(entries) != 1 {
		t.Fatalf("SessionEnd has %d entries, want 1: %s", len(entries), settingsRaw)
	}

	// Registering again must not add a second hook entry.
	report, err = Register(Options{
		TargetDir: dir,
		BinPath:   bin,
		Clients:   []Client{ClientClaude},
		WithHook:  true,
	})
	if err != nil {
		t.Fatalf("second Register: %v", err)
	}
	if report.HookSettings.Status != StatusUnchanged {
		t.Errorf("second run rewrote the settings: %s", report.HookSettings.Status)
	}
	settingsRaw, err = os.ReadFile(filepath.Join(dir, filepath.FromSlash(HookSettingsRel)))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(settingsRaw, &settings); err != nil {
		t.Fatal(err)
	}
	hooks, _ = settings["hooks"].(map[string]any)
	entries, _ = hooks["SessionEnd"].([]any)
	if len(entries) != 1 {
		t.Fatalf("SessionEnd has %d entries after the second run, want 1: %s", len(entries), settingsRaw)
	}
}

func TestRegisterRejectsBadTargets(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "repotory-mcp")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Register(Options{TargetDir: "", BinPath: bin}); err == nil {
		t.Error("an empty target directory was accepted")
	}
	if _, err := Register(Options{TargetDir: filepath.Join(dir, "nope"), BinPath: bin}); err == nil {
		t.Error("a missing target directory was accepted")
	}
	if _, err := Register(Options{TargetDir: dir, BinPath: filepath.Join(dir, "missing-binary")}); err == nil {
		t.Error("a missing binary was accepted")
	}
}

func TestRegisterReportsInvalidConfigInsteadOfOverwriting(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "repotory-mcp")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(dir, ".mcp.json")
	if err := os.WriteFile(broken, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := Register(Options{TargetDir: dir, BinPath: bin, Clients: []Client{ClientClaude}})
	if err != nil {
		t.Fatalf("Register: %v", err)
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

func TestParseClient(t *testing.T) {
	if c, err := ParseClient(" Claude "); err != nil || c != ClientClaude {
		t.Errorf("ParseClient(\" Claude \") = %q, %v", c, err)
	}
	if _, err := ParseClient("emacs"); err == nil {
		t.Error("an unknown client was accepted")
	}
}

func TestFindBinaryPrefersTheExplicitPath(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "repotory-mcp")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := FindBinary(bin)
	if err != nil {
		t.Fatalf("FindBinary: %v", err)
	}
	if got != bin {
		t.Errorf("FindBinary = %q, want %q", got, bin)
	}
}

func TestFindBinaryUsesTheEnvOverride(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "repotory-mcp")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REPOTORY_MCP_BIN", bin)
	got, err := FindBinary("")
	if err != nil {
		t.Fatalf("FindBinary: %v", err)
	}
	if got != bin {
		t.Errorf("FindBinary = %q, want %q", got, bin)
	}
}

// The contract check: the Go implementation and the CLI script
// (ide/vscode/repotory-init/index.mjs) must produce the same files, because
// "the button said it registered" and "the CLI registered it" have to mean the
// same thing. The fixture is shared with scripts/check-agent-init-contract.mjs.
//
// Regenerate with: go test ./internal/agentinit -run TestContractFixture -update
// InstructionsOnly is the collected-project checklist's one-click top-up: it
// writes the managed block and nothing else, and — the point of the mode — it
// must not require the MCP binary, because a machine that has never installed
// repotory-mcp is exactly the machine whose projects need topping up first.
func TestRegisterInstructionsOnlySkipsBinaryAndClients(t *testing.T) {
	dir := t.TempDir()

	report, err := Register(Options{
		TargetDir: dir,
		BinPath:   filepath.Join(dir, "does-not-exist"), // would hard-fail any other mode
		// Clients deliberately empty: in this mode it must NOT mean "all four"
		// (the Options doc keeps that meaning for the full setup), it must mean
		// "no client configs at all".
		InstructionsOnly: true,
		WithInstructions: true,
		WithHook:         true, // ignored in this mode: the hook is the full setup's decision
	})
	if err != nil {
		t.Fatalf("Register (instructions-only): %v", err)
	}
	if report.Binary != "" {
		t.Errorf("Binary = %q, want empty (no binary dependency)", report.Binary)
	}
	if len(report.Files) != 0 {
		t.Errorf("Files = %d entries, want 0 (no client configs)", len(report.Files))
	}
	if report.Hook != nil || report.HookSettings != nil {
		t.Errorf("hook must not be installed in instructions-only mode, got %v", report.Hook)
	}
	if len(report.Instructions) != len(InstructionFiles) {
		t.Fatalf("Instructions = %d entries, want %d", len(report.Instructions), len(InstructionFiles))
	}
	for _, f := range report.Instructions {
		if f.Status != StatusCreated {
			t.Errorf("%s: status = %s (%s), want created", f.Path, f.Status, f.Note)
		}
		raw, err := os.ReadFile(f.Path)
		if err != nil {
			t.Fatalf("read %s: %v", f.Path, err)
		}
		if !strings.Contains(string(raw), InstructionsBeginMark) {
			t.Errorf("%s must carry the managed block", f.Path)
		}
	}
	// Idempotent: a second run reports unchanged instead of duplicating.
	report2, err := Register(Options{
		TargetDir: dir, InstructionsOnly: true, WithInstructions: true,
	})
	if err != nil {
		t.Fatalf("second Register: %v", err)
	}
	for _, f := range report2.Instructions {
		if f.Status != StatusUnchanged {
			t.Errorf("second run: %s status = %s, want unchanged", f.Path, f.Status)
		}
	}
}

func TestContractFixture(t *testing.T) {
	dir := t.TempDir()
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

	got := canonicalize(t, dir, bin)
	fixturePath := filepath.Join("testdata", "contract.json")
	if *update {
		writeCanonical(t, fixturePath, got)
		t.Logf("contract fixture updated: %s", fixturePath)
		return
	}

	want, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	wantCanonical := canonicalizeJSON(t, want)
	if !jsonValueEqual(got, wantCanonical) {
		t.Errorf("the produced files differ from the contract fixture:\ngot:  %s\nwant: %s", mustJSON(got), mustJSON(wantCanonical))
		t.Errorf("if the change is intended, regenerate with -update and change ide/vscode/repotory-init/index.mjs in the same commit")
	}
}

// canonicalize turns a registration output directory into the comparable
// shape: JSON files by meaning, the hook script by its exact text.
func canonicalize(t *testing.T, dir, bin string) map[string]any {
	t.Helper()
	out := map[string]any{}
	for _, rel := range []string{
		".mcp.json",
		filepath.Join(".cursor", "mcp.json"),
		filepath.Join(".vscode", "mcp.json"),
		filepath.FromSlash(HookSettingsRel),
	} {
		raw, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s is not valid JSON: %v", rel, err)
		}
		out[filepath.ToSlash(rel)] = replaceBin(doc, bin)
	}
	script, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(HookScriptRel)))
	if err != nil {
		t.Fatalf("read hook script: %v", err)
	}
	out[HookScriptRel] = string(script)

	// ADR-0043: the instruction files are Markdown, compared as exact text —
	// a changed word in the block is a changed contract.
	for _, rel := range InstructionFiles {
		raw, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		out[rel] = string(raw)
	}
	return out
}

// replaceBin swaps the machine-specific binary path for a placeholder, so the
// fixture does not depend on where the binary happens to live.
func replaceBin(v any, bin string) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range t {
			out[k] = replaceBin(val, bin)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = replaceBin(val, bin)
		}
		return out
	case string:
		return strings.ReplaceAll(t, bin, "<BIN>")
	default:
		return v
	}
}

func canonicalizeJSON(t *testing.T, raw []byte) any {
	t.Helper()
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("fixture is not valid JSON: %v", err)
	}
	return doc
}

func writeCanonical(t *testing.T, path string, doc map[string]any) {
	t.Helper()
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(v any) string {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err.Error()
	}
	return string(out)
}
