package runtime

import (
	"testing"

	"repotory/internal/core/plugin"
	"repotory/internal/db"
)

// dynamicImporter serves a doc list the test can rewrite between phases, so one
// runtime instance can first seed via the real import and then be previewed.
type dynamicImporter struct {
	source string
	docs   *[]plugin.ImportDoc
}

func (d dynamicImporter) Source() string { return d.source }
func (d dynamicImporter) Import() ([]plugin.ImportDoc, error) {
	return *d.docs, nil
}

// The preview must classify against the same (project, source, title) key the
// real upsert uses, resolve landing read-only, and write NOTHING — no note, no
// placeholder project (ADR-0011 决策 4 dry-run).
func TestPreviewImportWritesNothing(t *testing.T) {
	database := newFullDB(t)
	rt := New(database)

	docs := []plugin.ImportDoc{
		{RepoPath: "/code/prev", Title: "existing", Content: "x", Kind: "log", Source: "fake"},
	}
	rt.RegisterSourceManual("fake", dynamicImporter{source: "fake", docs: &docs})

	if _, err := rt.TriggerImport("fake"); err != nil {
		t.Fatalf("seed TriggerImport: %v", err)
	}

	countNotes := func() int {
		t.Helper()
		var n int
		if err := database.QueryRow(`SELECT count(*) FROM project_notes`).Scan(&n); err != nil {
			t.Fatalf("count notes: %v", err)
		}
		return n
	}
	countProjects := func() int {
		t.Helper()
		var n int
		if err := database.QueryRow(`SELECT count(*) FROM projects`).Scan(&n); err != nil {
			t.Fatalf("count projects: %v", err)
		}
		return n
	}
	notesBefore, projectsBefore := countNotes(), countProjects()

	defaultPID, err := db.GetDefaultProjectID(database)
	if err != nil {
		t.Fatalf("default project: %v", err)
	}
	if defaultPID <= 0 {
		t.Fatalf("default project id = %d, want a seeded row", defaultPID)
	}

	// Same source key as the seeded note (update), a new sibling on the same
	// repo (create on the existing placeholder), a repo nobody owns (create,
	// but the placeholder itself must NOT be created by a preview), and an
	// agent-global doc (create on the seeded default project). Replace the
	// list rather than append: the seeded doc must appear exactly once.
	docs = []plugin.ImportDoc{
		plugin.ImportDoc{RepoPath: "/code/prev", Title: "existing", Content: "x2", Kind: "log", Source: "fake"},
		plugin.ImportDoc{RepoPath: "/code/prev", Title: "fresh", Content: "x", Kind: "log", Source: "fake"},
		plugin.ImportDoc{RepoPath: "/code/never-seen", Title: "ghost", Content: "x", Kind: "log", Source: "fake"},
		plugin.ImportDoc{RepoPath: "", Title: "global", Content: "x", Kind: "knowledge", Source: "fake"},
	}

	p, err := rt.PreviewImport("fake")
	if err != nil {
		t.Fatalf("PreviewImport: %v", err)
	}
	if p.Created != 3 || p.Updated != 1 || p.Skipped != 0 {
		t.Fatalf("preview = created %d, updated %d, skipped %d; want 3/1/0", p.Created, p.Updated, p.Skipped)
	}
	byTitle := map[string]ImportPreviewItem{}
	for _, it := range p.Items {
		byTitle[it.Title] = it
	}
	if byTitle["existing"].Action != "update" {
		t.Errorf("existing = %+v, want update", byTitle["existing"])
	}
	if it := byTitle["fresh"]; it.Action != "create" || it.ProjectID <= 0 {
		t.Errorf("fresh = %+v, want create on the placeholder project", it)
	}
	if it := byTitle["ghost"]; it.Action != "create" || it.ProjectID != 0 {
		t.Errorf("ghost = %+v, want create with project 0 (a preview must not create the placeholder)", it)
	}
	if it := byTitle["global"]; it.Action != "create" || it.ProjectID != defaultPID {
		t.Errorf("global = %+v, want create on the default project %d", it, defaultPID)
	}

	if got := countNotes(); got != notesBefore {
		t.Errorf("preview wrote notes: %d -> %d", notesBefore, got)
	}
	if got := countProjects(); got != projectsBefore {
		t.Errorf("preview wrote projects: %d -> %d (a placeholder leaked)", projectsBefore, got)
	}
}

func TestPreviewImportUnknownSource(t *testing.T) {
	database := newFullDB(t)
	rt := New(database)
	if _, err := rt.PreviewImport("nope"); err == nil {
		t.Fatal("an unknown source must error")
	}
}

// Preview must classify a formula-change doc the way upsertDoc will handle it:
// a legacy-title row exists, so the real import renames it in place — an
// update, not a create. Reporting it as a new note would lie about the count
// the UI shows.
func TestPreviewClassifiesLegacyRenameAsUpdate(t *testing.T) {
	database := setupDB(t)
	pid := newProject(t, database, "preview-rename")
	if _, err := db.CreateNoteEx(database, pid, "old title", "body", "", "log", "codex"); err != nil {
		t.Fatal(err)
	}
	rt := New(database)
	rt.RegisterSource("codex", staticImporter{source: "codex", docs: []plugin.ImportDoc{
		{ProjectID: pid, Title: "new title", Content: "body", Kind: "log", LegacyTitles: []string{"old title"}},
	}})
	pv, err := rt.PreviewImport("codex")
	if err != nil {
		t.Fatal(err)
	}
	if pv.Created != 0 || pv.Updated != 1 {
		t.Fatalf("preview = created %d / updated %d, want 0/1 (rename in place)", pv.Created, pv.Updated)
	}
}
