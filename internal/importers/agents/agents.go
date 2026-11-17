// Package agents provides one-shot session capture across the agent apps on
// this machine (ADR-0010 extended beyond Claude Code): WorkBuddy, Qoder,
// Trae CN and DeepSeek Harness (DSH), plus Claude Code itself. Each app keeps
// its own session store under the user home; none of these formats is a
// published contract, so every parser here is lenient by design — unknown
// lines are skipped and only the stable fields are relied on, exactly like
// the Claude importer (internal/importers/claude).
//
// A "one-shot" capture is a single explicit invocation: repotory-capture
// --agent <name> resolves the cwd's project, reads that agent's newest
// session transcript and writes one handoff note. It never runs implicitly
// (no poll, no auto-start): reading another tool's transcripts stays behind
// the claude_session_capture gate (ADR-0010 决策 1/2 — one opt-in switch
// covers every agent this package can read).
package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Agent ids accepted by repotory-capture --agent and the capture args.
const (
	Claude    = "claude"
	WorkBuddy = "workbuddy"
	Qoder     = "qoder"
	Trae      = "trae"
	DSH       = "dsh"
)

// All is the auto-detect probe order. Each entry names a store we know how to
// read; auto picks whichever store has the newest session file for the cwd.
var All = []string{Claude, WorkBuddy, Qoder, Trae, DSH}

// DisplayNames are the human labels used in handoff note headers.
var DisplayNames = map[string]string{
	Claude:    "Claude Code",
	WorkBuddy: "WorkBuddy",
	Qoder:     "Qoder",
	Trae:      "Trae",
	DSH:       "DeepSeek Harness",
}

// MachineNames are the per-note Agent values stored in HandoffInput.Agent.
var MachineNames = map[string]string{
	Claude:    "claude-code",
	WorkBuddy: "workbuddy",
	Qoder:     "qoder",
	Trae:      "trae",
	DSH:       "dsh",
}

// Known reports whether agent names a store this package can read.
func Known(agent string) bool {
	_, ok := DisplayNames[agent]
	return ok
}

// Session is the agent-agnostic summary one capture can build a handoff from.
type Session struct {
	Agent             string    // one of the agent ids above
	SessionID         string    // the store's own session id (idempotency key)
	Cwd               string    // cwd the session ran in, when the store records it
	GitBranch         string    // when the store records it
	FirstUserPrompt   string    // the session's goal
	LastAssistantText string    // the exit summary, when the store has raw turns
	ToolsUsed         []string  // tool names in first-seen order (transcript stores)
	Actions           []string  // what the agent did (memory-summary stores like Trae)
	MessageCount      int       // message lines seen (transcript stores)
	ModTime           time.Time // newest underlying file mtime (auto-detect ordering)
}

// HasContent reports whether the parse yielded anything a handoff could use.
func (s Session) HasContent() bool {
	return strings.TrimSpace(s.LastAssistantText) != "" ||
		strings.TrimSpace(s.FirstUserPrompt) != ""
}

// LatestForCwd reads the newest session for cwd from the named agent's store.
// ok=false (nil error) when that store has nothing usable for the cwd.
func LatestForCwd(cwd, agent string) (Session, bool, error) {
	switch agent {
	case Claude:
		return latestClaude(cwd)
	case WorkBuddy:
		return latestWorkBuddy(cwd)
	case Qoder:
		return latestQoder(cwd)
	case Trae:
		return latestTrae(cwd)
	case DSH:
		return latestDSH(cwd)
	default:
		return Session{}, false, fmt.Errorf("unknown agent %q (known: %s, auto)", agent, strings.Join(All, ", "))
	}
}

// LatestAny probes every known store for cwd and returns the newest session
// across them (by file mtime). ok=false when no store has anything.
func LatestAny(cwd string) (Session, bool, error) {
	var best Session
	found := false
	for _, agent := range All {
		sess, ok, err := LatestForCwd(cwd, agent)
		if err != nil {
			return Session{}, false, err
		}
		if ok && (!found || sess.ModTime.After(best.ModTime)) {
			best = sess
			found = true
		}
	}
	return best, found, nil
}

// homeJoin returns a path under the user home (or an error that reads as "no
// home" — callers treat it like an empty store, not a hard failure).
func homeJoin(elems ...string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{home}, elems...)...), nil
}

// slugOf is the cwd with its leading "/" dropped and the rest "/" -> "-"
// (/a/b -> a-b). The stores disagree on whether they prepend their own dash:
// Claude Code and Qoder do, WorkBuddy does not, Trae prefixes one, DSH wraps
// in double dashes — so each store adds its own decoration on top of this
// canonical form instead of re-deriving it.
func slugOf(cwd string) string {
	return strings.ReplaceAll(strings.TrimPrefix(cwd, "/"), "/", "-")
}

// newestFile returns the newest regular file matching pattern, or "" when
// none exists. Read errors on the glob are surfaced.
func newestFile(pattern string) (string, time.Time, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", time.Time{}, err
	}
	var best string
	var bestMod time.Time
	for _, m := range matches {
		fi, err := os.Stat(m)
		if err != nil || fi.IsDir() {
			continue
		}
		if best == "" || fi.ModTime().After(bestMod) {
			best, bestMod = m, fi.ModTime()
		}
	}
	return best, bestMod, nil
}

// truncateRunes keeps the tail-heavy text fields bounded.
func truncateRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}
