package main

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"repotory/internal/bounds"
	"repotory/internal/service"
)

// registerAuditTools wires the AI call audit query (ADR-0030).
//
// The tool is the "entry side / exit side" boundary made concrete from an
// agent's point of view: repotory_ask and the other reading tools pull
// knowledge IN, and this one shows what went OUT. An agent that has just
// sent a prompt can check whether it was sent, how big it was, and whether
// a review rule tripped.
//
// It exposes no prompt text, because the audit table stores none — the
// response carries the hash and the character count. That is a deliberate
// limit, not an oversight: the whole point of the default tier is that the
// audit trail proves what happened without becoming a second copy of the
// knowledge base.
func registerAuditTools(mcpServer *server.MCPServer, svc *service.Service) {
	mcpServer.AddTool(mcp.Tool{
		Name: "repotory_ai_audit",
		Description: "Show what this machine sent to an AI model recently: when, to which endpoint, with which model, from which surface (mcp/http/desktop), how many characters, and whether a local review rule blocked or flagged it. " +
			"Call this when you need to confirm what actually went out — for example after a prompt was rejected, or to report what an agent session disclosed. " +
			"Prompts and replies are NOT returned: the audit trail stores a hash and a size, never the text. " +
			"Returns the current audit status (whether auditing and review are enabled) plus the recent call list.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"limit": map[string]any{
					"type":        "number",
					"description": "How many recent calls to return (default 20, max 500)",
				},
			},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		status, err := svc.GetAIAuditStatus()
		if err != nil {
			return makeErrorResult(err), nil
		}
		rows, err := svc.ListAICallAudit(aiAuditLimit(req))
		if err != nil {
			return makeErrorResult(err), nil
		}
		return makeJSONResult(map[string]any{
			"status": status,
			"calls":  rows,
		})
	})

	mcpServer.AddTool(mcp.Tool{
		Name: "repotory_ai_review_rules",
		Description: "Show the local AI review rules and whether input/output review is enabled. " +
			"The rules are the user's own regular expressions (one per line, '#' for comments), matched case-insensitively against prompts before they leave and against replies as they arrive. " +
			"Cinder ships no built-in blocklist — a keyword list is a weak control — so this returns exactly what is configured, or an empty list if there are none.",
		InputSchema: mcp.ToolInputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		status, err := svc.GetAIAuditStatus()
		if err != nil {
			return makeErrorResult(err), nil
		}
		return makeJSONResult(map[string]any{
			"audit_enabled": status.AuditEnabled,
			"review_input":  status.ReviewInput,
			"review_output": status.ReviewOutput,
			"rule_count":    status.RuleCount,
			"retention":     status.Retention,
		})
	})
}

// aiAuditLimit clamps the optional limit at the agent surface, using the
// same shared ceiling as repotory_notes_list. The default stays 20 (the audit
// feed is shorter than the note feed), but the maximum cannot drift from the
// other MCP listings.
func aiAuditLimit(req mcp.CallToolRequest) int {
	if n, ok := req.GetArguments()["limit"].(float64); ok && int(n) > 0 {
		return bounds.ClampNotesLimit(int(n))
	}
	return 20
}
