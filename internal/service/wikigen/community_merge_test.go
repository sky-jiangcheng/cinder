package wikigen

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"repotory/internal/db"
)

// The two structural halves — merge candidate discovery and community
// detection — are the only paths in this package that read the page graph. Both
// were written to be model-free on the "free half" side and to gate the paid
// half behind an explicit switch, and neither property is visible from a
// signature: the gate is a control-flow fact (off means zero calls, not "calls
// that are discarded"), and candidate discovery is a blocking fact (pages that
// share no bucket must never be compared). These tests drive both through the
// Chat seam, so the transport is something they count rather than something they
// need.

func seedWikiPage(t *testing.T, database *sql.DB, projectID int64, slug, title, body, status string) *db.WikiPage {
	t.Helper()
	p, err := db.CreateWikiPageAs(database, db.WikiKindConcept, slug, title, projectID, body, status, "ai")
	if err != nil {
		t.Fatalf("create page %s: %v", slug, err)
	}
	return p
}

func linkWikiPages(t *testing.T, database *sql.DB, from, to int64) {
	t.Helper()
	if _, err := database.Exec(
		`INSERT INTO page_links (from_page_id, to_page_id, relation) VALUES (?, ?, ?)`,
		from, to, db.RelationRef,
	); err != nil {
		t.Fatalf("link %d->%d: %v", from, to, err)
	}
}

// A pair that shares a bucket must surface, and the report must count the
// denominator honestly (Possible is every pair, Candidates is what was
// actually compared).
func TestFindMergeCandidatesSurfacesSimilarSlugs(t *testing.T) {
	database := setupKernelDB(t)
	projectID := seedProject(t, database)
	seedWikiPage(t, database, projectID, "payment-gateway", "Payment Gateway", "gateway body", db.WikiStatusApproved)
	seedWikiPage(t, database, projectID, "payment-gateway-v2", "Payment Gateway V2", "gateway v2 body", db.WikiStatusApproved)
	// A page sharing no token and no slug prefix: it must not be paired with the
	// two above.
	seedWikiPage(t, database, projectID, "quota-refresh", "Quota Refresh", "quota body", db.WikiStatusApproved)

	report, err := FindMergeCandidates(database, projectID, 0)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if report.Pages != 3 {
		t.Fatalf("pages = %d, want 3", report.Pages)
	}
	if report.Possible != 3*2/2 {
		t.Fatalf("possible = %d, want 3", report.Possible)
	}
	if len(report.Candidates) != 1 {
		t.Fatalf("candidates = %d, want exactly the gateway pair: %+v", len(report.Candidates), report.Candidates)
	}
	pair := report.Candidates[0]
	if !strings.Contains(pair.WinnerSlug+pair.LoserSlug, "payment-gateway") {
		t.Fatalf("candidate is not the gateway pair: %+v", pair)
	}
}

// A library of one page has nothing to compare, and the report must still be
// usable: an empty slice, not nil, so the panel can map over it.
func TestFindMergeCandidatesSinglePageIsEmptySlice(t *testing.T) {
	database := setupKernelDB(t)
	projectID := seedProject(t, database)
	seedWikiPage(t, database, projectID, "only-page", "Only Page", "body", db.WikiStatusApproved)

	report, err := FindMergeCandidates(database, projectID, 0)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if report.Candidates == nil {
		t.Fatal("candidates must be an empty slice, not nil")
	}
	if len(report.Candidates) != 0 || report.Possible != 0 {
		t.Fatalf("candidates=%d possible=%d, want 0/0", len(report.Candidates), report.Possible)
	}
}

