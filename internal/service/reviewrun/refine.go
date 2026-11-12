package reviewrun

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"repotory/internal/review"
)

// Model review is P2 of ADR-0053: the rules only ever screen candidates, and
// the model pass is what turns a candidate into a verdict. Two properties
// matter more than the prompt itself:
//
//  1. Zero candidates must cost zero calls — a daily job that pings a paid
//     endpoint to be told "nothing to report" is a bug the user cannot see.
//  2. The model cannot invent findings. It only sees the candidates the rules
//     already produced, and its only jobs are confirm / dismiss plus a reason.
//     A reply that names an index we did not send is ignored, so a
//     hallucinated finding can never reach the report.
//
// Property 2 is enforced here rather than in the service refiner, because it is
// a rule about the report, not about the HTTP client: the refiner answers about
// a chunk it was handed, and this file is what decides whether that answer may
// touch anything.

const (
	// aiChunk caps how many candidates go into one call. Above this the
	// candidates compete for the model's attention and the answers get lazy;
	// the ADR's threshold was "split when a file has more than 5".
	aiChunk = 5
	// fileBudget bounds how much of the file is pasted into the prompt. A
	// whole 4000-line service file would cost more than the review is worth,
	// and the candidates already carry the local context that made them fire.
	fileBudget = 12000
)

// Verdict is one model answer about one candidate. Index refers to the position
// in the chunk that was sent, never to a database id.
type Verdict struct {
	Index      int    `json:"index"`
	Verdict    string `json:"verdict"`
	Reason     string `json:"reason"`
	Suggestion string `json:"suggestion"`
}

// SystemPrompt is deliberately narrow. The failure mode to avoid is a model
// that "improves" the review by finding new problems: those arrive with no rule
// id, no line, no reproducible trigger, and they are exactly the unfalsifiable
// output this design exists to prevent.
const SystemPrompt = `You are reviewing code changes that were already flagged by deterministic rules.
For each candidate, decide whether it is a real problem using the changed file and any provider/consumer evidence from other repositories:
- "confirmed": the code as written will misbehave at runtime, and the claim is accurate.
- "dismissed": the claim does not hold here (legitimate usage, already handled elsewhere, dead code, framework version where it works).
Do not invent new findings. Do not restate the rule. One short reason, one short fix.
Answer with JSON only, no prose and no code fences:
{"verdicts":[{"index":0,"verdict":"confirmed","reason":"...","suggestion":"..."}]}`

// Prompt builds the two messages. The candidate list is JSON-shaped so the
// model can echo indices reliably; the file is pasted after it because long
// context at the end is what models actually attend to.
func Prompt(path, content string, chunk []review.Finding) (system, user string) {
	var b strings.Builder
	b.WriteString("Candidates:\n")
	for i, f := range chunk {
		fmt.Fprintf(&b, "%d. rule=%s severity=%s line=%d\n   claim: %s\n   evidence:\n",
			i, f.Rule, f.Severity, f.Line, f.Message)
		for _, line := range strings.Split(f.Evidence, "\n") {
			b.WriteString("     " + line + "\n")
		}
	}
	if len(content) > fileBudget {
		content = content[:fileBudget] + "\n... (truncated)"
	}
	fmt.Fprintf(&b, "\nFile: %s\n```java\n%s\n```\n", path, content)
	return SystemPrompt, b.String()
}

// ParseVerdicts tolerates the three shapes models actually return: the
// documented object, a bare array (some models drop the wrapper), and either of
// those wrapped in prose or a code fence. Failing to parse leaves the rules'
// verdicts in place, which is the honest outcome.
func ParseVerdicts(reply string) []Verdict {
	// The array is tried first: its brackets are unambiguous, while a `{` may
	// belong to a nested object inside an array.
	if body := between(reply, '[', ']'); body != "" {
		var arr []Verdict
		if err := json.Unmarshal([]byte(body), &arr); err == nil {
			return arr
		}
	}
	if body := between(reply, '{', '}'); body != "" {
		var wrap struct {
			Verdicts []Verdict `json:"verdicts"`
		}
		if err := json.Unmarshal([]byte(body), &wrap); err == nil {
			return wrap.Verdicts
		}
	}
	return nil
}

// Refine runs the model pass over one file's findings. It is the database-free
// entry point to the same chunking rules Runner uses, so a caller that only has
// a refiner (a test, a future single-file preview) cannot grow a second
// implementation of "the model may not invent findings".
func Refine(ctx context.Context, refiner Refiner, path, content string, findings []review.Finding) ([]review.Finding, int, int) {
	r := &Runner{refiner: refiner}
	return r.refine(ctx, path, content, findings)
}

// refine is the model pass for one file. It returns the findings with verdicts
// applied, the number of calls that actually succeeded, and the number of
// failed calls. Only successful calls count toward ai_calls, so a run whose
// endpoint kept failing never looks fully reviewed.
//
// A failure is deliberately soft: the rules' output is already a usable report,
// so an unreachable endpoint degrades the report (candidates stay unverified)
// instead of losing the day.
func (r *Runner) refine(ctx context.Context, path, content string, findings []review.Finding) ([]review.Finding, int, int) {
	if r.refiner == nil || len(findings) == 0 {
		return findings, 0, 0
	}
	calls := 0
	failures := 0
	for start := 0; start < len(findings); start += aiChunk {
		end := start + aiChunk
		if end > len(findings) {
			end = len(findings)
		}
		if err := ctx.Err(); err != nil {
			return findings, calls, failures
		}
		chunk := findings[start:end]
		verdicts, err := r.refiner.Verdicts(ctx, path, content, chunk)
		if err != nil {
			// "Not configured" is not a failed call: the run is rule-only and
			// must not be labelled as partially reviewed. It is also not worth
			// retrying per file, so it ends the pass for this file.
			if isUnavailable(err) {
				log.Printf("review: model pass skipped for %s: %v", path, err)
				return findings, calls, failures
			}
			log.Printf("review: model pass failed for %s: %v", path, err)
			failures++
			continue
		}
		calls++
		for _, v := range verdicts {
			if v.Index < 0 || v.Index >= len(chunk) {
				continue // a hallucinated index must not touch anything
			}
			target := &findings[start+v.Index]
			switch strings.ToLower(strings.TrimSpace(v.Verdict)) {
			case string(review.VerdictConfirmed):
				target.Verdict = review.VerdictConfirmed
			case string(review.VerdictDismissed):
				target.Verdict = review.VerdictDismissed
			default:
				continue // "unverified" or nonsense: leave the rule's verdict
			}
			if reason := strings.TrimSpace(v.Reason); reason != "" {
				target.Message = oneLineJoin(target.Message, reason)
			}
			if s := strings.TrimSpace(v.Suggestion); s != "" {
				target.Suggestion = oneLineJoin(target.Suggestion, s)
			}
		}
	}
	return findings, calls, failures
}

// between returns the substring from the first open to the last close,
// inclusive, or "" when the delimiters are missing.
func between(s string, open, close byte) string {
	i := strings.IndexByte(s, open)
	j := strings.LastIndexByte(s, close)
	if i < 0 || j <= i {
		return ""
	}
	return s[i : j+1]
}

// oneLineJoin appends a second sentence to a finding's text, collapsing
// whitespace so the result still fits a table cell.
func oneLineJoin(a, b string) string {
	a = strings.Join(strings.Fields(a), " ")
	b = strings.Join(strings.Fields(b), " ")
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + " · " + b
}
