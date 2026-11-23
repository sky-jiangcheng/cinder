package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"repotory/internal/db"
)

// ADR-0069 integration: every page mutation that can change the approved set
// must leave the disk mirror consistent, the kill switch must stop all writes,
// and an in-memory store must never invent a directory.

func newProjectionTestService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.InitDB(filepath.Join(dir, "projection.db"))
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return New(database, "tester"), dir
}

func seedPendingPage(t *testing.T, svc *Service, pid int64, slug string) *db.WikiPage {
	t.Helper()
	p, err := db.CreateWikiPageAs(svc.db, db.WikiKindConcept, slug, strings.ToTitle(slug), pid, "body of "+slug, db.WikiStatusPending, "ai")
	if err != nil {
		t.Fatalf("create page %s: %v", slug, err)
	}
	return p
}

func TestApproveWikiPageRefreshesMirror(t *testing.T) {
	svc, dir := newProjectionTestService(t)
	pid := seedProject(t, svc.db, "mirror", "/tmp/mirror")
	p := seedPendingPage(t, svc, pid, "retry-policy")

	mirror := filepath.Join(dir, "pages")
	if _, err := os.Stat(mirror); !os.IsNotExist(err) {
		t.Fatal("mirror exists before any approval")
	}
	if err := svc.ApproveWikiPage(p.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	files, err := filepath.Glob(filepath.Join(mirror, "p*", "concepts", "retry-policy.md"))
	if err != nil || len(files) != 1 {
		t.Fatalf("approved page not mirrored: %v (%v)", files, err)
	}
	if _, err := os.Stat(filepath.Join(mirror, "PROJECTION-MANIFEST.json")); err != nil {
		t.Errorf("manifest missing: %v", err)
	}
}

func TestPagesProjectionKillSwitch(t *testing.T) {
	svc, dir := newProjectionTestService(t)
	pid := seedProject(t, svc.db, "mirror", "/tmp/mirror")
	if err := db.SetConfig(svc.db, "pages_projection", "0"); err != nil {
		t.Fatal(err)
	}
	p := seedPendingPage(t, svc, pid, "silent-page")
	if err := svc.ApproveWikiPage(p.ID); err != nil {
		t.Fatalf("approve with kill switch on must still succeed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pages")); !os.IsNotExist(err) {
		t.Error("pages_projection=0 but the mirror was written anyway")
	}
}

func TestMergePrunesLoserFromMirror(t *testing.T) {
	svc, dir := newProjectionTestService(t)
	pid := seedProject(t, svc.db, "mirror", "/tmp/mirror")
	loser := seedPendingPage(t, svc, pid, "dup-page")
	winner := seedPendingPage(t, svc, pid, "canon-page")
	for _, p := range []*db.WikiPage{loser, winner} {
		if err := svc.ApproveWikiPage(p.ID); err != nil {
			t.Fatal(err)
		}
	}
	mirror := filepath.Join(dir, "pages")
	loserFiles, _ := filepath.Glob(filepath.Join(mirror, "p*", "concepts", "dup-page.md"))
	if len(loserFiles) != 1 {
		t.Fatalf("loser not mirrored before merge: %v", loserFiles)
	}
	if _, err := svc.MergeWikiPagesInto(loser.ID, winner.ID); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if _, err := os.Stat(loserFiles[0]); !os.IsNotExist(err) {
		t.Error("merged-away page still on disk; the tombstone must leave the mirror")
	}
	winnerFiles, _ := filepath.Glob(filepath.Join(mirror, "p*", "concepts", "canon-page.md"))
	if len(winnerFiles) != 1 {
		t.Errorf("winner lost from mirror: %v", winnerFiles)
	}
}

func TestPromoteMovesMirrorTier(t *testing.T) {
	svc, dir := newProjectionTestService(t)
	pid := seedProject(t, svc.db, "mirror", "/tmp/mirror")
	p := seedPendingPage(t, svc, pid, "shared-rule")
	if err := svc.ApproveWikiPage(p.ID); err != nil {
		t.Fatal(err)
	}
	mirror := filepath.Join(dir, "pages")
	if err := svc.PromoteWikiPageToGlobal(p.ID); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if _, err := os.Stat(filepath.Join(mirror, "global", "concepts", "shared-rule.md")); err != nil {
		t.Errorf("page not in global tier: %v", err)
	}
	old, _ := filepath.Glob(filepath.Join(mirror, "p*", "concepts", "shared-rule.md"))
	if len(old) != 0 {
		t.Errorf("project-tier copy survived the move: %v", old)
	}
	if err := svc.AssignWikiPageProject(p.ID, pid); err != nil {
		t.Fatalf("assign back: %v", err)
	}
	if _, err := os.Stat(filepath.Join(mirror, "global", "concepts", "shared-rule.md")); !os.IsNotExist(err) {
		t.Error("global copy survived the move back to the project tier")
	}
	back, _ := filepath.Glob(filepath.Join(mirror, "p*", "concepts", "shared-rule.md"))
	if len(back) != 1 {
		t.Errorf("page not back in the project tier: %v", back)
	}
}

func TestInMemoryStoreHasNoMirror(t *testing.T) {
	svc, _ := setupService(t) // :memory:
	if dir := svc.PagesProjectionDir(); dir != "" {
		t.Errorf("in-memory store reports a mirror dir %q", dir)
	}
	if _, err := svc.RefreshPagesProjection(); err == nil {
		t.Error("explicit refresh on an in-memory store must error, not invent a path")
	}
	pid := seedProject(t, svc.db, "mem", "/tmp/mem")
	p := seedPendingPage(t, svc, pid, "mem-page")
	if err := svc.ApproveWikiPage(p.ID); err != nil {
		t.Fatalf("approve on in-memory store: %v", err)
	}
}

func TestContextHeaderExposesMirror(t *testing.T) {
	svc, _ := newProjectionTestService(t)
	pid := seedProject(t, svc.db, "mirror", "/tmp/mirror")
	res := svc.ResolveProject("mirror")
	if res.Project == nil {
		t.Fatal("project did not resolve")
	}
	if doc := svc.BuildProjectContext(res); strings.Contains(doc, "Approved pages mirror") {
		t.Error("mirror line shown before anything was projected")
	}
	p := seedPendingPage(t, svc, pid, "visible-page")
	if err := svc.ApproveWikiPage(p.ID); err != nil {
		t.Fatal(err)
	}
	doc := svc.BuildProjectContext(res)
	// PRAGMA database_list may resolve symlinks (/var → /private/var), so the
	// expected path comes from the service itself, not from re-joining dir.
	if !strings.Contains(doc, "Approved pages mirror (read-only, derived): `"+svc.PagesProjectionDir()+"`") {
		t.Error("context header does not point at the mirror")
	}
	if err := db.SetConfig(svc.db, "pages_projection", "0"); err != nil {
		t.Fatal(err)
	}
	if doc := svc.BuildProjectContext(res); strings.Contains(doc, "Approved pages mirror") {
		t.Error("mirror line shown while the kill switch is off")
	}
}
