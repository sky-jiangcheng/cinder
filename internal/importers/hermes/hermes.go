// Package hermes implements the built-in Hermes (Nous Research) agent-memory
// KnowledgeImporter (ADR-0011). Hermes is a product SEPARATE from OpenClaw. Its
// curated long-term memory lives as Markdown under the Hermes home dir:
//
//	~/.hermes/memories/{MEMORY.md,USER.md}      (root overridden by $HERMES_HOME)
//
// SECURITY: the Hermes home also contains `.env` (secrets), `mcp-tokens/`, and
// `state.db`. The memory half is hard-scoped to memories/*.md ONLY — a flat,
// non-recursive listing of that one subdirectory — and never reads the parent,
// subdirectories, or any non-.md file. The session half reads ONLY the
// messages table of state.db, read-only (see sessions.go).
//
// Sessions: imported since 2026-10-13, from state.db — the layout was confirmed
// against TencentCloud/TencentDB-Agent-Memory, whose shipped asset importer
// reads exactly these tables. ADR-0011 决策 3/5 had deferred this until a real
// sample or a shipped consumer settled the format; that condition is met.
//
// Hermes memory is agent-GLOBAL, so notes attach to the project named by the
// `hermes_project` config value; unset/unknown -> skipped. Registered as an
// OPT-IN manual source, excluded from startup auto-import (ADR-0011 决策 4).
package hermes

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"repotory/internal/core/plugin"
	"repotory/internal/db"
	"repotory/internal/importers/memsrc"
)

// SourceName is the stable knowledge-source identifier for Hermes memory.
const SourceName = "hermes"

// ProjectConfigKey names the config value (project name or numeric id) that
// Hermes's global memory notes attach to.
const ProjectConfigKey = "hermes_project"

const maxReadBytes = db.MaxNoteContentLen + 1

// Importer implements plugin.KnowledgeImporter for Hermes curated memory.
type Importer struct {
	db *sql.DB
}

// New creates a Hermes importer bound to the application database.
func New(database *sql.DB) *Importer {
	return &Importer{db: database}
}

// Source returns the stable source identifier "hermes".
func (i *Importer) Source() string { return SourceName }

// Probe reports whether Hermes is present on this machine: at least one
// memories/*.md or the state.db session store exists under the Hermes home.
// Read-only stat/readdir on memories/ and state.db only — never .env,
// mcp-tokens/ or anything else in the parent.
func (i *Importer) Probe() plugin.ProbeResult {
	mdir, ok := memoriesDir()
	if !ok {
		return plugin.ProbeResult{Detail: "无法解析 Hermes 主目录"}
	}
	mdocs := 0
	if entries, err := os.ReadDir(mdir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				mdocs++
			}
		}
	}
	dbPresent := false
	if path, ok := stateDBPath(); ok {
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			dbPresent = true
		}
	}
	switch {
	case mdocs > 0 && dbPresent:
		return plugin.ProbeResult{Detected: true, Detail: fmt.Sprintf("~/.hermes：%d 个记忆文件 + state.db 会话库", mdocs)}
	case mdocs > 0:
		return plugin.ProbeResult{Detected: true, Detail: fmt.Sprintf("~/.hermes/memories：%d 个记忆文件", mdocs)}
	case dbPresent:
		return plugin.ProbeResult{Detected: true, Detail: "~/.hermes/state.db：发现会话库"}
	default:
		return plugin.ProbeResult{Detail: "未发现 Hermes 记忆（~/.hermes/memories 与 state.db 均不存在）"}
	}
}

// memoriesDir resolves <hermesHome>/memories, honoring $HERMES_HOME over ~/.hermes.
func memoriesDir() (string, bool) {
	if h := strings.TrimSpace(os.Getenv("HERMES_HOME")); h != "" {
		return filepath.Join(h, "memories"), true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(home, ".hermes", "memories"), true
}

// Import yields one note per curated memories/*.md file plus one log note per
// session found in state.db (see sessions.go). Returns nil (no-op) when Hermes
// is not installed; a drifted state.db layout returns an error instead, so
// "imported nothing" never masquerades as "you have no sessions".
func (i *Importer) Import() ([]plugin.ImportDoc, error) {
	docs, err := i.importMemories()
	if err != nil {
		return nil, err
	}
	sessionDocs, err := i.importSessions()
	if err != nil {
		return nil, err
	}
	return append(docs, sessionDocs...), nil
}

// importMemories reads ONLY <hermesHome>/memories/*.md (flat) and yields one
// note per non-empty file.
func (i *Importer) importMemories() ([]plugin.ImportDoc, error) {
	dir, ok := memoriesDir()
	if !ok {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil // no Hermes memories dir: successful no-op
	}
	pid := memsrc.TargetProject(i.db, ProjectConfigKey)

	var docs []plugin.ImportDoc
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue // allowlist: top-level markdown only, no recursion
		}
		raw, err := memsrc.ReadCapped(filepath.Join(dir, e.Name()), maxReadBytes)
		if err != nil {
			continue
		}
		body := memsrc.StripFrontmatter(string(raw))
		if strings.TrimSpace(body) == "" {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		content := "> 由 Cinder 从 Hermes 全局记忆导入（source: hermes，allowlist: memories/*.md）\n\n" + body
		content = memsrc.ClipToBytes(content, db.MaxNoteContentLen)
		docs = append(docs, plugin.ImportDoc{
			ProjectID: pid,
			Title:     "Hermes · " + name,
			Content:   content,
			Kind:      "knowledge",
			Tags:      "hermes",
			Source:    SourceName,
		})
	}
	return docs, nil
}
