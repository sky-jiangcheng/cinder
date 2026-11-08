package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Function Calling loop (see aitools.go). The behaviours pinned here are the
// ones the protocol makes easy to get silently wrong:
//
//  1. The assistant echo of a tool-calling turn carries the tool_calls —
//     a tool message is only defined as the response to them, and strict
//     providers reject the pairing when the assistant half is missing.
//  2. A streamed tool_call reassembles from argument fragments and echoes
//     with the same id the UI was shown.
//  3. The round cap forces one final text-only round instead of returning
//     a tool-calling turn (which would surface as an empty reply).

// toolCallTurn is one provider answer that asks for list_projects.
func toolCallTurn(id string) map[string]any {
	return map[string]any{
		"choices": []map[string]any{{
			"message": map[string]any{
				"content": "",
				"tool_calls": []map[string]any{{
					"id":   id,
					"type": "function",
					"function": map[string]any{
						"name":      "list_projects",
						"arguments": "{}",
					},
				}},
			},
			"finish_reason": "tool_calls",
		}},
	}
}

// textTurn is one provider answer that speaks in words.
func textTurn(reply string) map[string]any {
	return map[string]any{
		"choices": []map[string]any{{
			"message":       map[string]any{"content": reply},
			"finish_reason": "stop",
		}},
	}
}

func respondJSON(t *testing.T, w http.ResponseWriter, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Fatalf("encode stub response: %v", err)
	}
}

// messageAt walks the decoded request body's messages array.
func messageAt(t *testing.T, body map[string]any, i int) map[string]any {
	t.Helper()
	messages, ok := body["messages"].([]any)
	if !ok {
		t.Fatalf("request carried no messages array")
	}
	if i >= len(messages) {
		t.Fatalf("request had %d messages, wanted index %d", len(messages), i)
	}
	message, ok := messages[i].(map[string]any)
	if !ok {
		t.Fatalf("message %d is not an object", i)
	}
	return message
}

func TestAskWithToolsEchoesToolCallsBackToTheProvider(t *testing.T) {
	requests := 0
	var followUp map[string]any
	handler := func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if requests == 1 {
			respondJSON(t, w, toolCallTurn("call_1"))
			return
		}
		followUp = body
		respondJSON(t, w, textTurn("two projects"))
	}
	svc := newAuditTestService(t, handler)
	cfg := mustCfg(t, svc)

	reply, _, err := svc.askWithTools(cfg, []chatMessage{{Role: "user", Content: "how many projects?"}}, svc.toolRegistry.definitions(), 5*time.Second)
	if err != nil {
		t.Fatalf("askWithTools: %v", err)
	}
	if reply != "two projects" {
		t.Fatalf("reply = %q, want the second turn's text", reply)
	}
	if requests != 2 {
		t.Fatalf("provider saw %d requests, want 2", requests)
	}

	// The echoed assistant turn must carry the tool_calls it emitted, and the
	// tool result must reference the same id — the pairing strict providers
	// validate.
	assistant := messageAt(t, followUp, 1)
	if assistant["role"] != "assistant" {
		t.Fatalf("message 1 role = %v, want assistant", assistant["role"])
	}
	calls, ok := assistant["tool_calls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("assistant echo carries no tool_calls: %v", assistant["tool_calls"])
	}
	call, _ := calls[0].(map[string]any)
	if call["id"] != "call_1" {
		t.Fatalf("echoed tool_call id = %v, want call_1", call["id"])
	}
	tool := messageAt(t, followUp, 2)
	if tool["role"] != "tool" || tool["tool_call_id"] != "call_1" {
		t.Fatalf("tool message not paired with call_1: %v", tool)
	}
}

