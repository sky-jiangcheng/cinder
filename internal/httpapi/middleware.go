package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// requestIDKey is private so no other package can forge or read the value
// except through RequestIDFromContext.
type requestIDKey struct{}

// RequestIDFromContext returns the id withRequestLog attached to ctx.
func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey{}).(string); ok {
		return v
	}
	return ""
}

// withRequestLog adds a request id, one structured access-log line, and panic
// recovery. The id is echoed in X-Request-ID, put on the request context, and
// logged with method/path/status/duration. It wraps only trusted handlers: New
// keeps loopbackGuard outside it, so a request rejected at the trust boundary
// never reaches this middleware (and is never assigned a request id).
func withRequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set("X-Request-ID", id)
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id))

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		defer func() {
			if rv := recover(); rv != nil {
				slog.Error("http panic",
					slog.String("request_id", id),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Any("panic", rv),
					slog.String("stack", string(debug.Stack())),
				)
				rec.WriteHeader(http.StatusInternalServerError)
			}
			slog.Info("http request",
				slog.String("request_id", id),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			)
		}()
		next.ServeHTTP(rec, r)
	})
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "rid-unavailable"
	}
	return hex.EncodeToString(b[:])
}

// statusRecorder captures the response status for the access log. Flusher is
// delegated so streaming endpoints (SSE) still see a working http.Flusher.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.wroteHeader {
		return
	}
	s.status = code
	s.wroteHeader = true
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(p []byte) (int, error) {
	if !s.wroteHeader {
		s.WriteHeader(http.StatusOK)
	}
	return s.ResponseWriter.Write(p)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