// Three approved pages wired in a triangle: one community of size 3, which is
// the default minimum. With the consent flag off the run must stop after the
// free half — the report says what it found and the model is never asked.
func TestRunCommunitySummaryGateOffNeverCallsTheModel(t *testing.T) {
	database := setupKernelDB(t)
	projectID := seedProject(t, database)
	a := seedWikiPage(t, database, projectID, "alpha", "Alpha", "alpha body", db.WikiStatusApproved)
	b := seedWikiPage(t, database, projectID, "beta", "Beta", "beta body", db.WikiStatusApproved)
	c := seedWikiPage(t, database, projectID, "gamma", "Gamma", "gamma body", db.WikiStatusApproved)
	linkWikiPages(t, database, a.ID, b.ID)
	linkWikiPages(t, database, b.ID, c.ID)
	linkWikiPages(t, database, c.ID, a.ID)

	chat := &countingChat{available: true}
	report, err := RunCommunitySummary(context.Background(), database, chat, projectID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.ApprovedPages != 3 {
		t.Fatalf("approved pages = %d, want 3", report.ApprovedPages)
	}
	if report.CommunitiesFound != 1 {
		t.Fatalf("communities = %d, want 1", report.CommunitiesFound)
	}
	if !report.CommunityGateOff {
		t.Fatal("report must say the gate is off, or the user cannot tell why nothing was written")
	}
	if chat.calls != 0 {
		t.Fatalf("gate off triggered %d model calls, want 0", chat.calls)
	}
	if report.PagesCreated != 0 {
		t.Fatalf("gate off created %d pages, want 0", report.PagesCreated)
	}
}

// With the flag on, each community files one pending synthesis page whose slug
// is the stable identity; a re-run updates that page in place instead of filing
// a second one.
func TestRunCommunitySummaryCreatesThenUpdatesTheSynthesisPage(t *testing.T) {
	database := setupKernelDB(t)
	projectID := seedProject(t, database)
	a := seedWikiPage(t, database, projectID, "alpha", "Alpha", "alpha body", db.WikiStatusApproved)
	b := seedWikiPage(t, database, projectID, "beta", "Beta", "beta body", db.WikiStatusApproved)
	c := seedWikiPage(t, database, projectID, "gamma", "Gamma", "gamma body", db.WikiStatusApproved)
	linkWikiPages(t, database, a.ID, b.ID)
	linkWikiPages(t, database, b.ID, c.ID)
	linkWikiPages(t, database, c.ID, a.ID)

	if err := db.SetConfig(database, KeyCommunity, "1"); err != nil {
		t.Fatal(err)
	}
	chat := &countingChat{available: true}

	first, err := RunCommunitySummary(context.Background(), database, chat, projectID)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if first.CommunityGateOff {
		t.Fatal("gate is on, report must not claim it is off")
	}
	if first.PagesCreated != 1 {
		t.Fatalf("created = %d, want 1", first.PagesCreated)
	}
	if first.Calls != 1 {
		t.Fatalf("calls = %d, want 1", first.Calls)
	}

	members := []string{"alpha", "beta", "gamma"}
	slug := CommunitySynthesisSlug(members)
	page, err := db.GetWikiPageBySlug(database, slug)
	if err != nil {
		t.Fatalf("synthesis page %s missing: %v", slug, err)
	}
	if page.Kind != db.WikiKindSynthesis {
		t.Fatalf("kind = %q, want %q", page.Kind, db.WikiKindSynthesis)
	}
	if page.Status != db.WikiStatusPending {
		t.Fatalf("a synthesis must land pending for review, got %q", page.Status)
	}
	if page.ProjectID != projectID {
		t.Fatalf("project = %d, want %d", page.ProjectID, projectID)
	}

	second, err := RunCommunitySummary(context.Background(), database, chat, projectID)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.PagesUpdated != 1 || second.PagesCreated != 0 {
		t.Fatalf("second run created=%d updated=%d, want 0/1", second.PagesCreated, second.PagesUpdated)
	}
	again, err := db.GetWikiPageBySlug(database, slug)
	if err != nil {
		t.Fatalf("lookup after re-run: %v", err)
	}
	if again.ID != page.ID {
		t.Fatalf("re-run filed a second page (%d vs %d); the slug is the identity", again.ID, page.ID)
	}
}

// Approved pages are never rewritten by an automated path (决策 7): an approved
// synthesis becomes a revision todo and its body is left exactly as a human
// approved it.
func TestRunCommunitySummaryLeavesApprovedSynthesisAlone(t *testing.T) {
	database := setupKernelDB(t)
	projectID := seedProject(t, database)
	a := seedWikiPage(t, database, projectID, "alpha", "Alpha", "alpha body", db.WikiStatusApproved)
	b := seedWikiPage(t, database, projectID, "beta", "Beta", "beta body", db.WikiStatusApproved)
	c := seedWikiPage(t, database, projectID, "gamma", "Gamma", "gamma body", db.WikiStatusApproved)
	linkWikiPages(t, database, a.ID, b.ID)
	linkWikiPages(t, database, b.ID, c.ID)
	linkWikiPages(t, database, c.ID, a.ID)

	if err := db.SetConfig(database, KeyCommunity, "1"); err != nil {
		t.Fatal(err)
	}
	slug := CommunitySynthesisSlug([]string{"alpha", "beta", "gamma"})
	approved := seedWikiPage(t, database, projectID, slug,
		CommunitySynthesisTitle([]string{"alpha", "beta", "gamma"}),
		"human approved text", db.WikiStatusApproved)

	chat := &countingChat{available: true}
	report, err := RunCommunitySummary(context.Background(), database, chat, projectID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.RevisionTodos != 1 {
		t.Fatalf("revision todos = %d, want 1", report.RevisionTodos)
	}
	if chat.calls != 0 {
		t.Fatalf("an approved synthesis must not spend a model call, got %d", chat.calls)
	}
	after, err := db.GetWikiPageBySlug(database, slug)
	if err != nil {
		t.Fatal(err)
	}
	if after.Content != approved.Content {
		t.Fatalf("approved body was rewritten: %q", after.Content)
	}
}
