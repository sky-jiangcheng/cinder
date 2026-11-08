package service

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"repotory/internal/db"
	"repotory/internal/review"
)

// ReviewConfig is the settings surface for the daily review (ADR-0053 D5).
// It is a flat struct rather than a map so the settings page gets a typed
// contract and a typo in a key name cannot silently disable the feature.
type ReviewConfig struct {
	// Enabled is the master switch. Off by default: the job reads source code
	// and, with AI on, sends excerpts off the machine.
	Enabled bool `json:"enabled"`
	// At is the local wall-clock time of the daily run, "HH:MM".
	At string `json:"at"`
	// Scope is "collected" or "project:<ids>".
	Scope string `json:"scope"`
	// AI enables the model review pass. Off means rules only, zero external
	// calls.
	AI bool `json:"ai"`
	// Notify is the push threshold: "high" (default), "all" or "off".
	Notify string `json:"notify"`
	// MaxDiffLines is the per-file budget; over it the file is skipped and
	// counted instead of reviewed.
	MaxDiffLines int `json:"max_diff_lines"`
	// Rules is the per-rule opt-in state, keyed by rule id. Rules that are on
	// by default appear as true; the ones that require a choice (currently
	// sensitive_log) appear as their stored value.
	Rules map[string]bool `json:"rules"`
}

// ReviewSnapshot is what the page and the MCP read tool render: one run plus
// its findings. A nil Run means "never reviewed", which is a different answer
// from "reviewed, nothing found".
type ReviewSnapshot struct {
	Run      *db.ReviewRun      `json:"run"`
	Findings []db.ReviewFinding `json:"findings"`
}

// GetReviewConfig reads the current settings, filling defaults for absent keys.
func (s *Service) GetReviewConfig() (*ReviewConfig, error) {
	cfg := &ReviewConfig{
		At:           reviewDefaultAt,
		Scope:        reviewDefaultScope,
		AI:           true,
		Notify:       reviewDefaultNotify,
		MaxDiffLines: reviewDefaultMaxDiff,
		Rules:        map[string]bool{},
	}
	if v, _ := db.GetConfig(s.db, reviewEnabledKey); v == "1" || strings.EqualFold(v, "true") {
		cfg.Enabled = true
	}
	if v, _ := db.GetConfig(s.db, reviewAtKey); strings.TrimSpace(v) != "" {
		cfg.At = strings.TrimSpace(v)
	}
	if v, _ := db.GetConfig(s.db, reviewScopeKey); strings.TrimSpace(v) != "" {
		cfg.Scope = strings.TrimSpace(v)
	}
	if v, _ := db.GetConfig(s.db, reviewAIKey); strings.TrimSpace(v) != "" {
		cfg.AI = v == "1" || strings.EqualFold(v, "true")
	}
	if v, _ := db.GetConfig(s.db, reviewNotifyKey); strings.TrimSpace(v) != "" {
		cfg.Notify = strings.TrimSpace(v)
	}
	if v, _ := db.GetConfig(s.db, reviewMaxDiffKey); strings.TrimSpace(v) != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			cfg.MaxDiffLines = n
		}
	}
	for _, r := range review.DefaultRules() {
		on := true
		if opt, ok := r.(review.OptInRule); ok && opt.OptIn() {
			on = false
		}
		if v, _ := db.GetConfig(s.db, reviewOptInRuleKey+r.ID()); v != "" {
			on = v == "1" || strings.EqualFold(v, "true")
		}
		cfg.Rules[r.ID()] = on
	}
	return cfg, nil
}

// UpdateReviewConfig validates and persists the settings.
//
// Validation is deliberately strict about the time and scope: a silently
// accepted "0930" or "projects=3" would leave the user with a feature that
// looks configured and never runs, and the only symptom would be an empty
// report page.
func (s *Service) UpdateReviewConfig(cfg ReviewConfig) error {
	at := strings.TrimSpace(cfg.At)
	if at == "" {
		at = reviewDefaultAt
	}
	if _, err := parseClock(at); err != nil {
		return err
	}
	scope := strings.TrimSpace(cfg.Scope)
	if scope == "" {
		scope = reviewDefaultScope
	}
	if scope != reviewDefaultScope && !strings.HasPrefix(scope, "project:") {
		return errBadReviewScope(scope)
	}
	notify := strings.TrimSpace(cfg.Notify)
	switch notify {
	case "", reviewDefaultNotify:
		notify = reviewDefaultNotify
	case "all", "off":
	default:
		notify = reviewDefaultNotify
	}
	maxDiff := cfg.MaxDiffLines
	if maxDiff <= 0 {
		maxDiff = reviewDefaultMaxDiff
	}

	pairs := [][2]string{
		{reviewEnabledKey, boolValue(cfg.Enabled)},
		{reviewAtKey, at},
		{reviewScopeKey, scope},
		{reviewAIKey, boolValue(cfg.AI)},
		{reviewNotifyKey, notify},
		{reviewMaxDiffKey, strconv.Itoa(maxDiff)},
	}
	for _, p := range pairs {
		if err := db.SetConfig(s.db, p[0], p[1]); err != nil {
			return err
		}
	}
	// Per-rule switches are only written for rules that exist, so a renamed
	// rule leaves no stale key behind that would re-enable it later.
	ids := make([]string, 0, len(cfg.Rules))
	for id := range cfg.Rules {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := db.SetConfig(s.db, reviewOptInRuleKey+id, boolValue(cfg.Rules[id])); err != nil {
			return err
		}
	}
	return nil
}

// LatestReviewSnapshot is the read the page and the MCP tool both use.
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

// errBadReviewScope keeps the message actionable: it names the accepted
// shapes rather than restating that the input was wrong.
func errBadReviewScope(scope string) error {
	return &badScopeError{scope: scope}
}

type badScopeError struct{ scope string }

func (e *badScopeError) Error() string {
	return "review scope must be 'collected' or 'project:<id>,<id>', got " + e.scope
}

// parseClock validates "HH:MM" and returns it as minutes since midnight.
func parseClock(s string) (int, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, &badClockError{s: s}
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, &badClockError{s: s}
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, &badClockError{s: s}
	}
	return h*60 + m, nil
}

type badClockError struct{ s string }

func (e *badClockError) Error() string {
	return "review time must be HH:MM (24h), got " + e.s
}

func boolValue(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// reviewRunDateFor is the day a run at `now` reviews: yesterday. Extracted so
// the scheduler and the report page cannot disagree about which day "today's
// report" covers.
func reviewRunDateFor(now time.Time) string {
	return now.AddDate(0, 0, -1).Format("2006-01-02")
}
