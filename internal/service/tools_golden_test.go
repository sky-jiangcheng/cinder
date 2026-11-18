package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"repotory/internal/aiclient"
	"repotory/internal/aitools"
)

// TestToolLoopRequestBodyGolden pins the bytes the tool loop sends. The loop
// moved from internal/service to internal/aitools; a silent change in field
// order, omission of tools, or a dropped system message would still pass every
// behavioural test, so the request body is compared against a capture taken
// before the move.
func TestToolLoopRequestBodyGolden(t *testing.T) {
	raw, err := os.ReadFile("../aitools/testdata/tool_loop_golden.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var golden struct {
		Bodies []string `json:"bodies"`
	}
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatalf("parse golden: %v", err)
	}

	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		w.Header().Set("Content-Type", "application/json")
		if len(bodies) == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"query_daily_stats","arguments":"{\"days\":3}"}}]},"finish_reason":"tool_calls"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	svc, _ := setupService(t)
	messages := []aiclient.Message{
		{Role: "system", Content: "you are helpful"},
		{Role: "user", Content: "how many lines yesterday?"},
	}
	cfg := aiChatConfig{baseURL: srv.URL, model: "test-model"}
	if _, _, err := aitools.Ask(context.Background(), cfg.provider(), messages,
		svc.toolRegistry.Definitions(), svc.toolRegistry, 5*time.Second); err != nil {
		t.Fatalf("loop: %v", err)
	}

	if len(bodies) != len(golden.Bodies) {
		t.Fatalf("sent %d requests, golden has %d", len(bodies), len(golden.Bodies))
	}
	for i := range golden.Bodies {
		if bodies[i] != golden.Bodies[i] {
			t.Errorf("request %d differs from golden\n got: %s\nwant: %s", i, bodies[i], golden.Bodies[i])
		}
	}
}
