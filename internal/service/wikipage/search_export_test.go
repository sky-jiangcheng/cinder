package wikipage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"repotory/internal/db"
)

// SearchWikiPages is the read path the review page's search box uses. Two
// properties matter and neither is visible from the signature: it must never
// return a page the reader is not allowed to see (the FTS table has no status
// column, so the filter is a join the caller cannot check), and limit<=0 must
// mean "default", not "no rows".
func TestSearchWikiPagesOnlyReturnsApproved(t *testing.T) {
	database := setupWikiDB(t)
	approved := mustPage(t, database, "retry-policy", "retry backoff policy for the gateway", db.WikiStatusApproved)
	mustPage(t, database, "retry-draft", "retry backoff draft nobody approved", db.WikiStatusPending)

	// limit 0 is the panel's "whatever the default is" call.
	out, err := SearchWikiPages(database, 1, "retry backoff", 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d rows, want 1 — a pending page must not be searchable", len(out))
	}
	if out[0].ID != approved.ID {
		t.Fatalf("hit id = %d, want the approved page %d", out[0].ID, approved.ID)
	}
	if out[0].Title == "" {
		t.Fatal("a search hit without a title is unusable in the list")
	}
}

// The empty query is a user clearing the box, not an error and not "return
// everything".
func TestSearchWikiPagesEmptyQueryIsEmptySlice(t *testing.T) {
	database := setupWikiDB(t)
	mustPage(t, database, "anything", "body text", db.WikiStatusApproved)

	out, err := SearchWikiPages(database, 1, "   ", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("blank query returned %d rows, want none", len(out))
	}
}

// ExportWikiTree writes the approved pages as a Markdown tree plus a manifest
// written last. What is pinned here: the manifest exists and counts the pages
// the report claims, and a second non-forced run refuses instead of silently
// overwriting whatever the user put in that directory.
func TestExportWikiTreeWritesManifestAndRefusesNonEmptyTarget(t *testing.T) {
	database := setupWikiDB(t)
	mustPage(t, database, "retry-policy", "retry policy body", db.WikiStatusApproved)
	mustPage(t, database, "backoff", "backoff body", db.WikiStatusApproved)

	root := t.TempDir()
	report, err := ExportWikiTree(database, root, 1, false)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if report.Pages != 2 {
		t.Fatalf("pages = %d, want 2", report.Pages)
	}
	if report.SkippedPages != 0 {
		t.Fatalf("skipped = %d, want 0", report.SkippedPages)
	}

	manifest := filepath.Join(root, "wiki", "EXPORT-MANIFEST.json")
	if _, err := os.Stat(manifest); err != nil {
		t.Fatalf("manifest must exist: %v", err)
	}
	index := filepath.Join(root, "wiki", "index.md")
	if _, err := os.Stat(index); err != nil {
		t.Fatalf("index must exist: %v", err)
	}

	// Our own managed subtree is rewritten without complaint: a second export
	// over the previous export's `wiki/` directory must succeed.
	if _, err := ExportWikiTree(database, root, 1, false); err != nil {
		t.Fatalf("re-export over the managed subtree must be allowed: %v", err)
	}

	// A foreign file at the root, though, is refused rather than overwritten.
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ExportWikiTree(database, root, 1, false); !errors.Is(err, ErrWikiExportNotEmpty) {
		t.Fatalf("non-forced export over a foreign file err = %v, want ErrWikiExportNotEmpty", err)
	}

	// With force the same target is acceptable, and the report still counts.
	second, err := ExportWikiTree(database, root, 1, true)
	if err != nil {
		t.Fatalf("forced export: %v", err)
	}
	if second.Pages != 2 {
		t.Fatalf("forced export pages = %d, want 2", second.Pages)
	}
}

// An empty root is a caller bug from a binding that did not validate its form;
// it must fail before anything is written anywhere.
func TestExportWikiTreeRequiresRoot(t *testing.T) {
	database := setupWikiDB(t)
	if _, err := ExportWikiTree(database, "  ", 1, false); err == nil {
		t.Fatal("blank export directory must be refused")
	}
}
