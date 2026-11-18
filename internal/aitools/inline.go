package aitools

import (
	"encoding/json"
	"strings"

	"repotory/internal/aiclient"
)

const (
	inlineOpen  = "<tool_call>"
	inlineClose = "</tool_call>"
	paramClose  = "</parameter>"
)

// InlineToolCalls parses the tool-call form some models emit as CONTENT instead
// of the OpenAI tool_calls field: a tool_call element wrapping a function element
// whose parameters carry k/v pairs. Observed live against nvidia/nemotron on
// OpenRouter (2026-11-11): the header told the model which tools exist, it chose
// the right repo and date, and the loop never ran the call - the raw markup
// reached the user as the answer. Only the parameters' inner text is untrusted
// model output and is never executed as anything but a JSON argument value;
// names must match a REGISTERED tool before anything runs. Returns nil when the
// content carries no inline calls; the caller keeps the text as the answer.
func InlineToolCalls(content string) []aiclient.ToolCall {
	var calls []aiclient.ToolCall
	for {
		start := strings.Index(content, inlineOpen)
		if start < 0 {
			break
		}
		rest := content[start+len(inlineOpen):]
		end := strings.Index(rest, inlineClose)
		if end < 0 {
			break
		}
		body := rest[:end]
		content = rest[end+len(inlineClose):]
		call := aiclient.ToolCall{Type: "function"}
		if name := between(body, "<function=", ">"); name != "" {
			call.Function.Name = name
		}
		if call.Function.Name == "" {
			call.Function.Name = between(body, "<function>", "</function>")
		}
		if call.Function.Name == "" {
			continue
		}
		args := map[string]any{}
		for _, chunk := range strings.Split(body, "<parameter=")[1:] {
			eq := strings.Index(chunk, ">")
			if eq < 0 {
				continue
			}
			key := chunk[:eq]
			val := chunk[eq+1:]
			if !strings.Contains(val, paramClose) {
				continue
			}
			args[strings.TrimSpace(key)] = strings.TrimSpace(val[:strings.Index(val, paramClose)])
		}
		b, err := json.Marshal(args)
		if err != nil {
			continue
		}
		call.Function.Arguments = string(b)
		calls = append(calls, call)
	}
	return calls
}

// between returns the text between the first occurrence of open and the
// following close, or "" when either is missing. Named to stay clear of
// review_ai.go's byte-delimited between.
func between(s, open, close string) string {
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// ContentWithoutInlineCalls strips the inline tool-call markup so a mixed turn
// (prose plus calls) does not repeat the call syntax around the answer. The
// full original content is still echoed to the provider on the follow-up
// round; this cleaned form is only for display if the turn ends up final.
func ContentWithoutInlineCalls(content string) string {
	out := content
	for {
		start := strings.Index(out, inlineOpen)
		if start < 0 {
			break
		}
		rest := out[start:]
		end := strings.Index(rest, inlineClose)
		if end < 0 {
			break
		}
		out = out[:start] + rest[end+len(inlineClose):]
	}
	return strings.TrimSpace(out)
}
