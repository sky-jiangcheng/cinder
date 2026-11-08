// cinder-mcp is the Cinder MCP server: it exposes the local Git knowledge
// base (notes, projects, search) to AI agents over the Model Context
// Protocol on stdio. The database is opened once at startup and every tool
// call shares the same service instance as the desktop app.
package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"repotory/internal/bounds"
	"repotory/internal/db"
	"repotory/internal/domain"
	"repotory/internal/platform"
	"repotory/internal/service"
	"repotory/internal/version"
)

func main() {
	platform.SetPrivateUmask() // owner-only files: DB sidecars, logs, exports

	d, err := db.InitDB(platform.GetDbPath())
	if err != nil {
		log.Fatalf("database error: %v", err)
	}
	defer d.Close()
	svc := service.New(d, platform.GetGitUserName())
	// This process IS the MCP surface (ADR-0030): every model call it makes
	// is an agent call, and the audit row says so.
	svc.SetAuditSurface(service.AISurfaceMCP)

	// serverInfo.version carries the memory-layer CONTRACT version, not the
	// release version. A client asking "what may I rely on" wants the answer
	// to survive a UI-only release; the release version is still one log line
	// away and rides along on /health as "version".
	mcpServer := server.NewMCPServer("repotory-mcp", version.CoreVersion)
	registerTools(mcpServer, svc)
	registerAskTool(mcpServer, svc)
	registerWikiTools(mcpServer, svc)
	registerContextTools(mcpServer, svc)
	registerScanTool(mcpServer, svc)
	registerAuditTools(mcpServer, svc)
	registerReviewTools(mcpServer, svc)

	// Seed the default scan roots so a headless (MCP-only) install can discover
	// repositories via repotory_scan without ever opening the desktop app.
	svc.EnsureDefaultScanRoots()

	log.Printf("Cinder MCP server v%s starting on stdio... (release %s, memory-layer contract %s)",
		version.CoreVersion, version.Version, version.CoreVersion)
	if err := server.ServeStdio(mcpServer); err != nil {
		log.Fatalf("MCP server error: %v", err)
	}
}

// maxArgQueryLen bounds the query argument of repotory_notes_search and
// repotory_ask. The query is escaped before it reaches SQLite, so this is not
// an injection guard — it stops a multi-megabyte argument from turning every
// FTS/LIKE match into a multi-second scan on each call.
const maxArgQueryLen = bounds.MaxQueryLen

// NOTE on the handoff guard inside repotory_notes_update: it protects well-behaved agents
// from destroying a session record. It is NOT a security boundary. The /api/rpc bridge
// (loopback, unauthenticated by design) reaches UpdateNoteFull directly and can rewrite
// a handoff, and so can any local process that opens the SQLite file. Saying so is the
// point: a reader who assumes the guard is enforcement will build on it. The desktop app
// must be able to edit handoffs, which is why the rule sits at the agent surface and not
// in the service layer.

