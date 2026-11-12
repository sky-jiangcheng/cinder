package service

import (
	"repotory/internal/service/reviewrun"
)

// The settings surface of the daily review. The reading, the validation and the
// defaults live in reviewrun/config.go beside the pipeline that consumes them,
// so the page and the runner cannot drift apart; these methods are the binding
// names the desktop app and the RPC bridge already expose.

// GetReviewConfig reads the current settings, filling defaults for absent keys.
func (s *Service) GetReviewConfig() (*ReviewConfig, error) {
	return reviewrun.GetConfig(s.db)
}

// UpdateReviewConfig validates and persists the settings. Validation is strict
// about the time and the scope: a silently accepted "0930" would leave the user
// with a feature that looks configured and never runs.
func (s *Service) UpdateReviewConfig(cfg ReviewConfig) error {
	return reviewrun.UpdateConfig(s.db, cfg)
}

// LatestReviewSnapshot is the read the page and the MCP tool both use. A nil
// Run means "never reviewed", which is a different answer from "reviewed,
// nothing found".
func (s *Service) LatestReviewSnapshot(severities []string) (*ReviewSnapshot, error) {
	run, findings, err := s.LatestReview(severities, nil)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return &ReviewSnapshot{}, nil
	}
	return &ReviewSnapshot{Run: run, Findings: findings}, nil
}

// ReviewSnapshotFor returns one day's report.
func (s *Service) ReviewSnapshotFor(runDate, scope string, severities []string) (*ReviewSnapshot, error) {
	run, findings, err := s.ReviewReport(runDate, scope, severities, nil)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return &ReviewSnapshot{}, nil
	}
	return &ReviewSnapshot{Run: run, Findings: findings}, nil
}
