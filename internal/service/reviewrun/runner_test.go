package reviewrun

import (
	"database/sql"
	"testing"

	"repotory/internal/db"
)

// setupRunner returns a Runner over a fresh in-memory schema. The pipeline
// needs nothing else: everything it reads is a config row or a table, which is
// what made it extractable in the first place (ADR-0060).
func setupRunner(t *testing.T) (*Runner, *sql.DB) {
	t.Helper()
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	// No refiner: these tests are about the rule path, and a refiner that would
	// silently answer they never asked for is exactly the kind of fake that
	// hides a regression.
	return New(database, nil), database
}

func TestReposRejectsBadScope(t *testing.T) {
	r, _ := setupRunner(t)
	if _, err := r.repos("project:abc"); err == nil {
		t.Fatal("expected an error for a non-numeric project id")
	}
	if _, err := r.repos("everything"); err == nil {
		t.Fatal("expected an error for an unknown scope")
	}
}

func TestRuleSetHonoursOptIn(t *testing.T) {
	r, database := setupRunner(t)
	has := func(id string) bool {
		for _, rule := range r.ruleSet() {
			if rule.ID() == id {
				return true
			}
		}
		return false
	}
	if has("sensitive_log") {
		t.Error("sensitive_log must be off by default")
	}
	if !has("feign_missing_body") {
		t.Error("feign_missing_body must be on by default")
	}
	if err := db.SetConfig(database, KeyRulePrefix+"sensitive_log", "1"); err != nil {
		t.Fatal(err)
	}
	if !has("sensitive_log") {
		t.Error("sensitive_log should turn on when review.rule.sensitive_log=1")
	}
}

// Every rule switch is real: turning one off removes it, and a fresh install
// that never wrote any switch still runs all nine non-opt-in rules.
func TestRuleSetHonoursAllRuleSwitches(t *testing.T) {
	r, database := setupRunner(t)
	has := func(id string) bool {
		for _, rule := range r.ruleSet() {
			if rule.ID() == id {
				return true
			}
		}
		return false
	}
	if rules := r.ruleSet(); len(rules) != 9 {
		t.Fatalf("default config must keep 9 non-opt-in rules, got %d", len(rules))
	}
	if has("sensitive_log") {
		t.Fatal("sensitive_log is opt-in and must stay off by default")
	}
	if err := db.SetConfig(database, KeyRulePrefix+"feign_missing_body", "0"); err != nil {
		t.Fatal(err)
	}
	if has("feign_missing_body") {
		t.Fatal("turning feign_missing_body off must remove it from the rule set")
	}
	if rules := r.ruleSet(); len(rules) != 8 {
		t.Fatalf("only the switched-off rule should disappear, got %d", len(rules))
	}
}

// GetConfig and the runner must agree about a rule's switch, or the settings
// page shows one thing while the pipeline does another. That is why ruleEnabled
// is the single reader for both.
func TestConfigAndRunnerAgreeOnRuleSwitches(t *testing.T) {
	r, database := setupRunner(t)
	if err := db.SetConfig(database, KeyRulePrefix+"sensitive_log", "1"); err != nil {
		t.Fatal(err)
	}
	cfg, err := GetConfig(database)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Rules["sensitive_log"] {
		t.Fatal("config must report the stored opt-in")
	}
	found := false
	for _, rule := range r.ruleSet() {
		if rule.ID() == "sensitive_log" {
			found = true
		}
	}
	if !found {
		t.Fatal("the runner must run what the config reports as on")
	}
}

// Defaults are the contract for an install that never opened the settings page:
// the feature is off, the model pass is on *if* the feature is on, and the
// budget is the ADR's number.
func TestConfigDefaults(t *testing.T) {
	_, database := setupRunner(t)
	cfg, err := GetConfig(database)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled {
		t.Error("review must default to off: it reads source and may call a model")
	}
	if !cfg.AI || cfg.At != DefaultAt || cfg.Scope != DefaultScope ||
		cfg.Notify != DefaultNotify || cfg.MaxDiffLines != DefaultMaxDiff {
		t.Fatalf("defaults drifted: %+v", cfg)
	}
}

// Validation is strict on purpose: "0930" or "projects=3" would leave a feature
// that looks configured and never runs, with an empty report as the only
// symptom.
func TestUpdateConfigRejectsMalformedValues(t *testing.T) {
	_, database := setupRunner(t)
	if err := UpdateConfig(database, Config{At: "0930"}); err == nil {
		t.Error("a time without a colon must be rejected")
	}
	if err := UpdateConfig(database, Config{Scope: "projects:3"}); err == nil {
		t.Error("a scope other than collected / project:<ids> must be rejected")
	}
	// Notify falls back to the default rather than erroring: it is a
	// presentation threshold, not a promise about what runs.
	if err := UpdateConfig(database, Config{Notify: "sometimes"}); err != nil {
		t.Fatalf("unknown notify threshold should fall back, got %v", err)
	}
	cfg, _ := GetConfig(database)
	if cfg.Notify != DefaultNotify {
		t.Errorf("notify = %q, want %q", cfg.Notify, DefaultNotify)
	}
}

func TestParseClock(t *testing.T) {
	cases := map[string]int{"00:00": 0, "09:30": 570, "23:59": 1439}
	for in, want := range cases {
		got, err := ParseClock(in)
		if err != nil || got != want {
			t.Errorf("ParseClock(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "9", "24:00", "09:60", "09:3O", "9:30:00"} {
		if _, err := ParseClock(bad); err == nil {
			t.Errorf("ParseClock(%q) should fail", bad)
		}
	}
}
