// Package codex implements the built-in Codex CLI session KnowledgeImporter
// (ADR-0011, source #2 after claude). Codex has no curated memory files the way
// Claude does (its memories/ dir and global AGENTS.md are typically empty;
// rules/default.rules is a sandbox allowlist, not knowledge). The one rich,
// per-project artifact it writes is the session rollout transcript at
//
//	~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl
//	~/.codex/archived_sessions/rollout-<ts>-<uuid>.jsonl
//
// This importer turns each session into one compact "log" note — the project's
// working directory (matched to a Cinder project via memsrc), the first user
// instruction and the last assistant reply — and hands it to the plugin runtime
// which upserts it idempotently by (project, source="codex", title).
//
// It STREAMS each file (never loads a full transcript into memory) and keeps
// only three small fields, so a multi-hundred-MB session cannot OOM the import.
// Parsing is deliberately lenient: Codex's rollout schema is not a public
// contract and changes across versions, so unknown/oversized lines are skipped
// rather than failing the whole scan (ADR-0011 决策 5).
package codex

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"repotory/internal/core/plugin"
	"repotory/internal/db"
	"repotory/internal/importers/memsrc"
)

// SourceName is the stable knowledge-source identifier for Codex sessions.
const SourceName = "codex"

const (
	// maxRolloutBytes skips an individual session file larger than this. Real
	// sessions stay well under it; it bounds the I/O of a single pathological
	// transcript. Oversized files are skipped with a log line, not an error.
	maxRolloutBytes = 32 << 20 // 32 MiB
	// maxLineBytes bounds one JSONL line; a single rollout line is one event.
	maxLineBytes = 8 << 20 // 8 MiB
	// maxFieldRunes bounds the retained prompt/reply text inside the note.
	maxFieldRunes = 4000
	// titlePromptRunes bounds how much of the first prompt goes into the title.
	titlePromptRunes = 60
)

// Importer implements plugin.KnowledgeImporter for Codex session rollouts.
type Importer struct {
	db *sql.DB
}

// New creates a Codex importer bound to the application database.
func New(database *sql.DB) *Importer {
	return &Importer{db: database}
}

// Source returns the stable source identifier "codex".
func (i *Importer) Source() string { return SourceName }

// Probe reports whether Codex session storage exists on this machine: at least
// one of ~/.codex/sessions or ~/.codex/archived_sessions is a directory.
// Read-only stat only; the content walk belongs to Import.
func (i *Importer) Probe() plugin.ProbeResult {
	home, err := os.UserHomeDir()
	if err != nil {
		return plugin.ProbeResult{Detail: "无法解析用户主目录（Codex 会话探活）"}
	}
	detected := 0
	for _, root := range sessionRoots(home) {
		if st, err := os.Stat(root); err == nil && st.IsDir() {
			detected++
		}
	}
	if detected == 0 {
		return plugin.ProbeResult{Detail: "未发现 Codex 会话记录（~/.codex/sessions 与 archived_sessions 均不存在）"}
	}
	return plugin.ProbeResult{Detected: true, Detail: fmt.Sprintf("~/.codex：发现 %d 个会话存储目录", detected)}
}

// parsed is the minimal state extracted from one rollout while streaming.
type parsed struct {
	cwd           string
	sessionID     string
	date          string // YYYY-MM-DD, from the meta timestamp
	firstUser     string
	lastAssistant string
	// firstUserRaw is the FIRST user message as it appeared in the file,
	// before envelope stripping. It exists only to compute the legacy title
	// (titleFor's pre-2.39 formula kept the raw first line, so notes imported
	// before then carry "<environment_context> · <id>" as their title and the
	// runtime must find and rename those rows, not duplicate them).
	firstUserRaw string
}

// Import walks the Codex session directories and returns one note per session
// that yields usable content. Sessions whose project cannot be matched still
// return a doc with ProjectID 0 (the runtime counts them as skipped), so the
// caller sees them in the import statistics rather than silently vanishing.
func (i *Importer) Import() ([]plugin.ImportDoc, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil // cannot resolve home: no-op, not a failure
	}

	projects, _ := db.GetAllProjects(i.db)
	repos, _ := db.GetAllRepositories(i.db)

	var docs []plugin.ImportDoc
	for _, root := range sessionRoots(home) {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return nil // unreadable dir/file: keep walking
			}
			if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
				return nil
			}
			doc, ok := i.docFor(path, projects, repos)
			if ok {
				docs = append(docs, doc)
			}
			return nil
		})
	}
	return docs, nil
}

