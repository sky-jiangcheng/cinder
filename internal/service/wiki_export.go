package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"repotory/internal/db"
)

// One-way wiki export (ADR-0014 决策 2 / TODO M6-W1b).
//
// The ADR chose "SQLite stays the SSOT, the file tree is a read-only export"
// over the file-tree-as-database design every reference implementation uses. The
// argument was that two writers force a second implementation of versioning,
// de-duplication and conflict handling. This function is where that choice pays
// or fails: it renders the derived layer as ordinary Markdown with wikilinks so
// Obsidian's graph view can answer the question a database cannot — "is this
// taxonomy actually the shape of the knowledge?" — while nothing ever flows back.
//
// Layout mirrors the reference implementations so an existing Obsidian habit
// transfers: wiki/<entities|concepts|sources|synthesis|queries>/<slug>.md plus an
// index and a manifest.

// wikiKindDirs maps a page kind to its export directory.
var wikiKindDirs = map[string]string{
	db.WikiKindEntity:    "entities",
	db.WikiKindConcept:   "concepts",
	db.WikiKindSource:    "sources",
	db.WikiKindSynthesis: "synthesis",
	db.WikiKindQuery:     "queries",
}

// WikiExportReport is what one export did, and doubles as the manifest written
// next to the files.
type WikiExportReport struct {
	// Root is where this run wrote. Deliberately excluded from the manifest: the
	// manifest exists to make "did the content change" answerable, and an absolute
	// path in it differs on every export to a different directory — which is
	// exactly what the determinism test caught the first time.
	Root  string           `json:"-"`
	Files []WikiExportFile `json:"files"`
	Pages int              `json:"pages"`
	// DanglingLinks counts [[wikilinks]] typed into page bodies whose target page
	// is not in this store. Page->page edges cannot dangle by construction (the
	// page_links FKs cascade with the target), so a body link is the ONLY way a
	// reference here can rot — and reporting it is what gives the W4 lint pass
	// something real to work from instead of a clean-looking export.
	DanglingLinks int `json:"dangling_links"`
	// SkippedPages counts pages whose slug is unwritable (path traversal or empty).
	SkippedPages int `json:"skipped_pages"`
	// StaleFiles are Markdown files already under <root>/wiki that this export did
	// NOT write — left behind by a deleted or renamed page. They are reported, never
	// deleted: pruning would mean treating the tree as ours to own, which is exactly
	// what ADR-0014 决策 2 refuses. The manifest is the source of truth for what is
	// current, so "delete it yourself if you want" is the honest contract.
	StaleFiles []string `json:"stale_files,omitempty"`
}

// WikiExportFile is one written file with its digest, so "the tree on disk
// matches what we wrote" is checkable without re-reading the database.
type WikiExportFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

// ErrWikiExportNotEmpty means the target directory already holds files and
// --force was not given.
var ErrWikiExportNotEmpty = fmt.Errorf("export directory is not empty")

