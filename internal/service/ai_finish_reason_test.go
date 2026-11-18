package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"repotory/internal/aiclient"
	"strings"
	"testing"
	"time"

	"repotory/internal/db"
)

// finish_reason used to be dropped on the floor, so a reply the server cut off
// and a reply that was simply malformed both surfaced as the same "cannot parse
// JSON" note. They call for opposite actions from the user (raise the output
// budget vs. re-run / change model), so the distinction has to survive.

func TestTruncatedHint(t *testing.T) {
	cases := []struct {
		reason string
		want   bool // whether a hint is expected
		label  string
	}{
		{"", false, "missing field is not evidence of truncation"},
		{"stop", false, "normal completion"},
		{"end_turn", false, "Ollama-style normal completion"},
		{"stop_sequence", false, "hit a stop sequence, not the budget"},
		{"length", true, "output budget exhausted"},
		{"max_tokens", true, "legacy alias for length"},
		{"model_length", true, "Gemini-style alias"},
		{"content_filter", true, "blocked, not truncated but also not complete"},
		{"something_new", true, "unknown reason still warrants a note"},
	}
	for _, tc := range cases {
		got := truncatedHint(tc.reason)
		if (got != "") != tc.want {
			t.Errorf("truncatedHint(%q) = %q, want hint=%v (%s)", tc.reason, got, tc.want, tc.label)
		}
	}
}

func TestTruncatedHintWordingIsActionable(t *testing.T) {
	// The whole point is that the user learns what to do next, so the hint has
	// to name the two real remedies rather than just restating the failure.
	h := truncatedHint("length")
	if h == "" {
		t.Fatal("length must produce a hint")
	}
	for _, want := range []string{"max_tokens", "模型"} {
		if !strings.Contains(h, want) {
			t.Errorf("hint %q does not mention %q", h, want)
		}
	}
	if strings.Contains(h, "无法解析") {
		t.Errorf("hint %q still claims a parse failure, which is the bug being fixed", h)
	}
}

func TestAIFinishReasonReadsFirstChoice(t *testing.T) {
	var cr aiclient.Response
	if err := json.Unmarshal([]byte(`{"choices":[{"finish_reason":"length","message":{"content":"{\"a\":"}}]}`), &cr); err != nil {
		t.Fatal(err)
	}
	if got := aiclient.FinishReason(cr); got != "length" {
		t.Errorf("aiclient.FinishReason = %q, want length", got)
	}
	if cr.Choices[0].Message.Content != `{"a":` {
		t.Errorf("content mangled: %q", cr.Choices[0].Message.Content)
	}

	var empty aiclient.Response
	if got := aiclient.FinishReason(empty); got != "" {
		t.Errorf("aiclient.FinishReason on no choices = %q, want empty", got)
	}
}

// End to end through the real HTTP path: a server that reports "length" must
// surface as Truncated on the answer the UI receives.
func TestAskAIWithEvidenceReportsTruncation(t *testing.T) {
	var reply string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(reply))
	}))
	defer srv.Close()

	svc, _ := setupService(t)
	if err := db.SetConfig(svc.db, "ai_chat_base_url", srv.URL); err != nil {
		t.Fatal(err)
	}
	if err := db.SetConfig(svc.db, "ai_chat_model", "test-model"); err != nil {
		t.Fatal(err)
	}

	// A prefix of a JSON array: valid text, not valid structure — exactly the
	// case that used to be indistinguishable from a malformed reply. Built with
	// encoding/json rather than a hand-escaped literal, which is where this kind
	// of fixture usually goes wrong.
	reply = mustJSON("length", `[{"type":"create_page","title":"很长的标题"}`)
	ans, err := svc.AskAIWithEvidence(context.Background(), 0, "问题", EvidenceBudget{MaxItems: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ans.Truncated {
		t.Error("Truncated = false for a finish_reason=length reply, want true")
	}

	reply = mustJSON("stop", "完整答案")
	ans, err = svc.AskAIWithEvidence(context.Background(), 0, "问题", EvidenceBudget{MaxItems: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ans.Truncated {
		t.Error("Truncated = true for a complete reply, want false")
	}
	if ans.Reply != "完整答案" {
		t.Errorf("Reply = %q, want 完整答案", ans.Reply)
	}
}

// mustJSON renders one OpenAI-compatible chat response.
func mustJSON(finishReason, content string) string {
	b, err := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"finish_reason": finishReason,
			"message":       map[string]any{"content": content},
		}},
	})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// The evidence Q&A path sends the layered project context plus up to 12 retrieved
// items — the same order of payload as a compile request, measured at 173s on a
// local 27B. Under the plain chat ceiling a legitimate answer was reported as a
// timeout, so this pins the ceiling to the one that fits the payload.
func TestAskAITimeoutFitsTheEvidencePayload(t *testing.T) {
	if aiAskTimeout <= aiclient.ChatTimeout {
		t.Errorf("aiAskTimeout (%v) must exceed the plain-chat ceiling (%v): the evidence path sends a far larger prompt",
			aiAskTimeout, aiclient.ChatTimeout)
	}
	// It still has to be a ceiling: an unbounded interactive call is the failure
	// mode aiclient.ChatTimeout exists to prevent.
	if aiAskTimeout > DefaultBatchChatTimeout {
		t.Errorf("aiAskTimeout (%v) must not exceed the batch ceiling (%v); the HTTP write deadline is derived from the latter",
			aiAskTimeout, DefaultBatchChatTimeout)
	}
}

// A slow-but-in-budget answer must arrive, not be reported as a timeout. Uses a
// stub slower than the plain-chat ceiling would allow but well inside aiAskTimeout,
// with the real ceiling in force — the regression this replaces was a false
// "failed" on work the model was still doing.
func TestAskAIWithEvidence_SlowAnswerStillArrives(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Longer than aiclient.ChatTimeout would allow, far shorter than aiAskTimeout.
		time.Sleep(150 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(mustJSON("stop", "慢但完整的答案")))
	}))
	defer srv.Close()

	svc, _ := setupService(t)
	if err := db.SetConfig(svc.db, "ai_chat_base_url", srv.URL); err != nil {
		t.Fatal(err)
	}
	if err := db.SetConfig(svc.db, "ai_chat_model", "test-model"); err != nil {
		t.Fatal(err)
	}
	ans, err := svc.AskAIWithEvidence(context.Background(), 0, "问题", EvidenceBudget{MaxItems: 1})
	if err != nil {
		t.Fatalf("a slow answer inside the ceiling must not fail: %v", err)
	}
	if ans.Reply != "慢但完整的答案" {
		t.Errorf("Reply = %q", ans.Reply)
	}
}
