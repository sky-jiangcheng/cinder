package service

import (
	"context"
	"fmt"
	"log"
	"strings"

	"repotory/internal/db"
	"repotory/internal/service/wikigen"
	"repotory/internal/service/wikipage"
)

// Ingest-time compilation (ADR-0015/0016). The kernel — the model call, the
// pending-only write discipline, the budgets, the JSON parsing — now lives in
// internal/service/wikigen (ADR-0060 step three). What stays here is the facade
// (CompileNote / CompileProjectNotes keep their names and signatures, so the
// Wails bindings, the RPC bridge and every existing test are unchanged) plus the
// human-action lifecycle below: approve / reject / delete and the review feeds
// have no model call, so under the split's own rule there is nothing to inject
// and nothing to move yet.

// WikiCompileReport is an alias, not a copy: its snake_case json tags cross the
// Wails binding to the frontend and the RPC bridge to a browser, and an alias
// keeps service.WikiCompileReport meaning the same type at every call site that
// already names it.
type WikiCompileReport = wikigen.WikiCompileReport

// Compat aliases for the vocabulary the write side moved to wikigen: the config
// key, the source tag and the per-note budget that this package's remaining
// readers and its tests still name. These are const aliases, not copies — the
// compiler fixes the value, so the two names cannot drift apart.
const (
	wikiCompileKey         = wikigen.KeyCompile
	compileSourceTag       = wikigen.CompileSourceTag
	compileMaxNotes        = wikigen.CompileMaxNotes
	compileMaxPagesPerNote = wikigen.CompileMaxPagesPerNote
)

// wikiCompileEnabled delegates to the kernel's gate so the config key has one
// reader. The job submit path (which stayed here) still asks this.
func (s *Service) wikiCompileEnabled() bool { return wikigen.CompileEnabled(s.db) }

// CompileNote runs the loop for one note. It is the unit the UI exposes, because
// "compile this one thing and let me look at it" is the smallest honest step.
// The database and the audited chat seam are the kernel's only dependencies.
func (s *Service) CompileNote(noteID int64) (*WikiCompileReport, error) {
	return wikigen.CompileNote(context.Background(), s.db, wikigenChat{s}, noteID)
}

// CompileProjectNotes runs over a project's notes under one shared budget. It is
// still a human pressing a button once per call, not a scheduled job.
func (s *Service) CompileProjectNotes(projectID int64, maxNotes int) (*WikiCompileReport, error) {
	return wikigen.CompileProjectNotes(context.Background(), s.db, wikigenChat{s}, projectID, maxNotes)
}

// ListPendingWikiPages is the review surface's feed of unreviewed compiler output:
// each entry carries its provenance so a reviewer can compare before approving.
func (s *Service) ListPendingWikiPages(projectID int64) ([]WikiPendingPage, error) {
	return s.wikiReviewFeed(db.WikiStatusPending, projectID)
}

// ListRejectedWikiPages returns the error-page tombstones (ADR-0015 review flow:
// an unqualified local-model result is REPLACED by an error page, not deleted).
// They stay in the graph so inbound [[links]] keep resolving — the alternative
// (delete) silently dangles every page that cited the rejected one.
func (s *Service) ListRejectedWikiPages(projectID int64) ([]WikiPendingPage, error) {
	return s.wikiReviewFeed(db.WikiStatusRejected, projectID)
}

// wikiReviewFeed is the single projection behind both review feeds. One
// implementation, because pending and rejected differ only by status and the UI
// must not get two subtly different shapes for the same kind of row.
func (s *Service) wikiReviewFeed(status string, projectID int64) ([]WikiPendingPage, error) {
	return wikipage.ReviewFeed(s.db, status, projectID)
}

// ApproveWikiPage publishes a pending page into retrieval. This is the only
// function that may make a page answer a question, and it is reachable only from
// a human action: no code path in the compiler or lint calls it.
func (s *Service) ApproveWikiPage(pageID int64) error {
	p, err := db.GetWikiPageByID(s.db, pageID)
	if err != nil {
		return fmt.Errorf("page %d not found: %w", pageID, err)
	}
	if p.Status == db.WikiStatusApproved {
		return nil // idempotent: approving twice is not an error
	}
	if err := db.SetWikiPageStatus(s.db, pageID, db.WikiStatusApproved); err != nil {
		return err
	}
	// ADR-0018 lane 1: approval is the moment a page's body [[wikilinks]] become
	// edges. Before approval the page is unreviewed and its links are not trusted
	// to mean anything; a human publishing it is the signal that "point at what it
	// points at" is safe. Non-fatal: the page is published even if an edge cannot
	// be written, so a link failure must not look like a failed approval.
	s.extractMentions(p)
	return nil
}

