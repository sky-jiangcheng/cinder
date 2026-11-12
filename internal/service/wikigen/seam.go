// Package wikigen is the model-driven half of the wiki write side: ingest-time
// compilation (ADR-0015/0016), merge-suggest (ADR-0019) and community synthesis
// (ADR-0020). It split out of internal/service in ADR-0060 step three, by the
// same strangler rules as reviewrun and wikipage before it:
//
//   - nothing here takes a *Service; dependencies arrive as parameters
//     (*sql.DB plus one Chat seam), so the package cannot grow back into the
//     monolith it left;
//   - the Service keeps its facade methods — bindings, MCP tools and every
//     existing test call the same names as before;
//   - the no-cycle rule is mechanical: this package must never import
//     internal/service, and `go list -deps` is how that is checked.
//
// # The seam
//
// Every model call here used to be the same three lines — resolve
// aiChatConfig, build two messages, call chatWithAudit — wrapped around three
// different contracts. ADR-0060 asked whether this group should reuse
// reviewrun.Refiner's interface type. It cannot, literally: Refiner's signature
// is typed on review.Finding and Verdict. The real choice was between a
// domain-shaped interface per kernel (the reviewrun template) and one narrow
// transport seam shared by the group. The transport seam won, for a reason
// worth restating where the next reader will find it:
//
// Adjudication (review) can fail to parse silently — an unreadable reply keeps
// the rules' verdicts, so parse can live behind a domain interface and the
// runner never needs to explain anything. Generation cannot. Here, an
// unparseable reply IS part of the output: each kernel owes the user a
// different sentence ("本次不写入任何东西", "本次不采信任何合并建议",
// "建档但正文留空"), and telling a truncated reply from a malformed one needs
// the raw FinishReason, not a verdict. A domain-named interface would either
// have to return raw text (a JudgePairs that delivers prose is a lie) or drag
// parse-failure phrasing across the package boundary. So the seam is honest
// about being transport: messages in, text plus finish_reason out.
//
// What deliberately does NOT cross the seam:
//
//   - tool calls. Message carries Role and Content and nothing else, the same
//     way compileOp's vocabulary contains no "update page" — the contract
//     cannot ask for what the type does not have. A generation kernel that
//     cannot express a tool call cannot grow one by accident;
//   - configuration. Endpoint, key and the ADR-0030 audit row stay in
//     internal/service, reached through Available and Chat;
//   - budget policy. "Zero candidates, zero calls" (the ADR-0053 D2 property
//     review's runner pins, merge's empty-candidate return, compile's page
//     ceilings, community's wall clock) lives in the kernels below, exactly
//     where it lived before the split. The seam carries neither the budgets
//     nor the gates; the kernels' tests can still assert them with a counting
//     fake.
package wikigen

import (
	"context"
	"errors"
	"time"

	"repotory/internal/service/wikipage"
)

// BatchTimeout bounds one non-interactive model call from this package.
//
// It used to be service.DefaultBatchChatTimeout (ai.go), defined there because
// cmd/server derives its HTTP write deadline from it and asserts on the
// relationship. The value exists because of THIS package's calls — compile on
// a local 27B model measured 173s, which is why the interactive ceiling (90s)
// is wrong here — so the definition moved with the callers. The service keeps
// `DefaultBatchChatTimeout = BatchTimeout` as an alias, so cmd/server's
// assertion and every existing reference keep working unchanged.
const BatchTimeout = 10 * time.Minute

// Message is one chat message as the generation kernels may express it: role
// and content, nothing else. The transport type inside internal/service has
// tool-call fields; this one does not, on purpose — see the package comment.
type Message struct {
	Role    string
	Content string
}

// Reply is a completed model answer plus the server's own verdict on it.
// FinishReason is what lets a kernel tell a truncated JSON array from a
// malformed one — they look identical as text and call for opposite user
// advice (raise the output budget vs. switch model). See TruncatedHint.
type Reply struct {
	Text         string
	FinishReason string
}

// ErrNotConfigured marks "the model pass cannot run at all here" — no
// endpoint, no model, nothing a retry would change. It is the wikigen twin of
// reviewrun.ErrUnavailable and of service.ErrAIChatNotConfigured: kernels
// translate it into their own user-facing note ("AI 端点未配置：…") instead of
// counting it as a failed call, because a run that never reached a model must
// not read as a run whose calls failed.
var ErrNotConfigured = errors.New("model pass is not configured")

// Chat is the one seam between the kernels in this package and the audited
// transport in internal/service. Everything endpoint-shaped — config
// resolution, the API key, the audit trail — sits behind it.
//
// Available answers "is there an endpoint configured at all" before any work
// is done. The kernels gate on it first (compile and community produce their
// "AI 端点未配置" note before counting notes scanned; merge skips before even
// reading the candidate list), which is the exact control-flow order the
// pre-split code had. Any configuration failure maps to ErrNotConfigured.
//
// The context parameters are honoured by future transports; the current one
// finishes in-flight calls regardless of cancellation, a property ADR-0016
// states on purpose (aborting a request saves no tokens and loses the answer).
type Chat interface {
	Available(ctx context.Context) error
	Chat(ctx context.Context, msgs []Message) (Reply, error)
}

// TruncatedHint turns a non-"stop" finish_reason into the one sentence the user
// can act on. Reasoning models routinely spend the output budget on hidden
// tokens (reasoning_tokens=1600 in a real run here), so this fires on ordinary
// configurations, not only exotic ones.
//
// Returns "" when the reply completed normally — callers use that to keep the
// old "cannot parse" wording, which is still correct for a reply that really was
// malformed rather than cut short.
//
// It moved here from service/ai.go with the seam: every kernel that receives a
// Reply needs it, and the service-side wrapper keeps the old callers (lint's
// model half, evidence, note organizing) and their tests compiling unchanged.
func TruncatedHint(finishReason string) string {
	switch finishReason {
	case "", "stop", "end_turn", "stop_sequence":
		return ""
	case "length", "max_tokens", "model_length":
		return "（回复被输出上限截断，并非格式错误：请换用输出预算更大的模型，或提高 max_tokens 后重试）"
	case "content_filter":
		return "（回复被内容过滤器拦截，请调整提问方式后重试）"
	default:
		return "（模型以 " + finishReason + " 结束本轮回复，未输出完整结果）"
	}
}

// clipRunes is the rune-safe truncation the kernels have always called by this
// name. The definition lives in wikipage as ClipRunes — this package is the
// "third caller" whose arrival the pre-split comment said would be the moment
// to stop keeping copies — and the alias keeps the moved code byte-identical.
func clipRunes(s string, max int) string {
	return wikipage.ClipRunes(s, max)
}

// firstNonEmpty is the two-arg empty-string coalesce. There is another copy in
// internal/service/llm.go; this is the second, kept for the same reason the
// pre-split code kept two clipRunes — three lines, and a shared textutil
// package would cost more indirection than the duplication does.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// truncateBytes is the byte-wise twin of clipRunes, kept as a name the kernels
// have always called. The definition lives in wikipage as TruncateBytes (ADR-0060
// step four extracted it when wikilint became the third package needing it); the
// alias keeps the moved code byte-identical.
func truncateBytes(s string, max int) string {
	return wikipage.TruncateBytes(s, max)
}
