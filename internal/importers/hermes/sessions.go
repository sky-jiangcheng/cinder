// Hermes session import (ADR-0011 决策 3/5, unblocked 2026-10-13).
//
// Hermes keeps its conversation history in ONE SQLite database, the SAME file
// TencentDB Agent Memory's asset importer reads:
//
//	$HERMES_HOME/state.db
//	  messages(session_id, role, content, tool_call_id, tool_calls, tool_name,
//	           timestamp, ...)   one row per message, ordered by id
//	  sessions(...)              metadata, NOT read here
//
// Verified against TencentCloud/TencentDB-Agent-Memory (agents/asset-import.ts
// scanSessions, read 2026-10-13): they query exactly the columns above, and
// their `sessions/` dir holds only raw request dumps — NOT the session store —
// so state.db is the single source of session history. That is what satisfies
// the "never guess a live format" rule ADR-0011 决策 5 set for deferring this
// importer: the format is now confirmed by a shipped consumer, not guessed.
//
// The schema carries NO working directory (unlike Codex rollouts), so session
// notes cannot be attributed per-project. They attach to the project named by
// the `hermes_project` config value, exactly like the curated memories in
// hermes.go — unset or unknown -> ProjectID 0, counted as skipped rather than
// scattered across the wrong projects.
//
// Read-only, hard-scoped: this opens state.db via memsrc.OpenReadOnly (never
// writes another tool's data) and reads ONLY the messages table. state.db sits
// next to secrets (.env, mcp-tokens/) — those paths are never touched.
package hermes

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"repotory/internal/core/plugin"
	"repotory/internal/db"
	"repotory/internal/importers/memsrc"
)

const (
	// maxMessageScan bounds how many rows of one session we read while looking
	// for the first prompt and the last reply. A session with thousands of tool
	// messages is not worth walking entirely for a two-excerpt note.
	maxMessageScan = 200
	// maxSessionScan bounds the total rows read across all sessions in one run,
	// so a multi-GB store cannot make the import walk everything. This is a
	// coarse I/O bound, not a recency filter: session ids are opaque, so which
	// sessions survive the cap follows SQLite's (session_id, id) order. The
	// layout probe and the loud failure on drift are what make that safe.
	maxSessionScan = maxMessageScan * 50
	// excerptChars caps each rendered excerpt. Message text can carry a whole
	// file diff; the note is a pointer to the session, not a copy of it.
	excerptChars = 3000
	// titlePromptRunes bounds how much of the first prompt goes into the title.
	titlePromptRunes = 60
)

