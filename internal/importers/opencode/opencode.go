// Package opencode implements the built-in OpenCode session KnowledgeImporter
// (ADR-0011, source #3 after claude and codex). OpenCode stores structured,
// per-session JSON under its XDG data dir:
//
//	<dataroot>/storage/session/<projectHash>/ses_<id>.json   {id,projectID,directory,title,slug,summary,time}
//	<dataroot>/storage/project/<projectID>.json              {id,worktree,...}
//
// Unlike codex (raw rollouts) this source is already summarized: each session
// file carries a title + a one-line summary + the project directory, so we can
// emit one compact note per session directly. The session's `directory` last
// path segment drives project matching (memsrc).
//
// Registered as an OPT-IN manual source (like codex): session history is
// sensitive, so it is excluded from the startup auto-import and only runs when
// triggered explicitly. See ADR-0011 决策 4.
package opencode

import (
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"repotory/internal/core/plugin"
	"repotory/internal/db"
	"repotory/internal/importers/memsrc"
)

// SourceName is the stable knowledge-source identifier for OpenCode sessions.
const SourceName = "opencode"

// maxSessionFileBytes bounds one session JSON read; session files are small
// metadata blobs, so anything larger is corrupt or unexpected.
const maxSessionFileBytes = db.MaxNoteContentLen + 1

// Importer implements plugin.KnowledgeImporter for OpenCode sessions.
type Importer struct {
	db *sql.DB

	// rejections collects the session files this importer refused to produce a
	// document from, with the reason. It is what makes a format change
	// upstream visible instead of silent — see plugin.RejectionReporter.
	mu         sync.Mutex
	rejections []plugin.Rejection
}

// reject records one unusable file. Bounded so a directory of thousands of
// broken files cannot grow the importer without limit.
func (i *Importer) reject(path, reason string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if len(i.rejections) >= maxRejections {
		return
	}
	i.rejections = append(i.rejections, plugin.Rejection{Path: path, Reason: reason})
}

// maxRejections bounds the collected rejection list.
const maxRejections = 200

// Rejections implements plugin.RejectionReporter: the session files this
// import could not read.
func (i *Importer) Rejections() []plugin.Rejection {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]plugin.Rejection, len(i.rejections))
	copy(out, i.rejections)
	return out
}

// New creates an OpenCode importer bound to the application database.
func New(database *sql.DB) *Importer {
	return &Importer{db: database}
}

// Source returns the stable source identifier "opencode".
func (i *Importer) Source() string { return SourceName }

// Probe reports whether OpenCode's session storage exists on this machine:
// <dataroot>/storage/session is a directory. Read-only stat only; the content
// walk belongs to Import.
func (i *Importer) Probe() plugin.ProbeResult {
	root, ok := sessionRoot()
	if !ok {
		return plugin.ProbeResult{Detail: "无法解析 OpenCode 数据目录"}
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return plugin.ProbeResult{Detail: "未发现 OpenCode 会话存储（" + root + " 不存在）"}
	}
	return plugin.ProbeResult{Detected: true, Detail: "发现 OpenCode 会话存储：" + root}
}

// sessionFile is the subset of OpenCode's session JSON we rely on. Unknown
// fields are ignored so schema drift in unrelated fields does not break import.
type sessionFile struct {
	ID        string       `json:"id"`
	ProjectID string       `json:"projectID"`
	Directory string       `json:"directory"`
	Title     string       `json:"title"`
	Slug      string       `json:"slug"`
	Summary   summaryField `json:"summary"`
	Time      struct {
		Created int64 `json:"created"`
		Updated int64 `json:"updated"`
	} `json:"time"`
}

// summaryField tolerates summary's two observed shapes: a one-line string in
// older exports and an object ({"additions":..,"deletions":..,"files":..}) in
// current ones. A strict string field made json.Unmarshal reject every real
// session file, so the whole source silently imported 0 notes (rehearsal
// finding, 2026-10-18). Object/null/anything-not-a-string decodes to empty;
// the title/slug fallback chain still yields a usable note.
type summaryField string

func (f *summaryField) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = summaryField(s)
		return nil
	}
	*f = ""
	return nil
}

