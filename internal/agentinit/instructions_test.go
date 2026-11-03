package agentinit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The managed block is the whole of ADR-0043's behaviour, so it is pinned by
// tests rather than by prose: an idempotent second run, user content preserved
// outside the markers, a missing file created, and a stale block replaced in
// place. None of these are visible to a type checker or to the contract fixture.

func TestUpsertCreatesBlockWhenNoFile(t *testing.T) {
	got := upsertInstructions("", false)
	if got != InstructionsBlock() {
		t.Errorf("a missing file should become exactly the block, got %q", got)
	}
}

func TestUpsertAppendsAfterUserContent(t *testing.T) {
	content := "# My project\n\nBuild with make.\n"
	got := upsertInstructions(content, true)
	if !strings.HasPrefix(got, content) {
		t.Errorf("the user's content must stay byte-for-byte at the top, got %q", got)
	}
	// upsert trims the file's trailing newlines before adding exactly one blank
	// line, so the seam is the trimmed content plus "\n\n".
	trimmed := strings.TrimRight(content, "\n")
	if !strings.Contains(got, trimmed+"\n\n"+InstructionsBeginMark) {
		t.Errorf("the block should be appended after exactly one blank line, got %q", got)
	}
	if !strings.HasSuffix(got, InstructionsEndMark+"\n") {
		t.Errorf("the block must end with the end marker, got %q", got[len(got)-40:])
	}
}

// The idempotency test is the one that matters most: a button clicked twice
// must not stack two blocks. Two blocks would mean the agent reads the same
// instruction twice on every session, and every later edit of the block would
// have to find and replace both.
func TestUpsertIsIdempotent(t *testing.T) {
	once := upsertInstructions("# My project\n\nBuild with make.\n", true)
	twice := upsertInstructions(once, true)
	if once != twice {
		t.Errorf("a second run must change nothing:\nonce:\n%s\ntwice:\n%s", once, twice)
	}
	if n := strings.Count(twice, InstructionsBeginMark); n != 1 {
		t.Errorf("expected exactly one begin marker, found %d", n)
	}
}

func TestUpsertReplacesStaleBlockInPlace(t *testing.T) {
	stale := "# Project\n\nbefore\n\n" + InstructionsBeginMark + "\nOLD TEXT\n" +
		InstructionsEndMark + "\n\nafter\n"
	got := upsertInstructions(stale, true)
	if strings.Contains(got, "OLD TEXT") {
		t.Error("the stale block body must be gone")
	}
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Error("content on both sides of the block must survive")
	}
	if !strings.Contains(got, "before\n\n"+InstructionsBeginMark) {
		t.Errorf("the replacement should stay where the old block was, got %q", got)
	}
	if n := strings.Count(got, InstructionsBeginMark); n != 1 {
		t.Errorf("expected exactly one block, found %d", n)
	}
}

// A begin marker without an end marker is a user who edited the block by hand
// and deleted one line. Appending rather than matching a half-open region keeps
// us from deleting everything after that marker — which is the one way this
// function could eat the user's file.
func TestUpsertHalfOpenMarkerAppends(t *testing.T) {
	content := "intro\n\n" + InstructionsBeginMark + "\nuser trimmed this\n\nthe rest of the file\n"
	got := upsertInstructions(content, true)
	if !strings.Contains(got, "the rest of the file") {
		t.Error("a half-open block must never cause trailing content to be removed")
	}
	if n := strings.Count(got, InstructionsEndMark); n != 1 {
		t.Errorf("expected one end marker after appending a complete block, found %d", n)
	}
}

func TestInstallInstructionsWritesBothFiles(t *testing.T) {
	dir := t.TempDir()
	got := installInstructions(dir)
	if len(got) != len(InstructionFiles) {
		t.Fatalf("expected %d results, got %d", len(InstructionFiles), len(got))
	}
	for i, rel := range InstructionFiles {
		res := got[i]
		if res.Status != StatusCreated {
			t.Errorf("%s: expected created, got %s (%s)", rel, res.Status, res.Note)
		}
		raw, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if string(raw) != InstructionsBlock() {
			t.Errorf("%s: content should be exactly the block", rel)
		}
	}
}

