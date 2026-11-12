package main

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"repotory/internal/service"
	"repotory/internal/writerapi"
)

// registerScanTool wires repotory_scan: the headless cold-start tool. Without
// it a pure-MCP install (no desktop app) could never populate the database,
// so the advertised "the desktop app is not required" path led to an empty
// knowledge base. Calling it once seeds the default scan roots and runs the
// scan synchronously, reducing the first-run funnel to install → scan →
// context instead of install → scan → star → refresh → use.
func registerScanTool(mcpServer *server.MCPServer, svc *service.Service, w *writerapi.Manager) {
	mcpServer.AddTool(mcp.Tool{
		Name: "repotory_scan",
		Description: "Discover local Git repositories and populate the knowledge base. Call this once after install (or whenever repotory_context reports no projects) before anything else: it seeds the platform default scan roots on first run, then scans them synchronously and reports how many repositories/projects were found. " +
			"After it returns, call repotory_context to load a project's full working context.",
		InputSchema: mcp.ToolInputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// A scan is a write (projects, repositories, daily_stats — and the root
		// seeding), so it runs on the writer: the catalogue entry seeds the
		// defaults and scans in one operation, and a client's disconnect
		// cancels the forwarded request, which cancels it on the writer too.
		var res service.ScanResult
		if err := w.ExecuteInto(ctx, writerapi.MethodScanRun, struct{}{}, &res); err != nil {
			return makeErrorResult(err), nil
		}
		payload := map[string]any{
			"success":     res.Success,
			"repos_found": res.ReposFound,
			"projects":    res.Projects,
			"next_step":   "Call repotory_context to load a project's full working context.",
		}
		// A partial sync must not masquerade as a clean scan: the agent should
		// know some project groups failed before it trusts the counts.
		if res.SyncErrors > 0 {
			payload["sync_errors"] = res.SyncErrors
			payload["warning"] = fmt.Sprintf("%d project group(s) failed to sync; counts are partial — run repotory_integrity before trusting results", res.SyncErrors)
		}
		return makeJSONResult(payload)
	})
}
