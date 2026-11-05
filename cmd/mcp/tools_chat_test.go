package main

import (
	"testing"
)

// The agent-facing surface of the cross-client shared conversation layer
// (schema v29).
//
// These tests drive the tool *handler*, not the service: the two are not the
// same contract, and the shape that broke was the handler's required-key table.
// Warden omits an absent optional string instead of sending "" (its MCP
// arguments must survive an older server's schema validation), so a create
// with no workspace attached reached the handler with no `path` key at all —
// the service accepts "" as unscoped, but the handler demanded the key, and
// the first send of every new Warden topic failed with "path is required".

func chatCreate(t *testing.T, ts *testServer, args map[string]any) (map[string]any, string) {
	t.Helper()
	res := ts.call("repotory_chat", args)
	if res.IsError {
		return nil, ts.text(res)
	}
	return ts.jsonPayload(res), ""
}

func TestChatCreateWithoutPathIsUnscoped(t *testing.T) {
	ts := newTestServer(t)
	payload, errMsg := chatCreate(t, ts, map[string]any{
		"operation": "create",
		"id":        "5f0f0b1a-1111-4aac-9a1a-0123456789ab",
		"title":     "unscoped topic",
	})
	if errMsg != "" {
		t.Fatalf("create without path failed: %s", errMsg)
	}
	thread, _ := payload["thread"].(map[string]any)
	if thread == nil {
		t.Fatalf("no thread in payload: %v", payload)
	}
	if got, _ := thread["path"].(string); got != "" {
		t.Errorf("path = %q, want \"\" (absent means unscoped)", got)
	}
	if got, _ := thread["id"].(string); got != "5f0f0b1a-1111-4aac-9a1a-0123456789ab" {
		t.Errorf("id = %q, want the client-supplied UUID", got)
	}
}

// The path is optional, not unvalidated: a non-string must not be coerced to
// "" and silently widen the thread to the whole library.
func TestChatCreateRejectsNonStringPath(t *testing.T) {
	ts := newTestServer(t)
	_, errMsg := chatCreate(t, ts, map[string]any{
		"operation": "create",
		"id":        "5f0f0b1a-2222-4aac-9a1a-0123456789ab",
		"title":     "numeric path",
		"path":      123,
	})
	if errMsg == "" {
		t.Fatal("create with a numeric path succeeded; want it rejected")
	}
}

// A supplied path is still stored — the relaxation must not turn into the
// create branch ignoring the argument.
func TestChatCreateStoresSuppliedPath(t *testing.T) {
	ts := newTestServer(t)
	payload, errMsg := chatCreate(t, ts, map[string]any{
		"operation": "create",
		"id":        "5f0f0b1a-3333-4aac-9a1a-0123456789ab",
		"title":     "scoped topic",
		"path":      "/tmp/scoped",
	})
	if errMsg != "" {
		t.Fatalf("create with path failed: %s", errMsg)
	}
	thread, _ := payload["thread"].(map[string]any)
	if got, _ := thread["path"].(string); got != "/tmp/scoped" {
		t.Errorf("path = %q, want /tmp/scoped", got)
	}
}
