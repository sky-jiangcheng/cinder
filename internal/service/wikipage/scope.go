package wikipage

import (
	"database/sql"
	"fmt"
	"log"

	"repotory/internal/db"
)

// Page scope: the project tier and the global tier (ADR-0031).
//
// Why this file exists. The context stack has three layers — L1 repository
// facts, L2 "what this project is", L3 "who the user is across projects" — and
// L3 has been structurally empty since it was written: layerGlobalProfile
// (context_layers.go) only accepts pages whose project_id is 0, while every
// page-creating path (wiki_compile.go, wiki_community.go, the answer-filing
// loop) requires a project. A live library with 45 pages had 0 global ones.
//
// The obvious fix — "just summarise across projects automatically" — is the one
// ADR-0015 explicitly refuses: producing L3 automatically is exactly "the
// model writes into the store with no gate". So the move has to be a human
// act, and this file is that act.
//
// The rules, and why each one:
//
//   - Only an APPROVED page may change scope. A pending page in the global tier
//     would be invisible to every project-scoped review feed (the pending and
//     rejected queries filter on project_id), i.e. a page nobody could ever
//     approve — an orphan. A rejected page is a tombstone and must not speak
//     at all.
//   - The scope move never touches status, content or the page's source: it
//     changes where a page may be seen, not what it says. Approval stays the
//     separate, irreversible-in-spirit human decision ADR-0015 is about.
//   - Promoting is idempotent: the same page promoted twice is not an error.
//     The reverse move needs an explicit target project, because "put it back
//     where it was" is not knowable once project_id is NULL.

// ListApprovedWikiPages is the scope surface's feed: the approved pages whose
// scope a human may widen or narrow. It reuses the review feed projection on
// purpose — pending, rejected and approved rows must look identical in the UI,
// otherwise "approve" and "promote" would be two different row shapes.
//
// The project filter keeps its existing meaning: a project id narrows to that
// project's pages, and 0 means "every page, global ones included" — a global
// page belongs to no project, so it only shows in the unfiltered view.
func ListApprovedWikiPages(database *sql.DB, projectID int64) ([]WikiPendingPage, error) {
	return ReviewFeed(database, db.WikiStatusApproved, projectID)
}

// PromoteWikiPageToGlobal moves an approved page into the global tier: it
// enters the L3 cross-project profile, and it becomes visible to every
// project's evidence gathering (the evidence path already treats project_id 0
// as "visible everywhere", wiki_evidence.go).
//
// This is the only way a page reaches the global tier. Nothing in the
// compiler, the linter or the community synthesis may call it — the global
// tier is a curated statement about how the user works, and a model writing it
// is the failure ADR-0015 exists to prevent.
func PromoteWikiPageToGlobal(database *sql.DB, pageID int64) error {
	p, err := db.GetWikiPageByID(database, pageID)
	if err != nil {
		return fmt.Errorf("page %d not found: %w", pageID, err)
	}
	if p.Status != db.WikiStatusApproved {
		return fmt.Errorf("refusing to promote page %d: only approved pages may enter the global tier "+
			"(page %d is %q)", pageID, pageID, p.Status)
	}
	if p.ProjectID == 0 {
		return nil // already global: idempotent
	}
	if err := db.SetWikiPageScope(database, pageID, 0); err != nil {
		return fmt.Errorf("promote page %d to global: %w", pageID, err)
	}
	log.Printf("wiki: page %d (%s) promoted from project %d to the global tier", pageID, p.Slug, p.ProjectID)
	return nil
}

// AssignWikiPageProject moves a page back into a project tier, the inverse of
// PromoteWikiPageToGlobal. It is what makes the promotion reversible: once a
// page is global, the project it came from is not knowable from the row, so
// the human names the target explicitly.
//
// The same approval rule applies — a page that was never published does not
// get a scope, it gets a review.
func AssignWikiPageProject(database *sql.DB, pageID int64, projectID int64) error {
	if projectID <= 0 {
		return fmt.Errorf("refusing to assign page %d: project id %d is not a project", pageID, projectID)
	}
	p, err := db.GetWikiPageByID(database, pageID)
	if err != nil {
		return fmt.Errorf("page %d not found: %w", pageID, err)
	}
	if p.Status != db.WikiStatusApproved {
		return fmt.Errorf("refusing to reassign page %d: only approved pages may change scope "+
			"(page %d is %q)", pageID, pageID, p.Status)
	}
	if p.ProjectID == projectID {
		return nil // already there: idempotent
	}
	if err := db.SetWikiPageScope(database, pageID, projectID); err != nil {
		return fmt.Errorf("assign page %d to project %d: %w", pageID, projectID, err)
	}
	log.Printf("wiki: page %d (%s) moved from scope %d to project %d", pageID, p.Slug, p.ProjectID, projectID)
	return nil
}
