package claude

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// M1 session-auto-capture parser (ADR-0010). This file is the mechanical,
// format-verified core only: it turns a Claude Code session transcript
// (~/.claude/projects/<slug>/<sessionId>.jsonl) into a structured summary that
// a handoff can be built from. It performs NO side effects — it does not write
// notes and nothing in the app invokes it on its own. The enablement decision
// (opt-in default, and hook vs on-demand trigger — ADR-0010 决策 1/4) and the
// Session->HandoffInput mapping in the service layer are intentionally left as
// gated next steps, so no transcript is ever read automatically until product
// signs off on the privacy posture.
//
// Claude Code's transcript format is not a published contract and drifts across
// versions (observed: v2.1.278 carries bespoke line types like
// "file-history-snapshot", "cost-state", "attachment"). Parsing is therefore
// lenient by design (ADR-0010 决策 5): unknown line types are ignored, and only
// the stable envelope fields (sessionId, cwd, gitBranch, timestamp) plus
// assistant/user `message.content` text and tool_use names are relied on.

const (
	maxSessionLineBytes = 8 << 20 // 8 MiB per JSONL line
	maxRetainedRunes    = 6000    // cap on the retained last-assistant text
)

// Session is the summarized view of one Claude Code transcript.
type Session struct {
	SessionID         string
	Cwd               string
	GitBranch         string
	LastTimestamp     string   // RFC3339 of the newest timestamped line seen
	FirstUserPrompt   string   // first user text block (the session's goal)
	LastAssistantText string   // newest assistant text block (the exit summary)
	ToolsUsed         []string // tool_use names in first-seen order, de-duplicated
	MessageCount      int      // assistant+user message lines seen (main chain only)
}

// contentBlock is one item in an Anthropic message `content` array. Only the
// fields we rely on are decoded; others are ignored.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Name string `json:"name"` // tool_use
}

// contentBlocks tolerates the two shapes Claude Code writes for
// message.content: a block array (most versions) and a plain string
// (observed on v2.1.296 user turns). A string is treated as one text block;
// a line whose content is neither form fails to unmarshal and is skipped
// whole — same lenient posture as every other unknown field.
type contentBlocks []contentBlock

func (c *contentBlocks) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		*c = contentBlocks{{Type: "text", Text: str}}
		return nil
	}
	var arr []contentBlock
	if err := json.Unmarshal(data, &arr); err != nil {
		return err
	}
	*c = arr
	return nil
}

// envelopeLine is the minimal, version-tolerant view of one transcript line.
type envelopeLine struct {
	Type        string `json:"type"`
	SessionID   string `json:"sessionId"`
	SessionID2  string `json:"session_id"`
	Timestamp   string `json:"timestamp"`
	Cwd         string `json:"cwd"`
	GitBranch   string `json:"gitBranch"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		Role    string        `json:"role"`
		Content contentBlocks `json:"content"`
	} `json:"message"`
}

// ParseSession streams a transcript reader and returns its summary. It never
// loads the whole file into memory and tolerates unknown/oversized lines.
func ParseSession(r io.Reader) (Session, error) {
	var s Session
	seenTool := map[string]bool{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxSessionLineBytes)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev envelopeLine
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue // not JSON we understand: skip
		}
		if ev.IsSidechain {
			continue // sub-agent transcript: not the main session handoff
		}
		if s.SessionID == "" {
			s.SessionID = firstOf(ev.SessionID, ev.SessionID2)
		}
		if ev.Cwd != "" {
			s.Cwd = ev.Cwd
		}
		if ev.GitBranch != "" {
			s.GitBranch = ev.GitBranch
		}
		if ev.Timestamp != "" && ev.Timestamp > s.LastTimestamp { // lexicographic: ok for RFC3339 same-zone
			s.LastTimestamp = ev.Timestamp
		}
		switch ev.Type {
		case "assistant":
			s.MessageCount++
			for _, b := range ev.Message.Content {
				switch b.Type {
				case "text":
					if strings.TrimSpace(b.Text) != "" {
						s.LastAssistantText = truncateRunes(b.Text, maxRetainedRunes)
					}
				case "tool_use":
					if b.Name != "" && !seenTool[b.Name] {
						seenTool[b.Name] = true
						s.ToolsUsed = append(s.ToolsUsed, b.Name)
					}
				}
			}
		case "user":
			// Only plain user turns count toward the handoff; tool_result-only
			// lines (content has tool_result blocks, no text) are skipped.
			hasText := false
			for _, b := range ev.Message.Content {
				if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
					hasText = true
					if s.FirstUserPrompt == "" {
						s.FirstUserPrompt = truncateRunes(b.Text, maxRetainedRunes)
					}
				}
			}
			if hasText {
				s.MessageCount++
			}
		}
	}
	if err := sc.Err(); err != nil && s.SessionID == "" && s.LastAssistantText == "" {
		return Session{}, err
	}
	return s, nil
}

// HasContent reports whether the parse yielded anything a handoff could use.
func (s Session) HasContent() bool {
	return strings.TrimSpace(s.LastAssistantText) != "" || s.FirstUserPrompt != ""
}

// LatestSessionFile returns the newest *.jsonl in a project dir (by mtime),
// skipping the non-transcript files Claude also drops there. Returns "" when
// none exist. It does not open or parse the file.
func LatestSessionFile(projectDir string) (string, error) {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return "", err
	}
	type cand struct {
		path string
		mod  time.Time
	}
	var cands []cand
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(projectDir, e.Name())
		fi, err := e.Info()
		if err != nil {
			continue
		}
		cands = append(cands, cand{path, fi.ModTime()})
	}
	if len(cands) == 0 {
		return "", nil
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mod.After(cands[j].mod) })
	return cands[0].path, nil
}

// LatestSessionForRootPath finds the newest Claude Code transcript for a
// project root path and parses it. Claude encodes a project dir as the path
// with "/" replaced by "-" (e.g. /Users/me/Proj -> -Users-me-Proj) under
// ~/.claude/projects. ok=false (no error) when the project has no session yet.
func LatestSessionForRootPath(rootPath string) (Session, bool, error) {
	if strings.TrimSpace(rootPath) == "" {
		return Session{}, false, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Session{}, false, nil
	}
	slug := strings.ReplaceAll(rootPath, "/", "-")
	dir := filepath.Join(home, ".claude", "projects", slug)
	latest, err := LatestSessionFile(dir)
	if err != nil || latest == "" {
		return Session{}, false, nil // no session dir/file: nothing to capture
	}
	f, err := os.Open(latest)
	if err != nil {
		return Session{}, false, err
	}
	defer f.Close()
	s, err := ParseSession(f)
	if err != nil {
		return Session{}, false, err
	}
	if !s.HasContent() {
		return Session{}, false, nil
	}
	return s, true, nil
}

func firstOf(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncateRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(r[:max-1]) + "…" // total length == max runes, incl. the ellipsis
}
