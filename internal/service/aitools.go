package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"repotory/internal/db"
	"repotory/internal/stats"
)

// ---------------------------------------------------------------------------
// OpenAI-compatible Function Calling types
// ---------------------------------------------------------------------------

// toolDefinition is the OpenAI-compatible "tools" array entry.
type toolDefinition struct {
	Type     string `json:"type"`
	Function struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Parameters  any    `json:"parameters"`
	} `json:"function"`
}

// toolCall is one tool invocation the model asked for.
type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// toolFunc is the server-side implementation of one callable tool.
type toolFunc func(ctx context.Context, args map[string]any) (any, error)

// toolRegistry maps a tool name to its OpenAI definition and implementation.
type toolRegistry struct {
	defs  []toolDefinition
	funcs map[string]toolFunc
}

func newToolRegistry() *toolRegistry {
	return &toolRegistry{funcs: make(map[string]toolFunc)}
}

func (r *toolRegistry) register(name, desc string, parameters any, fn toolFunc) {
	r.defs = append(r.defs, toolDefinition{
		Type: "function",
		Function: struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Parameters  any    `json:"parameters"`
		}{
			Name:        name,
			Description: desc,
			Parameters:  parameters,
		},
	})
	r.funcs[name] = fn
}

func (r *toolRegistry) definitions() []toolDefinition {
	if len(r.defs) == 0 {
		return nil
	}
	return r.defs
}

func (r *toolRegistry) call(ctx context.Context, name, rawArgs string) (any, error) {
	fn, ok := r.funcs[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	var args map[string]any
	if rawArgs != "" {
		if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
			return nil, fmt.Errorf("invalid arguments for %s: %w", name, err)
		}
	}
	return fn(ctx, args)
}

// maxToolRounds bounds the number of model→tool→model iterations.
const maxToolRounds = 5

// registerDashboardTools registers the tools that surface the dashboard's
// code-stats and project-listing capabilities to the AI conversation.
func (s *Service) registerDashboardTools(reg *toolRegistry) {
	reg.register(
		"query_daily_stats",
		"Query daily code-change stats for one repository on a given date. Returns files_changed, lines_added, lines_deleted and commits.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"repo_path": map[string]any{"type": "string", "description": "Absolute path to a Git repository"},
				"date":      map[string]any{"type": "string", "description": "Date in YYYY-MM-DD format"},
				"author":    map[string]any{"type": "string", "description": "Optional Git author filter"},
			},
			"required": []string{"repo_path", "date"},
		},
		s.toolQueryDailyStats,
	)
	reg.register(
		"list_projects",
		"List all known projects in Cinder, with names, root paths and repository counts.",
		map[string]any{"type": "object", "properties": map[string]any{}},
		s.toolListProjects,
	)
	reg.register(
		"get_project_stats",
		"Get aggregated daily code-change stats for one project on a given date.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project_id": map[string]any{"type": "number", "description": "Project ID (from list_projects)"},
				"date":       map[string]any{"type": "string", "description": "Date in YYYY-MM-DD format"},
			},
			"required": []string{"project_id", "date"},
		},
		s.toolGetProjectStats,
	)
	reg.register(
		"search_notes",
		"Search the local knowledge base (notes and pages) for relevant content.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":      map[string]any{"type": "string", "description": "The search query"},
				"project_id": map[string]any{"type": "number", "description": "Optional project ID to scope the search"},
			},
			"required": []string{"query"},
		},
		s.toolSearchNotes,
	)
}

func (s *Service) toolQueryDailyStats(ctx context.Context, args map[string]any) (any, error) {
	repoPath, _ := args["repo_path"].(string)
	date, _ := args["date"].(string)
	author, _ := args["author"].(string)
	if repoPath == "" {
		return nil, fmt.Errorf("repo_path is required")
	}
	if date == "" {
		date = s.git.GetYesterdayDate()
	}
	if err := s.git.ValidateDate(date); err != nil {
		return nil, fmt.Errorf("invalid date %q: %w", date, err)
	}
	res, err := stats.QueryStats(repoPath, date, author)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return map[string]any{"repo_path": repoPath, "date": date, "author": author, "note": "no git data"}, nil
	}
	return map[string]any{
		"repo_path":     repoPath,
		"date":          date,
		"author":        author,
		"files_changed": res.FilesChanged,
		"lines_added":   res.LinesAdded,
		"lines_deleted": res.LinesDeleted,
		"commits":       res.Commits,
	}, nil
}

