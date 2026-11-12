package main

import (
	"context"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"repotory/internal/service"
)

// Incremental commit review (ADR-0053) as two tools: one reads the last
// report, one produces a new one. Both are agent-facing on purpose — the point
// of the feature is that a human asks "what did yesterday's commits break?"
// and the agent answers from the same rows the desktop page renders (D6: one
// source of truth, two renderings).
//
// review_run is a write in the sense that it creates rows, but it changes no
// user content: it reads git and the model, and stores a report. That is why
// it is a tool at all rather than a desktop-only button — an agent that has
// just finished a session can trigger the review for today.
func registerReviewTools(mcpServer *server.MCPServer, svc *service.Service) {
	mcpServer.AddTool(mcp.Tool{
		Name: "repotory_review_latest",
		Description: "Read Cinder's most recent incremental code review: which collected projects were reviewed, how many commits/files/candidates, and the findings themselves (repo, file, line, rule, severity, verdict, evidence, message, suggestion). " +
			"A verdict of 'confirmed' means the problem is established; 'unverified' means a deterministic rule fired but the pattern has a legitimate reading and no model has checked it. " +
			"Call this when the user asks what the daily review found, or whether a specific risk was already reported. " +
			"Reports are stored data, never notes: nothing here writes to the knowledge base.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"days": map[string]any{
					"type":        "number",
					"description": "How many days back to include (default 1 = only the latest run, max 30)",
				},
				"severity": map[string]any{
					"type":        "string",
					"description": "Optional filter: high, medium or low (comma separated)",
				},
			},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		days := 1
		if n, ok := args["days"].(float64); ok && int(n) > 0 {
			days = int(n)
			if days > 30 {
				days = 30
			}
		}
		severities := splitCSV(argString(args["severity"]))

		run, findings, err := svc.LatestReview(severities, nil)
		if err != nil {
			return makeErrorResult(err), nil
		}
		if run == nil {
			return makeTextResult("no completed review yet — run repotory_review_run first, or enable the daily review in settings"), nil
		}
		runs, err := svc.ReviewRunsSince(days)
		if err != nil {
			return makeErrorResult(err), nil
		}
		return makeJSONResult(map[string]any{
			"run":      run,
			"findings": findings,
			"runs":     runs,
		})
	})

	mcpServer.AddTool(mcp.Tool{
		Name: "repotory_review_run",
		Description: "Run Cinder's incremental commit review for one day (default: yesterday) over the collected projects, and return the findings. " +
			"Only problems introduced by that day's commits are reported — pre-existing debt is deliberately out of scope. " +
			"Findings come from deterministic Java rules (Feign contracts, swallowed exceptions, self-invoked @Transactional, SQL concatenation, shared SimpleDateFormat, remote calls in loops); a model may be asked to confirm or dismiss each candidate, and 'ai_calls' in the result says how many calls that cost. " +
			"Safe to call repeatedly: the same day and scope is reviewed once, and a completed report is returned instead of being recomputed (pass force to redo it). " +
			"Use dry_run to preview without writing anything.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"date": map[string]any{
					"type":        "string",
					"description": "Day to review as YYYY-MM-DD (default: yesterday)",
				},
				"scope": map[string]any{
					"type":        "string",
					"description": "collected (default, from settings) or project:<id>,<id>",
				},
				"dry_run": map[string]any{
					"type":        "boolean",
					"description": "Compute and return the report without storing it",
				},
				"no_ai": map[string]any{
					"type":        "boolean",
					"description": "Rules only, zero model calls",
				},
				"force": map[string]any{
					"type":        "boolean",
					"description": "Re-run a day that already completed",
				},
			},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		var day time.Time
		if s := argString(args["date"]); s != "" {
			parsed, err := time.ParseInLocation("2006-01-02", s, time.Local)
			if err != nil {
				return makeInvalidResult("date must be YYYY-MM-DD"), nil
			}
			day = parsed
		}
		flags, msg := boolArgs(args, "dry_run", "no_ai", "force")
		if msg != "" {
			return makeInvalidResult(msg), nil
		}
		result, err := svc.RunReview(ctx, service.ReviewOptions{
			Date:   day,
			Scope:  argString(args["scope"]),
			DryRun: flags["dry_run"],
			NoAI:   flags["no_ai"],
			Force:  flags["force"],
		})
		if err != nil {
			return makeErrorResult(err), nil
		}
		if result.AlreadyDone {
			return makeJSONResult(map[string]any{
				"already_done": true,
				"run":          result.Run,
				"hint":         "this day and scope was already reviewed; pass force=true to redo it",
			})
		}
		// A dry run has no stored rows — `Stored` is nil there, and the tool
		// used to answer a preview with an empty findings list. The preview
		// conversion is the same one the CLI uses (service.StoredPreview),
		// so both surfaces show the same shape with real paths.
		findings := result.Stored
		if result.DryRun {
			findings = service.StoredPreview(result.Findings)
		}
		return makeJSONResult(map[string]any{
			"run":      result.Run,
			"findings": findings,
			"dry_run":  result.DryRun,
		})
	})
}

// argString reads an optional string argument.
func argString(v any) string {
	s, _ := v.(string)
	return s
}

// boolArgs reads optional boolean arguments. Only real booleans are accepted
// (matching repotory_notes_update's pinned): a string "true" is a malformed
// argument, not a value to coerce, so silently reading it as false would hide
// the caller's mistake. The second return value is a validation message.
func boolArgs(args map[string]any, names ...string) (map[string]bool, string) {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		raw, present := args[name]
		if !present || raw == nil {
			continue
		}
		b, ok := raw.(bool)
		if !ok {
			return nil, name + " must be a boolean"
		}
		out[name] = b
	}
	return out, ""
}

// splitCSV splits a comma-separated argument, dropping empty parts.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			part := s[start:i]
			for len(part) > 0 && (part[0] == ' ' || part[0] == '\t') {
				part = part[1:]
			}
			for len(part) > 0 && (part[len(part)-1] == ' ' || part[len(part)-1] == '\t') {
				part = part[:len(part)-1]
			}
			if part != "" {
				out = append(out, part)
			}
			start = i + 1
		}
	}
	return out
}
