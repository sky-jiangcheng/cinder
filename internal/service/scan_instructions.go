package service

// scan_instructions.go — writes the ADR-0043 instruction block on the moment a
// project comes under management, instead of waiting for a click.
//
// Why this is not the same decision as "the setup dialog writes into my files".
// That dialog is the user wiring ONE project on purpose. This is the other end:
// a scan is what makes a directory a project, and a project whose agent never
// received the block starts every session blind — the exact shape the
// 2026-11-10 audit measured (handoff feature idle for 15 days, first cause: no
// project had the block). Per-agent registration cannot close that gap either:
// there are now a dozen clients, several with no documented config path, and
// every one of them reads AGENTS.md. So the block IS the registration surface,
// and it is written for every collected project, every agent at once.
//
// The block is also the version carrier: upsert replaces a stale block in
// place, so a block written by an older release gets refreshed on the next
// scan and the instruction text cannot fossilise in hundreds of repositories.
//
// Boundaries kept from the one-click path (they are not weakened by being
// automatic):
//   - scan-time containers, the synthetic default row and missing directories
//     are skipped, because there is no project directory to instruct
//     (refusalForProject);
//   - only the marked block is touched — anything outside the markers is the
//     user's content and is never reformatted;
//   - a per-file failure is counted, not fatal: a read-only AGENTS.md must not
//     fail a scan that otherwise succeeded.
//
// "agent_instructions_auto=0" turns the automatic write off entirely. Unset
// means ON: the failure mode of the block is one file in git status, the
// failure mode of its absence is a memory layer nobody writes to.

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"repotory/internal/agentinit"
	"repotory/internal/db"
)

// agentInstructionsAutoKey is the config key that disables the automatic write.
const agentInstructionsAutoKey = "agent_instructions_auto"

// agentInstructionsAutoEnabled reads the gate. Unset (or any value other than
// "0") means enabled — see the file comment for why the safe default is ON
// here, which is the opposite of the transcript-reading gates.
func (s *Service) agentInstructionsAutoEnabled() bool {
	v, err := db.GetConfig(s.db, agentInstructionsAutoKey)
	return err != nil || v != "0"
}

// instructionsNeedUpdate reports whether a directory's instruction files are
// missing the current block, carry an older one, or do not exist yet.
//
// Compared against the full rendered block rather than the begin marker: the
// marker answers "did Cinder ever write here", which goes stale the moment the
// text changes, and a stale block teaches the previous protocol.
func instructionsNeedUpdate(dir string) bool {
	found := false
	want := agentinit.InstructionsBlock()
	for _, rel := range agentinit.InstructionFiles {
		raw, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			continue
		}
		found = true
		if !strings.Contains(string(raw), want) {
			return true
		}
	}
	return !found
}

// autoInstallAgentInstructions tops up every collected project that is missing
// the block. Returns how many projects were written and how many failed.
//
// It runs after the scan transaction commits, so a project that was just
// marked collected is already in the id list and gets the block in the same
// run — the user does not have to scan twice.
func (s *Service) autoInstallAgentInstructions(ids []int64) (written, failed int) {
	if !s.agentInstructionsAutoEnabled() {
		return 0, 0
	}
	for _, id := range ids {
		project, err := db.GetProjectByID(s.db, id)
		if err != nil {
			log.Printf("agent instructions auto-install: load project %d: %v", id, err)
			failed++
			continue
		}
		if refusalForProject(project) != AgentSetupOK {
			continue
		}
		dir := strings.TrimSpace(project.RootPath)
		if dir == "" {
			continue
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			// A path on an unmounted volume is not a failure to report: the
			// next scan with the volume back mounts it and tops it up.
			continue
		}
		if !instructionsNeedUpdate(dir) {
			continue
		}
		report, err := agentinit.Register(agentinit.Options{
			TargetDir:        dir,
			InstructionsOnly: true,
			WithInstructions: true,
		})
		if err != nil {
			log.Printf("agent instructions auto-install: %s: %v", dir, err)
			failed++
			continue
		}
		ok := true
		for _, f := range report.Instructions {
			if f.Status == agentinit.StatusFailed {
				log.Printf("agent instructions auto-install: %s: %s", f.Path, f.Note)
				ok = false
			}
		}
		if !ok {
			failed++
			continue
		}
		written++
	}
	return written, failed
}