func (s *Service) toolListProjects(ctx context.Context, _ map[string]any) (any, error) {
	projects, err := db.GetAllProjects(s.db)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(projects))
	for _, p := range projects {
		repos, _ := db.GetRepositoriesByProjectID(s.db, p.ID)
		out = append(out, map[string]any{
			"project_id": p.ID,
			"name":       p.Name,
			"root_path":  p.RootPath,
			"repo_count": len(repos),
			"is_starred": p.IsStarred,
			"collected":  p.Collected,
		})
	}
	return out, nil
}

func (s *Service) toolGetProjectStats(ctx context.Context, args map[string]any) (any, error) {
	projectID := int64(0)
	if v, ok := args["project_id"].(float64); ok {
		projectID = int64(v)
	}
	date, _ := args["date"].(string)
	if projectID == 0 {
		return nil, fmt.Errorf("project_id is required")
	}
	if date == "" {
		date = s.git.GetYesterdayDate()
	}
	if err := s.git.ValidateDate(date); err != nil {
		return nil, fmt.Errorf("invalid date %q: %w", date, err)
	}
	statsList, err := db.GetStatsByProject(s.db, projectID, date)
	if err != nil {
		return nil, err
	}
	repos, _ := db.GetRepositoriesByProjectID(s.db, projectID)
	project, err := db.GetProjectByID(s.db, projectID)
	if err != nil {
		return nil, err
	}
	var totalAdded, totalDeleted, totalFiles int
	authorStats := make(map[string]map[string]int)
	for _, st := range statsList {
		totalAdded += st.LinesAdded
		totalDeleted += st.LinesDeleted
		totalFiles += st.FilesChanged
		if authorStats[st.Author] == nil {
			authorStats[st.Author] = map[string]int{}
		}
		authorStats[st.Author]["lines_added"] += st.LinesAdded
		authorStats[st.Author]["lines_deleted"] += st.LinesDeleted
		authorStats[st.Author]["files_changed"] += st.FilesChanged
	}
	repoList := make([]map[string]any, 0, len(repos))
	for _, r := range repos {
		repoList = append(repoList, map[string]any{
			"repo_id": r.ID,
			"path":    r.Path,
			"name":    r.Path,
		})
	}
	return map[string]any{
		"project_id":   projectID,
		"project_name": project.Name,
		"date":         date,
		"total": map[string]any{
			"lines_added":   totalAdded,
			"lines_deleted": totalDeleted,
			"files_changed": totalFiles,
		},
		"by_author": authorStats,
		"repos":     repoList,
	}, nil
}

