package service

import (
	"context"

	"repotory/internal/db"
	"repotory/internal/review"
	"repotory/internal/service/reviewrun"
)

// The incremental commit review (ADR-0053) lives in internal/service/reviewrun
// as of ADR-0060 step one. What remains here is the service's edge of it: the
// database handle, the model call (endpoint, key, ADR-0030 audit row) and the
// names every caller already uses.
//
// The four types below are aliases, not copies. Moving a pipeline must not
// change what a caller serialises: the Wails binding, the RPC bridge and the
// MCP tools see one shape, and their snake_case json tags come along with it.

// ReviewOptions controls one run.
type ReviewOptions = reviewrun.Options

// ReviewResult is what every trigger returns (CLI, MCP tool, desktop button).
type ReviewResult = reviewrun.Result

// ReviewConfig is the daily-review settings surface (ADR-0053 D5).
type ReviewConfig = reviewrun.Config

// ReviewSnapshot is one run plus its findings, as the page and the MCP read
// tool render it.
type ReviewSnapshot = reviewrun.Snapshot

// reviewRunner binds the pipeline to this service's database and to the one
// thing the pipeline cannot own: the model call. A fresh Runner per call is
// deliberate — it holds no state, so there is nothing to invalidate when the
// endpoint or the API key changes under a running process.
func (s *Service) reviewRunner() *reviewrun.Runner {
	return reviewrun.New(s.db, reviewRefiner{s: s})
}

// RunReview executes one incremental review. Safe from the CLI, the MCP server
// and the desktop ticker alike: all side effects go through the database, and
// the unique (run_date, scope) key makes a concurrent second run fail fast
// instead of interleaving findings.
func (s *Service) RunReview(ctx context.Context, opts ReviewOptions) (*ReviewResult, error) {
	return s.reviewRunner().Run(ctx, opts)
}

// LatestReview returns the most recent completed run with its findings, which
// is what the desktop page and the MCP read tool both render.
func (s *Service) LatestReview(severities, verdicts []string) (*db.ReviewRun, []db.ReviewFinding, error) {
	return s.reviewRunner().Latest(severities, verdicts)
}

// ReviewReport returns one run by date+scope and its findings.
func (s *Service) ReviewReport(runDate, scope string, severities, verdicts []string) (*db.ReviewRun, []db.ReviewFinding, error) {
	return s.reviewRunner().Report(runDate, scope, severities, verdicts)
}

// ReviewRunsSince returns the completed runs of the last n days, newest first.
func (s *Service) ReviewRunsSince(days int) ([]db.ReviewRun, error) {
	return s.reviewRunner().RunsSince(days)
}

// StoredPreview converts in-memory candidates into the stored row shape, so a
// dry-run preview and a stored report render identically.
func StoredPreview(findings []review.Finding) []db.ReviewFinding {
	return reviewrun.StoredPreview(findings)
}
