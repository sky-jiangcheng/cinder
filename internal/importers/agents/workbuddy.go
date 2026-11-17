package agents

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
)

// WorkBuddy stores raw session transcripts at
// ~/.workbuddy/projects/<slug>/*.jsonl, where slug is the cwd with "/" -> "-"
// — the same convention as Claude Code. Lines carry a CodeBuddy-family
// envelope: session-meta lines name the session, message lines carry
// role + content blocks typed input_text (user) and output_text (assistant).
// Timestamps are epoch milliseconds; the dir slug is the cwd source of truth
// because message lines do not repeat the cwd.

type workbuddyLine struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Role      string `json:"role"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func parseWorkBuddy(r io.Reader) (Session, error) {
	var s Session
	s.Agent = WorkBuddy
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev workbuddyLine
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "session-meta":
			if s.SessionID == "" {
				s.SessionID = ev.SessionID
			}
		case "message":
			s.MessageCount++
			for _, b := range ev.Content {
				switch b.Type {
				case "input_text":
					if t := strings.TrimSpace(b.Text); t != "" && s.FirstUserPrompt == "" &&
						!strings.HasPrefix(t, "<system-reminder") {
						s.FirstUserPrompt = truncateRunes(t, 6000)
					}
				case "output_text":
					if t := strings.TrimSpace(b.Text); t != "" {
						s.LastAssistantText = truncateRunes(t, 6000)
					}
				}
			}
		}
	}
	return s, sc.Err()
}

func latestWorkBuddy(cwd string) (Session, bool, error) {
	if strings.TrimSpace(cwd) == "" {
		return Session{}, false, nil
	}
	dir, err := homeJoin(".workbuddy", "projects", slugOf(cwd))
	if err != nil {
		return Session{}, false, nil
	}
	latest, mod, err := newestFile(dir + "/*.jsonl")
	if err != nil || latest == "" {
		return Session{}, false, err
	}
	f, err := os.Open(latest)
	if err != nil {
		return Session{}, false, err
	}
	defer f.Close()
	s, err := parseWorkBuddy(f)
	if err != nil {
		return Session{}, false, err
	}
	if !s.HasContent() {
		return Session{}, false, nil
	}
	s.Cwd = cwd
	s.ModTime = mod
	return s, true, nil
}
