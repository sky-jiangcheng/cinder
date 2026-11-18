package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"repotory/internal/aitools"
)

func toolLoopServer(t *testing.T, hits *int32) *httptest.Server {
	t.Helper()
	var calls int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if hits != nil {
			atomic.AddInt32(hits, 1)
		}
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"probe","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}]}`))
	}))
}

// The loop used to shadow the caller's ctx with context.Background() and hand
// the already-canceled derivative to the tool handlers — every ctx-aware tool
// (search included) answered "context canceled". A handler must see a live ctx.
func TestAskWithToolsHandlersSeeLiveContext(t *testing.T) {
	srv := toolLoopServer(t, nil)
	defer srv.Close()

	svc, _ := setupService(t)
	svc.toolRegistry = aitools.NewRegistry()
	var handlerErr string
	var sawDeadline bool
	svc.toolRegistry.Register("probe", "probe", map[string]any{"type": "object"},
		func(ctx context.Context, _ map[string]any) (any, error) {
			if ctx.Err() != nil {
				handlerErr = ctx.Err().Error()
			}
			_, sawDeadline = ctx.Deadline()
			return "ok", nil
		})

	cfg := aiChatConfig{baseURL: srv.URL, model: "m"}
	if _, _, err := aitools.Ask(context.Background(), cfg.provider(), nil, svc.toolRegistry.Definitions(), svc.toolRegistry, 5*time.Second); err != nil {
		t.Fatalf("loop: %v", err)
	}
	if handlerErr != "" {
		t.Fatalf("tool handler saw ctx.Err() = %q, want a live ctx", handlerErr)
	}
	if sawDeadline {
		t.Fatalf("tool handler inherited the HTTP deadline; the caller's ctx should carry none here")
	}
}

// Cancellation has to reach the HTTP call too: previously the Background-derived
// ctx ignored a canceled caller and the request still went out.
func TestAskWithToolsRespectsCallerCancellation(t *testing.T) {
	var hits int32
	srv := toolLoopServer(t, &hits)
	defer srv.Close()

	svc, _ := setupService(t)
	cfg := aiChatConfig{baseURL: srv.URL, model: "m"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := aitools.Ask(ctx, cfg.provider(), nil, nil, svc.toolRegistry, 5*time.Second); err == nil {
		t.Fatal("a canceled caller ctx still produced a successful round")
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatalf("the endpoint was hit %d times after cancellation", hits)
	}
}
