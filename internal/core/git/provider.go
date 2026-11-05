// Package git abstracts the Git data source behind a single Provider
// interface. M1 ships LocalGitProvider (shelling out to the local `git` CLI
// via the existing stats/ and knowledge/ packages). Future milestones will
// add HTTP API / SSH providers for GitLab / Gitea / GitHub etc. without
// changing call sites.
package git

import (
	"repotory/internal/knowledge"
	"repotory/internal/stats"
)

// Provider is the single abstraction for all Git interactions used by
// Cinder. Any implementation must be safe for concurrent use because the
// scan engine and dashboard handlers call these methods from goroutines.
type Provider interface {
	// QueryStats runs a shortstat query for a single repo/date/author combo.
	QueryStats(repoPath, date, author string) (*stats.Result, error)

	// QueryStatsRange queries per-day stats for [startDate, endDate] inclusive.
	QueryStatsRange(repoPath, startDate, endDate, author string) ([]stats.DailyEntry, error)

	// GetRecentCommit returns the most recent commit across the given repos,
	// optionally filtered by author. Returns (nil, nil) when no commits exist.
	GetRecentCommit(repoPaths []string, filterAuthor string) (*stats.RecentCommit, error)

	// GetRecentCommits returns the most recent N commits across repos.
	GetRecentCommits(repoPaths []string, filterAuthor string, limit int) ([]stats.RecentCommit, error)

	// MineKnowledge aggregates README, tech stack and language breakdown for
	// one repository working tree.
	MineKnowledge(repoPath string) (*knowledge.RepoKnowledge, error)

	// ValidateDate / ValidateAuthor expose the input validators so callers do
	// not have to import the stats package directly.
	ValidateDate(date string) error
	ValidateAuthor(author string) error

	// Today / Yesterday helpers.
	GetTodayDate() string
	GetYesterdayDate() string

	// IsWorkday reports whether the given date falls on Mon-Fri.
	IsWorkday(date string) bool
}
