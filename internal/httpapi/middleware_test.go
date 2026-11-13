package httpapi

import (
	"bytes"
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"repotory/internal/db"
	"repotory/internal/service"
)

func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	oldDefault := slog.Default()
	oldWriter := log.Writer()
	oldFlags := log.Flags()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(oldDefault)
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
	})
	return buf
}

func TestWithRequestLogAssignsRequestID(t *testing.T) {
	captureSlog(t)
	h := withRequestLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if RequestIDFromContext(r.Context()) == "" {
			t.Error("request id missing from context")
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if got := rec.Header().Get("X-Request-ID"); got == "" {
		t.Fatal("X-Request-ID header missing")
	}
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", rec.Code)
	}
}

func TestWithRequestLogRecordsAccessLine(t *testing.T) {
	buf := captureSlog(t)
	h := withRequestLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/thing", nil))

	var line map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &line); err != nil {
		t.Fatalf("access log is not a single JSON line: %v (%q)", err, buf.String())
	}
	if line["method"] != http.MethodPost || line["path"] != "/api/thing" {
		t.Errorf("wrong method/path: %v", line)
	}
	if status, _ := line["status"].(float64); int(status) != http.StatusTeapot {
		t.Errorf("status = %v, want 418", line["status"])
	}
	if _, ok := line["duration_ms"]; !ok {
		t.Error("duration_ms missing")
	}
	if line["request_id"] == nil || line["request_id"] == "" {
		t.Error("request_id missing from log")
	}
}

func TestWithRequestLogRecoversPanic(t *testing.T) {
	buf := captureSlog(t)
	h := withRequestLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("kaboom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil)) // must not propagate
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	out := buf.String()
	if !strings.Contains(out, "kaboom") || !strings.Contains(out, "stack") {
		t.Errorf("panic log missing value/stack: %s", out)
	}
}

// The SSE endpoint asserts w.(http.Flusher); withRequestLog must not hide it.
func TestWithRequestLogPreservesFlusher(t *testing.T) {
	captureSlog(t)
	h := withRequestLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Flusher); !ok {
			t.Error("http.Flusher lost through withRequestLog")
		}
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/sse", nil))
}

// loopbackGuard stays outermost: a request rejected at the trust boundary gets
// no request id, while a trusted loopback request does.
func TestLoopbackGuardStaysOutsideRequestLog(t *testing.T) {
	captureSlog(t)
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	h := New(service.New(database, "me"), nil, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Host = "evil.com"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if got := rec.Header().Get("X-Request-ID"); got != "" {
		t.Errorf("rejected request got a request id %q: guard must stay outermost", got)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Host = "127.0.0.1:18765"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("X-Request-ID") == "" {
		t.Errorf("trusted request must get 200 and an id, got %d %q", rec.Code, rec.Header().Get("X-Request-ID"))
	}
}
