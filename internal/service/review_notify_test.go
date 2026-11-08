package service

import (
	"testing"

	"repotory/internal/db"
)

// The rail dot is the only push channel this app has (ADR-0053 D7), so the
// threshold is tested as a selection rule rather than through the UI: what
// qualifies, and what stays silent.

// seedReview stores one finished run with the given findings.
func seedReview(t *testing.T, svc *Service, findings []db.ReviewFinding) {
	t.Helper()
	runDate := "2026-01-02"
	id, err := db.StartReviewRun(svc.db, runDate, "collected", true)
	if err != nil {
		t.Fatalf("StartReviewRun: %v", err)
	}
	run := &db.ReviewRun{ID: id, RunDate: runDate, Scope: "collected", Status: "done", StartedAt: "2026-01-02T09:30:00"}
	if err := db.FinishReviewRun(svc.db, run); err != nil {
		t.Fatalf("FinishReviewRun: %v", err)
	}
	for i := range findings {
		findings[i].RunID = id
	}
	if err := db.InsertReviewFindings(svc.db, findings); err != nil {
		t.Fatalf("InsertReviewFindings: %v", err)
	}
}

func finding(severity, verdict string) db.ReviewFinding {
	return db.ReviewFinding{
		RepoPath: "/tmp/pay", FilePath: "A.java", Line: 1, Rule: "feign_missing_body",
		Severity: severity, Verdict: verdict, Message: "m",
	}
}

func TestReviewNotifyCountDefaultsToConfirmedHigh(t *testing.T) {
	svc, _ := setupService(t)
	seedReview(t, svc, []db.ReviewFinding{
		finding("high", "confirmed"),
		finding("high", "confirmed"),
		finding("high", "unverified"), // a candidate nobody checked: silent
		finding("medium", "confirmed"), // below the threshold
		finding("low", "confirmed"),
	})
	n, err := svc.ReviewNotifyCount()
	if err != nil {
		t.Fatalf("ReviewNotifyCount: %v", err)
	}
	if n != 2 {
		t.Fatalf("default threshold must count high+confirmed only, got %d", n)
	}
}

func TestReviewNotifyCountAllAndOff(t *testing.T) {
	svc, _ := setupService(t)
	seedReview(t, svc, []db.ReviewFinding{
		finding("high", "confirmed"),
		finding("medium", "unverified"),
		finding("low", "unverified"),
	})
	cfg, err := svc.GetReviewConfig()
	if err != nil {
		t.Fatal(err)
	}

	cfg.Notify = "all"
	if err := svc.UpdateReviewConfig(*cfg); err != nil {
		t.Fatal(err)
	}
	if n, _ := svc.ReviewNotifyCount(); n != 3 {
		t.Fatalf("notify=all must count every finding, got %d", n)
	}

	cfg.Notify = "off"
	if err := svc.UpdateReviewConfig(*cfg); err != nil {
		t.Fatal(err)
	}
	if n, _ := svc.ReviewNotifyCount(); n != 0 {
		t.Fatalf("notify=off must stay silent, got %d", n)
	}
}

func TestReviewNotifyCountWithoutAnyRun(t *testing.T) {
	svc, _ := setupService(t)
	n, err := svc.ReviewNotifyCount()
	if err != nil {
		t.Fatalf("ReviewNotifyCount: %v", err)
	}
	if n != 0 {
		t.Fatalf("never reviewed means no dot, got %d", n)
	}
}
