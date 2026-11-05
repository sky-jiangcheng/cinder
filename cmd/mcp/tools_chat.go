package main

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"repotory/internal/service"
)

func registerChatTool(s *server.MCPServer, svc *service.Service) {
	s.AddTool(mcp.Tool{
		Name:        "repotory_chat",
		Description: "Share persistent chat threads and messages between Warden and IDE clients. Isolated from knowledge notes, retrieval and approvals. Operations: list (at most 100, optional exact path; empty means all), create (client UUID id, title, optional absolute path — absent or empty is unscoped), read (thread_id; latest 500 messages in insertion order), append (thread_id, client UUID or UUID-reply id, role, content, source, optional reply_to referencing a user UUID in this thread), rename (thread_id, title). Create/append retries with identical IDs and payloads are idempotent; conflicts never overwrite content.",
		InputSchema: mcp.ToolInputSchema{
			Type:     "object",
			Required: []string{"operation"},
			Properties: map[string]any{
				"operation": map[string]any{"type": "string", "enum": []string{"list", "create", "read", "append", "rename"}},
				"path":      map[string]any{"type": "string", "description": "Absolute workspace path or empty; at most 4096 UTF-8 bytes. Stored verbatim; list matches exactly."},
				"id":        map[string]any{"type": "string", "description": "Client-generated UUID for create; UUID or UUID-reply for append idempotency."},
				"thread_id": map[string]any{"type": "string", "description": "Existing chat thread UUID."},
				"title":     map[string]any{"type": "string", "description": "Nonblank title without control characters; at most 160 Unicode characters.", "maxLength": service.MaxChatTitleLen},
				"role":      map[string]any{"type": "string", "enum": []string{"user", "assistant"}},
				"content":   map[string]any{"type": "string", "description": "Nonblank message text; at most 1048576 UTF-8 bytes. Preserved verbatim."},
				"source":    map[string]any{"type": "string", "enum": []string{"warden", "vscode", "jetbrains"}},
				"reply_to":  map[string]any{"type": "string", "description": "Optional UUID of a user message in this thread."},
			},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		op, err := toolString(args, "operation", true)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		required, ok := map[string][]string{
			"list":   {},
			"create": {"id", "title"},
			"read":   {"thread_id"},
			"append": {"thread_id", "id", "role", "content", "source"},
			"rename": {"thread_id", "title"},
		}[op]
		if !ok {
			return mcp.NewToolResultError("operation must be list, create, read, append or rename"), nil
		}
		values := map[string]string{}
		for _, key := range required {
			value, err := toolString(args, key, true)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			values[key] = value
		}
		var payload any
		switch op {
		case "list":
			path, e := toolString(args, "path", false)
			if e != nil {
				return mcp.NewToolResultError(e.Error()), nil
			}
			threads, e := svc.ListChatThreads(ctx, path)
			err, payload = e, struct {
				Threads []service.ChatThread `json:"threads"`
			}{threads}
		case "create":
			// Supplied-but-not-required: absent and "" both mean unscoped, while a
			// numeric/null path must not silently expand to the whole library.
			path, e := toolString(args, "path", false)
			if e != nil {
				return mcp.NewToolResultError(e.Error()), nil
			}
			thread, e := svc.CreateChatThread(ctx, values["id"], values["title"], path)
			err, payload = e, struct {
				Thread *service.ChatThread `json:"thread"`
			}{thread}
		case "read":
			payload, err = svc.ReadChatThread(ctx, values["thread_id"])
		case "append":
			var reply *string
			if _, supplied := args["reply_to"]; supplied {
				value, e := toolString(args, "reply_to", true)
				if e != nil {
					return mcp.NewToolResultError(e.Error()), nil
				}
				reply = &value
			}
			message, e := svc.AppendChatMessage(ctx, service.ChatAppendInput{
				ThreadID: values["thread_id"], ID: values["id"], Role: values["role"],
				Content: values["content"], Source: values["source"], ReplyTo: reply,
			})
			err, payload = e, struct {
				Message *service.ChatMessage `json:"message"`
			}{message}
		case "rename":
			thread, e := svc.RenameChatThread(ctx, values["thread_id"], values["title"])
			err, payload = e, struct {
				Thread *service.ChatThread `json:"thread"`
			}{thread}
		}
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// makeJSONResult serializes payload directly: no name/data wrapper.
		return makeJSONResult("chat", payload)
	})
}

// toolString distinguishes absent optional strings from malformed values. A
// numeric/null path must not silently become "" and expand to the whole library.
func toolString(args map[string]any, key string, required bool) (string, error) {
	value, present := args[key]
	if !present && !required {
		return "", nil
	}
	if !present {
		return "", fmt.Errorf("%s is required", key)
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return text, nil
}
