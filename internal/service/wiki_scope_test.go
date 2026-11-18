package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"repotory/internal/db"
)

// Page scope (ADR-0031): promoting an approved page into the global tier is
// the only way L3 — the cross-project profile — ever says anything.
//
// These tests pin the three properties the feature promises:
//  1. the promotion fills L3 (the layer was structurally empty before it);
//  2. the promoted page is still retrievable, and is now visible to projects
//     it did not belong to (a widening, not a move);
//  3. the whole thing is reversible, and never reachable without a human.

func scopeService(t *testing.T) (*Service, int64) {
	t.Helper()
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "scope-project", "/tmp/scope-project")
	return svc, pid
}

func scopePage(t *testing.T, svc *Service, pid int64, slug, content, status string) *db.WikiPage {
	t.Helper()
	page, err := db.CreateWikiPageAs(svc.db, db.WikiKindConcept, slug, slug, pid, content, status, "manual")
	if err != nil {
		t.Fatal(err)
	}
	return page
}

// globalProfileItems reports how many entries L3 currently carries. Zero means
// the layer is silent — the state every real library was in before ADR-0031.
func globalProfileItems(t *testing.T, svc *Service, projectID int64) int {
	t.Helper()
	m := svc.BuildLayeredMemory(context.Background(), projectID, "", LayerBudget{}, false)
	for _, blk := range m.Blocks {
		if blk.Layer == LayerGlobalProfile {
			return blk.Items
		}
	}
	return 0
}

// The acceptance bar: a promoted page makes the L3 layer say something.
func TestPromoteWikiPageToGlobal_FillsTheL3GlobalProfile(t *testing.T) {
	svc, pid := scopeService(t)
	page := scopePage(t, svc, pid, "globalprofile", "这一页描述跨项目的工作方式。", db.WikiStatusApproved)

	if items := globalProfileItems(t, svc, pid); items != 0 {
		t.Fatalf("L3 must be empty before the promotion, got %d items", items)
	}

	if err := svc.PromoteWikiPageToGlobal(page.ID); err != nil {
		t.Fatalf("PromoteWikiPageToGlobal: %v", err)
	}

	if items := globalProfileItems(t, svc, pid); items != 1 {
		t.Fatalf("L3 must carry the promoted page, got %d items", items)
	}
}

// The promoted page must remain findable — a page that fell out of retrieval
// would be a silent loss, and retrieval is the reason the page exists.
func TestPromoteWikiPageToGlobal_KeepsThePageRetrievable(t *testing.T) {
	svc, pid := scopeService(t)
	page := scopePage(t, svc, pid, "globalprofile", "the global profile token", db.WikiStatusApproved)

	if err := svc.PromoteWikiPageToGlobal(page.ID); err != nil {
		t.Fatalf("PromoteWikiPageToGlobal: %v", err)
	}

	hits, err := db.SearchWikiPages(svc.db, "globalprofile", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("the promoted page disappeared from retrieval")
	}
	found := false
	for _, h := range hits {
		if h.ID == page.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("retrieval did not return the promoted page: %+v", hits)
	}
}

// The promotion WIDENS the page's reach: before it, the page belonged to one
// project and was invisible to the others; after it, it is visible everywhere.
// That asymmetry is the whole value of the global tier — and it is why the
// scope change must stay a human act.
func TestPromoteWikiPageToGlobal_WidensVisibilityToEveryProject(t *testing.T) {
	svc, pid := scopeService(t)
	other := seedProject(t, svc.db, "other-project", "/tmp/other-project")
	page := scopePage(t, svc, pid, "globalprofile", "the global profile token", db.WikiStatusApproved)

	if visible := evidenceHasPage(svc, other, page.ID); visible {
		t.Error("a project-scoped page must not be visible to another project")
	}

	if err := svc.PromoteWikiPageToGlobal(page.ID); err != nil {
		t.Fatalf("PromoteWikiPageToGlobal: %v", err)
	}

	if !evidenceHasPage(svc, other, page.ID) {
		t.Error("a global page must be visible to every project's evidence gathering")
	}
}

// evidenceHasPage asks the production evidence path whether a page shows up
// for a project. It calls the same GatherEvidence entry point the answer
// panel uses, so the test measures what a user would actually see.
func evidenceHasPage(svc *Service, projectID, pageID int64) bool {
	ev := svc.GatherEvidence(context.Background(), projectID, "globalprofile", EvidenceBudget{MaxItems: 20, Deadline: 2 * time.Second})
	for _, item := range ev.Items {
		if item.Type == "page" && item.ID == pageID {
			return true
		}
	}
	return false
}

