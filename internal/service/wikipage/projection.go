package wikipage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"repotory/internal/db"
)

// Approved-page disk projection (ADR-0069): a derived, approved-only Markdown
// mirror of the wiki layer that Cinder maintains itself.
//
// How this differs from ExportWikiTree (ADR-0014 决策 2), which stays as it is:
//
//   - export is a REVIEW surface: every status lands on disk tagged in
//     frontmatter, the target is user-chosen, nothing is ever deleted, and a
//     non-empty directory needs --force. The projection is a READ surface:
//     approved pages only (the human gate of ADR-0015 governs it exactly like
//     retrieval and the session brief), the target is Cinder's own data
//     directory, and stale files ARE pruned — but only files the previous
//     manifest names. A file the user dropped into the tree is never touched.
//   - export is manual; the projection refreshes on every page mutation that
//     can change the approved set (approve / merge / promote / assign / file
//     answer), plus a CLI refresh for the catch-all cases.
//
// Nothing flows back: the mirror is a derived artifact, deletable at any time,
// rebuilt from the database on the next refresh. SQLite stays the SSOT.

// ProjectionManifestName is the file that records what the last refresh
// wrote. It is the pruning authority: only paths it lists may be removed.
const ProjectionManifestName = "PROJECTION-MANIFEST.json"

// ProjectionReport is what one refresh did. Files doubles as the manifest
// body, same shape as the export's so the digest convention carries over.
type ProjectionReport struct {
	Root          string           `json:"-"`
	Files         []WikiExportFile `json:"files"`
	Pages         int              `json:"pages"`
	SkippedPages  int              `json:"skipped_pages"`
	DanglingLinks int              `json:"dangling_links"`
	// Pruned lists managed files this refresh removed (page deleted, merged
	// away or moved between tiers). Report-only: the manifest records what
	// IS, not what was.
	Pruned []string `json:"-"`
}

// ProjectApprovedPages rebuilds rootDir as the approved-only mirror. It is a
// full rebuild every time — the page count is human-scale (tens to hundreds),
// the trigger is a human action, and a full rebuild is trivially idempotent,
// which an incremental diff over renames/merges/tier-moves would not be.
func ProjectApprovedPages(database *sql.DB, rootDir string) (*ProjectionReport, error) {
	if strings.TrimSpace(rootDir) == "" {
		return nil, fmt.Errorf("projection directory is required")
	}
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, err
	}
	report := &ProjectionReport{Root: abs}

	pages, err := db.ListWikiPagesByStatus(database, db.WikiStatusApproved, 0)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]db.WikiPage, len(pages))
	bySlug := make(map[string]db.WikiPage, len(pages))
	for _, p := range pages {
		byID[p.ID] = p
		bySlug[p.Slug] = p
	}

	// The previous manifest is read BEFORE anything is written: it is the
	// only authority for what may be pruned afterwards.
	previous := readProjectionManifest(abs)

	tiers := map[int64]string{}
	tierPages := map[string][]db.WikiPage{}
	files := map[string]string{}
	for _, p := range pages {
		dir, ok := wikiKindDirs[p.Kind]
		if !ok {
			report.SkippedPages++
			continue
		}
		tier := projectionTier(database, tiers, p.ProjectID)
		rel, ok := projectionRelPath(tier, dir, p.Slug)
		if !ok {
			report.SkippedPages++
			continue
		}
		body, dangling := renderProjectionPage(database, byID, bySlug, p)
		report.DanglingLinks += dangling
		files[rel] = body
		tierPages[tier] = append(tierPages[tier], p)
	}
	for tier, group := range tierPages {
		files[tier+"/index.md"] = renderProjectionIndex(group)
	}

	written, err := writeFiles(abs, files)
	if err != nil {
		return nil, err
	}
	report.Files = append(report.Files, written...)
	report.Pages = len(pages) - report.SkippedPages

	// Prune only what the previous manifest named and this run did not write.
	// A path that fails the safety check is skipped, not deleted: a corrupt
	// or hostile manifest must not turn the pruner into an arbitrary delete.
	current := make(map[string]bool, len(files))
	for rel := range files {
		current[rel] = true
	}
	for _, rel := range previous {
		if current[rel] || !safeManagedPath(rel) {
			continue
		}
		full := filepath.Join(abs, filepath.FromSlash(rel))
		if err := os.Remove(full); err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("prune %s: %w", rel, err)
			}
			continue
		}
		report.Pruned = append(report.Pruned, rel)
		// Tidy the directory the pruned file lived in, but only if it went
		// empty — a non-empty dir fails os.Remove and stays, which is the
		// whole safety argument for using Remove instead of RemoveAll.
		_ = os.Remove(filepath.Dir(full))
	}
	sort.Strings(report.Pruned)

	// Manifest last, same crash-consistency rule as the export: a half-done
	// refresh leaves a manifest that does not match the tree, which is the
	// signal to re-run rather than trust.
	manifest, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, err
	}
	last, err := writeFiles(abs, map[string]string{ProjectionManifestName: string(manifest) + "\n"})
	if err != nil {
		return nil, err
	}
	report.Files = append(report.Files, last...)
	sort.Slice(report.Files, func(i, j int) bool { return report.Files[i].Path < report.Files[j].Path })
	return report, nil
}

