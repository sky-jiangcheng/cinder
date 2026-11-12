package main

import (
	"fmt"
	"strings"
	"testing"
)

// The session memory loop is the product's headline capability, so the tests
// exercise it exactly the way an agent does: through the registered tool
// table, over a real in-memory database.

func TestContextToolSingleProject(t *testing.T) {
	ts := newTestServer(t)
	pid := ts.seedProject("auth-service", "/home/me/auth-service")
	if _, err := ts.svc.CreateNoteWithMeta(pid, "Token refresh design", "uses sliding window", "auth", "knowledge", "manual"); err != nil {
		t.Fatal(err)
	}

	res := ts.call("repotory_context", nil)
	text := ts.text(res)
	for _, want := range []string{
		"# Project Context: auth-service",
		"Token refresh design",
		"sliding window",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("context missing %q\n%s", want, text)
		}
	}
}

func TestContextToolMultipleProjectsReturnsCatalog(t *testing.T) {
	ts := newTestServer(t)
	idA := ts.seedProject("alpha", "/home/me/alpha")
	idB := ts.seedProject("beta", "/home/me/beta")

	text := ts.text(ts.call("repotory_context", nil))
	for _, want := range []string{
		fmt.Sprintf("| %d | alpha |", idA),
		fmt.Sprintf("| %d | beta |", idB),
	} {
		if !strings.Contains(text, want) {
			t.Errorf("ambiguous context call should list both real projects; want %q in\n%s", want, text)
		}
	}
}

func TestContextToolFuzzyNameMatch(t *testing.T) {
	ts := newTestServer(t)
	ts.seedProject("payments-core", "/home/me/payments-core")
	ts.seedProject("web", "/home/me/web")

	text := ts.text(ts.call("repotory_context", map[string]any{"project_name": "payments"}))
	if !strings.Contains(text, "# Project Context: payments-core") {
		t.Errorf("fuzzy name should resolve to payments-core\n%s", text)
	}
}

func TestHandoffToolCreatesNote(t *testing.T) {
	ts := newTestServer(t)
	pid := ts.seedProject("demo", "/home/me/demo")

	res := ts.call("repotory_handoff", map[string]any{
		"project_id": float64(pid),
		"agent":      "claude-code",
		"summary":    "fixed the FTS drift bug",
		"changes":    []any{"rebuilt the index"},
		"gotchas":    []any{"rebuild takes minutes on big libraries"},
	})
	payload := ts.jsonPayload(res)
	if payload["note_id"] == nil || payload["note_id"].(float64) <= 0 {
		t.Fatalf("handoff should return the created note_id, got %v", payload)
	}

	// The handoff must be visible to the very next context call, tagged
	// first — that ordering is the whole point of the memory loop.
	text := ts.text(ts.call("repotory_context", map[string]any{"project_id": float64(pid)}))
	if !strings.Contains(text, "Session Handoff") || !strings.Contains(text, "fixed the FTS drift bug") {
		t.Errorf("context should surface the fresh handoff\n%s", text)
	}
	handoffIdx := strings.Index(text, "Session Handoff")
	knowledgeIdx := strings.Index(text, "## Knowledge Notes")
	if knowledgeIdx == -1 || handoffIdx < knowledgeIdx {
		t.Errorf("handoff should render inside the Knowledge Notes section\n%s", text)
	}
}

func TestHandoffToolValidation(t *testing.T) {
	ts := newTestServer(t)
	pid := ts.seedProject("demo", "/home/me/demo")

	if got := ts.text(ts.call("repotory_handoff", map[string]any{
		"project_id": float64(pid),
	})); !strings.Contains(got, "summary is required") {
		t.Errorf("missing summary should be reported\n%s", got)
	}

	if got := ts.text(ts.call("repotory_handoff", map[string]any{
		"project_id": float64(pid),
		"summary":    "only a summary",
	})); !strings.Contains(got, "at least one") {
		t.Errorf("summary-only handoff should be reported\n%s", got)
	}
}

// ADR-0054: milestone=true writes a rolling checkpoint that the next session
// reads as current state, not a session-end summary. The MCP tool must
// accept the milestone flag and surface it in the result.
func TestHandoffToolMilestoneFlag(t *testing.T) {
	ts := newTestServer(t)
	pid := ts.seedProject("demo", "/home/me/demo")

	res := ts.call("repotory_handoff", map[string]any{
		"project_id": float64(pid),
		"summary":    "checkpoint: parser refactor done",
		"changes":    []any{"rewrote parser"},
		"next_steps": []any{"update wiki", "run regression"},
		"milestone":  true,
	})
	payload := ts.jsonPayload(res)
	if payload["milestone"] != true {
		t.Fatalf("handoff result should report milestone=true, got %v", payload["milestone"])
	}
	if payload["note_id"] == nil || payload["note_id"].(float64) <= 0 {
		t.Fatalf("handoff should return the created note_id, got %v", payload)
	}

	// The milestone handoff must be visible to the next context call and
	// labelled as a rolling checkpoint.
	text := ts.text(ts.call("repotory_context", map[string]any{"project_id": float64(pid)}))
	if !strings.Contains(text, "[Rolling checkpoint") {
		t.Errorf("context should label the milestone as a checkpoint\n%s", text)
	}
	if !strings.Contains(text, "checkpoint: parser refactor done") {
		t.Errorf("context should surface the milestone summary\n%s", text)
	}
}

// A non-string item in a handoff section used to be dropped silently; it is
// now an argument error, because a half-written handoff is worse than a
// rejected one.
func TestHandoffRejectsNonStringItems(t *testing.T) {
	ts := newTestServer(t)
	pid := ts.seedProject("demo", "/home/me/demo")
	for _, field := range []string{"changes", "decisions", "gotchas", "next_steps"} {
		res := ts.call("repotory_handoff", map[string]any{
			"project_id": float64(pid),
			"summary":    "a session",
			field:        []any{"ok", 42},
		})
		if !res.IsError {
			t.Errorf("%s with a non-string item should be a tool error", field)
			continue
		}
		if got := ts.text(res); !strings.Contains(got, "must be a string") {
			t.Errorf("%s: message = %q, want the string refusal", field, got)
		}
	}
}
