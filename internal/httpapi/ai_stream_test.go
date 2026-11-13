package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"repotory/internal/db"
	"repotory/internal/service"
)

// The streaming endpoint must not be exercised over a real listen socket in
// the sandbox (httptest loopback is unavailable), so these tests cover the
// paths that fail deterministically before any network call: method guard,
// parameter validation, and the not-configured error frame. SSE parsing itself
// is unit-tested as a pure function in internal/service/ai_stream_test.go.

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return New(service.New(database, "me"), nil, nil)
}

func TestAskStreamRejectsWrongMethod(t *testing.T) {
	h := newTestHandler(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/ai/ask-stream", nil)
	req.Host = "127.0.0.1:18765"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestAskStreamRejectsEmptyQuestion(t *testing.T) {
	h := newTestHandler(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/ai/ask-stream",
		strings.NewReader(`{"project_id":1,"question":"  "}`))
	req.Host = "127.0.0.1:18765"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestAskStreamRejectsBadJSON(t *testing.T) {
	h := newTestHandler(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/ai/ask-stream",
		strings.NewReader(`{"project_id":`)) // truncated JSON
	req.Host = "127.0.0.1:18765"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// Without a configured AI endpoint the ask fails before any network I/O, so
// this path is safe to run in the sandbox: one SSE error frame, 200 status
// (headers are already committed by the time the service error surfaces).
func TestAskStreamErrorFrameWhenNotConfigured(t *testing.T) {
	h := newTestHandler(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/ai/ask-stream",
		strings.NewReader(`{"project_id":1,"question":"what is this project?"}`))
	req.Host = "127.0.0.1:18765"
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (SSE headers were committed before service error)", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"error"`) {
		t.Fatalf("expected an error frame, got: %s", body)
	}
	if strings.Contains(body, `"delta"`) {
		t.Fatalf("error path must not emit delta frames: %s", body)
	}
}