// projectionTier names the per-project directory: "global" for pages that
// span projects, "p<id>-<name-slug>" otherwise. The id leads so two projects
// with the same name never collide; the slug is for the human reading the
// tree. A project row that cannot be read (deleted mid-refresh) falls back to
// the bare id rather than failing the whole mirror.
func projectionTier(database *sql.DB, cache map[int64]string, projectID int64) string {
	if projectID <= 0 {
		return "global"
	}
	if t, ok := cache[projectID]; ok {
		return t
	}
	t := fmt.Sprintf("p%d", projectID)
	if p, err := db.GetProjectByID(database, projectID); err == nil && p != nil {
		if slug := db.NormalizeWikiSlug(p.Name); slug != "" {
			t = fmt.Sprintf("p%d-%s", projectID, slug)
		}
	}
	cache[projectID] = t
	return t
}

// projectionRelPath is the traversal guard, same posture as the export's
// pageRelPath: slugs are derived from user titles, so a filesystem writer
// rejects rather than assumes.
func projectionRelPath(tier, kindDir, slug string) (string, bool) {
	s := strings.TrimSpace(slug)
	if s == "" || strings.ContainsAny(s, `/\`) || s == "." || s == ".." {
		return "", false
	}
	return tier + "/" + kindDir + "/" + s + ".md", true
}

// safeManagedPath validates a path from the PREVIOUS manifest before the
// pruner may delete it: relative, no traversal, no absolute prefix.
func safeManagedPath(rel string) bool {
	// filepath.IsAbs needs a drive letter on Windows, so a POSIX absolute
	// path such as "/etc/passwd" slips through there. Reject the leading
	// slash explicitly to keep the pruner's containment check identical on
	// every platform.
	if rel == "" || strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) || strings.Contains(rel, `\`) {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	return clean == rel && !strings.HasPrefix(clean, "../") && clean != ".."
}

func readProjectionManifest(abs string) []string {
	raw, err := os.ReadFile(filepath.Join(abs, ProjectionManifestName))
	if err != nil {
		return nil
	}
	var m struct {
		Files []WikiExportFile `json:"files"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	var out []string
	for _, f := range m.Files {
		if f.Path != ProjectionManifestName {
			out = append(out, f.Path)
		}
	}
	return out
}

// renderProjectionPage is renderPage's read-surface twin. Two differences,
// both content boundaries: no status field (everything here is approved by
// construction, and a status line would imply other statuses might appear),
// and edges are filtered to approved targets — a backlink from a pending
// page would otherwise leak its title into the mirror. The source-notes
// section is dropped too: the mirror is the page layer, notes stay reachable
// through the tools.
func renderProjectionPage(database *sql.DB, byID map[int64]db.WikiPage, bySlug map[string]db.WikiPage, p db.WikiPage) (string, int) {
	dangling := 0
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "repotory_page_id: %d\nslug: %s\ntitle: %s\nkind: %s\n", p.ID, p.Slug, yamlQuote(p.Title), p.Kind)
	if p.ProjectID > 0 {
		fmt.Fprintf(&b, "project_id: %d\n", p.ProjectID)
	}
	b.WriteString("source: repotory-projection\n---\n\n")
	fmt.Fprintf(&b, "# %s\n\n", p.Title)
	body := strings.TrimSpace(p.Content)
	if body != "" {
		b.WriteString(body + "\n\n")
		for _, m := range WikiLinkRe.FindAllStringSubmatch(body, -1) {
			if _, ok := bySlug[m[1]]; !ok {
				dangling++
			}
		}
	}
	for _, sec := range []struct {
		heading string
		edges   func(*sql.DB, int64) ([]db.PageEdge, error)
	}{
		{"## 出链", db.WikiEdgesFrom},
		{"## 反向链接", db.WikiEdgesTo},
	} {
		edges, err := sec.edges(database, p.ID)
		if err != nil {
			continue
		}
		var lines []string
		for _, e := range edges {
			if _, ok := byID[e.PageID]; !ok {
				continue
			}
			lines = append(lines, fmt.Sprintf("- [[%s|%s]] — %s", e.Slug, e.Title, e.Relation))
		}
		if len(lines) > 0 {
			b.WriteString(sec.heading + "\n\n" + strings.Join(lines, "\n") + "\n\n")
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n", dangling
}

// renderProjectionIndex is the per-tier catalog, approved-only by
// construction. Same shape as the export index so an Obsidian habit
// transfers.
func renderProjectionIndex(pages []db.WikiPage) string {
	var b strings.Builder
	b.WriteString("---\ntitle: Wiki index\nsource: repotory-projection\n---\n\n")
	b.WriteString("# Wiki index\n\n")
	order := []string{db.WikiKindEntity, db.WikiKindConcept, db.WikiKindSource, db.WikiKindSynthesis, db.WikiKindQuery}
	for _, kind := range order {
		var group []db.WikiPage
		for _, p := range pages {
			if p.Kind == kind {
				group = append(group, p)
			}
		}
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## %s (%d)\n\n", kind, len(group))
		sort.Slice(group, func(i, j int) bool { return group[i].Slug < group[j].Slug })
		for _, p := range group {
			fmt.Fprintf(&b, "- [[%s|%s]]", p.Slug, p.Title)
			if line := FirstLine(p.Content); line != "" && line != p.Title {
				fmt.Fprintf(&b, " — %s", ClipRunes(line, 100))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}
