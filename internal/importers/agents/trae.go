package agents

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Trae CN does not keep raw transcripts; it writes per-session memory
// summaries at ~/.trae-cn/memory/projects/<slug>/<YYYYMMDD>/session_memory_<id>.jsonl
// with one structured record per line: intent (the session's goal), actions
// (what was done) and outcome (where it ended). That is already handoff-shaped
// material — intent maps to the prompt, actions to Changes items, outcome to
// the summary.
//
// The project slug is "-<path-with-dashes>" followed by a workspace suffix
// "-p<digits>-<hash>" (e.g. -Users-me-Workspace-Script--p2-0329e6…). The
// suffix's dash keeps a cwd from swallowing its children: /a/Script matches
// only dirs whose remainder after the path slug is empty or starts the
// workspace marker, never /a/Script/RepoNest.

var traeWorkspaceSuffix = regexp.MustCompile(`^-{1,2}p\d+-`)

type traeMemory struct {
	Intent  string   `json:"intent"`
	Actions []string `json:"actions"`
	Outcome string   `json:"outcome"`
}

func latestTrae(cwd string) (Session, bool, error) {
	if strings.TrimSpace(cwd) == "" {
		return Session{}, false, nil
	}
	root, err := homeJoin(".trae-cn", "memory", "projects")
	if err != nil {
		return Session{}, false, nil
	}
	prefix := "-" + slugOf(cwd)
	entries, err := os.ReadDir(root)
	if err != nil {
		return Session{}, false, nil
	}
	var dirs []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() {
			continue
		}
		if name == prefix {
			dirs = []string{name}
			break
		}
		remainder := strings.TrimPrefix(name, prefix+"-")
		if remainder != name && traeWorkspaceSuffix.MatchString(remainder) {
			dirs = append(dirs, name)
		}
	}
	for _, dirName := range dirs {
		latest, mod, err := newestFile(root + "/" + dirName + "/*/session_memory_*.jsonl")
		if err != nil || latest == "" {
			continue
		}
		sess, ok, err := parseTraeFile(latest)
		if err != nil || !ok {
			continue
		}
		sess.Cwd = cwd
		sess.ModTime = mod
		return sess, true, nil
	}
	return Session{}, false, nil
}

func parseTraeFile(path string) (Session, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return Session{}, false, err
	}
	defer f.Close()
	var s Session
	s.Agent = Trae
	// session_memory_<id>.jsonl — the filename carries the session id.
	base := filepath.Base(path)
	s.SessionID = strings.TrimSuffix(strings.TrimPrefix(base, "session_memory_"), ".jsonl")
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for sc.Scan() {
		var m traeMemory
		if json.Unmarshal([]byte(sc.Text()), &m) != nil {
			continue
		}
		if t := strings.TrimSpace(m.Intent); t != "" && s.FirstUserPrompt == "" {
			s.FirstUserPrompt = truncateRunes(t, 6000)
		}
		if t := strings.TrimSpace(m.Outcome); t != "" {
			s.LastAssistantText = truncateRunes(t, 6000)
		}
		for _, a := range m.Actions {
			if t := strings.TrimSpace(a); t != "" {
				s.Actions = append(s.Actions, t)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Session{}, false, err
	}
	if !s.HasContent() {
		return Session{}, false, nil
	}
	return s, true, nil
}
