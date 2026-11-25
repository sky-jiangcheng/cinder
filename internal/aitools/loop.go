package aitools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"repotory/internal/aiclient"
)

// Provider is the resolved chat endpoint the loop posts to. BaseURL is the
// CONFIGURED base ("https://api.deepseek.com" or ".../v1"), never a completions
// endpoint: the loop resolves it through aiclient.CandidateURLs.
type Provider struct {
	BaseURL string
	Model   string
	APIKey  string
}

// MaxRounds bounds the number of model-tool-model iterations.
const MaxRounds = 5

// truncate cuts an error body to max bytes on a rune boundary. Kept local
// instead of reusing aiclient's clip because the tool loop reports raw bodies
// without an ellipsis; error text is user-visible and golden-compared.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut]
}

// Ask runs a single model call with tool definitions, executes any tool calls
// the model requests, feeds the results back, and repeats up to MaxRounds
// times. It returns the final assistant reply and the finish reason.
func Ask(ctx context.Context, p Provider, messages []aiclient.Message, tools []aiclient.ToolDefinition, reg *Registry, timeout time.Duration) (string, string, error) {
	candidates := aiclient.CandidateURLs(p.BaseURL, "/chat/completions")
	url := candidates[0]
	round := 0
	for {
		round++
		body, err := json.Marshal(aiclient.Request{Model: p.Model, Messages: messages, Tools: tools})
		if err != nil {
			return "", "", err
		}
		// Derive the HTTP deadline from the CALLER's ctx, not Background: the
		// loop used to shadow ctx with a Background-derived one, and the tool
		// handlers below then received it AFTER cancel() - every ctx-aware
		// tool (search included) failed with "context canceled".
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		status, rawBody, err := aiclient.PostJSON(callCtx, url, p.APIKey, body)
		cancel()
		if err != nil {
			return "", "", err
		}
		if next, ok := aiclient.NextCandidate(candidates, url, status, string(rawBody)); ok {
			url = next
			// A URL probe is not a tool round: the model has not seen this
			// attempt, so it must not eat into the tool budget.
			round--
			continue
		}
		if status != http.StatusOK {
			return "", "", fmt.Errorf("AI endpoint returned %d: %s", status, truncate(string(rawBody), 300))
		}
		var cr aiclient.Response
		if err := json.Unmarshal(rawBody, &cr); err != nil {
			return "", "", err
		}
		// Not Response.ProviderError(): that helper truncates with clip (ellipsis),
		// while the loop has always shown the raw head of the body.
		if cr.Error != nil {
			kind := strings.TrimSpace(cr.Error.Metadata.ErrorType)
			msg := strings.TrimSpace(cr.Error.Message)
			switch {
			case msg != "" && kind != "":
				return "", "", fmt.Errorf("AI endpoint error (%s): %s", kind, truncate(msg, 300))
			case msg != "":
				return "", "", fmt.Errorf("AI endpoint error: %s", truncate(msg, 300))
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
		calls := msg.ToolCalls
		// Some models emit tool calls as inline markup in content instead of
		// the tool_calls field. Recognising them here is the difference between
		// the loop running the query and the user reading raw call syntax as
		// the answer.
		if len(calls) == 0 {
			calls = InlineToolCalls(assistantContent)
		}
		// The echo carries the tool_calls the model emitted: a tool message is
		// only defined as the response to a PRECEDING assistant message with
		// tool_calls, and strict providers reject the pairing when the
		// assistant half is missing.
		messages = append(messages, aiclient.Message{Role: "assistant", Content: assistantContent, ToolCalls: calls})
		if len(calls) == 0 {
			return assistantContent, cr.Choices[0].FinishReason, nil
		}
		if tools == nil {
			// The previous round hit the cap and forced text-only (below); a
			// provider that still answers with tool calls is broken.
			return "", "", fmt.Errorf("the model kept requesting tools after the final round")
		}
		if round >= MaxRounds {
			// Round cap. Returning the current turn would surface an empty
			// reply - the model spoke in tool_calls, not words. One last call
			// WITHOUT tools forces an answer in text.
			tools = nil
		}
		for i := range calls {
			if calls[i].ID == "" {
				// A provider that never sent an id gets a stable synthetic one;
				// the pairing only has to agree within this conversation.
				calls[i].ID = fmt.Sprintf("call_%d", i)
			}
			result, err := reg.Call(ctx, calls[i].Function.Name, calls[i].Function.Arguments)
			var content string
			if err != nil {
				content = fmt.Sprintf("{\"error\": \"%s\"}", err.Error())
			} else {
				b, _ := json.Marshal(result)
				content = string(b)
			}
			messages = append(messages, aiclient.Message{
				Role:       "tool",
				Content:    content,
				ToolCallID: calls[i].ID,
			})
		}
	}
}

// toolCallAcc accumulates one tool_call across SSE chunks.
type toolCallAcc struct {
	ID   string
	Name string
	Args strings.Builder
}

// AskStream runs a streaming model call with tool definitions, executing any
// tool calls the model requests and feeding results back, repeating up to
// MaxRounds times. Content deltas flow to onDelta as they arrive; tool
// start/result flow to onToolCall/onToolResult. Returns the final assistant
// reply and finish reason.
func AskStream(
	ctx context.Context,
	p Provider,
	messages []aiclient.Message,
	tools []aiclient.ToolDefinition,
	reg *Registry,
	onDelta func(string),
	onToolCall func(id, name string),
	onToolResult func(id string, result any),
	timeout time.Duration,
) (string, aiclient.FinishReasons, error) {
	candidates := aiclient.CandidateURLs(p.BaseURL, "/chat/completions")
	url := candidates[0]
	round := 0
	for {
		round++
		// StreamRequest, not Request: the flag is what makes the endpoint
		// answer in SSE. Without it OpenAI-compatible providers return a
		// single JSON body that parseSSE skips line by line, yielding an
		// empty reply with no error — the UI then spins forever.
		body, err := json.Marshal(aiclient.StreamRequest{Model: p.Model, Messages: messages, Tools: tools, Stream: true})
		if err != nil {
			return "", "", err
		}
		// Same rule as the batch loop: the HTTP deadline derives from the
		// caller's ctx so tool handlers get a live one.
		callCtx, cancel := context.WithTimeout(ctx, timeout)

		var acc strings.Builder
		var calls []toolCallAcc

		status, finishReason, err := aiclient.PostJSONToolStream(callCtx, url, p.APIKey, body, func(content string) {
			acc.WriteString(content)
			if onDelta != nil {
				onDelta(content)
			}
		}, func(tc aiclient.ToolCallDelta) {
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
		})
		cancel()
		if err != nil {
			if next, ok := aiclient.NextCandidate(candidates, url, status, err.Error()); ok {
				url = next
				// A URL probe is not a tool round: the model has not seen this
				// attempt, so it must not eat into the tool budget.
				round--
				continue
			}
			return "", "", err
		}
		// Inline tool calls fold into the same accumulator shape the native
		// protocol uses, so execution below is one path for both forms.
		if len(calls) == 0 {
			for _, tc := range InlineToolCalls(acc.String()) {
				c := toolCallAcc{ID: tc.ID, Name: tc.Function.Name}
				c.Args.WriteString(tc.Function.Arguments)
				calls = append(calls, c)
			}
		}
		if len(calls) == 0 {
			return acc.String(), finishReason, nil
		}
		if tools == nil {
			// The previous round hit the cap and forced text-only (below); a
			// provider that still answers with tool calls is broken.
			return "", "", fmt.Errorf("the model kept requesting tools after the final round")
		}
		if round >= MaxRounds {
			tools = nil
		}
		for i := range calls {
			if calls[i].ID == "" {
				calls[i].ID = fmt.Sprintf("call_%d", i)
			}
		}
		echo := make([]aiclient.ToolCall, len(calls))
		for i, tc := range calls {
			echo[i] = aiclient.ToolCall{ID: tc.ID, Type: "function"}
			echo[i].Function.Name = tc.Name
			echo[i].Function.Arguments = tc.Args.String()
		}
		messages = append(messages, aiclient.Message{Role: "assistant", Content: acc.String(), ToolCalls: echo})

		for _, tc := range calls {
			if onToolCall != nil {
				onToolCall(tc.ID, tc.Name)
			}
			result, err := reg.Call(ctx, tc.Name, tc.Args.String())
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
			messages = append(messages, aiclient.Message{
				Role:       "tool",
				Content:    content,
				ToolCallID: tc.ID,
			})
		}
	}
}
