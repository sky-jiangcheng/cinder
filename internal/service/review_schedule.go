package service

import (
	"context"
	"log"
	"time"

	"repotory/internal/db"
)

// reviewTickInterval is how often the desktop app asks "is it time yet".
//
// ADR-0053 D5 deliberately has no cron parser: the only schedule the product
// needs is "once a day at HH:MM", and a ten-minute poll of a single
// app_config key answers it without a dependency whose cross-platform
// semantics would need their own tests. The cost of polling is one row read
// per ten minutes.
const reviewTickInterval = 10 * time.Minute

// startReviewTicker launches the daily review loop as tracked background work.
// It is called from Startup, before the plugin-runtime guard, because the
// review needs neither the runtime nor any importer.
func (s *Service) startReviewTicker() {
	s.bgGo("review", func(ctx context.Context) {
		// First check one interval away, like wiki lint: opening the app must
		// not trigger a filesystem+model job while the user is logging in.
		for {
			t := time.NewTicker(reviewTickInterval)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
			t.Stop()
			s.maybeRunScheduledReview(ctx)
		}
	})
}

// maybeRunScheduledReview runs the day's review when all of these hold:
// the feature is enabled, the configured time has passed, and the day has no
// completed report yet. Every condition is checked against the database on
// each tick, so flipping the switch in settings takes effect without a
// restart.
func (s *Service) maybeRunScheduledReview(ctx context.Context) {
	cfg, err := s.GetReviewConfig()
	if err != nil {
		log.Printf("review: read config: %v", err)
		return
	}
	if !cfg.Enabled {
		return
	}
	now := time.Now()
	at, err := parseClock(cfg.At)
	if err != nil {
		log.Printf("review: %v", err)
		return
	}
	if now.Hour()*60+now.Minute() < at {
		return
	}
	runDate := reviewRunDateFor(now)
	existing, err := db.GetReviewRun(s.db, runDate, cfg.Scope)
	if err != nil {
		log.Printf("review: look up today's run: %v", err)
		return
	}
	if existing != nil && existing.Status == db.ReviewStatusDone {
		return // already reviewed; the ticker's job is done for the day
	}
	result, err := s.RunReview(ctx, ReviewOptions{Date: now.AddDate(0, 0, -1), Scope: cfg.Scope})
	if err != nil {
		log.Printf("review: scheduled run failed: %v", err)
		return
	}
	if result.AlreadyDone {
		return
	}
	log.Printf("review: %s done — %d commits, %d candidates, %d ai calls",
		runDate, result.Run.Commits, result.Run.Candidates, result.Run.AICalls)
}
