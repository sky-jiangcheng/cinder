package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"repotory/internal/service"
)

// registerAskTool wires repotory_ask: the question-answering tool.
//
// What it used to be, and why that was not an answer. The tool ran
// svc.SearchAll(query) and formatted the first five hits as text — an FTS
// grep with a friendly wrapper. An agent asking "why do we retry on 503?" got
// five note titles and had to do the answering itself, while the desktop
// panel (AskAIWithEvidence) produced an actual answer with citations. Two
// surfaces answering the same question differently is the drift this repo
// keeps writing ADRs about.
//
// What it is now: the SAME retrieval and the SAME prompt assembly as the
// desktop panel — svc.AskAIWithEvidence — so the answer and its sources are
// one implementation, not two. TODO Sprint 1.5's acceptance bar ("the desktop
// and MCP give an answer with sources for the same question") is a statement
// about code identity, not about a similar look.
//
// The fallback matters and is not a euphemism. An install with no AI endpoint
// configured gets keyword matches back — but it is TOLD they are keyword
// matches and not an answer. Silently returning grep output as if it were an
// answer would be the worst of both worlds: the agent would treat five note
// titles as a conclusion. And a configured-but-failing endpoint is an ERROR,
// not a fallback: "the endpoint is down" and "you have no endpoint" are
// different facts, and only the first one is worth retrying.
func registerAskTool(mcpServer *server.MCPServer, svc *service.Service) {
	mcpServer.AddTool(mcp.Tool{
		Name: "repotory_ask",
		Description: "Ask a question about the local knowledge base and get an ANSWER with its sources. " +
			"Retrieves relevant knowledge pages and notes, sends them to the configured AI endpoint, and returns the reply " +
			"plus a numbered source list (the bracketed refs in the answer point into it). " +
			"Pass project_id (or path) to scope the answer to one project; omit both for a cross-project answer. " +
			"If no AI endpoint is configured, this returns keyword matches instead — clearly labelled, never as an answer.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "The question to answer",
				},
				"project_id": map[string]any{
					"type":        "number",
					"description": "Optional project ID to scope the answer to (see repotory_projects_list).",
				},
				"path": map[string]any{
					"type": "string",
					"description": "Optional absolute directory (IDE workspace folder / agent cwd). " +
						"Used to resolve the project server-side when project_id is not given.",
				},
			},
			Required: []string{"query"},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		query, _ := args["query"].(string)
		if strings.TrimSpace(query) == "" {
			return makeInvalidResult("query is required"), nil
		}
		if len(query) > maxArgQueryLen {
			return makeInvalidResult(fmt.Sprintf("query too long: %d bytes (max %d)", len(query), maxArgQueryLen)), nil
		}
		projectID, msg := resolveProjectID(svc, args)
		if msg != "" {
			return makeInvalidResult(msg), nil
		}

		answer, err := svc.AskAIWithEvidence(projectID, query, service.DefaultEvidenceBudget)
		if err != nil {
			if errors.Is(err, service.ErrAIChatNotConfigured) {
				fallback, fbErr := keywordFallback(svc, query)
				if fbErr != nil {
					return makeErrorResult(fbErr), nil
				}
				return makeTextResult(fallback), nil
			}
			// A configured endpoint that fails is an error, not a fallback: the
			// caller must be able to tell "retry later" from "there is no answer".
			return makeErrorResult(err), nil
		}
		return makeTextResult(formatEvidenceAnswer(answer)), nil
	})
}

// keywordFallback is what an install without an AI endpoint gets: the FTS
// matches, labelled as matches.
//
// The label is the point. The old tool returned this text unqualified and an
// agent read it as an answer; the wording has to say what it is.
func keywordFallback(svc *service.Service, query string) (string, error) {
	hits, err := svc.SearchAll(query)
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return "No results found for: " + query, nil
	}
	return "No AI endpoint is configured, so this is a keyword match list, NOT an answer:\n\n" +
		strings.Join(service.FormatSearchAnswer(hits, 5), "\n---\n"), nil
}

// formatEvidenceAnswer renders the answer and the sources it was built from.
//
// The sources are the reason this tool exists as an answer tool rather than a
// search tool: the bracketed refs in the reply ("[P1]") mean nothing unless
// the reader can see what P1 is.
func formatEvidenceAnswer(ans *service.EvidenceAnswer) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(ans.Reply))
	if ans.Truncated {
		b.WriteString("\n\n_(the model's reply was cut off by the server's length limit)_")
	}
	ev := ans.Evidence
	if ev == nil || len(ev.Items) == 0 {
		b.WriteString("\n\n_(no local knowledge matched this question; the reply above is the model's own)_")
		return b.String()
	}
	b.WriteString("\n\nSources:\n")
	for _, it := range ev.Items {
		fmt.Fprintf(&b, "[%s] %s %q (id %d", it.Ref, it.Type, it.Title, it.ID)
		if it.Slug != "" {
			fmt.Fprintf(&b, ", slug %s", it.Slug)
		}
		if it.ProjectName != "" {
			fmt.Fprintf(&b, ", project %s", it.ProjectName)
		}
		b.WriteString(")\n")
	}
	if ev.Truncated {
		fmt.Fprintf(&b, "(%d further match(es) omitted by the context budget)\n", ev.Dropped)
	}
	return b.String()
}
