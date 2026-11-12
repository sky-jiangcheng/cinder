package reviewrun

import (
	"database/sql"
	"sort"
	"strconv"
	"strings"
	"time"

	"repotory/internal/db"
	"repotory/internal/review"
)

// Config keys (ADR-0053 D5). They live in app_config like every other user
// setting, so the settings page and the CLI read the same source.
const (
	KeyEnabled    = "review.enabled"
	KeyAt         = "review.at"
	KeyScope      = "review.scope"
	KeyAI         = "review.ai"
	KeyNotify     = "review.notify"
	KeyMaxDiff    = "review.max_diff_lines"
	KeyRulePrefix = "review.rule." // + rule id -> "1"
)

// Defaults for the keys above. `review.enabled=0` is not caution theatre: the
// job reads source code and (with AI on) sends excerpts off the machine, so it
// must be a deliberate choice (ADR-0053 D5).
const (
	DefaultAt      = "09:30"
	DefaultScope   = "collected"
	DefaultNotify  = "high"
	DefaultMaxDiff = 2000
)

// Config is the settings surface for the daily review (ADR-0053 D5). It is a
// flat struct rather than a map so the settings page gets a typed contract and
// a typo in a key name cannot silently disable the feature.
type Config struct {
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

// Snapshot is what the page and the MCP read tool render: one run plus its
// findings. A nil Run means "never reviewed", which is a different answer from
// "reviewed, nothing found".
type Snapshot struct {
	Run      *db.ReviewRun      `json:"run"`
	Findings []db.ReviewFinding `json:"findings"`
}

// GetConfig reads the current settings, filling defaults for absent keys.
func GetConfig(database *sql.DB) (*Config, error) {
	cfg := &Config{
		At:           DefaultAt,
		Scope:        DefaultScope,
		AI:           true,
		Notify:       DefaultNotify,
		MaxDiffLines: DefaultMaxDiff,
		Rules:        map[string]bool{},
	}
	if v, _ := db.GetConfig(database, KeyEnabled); v == "1" || strings.EqualFold(v, "true") {
		cfg.Enabled = true
	}
	if v, _ := db.GetConfig(database, KeyAt); strings.TrimSpace(v) != "" {
		cfg.At = strings.TrimSpace(v)
	}
	if v, _ := db.GetConfig(database, KeyScope); strings.TrimSpace(v) != "" {
		cfg.Scope = strings.TrimSpace(v)
	}
	if v, _ := db.GetConfig(database, KeyAI); strings.TrimSpace(v) != "" {
		cfg.AI = v == "1" || strings.EqualFold(v, "true")
	}
	if v, _ := db.GetConfig(database, KeyNotify); strings.TrimSpace(v) != "" {
		cfg.Notify = strings.TrimSpace(v)
	}
	if v, _ := db.GetConfig(database, KeyMaxDiff); strings.TrimSpace(v) != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			cfg.MaxDiffLines = n
		}
	}
	for _, rule := range review.DefaultRules() {
		cfg.Rules[rule.ID()] = ruleEnabled(database, rule)
	}
	return cfg, nil
}

// UpdateConfig validates and persists the settings.
//
// Validation is deliberately strict about the time and scope: a silently
// accepted "0930" or "projects=3" would leave the user with a feature that
// looks configured and never runs, and the only symptom would be an empty
// report page.
func UpdateConfig(database *sql.DB, cfg Config) error {
	at := strings.TrimSpace(cfg.At)
	if at == "" {
		at = DefaultAt
	}
	if _, err := ParseClock(at); err != nil {
		return err
	}
	scope := strings.TrimSpace(cfg.Scope)
	if scope == "" {
		scope = DefaultScope
	}
	if scope != DefaultScope && !strings.HasPrefix(scope, "project:") {
		return &badScopeError{scope: scope}
	}
	notify := strings.TrimSpace(cfg.Notify)
	switch notify {
	case "", DefaultNotify:
		notify = DefaultNotify
	case "all", "off":
	default:
		notify = DefaultNotify
	}
	maxDiff := cfg.MaxDiffLines
	if maxDiff <= 0 {
		maxDiff = DefaultMaxDiff
	}

	pairs := [][2]string{
		{KeyEnabled, boolValue(cfg.Enabled)},
		{KeyAt, at},
		{KeyScope, scope},
		{KeyAI, boolValue(cfg.AI)},
		{KeyNotify, notify},
		{KeyMaxDiff, strconv.Itoa(maxDiff)},
	}
	for _, p := range pairs {
		if err := db.SetConfig(database, p[0], p[1]); err != nil {
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
		if err := db.SetConfig(database, KeyRulePrefix+id, boolValue(cfg.Rules[id])); err != nil {
			return err
		}
	}
	return nil
}

// ruleEnabled reports one rule's effective switch: the stored value when there
// is one, else the rule's own default (off for OptInRule ones). Both the runner
// and GetConfig read it, which is what keeps the settings page and the pipeline
// from disagreeing.
func ruleEnabled(database *sql.DB, rule review.Rule) bool {
	on := true
	if opt, ok := rule.(review.OptInRule); ok && opt.OptIn() {
		on = false
	}
	if v, _ := db.GetConfig(database, KeyRulePrefix+rule.ID()); v != "" {
		on = v == "1" || strings.EqualFold(v, "true")
	}
	return on
}

// ParseClock validates "HH:MM" and returns it as minutes since midnight.
func ParseClock(s string) (int, error) {
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

// RunDateFor is the day a run at `now` reviews: yesterday. Extracted so the
// scheduler and the report page cannot disagree about which day "today's
// report" covers.
func RunDateFor(now time.Time) string {
	return now.AddDate(0, 0, -1).Format("2006-01-02")
}

// badScopeError keeps the message actionable: it names the accepted shapes
// rather than restating that the input was wrong.
type badScopeError struct{ scope string }

func (e *badScopeError) Error() string {
	return "review scope must be 'collected' or 'project:<id>,<id>', got " + e.scope
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
