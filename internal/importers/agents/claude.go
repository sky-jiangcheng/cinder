package agents

import "repotory/internal/importers/claude"

// latestClaude delegates to the Claude importer: Claude Code stores raw
// transcripts at ~/.claude/projects/<slug>/*.jsonl and already has the
// format-verified parser (ADR-0010). Session ids flow through untouched so
// hook retries stay idempotent (ADR-0057 D1).
func latestClaude(cwd string) (Session, bool, error) {
	s, ok, err := claude.LatestSessionForRootPath(cwd)
	if err != nil || !ok {
		return Session{}, false, err
	}
	return Session{
		Agent:             Claude,
		SessionID:         s.SessionID,
		Cwd:               s.Cwd,
		GitBranch:         s.GitBranch,
		FirstUserPrompt:   s.FirstUserPrompt,
		LastAssistantText: s.LastAssistantText,
		ToolsUsed:         s.ToolsUsed,
		MessageCount:      s.MessageCount,
	}, true, nil
}
