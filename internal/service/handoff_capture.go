package service

import (
	"fmt"

	"repotory/internal/db"
	"repotory/internal/importers/agents"
	"repotory/internal/importers/claude"
	"repotory/internal/service/wikipage"
)

// claudeCaptureConfigKey gates M1 session auto-capture. Default OFF: capture
// only runs when the value is exactly "1" (ADR-0010 决策落地: C端按需 opt-in).
// Reading other tools' session transcripts is a privacy-sensitive capability,
// so it is never enabled implicitly and is not part of any background poll.
// One switch gates every agent the capture path can read (claude, workbuddy,
// qoder, trae, dsh) — a second gate per agent would just multiply the places
// a user must opt in without changing what is protected.
const claudeCaptureConfigKey = "claude_session_capture"

// claudeCaptureEnabled reports whether the (opt-in) session capture is on.
func (s *Service) claudeCaptureEnabled() bool {
	v, err := db.GetConfig(s.db, claudeCaptureConfigKey)
	return err == nil && v == "1"
}

// CaptureClaudeHandoff performs an ON-DEMAND M1 capture: it reads the newest
// Claude Code session transcript for the given project and persists a
// machine-generated handoff note through the shared CreateHandoffNote path
// (tagged "handoff", so repotory_context surfaces it first next session).
//
// It is gated by the `claude_session_capture` config (default off) and is
// deliberately triggered only explicitly (no startup/hook automation on this
// path). The B-end SessionEnd-hook automation is a separate, opt-in step
// (ADR-0010 决策落地); this method is the capture primitive both share.
func (s *Service) CaptureClaudeHandoff(projectID int64) (*HandoffResult, error) {
	if !s.claudeCaptureEnabled() {
		return nil, fmt.Errorf("session capture is disabled (set %s=1 to enable)", claudeCaptureConfigKey)
	}
	proj, err := db.GetProjectByID(s.db, projectID)
	if err != nil {
		return nil, fmt.Errorf("project %d not found: %w", projectID, err)
	}
	sess, ok, err := claude.LatestSessionForRootPath(proj.RootPath)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("no capturable Claude Code session for %s", proj.RootPath)
	}
	in := handoffFromClaudeSession(sess)
	in.ProjectID = projectID
	return s.CreateHandoffNote(in)
}

// CaptureClaudeSessionByCwd is the Claude-specific face of the capture path
// (kept for the SessionEnd hook's args and existing tests). See
// CaptureAgentSessionByCwd for the cwd-vs-root rationale every agent shares.
func (s *Service) CaptureClaudeSessionByCwd(cwd string) (*HandoffResult, error) {
	return s.CaptureAgentSessionByCwd(agents.Claude, cwd)
}

// CaptureAgentSessionByCwd runs a one-shot capture for any supported agent
// (claude / workbuddy / qoder / trae / dsh, or "auto" = newest across all).
// It resolves the project for cwd through the same path tiers every other
// caller uses, then persists a machine-generated handoff note.
//
// The transcript is looked up by the CWD ITSELF, not the resolved project's
// root: agent stores key transcript dirs by the session's cwd, and path-tier
// resolution can attribute a cwd to a project whose root differs from it
// (tier 1: a repository row pointing at a container auto-group project).
// Looking up by the resolved root would systematically miss every such
// session; the project here only decides which bucket the note lands in.
//
// An empty agent means claude (the hook's historical default).
func (s *Service) CaptureAgentSessionByCwd(agent, cwd string) (*HandoffResult, error) {
	res := s.ResolveProjectByPath(cwd)
	if res == nil || res.Project == nil || len(res.Candidates) > 0 {
		return nil, fmt.Errorf("no unambiguous Cinder project matches cwd %q", cwd)
	}
	if !s.claudeCaptureEnabled() {
		return nil, fmt.Errorf("session capture is disabled (set %s=1 to enable)", claudeCaptureConfigKey)
	}
	if agent == "" {
		agent = agents.Claude
	}
	var (
		sess agents.Session
		ok   bool
		err  error
	)
	if agent == agents.Claude {
		// Claude keeps its own parser; LatestForCwd would re-enter the same
		// code through the adapter, so call it directly.
		cs, cok, cerr := claude.LatestSessionForRootPath(cwd)
		if cerr != nil {
			return nil, cerr
		}
		if !cok {
			return nil, fmt.Errorf("no capturable session for %s", cwd)
		}
		sess, ok = agentSessionFromClaude(cs), true
	} else if agent == "auto" {
		sess, ok, err = agents.LatestAny(cwd)
	} else {
		sess, ok, err = agents.LatestForCwd(cwd, agent)
	}
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("no capturable %s session for %s", agent, cwd)
	}
	in := handoffFromAgentSession(sess)
	in.ProjectID = res.Project.ID
	return s.CreateHandoffNote(in)
}