func (s *Service) toolSearchNotes(ctx context.Context, args map[string]any) (any, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	projectID := int64(0)
	if v, ok := args["project_id"].(float64); ok {
		projectID = int64(v)
	}
	hits := s.SearchAll(query)
	out := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		if projectID > 0 && h.ProjectID != projectID {
			continue
		}
		out = append(out, map[string]any{
			"id":         h.ID,
			"type":       h.Type,
			"title":      h.Title,
			"snippet":    h.Snippet,
			"project_id": h.ProjectID,
			"rank":       h.Rank,
		})
		if len(out) >= 10 {
			break
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Tool-calling loop for AskAI / AskAIWithEvidence
// ---------------------------------------------------------------------------

// toolChatRequest is the OpenAI-compatible request body with tools support.
type toolChatRequest struct {
	Model    string           `json:"model"`
	Messages []chatMessage    `json:"messages"`
	Tools    []toolDefinition `json:"tools,omitempty"`
}

// toolChatResponse is the response body that may carry tool_calls.
type toolChatResponse struct {
	Choices []struct {
		Message struct {
			Content   string     `json:"content"`
			ToolCalls []toolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *chatErrorEnvelope `json:"error"`
}

// askWithTools runs a single model call with tool definitions, executes any
// tool calls the model requests, feeds results back, and repeats up to
// maxToolRounds times. Returns the final assistant reply and finish reason.
func (s *Service) askWithTools(cfg aiChatConfig, messages []chatMessage, tools []toolDefinition, timeout time.Duration) (string, string, error) {
	// cfg.baseURL is the CONFIGURED base ("https://api.deepseek.com" or
	// ".../v1"), never a completions endpoint. The plain and streaming chat
	// paths resolve it through aiCandidateURLs; this loop used to post to the
	// bare base, which is a 404 on every provider whose completions route sits
	// one segment deeper — the "AI endpoint returned 404: Not Found" the
	// assistant panel showed while Settings → AI reported a healthy endpoint.
	// Candidates are tried in the same order, with the same rule as those
	// paths: a 404 means "wrong shape of URL, try the next one".
	candidates := aiCandidateURLs(cfg.baseURL, "/chat/completions")
	url := candidates[0]
	round := 0
	for {
		round++
		body, err := json.Marshal(toolChatRequest{
			Model:    cfg.model,
			Messages: messages,
			Tools:    tools,
		})
		if err != nil {
			return "", "", err
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		status, rawBody, err := aiPostJSON(ctx, url, cfg.apiKey, body)
		cancel()
		if err != nil {
			return "", "", err
		}
		if next, ok := nextAICandidate(candidates, url, status); ok {
			url = next
			// A URL probe is not a tool round: the model has not seen this
			// attempt, so it must not eat into the tool budget.
			round--
			continue
		}
		if status != http.StatusOK {
			return "", "", fmt.Errorf("AI endpoint returned %d: %s", status, truncateBytes(string(rawBody), 300))
		}
		var cr toolChatResponse
		if err := json.Unmarshal(rawBody, &cr); err != nil {
			return "", "", err
		}
		if cr.Error != nil {
			kind := strings.TrimSpace(cr.Error.Metadata.ErrorType)
			msg := strings.TrimSpace(cr.Error.Message)
			switch {
			case msg != "" && kind != "":
				return "", "", fmt.Errorf("AI endpoint error (%s): %s", kind, truncateBytes(msg, 300))
			case msg != "":
				return "", "", fmt.Errorf("AI endpoint error: %s", truncateBytes(msg, 300))
			case kind != "":
				return "", "", fmt.Errorf("AI endpoint error (%s)", kind)
			default:
				return "", "", fmt.Errorf("AI endpoint returned an error")
			}
		}
		if len(cr.Choices) == 0 {
			return "", "", fmt.Errorf("no choices returned")
		}
		msg := cr.Choices[0].Message
		assistantContent := msg.Content
		// The echo carries the tool_calls the model emitted: a tool message is
		// only defined as the response to a PRECEDING assistant message with
		// tool_calls, and strict providers reject the pairing when the
		// assistant half is missing.
		messages = append(messages, chatMessage{Role: "assistant", Content: assistantContent, ToolCalls: msg.ToolCalls})
		if len(msg.ToolCalls) == 0 {
			return assistantContent, cr.Choices[0].FinishReason, nil
		}
		if tools == nil {
			// The previous round hit the cap and forced text-only (below); a
			// provider that still answers with tool calls is broken.
			return "", "", fmt.Errorf("the model kept requesting tools after the final round")
		}
		if round >= maxToolRounds {
			// Round cap. Returning the current turn would surface an empty
			// reply — the model spoke in tool_calls, not words. One last call
			// WITHOUT tools forces an answer in text.
			tools = nil
		}
		for _, call := range msg.ToolCalls {
			result, err := s.toolRegistry.call(context.Background(), call.Function.Name, call.Function.Arguments)
			var content string
			if err != nil {
				content = fmt.Sprintf("{\"error\": \"%s\"}", err.Error())
			} else {
				b, _ := json.Marshal(result)
				content = string(b)
			}
			messages = append(messages, chatMessage{
				Role:       "tool",
				Content:    content,
				ToolCallID: call.ID,
			})
		}
	}
}

// ---------------------------------------------------------------------------
// Additional dashboard tools
// ---------------------------------------------------------------------------

// registerDashboardToolsExtra registers the additional dashboard tools,
// including the batch-project query tool used when a user picks several
// projects.
func (s *Service) registerDashboardToolsExtra(reg *toolRegistry) {
	reg.register(
		"get_recent_commits",
		"Get the most recent commits across one project's repositories, newest first. Returns hash, time, message, author and branch per commit.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project_id": map[string]any{"type": "number", "description": "Project ID (from list_projects)"},
				"limit":      map[string]any{"type": "number", "description": "Max commits to return (default 10, max 50)"},
			},
			"required": []string{"project_id"},
		},
		s.toolGetRecentCommits,
	)
	reg.register(
		"get_heatmap",
		"Get daily code-change heatmap for one project over a trailing window of days. Returns per-day lines_added, lines_deleted and commits.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project_id": map[string]any{"type": "number", "description": "Project ID (from list_projects)"},
				"days":       map[string]any{"type": "number", "description": "Number of trailing days (default 30, max 90)"},
			},
			"required": []string{"project_id"},
		},
		s.toolGetHeatmap,
	)
	reg.register(
		"get_project_overview",
		"Get a project's overview: name, path, repository count, total and personal lines added/deleted for today, and whether today is below the daily standard.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project_id": map[string]any{"type": "number", "description": "Project ID (from list_projects)"},
			},
			"required": []string{"project_id"},
		},
		s.toolGetProjectOverview,
	)
	reg.register(
		"query_project_stats",
		"Query aggregate daily code-change stats for one or more projects. Required when the user has selected several projects from project list. Returns one record per project.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "number"},
					"description": "Project IDs obtained from list_projects.",
				},
				"date": map[string]any{
					"type":        "string",
					"description": "Date in YYYY-MM-DD format.",
				},
			},
			"required": []string{"project_ids", "date"},
		},
		s.toolQueryProjectStats,
	)
}

