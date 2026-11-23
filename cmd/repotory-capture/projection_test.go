package main

import (
	"os"
	"path/filepath"
	"testing"

	"repotory/internal/db"
)

// project-pages is the catch-all refresh (ADR-0069): it must work read-only
// against a closed store, default its output to pages/ beside the database
// file, and fail cleanly on a missing database.

func seedProjectionDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "capture.db")
	database, err := db.InitDB(path)
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	if _, err := db.CreateWikiPageAs(database, db.WikiKindConcept, "cli-page", "CLI Page", 0, "body", db.WikiStatusApproved, "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateWikiPageAs(database, db.WikiKindConcept, "cli-pending", "CLI Pending", 0, "body", db.WikiStatusPending, "ai"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProjectPagesExplicitOut(t *testing.T) {
	path := seedProjectionDB(t)
	out := filepath.Join(t.TempDir(), "mirror")
	if code := runProjectPages([]string{"-db", path, "-out", out}); code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(out, "global", "concepts", "cli-page.md")); err != nil {
		t.Errorf("approved page not projected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "global", "concepts", "cli-pending.md")); !os.IsNotExist(err) {
		t.Error("pending page leaked into the mirror")
	}
	if _, err := os.Stat(filepath.Join(out, "PROJECTION-MANIFEST.json")); err != nil {
		t.Errorf("manifest missing: %v", err)
	}
}

func TestProjectPagesDefaultOutBesideDatabase(t *testing.T) {
	path := seedProjectionDB(t)
	if code := runProjectPages([]string{"-db", path}); code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "pages", "global", "concepts", "cli-page.md")); err != nil {
		t.Errorf("default mirror not beside the database: %v", err)
	}
}

func TestProjectPagesMissingDatabaseFails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such.db")
	if code := runProjectPages([]string{"-db", missing, "-out", t.TempDir()}); code != 1 {
		t.Errorf("exit code %d, want 1 for a missing database", code)
	}
}
