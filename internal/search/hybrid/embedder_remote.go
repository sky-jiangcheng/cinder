package hybrid

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// RemoteEmbedder implements Embedder against any OpenAI-compatible
// /v1/embeddings server — the de-facto "standard protocol" shared by OpenAI,
// Voyage, Jina, and self-hosted servers (Ollama, llama.cpp server, LM Studio).
// Point BaseURL + Model at whichever you choose; APIKey may be empty for local
// servers that need no auth.
//
// This is the M3-A groundwork (ADR-0012): it is deliberately NOT wired into the
// search path yet. A ships only behind an explicit, default-OFF enablement plus
// the A/B quality gate, because it sends note text to an external endpoint (a
// privacy/risk decision documented in ADR-0012 决策落地). Building it as a
// pluggable Embedder keeps the vendor choice open and lets the search wiring
// stay independent of which endpoint a user picks.
type RemoteEmbedder struct {
	BaseURL string // e.g. "https://api.openai.com/v1" or "http://localhost:11434/v1"
	APIKey  string // optional bearer token; empty for local servers
	Model   string
	// Dim is the expected vector length; 0 means "trust the server's output".
	Dim int
	// HTTP client; if nil a 120s-timeout default is used. Remote endpoints can
	// spend ~15s of fixed overhead per request before streaming a multi-MB
	// vector payload; 30s and even 60s proved too tight in practice.
	HTTP *http.Client
}

// Dimension implements Embedder.
func (e *RemoteEmbedder) Dimension() int { return e.Dim }

type embeddingsRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingsResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Embed POSTs the texts and returns one vector per input, in input order.
func (e *RemoteEmbedder) Embed(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(e.BaseURL) == "" {
		return nil, fmt.Errorf("hybrid: RemoteEmbedder BaseURL not configured")
	}
	body, err := json.Marshal(embeddingsRequest{Model: e.Model, Input: texts})
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(e.BaseURL, "/") + "/embeddings"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.APIKey)
	}
	client := e.HTTP
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hybrid: embed request: %w", err)
	}
	defer resp.Body.Close()
	const respCap = 8 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, respCap+1))
	if err != nil {
		return nil, fmt.Errorf("hybrid: read embed response: %w", err)
	}
	if len(raw) > respCap {
		return nil, fmt.Errorf("hybrid: embed response exceeds %d MiB cap; reduce the batch size", respCap>>20)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hybrid: embed endpoint %s: %s", resp.Status, firstLine(string(raw)))
	}
	var er embeddingsResponse
	if err := json.Unmarshal(raw, &er); err != nil {
		return nil, fmt.Errorf("hybrid: decode embeddings: %w", err)
	}
	if er.Error != nil && er.Error.Message != "" {
		return nil, fmt.Errorf("hybrid: embed api error: %s", er.Error.Message)
	}
	if len(er.Data) != len(texts) {
		return nil, fmt.Errorf("hybrid: got %d embeddings for %d inputs", len(er.Data), len(texts))
	}
	// Responses may be unordered; index them by the server's Index field.
	out := make([][]float32, len(texts))
	for _, d := range er.Data {
		if d.Index >= 0 && d.Index < len(texts) {
			out[d.Index] = d.Embedding
		}
	}
	for i, v := range out {
		if v == nil {
			return nil, fmt.Errorf("hybrid: missing embedding for input %d", i)
		}
		if e.Dim > 0 && len(v) != e.Dim {
			return nil, fmt.Errorf("hybrid: input %d: dim %d, expected %d", i, len(v), e.Dim)
		}
	}
	return out, nil
}

// firstLine truncates an error body to one line for safe log surfaces.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
