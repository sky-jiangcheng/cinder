package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func captureMCPSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	oldDefault := slog.Default()
	oldWriter := log.Writer()
	oldFlags := log.Flags()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(oldDefault)
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
	})
	return buf
}

func decodeLastMCPLog(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &rec); err != nil {
		t.Fatalf("log line is not JSON: %q: %v", lines[len(lines)-1], err)
	}
	return rec
}

func toolReq(name string) mcp.CallToolRequest {
	return mcp.CallToolRequest{Params: mcp.CallToolParams{Name: name}}
}

func TestInstrumentToolsLogsSuccess(t *testing.T) {
	buf := captureMCPSlog(t)
	wrapped := instrumentTools(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	res, err := wrapped(context.Background(), toolReq("repotory_notes_list"))
	if err != nil || res == nil {
		t.Fatalf("handler result = %v, %v", res, err)
	}
	rec := decodeLastMCPLog(t, buf)
	if rec["tool"] != "repotory_notes_list" || rec["ok"] != true {
		t.Errorf("success log = %v", rec)
	}
	if _, ok := rec["duration_ms"]; !ok {
		t.Error("duration_ms missing")
	}
}

func TestInstrumentToolsLogsHandlerError(t *testing.T) {
	buf := captureMCPSlog(t)
	wrapped := instrumentTools(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, errors.New("database is locked")
	})
	_, _ = wrapped(context.Background(), toolReq("repotory_ask"))
	rec := decodeLastMCPLog(t, buf)
	if rec["tool"] != "repotory_ask" || rec["ok"] != false || rec["error"] != "database is locked" {
		t.Errorf("error log = %v", rec)
	}
}

func TestInstrumentToolsLogsToolErrorResult(t *testing.T) {
	buf := captureMCPSlog(t)
	wrapped := instrumentTools(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return makeErrorResult(errors.New("boom")), nil
	})
	_, _ = wrapped(context.Background(), toolReq("repotory_notes_create"))
	rec := decodeLastMCPLog(t, buf)
	if rec["ok"] != false {
		t.Errorf("an IsError result must log ok=false: %v", rec)
	}
	if errText, _ := rec["error"].(string); !strings.Contains(errText, "boom") {
		t.Errorf("error summary missing: %v", rec)
	}
}

// A real tools/call through the request dispatcher (the production ServeStdio
// path) must pass through the middleware, not just a directly wrapped handler.
func TestInstrumentToolsCoversDispatchedToolCall(t *testing.T) {
	buf := captureMCPSlog(t)
	s := server.NewMCPServer("test", "test", server.WithToolHandlerMiddleware(instrumentTools))
	s.AddTool(mcp.Tool{Name: "probe_tool", InputSchema: mcp.ToolInputSchema{Type: "object"}},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		})
	msg := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"probe_tool","arguments":{}}}`)
	if resp := s.HandleMessage(context.Background(), msg); resp == nil {
		t.Fatal("tools/call produced no response")
	}
	rec := decodeLastMCPLog(t, buf)
	if rec["tool"] != "probe_tool" || rec["ok"] != true {
		t.Errorf("middleware did not log the dispatched tool call: %v (log=%s)", rec, buf.String())
	}
}

// The wrapper must pass the result through unchanged (tool behaviour intact).
func TestInstrumentToolsPassesResultThrough(t *testing.T) {
	captureMCPSlog(t)
	want := mcp.NewToolResultText("payload")
	wrapped := instrumentTools(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return want, nil
	})
	got, err := wrapped(context.Background(), toolReq("repotory_projects_list"))
	if err != nil || got != want {
		t.Fatalf("result was altered: got %p/%v, want %p", got, err, want)
	}
}
