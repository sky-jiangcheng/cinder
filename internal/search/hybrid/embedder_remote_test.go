package hybrid

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRemoteEmbedderOrderingAndAuth(t *testing.T) {
	var gotAuth string
	var gotBody embeddingsRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		// Return the two vectors OUT OF ORDER to exercise Index-based reassembly.
		_, _ = w.Write([]byte(`{"data":[{"index":1,"embedding":[0.7,0.8]},{"index":0,"embedding":[0.1,0.2]}]}`))
	}))
	defer srv.Close()

	e := &RemoteEmbedder{BaseURL: srv.URL + "/v1", APIKey: "secret", Model: "test-embed", Dim: 2}
	out, err := e.Embed([]string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if gotBody.Model != "test-embed" || len(gotBody.Input) != 2 || gotBody.Input[0] != "alpha" {
		t.Errorf("request body = %+v", gotBody)
	}
	// Reassembled in input order despite out-of-order response.
	if len(out) != 2 || out[0][0] != 0.1 || out[1][0] != 0.7 {
		t.Fatalf("wrong order: %v", out)
	}
}

func TestRemoteEmbedderErrors(t *testing.T) {
	t.Run("http error status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"bad model"}}`))
		}))
		defer srv.Close()
		if _, err := (&RemoteEmbedder{BaseURL: srv.URL + "/v1", Model: "x"}).Embed([]string{"t"}); err == nil {
			t.Fatal("expected error on non-200")
		}
	})
	t.Run("dim mismatch", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[1,2,3]}]}`))
		}))
		defer srv.Close()
		_, err := (&RemoteEmbedder{BaseURL: srv.URL + "/v1", Model: "x", Dim: 2}).Embed([]string{"t"})
		if err == nil || !strings.Contains(err.Error(), "dim") {
			t.Fatalf("expected dim mismatch error, got %v", err)
		}
	})
	t.Run("unconfigured base url", func(t *testing.T) {
		if _, err := (&RemoteEmbedder{}).Embed([]string{"t"}); err == nil {
			t.Fatal("expected error when BaseURL empty")
		}
	})
	t.Run("empty input", func(t *testing.T) {
		out, err := (&RemoteEmbedder{BaseURL: "http://x/v1"}).Embed(nil)
		if err != nil || out != nil {
			t.Fatalf("empty input: out=%v err=%v", out, err)
		}
	})
	t.Run("truncated response body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "1000")
			_, _ = w.Write([]byte(`{"data":[`))
		}))
		defer srv.Close()
		if _, err := (&RemoteEmbedder{BaseURL: srv.URL + "/v1", Model: "x"}).Embed([]string{"t"}); err == nil {
			t.Fatal("a body cut mid-transfer must surface as an error, not a partial decode")
		}
	})
}

func TestRemoteEmbedderImplementsEmbedder(t *testing.T) {
	var _ Embedder = (*RemoteEmbedder)(nil)
}
