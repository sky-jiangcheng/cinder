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
		Description: "Read stored repository and knowledge metrics; does not scan disk, run git or refresh caches. Empty path means the whole library. A workspace path selects its repository (or matched project's repositories); notes and pending review counts use its owning project. Today's counts use cached all-author daily_stats, not personal rows. totalLines is null unless cached language LOC covers the entire unambiguous scope. refreshedAt is the latest stored repository scan timestamp, or null; it is not the request time. Unknown paths return empty counts, never global totals.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{"type": "string", "description": "Optional absolute workspace path; empty means all. At most 4096 UTF-8 bytes."},
			},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		path, err := toolString(req.GetArguments(), "path", false)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		metrics, err := svc.GetMetrics(ctx, path)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return makeJSONResult("metrics", metrics)
	})
}
