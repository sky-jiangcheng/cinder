package main

import (
	"strings"
	"testing"

	"repotory/internal/bounds"
)

// repotory_ask is the question-answering tool (TODO Sprint 1.5).
//
// The one behaviour worth pinning without a live model is the fallback: an
// install with no AI endpoint must get keyword matches, and must be told they
// are keyword matches. Returning the old unqualified grep output under the
// name "ask" is how an agent ends up treating five note titles as a
// conclusion — the tool would be answering a question it never answered.

func TestAskWithoutAIEndpointLabelsItsFallback(t *testing.T) {
	ts := newTestServer(t)
	pid := ts.seedProject("demo", "/tmp/demo")
	if _, err := ts.svc.CreateNoteWithMeta(pid, "Retry policy", "the collector retries three times", "", "knowledge", "mcp"); err != nil {
		t.Fatal(err)
	}

	res := ts.call("repotory_ask", map[string]any{"query": "retry"})
	text := ts.text(res)
	if !strings.Contains(text, "NOT an answer") {
		t.Errorf("the fallback must say it is not an answer, got: %s", text)
	}
	if !strings.Contains(text, "Retry policy") {
		t.Errorf("the fallback must still carry the matches, got: %s", text)
	}
}

// With no match at all the tool says so, rather than answering with nothing
// that reads like an answer.
func TestAskWithoutAIEndpointReportsNoResults(t *testing.T) {
	ts := newTestServer(t)
	res := ts.call("repotory_ask", map[string]any{"query": "nothing-matches-this-token"})
	if got := ts.text(res); !strings.Contains(got, "No results found") {
		t.Errorf("message = %q, want it to report no results", got)
	}
}

// An empty query is still a usage error, not an empty answer.
func TestAskStillRequiresAQuery(t *testing.T) {
	ts := newTestServer(t)
	res := ts.call("repotory_ask", nil)
	if got := ts.text(res); !strings.Contains(got, "query is required") {
		t.Errorf("message = %q, want it to mention query is required", got)
	}
}

// An oversized path used to degrade silently to a cross-project answer
// (resolveProjectID returned 0). It must now be an explicit argument error —
// this assertion fails against the old behaviour.
func TestAskRejectsOversizedPath(t *testing.T) {
	ts := newTestServer(t)
	res := ts.call("repotory_ask", map[string]any{
		"query": "retry",
		"path":  strings.Repeat("p", bounds.MaxPathLen+1),
	})
	if !res.IsError {
		t.Fatalf("oversized path must be a tool error, got a cross-project answer: %s", ts.text(res))
	}
	if got := ts.text(res); !strings.Contains(got, "path too long") {
		t.Errorf("message = %q, want the length refusal", got)
	}
}