func (s *Service) toolGetRecentCommits(ctx context.Context, args map[string]any) (any, error) {
	projectID := int64(0)
	if v, ok := args["project_id"].(float64); ok {
		projectID = int64(v)
	}
	limit := 10
	if v, ok := args["limit"].(float64); ok && v > 0 {
		if int(v) < limit {
			limit = int(v)
		}
		if limit > 50 {
			limit = 50
		}
	}
	if projectID == 0 {
		return nil, fmt.Errorf("project_id is required")
	}
	repos, err := db.GetRepositoriesByProjectID(s.db, projectID)
	if err != nil {
		return nil, err
	}
	if len(repos) == 0 {
		return []map[string]any{}, nil
	}
	var paths []string
	for _, r := range repos {
		paths = append(paths, r.Path)
	}
	commits, err := stats.GetRecentCommits(paths, "", limit)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(commits))
	for _, c := range commits {
		out = append(out, map[string]any{
			"hash":    c.Hash,
			"time":    c.Time,
			"message": c.Message,
			"author":  c.Author,
			"repo":    c.Repo,
			"branch":  c.Branch,
		})
	}
	return out, nil
}

func (s *Service) toolGetHeatmap(ctx context.Context, args map[string]any) (any, error) {
	projectID := int64(0)
	if v, ok := args["project_id"].(float64); ok {
		projectID = int64(v)
	}
	days := 30
	if v, ok := args["days"].(float64); ok && v > 0 {
		if int(v) < days {
			days = int(v)
		}
		if days > 90 {
			days = 90
		}
	}
	if projectID == 0 {
		return nil, fmt.Errorf("project_id is required")
	}
	today := stats.GetTodayDate()
	start := stats.DaysBefore(today, days-1)
	heatmap, err := db.GetHeatmapData(s.db, start, today, "", projectID)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(heatmap))
	for _, h := range heatmap {
		out = append(out, map[string]any{
			"date":          h.Date,
			"lines_added":   h.LinesAdded,
			"lines_deleted": h.LinesDeleted,
			"commits":       h.Commits,
		})
	}
	return out, nil
}

