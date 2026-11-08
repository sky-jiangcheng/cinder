package main

import (
	"strings"
	"testing"
)

// The two review tools are the agent-facing half of ADR-0053 D7. What matters
// at this layer is not the review logic (covered by service tests against a
// real repository) but the wiring: the tools exist, they answer in JSON, and
// the "nothing reviewed yet" case is a sentence rather than an empty object.

func TestReviewLatestWithoutRunsExplainsItself(t *testing.T) {
	ts := newTestServer(t)
	res := ts.call("repotory_review_latest", map[string]any{})
	body := ts.text(res)
	if !strings.Contains(body, "no completed review") {
		t.Fatalf("an empty history should say so plainly, got %q", body)
	}
}

func TestReviewRunDryRunReturnsReportShape(t *testing.T) {
	ts := newTestServer(t)
	res := ts.call("repotory_review_run", map[string]any{"dry_run": true, "no_ai": true, "date": "2020-01-01"})
	payload := ts.jsonPayload(res)

	run, ok := payload["run"].(map[string]any)
	if !ok {
		t.Fatalf("payload has no run object: %v", payload)
	}
	if run["run_date"] != "2020-01-01" {
		t.Errorf("run_date = %v, want 2020-01-01", run["run_date"])
	}
	if payload["dry_run"] != true {
		t.Errorf("dry_run flag missing from the reply: %v", payload["dry_run"])
	}
	// A dry run must not be retrievable afterwards, which is the property the
	// flag exists for.
	after := ts.text(ts.call("repotory_review_latest", map[string]any{}))
	if !strings.Contains(after, "no completed review") {
		t.Errorf("dry run stored a report: %q", after)
	}
}

func TestReviewRunRejectsBadDate(t *testing.T) {
	ts := newTestServer(t)
	body := ts.text(ts.call("repotory_review_run", map[string]any{"date": "yesterday-ish"}))
	if !strings.Contains(body, "YYYY-MM-DD") {
		t.Fatalf("bad date should point at the accepted format, got %q", body)
	}
}

func TestSplitCSV(t *testing.T) {
	cases := map[string][]string{
		"":               nil,
		"high":           {"high"},
		" high , medium": {"high", "medium"},
	}
	for in, want := range cases {
		got := splitCSV(in)
		if len(got) != len(want) {
			t.Errorf("splitCSV(%q) = %v, want %v", in, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("splitCSV(%q)[%d] = %q, want %q", in, i, got[i], want[i])
			}
		}
	}
}