// docFor parses one rollout and builds its ImportDoc. Returns ok=false when the
// file is too large, unparseable, or has no retained message content.
func (i *Importer) docFor(path string, projects []db.Project, repos []db.Repository) (plugin.ImportDoc, bool) {
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxRolloutBytes {
		log.Printf("codex importer: skipping %s: %d bytes exceeds the %d-byte session cap", path, fi.Size(), maxRolloutBytes)
		return plugin.ImportDoc{}, false
	}
	p, err := parseRolloutFile(path)
	if err != nil {
		log.Printf("codex importer: skipping %s: %v", path, err)
		return plugin.ImportDoc{}, false
	}
	if p.firstUser == "" && p.lastAssistant == "" {
		return plugin.ImportDoc{}, false // nothing usable in this transcript
	}

	display := memsrc.LastPathSegment(p.cwd)
	pid := memsrc.MatchProject(display, projects, repos) // 0 when unmatched -> skipped

	content := renderNote(p)
	content = memsrc.ClipToBytes(content, db.MaxNoteContentLen)
	return plugin.ImportDoc{
		ProjectID: pid,
		RepoPath:  p.cwd, // when pid==0 the runtime lands the note on a placeholder named for this repo (ADR-0023)
		Title:     titleFor(p),
		Content:   content,
		Kind:      "log",
		Tags:      "codex",
		Source:    SourceName,
		// The old formula's title for the SAME session file. The 2.39 title
		// change (timestamp head → random tail, and the envelope-aware
		// snippet) would otherwise re-import every existing codex note as a
		// NEW row next to the old one: upsert keys on (project, source,
		// title). The runtime renames the legacy row in place when it finds
		// one, so history, versions and idempotency survive the formula move.
		LegacyTitles: legacyCandidates(p),
	}, true
}

// legacyCandidates lists the titles the previous formula could have written
// for this session: the raw-first-line variant, and the envelope-stripped
// variant of the same (the 2026-10-21 smoke fix changed which text the title
// was built from, so libraries touched by both revisions carry both shapes).
func legacyCandidates(p parsed) []string {
	out := make([]string, 0, 2)
	seen := map[string]bool{}
	add := func(t string) {
		if t == "" || seen[t] {
			return
		}
		seen[t] = true
		out = append(out, t)
	}
	add(legacyTitleFor(p))
	add(legacyTitleFor(parsed{sessionID: p.sessionID, firstUserRaw: p.firstUser}))
	return out
}

// sessionRoots lists the directories to scan for rollout files.
func sessionRoots(home string) []string {
	codex := filepath.Join(home, ".codex")
	return []string{
		filepath.Join(codex, "sessions"),
		filepath.Join(codex, "archived_sessions"),
	}
}

// parseRolloutFile streams a rollout, extracting cwd + session id/date from the
// session_meta line, and the first user / last assistant message texts.
func parseRolloutFile(path string) (parsed, error) {
	f, err := os.Open(path)
	if err != nil {
		return parsed{}, err
	}
	defer f.Close()

	var p parsed
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev rolloutEvent
		if json.Unmarshal(line, &ev) != nil {
			continue // not JSON we understand; lenient skip
		}
		switch {
		case ev.Type == "session_meta":
			if p.cwd == "" {
				p.cwd = ev.Payload.Cwd
			}
			if p.sessionID == "" {
				p.sessionID = firstNonEmpty(ev.Payload.SessionID, ev.Payload.ID)
			}
			if len(ev.Payload.Timestamp) >= 10 {
				p.date = ev.Payload.Timestamp[:10]
			}
		case ev.Payload.Type == "message":
			txt := extractText(ev.Payload.Content)
			switch ev.Payload.Role {
			case "user":
				// Keep the first user message that still has real prompt text
				// after the injected envelopes are stripped (see
				// stripBoilerplate). Codex opens most sessions with
				// <environment_context> and AGENTS.md blocks; taking the first
				// message verbatim put "<environment_context> · 019f7f28" as the
				// note title and a full sandbox-permission XML tree as its body
				// on all 50 sampled notes (the 2026-10-21 UI smoke test).
				if p.firstUserRaw == "" {
					p.firstUserRaw = txt
				}
				if p.firstUser == "" {
					if txt = stripBoilerplate(txt); txt != "" {
						p.firstUser = txt
					}
				}
			case "assistant":
				if txt != "" {
					p.lastAssistant = txt
				}
			}
		}
	}
	// A Scanner error (e.g. ErrTooLong) is non-fatal here: we return whatever
	// we managed to retain. Only a hard read failure that yielded nothing
	// surfaces as an error.
	if err := sc.Err(); err != nil && p.cwd == "" && p.firstUser == "" && p.lastAssistant == "" {
		return parsed{}, err
	}
	return p, nil
}

// agentsMdHeadingRe matches the markdown heading Codex puts at the top of the
// first user message when a project AGENTS.md exists, e.g.
// "# AGENTS.md instructions for /Users/…/investment-dashboard".
var agentsMdHeadingRe = regexp.MustCompile(`(?m)^#\s*AGENTS\.md instructions[^\n]*\n?`)

// leadingTagOpenRe matches an opening <tag> at the string start. Codex chains
// the same boilerplate envelopes CodeBuddy does — <environment_context> always,
// plus <INSTRUCTIONS> and <user_instructions> when applicable — ahead of the
// real prompt. RE2 has no backreferences, so the closing tag is located by name
// in code.
var leadingTagOpenRe = regexp.MustCompile(`\A\s*<([a-zA-Z_]+)>`)

