package main

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"repotory/internal/service"
	"repotory/internal/writerapi"
)

// registerConfigTools wires the configuration read/write pair (ADR-0063,
// 2026-11-16). The owner reclassified configuration writes from human-gate to
// catalogue-covered so the IDE panels can save AI provider settings over MCP:
// an IDE-only user (cinder-mcp, no desktop, no server) previously had no
// entry point to configure an endpoint, so repotory_ask permanently fell back
// to keyword matching.
//
// The write goes through the single-writer catalogue (config.update) exactly
// like every other catalogue write: this process executes it when it holds
// the write role, and forwards it to the elected writer otherwise. The
// executor calls the same service.UpdateConfig the HTTP binding calls, so the
// allow-list and value validation are identical on both faces.
func registerConfigTools(mcpServer *server.MCPServer, svc *service.Service, w *writerapi.Manager) {
	mcpServer.AddTool(mcp.Tool{
		Name: "repotory_config_get",
		Description: "Read the full Cinder configuration: every settable key and its current value, plus the scan roots. " +
			"Secrets (API keys) are masked to ******** — the response says a key is configured, never what it is. " +
			"The IDE panels read their provider settings through this tool.",
		InputSchema: mcp.ToolInputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		cfg, err := svc.GetConfig()
		if err != nil {
			return makeErrorResult(err), nil
		}
		return makeJSONResult(cfg)
	})

	mcpServer.AddTool(mcp.Tool{
		Name: "repotory_config_update",
		Description: "Update a single Cinder configuration key-value pair. " +
			"Allowed keys: " + joinKeys(service.AllowedConfigKeys()) + ". " +
			"Unknown keys are rejected; numeric keys reject non-numeric values. " +
			"The write goes through the single-writer contract, so it is executed by whichever process holds the write role. " +
			"This is the MCP twin of the UpdateConfig binding (ADR-0063): the IDE panels save AI provider settings (ai_chat_base_url / ai_chat_model / ai_chat_api_key) through it.",
		InputSchema: mcp.ToolInputSchema{
			Type:     "object",
			Required: []string{"key", "value"},
			Properties: map[string]any{
				"key": map[string]any{
					"type":        "string",
					"description": "Configuration key (one of the allowed keys listed in the tool description)",
				},
				"value": map[string]any{
					"type":        "string",
					"description": "New value. For secret keys, the real value — the read side masks it, the write side stores it",
				},
			},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		key, _ := args["key"].(string)
		value, _ := args["value"].(string)
		if key == "" {
			return makeInvalidResult("key is required"), nil
		}
		var result map[string]string
		if err := w.ExecuteInto(ctx, writerapi.MethodConfigUpdate, writerapi.ConfigUpdateArgs{
			Key:   key,
			Value: value,
		}, &result); err != nil {
			return makeErrorResult(err), nil
		}
		return makeJSONResult(result)
	})
}

// joinKeys renders the allowed-key list for the tool description.
func joinKeys(keys []string) string {
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += ", "
		}
		out += k
	}
	return out
}
