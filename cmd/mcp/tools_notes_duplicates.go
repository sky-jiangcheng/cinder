package main

import (
	"context"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"repotory/internal/bounds"
	"repotory/internal/domain"
	"repotory/internal/service"
)

// registerNotesDuplicatesTool wires repotory_notes_duplicates: the read-only
// duplicate-pair finder.
//
// Why this exists on the agent face. The desktop-only reason said "a suggested
// merge pair for a human to inspect; agents must not act on a heuristic
// match" — true, and it still is: this tool PROPOSES pairs, it never merges,
// edits or deletes, so the gate stays in the shape of the answer rather than
// in who reads it. What that reason got wrong is the other half: an agent
// writing a weekly review, or a copilot asked "do I have this written down
// twice", cannot see the card grid at all. A pair is a suggestion on both
// faces; only the renderer differs.
//
// Scope is one project, or the whole knowledge base when omitted. Cross-project
// pairs are deliberately not offered by the service: two notes in different
// projects that overlap are usually the same subject seen from two angles, not
// a mistake to merge.
func registerNotesDuplicatesTool(mcpServer *server.MCPServer, svc *service.Service) {
	mcpServer.AddTool(mcp.Tool{
		Name: "repotory_notes_duplicates",
		Description: "Find note pairs that look like the same note written twice (read-only: proposes pairs with their shared terms, never merges). " +
			"Scoped to one project, or the whole knowledge base when no project is given.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"project_id": map[string]any{
					"type":        "number",
					"description": "Optional project ID to scope the search to (see repotory_projects_list). Omit for the whole knowledge base.",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Optional absolute directory (IDE workspace folder / agent cwd). Resolves to a project server-side when project_id is not given, the same way repotory_context's path does.",
				},
			},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		projectID, _ := args["project_id"].(float64)
		path, _ := args["path"].(string)
		if msg := bounds.Check("path", path, bounds.MaxPathLen); msg != "" {
			return makeInvalidResult(msg), nil
		}

		scope := int64(projectID)
		if scope <= 0 && strings.TrimSpace(path) != "" {
			// Resolution lives server-side so an agent and the IDE cannot
			// disagree about which project a folder belongs to.
			if res := svc.ResolveWorkspace("", path); res != nil && res.Project != nil {
				scope = res.Project.ID
			}
		}

		pairs := svc.DuplicateNotes(scope)
		if pairs == nil {
			// An empty result is [] on the wire, never null: a client that
			// cannot tell "no duplicates" from "the field is absent" reports
			// the second as a contract problem.
			pairs = []domain.NoteDuplicate{}
		}
		return makeJSONResult(pairs)
	})
}
