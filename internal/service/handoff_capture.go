package service

import (
	"fmt"

	"repotory/internal/db"
	"repotory/internal/importers/claude"
	"repotory/internal/service/wikipage"
)

// claudeCaptureConfigKey gates M1 session auto-capture. Default OFF: capture
// only runs when the value is exactly "1" (ADR-0010 决策落地: C端按需 opt-in).
// Reading raw ~/.claude session transcripts is a privacy-sensitive capability,
// so it is never enabled implicitly and is not part of any background poll.
const claudeCaptureConfigKey = "claude_session_capture"

// claudeCaptureEnabled reports whether the (opt-in) Claude session capture is on.
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
		return nil, fmt.Errorf("Claude session capture is disabled (set %s=1 to enable)", claudeCaptureConfigKey)
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
	in := handoffFromSession(sess)
	in.ProjectID = projectID
	return s.CreateHandoffNote(in)
}

// CaptureClaudeSessionByCwd resolves the project for a working directory
// through the same path tiers every other caller uses, then runs the same
// gated on-demand capture. It exists for the B-end SessionEnd hook: Claude
// Code runs `repotory-capture` with the closing session's cwd. A short final
// path segment used to substring-match unrelated projects; an ambiguous or
// unknown path now writes nothing rather than guessing.
func (s *Service) CaptureClaudeSessionByCwd(cwd string) (*HandoffResult, error) {
	res := s.ResolveProjectByPath(cwd)
	if res == nil || res.Project == nil || len(res.Candidates) > 0 {
		return nil, fmt.Errorf("no unambiguous Cinder project matches cwd %q", cwd)
	}
	return s.CaptureClaudeHandoff(res.Project.ID)
}

// handoffFromSession maps a parsed Claude session onto a HandoffInput,
// respecting the handoff write bounds enforced by CreateHandoffNote. Summary is
// the last assistant message (falling back to the first prompt); the tool list
// and git branch populate Changes so the record always satisfies the
// "at least one section" requirement even when the assistant text is terse.
func handoffFromSession(sess claude.Session) HandoffInput {
	summary := sess.LastAssistantText
	if summary == "" {
		summary = sess.FirstUserPrompt
	}
	summary = truncateBytes(summary, maxHandoffSummaryLen)

	branch := sess.GitBranch
	if branch == "" {
		branch = "未知"
	}
	changes := []string{
		fmt.Sprintf("自动捕捉自 Claude Code 会话 %s（git 分支 %s）", shortID(sess.SessionID), branch),
	}
	if len(sess.ToolsUsed) > 0 {
		// Budget: 1 header line + at most (max-2) tool lines + 1 overflow bullet
		// stays within maxHandoffItems total Changes.
		toolChanges := make([]string, 0, len(sess.ToolsUsed))
		for _, t := range sess.ToolsUsed {
			if len(toolChanges) >= maxHandoffItems-2 { // header + this bullet leave room
				toolChanges = append(toolChanges, fmt.Sprintf("……（共 %d 种工具）", len(sess.ToolsUsed)))
				break
			}
			toolChanges = append(toolChanges, "使用工具 "+truncateBytes(t, maxHandoffItemLen))
		}
		changes = append(changes, toolChanges...)
	}

	return HandoffInput{
		Agent:   "claude-code",
		Summary: summary,
		Changes: changes,
		Tags:    "auto-captured",
		// The session id is the whole point of the capture path being
		// idempotent: the SessionEnd hook can fire twice for one session (a
		// crash, a retry, a manual run after the hook), and a second
		// "auto-captured" handoff next to the first is noise the reader has to
		// disambiguate (ADR-0057 D1).
		SessionID: sess.SessionID,
	}
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