func TestStreamAskWithToolsReassemblesFragmentsAndEchoesThem(t *testing.T) {
	requests := 0
	var followUp map[string]any
	var gotCalls [][2]string
	var gotResults []string
	var gotDeltas strings.Builder
	handler := func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests > 1 {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			followUp = body
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if requests == 1 {
			// The tool_call arrives as fragments across chunks: id and name in
			// the first, the arguments split over two more, the finish reason
			// on its own.
			_, _ = w.Write([]byte("data: " + stubChunk(t, map[string]any{
				"choices": []map[string]any{{"delta": map[string]any{
					"tool_calls": []map[string]any{{
						"index": 0, "id": "call_9", "type": "function",
						"function": map[string]any{"name": "list_projects", "arguments": ""},
					}},
				}}},
			}) + "\n\n"))
			_, _ = w.Write([]byte("data: " + stubChunk(t, map[string]any{
				"choices": []map[string]any{{"delta": map[string]any{
					"tool_calls": []map[string]any{{
						"index":    0,
						"function": map[string]any{"arguments": "{}"},
					}},
				}}},
			}) + "\n\n"))
			_, _ = w.Write([]byte("data: " + stubChunk(t, map[string]any{
				"choices": []map[string]any{{"delta": map[string]any{}, "finish_reason": "tool_calls"}},
			}) + "\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			return
		}
		_, _ = w.Write([]byte("data: " + stubChunk(t, map[string]any{
			"choices": []map[string]any{{"delta": map[string]any{"content": "done"}}},
		}) + "\n\n"))
		_, _ = w.Write([]byte("data: " + stubChunk(t, map[string]any{
			"choices": []map[string]any{{"delta": map[string]any{}, "finish_reason": "stop"}},
		}) + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}
	svc := newAuditTestService(t, handler)
	cfg := mustCfg(t, svc)

	reply, _, err := svc.streamAskWithTools(
		cfg,
		[]chatMessage{{Role: "user", Content: "how many projects?"}},
		svc.toolRegistry.definitions(),
		func(delta string) { gotDeltas.WriteString(delta) },
		func(id, name string) { gotCalls = append(gotCalls, [2]string{id, name}) },
		func(id string, _ any) { gotResults = append(gotResults, id) },
		5*time.Second,
	)
	if err != nil {
		t.Fatalf("streamAskWithTools: %v", err)
	}
	if reply != "done" {
		t.Fatalf("reply = %q, want the second round's text", reply)
	}
	if len(gotCalls) != 1 || gotCalls[0] != [2]string{"call_9", "list_projects"} {
		t.Fatalf("onToolCall = %v, want one [call_9 list_projects]", gotCalls)
	}
	if len(gotResults) != 1 || gotResults[0] != "call_9" {
		t.Fatalf("onToolResult ids = %v, want [call_9]", gotResults)
	}
	if gotDeltas.String() != "done" {
		t.Fatalf("streamed deltas = %q, want only the final round's text", gotDeltas.String())
	}

	// The reassembled call echoes with the id the UI was shown.
	assistant := messageAt(t, followUp, 1)
	calls, ok := assistant["tool_calls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("assistant echo carries no tool_calls: %v", assistant["tool_calls"])
	}
	call, _ := calls[0].(map[string]any)
	function, _ := call["function"].(map[string]any)
	if call["id"] != "call_9" || function["name"] != "list_projects" || function["arguments"] != "{}" {
		t.Fatalf("echoed call = %v, want call_9/list_projects/{}", call)
	}
	tool := messageAt(t, followUp, 2)
	if tool["role"] != "tool" || tool["tool_call_id"] != "call_9" {
		t.Fatalf("tool message not paired with call_9: %v", tool)
	}
}

func TestAskWithToolsRoundCapForcesAFinalTextOnlyRound(t *testing.T) {
	toolRequests := 0
	textRequests := 0
	handler := func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if _, hasTools := body["tools"]; !hasTools {
			textRequests++
			respondJSON(t, w, textTurn("final answer"))
			return
		}
		toolRequests++
		respondJSON(t, w, toolCallTurn("call_loop"))
	}
	svc := newAuditTestService(t, handler)
	cfg := mustCfg(t, svc)

	reply, _, err := svc.askWithTools(cfg, []chatMessage{{Role: "user", Content: "loop me"}}, svc.toolRegistry.definitions(), 5*time.Second)
	if err != nil {
		t.Fatalf("askWithTools: %v", err)
	}
	if reply != "final answer" {
		t.Fatalf("reply = %q, want the forced text round's answer", reply)
	}
	if toolRequests != maxToolRounds {
		t.Fatalf("%d tool rounds, want exactly maxToolRounds (%d)", toolRequests, maxToolRounds)
	}
	if textRequests != 1 {
		t.Fatalf("%d text-only rounds, want exactly 1", textRequests)
	}
}

func stubChunk(t *testing.T, body any) string {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal stub chunk: %v", err)
	}
	return string(raw)
}

// TestAskWithToolsResolvesTheBaseURL is the regression for the "AI endpoint
// returned 404: Not Found" the assistant panel showed.
//
// The configured value is a BASE ("https://api.deepseek.com", ".../v1"), not a
// completions endpoint. Every other AI path resolved it through
// aiCandidateURLs; the tool loop posted to the bare base, so a provider whose
// completions route sits one segment deeper answered 404 while Settings → AI
// still called the endpoint healthy.
func TestAskWithToolsResolvesTheBaseURL(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		// Only the real completions route answers; anything else is the 404 a
		// real provider returns for a bare base URL.
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"Not Found","code":404}}`))
			return
		}
		respondJSON(t, w, textTurn("resolved"))
	}
	// nil handler: the helper then leaves the base URL unset, so this test owns
	// it. The base is the ROOT — exactly what a user pastes from a provider's
	// docs page — while the completions route lives one segment deeper.
	svc := newAuditTestService(t, nil)
	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)
	if err := svc.UpdateConfig("ai_chat_base_url", srv.URL); err != nil {
		t.Fatalf("set base url: %v", err)
	}

	reply, _, err := svc.askWithTools(mustCfg(t, svc), []chatMessage{{Role: "user", Content: "hi"}}, nil, 5*time.Second)
	if err != nil {
		t.Fatalf("askWithTools must walk to the working URL, got: %v", err)
	}
	if reply != "resolved" {
		t.Fatalf("reply = %q", reply)
	}
}

// TestStreamAskWithToolsResolvesTheBaseURL is the streaming twin of
// TestAskWithToolsResolvesTheBaseURL, and it is the one that matters.
//
// The assistant panel STREAMS, so the streaming tool loop is the path the
// user actually hits. The batch loop learned to walk the candidate URLs; this
// one still posted to the bare base, which is why the panel kept saying
// "AI endpoint returned 404" while Settings → AI reported a healthy endpoint.
//
// The configured value is the ROOT of the endpoint — exactly what a user
// pastes from a provider's docs page — while the completions route lives one
// segment deeper.
func TestStreamAskWithToolsResolvesTheBaseURL(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"Not Found","code":404}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: " + stubChunk(t, map[string]any{
			"choices": []map[string]any{{"delta": map[string]any{"content": "resolved"}}},
		}) + "\n\n"))
		_, _ = w.Write([]byte("data: " + stubChunk(t, map[string]any{
			"choices": []map[string]any{{"delta": map[string]any{}, "finish_reason": "stop"}},
		}) + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}
	// nil handler: the helper then leaves the base URL unset, so this test owns
	// it — the base is the ROOT, not the completions endpoint.
	svc := newAuditTestService(t, nil)
	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)
	if err := svc.UpdateConfig("ai_chat_base_url", srv.URL); err != nil {
		t.Fatalf("set base url: %v", err)
	}

	var deltas strings.Builder
	reply, _, err := svc.streamAskWithTools(
		mustCfg(t, svc),
		[]chatMessage{{Role: "user", Content: "hi"}},
		nil,
		func(d string) { deltas.WriteString(d) },
		nil,
		nil,
		5*time.Second,
	)
	if err != nil {
		t.Fatalf("streamAskWithTools must walk to the working URL, got: %v", err)
	}
	if reply != "resolved" {
		t.Fatalf("reply = %q", reply)
	}
	if deltas.String() != "resolved" {
		t.Fatalf("streamed deltas = %q, want the answer", deltas.String())
	}
}

// TestAskWithToolsDoesNotWalkCandidatesOnAuthFailure keeps the 404 rule narrow:
// a 401 is the endpoint answering, not a wrong URL, and retrying it against a
// guessed sibling path would turn one clear error into three confusing ones.
func TestAskWithToolsDoesNotWalkCandidatesOnAuthFailure(t *testing.T) {
	requests := 0
	handler := func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}
	svc := newAuditTestService(t, handler)

	_, _, err := svc.askWithTools(mustCfg(t, svc), []chatMessage{{Role: "user", Content: "hi"}}, nil, 5*time.Second)
	if err == nil {
		t.Fatal("expected an auth error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("error should name the status, got %v", err)
	}
	if requests != 1 {
		t.Fatalf("provider saw %d requests, want exactly 1 (no candidate walking on 401)", requests)
	}
}
