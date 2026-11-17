package agents

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// DeepSeek Harness (DSH) stores sessions at
// ~/.dsh/sessions/-<path-with-dashes>--/session-<id>/session.v4.jsonl.zstd
// (zstd-compressed JSONL; the slug is "-" + the dashed cwd + "--"). The
// header line (type "session") carries the session id and cwd; user turns are
// user/message events whose data.source.kind is "user" (the harness itself
// injects system reminders as user/message too — those are skipped);
// session/title carries the harness's own title. Assistant text arrives as
// chunk events inside assistant/attempt; the shape has drifted between
// versions, so any chunk carrying a non-empty "text" field counts.

type dshLine struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Cwd  string `json:"cwd"`
	Data struct {
		Title  string `json:"title"`
		Kind   string `json:"kind"`
		Role   string `json:"role"`
		Text   string `json:"text"`
		Source struct {
			Kind string `json:"kind"`
		} `json:"source"`
		Stream []struct {
			Chunk struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"chunk"`
		} `json:"stream"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"data"`
}

func parseDSH(r io.Reader) (Session, error) {
	var s Session
	s.Agent = DSH
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev dshLine
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "session":
			if s.SessionID == "" {
				s.SessionID = ev.ID
			}
			if ev.Cwd != "" {
				s.Cwd = ev.Cwd
			}
		case "session/title":
			if t := strings.TrimSpace(ev.Data.Title); t != "" {
				s.ToolsUsed = append(s.ToolsUsed, "title: "+t)
			}
		case "user/message":
			if ev.Data.Source.Kind == "user" {
				for _, b := range ev.Data.Content {
					t := strings.TrimSpace(b.Text)
					if t != "" && !strings.HasPrefix(t, "<system-reminder") && s.FirstUserPrompt == "" {
						s.FirstUserPrompt = truncateRunes(t, 6000)
					}
				}
			}
		case "assistant/attempt":
			for _, st := range ev.Data.Stream {
				if t := strings.TrimSpace(st.Chunk.Text); t != "" &&
					(st.Chunk.Type == "text" || st.Chunk.Type == "output_text" || st.Chunk.Type == "text-delta") {
					s.LastAssistantText = truncateRunes(t, 6000)
				}
			}
		}
	}
	return s, sc.Err()
}

func latestDSH(cwd string) (Session, bool, error) {
	if strings.TrimSpace(cwd) == "" {
		return Session{}, false, nil
	}
	dir, err := homeJoin(".dsh", "sessions", "--"+slugOf(cwd)+"--")
	if err != nil {
		return Session{}, false, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Session{}, false, nil
	}
	var best string
	var bestMod time.Time
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "session-") {
			continue
		}
		path := dir + "/" + e.Name() + "/session.v4.jsonl.zstd"
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		if best == "" || fi.ModTime().After(bestMod) {
			best, bestMod = path, fi.ModTime()
		}
	}
	if best == "" {
		return Session{}, false, nil
	}
	f, err := os.Open(best)
	if err != nil {
		return Session{}, false, err
	}
	defer f.Close()
	dec, err := zstd.NewReader(f)
	if err != nil {
		return Session{}, false, err
	}
	defer dec.Close()
	// *zstd.Decoder is itself an io.Reader.
	s, err := parseDSH(dec)
	if err != nil {
		return Session{}, false, err
	}
	if !s.HasContent() {
		return Session{}, false, nil
	}
	s.ModTime = bestMod
	return s, true, nil
}