// Both files, always — ADR-0043 decision 2. An instruction file nobody reads is
// inert: it produces no behaviour and no error, which is precisely how the
// product ended up with a memory layer and zero handoffs.
func TestInstructionFilesCoverStandardAndClaude(t *testing.T) {
	if len(InstructionFiles) < 2 {
		t.Fatalf("both AGENTS.md and CLAUDE.md must be written, got %v", InstructionFiles)
	}
	found := map[string]bool{}
	for _, f := range InstructionFiles {
		found[f] = true
	}
	for _, want := range []string{"AGENTS.md", "CLAUDE.md"} {
		if !found[want] {
			t.Errorf("%s missing from InstructionFiles: %v", want, InstructionFiles)
		}
	}
}

func TestInstallInstructionsSecondRunIsUnchanged(t *testing.T) {
	dir := t.TempDir()
	first := installInstructions(dir)
	for _, r := range first {
		if r.Status != StatusCreated {
			t.Fatalf("setup: %s -> %s", r.Path, r.Status)
		}
	}
	second := installInstructions(dir)
	for i, r := range second {
		if r.Status != StatusUnchanged {
			t.Errorf("%s: second run should be unchanged, got %s", InstructionFiles[i], r.Status)
		}
	}
}

// The report must tell the user a file was edited, not silently rewrite a
// tracked file. This is the same posture as the MCP config: back up, then write.
func TestInstallInstructionsBacksUpExistingFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(file, []byte("# Mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := installInstructions(dir)
	var agents *FileResult
	for i := range res {
		if filepath.Base(res[i].Path) == "AGENTS.md" {
			agents = &res[i]
		}
	}
	if agents == nil {
		t.Fatal("AGENTS.md not reported")
	}
	if agents.Status != StatusUpdated {
		t.Fatalf("expected updated, got %s", agents.Status)
	}
	if !strings.HasPrefix(agents.Note, "backup: ") {
		t.Errorf("an edited file must report its backup, note=%q", agents.Note)
	}
	raw, err := os.ReadFile(file + BackupSuffix)
	if err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if string(raw) != "# Mine\n" {
		t.Errorf("backup should hold the original, got %q", raw)
	}
	now, _ := os.ReadFile(file)
	if !strings.HasPrefix(string(now), "# Mine\n") {
		t.Error("the original content must survive at the top of the file")
	}
}

func TestRegisterIncludesInstructionsWhenRequested(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "repotory-mcp")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rep, err := Register(Options{
		TargetDir:        dir,
		BinPath:          bin,
		Clients:          []Client{ClientClaude},
		WithInstructions: true,
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(rep.Instructions) != len(InstructionFiles) {
		t.Fatalf("expected %d instruction results, got %d", len(InstructionFiles), len(rep.Instructions))
	}
	if len(rep.Warnings) != 0 {
		t.Errorf("no failures expected, warnings: %v", rep.Warnings)
	}
}

func TestRegisterOmitsInstructionsByDefault(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "repotory-mcp")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rep, err := Register(Options{TargetDir: dir, BinPath: bin, Clients: []Client{ClientClaude}})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if rep.Instructions != nil {
		t.Errorf("instructions must not be written unless asked, got %v", rep.Instructions)
	}
	for _, rel := range InstructionFiles {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			t.Errorf("%s was created without WithInstructions", rel)
		}
	}
}

// The block names the negative criterion, because that is the part the product
// got wrong before: an instruction to "record what you did" produces a commit
// log in prose — the exact shape the knowledge layer starved on. A test pins
// it so trimming the block down cannot silently drop the only sentence that
// distinguishes a handoff from a changelog.
func TestInstructionsBlockCarriesTheNegativeCriterion(t *testing.T) {
	// Compared with whitespace collapsed: the block wraps its lines for the
	// reader, and a hard substring match would break on a reflow that changes
	// nothing about the instruction.
	block := strings.Join(strings.Fields(InstructionsBlock()), " ")
	for _, want := range []string{
		"repotory_context",
		"repotory_handoff",
		"gotchas",
		"decisions",
		"next_steps",
		"Do not record what the commit log already shows",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the block must contain %q", want)
		}
	}
	if !strings.Contains(InstructionsBlock(), InstructionsBeginMark) ||
		!strings.Contains(InstructionsBlock(), InstructionsEndMark) {
		t.Error("the block must be wrapped in both markers")
	}
}
