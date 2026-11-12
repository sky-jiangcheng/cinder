package wikipage

import (
	"database/sql"
	"strings"
	"testing"

	"repotory/internal/db"
)

// The read side touches nothing but the database, so every test here builds one
// and calls a function — no Service, no git fixture, no model. That property is
// the point of the extraction (ADR-0060 step two): if a future change makes one
// of these need something else, this file is where it will fail first.

func setupWikiDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func mustPage(t *testing.T, database *sql.DB, slug, content string, status string) *db.WikiPage {
	t.Helper()
	p, err := db.CreateWikiPageAs(database, "concept", slug, slug, 1, content, status, "ai")
	if err != nil {
		t.Fatalf("create page %s: %v", slug, err)
	}
	return p
}

// The browse feed must emit [] rather than nil: the panel maps over the result
// directly, and a nil slice serialises to JSON null.
func TestBrowseEmptyFeedIsEmptySlice(t *testing.T) {
	database := setupWikiDB(t)
	out, err := BrowseWikiPages(database, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		t.Fatal("empty feed must be an empty slice, not nil")
	}
	if len(out) != 0 {
		t.Fatalf("want 0 rows, got %d", len(out))
	}
}

// Only approved pages are readable from here: the gate decides what may be READ
// as much as what may be cited.
func TestGetWikiPageRefusesPending(t *testing.T) {
	database := setupWikiDB(t)
	p := mustPage(t, database, "draft", "body", db.WikiStatusPending)
	if _, err := GetWikiPage(database, p.ID); err == nil {
		t.Fatal("a pending page must not be readable from the browse surface")
	}
}

func TestGetWikiPageApprovedCarriesGraphAndSources(t *testing.T) {
	database := setupWikiDB(t)
	p := mustPage(t, database, "live", "body", db.WikiStatusApproved)
	got, err := GetWikiPage(database, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Nil slices would serialise to null and break the panel's `.map`.
	if got.OutLinks == nil || got.InLinks == nil || got.SourceNoteIDs == nil {
		t.Fatalf("graph arrays must be non-nil: %+v", got)
	}
	if got.Status != db.WikiStatusApproved {
		t.Errorf("status = %q", got.Status)
	}
}

// "No history yet" is a fact about the page, not a failure — and it must not be
// null either.
func TestListWikiPageVersionsEmptyIsEmptySlice(t *testing.T) {
	database := setupWikiDB(t)
	p := mustPage(t, database, "fresh", "body", db.WikiStatusApproved)
	vers, err := ListWikiPageVersions(database, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if vers == nil {
		t.Fatal("want an empty slice, got nil")
	}
}

// A CJK snippet cut on a byte boundary renders as a replacement glyph, which
// reads as corrupted knowledge. This pins the rune-based window.
func TestPageSnippetIsRuneSafe(t *testing.T) {
	content := strings.Repeat("中文内容", 100)
	out := pageSnippet(content, "")
	if !strings.HasPrefix(out, "中文内容") {
		t.Fatalf("snippet must start on a whole rune: %q", out)
	}
	for _, r := range out {
		if r == '\uFFFD' {
			t.Fatalf("snippet contains a replacement glyph: %q", out)
		}
	}
}

// The promotion gate is a human act (ADR-0015): a page that was never published
// does not get a scope, it gets a review.
func TestScopeRefusesNonApprovedPages(t *testing.T) {
	database := setupWikiDB(t)
	pending := mustPage(t, database, "pending-one", "body", db.WikiStatusPending)
	if err := PromoteWikiPageToGlobal(database, pending.ID); err == nil {
		t.Fatal("a pending page must not be promoted")
	}
	if err := AssignWikiPageProject(database, pending.ID, 2); err == nil {
		t.Fatal("a pending page must not be reassigned")
	}
}

// Promoting twice is not an error: the same human action retried must not look
// like a failure.
func TestPromoteIsIdempotent(t *testing.T) {
	database := setupWikiDB(t)
	p := mustPage(t, database, "to-global", "body", db.WikiStatusApproved)
	if err := PromoteWikiPageToGlobal(database, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := PromoteWikiPageToGlobal(database, p.ID); err != nil {
		t.Fatalf("second promotion must be a no-op, got %v", err)
	}
	after, err := db.GetWikiPageByID(database, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ProjectID != 0 {
		t.Errorf("project_id = %d, want 0 (global)", after.ProjectID)
	}
}

func TestExportRequiresADirectory(t *testing.T) {
	database := setupWikiDB(t)
	if _, err := ExportWikiTree(database, "  ", 0, false); err == nil {
		t.Fatal("an empty export directory must be refused")
	}
}
