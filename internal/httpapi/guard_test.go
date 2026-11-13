package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"repotory/internal/db"
	"repotory/internal/service"
)

// newLocalRequest is httptest.NewRequest with the Host a real local client
// sends. httptest.NewRequest defaults to Host "example.com", which the
// loopback guard must reject — so tests that want to reach the handlers have
// to pin the host explicitly.
func newLocalRequest(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.Host = "127.0.0.1:18765"
	return req
}

// newGuardTestHandler builds the full handler (guard included) over an
// in-memory database.
func newGuardTestHandler(t *testing.T) http.Handler {
	t.Helper()
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return New(service.New(database, "me"), nil, nil)
}

func TestLoopbackGuardRejectsForeignHost(t *testing.T) {
	h := newGuardTestHandler(t)
	for _, host := range []string{
		"evil.com",
		"evil.com:18765",     // DNS rebinding: browser sends the page's domain
		"localhost.evil.com", // not the reserved "localhost"
		"127.0.0.1.evil.com",
		"", // HTTP/1.0 without Host
	} {
		rec := httptest.NewRecorder()
		req := newLocalRequest(http.MethodGet, "/health")
		req.Host = host
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("host %q: status = %d, want 403", host, rec.Code)
		}
	}
}

func TestLoopbackGuardAcceptsLocalHosts(t *testing.T) {
	h := newGuardTestHandler(t)
	for _, host := range []string{
		"127.0.0.1:18765",
		"localhost:18765",
		"127.0.0.1",
		"localhost",
		"[::1]:18765",
		"::1",
	} {
		rec := httptest.NewRecorder()
		req := newLocalRequest(http.MethodGet, "/health")
		req.Host = host
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("host %q: status = %d, want 200", host, rec.Code)
		}
	}
}

func TestLoopbackGuardRejectsCrossOrigin(t *testing.T) {
	h := newGuardTestHandler(t)

	rec := httptest.NewRecorder()
	req := newLocalRequest(http.MethodGet, "/health")
	req.Header.Set("Origin", "https://evil.com")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("foreign Origin: status = %d, want 403", rec.Code)
	}

	// A loopback Origin (e.g. a future local web UI on a dev port) is fine.
	rec = httptest.NewRecorder()
	req = newLocalRequest(http.MethodGet, "/health")
	req.Header.Set("Origin", "http://localhost:5173")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("loopback Origin: status = %d, want 200", rec.Code)
	}
}

func TestLoopbackGuardPassesErrorsThrough(t *testing.T) {
	// The guard must not swallow handler responses: a valid host keeps
	// handler status codes (400 for missing query) untouched.
	h := newGuardTestHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newLocalRequest(http.MethodGet, "/api/search"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing query: status = %d, want 400", rec.Code)
	}
}