// ExportWikiTree renders the wiki layer into rootDir/wiki/**. It refuses to
// overwrite anything unless force is set, because this path is by contract
// one-way: silently replacing files the user may have annotated in Obsidian would
// turn a read-only view into an unasked-for destructive writer, which is the exact
// failure mode ADR-0014 决策 2 was written to avoid.
func (s *Service) ExportWikiTree(rootDir string, projectID int64, force bool) (*WikiExportReport, error) {
	if strings.TrimSpace(rootDir) == "" {
		return nil, fmt.Errorf("export directory is required")
	}
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, err
	}
	report := &WikiExportReport{Root: filepath.Join(abs, "wiki")}

	if err := guardEmptyDir(abs, force); err != nil {
		return nil, err
	}

	pages, err := db.ListWikiPages(s.db, "", projectID)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]db.WikiPage, len(pages))
	bySlug := make(map[string]db.WikiPage, len(pages))
	for _, p := range pages {
		byID[p.ID] = p
		bySlug[p.Slug] = p
	}

	files := map[string]string{}
	for _, p := range pages {
		dir, ok := wikiKindDirs[p.Kind]
		if !ok {
			report.SkippedPages++
			continue
		}
		rel, ok := pageRelPath(dir, p.Slug)
		if !ok {
			report.SkippedPages++
			continue
		}
		body, dangling := s.renderPage(byID, bySlug, p)
		report.DanglingLinks += dangling
		files[filepath.ToSlash(rel)] = body
	}

	files["wiki/index.md"] = renderIndex(pages, byID)

	manifestFiles, err := writeFiles(abs, files)
	if err != nil {
		return nil, err
	}
	report.Files = append(report.Files, manifestFiles...)
	report.Pages = len(pages) - report.SkippedPages

	// The manifest is written last and lists everything else: an export that
	// crashed halfway leaves a manifest that does not match the tree, which is the
	// signal (same philosophy as the packaging manifests' sha256 gate) that the
	// run must be repeated rather than trusted.
	manifest, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, err
	}
	last, err := writeFiles(abs, map[string]string{"wiki/EXPORT-MANIFEST.json": string(manifest) + "\n"})
	if err != nil {
		return nil, err
	}
	report.Files = append(report.Files, last...)
	sort.Slice(report.Files, func(i, j int) bool { return report.Files[i].Path < report.Files[j].Path })

	// Detect leftovers after the fact: the manifest names what we wrote, so
	// anything else under wiki/ is either ours-but-obsolete or the user's. The
	// export cannot tell those apart, which is another reason it only reports.
	written := make(map[string]bool, len(report.Files))
	for _, f := range report.Files {
		written[f.Path] = true
	}
	stale, err := findStaleMarkdown(filepath.Join(abs, "wiki"), written)
	if err != nil {
		return nil, err
	}
	report.StaleFiles = stale
	return report, nil
}

// guardEmptyDir refuses to write into a directory that already contains files,
// unless forced. The export root itself may exist (that is the point of re-export),
// but only with the tree we manage.
func guardEmptyDir(abs string, force bool) error {
	if force {
		return nil
	}
	entries, err := os.ReadDir(abs)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect %s: %w", abs, err)
	}
	for _, e := range entries {
		// Our own managed subtree is always fine to rewrite.
		if e.IsDir() && e.Name() == "wiki" {
			continue
		}
		return fmt.Errorf("%w: %s already contains %q (pass --force to overwrite)",
			ErrWikiExportNotEmpty, abs, e.Name())
	}
	return nil
}