// agentSessionFromClaude lifts a Claude importer session into the shared
// agent-session shape (field-for-field; nothing is lost or invented).
func agentSessionFromClaude(cs claude.Session) agents.Session {
	return agents.Session{
		Agent:             agents.Claude,
		SessionID:         cs.SessionID,
		Cwd:               cs.Cwd,
		GitBranch:         cs.GitBranch,
		FirstUserPrompt:   cs.FirstUserPrompt,
		LastAssistantText: cs.LastAssistantText,
		ToolsUsed:         cs.ToolsUsed,
		MessageCount:      cs.MessageCount,
	}
}

// handoffFromClaudeSession keeps the desktop's on-demand Claude capture on
// the same mapping the agent-generic path uses.
func handoffFromClaudeSession(cs claude.Session) HandoffInput {
	return handoffFromAgentSession(agentSessionFromClaude(cs))
}

// handoffFromAgentSession maps a parsed agent session onto a HandoffInput,
// respecting the handoff write bounds enforced by CreateHandoffNote. Summary is
// the last assistant message (falling back to the first prompt); the tool list
// (transcript stores) or action list (memory stores like Trae) populate
// Changes so the record always satisfies the "at least one section"
// requirement even when the assistant text is terse.
func handoffFromAgentSession(sess agents.Session) HandoffInput {
	summary := sess.LastAssistantText
	if summary == "" {
		summary = sess.FirstUserPrompt
	}
	summary = truncateBytes(summary, maxHandoffSummaryLen)

	branch := sess.GitBranch
	if branch == "" {
		branch = "未知"
	}
	display := agents.DisplayNames[sess.Agent]
	if display == "" {
		display = sess.Agent
	}
	changes := []string{
		fmt.Sprintf("自动捕捉自 %s 会话 %s（git 分支 %s）", display, shortID(sess.SessionID), branch),
	}
	// Trae's memory summaries carry the actions directly; transcript stores
	// contribute the tool inventory instead.
	if len(sess.Actions) > 0 {
		itemChanges := boundedChangeItems(sess.Actions, "……（共 %d 项）")
		changes = append(changes, itemChanges...)
	} else if len(sess.ToolsUsed) > 0 {
		toolChanges := make([]string, 0, len(sess.ToolsUsed))
		for _, t := range sess.ToolsUsed {
			if len(toolChanges) >= maxHandoffItems-2 { // header + this bullet leave room
				break
			}
			toolChanges = append(toolChanges, "使用工具 "+truncateBytes(t, maxHandoffItemLen))
		}
		if len(sess.ToolsUsed) > len(toolChanges) {
			toolChanges = append(toolChanges, fmt.Sprintf("……（共 %d 种工具）", len(sess.ToolsUsed)))
		}
		changes = append(changes, toolChanges...)
	}

	agentName := agents.MachineNames[sess.Agent]
	if agentName == "" {
		agentName = sess.Agent
	}
	return HandoffInput{
		Agent:   agentName,
		Summary: summary,
		Changes: changes,
		Tags:    "auto-captured",
		// The session id is the whole point of the capture path being
		// idempotent: the hook can fire twice for one session (a crash, a
		// retry, a manual run after the hook), and a second "auto-captured"
		// handoff next to the first is noise the reader has to disambiguate
		// (ADR-0057 D1).
		SessionID: sess.SessionID,
	}
}

// boundedChangeItems caps a free-form list into Changes lines: at most
// maxHandoffItems-2 bullets (the header and the overflow bullet take the
// rest), each truncated to one item's byte budget, plus an overflow bullet
// carrying the true count.
func boundedChangeItems(items []string, overflowFormat string) []string {
	// Budget: 1 header line + at most (max-2) item lines + 1 overflow bullet
	// stays within maxHandoffItems total Changes.
	out := make([]string, 0, len(items))
	for _, t := range items {
		if len(out) >= maxHandoffItems-2 {
			break
		}
		out = append(out, truncateBytes(t, maxHandoffItemLen))
	}
	if len(items) > len(out) {
		out = append(out, fmt.Sprintf(overflowFormat, len(items)))
	}
	return out
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8] + "…"
	}
	if id == "" {
		return "?"
	}
	return id
}

// truncateBytes is the service-side alias for the one definition, now owned by
// wikipage (ADR-0060 step four's third caller was wikilint, which triggered the
// extraction). Kept as a wrapper so the call sites in this package read
// unchanged.
func truncateBytes(s string, max int) string {
	return wikipage.TruncateBytes(s, max)
}
