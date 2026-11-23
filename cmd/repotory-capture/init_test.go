package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The init subcommand's whole promise is "one command wires the project":
// run() must dispatch it before the capture path, so no database is opened
// and no capture gate is touched. HOME is redirected so an accidental
// default-path write would land in the temp tree and be visible here.
func TestRunInitAllWiresProjectWithoutTouchingDB(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := t.TempDir()
	bin := filepath.Join(dir, "cinder-mcp")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	code := run([]string{"init", "-all", "-client", "claude", "-bin", bin, "-cwd", dir})
	if code != 0 {
		t.Fatalf("run(init -all) = %d, want 0", code)
	}
	for _, rel := range []string{
		".mcp.json",
		".claude/settings.json",
		".claude/hooks/repotory-handoff.sh",
		".claude/hooks/repotory-brief.sh",
		"AGENTS.md",
		"CLAUDE.md",
	} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("init -all did not write %s: %v", rel, err)
		}
	}
	// Nothing under HOME: no database, no pending record, no config gate.
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("init wrote into HOME (the capture path leaked in): %v", entries)
	}
}

// uninstall reverses init exactly, and a second run is a no-op exit 0.
func TestRunUninstallReversesInitAndIsIdempotent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	bin := filepath.Join(dir, "cinder-mcp")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"init", "-all", "-client", "claude", "-bin", bin, "-cwd", dir}); code != 0 {
		t.Fatalf("init = %d", code)
	}

	for i := 0; i < 2; i++ {
		if code := run([]string{"uninstall", "-client", "claude", "-cwd", dir}); code != 0 {
			t.Fatalf("uninstall run %d = %d, want 0", i+1, code)
		}
	}
	for _, rel := range []string{
		".claude/hooks/repotory-handoff.sh",
		".claude/hooks/repotory-brief.sh",
		"AGENTS.md",
		"CLAUDE.md",
	} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Errorf("%s survived uninstall (err = %v)", rel, err)
		}
	}
}

// A bad client name is a usage error (exit 2), not a half-written install.
func TestRunInitRejectsUnknownClient(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	if code := run([]string{"init", "-client", "emacs", "-cwd", dir}); code != 2 {
		t.Errorf("run(init -client emacs) = %d, want 2", code)
	}
	if _, err := os.Stat(filepath.Join(dir, ".mcp.json")); !os.IsNotExist(err) {
		t.Error("a rejected init still wrote .mcp.json")
	}
}

// A machine without the MCP binary gets guidance and exit 1, never a config
// pointing at a binary that does not exist.
func TestRunInitMissingBinaryFailsWithGuidance(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv("REPOTORY_MCP_BIN", "")
	dir := t.TempDir()
	code := run([]string{"init", "-client", "claude", "-bin", filepath.Join(dir, "nope"), "-cwd", dir})
	if code != 1 {
		t.Errorf("run(init) with a missing binary = %d, want 1", code)
	}
	if _, err := os.Stat(filepath.Join(dir, ".mcp.json")); !os.IsNotExist(err) {
		t.Error("init wrote .mcp.json despite the missing binary")
	}
}