// notesListLimit reads the optional limit argument for repotory_notes_list,
// defaulting to 50 and clamped to 500 so a single call cannot dump the whole
// knowledge base over the wire.
func notesListLimit(req mcp.CallToolRequest) int {
	limit := bounds.AgentNotesDefault
	if v, ok := req.GetArguments()["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	// The clamp itself is shared with any other agent-facing listing surface, so
	// the default and the ceiling cannot drift apart per transport.
	return bounds.ClampNotesLimit(limit)
}

// registerTools wires every MCP tool onto the server. Split out of main so
// tests can build a real server over a fixture service and invoke handlers
// through the same path a client does, instead of reaching for unexported
// handlers.
func registerTools(mcpServer *server.MCPServer, svc *service.Service) {
	registerChatTool(mcpServer, svc)
	registerMetricsTool(mcpServer, svc)

	mcpServer.AddTool(mcp.Tool{
		Name:        "repotory_notes_list",
		Description: "List knowledge notes across projects (bounded: default 50, max 500). For one project's working set call repotory_context instead — it returns that project's notes, todos and mined repo knowledge in one call, which is why this tool has no project_id argument.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"limit": map[string]any{
					"type":        "number",
					"description": "Max notes to return (default: 50, max: 500)",
				},
			},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// The limit is pushed into the SQL query: an agent asking for 50
		// notes on a 10k-note knowledge base used to receive all 10k over
		// the wire first.
		notes := svc.ListAllNotesLimited(notesListLimit(req))
		return makeJSONResult("notes_list", notes)
	})

	mcpServer.AddTool(mcp.Tool{
		Name:        "repotory_notes_search",
		Description: "Search notes and todos by query using FTS5 full-text search",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Optional absolute directory (IDE workspace folder / agent cwd). When it resolves inside a scanned repository, notes recorded in that repository are ranked first. Ranking only: nothing is filtered out, and todos are untouched.",
				},
			},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, _ := req.GetArguments()["query"].(string)
		if query == "" {
			return makeTextResult("query is required"), nil
		}
		if len(query) > maxArgQueryLen {
			return makeTextResult(fmt.Sprintf("query too long: %d bytes (max %d)", len(query), maxArgQueryLen)), nil
		}
		hits := svc.SearchAll(query)
		if path, _ := req.GetArguments()["path"].(string); strings.TrimSpace(path) != "" {
			hits = svc.RankHitsByWorkspace(hits, path)
		}
		return makeJSONResult("notes_search", hits)
	})

	mcpServer.AddTool(mcp.Tool{
		Name:        "repotory_notes_read",
		Description: "Read a single note by ID",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"id": map[string]any{
					"type":        "number",
					"description": "Note ID",
				},
			},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, _ := req.GetArguments()["id"].(float64)
		note, err := svc.GetNote(int64(id))
		if err != nil {
			return makeTextResult(fmt.Sprintf("note not found: %v", err)), nil
		}
		return makeJSONResult("note_read", note)
	})

	mcpServer.AddTool(mcp.Tool{
		Name:        "repotory_projects_list",
		Description: "List all projects",
		InputSchema: mcp.ToolInputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Empty must serialise as [] not null: a fresh install returns a nil
		// slice from ListProjects, and an agent (or any JSON consumer) testing
		// for an empty array should not have to handle null. Same rule the
		// httpapi layer documents in its own regression test.
		projects := svc.ListProjects()
		if projects == nil {
			projects = []domain.Project{}
		}
		return makeJSONResult("projects_list", projects)
	})

	mcpServer.AddTool(mcp.Tool{
		Name:        "repotory_projects_stats",
		Description: "Get statistics for a specific project (repos, stats)",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"id": map[string]any{
					"type":        "number",
					"description": "Project ID",
				},
			},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, _ := req.GetArguments()["id"].(float64)
		summary, err := svc.GetProjectSummary(int64(id))
		if err != nil {
			return makeTextResult(fmt.Sprintf("project not found: %v", err)), nil
		}
		return makeJSONResult("project_stats", summary)
	})

	mcpServer.AddTool(mcp.Tool{
		Name:        "repotory_notes_create",
		Description: "Create a new knowledge note",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"project_id": map[string]any{
					"type":        "number",
					"description": "Project ID to attach the note to",
				},
				"title": map[string]any{
					"type":        "string",
					"description": "Note title",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Markdown content of the note",
				},
				"category": map[string]any{
					"type":        "string",
					"description": "Category: knowledge, log, idea, or other (default: knowledge)",
				},
				"tags": map[string]any{
					"type":        "string",
					"description": "Comma-separated tags",
				},
			},
			Required: []string{"project_id", "title", "content"},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		projectID, _ := args["project_id"].(float64)
		title, _ := args["title"].(string)
		content, _ := args["content"].(string)
		category, _ := args["category"].(string)
		if category == "" {
			category = "knowledge"
		}
		tags, _ := args["tags"].(string)

		note, err := svc.CreateNoteWithMeta(int64(projectID), title, content, tags, category, "mcp")
		if err != nil {
			return makeTextResult(fmt.Sprintf("error: %v", err)), nil
		}
		return makeJSONResult("note_created", note)
	})

	mcpServer.AddTool(mcp.Tool{
		Name:        "repotory_notes_update",
		Description: "Update an existing note's content and/or metadata. Omitted fields keep their current value (there is deliberately no way to clear the title or tags — write a new note instead). Pass pinned=true to pin the note to the top of its project, pinned=false to unpin it; omitting it keeps the current state. Notes tagged 'handoff' are session-protocol records and cannot be updated here: write a new handoff with repotory_handoff instead.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"id": map[string]any{
					"type":        "number",
					"description": "Note ID to update",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "New Markdown content",
				},
				"title": map[string]any{
					"type":        "string",
					"description": "New title",
				},
				"tags": map[string]any{
					"type":        "string",
					"description": "New comma-separated tags",
				},
				"category": map[string]any{
					"type":        "string",
					"description": "New category: knowledge, log, idea, or other",
				},
				"pinned": map[string]any{
					"type":        "boolean",
					"description": "Pin the note to the top of its project (true) or unpin it (false). Omit to keep the current state.",
				},
			},
			Required: []string{"id"},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["id"].(float64)
		content, _ := args["content"].(string)
		title, _ := args["title"].(string)
		tags, _ := args["tags"].(string)
		category, _ := args["category"].(string)

		// pinned is a BOOLEAN, so "omitted" and "false" are two different
		// requests that both arrive as a JSON value: presence has to be read
		// from the argument map, never inferred from the value. Getting this
		// wrong is silent in either direction — reading false as absent makes
		// unpinning impossible, and reading absent as false unpins every note
		// the tool touched (db.UpdateNoteFull replaces pinned wholesale).
		pinnedValue := false
		hasPinned := false
		if raw, present := args["pinned"]; present && raw != nil {
			pinned, ok := raw.(bool)
			if !ok {
				// A client that sends "pinned": "true" meant to set it. Saying
				// so beats dropping the argument and reporting success.
				return makeTextResult(fmt.Sprintf("pinned must be a boolean, got %T", raw)), nil
			}
			pinnedValue = pinned
			hasPinned = true
		}

		if content == "" && title == "" && tags == "" && category == "" && !hasPinned {
			return makeTextResult("nothing to update: provide at least one of content, title, tags, category, pinned"), nil
		}

		// Load the stored note first: this is a partial-update tool, so every
		// omitted field must merge into the existing value. UpdateNoteMeta
		// replaces title/tags/kind/pinned wholesale, so calling it with only
		// "category" (a documented use) used to silently wipe the title and
		// tags and unpin the note — data loss through a normal tool call.
		note, err := svc.GetNote(int64(id))
		if err != nil {
			return makeTextResult(fmt.Sprintf("note not found: %v", err)), nil
		}

		// Handoff notes are the session-memory protocol's exit records:
		// repotory_context renders the latest one in full and the next
		// session acts on it. One mistaken update used to silently overwrite
		// that contract, so this agent-facing write path treats them as
		// append-only. Writing a new handoff (repotory_handoff) is the
		// supported flow; fixing the record itself stays a desktop-UI
		// operation.
		if service.IsHandoffNote(*note) {
			return makeTextResult(fmt.Sprintf("refusing to update note %d: it is a session handoff record (tagged 'handoff') — the persisted protocol record between sessions, protected from plain overwrites. Write a new handoff with repotory_handoff; if this record itself is wrong, edit it from the desktop app.", note.ID)), nil
		}

		// Merge omitted fields from the stored note and write everything back
		// in ONE UpdateNoteFull. The old two-call path (UpdateNote then
		// UpdateNoteMeta) was non-atomic: a metadata failure left the note
		// with new content and old title/tags. Omitted-field semantics are
		// unchanged ("keep what is there") — and `pinned` joins that rule: it
		// is written from the stored value unless the caller named it.
		mergedContent, mergedTitle, mergedTags, mergedKind := note.Content, note.Title, note.Tags, note.Kind
		if content != "" {
			mergedContent = content
		}
		if title != "" {
			mergedTitle = title
		}
		if tags != "" {
			mergedTags = tags
		}
		if category != "" {
			mergedKind = category
		}
		mergedPinned := note.Pinned
		if hasPinned {
			mergedPinned = pinnedValue
		}
		if err := svc.UpdateNoteFull(note.ID, mergedContent, mergedTitle, mergedTags, mergedKind, mergedPinned); err != nil {
			return makeTextResult(fmt.Sprintf("error: %v", err)), nil
		}
		updated, err := svc.GetNote(note.ID)
		if err != nil {
			return makeTextResult(fmt.Sprintf("error fetching note: %v", err)), nil
		}
		return makeJSONResult("note_updated", updated)
	})

	// The tidying draft. Deliberately a PROPOSAL tool: it returns the model's
	// rewrite and writes nothing, so an agent can prepare a reorganized note
	// but only a human save (repotory_notes_update) lands it — the review
	// gate of ADR-0015 applies to machine-generated wording too.
	mcpServer.AddTool(mcp.Tool{
		Name: "repotory_notes_organize",
		Description: "Ask the configured AI to restructure one note into clean Markdown: same facts, better structure, a table-of-contents index when the note grew sections, and a mermaid flowchart when the content is a process. " +
			"Returns a PROPOSAL (title/tags/content) — nothing is saved; review it, edit if needed, then save with repotory_notes_update. " +
			"Requires the AI endpoint to be configured (Settings → AI in the desktop app).",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"id": map[string]any{
					"type":        "number",
					"description": "Note ID to organize",
				},
			},
			Required: []string{"id"},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["id"].(float64)
		if id <= 0 {
			return makeTextResult("a positive note id is required"), nil
		}
		res, err := svc.OrganizeNote(int64(id))
		if err != nil {
			return makeTextResult(fmt.Sprintf("error: %v", err)), nil
		}
		return makeJSONResult("note_organized", res)
	})

	mcpServer.AddTool(mcp.Tool{
		Name:        "repotory_agent_score",
		Description: "Check AI-readiness of the local Cinder installation (database, notes, search, MCP, llms.txt, SKILL.md, i18n)",
		InputSchema: mcp.ToolInputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return makeTextResult(runAgentScore(svc)), nil
	})

	// Deliberately separate from repotory_agent_score, which answers "is this
	// install set up well enough to be used" (notes present, MCP reachable,
	// locales shipped). This one answers the question that tool cannot: "is the
	// data in it still true" — has the FTS index drifted out of sync with the
	// notes, are there rows pointing at projects that no longer exist, is the
	// mined repository cache stale. An agent that suspects a search returned
	// too few results should run this before concluding anything about the
	// knowledge base.
	mcpServer.AddTool(mcp.Tool{
		Name: "repotory_integrity",
		Description: "Audit how much the local Cinder knowledge base can be trusted: FTS index drift, orphan rows, schema shape vs version stamp, scan coverage, mined-cache freshness. " +
			"Use when search results look incomplete or you need to know whether the data is current. Complements repotory_agent_score, which checks installation readiness, not data integrity.",
		InputSchema: mcp.ToolInputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return makeTextResult(runIntegrityReport(svc)), nil
	})
}