// stripBoilerplate removes the injected envelope chain from a user message and
// returns what is left, empty when the message carried nothing else. An
// unclosed envelope is left alone: stripping half of it would fabricate text.
func stripBoilerplate(s string) string {
	for {
		if loc := agentsMdHeadingRe.FindStringIndex(s); loc != nil && loc[0] == 0 {
			s = s[loc[1]:]
			continue
		}
		m := leadingTagOpenRe.FindStringSubmatch(s)
		if m == nil {
			break
		}
		closeTag := "</" + m[1] + ">"
		i := strings.Index(s, closeTag)
		if i < 0 {
			break
		}
		s = s[i+len(closeTag):]
	}
	return strings.TrimSpace(s)
}

// rolloutEvent is the minimal, version-tolerant view of one JSONL line.
type rolloutEvent struct {
	Type    string         `json:"type"`
	Payload rolloutPayload `json:"payload"`
}

type rolloutPayload struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	Cwd       string          `json:"cwd"`
	SessionID string          `json:"session_id"`
	ID        string          `json:"id"`
	Timestamp string          `json:"timestamp"`
	Content   json.RawMessage `json:"content"`
}

// extractText pulls plain text out of a message content field, which may be a
// bare string or an array of {type,text} parts (input_text / output_text /
// text). Parts are joined by newlines; non-text parts are ignored.
func extractText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, part := range parts {
			if strings.TrimSpace(part.Text) != "" {
				b.WriteString(part.Text)
				b.WriteByte('\n')
			}
		}
		return strings.TrimSpace(b.String())
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	return ""
}

// titleFor builds a stable, collision-resistant note title: a first-prompt
// snippet plus the random tail of the session id. Deterministic across
// re-imports of the same file, so upsertDoc updates rather than duplicates.
//
// The tail, not the head: Codex session ids are UUIDv7, whose first 8 hex
// characters are the millisecond timestamp. Two sessions started in the same
// ~50-day window shared that prefix, and Codex opens most sessions with the
// same injected envelope — so the old formula (first line of the first
// message + the id head) produced the SAME title for unrelated sessions
// ("<environment_context> · 019f7f28" ×2 on the live library, 2026-11-16).
// The third UUID segment is random per session; it disambiguates.
func titleFor(p parsed) string {
	snip := ""
	if p.firstUser != "" {
		firstLine := strings.TrimSpace(strings.SplitN(p.firstUser, "\n", 2)[0])
		snip = truncateRunes(firstLine, titlePromptRunes)
	}
	suffix := sessionIDSuffix(p.sessionID)
	switch {
	case snip != "" && suffix != "":
		return snip + " · " + suffix
	case snip != "":
		return snip
	case suffix != "":
		return "Codex 会话 · " + suffix
	default:
		return "Codex 会话"
	}
}

// legacyTitleFor reproduces the pre-2.39 title formula for one session, so
// the runtime can find the rows the old formula wrote and rename them in
// place instead of inserting duplicates next to them.
func legacyTitleFor(p parsed) string {
	snip := ""
	if p.firstUserRaw != "" {
		firstLine := strings.TrimSpace(strings.SplitN(p.firstUserRaw, "\n", 2)[0])
		snip = truncateRunes(firstLine, titlePromptRunes)
	}
	id8 := p.sessionID
	if len(id8) > 8 {
		id8 = id8[:8]
	}
	switch {
	case snip != "" && id8 != "":
		return snip + " · " + id8
	case snip != "":
		return snip
	case id8 != "":
		return "Codex 会话 · " + id8
	default:
		return "Codex 会话"
	}
}

// sessionIDSuffix returns the part of a rollout session id that varies per
// session. A full UUIDv7 looks like 019f7f28-1121-7451-8596-496babdbe598:
// segment 0 is the timestamp (collides across nearby sessions), segment 2 is
// random. Short/partial ids seen in fixtures and old files fall back to the
// last segment, then to the whole string.
func sessionIDSuffix(id string) string {
	if id == "" {
		return ""
	}
	segs := strings.Split(id, "-")
	switch {
	case len(segs) >= 3:
		return segs[2]
	case len(segs) == 2:
		return segs[1]
	default:
		return segs[0]
	}
}

// renderNote composes the compact markdown body: cwd, first instruction, last
// assistant reply, and a provenance footer.
func renderNote(p parsed) string {
	var b strings.Builder
	b.WriteString("> 由 Cinder 从 Codex 会话记录自动导入（source: codex）\n\n")
	if p.cwd != "" {
		b.WriteString("**工作目录**：`" + p.cwd + "`\n\n")
	}
	if p.date != "" {
		b.WriteString("**日期**：" + p.date + "\n\n")
	}
	if p.firstUser != "" {
		b.WriteString("## 首次指令\n\n")
		b.WriteString(truncateRunes(p.firstUser, maxFieldRunes))
		b.WriteString("\n\n")
	}
	if p.lastAssistant != "" {
		b.WriteString("## 最近回复\n\n")
		b.WriteString(truncateRunes(p.lastAssistant, maxFieldRunes))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max])) + "…"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
