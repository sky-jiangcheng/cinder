package aiclient

import (
	"encoding/json"
	"strings"
	"testing"
)

// SSE parsing is a pure function on purpose (see ParseSSEStream doc): these
// tests feed strings, not a live endpoint, so they run everywhere — the
// httptest loopback is unavailable in the sandbox, and end-to-end-only test
// coverage would leave chunking logic unverified.

func TestParseSSEStreamAssemblesDeltas(t *testing.T) {
	in := "data: {\"choices\":[{\"delta\":{\"content\":\"你\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"好\"}}]}\n\n" +
		"data: [DONE]\n\n"
	var got strings.Builder
	reason, err := ParseSSEStream(strings.NewReader(in), func(d string) { got.WriteString(d) })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.String() != "你好" {
		t.Fatalf("deltas = %q, want 你好", got.String())
	}
	if reason != "" {
		t.Fatalf("finish reason = %q, want empty (no reason chunk)", reason)
	}
}

func TestParseSSEStreamTracksLastFinishReason(t *testing.T) {
	in := "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"b\"},\"finish_reason\":\"length\"}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	reason, err := ParseSSEStream(strings.NewReader(in), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The last non-empty reason wins — a server that emits "length" then
	// "stop" on a final chunk ends as "stop".
	if reason != "stop" {
		t.Fatalf("finish reason = %q, want stop", reason)
	}
}

func TestParseSSEStreamStopsAtDoneWithoutNewline(t *testing.T) {
	// Some servers write chunks without a trailing blank line; the parser must
	// not treat that as truncation.
	in := "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\ndata: [DONE]"
	var got strings.Builder
	_, err := ParseSSEStream(strings.NewReader(in), func(d string) { got.WriteString(d) })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.String() != "x" {
		t.Fatalf("deltas = %q, want x", got.String())
	}
}

func TestParseSSEStreamReportsErrorPayload(t *testing.T) {
	in := "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n" +
		"data: {\"error\":{\"message\":\"context length exceeded\"}}\n\n"
	var got strings.Builder
	_, err := ParseSSEStream(strings.NewReader(in), func(d string) { got.WriteString(d) })
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "context length exceeded") {
		t.Fatalf("error = %v, want to carry the server message", err)
	}
	if got.String() != "partial" {
		t.Fatalf("deltas before failure = %q, want partial", got.String())
	}
}

func TestParseSSEStreamRejectsMalformedChunk(t *testing.T) {
	in := "data: {not json\n\n"
	_, err := ParseSSEStream(strings.NewReader(in), nil)
	if err == nil {
		t.Fatal("expected error on malformed chunk, got nil")
	}
}

func TestParseSSEStreamIgnoresCommentsAndEmptyLines(t *testing.T) {
	in := ": keep-alive comment\n\n" +
		"event: message\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n" +
		"\n"
	var got strings.Builder
	_, err := ParseSSEStream(strings.NewReader(in), func(d string) { got.WriteString(d) })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.String() != "ok" {
		t.Fatalf("deltas = %q, want ok", got.String())
	}
}

// StreamRequest must serialize with stream:true — the flag is the whole
// point of the request shape, and a missing/normalized-away flag would silently
// turn the ask back into a non-streaming call the UI never signed up for.
func TestChatStreamRequestCarriesStreamFlag(t *testing.T) {
	raw, err := json.Marshal(StreamRequest{Model: "m", Stream: true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"stream":true`) {
		t.Fatalf("stream flag missing from request body: %s", raw)
	}
	if !strings.Contains(string(raw), `"model":"m"`) {
		t.Fatalf("model missing from request body: %s", raw)
	}
}
