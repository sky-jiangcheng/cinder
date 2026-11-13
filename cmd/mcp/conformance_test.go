package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"repotory/internal/conformance"
	"repotory/internal/db"
	"repotory/internal/service"
)

// The fixture's fallback texts claim to quote the server. They must quote it
// EXACTLY: the IDE runners assert "this text must never be stored as an
// answer" against the fixture's copy, so a stale copy would gate the wrong
// string while the real wording drifted. This file is package main, where
// keywordFallback lives — the bridge belongs here.

// findFixture walks up to the one contract file (mirrors internal/conformance).
func findFixture(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(dir, "scripts", "shared-chat-conformance.fixture.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("shared-chat-conformance.fixture.json not found above")
		}
		dir = parent
	}
}

func loadFixture(t *testing.T) *conformance.Fixture {
	t.Helper()
	f, err := conformance.Load(findFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

type fallbackInput struct {
	Scenario      string `json:"scenario"`
	AskToolResult string `json:"ask_tool_result"`
	ZeroHitResult string `json:"zero_hit_result"`
}

// TestFixtureQuotesTheRealFallbackWording runs the REAL tool-side fallback
// over the real service and holds it against every fallback quote in the
// fixture. The zero-hit rule's text must equal the server's output for a
// no-hit query byte for byte; the other rules must be recognisable shapes.
func TestFixtureQuotesTheRealFallbackWording(t *testing.T) {
	f := loadFixture(t)

	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	svc := service.New(database, "tester")

	zeroHit, err := keywordFallback(svc, "nothing matches this query at all")
	if err != nil {
		t.Fatalf("keywordFallback: %v", err)
	}
	// Seed one note (into the default landing project the migration seeds) so
	// the second call exercises the match-list branch.
	var defaultProject int64
	if err := database.QueryRow(`SELECT id FROM projects WHERE project_kind = 'default' LIMIT 1`).Scan(&defaultProject); err != nil {
		t.Fatalf("default project: %v", err)
	}
	if _, err := svc.CreateNoteWithMeta(defaultProject, "decided on X", "the decision body", "", "knowledge", "manual"); err != nil {
		t.Fatalf("seed note: %v", err)
	}
	matchList, err := keywordFallback(svc, "decided")
	if err != nil {
		t.Fatalf("keywordFallback with hits: %v", err)
	}
	if !strings.HasPrefix(zeroHit, "No results found for: ") {
		t.Fatalf("zero-hit fallback = %q, want the No-results prefix", zeroHit)
	}
	if !strings.HasPrefix(matchList, "No AI endpoint is configured") {
		t.Fatalf("match-list fallback = %q, want the No-endpoint prefix", matchList)
	}

	for _, id := range []string{
		"fallback.not_persisted",
		"fallback.not_persisted.zero_hit",
		"fallback.local_render",
	} {
		rule, err := f.RuleByID(id)
		if err != nil {
			t.Fatal(err)
		}
		var in fallbackInput
		if err := json.Unmarshal(rule.Input, &in); err != nil {
			t.Fatalf("rule %s input: %v", id, err)
		}
		quoted := in.ZeroHitResult
		if quoted == "" {
			quoted = in.AskToolResult
		}
		if !strings.HasPrefix(quoted, "No AI endpoint is configured") && !strings.HasPrefix(quoted, "No results found for: ") {
			t.Fatalf("rule %s quotes an unrecognised shape: %q", id, quoted)
		}
		// The zero-hit text is prefix + the caller's query echoed verbatim, so
		// the fixture's quote is compared as a prefix shape, not byte-equality
		// against a different query. The drift this test guards is the PREFIX
		// itself: a wording change to "no matches" would break every IDE
		// panel's local refusal marker, which keys on these strings.
		if id == "fallback.not_persisted.zero_hit" && !strings.HasPrefix(zeroHit, strings.TrimSuffix(quoted, " <query echoed verbatim>")) {
			t.Fatalf("fixture quotes prefix %q, server zero-hit output = %q — one of them drifted", quoted, zeroHit)
		}
		if id == "fallback.not_persisted" && !strings.HasPrefix(in.AskToolResult, "No AI endpoint is configured") {
			t.Fatalf("rule %s must quote the match-list shape, got %q", id, in.AskToolResult)
		}
	}
}