func (s *Service) toolGetProjectOverview(ctx context.Context, args map[string]any) (any, error) {
	projectID := int64(0)
	if v, ok := args["project_id"].(float64); ok {
		projectID = int64(v)
	}
	if projectID == 0 {
		return nil, fmt.Errorf("project_id is required")
	}
	project, err := db.GetProjectByID(s.db, projectID)
	if err != nil {
		return nil, err
	}
	repos, err := db.GetRepositoriesByProjectID(s.db, projectID)
	if err != nil {
		return nil, err
	}
	today := s.git.GetTodayDate()
	statsList, err := db.GetStatsByProject(s.db, projectID, today)
	if err != nil {
		return nil, err
	}
	var totalAdded, totalDeleted, myAdded, myDeleted int
	author := s.gitUser()
	for _, st := range statsList {
		totalAdded += st.LinesAdded
		totalDeleted += st.LinesDeleted
		if st.Author == author {
			myAdded += st.LinesAdded
			myDeleted += st.LinesDeleted
		}
	}
	codeStd := s.dailyCodeStandard()
	isWorkday := s.git.IsWorkday(today)
	belowStandard := isWorkday && myAdded < codeStd
	return map[string]any{
		"project_id":   projectID,
		"project_name": project.Name,
		"root_path":    project.RootPath,
		"repo_count":   len(repos),
		"date":         today,
		"total": map[string]any{
			"lines_added":   totalAdded,
			"lines_deleted": totalDeleted,
		},
		"mine": map[string]any{
			"lines_added":   myAdded,
			"lines_deleted": myDeleted,
		},
		"is_workday":     isWorkday,
		"below_standard": belowStandard,
		"daily_standard": codeStd,
	}, nil
}

// ---------------------------------------------------------------------------
// Streaming tool-calling loop (for AskAIWithEvidenceStream)
// ---------------------------------------------------------------------------

