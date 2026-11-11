package main

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"repotory/internal/service"
)

func registerMetricsTool(s *server.MCPServer, svc *service.Service) {
	s.AddTool(mcp.Tool{
		Name:        "repotory_metrics",
		Description: "Read stored repository and knowledge metrics; does not scan disk, run git or refresh caches. Empty path means the whole library. A workspace path selects its repository (or matched project's repositories); notes and pending review counts use its owning project. Today's counts (*Today trio) always use cached all-author daily_stats, not personal rows. since/until (YYYY-MM-DD, inclusive; default today) add a window sum: commits/additions/deletions, null with stale=true when the window extends past the last scan, because days nobody scanned are absent, not zero — run repotory_scan first, never report a stale window as no activity. totalLines is null unless cached language LOC covers the entire unambiguous scope. refreshedAt is the latest stored repository scan timestamp, or null; it is not the request time. Unknown paths return empty counts, never global totals.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"path":  map[string]any{"type": "string", "description": "Optional absolute workspace path; empty means all. At most 4096 UTF-8 bytes."},
				"since": map[string]any{"type": "string", "description": "Optional window start, YYYY-MM-DD inclusive; defaults to until."},
				"until": map[string]any{"type": "string", "description": "Optional window end, YYYY-MM-DD inclusive; defaults to today. Max span 366 days."},
			},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		path, err := toolString(req.GetArguments(), "path", false)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		since, err := toolString(req.GetArguments(), "since", false)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		until, err := toolString(req.GetArguments(), "until", false)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		metrics, err := svc.GetMetricsRange(ctx, path, since, until)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return makeJSONResult("metrics", metrics)
	})
}