// Import walks the OpenCode session storage and returns one note per session.
func (i *Importer) Import() ([]plugin.ImportDoc, error) {
	// Each run reports its own rejections; a stale list from the previous run
	// would claim failures that have since been fixed.
	i.mu.Lock()
	i.rejections = nil
	i.mu.Unlock()

	root, ok := sessionRoot()
	if !ok {
		return nil, nil // no OpenCode data dir: successful no-op
	}
	projects, _ := db.GetAllProjects(i.db)
	repos, _ := db.GetAllRepositories(i.db)

	var docs []plugin.ImportDoc
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		if doc, ok := i.docFor(path, projects, repos); ok {
			docs = append(docs, doc)
		}
		return nil
	})
	return docs, nil
}

// docFor reads and shapes one session file into an ImportDoc.
func (i *Importer) docFor(path string, projects []db.Project, repos []db.Repository) (plugin.ImportDoc, bool) {
	raw, err := memsrc.ReadCapped(path, maxSessionFileBytes)
	if err != nil {
		log.Printf("opencode importer: skipping %s: %v", path, err)
		i.reject(path, "读取失败: "+err.Error())
		return plugin.ImportDoc{}, false
	}
	var s sessionFile
	if err := json.Unmarshal(raw, &s); err != nil {
		// The 2026-10-18 incident: OpenCode changed a field's type upstream and
		// EVERY session file stopped unmarshalling. The import then reported
		// zero created notes, with no error anywhere — indistinguishable from
		// "nothing new to import". This rejection is what turns that into a
		// visible "12 files could not be read".
		i.reject(path, "无法解析为 OpenCode 会话文件: "+err.Error())
		return plugin.ImportDoc{}, false // not a session file we recognize
	}
	title := strings.TrimSpace(s.Title)
	if title == "" {
		title = strings.TrimSpace(s.Slug)
	}
	summary := strings.TrimSpace(string(s.Summary))
	if title == "" && summary == "" {
		i.reject(path, "会话文件没有可用的标题或摘要")
		return plugin.ImportDoc{}, false // nothing usable
	}

	pid := memsrc.MatchProject(memsrc.LastPathSegment(s.Directory), projects, repos)
	content := renderNote(s, title, summary)
	content = memsrc.ClipToBytes(content, db.MaxNoteContentLen)
	return plugin.ImportDoc{
		ProjectID: pid,
		RepoPath:  s.Directory, // when pid==0 the runtime lands the note on a placeholder named for this repo (ADR-0023)
		Title:     noteTitle(title, s.ID),
		Content:   content,
		Kind:      "log",
		Tags:      "opencode",
		Source:    SourceName,
	}, true
}

// sessionRoot returns <dataroot>/storage/session. XDG_DATA_HOME honored, else
// ~/.local/share. ok=false when the home dir cannot be resolved.
func sessionRoot() (string, bool) {
	base := ""
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		base = filepath.Join(xdg, "opencode")
	} else if home, err := os.UserHomeDir(); err == nil {
		base = filepath.Join(home, ".local", "share", "opencode")
	} else {
		return "", false
	}
	return filepath.Join(base, "storage", "session"), true
}

// noteTitle keeps a stable, unique title: display title plus a short session-id
// suffix so same-titled sessions do not collide under the runtime's
// (project, source, title) upsert key.
func noteTitle(title, id string) string {
	base := title
	if base == "" {
		base = "OpenCode 会话"
	}
	base = truncateRunes(firstLine(base), 60)
	tail := id
	if len(tail) > 8 {
		tail = tail[len(tail)-8:]
	}
	return base + " · " + tail
}

// renderNote composes the compact markdown body.
func renderNote(s sessionFile, title, summary string) string {
	var b strings.Builder
	b.WriteString("> 由 Cinder 从 OpenCode 会话记录自动导入（source: opencode）\n\n")
	if title != "" {
		b.WriteString("**标题**：" + title + "\n\n")
	}
	if s.Directory != "" {
		b.WriteString("**目录**：`" + s.Directory + "`\n\n")
	}
	if d := formatMillis(s.Time.Updated); d != "" {
		b.WriteString("**日期**：" + d + "\n\n")
	}
	if summary != "" {
		b.WriteString("## 摘要\n\n")
		b.WriteString(truncateRunes(summary, 4000))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func formatMillis(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).Format("2006-01-02")
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
