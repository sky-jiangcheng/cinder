package wikipage

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"repotory/internal/db"
)

// The projection is a READ surface (ADR-0069): approved-only, manifest-scoped
// pruning, deterministic output. These tests pin each of those contracts —
// especially the two that would leak unreviewed content or delete user files
// if they regressed.

func projectionProject(t *testing.T, database *sql.DB, name string) int64 {
	t.Helper()
	res, err := database.Exec(
		`INSERT INTO projects (name, root_path, project_kind) VALUES (?, ?, 'scanned')`,
		name, "/tmp/"+name)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func approvedPage(t *testing.T, database *sql.DB, kind, slug, title string, projectID int64, body string) *db.WikiPage {
	t.Helper()
	p, err := db.CreateWikiPageAs(database, kind, slug, title, projectID, body, db.WikiStatusApproved, "manual")
	if err != nil {
		t.Fatalf("create page %s: %v", slug, err)
	}
	return p
}

// walkMirror returns every file under root keyed by slash-relative path.
func walkMirror(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		out[filepath.ToSlash(rel)] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatalf("walk mirror: %v", err)
	}
	return out
}

func TestProjectApprovedPagesApprovedOnlyWithTiers(t *testing.T) {
	database := setupWikiDB(t)
	pid := projectionProject(t, database, "Pay Center")
	approvedPage(t, database, db.WikiKindEntity, "payment-gateway", "Payment Gateway", pid, "Retries and idempotency.")
	approvedPage(t, database, db.WikiKindConcept, "shared-idempotency", "Shared Idempotency", 0, "Cross-project rule.")
	mustPage(t, database, "pending-secret-plans", "PENDING-BODY-MARKER", db.WikiStatusPending)
	rej, err := db.CreateWikiPageAs(database, db.WikiKindSource, "rejected-src", "REJECTED-TITLE-MARKER", pid, "x", db.WikiStatusRejected, "ai")
	if err != nil {
		t.Fatal(err)
	}
	_ = rej

	root := filepath.Join(t.TempDir(), "pages")
	report, err := ProjectApprovedPages(database, root)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if report.Pages != 2 {
		t.Errorf("Pages = %d, want 2 (pending and rejected must not count)", report.Pages)
	}

	files := walkMirror(t, root)
	tier := "p" + strconv.FormatInt(pid, 10) + "-pay-center"
	for _, want := range []string{
		tier + "/entities/payment-gateway.md",
		tier + "/index.md",
		"global/concepts/shared-idempotency.md",
		"global/index.md",
		ProjectionManifestName,
	} {
		if _, ok := files[want]; !ok {
			t.Errorf("missing %s; got %v", want, keysOf(files))
		}
	}
	for rel, body := range files {
		if strings.Contains(body, "PENDING-BODY-MARKER") || strings.Contains(body, "REJECTED-TITLE-MARKER") ||
			strings.Contains(body, "pending-secret-plans") || strings.Contains(body, "rejected-src") {
			t.Errorf("%s leaks unapproved content", rel)
		}
		if strings.Contains(body, "status:") {
			t.Errorf("%s carries a status field; the mirror is approved-only by construction", rel)
		}
	}
	page := files[tier+"/entities/payment-gateway.md"]
	if !strings.Contains(page, "source: repotory-projection") {
		t.Error("page lacks the projection frontmatter marker")
	}
}

func TestProjectApprovedPagesEdgesFilteredToApproved(t *testing.T) {
	database := setupWikiDB(t)
	target := approvedPage(t, database, db.WikiKindEntity, "gateway", "Gateway", 0, "body")
	friend := approvedPage(t, database, db.WikiKindConcept, "retry-rule", "Retry Rule", 0, "body")
	pending := mustPage(t, database, "pending-referrer", "PENDING-REFERRER-BODY", db.WikiStatusPending)
	if err := db.LinkWikiPages(database, friend.ID, target.ID, "depends-on"); err != nil {
		t.Fatal(err)
	}
	if err := db.LinkWikiPages(database, pending.ID, target.ID, "mentions"); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(t.TempDir(), "pages")
	if _, err := ProjectApprovedPages(database, root); err != nil {
		t.Fatal(err)
	}
	body := walkMirror(t, root)["global/entities/gateway.md"]
	if !strings.Contains(body, "[[retry-rule|Retry Rule]]") {
		t.Error("approved backlink missing from the mirror")
	}
	if strings.Contains(body, "pending-referrer") {
		t.Error("a pending page's backlink leaked its slug/title into the mirror")
	}
}