// pageRelPath maps a slug to a safe path under the export root. A slug is
// generated by NormalizeWikiSlug, but it is derived from user titles and this is a
// filesystem writer, so traversal is rejected rather than assumed impossible.
func pageRelPath(dir, slug string) (string, bool) {
	s := strings.TrimSpace(slug)
	if s == "" || strings.ContainsAny(s, `/\`) || s == "." || s == ".." {
		return "", false
	}
	return filepath.Join("wiki", dir, s+".md"), true
}

// findStaleMarkdown lists .md files under wikiDir that this run did not write,
// by their path relative to the export root.
func findStaleMarkdown(wikiDir string, written map[string]bool) ([]string, error) {
	var stale []string
	err := filepath.WalkDir(wikiDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, rerr := filepath.Rel(filepath.Dir(wikiDir), path)
		if rerr != nil {
			return rerr
		}
		if !written[filepath.ToSlash(rel)] {
			stale = append(stale, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(stale)
	return stale, err
}

var wikiLinkRe = regexp.MustCompile(`\[\[([^\]|]+)(?:\|[^\]]+)?\]\]`)

// renderPage produces one Markdown document: frontmatter, the page body, its
// outgoing links as wikilinks, backlinks, and the notes it was compiled from.
// The second return counts body wikilinks whose target page does not exist.
func (s *Service) renderPage(byID map[int64]db.WikiPage, bySlug map[string]db.WikiPage, p db.WikiPage) (string, int) {
	dangling := 0
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "repotory_page_id: %d\nslug: %s\ntitle: %s\nkind: %s\n", p.ID, p.Slug, yamlQuote(p.Title), p.Kind)
	// Status is exported so the tree can be reviewed where it is read: pending
	// pages appear here tagged pending (that is the review surface we have today),
	// while index.md below lists approved pages only.
	fmt.Fprintf(&b, "status: %s\nsource: %s\n", p.Status, p.Source)
	if p.ProjectID > 0 {
		fmt.Fprintf(&b, "project_id: %d\n", p.ProjectID)
	}
	b.WriteString("source: repotory-export\n---\n\n")
	fmt.Fprintf(&b, "# %s\n\n", p.Title)
	body := strings.TrimSpace(p.Content)
	if body != "" {
		b.WriteString(body + "\n\n")
		// A [[target]] typed into a body is the one reference this layer cannot
		// protect with a constraint, so it is checked here and counted.
		for _, m := range wikiLinkRe.FindAllStringSubmatch(body, -1) {
			if _, ok := bySlug[m[1]]; !ok {
				dangling++
			}
		}
	}

	if out, err := db.WikiEdgesFrom(s.db, p.ID); err != nil {
		log.Printf("wiki export: out edges for page %d: %v", p.ID, err)
	} else if len(out) > 0 {
		b.WriteString("## 出链\n\n")
		for _, e := range out {
			// The FK guarantees the target exists, so no "missing" branch here:
			// asserting one would be asserting dead code.
			fmt.Fprintf(&b, "- [[%s|%s]] — %s\n", e.Slug, e.Title, e.Relation)
		}
		b.WriteString("\n")
	}
	if back, err := db.WikiEdgesTo(s.db, p.ID); err != nil {
		log.Printf("wiki export: in edges for page %d: %v", p.ID, err)
	} else if len(back) > 0 {
		b.WriteString("## 反向链接\n\n")
		for _, e := range back {
			fmt.Fprintf(&b, "- [[%s|%s]] — %s\n", e.Slug, e.Title, e.Relation)
		}
		b.WriteString("\n")
	}
	if notes, err := db.NotesForPage(s.db, p.ID); err != nil {
		log.Printf("wiki export: source notes for page %d: %v", p.ID, err)
	} else if len(notes) > 0 {
		b.WriteString("## 来源笔记\n\n")
		for _, id := range notes {
			n, err := db.GetNoteByID(s.db, id)
			if err != nil {
				log.Printf("wiki export: source note %d for page %d: %v", id, p.ID, err)
				continue
			}
			if n == nil {
				continue
			}
			title := strings.TrimSpace(n.Title)
			if title == "" {
				title = firstLine(n.Content)
			}
			fmt.Fprintf(&b, "- note #%d — %s\n", n.ID, title)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n", dangling
}

// renderIndex is the content-oriented catalog: every exported page, grouped by
// kind, one line each. In the reference design the index is what lets an agent
// navigate without embeddings, so it lists everything rather than a sample.
func renderIndex(pages []db.WikiPage, byID map[int64]db.WikiPage) string {
	var b strings.Builder
	b.WriteString("---\ntitle: Wiki index\nsource: repotory-export\n---\n\n")
	b.WriteString("# Wiki index\n\n")
	order := []string{db.WikiKindEntity, db.WikiKindConcept, db.WikiKindSource, db.WikiKindSynthesis, db.WikiKindQuery}
	for _, kind := range order {
		var group []db.WikiPage
		for _, p := range pages {
			// The index is a retrieval artifact, so it stays approved-only: an
			// index an agent reads first must not route it into unreviewed pages.
			if p.Kind == kind && p.Status == db.WikiStatusApproved {
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
			if line := firstLine(p.Content); line != "" && line != p.Title {
				fmt.Fprintf(&b, " — %s", clipRunes(line, 100))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(byID) == 0 {
		b.WriteString("_没有页面。这一层是派生产物，需要 W3 的编译步骤或手工建页才会填充。_\n")
	}
	return b.String()
}

func writeFiles(root string, files map[string]string) ([]WikiExportFile, error) {
	paths := make([]string, 0, len(files))
	for rel := range files {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	out := make([]WikiExportFile, 0, len(paths))
	for _, rel := range paths {
		body := files[rel]
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			return nil, err
		}
		sum := sha256.Sum256([]byte(body))
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			return nil, err
		}
		out = append(out, WikiExportFile{
			Path: rel, SHA256: hex.EncodeToString(sum[:]), Bytes: len(body),
		})
	}
	return out, nil
}

func yamlQuote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