// extractMentions turns the [[slug]] / [[slug|title]] links in a page body into
// `mentions` edges. It is deterministic and model-free — which is exactly why it
// is safe to run automatically, unlike anything the compiler emits. It links only
// to pages that exist (a broken link stays the lint's dangling finding, never a
// fabricated node), never to itself, and it is idempotent because LinkWikiPages
// collapses the (from, to, relation) triple.
func (s *Service) extractMentions(p *db.WikiPage) {
	seen := map[int64]bool{}
	for _, m := range wikipage.WikiLinkRe.FindAllStringSubmatch(p.Content, -1) {
		target := strings.TrimSpace(m[1])
		if target == "" {
			continue
		}
		to := s.resolveWikiLink(target)
		if to == 0 || to == p.ID || seen[to] {
			continue
		}
		seen[to] = true
		if err := db.LinkWikiPages(s.db, p.ID, to, db.RelationMentions); err != nil {
			log.Printf("wiki: could not record mention %d -> %q: %v", p.ID, target, err)
		}
	}
}

// resolveWikiLink maps a [[...]] token to a page id: the raw token as a slug
// first (that is how the export writes links), then its normalized form (how a
// hand-typed [[Title]] resolves). Returns 0 when nothing matches.
func (s *Service) resolveWikiLink(token string) int64 {
	if p, err := db.GetWikiPageBySlug(s.db, token); err == nil {
		return p.ID
	}
	if norm := db.NormalizeWikiSlug(token); norm != "" && norm != token {
		if p, err := db.GetWikiPageBySlug(s.db, norm); err == nil {
			return p.ID
		}
	}
	return 0
}

// rejectedPageBody replaces an unqualified compiler result. The row is kept so
// inbound links stay valid; only its content becomes an honest error placeholder.
const rejectedPageBody = "⚠️ 此页面由本地模型生成，经人工审核判定为不合格，内容已被替换为错误页。" +
	"引用它的页面仍能看到这个节点（链接不会失效），但它不再作为知识被检索。" +
	"如需彻底移除，请在审核界面点“彻底删除”。"

// RejectWikiPage replaces a pending page with an error page (tombstone): status
// flips to 'rejected' and the body is overwritten with an error notice. It does
// NOT delete the row — deleting would cascade away every inbound link and
// silently dangle the pages that cited it, which is worse than keeping a marked
// node. Approved pages cannot be rejected.
func (s *Service) RejectWikiPage(pageID int64) error {
	p, err := db.GetWikiPageByID(s.db, pageID)
	if err != nil {
		return fmt.Errorf("page %d not found: %w", pageID, err)
	}
	if p.Status == db.WikiStatusApproved {
		return fmt.Errorf("refusing to reject approved page %d: rejection only applies to pending pages", pageID)
	}
	if err := db.UpdateWikiPage(s.db, p.ID, p.Title, p.Kind, rejectedPageBody); err != nil {
		return fmt.Errorf("replace page %d with error page: %w", pageID, err)
	}
	return db.SetWikiPageStatus(s.db, pageID, db.WikiStatusRejected)
}

// DeleteCompiledPage permanently removes a page that was never published
// (pending or already rejected). It is the escape hatch that keeps rejected
// tombstones from accumulating forever. Approved pages are refused here too —
// destroying published knowledge must not be a side effect of cleaning the
// review queue.
func (s *Service) DeleteCompiledPage(pageID int64) error {
	p, err := db.GetWikiPageByID(s.db, pageID)
	if err != nil {
		return fmt.Errorf("page %d not found: %w", pageID, err)
	}
	if p.Status == db.WikiStatusApproved {
		return fmt.Errorf("refusing to delete approved page %d: unpublish it first", pageID)
	}
	return db.DeleteWikiPage(s.db, pageID)
}