// stateDBPath returns the path to Hermes's state database. Honors $HERMES_HOME
// over ~/.hermes, matching memoriesDir() in hermes.go.
func stateDBPath() (string, bool) {
	if h := strings.TrimSpace(os.Getenv("HERMES_HOME")); h != "" {
		return filepath.Join(h, "state.db"), true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(home, ".hermes", "state.db"), true
}

// sessionDoc is one extracted session before it becomes an ImportDoc.
type sessionDoc struct {
	sid           string
	firstUser     string
	lastAssistant string
}

// importSessions reads state.db and returns one log note per session that
// yields usable content.
//
// Returns (nil, nil) when Hermes is simply not installed (no state.db: nothing
// to do) and a non-nil error when the file IS there but the layout is not
// recognized (drift). The two must not look the same to the caller — "I
// imported nothing" and "you have no sessions" are different outcomes.
func (i *Importer) importSessions() ([]plugin.ImportDoc, error) {
	path, ok := stateDBPath()
	if !ok {
		return nil, nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil, nil // no state database: Hermes absent, a successful no-op
	}

	st, err := memsrc.OpenReadOnly(path)
	if err != nil {
		return nil, fmt.Errorf("hermes importer: open %s: %w", path, err)
	}
	defer st.Close()

	if err := probeSessionsLayout(st); err != nil {
		// Loud on purpose: see the package comment.
		return nil, fmt.Errorf("hermes importer: %w", err)
	}

	sessions, err := readSessions(st)
	if err != nil {
		return nil, fmt.Errorf("hermes importer: read %s: %w", path, err)
	}
	if len(sessions) == 0 {
		return nil, nil
	}

	pid := memsrc.TargetProject(i.db, ProjectConfigKey)
	var docs []plugin.ImportDoc
	for _, s := range sessions {
		docs = append(docs, plugin.ImportDoc{
			ProjectID: pid,
			Title:     sessionTitle(s.sid, s.firstUser),
			Content:   memsrc.ClipToBytes(renderSessionNote(s), db.MaxNoteContentLen),
			Kind:      "log",
			Tags:      "hermes",
			Source:    SourceName,
		})
	}
	if pid == 0 {
		log.Printf("hermes importer: %d session(s) read, but %q is unset/unknown — notes counted as skipped",
			len(sessions), ProjectConfigKey)
	}
	return docs, nil
}

// readSessions pulls each session's first user prompt and last assistant reply
// out of the messages table, in one bounded pass.
func readSessions(st *sql.DB) ([]sessionDoc, error) {
	rows, err := st.Query(
		`SELECT session_id, role, COALESCE(content, '')
		   FROM messages
		  ORDER BY session_id, id
		  LIMIT ?`, maxSessionScan)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type acc struct {
		first, last string
		rows        int
	}
	bySession := map[string]*acc{}
	var order []string // first appearance = session id order
	for rows.Next() {
		var (
			sid  string
			role string
			text string
		)
		if err := rows.Scan(&sid, &role, &text); err != nil {
			return nil, err
		}
		text = strings.TrimSpace(text)
		a := bySession[sid]
		if a == nil {
			a = &acc{}
			bySession[sid] = a
			order = append(order, sid)
		}
		if a.rows >= maxMessageScan {
			continue // cap reached for this session; keep counting nothing
		}
		a.rows++
		switch role {
		case "user":
			if a.first == "" && text != "" {
				a.first = text
			}
		case "assistant":
			if text != "" {
				a.last = text // keep walking: the last one wins
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Sessions with only one side are still worth a note (an interrupted
	// question is real history), so we need at least one of the two.
	var out []sessionDoc
	for _, sid := range order {
		a := bySession[sid]
		if a.first == "" && a.last == "" {
			continue
		}
		out = append(out, sessionDoc{sid: sid, firstUser: a.first, lastAssistant: a.last})
	}
	return out, nil
}

// requiredCols are the columns this importer reads. Anything missing means
// Hermes's layout moved under us, which must be reported instead of silently
// importing zero notes.
var requiredCols = []string{"session_id", "role", "content"}

func probeSessionsLayout(st *sql.DB) error {
	var n int
	if err := st.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name = 'messages'").Scan(&n); err != nil {
		return fmt.Errorf("probe messages: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("unrecognized Hermes state layout: table \"messages\" is missing (best-effort source refusing to guess; nothing was imported)")
	}
	have := map[string]bool{}
	rows, err := st.Query("PRAGMA table_info(messages)")
	if err != nil {
		return fmt.Errorf("probe messages columns: %w", err)
	}
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close() //nolint:errcheck // probe already failing, error reported below
			break
		}
		have[name] = true
	}
	rows.Close() //nolint:errcheck // probe result already captured in `have`
	for _, c := range requiredCols {
		if !have[c] {
			return fmt.Errorf("unrecognized Hermes state layout: messages.%s is missing (best-effort source refusing to guess; nothing was imported)", c)
		}
	}
	return nil
}

// sessionTitle keeps a stable, unique title: the runtime upserts on
// (project, source, title), so the session id tail is what prevents two
// sessions from overwriting each other.
func sessionTitle(sid, firstUser string) string {
	snip := ""
	if firstUser != "" {
		snip = truncateRunes(strings.SplitN(firstUser, "\n", 2)[0], titlePromptRunes)
	}
	id8 := shortID(sid)
	switch {
	case snip != "":
		return snip + " · " + id8
	case id8 != "":
		return "Hermes 会话 · " + id8
	default:
		return "Hermes 会话"
	}
}

// renderSessionNote composes the compact markdown body: provenance, session id,
// first prompt, last reply, and a footer pointing at the session row.
func renderSessionNote(s sessionDoc) string {
	var b strings.Builder
	b.WriteString("> 由 Cinder 从 Hermes 会话记录自动导入（source: hermes，**best-effort**：只取首条提问与末条回复，不是完整转录）\n\n")
	b.WriteString("**会话**：`" + s.sid + "`\n\n")
	if s.firstUser != "" {
		b.WriteString("## 首条提问\n\n")
		b.WriteString(truncateRunes(s.firstUser, excerptChars))
		b.WriteString("\n\n")
	}
	if s.lastAssistant != "" {
		b.WriteString("## 末条回复\n\n")
		b.WriteString(truncateRunes(s.lastAssistant, excerptChars))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// shortID takes the tail of an id: Hermes session ids are long opaque strings,
// and the tail is what distinguishes two sessions in a title.
func shortID(id string) string {
	if len(id) > 8 {
		return id[len(id)-8:]
	}
	if id == "" {
		return "unknown"
	}
	return id
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}