// streamToolChoice is one tool_call entry inside a streaming delta chunk.
type streamToolChoice struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// streamToolDelta is one SSE data: payload of a streaming tool-enabled response.
type streamToolDelta struct {
	Choices []struct {
		Delta struct {
			Content   string             `json:"content"`
			ToolCalls []streamToolChoice `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// toolCallAcc accumulates one tool_call across multiple SSE chunks.
type toolCallAcc struct {
	Index int
	ID    string
	Name  string
	Args  strings.Builder
}

// aiPostJSONToolStream posts a tool-enabled streaming request to ONE url and
// delivers each decoded SSE chunk to onDelta.
//
// The status comes back BESIDE the error, not inside it, so the caller can
// tell "this URL shape is wrong" (404 — try the next candidate) from "the
// endpoint answered" (401, 5xx — do not retry). Falls back to localhost like
// the batch path.
func aiPostJSONToolStream(ctx context.Context, rawURL, apiKey string, body []byte, onDelta func(streamToolDelta)) (int, error) {
	try := func(u string) (int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
		if err != nil {
			return 0, err
		}
		req.Header.Set("Content-Type", "application/json")
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
			return resp.StatusCode, fmt.Errorf("AI endpoint returned %d: %s", resp.StatusCode, truncateBytes(string(raw), 300))
		}
		return resp.StatusCode, parseSSEToolStream(resp.Body, onDelta)
	}
	status, err := try(rawURL)
	if err != nil {
		if fallback := aiLocalhostFallback(rawURL); fallback != "" {
			status, err = try(fallback)
		}
	}
	return status, err
}

// parseSSEToolStream consumes an OpenAI-compatible SSE stream that may carry
// content deltas and tool_call deltas. Each chunk is handed to onDelta.
func parseSSEToolStream(r io.Reader, onDelta func(streamToolDelta)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 16*1024), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			break
		}
		var delta streamToolDelta
		if err := json.Unmarshal([]byte(payload), &delta); err != nil {
			return fmt.Errorf("malformed SSE chunk: %w", err)
		}
		if delta.Error != nil && delta.Error.Message != "" {
			return fmt.Errorf("%s", delta.Error.Message)
		}
		onDelta(delta)
	}
	return sc.Err()
}

// streamAskWithTools runs a streaming model call with tool definitions,
// executing any tool calls the model requests and feeding results back,
// repeating up to maxToolRounds times. Content deltas flow to onDelta as they
// arrive; tool call start/result flow to onToolCall/onToolResult.
// Returns the final assistant reply and finish reason.
func (s *Service) streamAskWithTools(
	cfg aiChatConfig,
	messages []chatMessage,
	tools []toolDefinition,
	onDelta func(string),
	onToolCall func(id, name string),
	onToolResult func(id string, result any),
	timeout time.Duration,
) (string, finishReasons, error) {
	// Same URL resolution as the batch tool loop: cfg.baseURL is a BASE
	// ("https://api.deepseek.com", ".../v1"), never a completions endpoint.
	// The assistant panel streams, so this path is the one the user actually
	// hits — posting it to the bare base is what produced the 404.
	candidates := aiCandidateURLs(cfg.baseURL, "/chat/completions")
	url := candidates[0]
	round := 0
	for {
		round++
		body, err := json.Marshal(toolChatRequest{
			Model:    cfg.model,
			Messages: messages,
			Tools:    tools,
		})
		if err != nil {
			return "", "", err
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)

		var acc strings.Builder
		var finishReason finishReasons
		var calls []toolCallAcc

		status, err := aiPostJSONToolStream(ctx, url, cfg.apiKey, body, func(delta streamToolDelta) {
			for _, c := range delta.Choices {
				if c.Delta.Content != "" {
					acc.WriteString(c.Delta.Content)
					if onDelta != nil {
						onDelta(c.Delta.Content)
					}
				}
				for _, tc := range c.Delta.ToolCalls {
					for len(calls) <= tc.Index {
						calls = append(calls, toolCallAcc{})
					}
					tca := &calls[tc.Index]
					if tc.ID != "" {
						tca.ID = tc.ID
					}
					if tc.Function.Name != "" {
						tca.Name = tc.Function.Name
					}
					if tc.Function.Arguments != "" {
						tca.Args.WriteString(tc.Function.Arguments)
					}
				}
				if c.FinishReason != nil && *c.FinishReason != "" {
					finishReason = finishReasons(*c.FinishReason)
				}
			}
		})
		cancel()
		if err != nil {
			if next, ok := nextAICandidate(candidates, url, status); ok {
				url = next
				// A URL probe is not a tool round: the model has not seen this
				// attempt, so it must not eat into the tool budget.
				round--
				continue
			}
			return "", "", err
		}

		// No tool calls: final answer.
		if len(calls) == 0 {
			return acc.String(), finishReason, nil
		}

		if tools == nil {
			// The previous round hit the cap and forced text-only (below); a
			// provider that still answers with tool calls is broken.
			return "", "", fmt.Errorf("the model kept requesting tools after the final round")
		}
		if round >= maxToolRounds {
			// Round cap. Returning the current turn would surface an empty
			// reply — the model spoke in tool_calls, not words. One last call
			// WITHOUT tools forces an answer in text.
			tools = nil
		}

		// Tool calls: echo the assistant turn WITH the calls it made (a tool
		// result is only defined as the response to them), then execute each
		// and feed the results back. A provider that never sent an id gets a
		// stable synthetic one — the pairing only has to agree within this
		// conversation.
		for i := range calls {
			if calls[i].ID == "" {
				calls[i].ID = fmt.Sprintf("call_%d", i)
			}
		}
		echo := make([]toolCall, len(calls))
		for i, tc := range calls {
			echo[i] = toolCall{ID: tc.ID, Type: "function"}
			echo[i].Function.Name = tc.Name
			echo[i].Function.Arguments = tc.Args.String()
		}
		messages = append(messages, chatMessage{Role: "assistant", Content: acc.String(), ToolCalls: echo})

		for _, tc := range calls {
			if onToolCall != nil {
				onToolCall(tc.ID, tc.Name)
			}
			result, err := s.toolRegistry.call(context.Background(), tc.Name, tc.Args.String())
			var content string
			if err != nil {
				content = fmt.Sprintf("{\"error\": \"%s\"}", err.Error())
			} else {
				b, _ := json.Marshal(result)
				content = string(b)
			}
			if onToolResult != nil {
				onToolResult(tc.ID, result)
			}
			messages = append(messages, chatMessage{
				Role:       "tool",
				Content:    content,
				ToolCallID: tc.ID,
			})
		}
	}
}

func (s *Service) toolQueryProjectStats(ctx context.Context, args map[string]any) (any, error) {
	projectIDsRaw, _ := args["project_ids"].([]any)
	date, _ := args["date"].(string)
	if len(projectIDsRaw) == 0 {
		return nil, fmt.Errorf("project_ids is required")
	}
	if date == "" {
		date = s.git.GetYesterdayDate()
	}
	if err := s.git.ValidateDate(date); err != nil {
		return nil, fmt.Errorf("invalid date %q: %w", date, err)
	}
	out := make([]any, 0, len(projectIDsRaw))
	for _, v := range projectIDsRaw {
		pid, ok := v.(float64)
		if !ok {
			return nil, fmt.Errorf("invalid project_id %v", v)
		}
		res, err := s.toolGetProjectStats(ctx, map[string]any{
			"project_id": pid,
			"date":       date,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
}

// queryStatsBatchStat is the payload returned for one project when batch-querying
// project code-change stats from the dashboard.
type queryStatsBatchStat struct {
	ProjectID   int64  `json:"project_id"`
	ProjectName string `json:"project_name"`
	Date        string `json:"date"`
	Total       struct {
		LinesAdded   int `json:"lines_added"`
		LinesDeleted int `json:"lines_deleted"`
		FilesChanged int `json:"files_changed"`
	} `json:"total"`
	ByAuthor map[string]map[string]int `json:"by_author"`
	Repos    []map[string]any          `json:"repos"`
}

func (s *Service) QueryProjectStatsBatch(projectIDs []int64, date string) ([]queryStatsBatchStat, error) {
	if date == "" {
		date = s.git.GetYesterdayDate()
	}
	if err := s.git.ValidateDate(date); err != nil {
		return nil, fmt.Errorf("invalid date %q: %w", date, err)
	}
	out := make([]queryStatsBatchStat, 0, len(projectIDs))
	for _, pid := range projectIDs {
		statsList, err := db.GetStatsByProject(s.db, pid, date)
		if err != nil {
			return nil, err
		}
		repos, _ := db.GetRepositoriesByProjectID(s.db, pid)
		project, err := db.GetProjectByID(s.db, pid)
		if err != nil {
			return nil, err
		}

		var totalAdded, totalDeleted, totalFiles int
		byAuthor := make(map[string]map[string]int)
		for _, st := range statsList {
			totalAdded += st.LinesAdded
			totalDeleted += st.LinesDeleted
			totalFiles += st.FilesChanged
			if byAuthor[st.Author] == nil {
				byAuthor[st.Author] = map[string]int{}
			}
			byAuthor[st.Author]["lines_added"] += st.LinesAdded
			byAuthor[st.Author]["lines_deleted"] += st.LinesDeleted
			byAuthor[st.Author]["files_changed"] += st.FilesChanged
		}
		repoList := make([]map[string]any, 0, len(repos))
		for _, r := range repos {
			repoList = append(repoList, map[string]any{
				"repo_id": r.ID,
				"path":    r.Path,
				"name":    r.Path,
			})
		}
		out = append(out, queryStatsBatchStat{
			ProjectID:   pid,
			ProjectName: project.Name,
			Date:        date,
			Total: struct {
				LinesAdded   int `json:"lines_added"`
				LinesDeleted int `json:"lines_deleted"`
				FilesChanged int `json:"files_changed"`
			}{
				LinesAdded:   totalAdded,
				LinesDeleted: totalDeleted,
				FilesChanged: totalFiles,
			},
			ByAuthor: byAuthor,
			Repos:    repoList,
		})
	}
	return out, nil
}
