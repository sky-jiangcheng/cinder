package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"repotory/internal/service"
)

// registerWikiTools wires the approved-knowledge layer into the agent surface
// (ADR-0032 决策 5).
//
// Two tools, both pure reads:
//
//   - repotory_wiki_search — with a query it ranks approved pages by bm25
//     (the same retrieval the evidence path uses); with an empty query it
//     lists the approved pages newest first, which is the browse feed.
//   - repotory_wiki_read — one page's body, its edges and its source notes.
//
// The gate here is on the WRITE side (ADR-0015): a page reaches this surface
// only after a human approved it, so reading one is reading published
// knowledge, exactly like repotory_notes_read reads an approved note. The
// scope tools (promote / assign) are a different case — they decide what
// speaks for the user across projects — and stay human-only.
//
// The page scope follows the evidence path: a project scope means "that
// project's pages PLUS the global ones", so an agent standing in project A
// sees project A's pages and the cross-project ones, not project B's.
func registerWikiTools(mcpServer *server.MCPServer, svc *service.Service) {
	mcpServer.AddTool(mcp.Tool{
		Name: "repotory_wiki_search",
		Description: "Search the approved knowledge pages — the concepts, entities, syntheses and source pages the user reviewed and published. " +
			"With a query, pages are ranked by relevance (FTS5, CJK-tolerant). With an empty query, the approved pages are listed newest first. " +
			"Pass project_id (or path) to scope to one project; omit both to search across projects. " +
			"Unapproved pages are never returned — a page a human has not reviewed does not exist here.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search terms. Empty lists the approved pages newest first.",
				},
				"project_id": map[string]any{
					"type":        "number",
					"description": "Optional project ID to scope to (see repotory_projects_list).",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Optional absolute directory (IDE workspace folder / agent cwd) used to resolve the project server-side.",
				},
				"limit": map[string]any{
					"type":        "number",
					"description": "Max pages to return (default: 20, max: 50)",
				},
			},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		query, _ := args["query"].(string)
		if len(query) > maxArgQueryLen {
			return makeInvalidResult(fmt.Sprintf("query too long: %d bytes (max %d)", len(query), maxArgQueryLen)), nil
		}
		limit := wikiSearchLimit(req)
		projectID, msg := resolveProjectID(svc, args)
		if msg != "" {
			return makeInvalidResult(msg), nil
		}

		var (
			pages []service.WikiPageSummary
			err   error
		)
		if strings.TrimSpace(query) == "" {
			// No query is the browse feed, not a failed search: the list query
			// is a different statement ("what is published") than the FTS one.
			pages, err = svc.BrowseWikiPages(projectID, limit)
		} else {
			pages, err = svc.SearchWikiPages(projectID, query, limit)
		}
		if err != nil {
			return makeErrorResult(err), nil
		}
		if pages == nil {
			pages = []service.WikiPageSummary{}
		}
		return makeJSONResult(pages)
	})

	mcpServer.AddTool(mcp.Tool{
		Name:        "repotory_wiki_read",
		Description: "Read one approved knowledge page in full: its body, the pages it links to, the pages that link to it, and the notes it was compiled from.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"id": map[string]any{
					"type":        "number",
					"description": "Page ID (from repotory_wiki_search)",
				},
			},
			Required: []string{"id"},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, _ := req.GetArguments()["id"].(float64)
		if int64(id) <= 0 {
			return makeInvalidResult("a positive page id is required"), nil
		}
		page, err := svc.GetWikiPage(int64(id))
		if err != nil {
			return makeErrorResult(err), nil
		}
		return makeJSONResult(page)
	})
}

// wikiSearchLimit reads the optional limit argument, defaulting to 20 and
// clamped to 50. The ceiling exists for the same reason notes_list has one: a
// single call must not be able to dump the whole knowledge base over the wire.
func wikiSearchLimit(req mcp.CallToolRequest) int {
	limit := 20
	if v, ok := req.GetArguments()["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	if limit > 50 {
		limit = 50
	}
	return limit
}