// A page nobody approved may not enter the global tier: the tier is what
// steers every answer, and ADR-0015's whole point is that unreviewed content
// never does.
func TestPromoteWikiPageToGlobal_RefusesUnapprovedPages(t *testing.T) {
	svc, pid := scopeService(t)
	pending := scopePage(t, svc, pid, "pendingpage", "pending content", db.WikiStatusPending)

	if err := svc.PromoteWikiPageToGlobal(pending.ID); err == nil {
		t.Fatal("a pending page must not be promotable")
	}

	rejected := scopePage(t, svc, pid, "rejectedpage", "rejected content", db.WikiStatusRejected)
	if err := svc.PromoteWikiPageToGlobal(rejected.ID); err == nil {
		t.Fatal("a rejected page must not be promotable")
	}

	if items := globalProfileItems(t, svc, pid); items != 0 {
		t.Errorf("L3 must stay empty after a refused promotion, got %d items", items)
	}
}

// Promoting twice is not an error: the button is a click, and a double click
// must not produce an error toast for work that is already done.
func TestPromoteWikiPageToGlobal_IsIdempotent(t *testing.T) {
	svc, pid := scopeService(t)
	page := scopePage(t, svc, pid, "globalprofile", "body", db.WikiStatusApproved)

	if err := svc.PromoteWikiPageToGlobal(page.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.PromoteWikiPageToGlobal(page.ID); err != nil {
		t.Fatalf("the second promotion must be a no-op, got %v", err)
	}
	if items := globalProfileItems(t, svc, pid); items != 1 {
		t.Errorf("L3 must still carry exactly one entry, got %d", items)
	}
}

// The reverse move exists, because a one-way ratchet is not a feature: the
// page goes back to its project and L3 goes quiet again.
func TestAssignWikiPageProject_ReversesThePromotion(t *testing.T) {
	svc, pid := scopeService(t)
	page := scopePage(t, svc, pid, "globalprofile", "body", db.WikiStatusApproved)

	if err := svc.PromoteWikiPageToGlobal(page.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.AssignWikiPageProject(page.ID, pid); err != nil {
		t.Fatalf("AssignWikiPageProject: %v", err)
	}

	if items := globalProfileItems(t, svc, pid); items != 0 {
		t.Errorf("L3 must be empty again after the page went back, got %d items", items)
	}
	got, err := db.GetWikiPageByID(svc.db, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProjectID != pid {
		t.Errorf("page ProjectID = %d, want %d", got.ProjectID, pid)
	}
}

// The target project must exist, and it must be a project (0 is the global
// tier, not a project). Both failures are reported, not silently ignored.
func TestAssignWikiPageProject_RejectsBadTargets(t *testing.T) {
	svc, pid := scopeService(t)
	page := scopePage(t, svc, pid, "globalprofile", "body", db.WikiStatusApproved)

	if err := svc.AssignWikiPageProject(page.ID, 0); err == nil {
		t.Error("project id 0 is the global tier, not a project")
	}
	if err := svc.AssignWikiPageProject(page.ID, 4242); err == nil {
		t.Error("a project that does not exist must be refused")
	}

	pending := scopePage(t, svc, pid, "pendingpage", "pending", db.WikiStatusPending)
	if err := svc.AssignWikiPageProject(pending.ID, pid); err == nil {
		t.Error("an unapproved page must not change scope")
	}
}

// The scope surface feeds the review page, and it must show approved pages —
// including the global ones, which is the only way a user can find a page
// they promoted and move it back.
func TestListApprovedWikiPages_ShowsProjectAndGlobalPages(t *testing.T) {
	svc, pid := scopeService(t)
	projectPage := scopePage(t, svc, pid, "projectpage", "body", db.WikiStatusApproved)
	globalPage := scopePage(t, svc, pid, "globalpage", "body", db.WikiStatusApproved)
	pending := scopePage(t, svc, pid, "pendingpage", "body", db.WikiStatusPending)

	if err := svc.PromoteWikiPageToGlobal(globalPage.ID); err != nil {
		t.Fatal(err)
	}

	all, err := svc.ListApprovedWikiPages(0)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[int64]bool{}
	for _, item := range all {
		ids[item.WikiPage.ID] = true
	}
	if !ids[projectPage.ID] || !ids[globalPage.ID] {
		t.Errorf("the unfiltered scope feed must carry both the project page and the global page, got %+v", ids)
	}
	if ids[pending.ID] {
		t.Error("the scope feed must not carry unapproved pages")
	}

	// A project filter narrows to that project's pages. A global page belongs to
	// no project, so it shows up in the unfiltered view only.
	scoped, err := svc.ListApprovedWikiPages(pid)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range scoped {
		if item.WikiPage.ID == globalPage.ID {
			t.Error("the global page must not appear under a project filter")
		}
	}
	if len(scoped) != 1 || scoped[0].WikiPage.ID != projectPage.ID {
		t.Errorf("the project-filtered scope feed = %d entries, want the one project page", len(scoped))
	}
}

// A page that does not exist must be an error on both moves — a silent no-op
// would let the UI claim a scope change that never happened.
func TestScopeMoves_ReportMissingPages(t *testing.T) {
	svc, _ := scopeService(t)
	if err := svc.PromoteWikiPageToGlobal(9999); err == nil {
		t.Error("promoting a missing page must fail")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Errorf("the error should say the page was not found, got %v", err)
	}
	if err := svc.AssignWikiPageProject(9999, 1); err == nil {
		t.Error("assigning a missing page must fail")
	}
}
