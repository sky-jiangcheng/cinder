package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"repotory/internal/db"
	"repotory/internal/service/wikigen"
	"repotory/internal/service/wikipage"
	"repotory/internal/service/wikireview"
)

// wikiReviewTickInterval is the poll cadence of the automatic reviewer. It is
// deliberately slower than the UI's compile poll: a model verdict costs tokens
// and the human queue is not a real-time surface. The first poll is one full
// interval away, like lint and the daily review, so opening the app never
// starts an outbound call.
const wikiReviewTickInterval = 5 * time.Minute

// startWikiReviewTicker runs the optional AI-assisted review loop. It starts
// before the plugin-runtime guard: it needs only the database and the already
// configured AI endpoint, not any importer.
func (s *Service) startWikiReviewTicker() {
	s.bgGo("wiki-review", func(ctx context.Context) {
		for {
			t := time.NewTicker(wikiReviewTickInterval)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
			t.Stop()
			s.maybeReviewPendingPages(ctx)
		}
	})
}

// maybeReviewPendingPages applies the three configured behaviours to every
// pending page. All settings are re-read each tick so a change in Settings
// takes effect without a restart.
func (s *Service) maybeReviewPendingPages(ctx context.Context) {
	cfg, err := wikireview.GetConfig(s.db)
	if err != nil {
		log.Printf("wiki review: read config: %v", err)
		return
	}
	if !cfg.Enabled() {
		return
	}

	stale := map[int64]bool{}
	if cfg.TimeoutHours > 0 {
		cutoff := time.Now().Add(-time.Duration(cfg.TimeoutHours) * time.Hour).UTC().Format("2006-01-02 15:04:05")
		ids, err := db.ListStalePendingWikiPageIDs(s.db, cutoff)
		if err != nil {
			log.Printf("wiki review: list stale pages: %v", err)
			return
		}
		for _, id := range ids {
			stale[id] = true
		}
	}

	pages, err := db.ListWikiPagesByStatus(s.db, db.WikiStatusPending, 0)
	if err != nil {
		log.Printf("wiki review: list pending pages: %v", err)
		return
	}
	for i := range pages {
		if ctx.Err() != nil {
			return
		}
		s.reviewOnePendingPage(ctx, wikigenChat{s}, &pages[i], cfg, stale[pages[i].ID])
	}
}

// reviewOnePendingPage is one page's decision. Ordering matters:
//  1. a hard rule veto always wins, including over the timeout;
//  2. timeout publishes without a model verdict (AI may be down, but the
//     human window has closed) as long as the rules pass;
//  3. otherwise the model annotates the page, and auto-approval requires an
//     explicit approve from that model.
//
// A page that already carries a verdict is never re-judged: the ticker would
// otherwise send the same body to the model every five minutes until a human
// clicks something.
func (s *Service) reviewOnePendingPage(ctx context.Context, chat wikigen.Chat, page *db.WikiPage, cfg wikireview.Config, timedOut bool) {
	// Re-read before deciding: a human may have approved or rejected the page
	// while this pass was listing the queue, and an older verdict must not be
	// re-sent to the model on the next tick.
	p, err := db.GetWikiPageByID(s.db, page.ID)
	if err != nil || p.Status != db.WikiStatusPending {
		return
	}
	if veto := wikireview.Veto(p); veto != "" {
		if p.ReviewVerdict != wikireview.VerdictReject || p.ReviewSource != wikireview.SourceRule {
			_ = db.UpdateWikiReview(s.db, p.ID, wikireview.VerdictReject, veto, wikireview.SourceRule)
		}
		return
	}
	if timedOut {
		s.autoApprovePage(p, wikireview.SourceTimeout,
			fmt.Sprintf("人工审核超过 %d 小时未处理，规则检查通过，自动通过", cfg.TimeoutHours))
		return
	}
	if !cfg.AI || p.ReviewVerdict != "" {
		return
	}

	verdict, err := wikireview.Judge(ctx, chat, p)
	if err != nil {
		log.Printf("wiki review: page %d: %v", p.ID, err)
		_ = db.UpdateWikiReview(s.db, p.ID, wikireview.VerdictError,
			wikipage.ClipRunes(err.Error(), 240), wikireview.SourceAI)
		return
	}
	if err := db.UpdateWikiReview(s.db, p.ID, verdictOf(verdict.Pass), verdict.Reason, wikireview.SourceAI); err != nil {
		log.Printf("wiki review: store verdict for page %d: %v", p.ID, err)
		return
	}
	if cfg.AutoApprove && verdict.Pass {
		s.autoApprovePage(p, wikireview.SourceAuto, "AI 建议通过，规则检查通过，自动批准："+verdict.Reason)
	}
}

func verdictOf(pass bool) string {
	if pass {
		return wikireview.VerdictApprove
	}
	return wikireview.VerdictReject
}

// autoApprovePage is the only new path into retrieval. It re-checks the status
// so a human decision made while the model was answering wins, then goes
// through the same ApproveWikiPage as the UI — mentions and the disk mirror
// are therefore identical for manual and automatic approvals.
func (s *Service) autoApprovePage(p *db.WikiPage, source, reason string) {
	current, err := db.GetWikiPageByID(s.db, p.ID)
	if err != nil || current.Status != db.WikiStatusPending {
		return
	}
	if err := s.ApproveWikiPage(p.ID); err != nil {
		log.Printf("wiki review: auto-approve page %d: %v", p.ID, err)
		return
	}
	if err := db.UpdateWikiReview(s.db, p.ID, wikireview.VerdictApprove, reason, source); err != nil {
		log.Printf("wiki review: store auto approval for page %d: %v", p.ID, err)
	}
}
