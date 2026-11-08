package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"repotory/internal/db"
	"repotory/internal/review"
)

// countingHandler answers every chat request with reply and counts them. The
// count IS the test in the "zero candidates, zero calls" case: cost is the one
// property of the model pass a user can check for themselves.
func countingHandler(n *int32, reply string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(n, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}
}

func TestRefineSendsNothingWithoutCandidates(t *testing.T) {
	var calls int32
	svc := newAuditTestService(t, countingHandler(&calls, `{"verdicts":[]}`))

	got, n := svc.refineWithCandidates(context.Background(), "a/Foo.java", "class Foo {}", nil)
	if len(got) != 0 || n != 0 {
		t.Fatalf("no candidates should mean no call: findings=%v calls=%d", got, n)
	}
	if calls != 0 {
		t.Fatalf("http requests = %d, want 0 — candidate-free runs must not cost money", calls)
	}
}

func TestRefineAppliesVerdicts(t *testing.T) {
	var calls int32
	reply := `{"verdicts":[
		{"index":0,"verdict":"confirmed","reason":"proxy drops the body","suggestion":"add @RequestBody"},
		{"index":1,"verdict":"dismissed","reason":"provider reads it as a query param","suggestion":""}
	]}`
	svc := newAuditTestService(t, countingHandler(&calls, reply))

	in := []review.Finding{
		{Rule: "feign_missing_body", Severity: review.SeverityHigh, Verdict: review.VerdictUnverified,
			Line: 7, Evidence: "> Result<X> y(Req r);", Message: "no binder annotation"},
		{Rule: "feign_missing_body", Severity: review.SeverityMedium, Verdict: review.VerdictUnverified,
			Line: 12, Evidence: "> Result<X> z(Req r);", Message: "no binder annotation"},
	}
	got, n := svc.refineWithCandidates(context.Background(), "a/PayClient.java", "class X {}", in)
	if n != 1 || calls != 1 {
		t.Fatalf("want exactly one call, got %d (requests %d)", n, calls)
	}
	if got[0].Verdict != review.VerdictConfirmed {
		t.Errorf("index 0 verdict = %q, want confirmed", got[0].Verdict)
	}
	if !strings.Contains(got[0].Message, "proxy drops the body") {
		t.Errorf("attribution missing from message: %q", got[0].Message)
	}
	if got[0].Suggestion != "add @RequestBody" {
		t.Errorf("suggestion = %q", got[0].Suggestion)
	}
	if got[1].Verdict != review.VerdictDismissed {
		t.Errorf("index 1 verdict = %q, want dismissed", got[1].Verdict)
	}
	// A dismissed candidate keeps its line: the report still shows what the
	// rule saw, so a wrongly dismissed finding can be spotted later.
	if got[1].Line != 12 {
		t.Errorf("line = %d, want 12", got[1].Line)
	}
}

func TestRefineIgnoresUnknownIndex(t *testing.T) {
	var calls int32
	svc := newAuditTestService(t, countingHandler(&calls,
		`{"verdicts":[{"index":7,"verdict":"confirmed","reason":"made up"},{"index":-1,"verdict":"confirmed"}]}`))

	in := []review.Finding{{Rule: "r", Severity: review.SeverityHigh, Verdict: review.VerdictUnverified, Line: 1}}
	got, _ := svc.refineWithCandidates(context.Background(), "a/B.java", "class B {}", in)
	if got[0].Verdict != review.VerdictUnverified {
		t.Fatalf("an out-of-range index must not change anything: %+v", got[0])
	}
}

func TestRefineSurvivesBadReply(t *testing.T) {
	var calls int32
	svc := newAuditTestService(t, countingHandler(&calls, "I think this is fine, no JSON here"))
	in := []review.Finding{{Rule: "r", Severity: review.SeverityHigh, Verdict: review.VerdictUnverified, Line: 1}}
	got, n := svc.refineWithCandidates(context.Background(), "a/B.java", "class B {}", in)
	if n != 1 {
		t.Errorf("the call still cost money, so it must be counted: %d", n)
	}
	if got[0].Verdict != review.VerdictUnverified {
		t.Errorf("unparseable reply must leave the rule verdict: %+v", got[0])
	}
}

func TestParseReviewVerdictsShapes(t *testing.T) {
	cases := map[string]int{
		`{"verdicts":[{"index":0,"verdict":"confirmed"}]}`:                       1,
		"```json\n{\"verdicts\":[{\"index\":0,\"verdict\":\"dismissed\"}]}\n```": 1,
		`[{"index":0,"verdict":"confirmed"}]`:                                    1,
		`not json`:                                                               0,
	}
	for reply, want := range cases {
		if got := parseReviewVerdicts(reply); len(got) != want {
			t.Errorf("parseReviewVerdicts(%q) = %d verdicts, want %d", reply, len(got), want)
		}
	}
}

// End-to-end with a model: the stored verdict reflects the model's answer and
// the run records one call. This is the only test that proves the wiring, as
// opposed to the pieces.
func TestRunReviewUsesModelWhenEnabled(t *testing.T) {
	var calls int32
	svc := newAuditTestService(t, countingHandler(&calls,
		`{"verdicts":[{"index":0,"verdict":"confirmed","reason":"the body is never sent"}]}`))
	dir := reviewRepo(t)
	collectedProject(t, svc, dir)

	result, err := svc.RunReview(context.Background(), ReviewOptions{Date: time.Now().AddDate(0, 0, -1)})
	if err != nil {
		t.Fatalf("RunReview: %v", err)
	}
	if result.Run.AICalls != 1 {
		t.Errorf("ai_calls = %d, want 1", result.Run.AICalls)
	}
	if calls != 1 {
		t.Errorf("http requests = %d, want 1", calls)
	}
	var confirmed bool
	for _, f := range result.Stored {
		if f.Rule == "feign_missing_body" && f.Verdict == string(review.VerdictConfirmed) {
			confirmed = true
		}
	}
	if !confirmed {
		t.Errorf("model confirmation did not reach the report: %+v", result.Stored)
	}

	// NoAI must beat the config: the same day, forced, with zero calls.
	if err := db.SetConfig(svc.db, reviewAIKey, "0"); err != nil {
		t.Fatal(err)
	}
	forced, err := svc.RunReview(context.Background(), ReviewOptions{
		Date: time.Now().AddDate(0, 0, -1), Force: true, NoAI: true,
	})
	if err != nil {
		t.Fatalf("forced run: %v", err)
	}
	if forced.Run.AICalls != 0 || calls != 1 {
		t.Errorf("NoAI run made calls: ai_calls=%d requests=%d", forced.Run.AICalls, calls)
	}
}
