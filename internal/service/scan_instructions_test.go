package service

// The automatic top-up that makes "a project Cinder manages" mean "its agents
// have the protocol". The block bytes and the upsert rules are pinned in
// internal/agentinit; what is pinned here is the policy: which projects get it,
// when it is considered current, and that turning the gate off really stops the
// writes.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"repotory/internal/db"
)

func TestAutoInstallWritesTheBlockForCollectedProjects(t *testing.T) {
	s, _ := setupAgentService(t)
	dir := t.TempDir()
	pid := seedManualProject(t, s, "demo", dir)

	written, failed := s.autoInstallAgentInstructions([]int64{pid})
	if written != 1 || failed != 0 {
		t.Fatalf("written=%d failed=%d", written, failed)
	}
	for _, rel := range []string{"AGENTS.md", "CLAUDE.md"} {
		raw, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if !strings.Contains(string(raw), "repotory_handoff") {
			t.Errorf("%s does not carry the block:\n%s", rel, raw)
		}
	}

	// A second pass must not rewrite: the block is current, so the file's
	// mtime and bytes stay put. A scan runs often; a write per scan would
	// leave every repository permanently dirty in git status.
	before, err := os.Stat(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if written, failed := s.autoInstallAgentInstructions([]int64{pid}); written != 0 || failed != 0 {
		t.Fatalf("second pass written=%d failed=%d, want 0/0", written, failed)
	}
	after, err := os.Stat(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("a current block was rewritten")
	}
}

func TestAutoInstallReplacesAStaleBlock(t *testing.T) {
	s, _ := setupAgentService(t)
	dir := t.TempDir()
	pid := seedManualProject(t, s, "demo", dir)

	// An older release's block, with the user's own text around it. The
	// refresh must replace only the marked region.
	stale := "# Project notes\n\nkeep me\n\n" +
		"<!-- repotory:begin -->\nold protocol\n<!-- repotory:end -->\n\nkeep me too\n"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	if !instructionsNeedUpdate(dir) {
		t.Fatal("a stale block must need an update")
	}
	if written, failed := s.autoInstallAgentInstructions([]int64{pid}); written != 1 || failed != 0 {
		t.Fatalf("written=%d failed=%d", written, failed)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if strings.Contains(got, "old protocol") {
		t.Error("the stale block survived")
	}
	if !strings.HasPrefix(got, "# Project notes\n\nkeep me\n\n") || !strings.HasSuffix(got, "keep me too\n") {
		t.Errorf("text outside the markers was rewritten:\n%s", got)
	}
	if !strings.Contains(got, "repotory_handoff") {
		t.Error("the current block did not land")
	}
}

func TestAutoInstallSkipsContainersAndHonoursTheOffSwitch(t *testing.T) {
	s, _ := setupAgentService(t)
	containerDir := t.TempDir()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	container, err := db.SyncProjectTx(tx, "home", containerDir, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// A scan-time container is not a project the user works in; its
	// instruction file would apply to every repository underneath it.
	if written, failed := s.autoInstallAgentInstructions([]int64{container}); written != 0 || failed != 0 {
		t.Fatalf("container written=%d failed=%d", written, failed)
	}
	if _, err := os.Stat(filepath.Join(containerDir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Error("a container received an instruction file")
	}

	// agent_instructions_auto=0 must stop the write even for a real project.
	dir := t.TempDir()
	pid := seedManualProject(t, s, "demo", dir)
	if err := db.SetConfig(s.db, agentInstructionsAutoKey, "0"); err != nil {
		t.Fatal(err)
	}
	if written, _ := s.autoInstallAgentInstructions([]int64{pid}); written != 0 {
		t.Fatalf("wrote %d project(s) with the gate off", written)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Error("the gate was ignored")
	}
}
