package agents

import (
	"os"
	"strings"

	"repotory/internal/importers/claude"
)

// Qoder stores raw session transcripts at
// ~/.qoder/projects/-<slug>/<sessionId>.jsonl (plus a same-named directory
// for session state). Its line format is the Claude Code envelope —
// type user/assistant, message.content blocks, isSidechain, per-line cwd —
// so parsing reuses the format-verified Claude parser instead of forking it.
// The slug carries a leading dash ("/" + cwd with "/" -> "-") and some
// workspaces append an instance suffix after the path slug; because every
// message line repeats the cwd, candidates from the prefix match are
// accepted only when the parsed session's cwd agrees.

func latestQoder(cwd string) (Session, bool, error) {
	if strings.TrimSpace(cwd) == "" {
		return Session{}, false, nil
	}
	projects, err := homeJoin(".qoder", "projects")
	if err != nil {
		return Session{}, false, nil
	}
	prefix := "-" + slugOf(cwd)
	entries, err := os.ReadDir(projects)
	if err != nil {
		return Session{}, false, nil
	}
	// Exact slug wins; otherwise accept suffix dirs, newest first. The parsed
	// cwd check below keeps a prefix collision (cwd Script vs Script-RepoNest)
	// from attributing another workspace's session.
	var dirs []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || name != prefix && !strings.HasPrefix(name, prefix+"-") {
			continue
		}
		if name == prefix {
			dirs = []string{e.Name()}
			break
		}
		dirs = append(dirs, name)
	}
	for _, dirName := range dirs {
		latest, mod, err := newestFile(projects + "/" + dirName + "/*.jsonl")
		if err != nil || latest == "" {
			continue
		}
		sess, ok, err := parseQoderFile(latest, cwd)
		if err != nil || !ok {
			continue
		}
		sess.ModTime = mod
		return sess, true, nil
	}
	return Session{}, false, nil
}

func parseQoderFile(path, cwd string) (Session, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return Session{}, false, err
	}
	defer f.Close()
	cs, err := claude.ParseSession(f)
	if err != nil {
		return Session{}, false, err
	}
	if cs.Cwd != "" && cs.Cwd != cwd {
		return Session{}, false, nil // another workspace's suffixed slug dir
	}
	if !cs.HasContent() {
		return Session{}, false, nil
	}
	return Session{
		Agent:             Qoder,
		SessionID:         cs.SessionID,
		Cwd:               cs.Cwd,
		GitBranch:         cs.GitBranch,
		FirstUserPrompt:   cs.FirstUserPrompt,
		LastAssistantText: cs.LastAssistantText,
		ToolsUsed:         cs.ToolsUsed,
		MessageCount:      cs.MessageCount,
	}, true, nil
}
