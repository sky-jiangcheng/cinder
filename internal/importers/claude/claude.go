// Package claude implements the built-in Claude memory KnowledgeImporter
// (issue #35). It reads notes from ~/.claude/projects/*/memory/*.md, matches
// each to a Cinder project by name or repository path, and produces
// plugin.ImportDoc values that the plugin runtime upserts into the knowledge
// base. Imports are idempotent: the runtime updates existing notes rather than
// duplicating them.
package claude

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

// SourceName is the stable knowledge-source identifier registered by this
// importer.
const SourceName = "claude"

// maxImportReadBytes caps how much of one memory file is read into memory.
// Claude's memory files are human-written Markdown notes, so anything above
// the note content bound is junk or a mistake. The cap matters on its own:
// os.ReadFile loads the whole file before any bound check could reject it, so
// a single oversized file OOM'd the import.
const maxImportReadBytes = db.MaxNoteContentLen + 1

// Importer implements plugin.KnowledgeImporter for Claude memory files.
type Importer struct {
	db *sql.DB
}

// New creates a Claude memory importer bound to the application database.
func New(database *sql.DB) *Importer {
	return &Importer{db: database}
}

// Source returns the stable source identifier "claude".
func (i *Importer) Source() string { return SourceName }

// Probe reports whether Claude's per-project memory actually exists on this
// machine: at least one memory/*.md (other than the reserved MEMORY.md) under
// ~/.claude/projects. Read-only, flat listing only — never touches anything
// outside the projects/ tree.
func (i *Importer) Probe() plugin.ProbeResult {
	home, err := os.UserHomeDir()
	if err != nil {
		return plugin.ProbeResult{Detail: "无法解析用户主目录（Claude 记忆探活）"}
	}
	claudeDir := filepath.Join(home, ".claude", "projects")
	entries, err := os.ReadDir(claudeDir)
	if err != nil {
		return plugin.ProbeResult{Detail: "未发现 Claude 项目记忆（~/.claude/projects 不存在）"}
	}
	projects, files := 0, 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		projects++
		memEntries, err := os.ReadDir(filepath.Join(claudeDir, e.Name(), "memory"))
		if err != nil {
			continue // a project dir without a memory/ subtree is not a memory source
		}
		for _, m := range memEntries {
			if m.IsDir() || !strings.HasSuffix(m.Name(), ".md") {
				continue
			}
			if strings.TrimSuffix(m.Name(), ".md") == "MEMORY" {
				continue // reserved file, never imported
			}
			files++
		}
	}
	if files == 0 {
		return plugin.ProbeResult{Detail: "未发现 Claude 记忆文件（~/.claude/projects 下没有 memory/*.md）"}
	}
	return plugin.ProbeResult{Detected: true, Detail: fmt.Sprintf("~/.claude/projects：%d 个项目目录，%d 个记忆文件", projects, files)}
}

// Import scans ~/.claude/projects/*/memory/*.md and returns documents to
// upsert. Files whose project cannot be matched to a Cinder project are
// returned with ProjectID 0, which the runtime counts as skipped.
func (i *Importer) Import() ([]plugin.ImportDoc, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot resolve home directory")
	}
	claudeDir := filepath.Join(home, ".claude", "projects")
	entries, err := os.ReadDir(claudeDir)
	if err != nil {
		// No Claude memory directory yet; a successful no-op.
		return nil, nil
	}

	projects, _ := db.GetAllProjects(i.db)
	repos, _ := db.GetAllRepositories(i.db)

	var docs []plugin.ImportDoc
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		memDir := filepath.Join(claudeDir, e.Name(), "memory")
		memEntries, err := os.ReadDir(memDir)
		if err != nil {
			continue
		}

		displayName := DisplayName(e.Name())
		if len(displayName) < 2 {
			continue
		}
		pid := MatchProject(displayName, projects, repos)

		for _, m := range memEntries {
			if m.IsDir() || !strings.HasSuffix(m.Name(), ".md") {
				continue
			}
			base := strings.TrimSuffix(m.Name(), ".md")
			if base == "MEMORY" {
				continue
			}
			raw, err := memsrc.ReadCapped(filepath.Join(memDir, m.Name()), maxImportReadBytes)
			if err != nil {
				continue
			}
			if len(raw) > db.MaxNoteContentLen {
				// The runtime would reject the oversized doc anyway; skipping
				// here keeps the failure attributed to the file that caused it.
				log.Printf("claude importer: skipping %s/%s: %d bytes exceeds the %d-byte note limit",
					e.Name(), m.Name(), len(raw), db.MaxNoteContentLen)
				continue
			}
			docs = append(docs, plugin.ImportDoc{
				ProjectID: pid, // 0 when no project matched -> the runtime lands it (ADR-0023)
				// Claude's match key is a decoded label, not a real repo path, so the
				// placeholder is keyed on a synthetic URI (it will not auto-promote on
				// scan — the ADR calls this out) but carries the human repo name.
				RepoPath: "import://claude/" + e.Name(),
				RepoName: displayName,
				Title:    NoteTitle(base),
				Content:  StripFrontmatter(string(raw)),
				Kind:     "knowledge",
				Source:   SourceName,
			})
		}
	}
	return docs, nil
}

// DisplayName extracts the final path segment from a Claude project dir name
// like "-Users-name-Workspace-ProjectName" -> "ProjectName".
func DisplayName(dirName string) string {
	s := dirName
	if strings.HasPrefix(s, "-") {
		s = strings.TrimPrefix(s, "-")
	}
	parts := strings.Split(s, "-")
	return parts[len(parts)-1]
}

// NoteTitle maps a Claude memory filename to a human-readable note title.
func NoteTitle(filename string) string {
	switch filename {
	case "project":
		return "项目知识"
	case "user":
		return "用户信息"
	case "feedback":
		return "反馈记录"
	case "reference":
		return "参考信息"
	default:
		return filename
	}
}

// MatchProject finds the Cinder project id for a Claude memory dir name. It
// is a thin wrapper over the shared memsrc matcher, kept exported so the Claude
// importer's own tests and callers remain stable while the matching rules stay
// common across all built-in importers.
func MatchProject(displayName string, projects []db.Project, repos []db.Repository) int64 {
	return memsrc.MatchProject(displayName, projects, repos)
}

// StripFrontmatter removes a leading YAML frontmatter block from a markdown
// string. Thin wrapper over memsrc.StripFrontmatter, kept exported for this
// package's tests and callers while the logic stays shared across importers.
func StripFrontmatter(s string) string {
	return memsrc.StripFrontmatter(s)
}
