// Package conformance loads the three-surface behaviour fixture
// (scripts/shared-chat-conformance.fixture.json, ADR-0059 gate B) and enforces
// its structural invariants. One Go package serves three consumers: the Go
// runner (the ask tool's own fallback wording is one of the surfaces under
// test), the version-drift gate (the fixture ↔ documentation rule list), and
// the two IDE runners — which re-read the same file and re-derive the same
// invariants in their own languages, because a validator shared only as a Go
// library would leave the Kotlin/TS side trusting an unchecked read.
package conformance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Fixture is the parsed contract file.
type Fixture struct {
	ContractVersion struct {
		Core   string `json:"core"`
		Rules  int    `json:"rules"`
		Source string `json:"source"`
		Note   string `json:"note"`
	} `json:"contract_version"`
	Input struct {
		MessageFields   []string `json:"message_fields"`
		ServerReadOrder string   `json:"server_read_order"`
		ReplyGuard      string   `json:"reply_guard"`
	} `json:"input"`
	Rules []Rule `json:"rules"`
}

// Rule is one behaviour the three surfaces must (eventually) agree on.
type Rule struct {
	ID       string          `json:"id"`
	Status   string          `json:"status"` // settled | skipped
	Statement string         `json:"statement"`
	Input    json.RawMessage `json:"input"`
	Expect   json.RawMessage `json:"expect"`
	SkipReason string        `json:"skip_reason"`
	// AssertForbidden marks a skipped rule a runner must NOT assert: writing
	// the assertion before the product decision exists would pin today's
	// divergence as the standard (ADR-0059: the fixture is the target, not a
	// snapshot).
	AssertForbidden bool `json:"assert_forbidden"`
}

// Settled and Skipped are the two rule statuses. Anything else is a typo in
// the fixture and must fail loudly: a rule with an unknown status would
// silently be treated as skipped by a runner that tests equality, or as
// settled by one that tests inequality — two readers, two contracts.
const (
	Settled = "settled"
	Skipped = "skipped"
)

// Load reads and validates the fixture at path.
func Load(path string) (*Fixture, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("conformance: read fixture: %w", err)
	}
	var f Fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("conformance: parse %s: %w", path, err)
	}
	if err := f.validate(); err != nil {
		return nil, fmt.Errorf("conformance: %s: %w", filepath.Base(path), err)
	}
	return &f, nil
}

// SettledRules returns the enforced rules, in file order.
func (f *Fixture) SettledRules() []Rule {
	out := make([]Rule, 0, len(f.Rules))
	for _, r := range f.Rules {
		if r.Status == Settled {
			out = append(out, r)
		}
	}
	return out
}

// SkippedRules returns the deferred rules, in file order.
func (f *Fixture) SkippedRules() []Rule {
	out := make([]Rule, 0, len(f.Rules))
	for _, r := range f.Rules {
		if r.Status == Skipped {
			out = append(out, r)
		}
	}
	return out
}

// RuleByID returns one rule, or an error naming the missing id: runners must
// fail when the rule they implement disappears rather than quietly skip it.
func (f *Fixture) RuleByID(id string) (*Rule, error) {
	for i := range f.Rules {
		if f.Rules[i].ID == id {
			return &f.Rules[i], nil
		}
	}
	return nil, fmt.Errorf("rule %q not in fixture", id)
}

// validate enforces the structural contract: every rule carries exactly one
// status, settled rules have expectations, skipped rules have a reason and may
// not carry expectations they would never be run against, ids are unique, and
// the declared rule count matches the list (the same "counts are load-bearing"
// rule as TestToolsRegistered).
func (f *Fixture) validate() error {
	if f.ContractVersion.Rules != len(f.Rules) {
		return fmt.Errorf("contract_version.rules = %d but the file holds %d rules — bump the count when you add one (gate 11 checks the docs' copy)",
			f.ContractVersion.Rules, len(f.Rules))
	}
	seen := map[string]bool{}
	for _, r := range f.Rules {
		if r.ID == "" {
			return fmt.Errorf("a rule without an id")
		}
		if seen[r.ID] {
			return fmt.Errorf("duplicate rule id %q", r.ID)
		}
		seen[r.ID] = true
		switch r.Status {
		case Settled:
			if len(r.Expect) == 0 {
				return fmt.Errorf("settled rule %q carries no expect", r.ID)
			}
			if r.SkipReason != "" || r.AssertForbidden {
				return fmt.Errorf("settled rule %q must not carry skip fields", r.ID)
			}
		case Skipped:
			if r.SkipReason == "" {
				return fmt.Errorf("skipped rule %q must name what is still open", r.ID)
			}
			if !r.AssertForbidden {
				return fmt.Errorf("skipped rule %q must set assert_forbidden — a skip without the guard lets a runner assert it anyway", r.ID)
			}
			if len(r.Expect) != 0 {
				return fmt.Errorf("skipped rule %q carries an expect it is never run against", r.ID)
			}
		default:
			return fmt.Errorf("rule %q has status %q: only settled and skipped exist", r.ID, r.Status)
		}
	}
	return nil
}
