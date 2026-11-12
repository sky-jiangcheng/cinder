package main

import (
	"encoding/json"
	"strings"
	"testing"

	"repotory/internal/bounds"
	"repotory/internal/db"
)

// The agent-facing surface of the approved knowledge layer (ADR-0032 决策 5).
//
// Two properties are worth pinning here, and they are different in kind:
//
//   - the gate: an unapproved page must not be reachable by ANY tool, because
//     the tools are the path an agent takes;
//   - the shape: an empty query lists the pages (the browse feed) instead of
//     failing, because "list what is published" and "search for X" are two
//     different questions and the tool has to be able to answer both.

func wikiPage(t *testing.T, ts *testServer, kind, slug string, projectID int64, content, status string) *db.WikiPage {
	t.Helper()
	page, err := db.CreateWikiPageAs(ts.db, kind, slug, slug, projectID, content, status, "manual")
	if err != nil {
		t.Fatal(err)
	}
	return page
}

// The search tool finds an approved page by its content.
func TestWikiSearchFindsApprovedPages(t *testing.T) {
	ts := newTestServer(t)
	pid := ts.seedProject("demo", "/tmp/demo")
	wikiPage(t, ts, db.WikiKindConcept, "retry-page", pid, "the retry budget of the collector", db.WikiStatusApproved)

	res := ts.call("repotory_wiki_search", map[string]any{"query": "retry"})
	var pages []map[string]any
	if err := json.Unmarshal([]byte(ts.text(res)), &pages); err != nil {
		t.Fatalf("result is not a JSON array: %v\n%s", err, ts.text(res))
	}
	if len(pages) != 1 {
		t.Fatalf("got %d pages, want 1: %s", len(pages), ts.text(res))
	}
	if slug, _ := pages[0]["slug"].(string); slug != "retry-page" {
		t.Errorf("slug = %q, want retry-page", slug)
	}
}

// An empty query lists the approved pages — the browse feed, not a failed
// search.
func TestWikiSearchWithEmptyQueryListsApprovedPages(t *testing.T) {
	ts := newTestServer(t)
	pid := ts.seedProject("demo", "/tmp/demo")
	wikiPage(t, ts, db.WikiKindConcept, "listed-page", pid, "body", db.WikiStatusApproved)
	wikiPage(t, ts, db.WikiKindConcept, "pending-page", pid, "body", db.WikiStatusPending)

	res := ts.call("repotory_wiki_search", map[string]any{"query": ""})
	var pages []map[string]any
	if err := json.Unmarshal([]byte(ts.text(res)), &pages); err != nil {
		t.Fatalf("result is not a JSON array: %v\n%s", err, ts.text(res))
	}
	if len(pages) != 1 {
		t.Fatalf("got %d pages, want 1 (the pending one must not be listed): %s", len(pages), ts.text(res))
	}
	if slug, _ := pages[0]["slug"].(string); slug != "listed-page" {
		t.Errorf("slug = %q, want listed-page", slug)
	}
}

// The gate holds on the agent surface too: a page nobody approved is not
// searchable and not readable.
func TestWikiToolsNeverExposeUnapprovedPages(t *testing.T) {
	ts := newTestServer(t)
	pid := ts.seedProject("demo", "/tmp/demo")
	pending := wikiPage(t, ts, db.WikiKindConcept, "secret-page", pid, "confidential token", db.WikiStatusPending)

	res := ts.call("repotory_wiki_search", map[string]any{"query": "confidential"})
	if strings.Contains(ts.text(res), "secret-page") {
		t.Errorf("a pending page leaked into the search result: %s", ts.text(res))
	}

	read := ts.call("repotory_wiki_read", map[string]any{"id": float64(pending.ID)})
	if !strings.Contains(ts.text(read), "error") {
		t.Errorf("reading a pending page must fail loudly, got: %s", ts.text(read))
	}
	if strings.Contains(ts.text(read), "confidential token") {
		t.Errorf("a pending page's body leaked: %s", ts.text(read))
	}
}

// Reading an approved page returns its body.
func TestWikiReadReturnsThePageBody(t *testing.T) {
	ts := newTestServer(t)
	pid := ts.seedProject("demo", "/tmp/demo")
	page := wikiPage(t, ts, db.WikiKindSynthesis, "readable-page", pid, "the compiled body", db.WikiStatusApproved)

	res := ts.call("repotory_wiki_read", map[string]any{"id": float64(page.ID)})
	payload := ts.jsonPayload(res)
	if content, _ := payload["content"].(string); content != "the compiled body" {
		t.Errorf("content = %q, want the page body", content)
	}
}

// The wiki search shares resolveProjectID with repotory_ask, so an oversized
// path must be refused here too instead of becoming a cross-project list.
func TestWikiSearchRejectsOversizedPath(t *testing.T) {
	ts := newTestServer(t)
	res := ts.call("repotory_wiki_search", map[string]any{
		"query": "retry",
		"path":  strings.Repeat("p", bounds.MaxPathLen+1),
	})
	if !res.IsError {
		t.Fatalf("oversized path must be a tool error, got a cross-project list: %s", ts.text(res))
	}
	if got := ts.text(res); !strings.Contains(got, "path too long") {
		t.Errorf("message = %q, want the length refusal", got)
	}
}
