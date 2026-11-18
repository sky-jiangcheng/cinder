package reviewrun

import (
	"context"
	"errors"
	"strings"
	"testing"

	"repotory/internal/review"
)

// fakeRefiner stands in for the model pass so the verdict-application rules can
// be tested without an endpoint. The interesting behaviour here is not "does it
// call" but "what does it do with an answer it must not fully trust".
type fakeRefiner struct {
	verdicts []Verdict
	err      error
	calls    int
}

func (f *fakeRefiner) Verdicts(ctx context.Context, path, content string, chunk []review.Finding) ([]Verdict, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.verdicts, nil
}

func findings(n int) []review.Finding {
	out := make([]review.Finding, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, review.Finding{
			Rule:     "rule_x",
			Severity: "warn",
			Line:     i + 1,
			Message:  "the rule says this is a problem",
			Verdict:  review.VerdictUnverified,
		})
	}
	return out
}

// Models return three shapes in practice: the documented object, a bare array,
// and either of those wrapped in prose or a fence. All three must parse, and
// garbage must parse to nothing rather than to a half-filled verdict.
func TestParseVerdictsShapes(t *testing.T) {
	cases := map[string]struct {
		reply string
		want  int
	}{
		"object":    {`{"verdicts":[{"index":0,"verdict":"confirmed","reason":"r"}]}`, 1},
		"array":     {`[{"index":0,"verdict":"dismissed","reason":"r"}]`, 1},
		"fenced":    {"```json\n{\"verdicts\":[{\"index\":1,\"verdict\":\"confirmed\"}]}\n```", 1},
		"prose+arr": {`Here you go: [{"index":0,"verdict":"confirmed"}] hope this helps`, 1},
		"prose":     {`I could not review this file.`, 0},
		"empty":     {``, 0},
	}
	for name, tc := range cases {
		if got := ParseVerdicts(tc.reply); len(got) != tc.want {
			t.Fatalf("%s: parsed %d verdict(s), want %d (%q)", name, len(got), tc.want, tc.reply)
		}
	}
}

// A verdict only moves a finding the model was actually shown, only to the two
// words the report understands, and only adds text — it never replaces the
// rule's claim. A hallucinated index is ignored, not clamped.
func TestRefineAppliesOnlyValidVerdicts(t *testing.T) {
	in := findings(2)
	refiner := &fakeRefiner{verdicts: []Verdict{
		{Index: 0, Verdict: "confirmed", Reason: "provider returns 204 here", Suggestion: "handle it"},
		{Index: 1, Verdict: "maybe", Reason: "nonsense"},
		{Index: 99, Verdict: "confirmed", Reason: "out of range"},
	}}
	out, calls, failures := Refine(context.Background(), refiner, "src/A.java", "class A {}", in)

	if calls != 1 || failures != 0 {
		t.Fatalf("calls=%d failures=%d, want 1/0", calls, failures)
	}
	if out[0].Verdict != review.VerdictConfirmed {
		t.Fatalf("verdict=%q, want confirmed", out[0].Verdict)
	}
	if !strings.Contains(out[0].Message, "the rule says this is a problem") ||
		!strings.Contains(out[0].Message, "provider returns 204 here") {
		t.Fatalf("message=%q, want the rule's claim plus the reason", out[0].Message)
	}
	if out[0].Suggestion != "handle it" {
		t.Fatalf("suggestion=%q", out[0].Suggestion)
	}
	// "maybe" is not a verdict: the rule's judgement stands.
	if out[1].Verdict != review.VerdictUnverified {
		t.Fatalf("verdict=%q, want the rule's unverified left alone", out[1].Verdict)
	}
}

// "Not configured" is not a failed call: the run is rule-only and must not be
// counted as partially reviewed. An actual error is a failed call, and the
// rule's findings still come back usable.
func TestRefineDistinguishesUnavailableFromFailure(t *testing.T) {
	in := findings(2)

	unavailable, calls, failures := Refine(context.Background(), &fakeRefiner{err: ErrUnavailable}, "f", "c", in)
	if calls != 0 || failures != 0 {
		t.Fatalf("unavailable: calls=%d failures=%d, want 0/0", calls, failures)
	}
	if len(unavailable) != 2 || unavailable[0].Verdict != review.VerdictUnverified {
		t.Fatalf("the rule-only report must survive untouched")
	}

	broken, calls, failures := Refine(context.Background(), &fakeRefiner{err: errors.New("502")}, "f", "c", findings(2))
	if calls != 0 || failures != 1 {
		t.Fatalf("failure: calls=%d failures=%d, want 0/1", calls, failures)
	}
	if len(broken) != 2 {
		t.Fatalf("findings=%d, want the rules' output to survive a dead endpoint", len(broken))
	}
}

// The chunking contract: five findings per call, and an index is resolved
// against the chunk it was sent with, not against the whole file.
func TestRefineChunksResolveIndicesPerChunk(t *testing.T) {
	in := findings(6)
	refiner := &fakeRefiner{verdicts: []Verdict{{Index: 0, Verdict: "confirmed", Reason: "first of this chunk"}}}
	out, calls, _ := Refine(context.Background(), refiner, "f", "c", in)
	if calls != 2 {
		t.Fatalf("calls=%d, want 2 chunks for 6 findings", calls)
	}
	if out[0].Verdict != review.VerdictConfirmed {
		t.Fatalf("index 0 of chunk 1 did not land on finding 0")
	}
	if out[5].Verdict != review.VerdictConfirmed {
		t.Fatalf("index 0 of chunk 2 did not land on finding 5")
	}
	if out[1].Verdict != review.VerdictUnverified {
		t.Fatalf("finding 1 must not be touched by a different chunk's verdict")
	}
}
