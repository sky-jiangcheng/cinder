// instructions.go — the managed block that teaches an agent to read and write
// the memory layer (ADR-0043).
//
// Why this file exists. The two instructions have been in the manual since the
// recipe page was written ("call repotory_context before starting work, call
// repotory_handoff before ending it"), and the same page admitted the gap:
// "no guarantee the model will call tools on its own — writing it into the
// client's prompt is the reliable approach". The product never turned that
// sentence into anything clickable, so the 2.1 timebox measured zero handoffs
// while six agent sessions ran in the same window: the tools were registered,
// nothing ever told the agent to use them.
//
// This is the protocol-neutral half of that fix. The Claude Code SessionEnd
// hook (ADR-0010) only exists for Claude Code, and it swallows every failure,
// so "installed but silent" and "not installed" look identical. An
// instruction file is readable on disk, works for every agent that reads
// AGENTS.md, and is written by the agent that is already doing the work — no
// second model round paid at session end.
//
// Both implementations (this package and scripts/repotory-init/index.mjs) must
// write the same bytes; the golden fixture in testdata/contract.json pins it.
package agentinit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// InstructionsBeginMark and InstructionsEndMark delimit the managed block.
	// They are HTML comments so the file stays valid Markdown and the block
	// renders as nothing.
	InstructionsBeginMark = "<!-- repotory:begin -->"
	InstructionsEndMark   = "<!-- repotory:end -->"
)

// InstructionFiles are the project instruction files the block is written to.
//
// Both, always — not one chosen per client. An instruction file nobody reads is
// inert: it produces no behaviour and no error. Writing both costs one extra
// file; picking per client costs a silent break the first time the user switches
// agent. AGENTS.md is the de facto cross-agent standard (Codex, opencode,
// Cursor, Amp all read it); CLAUDE.md is Claude Code's own.
var InstructionFiles = []string{"AGENTS.md", "CLAUDE.md"}

// instructionsBlockBody is the text inside the markers.
//
// Three sentences and a negative criterion, deliberately short: this lands in
// the user's repository and every agent reads it into its context on every
// session, so it competes for the same attention as the project's own
// instructions. It is in English for the same reason the hook's prompt is —
// it is machine-facing instruction text, not UI copy, so it does not go through
// web/src/locales and is shared by both implementations as one string.
//
// The negative criterion is the substance of this file. Without it the
// instruction makes agents log activity — the shape this product's upstream
// already starved on (idea notes 0, manual notes 1) — instead of carrying over
// the state a later session would otherwise re-discover the hard way.
const instructionsBlockBody = `## Repotory memory layer

This project's knowledge and session memory live in Repotory, a local SQLite
database, reachable through the repotory_* MCP tools.

- At the start of work, call repotory_context with the current repository path
  and read what it returns before you plan. Its first entry is the most recent
  session handoff, so the previous session's conclusions come first.
- Before you finish, call repotory_handoff for this project with the fields
  gotchas, decisions, changes, next_steps and a short summary.
- Record what a later session would otherwise re-learn the hard way — a
  constraint that is not visible in the code, a decision and the alternative
  it rejected, a trap you just hit. Do not record what the commit log already
  shows: a list of files touched is not a handoff, and the notes that bury the
  useful signal are the ones that stop the next session from reading them.

Installed by Repotory. To remove, delete everything between the repotory:begin
and repotory:end markers (this block is plain text in your repository, and it
will show up in git status until you commit or delete it).
`

// InstructionsBlock renders the full managed block, markers included.
func InstructionsBlock() string {
	return InstructionsBeginMark + "\n" + instructionsBlockBody + InstructionsEndMark + "\n"
}

// upsertInstructions places the managed block in one file's content without
// touching anything outside it.
//
// Three shapes, and the order matters:
//   - our block is already there → replace it in place (this makes the operation
//     idempotent: running twice yields identical bytes and reports "unchanged")
//   - the file exists with other content → append, separated by a blank line
//   - there is no file at all → the block is the whole file
//
// Anything outside the markers is user content and is never reformatted,
// reordered or removed. We own that paragraph, not the file.
func upsertInstructions(content string, hadFile bool) string {
	block := InstructionsBlock()
	begin := strings.Index(content, InstructionsBeginMark)
	end := strings.Index(content, InstructionsEndMark)
	if begin >= 0 && end > begin {
		end += len(InstructionsEndMark)
		// Keep whatever trailing newline the old block had, so an idempotent
		// second run does not shift the bytes after the marker.
		tail := content[end:]
		nl := 0
		for nl < len(tail) && (tail[nl] == '\n' || tail[nl] == '\r') {
			nl++
		}
		replaced := content[:begin] + block + tail[nl:]
		return replaced
	}
	if !hadFile || strings.TrimSpace(content) == "" {
		return block
	}
	trimmed := strings.TrimRight(content, "\n")
	return trimmed + "\n\n" + block
}

// installInstructions writes the managed block into every instruction file of
// one project directory and reports each file separately, in the order of
// InstructionFiles.
//
// A failure on one file does not stop the other: a read-only AGENTS.md should
// not prevent CLAUDE.md from receiving the instruction. Each failure lands in
// the per-file note so the UI can say which one did not make it.
func installInstructions(targetDir string) []FileResult {
	out := make([]FileResult, 0, len(InstructionFiles))
	for _, rel := range InstructionFiles {
		out = append(out, writeInstructionsFile(filepath.Join(targetDir, rel), rel))
	}
	return out
}

// writeInstructionsFile applies upsertInstructions to one path.
func writeInstructionsFile(file, rel string) FileResult {
	res := FileResult{Path: file, Status: StatusUnchanged}

	raw, err := os.ReadFile(file)
	hadFile := err == nil
	if err != nil && !os.IsNotExist(err) {
		res.Status = StatusFailed
		res.Note = fmt.Sprintf("cannot read %s: %v", file, err)
		return res
	}

	next := upsertInstructions(string(raw), hadFile)
	if hadFile && string(raw) == next {
		return res
	}

	if hadFile {
		backup := file + BackupSuffix
		if err := os.WriteFile(backup, raw, 0o600); err != nil {
			res.Status = StatusFailed
			res.Note = fmt.Sprintf("cannot back up %s: %v", file, err)
			return res
		}
		res.Note = "backup: " + backup
		res.Status = StatusUpdated
	} else {
		res.Status = StatusCreated
	}

	if err := os.WriteFile(file, []byte(next), 0o644); err != nil {
		res.Status = StatusFailed
		res.Note = fmt.Sprintf("cannot write %s: %v", file, err)
		return res
	}
	return res
}
