// Package openclaw implements the built-in OpenClaw agent-memory
// KnowledgeImporter (ADR-0011). OpenClaw stores its agent-global memory as
// Markdown under the workspace dir:
//
//	~/.openclaw-autoclaw/workspace/{IDENTITY,SOUL,USER,AGENTS,HEARTBEAT,TOOLS}.md
//
// SECURITY: the parent `~/.openclaw-autoclaw/` also contains private keys
// (office-plugin-tls/*.pem), a vault (vault-roots.json), device identity and
// signed client tokens. This importer is therefore hard-scoped to the
// workspace/*.md files ONLY — a flat, non-recursive listing of that one
// directory — and never reads the parent, subdirectories, or any non-.md file.
//
// These files are agent-GLOBAL memory (persona/user profile), not per-project,
// so unlike codex/opencode there is no per-session cwd to match. Notes attach
// to a project named by the `openclaw_project` config value (project name or
// id); unset/unknown -> skipped (0), so a misconfiguration cannot scatter
// agent-persona text into random projects. Registered as an OPT-IN manual
// source: excluded from startup auto-import, triggered explicitly (ADR-0011 决策 4).
package openclaw

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

// SourceName is the stable knowledge-source identifier for OpenClaw memory.
const SourceName = "openclaw"

// ProjectConfigKey names the config value (project name or numeric id) that
// OpenClaw's global memory notes attach to.
const ProjectConfigKey = "openclaw_project"

// maxReadBytes bounds one memory file; these are small curated markdown docs.
const maxReadBytes = db.MaxNoteContentLen + 1

// Importer implements plugin.KnowledgeImporter for OpenClaw workspace memory.
type Importer struct {
	db *sql.DB
}

// New creates an OpenClaw importer bound to the application database.
func New(database *sql.DB) *Importer {
	return &Importer{db: database}
}

// Source returns the stable source identifier "openclaw".
func (i *Importer) Source() string { return SourceName }

// Probe reports whether OpenClaw's agent-global memory exists on this machine:
// at least one .md file in ~/.openclaw-autoclaw/workspace. Flat listing of
// that ONE allowlisted directory only — never the parent (keys, vault, tokens).
func (i *Importer) Probe() plugin.ProbeResult {
	home, err := os.UserHomeDir()
	if err != nil {
		return plugin.ProbeResult{Detail: "无法解析用户主目录（OpenClaw 记忆探活）"}
	}
	dir := filepath.Join(home, ".openclaw-autoclaw", "workspace")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return plugin.ProbeResult{Detail: "未发现 OpenClaw 记忆（~/.openclaw-autoclaw/workspace 不存在）"}
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			n++
		}
	}
	if n == 0 {
		return plugin.ProbeResult{Detail: "未发现 OpenClaw 记忆文件（workspace/ 下没有 *.md）"}
	}
	return plugin.ProbeResult{Detected: true, Detail: fmt.Sprintf("~/.openclaw-autoclaw/workspace：%d 个记忆文件", n)}
}

// Import reads ONLY ~/.openclaw-autoclaw/workspace/*.md (flat) and yields one
// note per non-empty file. Returns nil (no-op) when OpenClaw is not installed.
func (i *Importer) Import() ([]plugin.ImportDoc, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil
	}
	dir := filepath.Join(home, ".openclaw-autoclaw", "workspace")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil // no OpenClaw workspace: successful no-op
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
		content := "> 由 Cinder 从 OpenClaw 全局记忆导入（source: openclaw，allowlist: workspace/*.md）\n\n" + body
		content = memsrc.ClipToBytes(content, db.MaxNoteContentLen)
		docs = append(docs, plugin.ImportDoc{
			ProjectID: pid,
			Title:     "OpenClaw · " + name,
			Content:   content,
			Kind:      "knowledge",
			Tags:      "openclaw",
			Source:    SourceName,
		})
	}
	return docs, nil
}
