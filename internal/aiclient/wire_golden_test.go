package aiclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

type wireReq struct {
	Method string            `json:"method"`
	Path   string            `json:"path"`
	Header map[string]string `json:"header"`
	Body   string            `json:"body"`
}

// TestWireShapeMatchesGolden pins the bytes this package puts on the wire.
// The golden was captured from internal/service/ai.go BEFORE the extraction
// (ADR-0064), so a passing run is the proof that moving the code changed
// nothing a provider can observe: method, path, Authorization, Content-Type,
// and body — including the streaming request that differs only by stream:true.
func TestWireShapeMatchesGolden(t *testing.T) {
	raw, err := os.ReadFile("testdata/aiclient_golden.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var want []wireReq
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("decode golden: %v", err)
	}

	var got []wireReq
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		b, _ := io.ReadAll(r.Body)
		got = append(got, wireReq{
			Method: r.Method,
			Path:   r.URL.Path,
			Header: map[string]string{
				"authorization": r.Header.Get("Authorization"),
				"content-type":  r.Header.Get("Content-Type"),
				"accept":        r.Header.Get("Accept"),
			},
			Body: string(b),
		})
		if hits == 3 {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	ctx := context.Background()
	if _, _, err := Get(ctx, srv.URL+"/v1/models", "sk-test"); err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, _, err := PostJSON(ctx, srv.URL+"/v1/chat/completions", "sk-test",
		[]byte(`{"model":"m","messages":[{"role":"user","content":"q"}]}`)); err != nil {
		t.Fatalf("post: %v", err)
	}
	if _, _, err := PostJSONStream(ctx, srv.URL+"/v1/chat/completions", "sk-test",
		[]byte(`{"model":"m","messages":[],"stream":true}`), nil); err != nil {
		t.Fatalf("stream: %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("captured %d requests, golden has %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Method != want[i].Method || got[i].Path != want[i].Path {
			t.Errorf("req %d: %s %s, want %s %s", i, got[i].Method, got[i].Path, want[i].Method, want[i].Path)
		}
		if got[i].Body != want[i].Body {
			t.Errorf("req %d body drifted:\n got: %s\nwant: %s", i, got[i].Body, want[i].Body)
		}
		for k, v := range want[i].Header {
			if got[i].Header[k] != v {
				t.Errorf("req %d header %s = %q, want %q", i, k, got[i].Header[k], v)
			}
		}
	}
}
