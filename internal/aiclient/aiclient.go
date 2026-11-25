// Package aiclient is the HTTP/SSE transport for OpenAI-compatible endpoints:
// URL candidate probing, proxy selection, request posting, stream decoding, and
// the provider error envelope. It knows nothing about Service, the audit trail,
// or the tool loop — those stay in internal/service (ADR-0064).
package aiclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type Message struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	// ToolCalls rides on the assistant echo of a tool-calling turn. The
	// protocol pairs every tool message with the PRECEDING assistant
	// message's tool_calls; omitting them there makes strict providers
	// reject the whole follow-up request.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type Request struct {
	Model    string           `json:"model"`
	Messages []Message        `json:"messages"`
	Tools    []ToolDefinition `json:"tools,omitempty"`
}

// ToolDefinition is the OpenAI-compatible "tools" array entry. Parameters is
// left as any because each tool ships its own JSON schema.
type ToolDefinition struct {
	Type     string `json:"type"`
	Function struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Parameters  any    `json:"parameters"`
	} `json:"function"`
}

// ToolCallDelta is one tool_call fragment inside a streaming delta. Index
// pairs the fragments of the same call; ID and name normally arrive on the
// first fragment only.
type ToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type Response struct {
	Choices []struct {
		Message struct {
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		// finish_reason is the server's own verdict on the reply: "stop" (it
		// finished on its own), "length" (the output budget ran out mid-reply),
		// "content_filter", etc. It is carried through rather than parsed
		// because a truncated JSON array and a malformed one are indistinguishable
		// once you only have the text — and they call for opposite user actions.
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	// Error is the OpenAI-compatible error envelope. Gateways such as
	// OpenRouter answer HTTP 200 with {"error":{...}} and NO choices when the
	// upstream provider is saturated, so without this field the real cause
	// ("provider_unavailable — retry later") is discarded and the user is left
	// with a bare "no choices".
	Error *ErrorEnvelope `json:"error"`
}

// ErrorEnvelope is the error object some providers return in a 200 body.
type ErrorEnvelope struct {
	Message  string `json:"message"`
	Metadata struct {
		// ErrorType classifies the failure ("provider_unavailable",
		// "rate_limit_exceeded", …) and is the part worth acting on.
		ErrorType string `json:"error_type"`
	} `json:"metadata"`
}

// StreamRequest is Request plus stream:true. Other than the flag and the
// optional tools array, the body keeps the non-streaming shape — anything else
// would risk an endpoint applying different prompt handling to the two modes.
// A tool-enabled stream MUST use this type: a plain Request has no stream flag,
// and an endpoint answering a tool loop with JSON instead of SSE leaves the
// UI spinning on an empty reply (the 2026-11-19 "commit count hangs" bug).
type StreamRequest struct {
	Model    string           `json:"model"`
	Messages []Message        `json:"messages"`
	Tools    []ToolDefinition `json:"tools,omitempty"`
	Stream   bool             `json:"stream"`
}

// StreamDelta is one SSE data: payload of a streaming /chat/completions
// response. Delta content arrives piecemeal; finish_reason is normally present
// only on the final chunk (it is a pointer so absent can be told from "stop").
type StreamDelta struct {
	Choices []struct {
		Delta struct {
			Content   string          `json:"content"`
			ToolCalls []ToolCallDelta `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// FinishReasons is the server's verdict on the completed stream: "stop",
// "length" (output budget exhausted mid-reply), "content_filter", ...; ""
// when the stream ended without one. Purposely named in the plural: SSE
// reports it once per chunk, and the last non-empty value wins.
type FinishReasons string

// ModelListResult is the payload of ListModels: the advertised model ids
// plus the base URL that actually worked — it can differ from the typed one
// when a near-miss path variant (see CandidateURLs) answered instead.
type ModelListResult struct {
	Models          []string `json:"models"`
	ResolvedBaseURL string   `json:"resolved_base_url"`
}

// TestResult is the payload of TestChat.
type TestResult struct {
	Reply           string `json:"reply"`
	ResolvedBaseURL string `json:"resolved_base_url"`
	// FinishReason is the server's verdict on the reply, "" when it completed
	// normally. Callers that parse structured output need this to tell a
	// truncated reply from a malformed one; see truncatedHint.
	FinishReason string `json:"finish_reason,omitempty"`
}

// FinishReason reports whether the model stopped on its own. Anything other
// than the OpenAI-compatible "stop" (notably "length", i.e. truncated by the
// output token budget) means the content we got back is a prefix, not a
// finished reply. Empty is treated as "stop" because plenty of servers omit
// the field on success and a missing reason is not evidence of truncation.
func FinishReason(cr Response) string {
	if len(cr.Choices) == 0 {
		return ""
	}
	return strings.TrimSpace(cr.Choices[0].FinishReason)
}

// CandidateURLs builds the /models or /chat/completions URLs to probe in
// order. Users commonly mistype LM Studio's base as ".../api/v1" when the
// OpenAI layer lives at ".../v1", so near-miss variants are probed too and
// the working one is reported back instead of surfacing a dead 404.
func CandidateURLs(base, path string) []string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	urls := []string{base + path}
	if !strings.HasSuffix(base, "/v1") {
		urls = append(urls, base+"/v1"+path)
	}
	if strings.HasSuffix(base, "/api/v1") {
		urls = append(urls, strings.TrimSuffix(base, "/api/v1")+"/v1"+path)
	}
	if strings.HasSuffix(base, "/api") {
		urls = append(urls, strings.TrimSuffix(base, "/api")+path)
	}
	seen := make(map[string]bool, len(urls))
	out := urls[:0]
	for _, u := range urls {
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

// PathRejected answers "is this response a wrong-path answer worth another
// candidate URL". A 404 always is — the classic wrong-shape probe. A 400
// whose body is the relay-gateway rejection ("Request path cannot be
// relayed" — CF-Worker-style proxies that only forward /v1/*) is the same
// dead end: the path itself is unsupported, so the sibling candidate may be
// the right one. Any other 400 is about the REQUEST (bad model, bad body)
// and must surface to the user unchanged — retrying it against a made-up
// sibling would only mask the real error.
func PathRejected(status int, body string) bool {
	if status == http.StatusNotFound {
		return true
	}
	return status == http.StatusBadRequest &&
		strings.Contains(strings.ToLower(body), "request path cannot be relayed")
}

// NextCandidate answers "is this 404 a URL-shape problem worth another try".
//
// It is shared by every path that walks CandidateURLs so the rule lives in
// one place: only a rejected path (404, or the relay-gateway 400 — see
// PathRejected) moves to the next candidate, while an auth failure or a 5xx
// is a real answer about the endpoint and must not be retried against a
// made-up sibling URL.
func NextCandidate(candidates []string, current string, status int, body string) (string, bool) {
	if !PathRejected(status, body) {
		return "", false
	}
	for i, c := range candidates {
		if c == current && i+1 < len(candidates) {
			return candidates[i+1], true
		}
	}
	return "", false
}

// LocalhostFallback rewrites a "localhost" URL to its 127.0.0.1 equivalent.
// The resolver prefers ::1 and a server listening on IPv4 only reported
// "dial tcp [::1]:... connection refused" even though the service was up.
//
// The host is swapped through net/url rather than a string Replace: a path or
// query that happens to contain "localhost" must not be rewritten, and a
// hand-typed "127.0.0.1:1234" must not turn into the unparseable
// "127.0.0.1:127.0.0.1:1234". Returns "" when no swap applies.
func LocalhostFallback(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(u.Hostname(), "localhost") {
		return ""
	}
	// A bracketed IPv6 literal must keep its brackets in Host; Go's URL.Host
	// does not add them for us.
	if port := u.Port(); port != "" {
		u.Host = "127.0.0.1:" + port
	} else {
		u.Host = "127.0.0.1"
	}
	return u.String()
}

// ProxyFunc is the Transport's per-request proxy hook. It delegates to
// ProxyFuncWith using the environment resolver (ProxyFromEnvironment), which
// keeps NO_PROXY support and honors any non-loopback upstream proxy.
func ProxyFunc(req *http.Request) (*url.URL, error) {
	return ProxyFuncWith(req, http.ProxyFromEnvironment)
}

// ProxyFuncWith decides per request whether to use a proxy.
//
//   - A loopback target host (localhost / 127.0.0.1 / ::1) is always direct:
//     local model servers never need an external proxy, and a dead local proxy
//     must not be able to block them.
//   - For a remote target, defer to the env resolver (so NO_PROXY is still
//     honored). But if the resolved proxy is itself loopback, skip it and go
//     direct — a local proxy that is down should not stall a remote call that
//     works without it.
//
// The resolver is a parameter only so the decision can be unit-tested without
// fighting the process environment; production uses http.ProxyFromEnvironment.
func ProxyFuncWith(req *http.Request, envProxy func(*http.Request) (*url.URL, error)) (*url.URL, error) {
	if IsLoopbackHost(req.URL.Hostname()) {
		return nil, nil
	}
	proxy, err := envProxy(req)
	if err != nil {
		return nil, err
	}
	if proxy != nil && IsLoopbackHost(proxy.Hostname()) {
		return nil, nil
	}
	return proxy, nil
}

// IsLoopbackHost reports whether host is the loopback interface. The input may
// already be port-stripped (url.URL.Hostname), but a raw host:port is also
// accepted.
func IsLoopbackHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Get/aiPost issue one request, retrying once against 127.0.0.1 when the
// host was "localhost" and the first attempt failed at the transport level.
func Get(ctx context.Context, rawURL, apiKey string) (int, []byte, error) {
	try := func(u string) (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return 0, nil, err
		}
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		return resp.StatusCode, raw, nil
	}
	status, raw, err := try(rawURL)
	if err != nil {
		if fallback := LocalhostFallback(rawURL); fallback != "" {
			status, raw, err = try(fallback)
		}
	}
	return status, raw, err
}

func PostJSON(ctx context.Context, rawURL, apiKey string, body []byte) (int, []byte, error) {
	try := func(u string) (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		return resp.StatusCode, raw, nil
	}
	status, raw, err := try(rawURL)
	if err != nil {
		if fallback := LocalhostFallback(rawURL); fallback != "" {
			status, raw, err = try(fallback)
		}
	}
	return status, raw, err
}

// PostJSONStream is PostJSON for stream:true: same transport (candidate
// URL handled by the caller, localhost fallback inside), but the response
// body is decoded as an SSE stream on the fly instead of buffered whole.
// Each content delta is handed to onDelta as it arrives; the returned
// FinishReasons is the server's own verdict on the reply (see ChatGPT's
// StreamDelta for why it lands on the last chunk). A non-2xx response is
// read into raw and returned verbatim so the caller can classify it (404 →
// next candidate, auth → fail fast, etc.) exactly like the batch path.
func PostJSONStream(ctx context.Context, rawURL, apiKey string, body []byte, onDelta func(string)) (int, FinishReasons, error) {
	try := func(u string) (int, FinishReasons, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
		if err != nil {
			return 0, "", err
		}
		req.Header.Set("Content-Type", "application/json")
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return 0, "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
			return resp.StatusCode, "", fmt.Errorf("AI endpoint returned %d: %s", resp.StatusCode, clip(string(raw), 300))
		}
		reason, err := ParseSSEStream(resp.Body, onDelta)
		return http.StatusOK, reason, err
	}
	status, reason, err := try(rawURL)
	if err != nil && status == 0 { // transport-level failure only; a 404/401 body is a verdict, not a retry
		if fallback := LocalhostFallback(rawURL); fallback != "" {
			status, reason, err = try(fallback)
		}
	}
	return status, reason, err
}

// ParseSSEStream consumes an OpenAI-compatible SSE response body. Every
// content delta is delivered to onDelta as it is decoded; the first error
// payload (an OpenAI endpoint can fold a failure into the 200 stream) is
// reported, and "<-[DONE]->" ends the stream early.
//
// Kept as a pure function (io.Reader in, no network) so the streaming
// parser can be unit-tested with a strings.Reader — the httptest loopback
// is unavailable in the sandbox, and the alternative (only exercising
// streaming via end-to-end tests) would leave the chunking logic untested.
// parseSSE is the chunk-level core: it decodes each data: payload, tracks the
// last non-empty finish_reason, and hands the whole delta to onChunk. Content
// extraction lives in the wrappers so the tool-enabled stream can read the
// same payload without a second scanner.
func parseSSE(r io.Reader, onChunk func(StreamDelta)) (FinishReasons, error) {
	sc := bufio.NewScanner(r)
	// SSE chunks are normally small; raise the cap so an unusually chatty
	// server cannot be misread as a truncated stream.
	sc.Buffer(make([]byte, 0, 16*1024), 4<<20)

	var reason FinishReasons
	var streamErr error
	sawFrame := false
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue // ":comment" / "event:" / blank separator lines
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		sawFrame = true
		if payload == "[DONE]" {
			break
		}
		var delta StreamDelta
		if err := json.Unmarshal([]byte(payload), &delta); err != nil {
			// A malformed chunk is a stream failure: without the full text
			// the caller cannot tell truncation from corruption, so report
			// it rather than silently ending with a half answer.
			return reason, fmt.Errorf("malformed SSE chunk: %w", err)
		}
		if delta.Error != nil && delta.Error.Message != "" {
			streamErr = fmt.Errorf("%s", delta.Error.Message)
			continue
		}
		for _, c := range delta.Choices {
			if c.FinishReason != nil && *c.FinishReason != "" {
				reason = FinishReasons(*c.FinishReason)
			}
		}
		onChunk(delta)
	}
	if err := sc.Err(); err != nil {
		return reason, err
	}
	if streamErr != nil {
		return reason, streamErr
	}
	// A 200 with a non-SSE body (typically a single JSON completion, which
	// happens when the request omitted stream:true) used to slip through as
	// an empty, successful reply — indistinguishable from "model said
	// nothing" and rendered as an endless typing indicator. Fail loudly.
	if !sawFrame {
		return reason, fmt.Errorf("endpoint returned no SSE data frames (expected stream:true in the request)")
	}
	return reason, nil
}

// ParseSSEStream assembles the content deltas of an OpenAI-compatible SSE
// stream and reports the last non-empty finish_reason.
func ParseSSEStream(r io.Reader, onDelta func(string)) (FinishReasons, error) {
	if onDelta == nil {
		onDelta = func(string) {}
	}
	return parseSSE(r, func(delta StreamDelta) {
		for _, c := range delta.Choices {
			if c.Delta.Content != "" {
				onDelta(c.Delta.Content)
			}
		}
	})
}

// ParseSSEToolStream is ParseSSEStream plus tool-call fragments: a tool-enabled
// stream arrives as content deltas and tool_call deltas interleaved, often
// with the arguments of one call split across many chunks.
func ParseSSEToolStream(r io.Reader, onDelta func(string), onTool func(ToolCallDelta)) (FinishReasons, error) {
	return parseSSE(r, func(delta StreamDelta) {
		for _, c := range delta.Choices {
			if c.Delta.Content != "" && onDelta != nil {
				onDelta(c.Delta.Content)
			}
			if onTool != nil {
				for _, tc := range c.Delta.ToolCalls {
					onTool(tc)
				}
			}
		}
	})
}

// NetworkHint turns a raw transport error into an actionable hint.
func NetworkHint(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return fmt.Errorf("nothing is listening on that host:port — is the server running, and is the port right? (local servers may listen on 127.0.0.1 only)")
	case strings.Contains(msg, "context deadline exceeded") || strings.Contains(msg, "Client.Timeout"):
		return fmt.Errorf("connection timed out")
	case strings.Contains(msg, "no such host"):
		return fmt.Errorf("host not found — check the address")
	}
	return fmt.Errorf("AI endpoint unreachable: %w", err)
}

// ParseModels accepts the OpenAI shape ({"data":[{"id"}]}) and falls back
// to Ollama's native ({"models":[{"name"}]}).
func ParseModels(raw []byte) ([]string, bool) {
	var openai struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &openai); err == nil && len(openai.Data) > 0 {
		ids := make([]string, 0, len(openai.Data))
		for _, m := range openai.Data {
			if m.ID != "" {
				ids = append(ids, m.ID)
			}
		}
		if len(ids) > 0 {
			sort.Strings(ids)
			return ids, true
		}
	}
	var ollama struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &ollama); err == nil && len(ollama.Models) > 0 {
		ids := make([]string, 0, len(ollama.Models))
		for _, m := range ollama.Models {
			if m.Name != "" {
				ids = append(ids, m.Name)
			}
		}
		if len(ids) > 0 {
			sort.Strings(ids)
			return ids, true
		}
	}
	return nil, false
}

// PostJSONToolStream is PostJSONStream for tool-enabled turns: content deltas
// and tool_call fragments both flow out. It keeps the older tool-loop retry
// rule — ANY error retries the localhost fallback, including a 404 verdict —
// because that is what the loop did before the transport lived here; changing
// it is a behaviour decision, not part of the move.
func PostJSONToolStream(ctx context.Context, rawURL, apiKey string, body []byte, onDelta func(string), onTool func(ToolCallDelta)) (int, FinishReasons, error) {
	try := func(u string) (int, FinishReasons, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
		if err != nil {
			return 0, "", err
		}
		req.Header.Set("Content-Type", "application/json")
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return 0, "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
			return resp.StatusCode, "", fmt.Errorf("AI endpoint returned %d: %s", resp.StatusCode, clip(string(raw), 300))
		}
		reason, err := ParseSSEToolStream(resp.Body, onDelta, onTool)
		return resp.StatusCode, reason, err
	}
	status, reason, err := try(rawURL)
	if err != nil {
		if fallback := LocalhostFallback(rawURL); fallback != "" {
			status, reason, err = try(fallback)
		}
	}
	return status, reason, err
}

// ListModels probes the endpoint's /models (with near-miss path candidates
// and the localhost fallback) using the form's current values — no save
// required. ResolvedBaseURL is the base that actually worked so the UI can
// offer to adopt it.
func ListModels(baseURL, apiKey string) (*ModelListResult, error) {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		return nil, fmt.Errorf("base URL is required")
	}
	candidates := CandidateURLs(base, "/models")
	ctx, cancel := context.WithTimeout(context.Background(), ProbeTimeout)
	defer cancel()

	var lastNet error
	var lastStatus int
	var lastBody string
	for _, url := range candidates {
		status, raw, err := Get(ctx, url, strings.TrimSpace(apiKey))
		if err != nil {
			lastNet = err
			continue
		}
		if PathRejected(status, string(raw)) {
			lastStatus, lastBody = status, string(raw)
			continue
		}
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			return nil, fmt.Errorf("auth failed (%d) — check the API key", status)
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("AI endpoint returned %d: %s", status, clip(string(raw), 200))
		}
		models, ok := ParseModels(raw)
		if !ok {
			return nil, fmt.Errorf("endpoint returned no models")
		}
		return &ModelListResult{
			Models:          models,
			ResolvedBaseURL: strings.TrimSuffix(url, "/models"),
		}, nil
	}
	if lastNet != nil {
		return nil, NetworkHint(lastNet)
	}
	return nil, fmt.Errorf("no /models path found — tried: %s (last status %d: %s)", strings.Join(candidates, ", "), lastStatus, clip(lastBody, 120))
}

// TestChat verifies a specific model works by sending a minimal completion.
// Takes the raw form values so untested settings can be validated before save.
func TestChat(baseURL, model, apiKey string) (*TestResult, error) {
	return TestChatWith(baseURL, model, apiKey, []Message{{Role: "user", Content: "Reply with exactly: OK"}})
}

// TestChatWith is TestChat with explicit messages (AskAI reuses it so a
// saved-but-imprecise base URL gets the same candidate probing).
func TestChatWith(baseURL, model, apiKey string, messages []Message) (*TestResult, error) {
	return ProbeChatWithTimeout(baseURL, model, apiKey, messages, ChatTimeout)
}

// TestChatWithTimeout is the batch-path variant: same contract, longer ceiling.
func TestChatWithTimeout(baseURL, model, apiKey string, messages []Message, timeout time.Duration) (*TestResult, error) {
	return ProbeChatWithTimeout(baseURL, model, apiKey, messages, timeout)
}

func ProbeChatWithTimeout(baseURL, model, apiKey string, messages []Message, timeout time.Duration) (*TestResult, error) {
	base := strings.TrimSpace(baseURL)
	if base == "" || strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("base URL and model are required")
	}
	candidates := CandidateURLs(base, "/chat/completions")
	body, err := json.Marshal(Request{Model: strings.TrimSpace(model), Messages: messages})
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = ChatTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var lastNet error
	var lastStatus int
	var lastBody string
	for _, url := range candidates {
		status, raw, err := PostJSON(ctx, url, strings.TrimSpace(apiKey), body)
		if err != nil {
			lastNet = err
			continue
		}
		if PathRejected(status, string(raw)) {
			lastStatus, lastBody = status, string(raw)
			continue
		}
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			return nil, fmt.Errorf("auth failed (%d) — check the API key", status)
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("AI endpoint returned %d: %s", status, clip(string(raw), 300))
		}
		var cr Response
		if err := json.Unmarshal(raw, &cr); err != nil {
			return nil, fmt.Errorf("AI endpoint returned malformed JSON: %w", err)
		}
		if len(cr.Choices) == 0 {
			// A 200 body can still carry an error envelope instead of a
			// completion (OpenRouter does this when the upstream provider is
			// saturated). Report the provider's own words — "no choices" alone
			// tells the user nothing about what to do next.
			if perr := cr.ProviderError(); perr != nil {
				return nil, perr
			}
			return nil, fmt.Errorf("AI endpoint returned no choices")
		}
		return &TestResult{
			Reply:           strings.TrimSpace(cr.Choices[0].Message.Content),
			ResolvedBaseURL: strings.TrimSuffix(url, "/chat/completions"),
			FinishReason:    FinishReason(cr),
		}, nil
	}
	if lastNet != nil {
		return nil, NetworkHint(lastNet)
	}
	return nil, fmt.Errorf("no working path found — tried: %s (last status %d: %s)", strings.Join(candidates, ", "), lastStatus, clip(lastBody, 120))
}

// StreamChatWithTimeout is ProbeChatWithTimeout with stream:true: it probes the
// candidate URLs in order (same 404-continue / auth-fail-fast / last-network-
// error hint policy), streams each content delta to onDelta as it arrives, and
// returns the fully received reply plus the server's finish_reason. The reply
// aggregate stays in one place so both the streaming caller (which forwards
// deltas) and the plain caller (which may ignore them) can rely on it.
func StreamChatWithTimeout(baseURL, model, apiKey string, messages []Message, timeout time.Duration, onDelta func(string)) (string, FinishReasons, error) {
	base := strings.TrimSpace(baseURL)
	if base == "" || strings.TrimSpace(model) == "" {
		return "", "", fmt.Errorf("base URL and model are required")
	}
	body, err := json.Marshal(StreamRequest{Model: strings.TrimSpace(model), Messages: messages, Stream: true})
	if err != nil {
		return "", "", err
	}
	if timeout <= 0 {
		timeout = ChatTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var b strings.Builder
	delta := func(d string) {
		b.WriteString(d)
		if onDelta != nil {
			onDelta(d)
		}
	}

	var lastNet error
	var lastStatus int
	var lastBody string
	for _, url := range CandidateURLs(base, "/chat/completions") {
		status, reason, err := PostJSONStream(ctx, url, strings.TrimSpace(apiKey), body, delta)
		if err != nil {
			if PathRejected(status, err.Error()) {
				lastStatus, lastBody = status, err.Error()
				continue
			}
			if status == http.StatusUnauthorized || status == http.StatusForbidden {
				return b.String(), "", fmt.Errorf("auth failed (%d) — check the API key", status)
			}
			if status == 0 {
				lastNet = err
				continue
			}
			return b.String(), "", err
		}
		// Streaming worked even when the last finish_reason chunk never
		// arrived; missing reason is not evidence of truncation (see
		// truncatedHint), so success wins here.
		return strings.TrimSpace(b.String()), reason, nil
	}
	if lastNet != nil {
		return b.String(), "", NetworkHint(lastNet)
	}
	return b.String(), "", fmt.Errorf("no working path found — tried: %s (last status %d: %s)", strings.Join(CandidateURLs(base, "/chat/completions"), ", "), lastStatus, clip(lastBody, 120))
}

// ChatTimeout bounds the PLAIN chat path (a short question, no retrieval): a
// user staring at a chat box should get an error, not a hung panel.
//
// It used to bound every path, which was wrong in the other direction — a local
// 27B model needs well over 90s for one compile request, so ingest-time
// compilation could never succeed no matter what the user configured. Discovered
// by running the real rehearsal rather than by reading the code. The batch paths
// moved to DefaultBatchChatTimeout and evidence-backed Q&A to aiAskTimeout; this
// ceiling now covers only what it was written for.
const ChatTimeout = 90 * time.Second

const ProbeTimeout = 15 * time.Second

// httpClient is the dedicated transport for AI calls. It deliberately does
// not use http.DefaultClient: DefaultClient honors HTTP(S)_PROXY from the
// environment, which on a developer box is often a local proxy (ClashVerge,
// 127.0.0.1:PORT). When that proxy is down, a remotely-reachable endpoint such
// as OpenRouter becomes unreachable with "nothing is listening on that
// host:port" — that is the proxy's own dead port, not the AI host. ProxyFunc
// keeps the useful parts of ProxyFromEnvironment (it still honors NO_PROXY and
// any non-loopback upstream proxy) while never routing through a loopback
// proxy, and never sending a local AI server (Ollama/LM Studio) out to an
// external proxy at all.
var httpClient = &http.Client{
	Transport: &http.Transport{
		Proxy: ProxyFunc,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1500 * time.Millisecond,
	},
}

// ToolCall is one tool invocation the model asked for.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ProviderError renders the provider's own error envelope as a Go error, or
// nil when the body carried no error object.
//
// The message is passed through verbatim (clipped) because it names the actual
// cause — "Worker local total request limit reached (16/16)" — while the
// surrounding error_type says whether waiting helps.
func (cr Response) ProviderError() error {
	if cr.Error == nil {
		return nil
	}
	msg := strings.TrimSpace(cr.Error.Message)
	kind := strings.TrimSpace(cr.Error.Metadata.ErrorType)
	switch {
	case msg != "" && kind != "":
		return fmt.Errorf("AI endpoint error (%s): %s", kind, clip(msg, 300))
	case msg != "":
		return fmt.Errorf("AI endpoint error: %s", clip(msg, 300))
	case kind != "":
		return fmt.Errorf("AI endpoint error (%s)", kind)
	default:
		return fmt.Errorf("AI endpoint returned an error")
	}
}

// clip shortens an error body for a message a user will read; the tail of a
// 4xx payload is rarely the part that identifies the cause.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
