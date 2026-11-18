package wikipage

import (
	"database/sql"
	"testing"

	"repotory/internal/db"
)

func feedDB(t *testing.T) (*sql.DB, int64) {
	t.Helper()
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	res, err := database.Exec(
		`INSERT INTO projects (name, root_path, project_kind) VALUES ('feed', '/tmp/feed', 'scanned')`,
	)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("project id: %v", err)
	}
	return database, id
}

// The review feed is the ONE projection three surfaces render. If it dropped
// provenance or edges, "approve" and "promote" would show the same page as two
// different shapes — the drift the comment on ReviewFeed exists to prevent.
func TestReviewFeedCarriesProvenanceAndEdges(t *testing.T) {
	database, projectID := feedDB(t)
	note, err := db.CreateNoteEx(database, projectID, "Source", "the note behind the page", "", "other", "manual")
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	from, err := db.CreateWikiPageAs(database, "concept", "gateway-routing", "Gateway Routing",
		projectID, "body", db.WikiStatusPending, "wiki-compile")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}
	to, err := db.CreateWikiPageAs(database, "concept", "routing-table", "Routing Table",
		projectID, "body", db.WikiStatusPending, "wiki-compile")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}
	if err := db.AttachNoteToPage(database, note.ID, from.ID); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := db.LinkWikiPages(database, from.ID, to.ID, "ref"); err != nil {
		t.Fatalf("link: %v", err)
	}
	// One approved page must not leak into the pending feed.
	if _, err := db.CreateWikiPageAs(database, "concept", "approved-one", "Approved One",
		projectID, "body", db.WikiStatusApproved, "manual"); err != nil {
		t.Fatalf("create approved page: %v", err)
	}

	items, err := ReviewFeed(database, db.WikiStatusPending, projectID)
	if err != nil {
		t.Fatalf("review feed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items=%d, want the 2 pending pages only", len(items))
	}
	var found *WikiPendingPage
	for i := range items {
		if items[i].WikiPage.Slug == "gateway-routing" {
			found = &items[i]
		}
	}
	if found == nil {
		t.Fatal("the pending page is missing from its own feed")
	}
	if len(found.Sources) != 1 || found.Sources[0] != note.ID {
		t.Fatalf("sources=%v, want the note it came from", found.Sources)
	}
	if len(found.OutLinks) != 1 || found.OutLinks[0].Slug != "routing-table" {
		t.Fatalf("out_links=%v, want the edge to routing-table", found.OutLinks)
	}
	// The reverse edge is the other row's business: a page's own inbound list
	// must stay empty here, or every row would claim to cite itself.
	if len(found.InBacks) != 0 {
		t.Fatalf("in_links=%v, want none for the from-page", found.InBacks)
	}
	for i := range items {
		if items[i].WikiPage.Slug == "routing-table" && len(items[i].InBacks) != 1 {
			t.Fatalf("the target page has %d backlinks, want 1", len(items[i].InBacks))
		}
	}
}

// The feed is a projection, not a filter: an empty status is an empty list,
// never null (the JSON contract every surface relies on).
func TestReviewFeedEmptyIsSlice(t *testing.T) {
	database, projectID := feedDB(t)
	items, err := ReviewFeed(database, db.WikiStatusPending, projectID)
	if err != nil {
		t.Fatalf("review feed: %v", err)
	}
	if items == nil {
		t.Fatal("empty feed returned nil; JSON would read as null")
	}
	if len(items) != 0 {
		t.Fatalf("items=%d, want 0", len(items))
	}
}