func TestProjectApprovedPagesPrunesOnlyManagedFiles(t *testing.T) {
	database := setupWikiDB(t)
	keep := approvedPage(t, database, db.WikiKindEntity, "keep", "Keep", 0, "body")
	drop := approvedPage(t, database, db.WikiKindEntity, "drop", "Drop", 0, "body")
	_ = keep

	root := filepath.Join(t.TempDir(), "pages")
	if _, err := ProjectApprovedPages(database, root); err != nil {
		t.Fatal(err)
	}
	// A file the user dropped into the tree is NOT ours to delete.
	userFile := filepath.Join(root, "global", "my-annotations.md")
	if err := os.WriteFile(userFile, []byte("user notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteWikiPage(database, drop.ID); err != nil {
		t.Fatal(err)
	}

	report, err := ProjectApprovedPages(database, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Pruned) != 1 || report.Pruned[0] != "global/entities/drop.md" {
		t.Errorf("Pruned = %v, want [global/entities/drop.md]", report.Pruned)
	}
	files := walkMirror(t, root)
	if _, ok := files["global/entities/drop.md"]; ok {
		t.Error("deleted page's file survived the refresh")
	}
	if _, ok := files["global/entities/keep.md"]; !ok {
		t.Error("still-approved page was lost")
	}
	if _, ok := files["global/my-annotations.md"]; !ok {
		t.Error("user file was pruned; only manifest-listed paths may be removed")
	}
}

func TestProjectApprovedPagesHostileManifestCannotDelete(t *testing.T) {
	database := setupWikiDB(t)
	approvedPage(t, database, db.WikiKindEntity, "safe", "Safe", 0, "body")

	base := t.TempDir()
	root := filepath.Join(base, "pages")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside.md")
	if err := os.WriteFile(outside, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A corrupt/hostile manifest names a traversal path and an absolute path.
	manifest := `{"files":[{"path":"../outside.md"},{"path":"` + outside + `"},{"path":"a/../../outside.md"}]}`
	if err := os.WriteFile(filepath.Join(root, ProjectionManifestName), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := ProjectApprovedPages(database, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Pruned) != 0 {
		t.Errorf("Pruned = %v, want none: hostile paths must be skipped", report.Pruned)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Error("the pruner deleted a file outside the mirror")
	}
}

func TestProjectApprovedPagesDeterministic(t *testing.T) {
	database := setupWikiDB(t)
	pid := projectionProject(t, database, "determinism")
	approvedPage(t, database, db.WikiKindEntity, "alpha", "Alpha", pid, "body with [[beta]] link")
	approvedPage(t, database, db.WikiKindConcept, "beta", "Beta", 0, "body")

	root := filepath.Join(t.TempDir(), "pages")
	first, err := ProjectApprovedPages(database, root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ProjectApprovedPages(database, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Files) != len(second.Files) {
		t.Fatalf("file count changed: %d vs %d", len(first.Files), len(second.Files))
	}
	for i := range first.Files {
		if first.Files[i] != second.Files[i] {
			t.Errorf("file %s differs between identical runs: %+v vs %+v",
				first.Files[i].Path, first.Files[i], second.Files[i])
		}
	}
	// The manifest on disk must match the report (Root excluded, Pruned excluded).
	raw, err := os.ReadFile(filepath.Join(root, ProjectionManifestName))
	if err != nil {
		t.Fatal(err)
	}
	var m ProjectionReport
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m.Pages != 2 || len(m.Files) != len(first.Files)-1 {
		t.Errorf("manifest mismatch: pages=%d files=%d", m.Pages, len(m.Files))
	}
}

func TestSafeManagedPath(t *testing.T) {
	for _, tc := range []struct {
		rel  string
		want bool
	}{
		{"global/entities/x.md", true},
		{"p1-pay/concepts/y.md", true},
		{ProjectionManifestName, true},
		{"", false},
		{"/etc/passwd", false},
		{"../outside.md", false},
		{"a/../../outside.md", false},
		{`a\b.md`, false},
		{"a/../b.md", false}, // cleans to b.md ≠ rel: not a path we wrote
		{"..", false},
		{"./x.md", false},
	} {
		if got := safeManagedPath(tc.rel); got != tc.want {
			t.Errorf("safeManagedPath(%q) = %v, want %v", tc.rel, got, tc.want)
		}
	}
}

func TestProjectionRelPathRejectsTraversal(t *testing.T) {
	for _, slug := range []string{"", " ", "..", ".", "a/b", `a\b`} {
		if _, ok := projectionRelPath("global", "entities", slug); ok {
			t.Errorf("projectionRelPath accepted hostile slug %q", slug)
		}
	}
	if rel, ok := projectionRelPath("global", "entities", "fine-slug"); !ok || rel != "global/entities/fine-slug.md" {
		t.Errorf("projectionRelPath(fine-slug) = %q, %v", rel, ok)
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
