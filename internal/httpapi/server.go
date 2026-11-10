// Package httpapi exposes Cinder's shared business logic (internal/service)
// over a small HTTP/JSON surface so that external runtimes — most importantly
// the DeepSeek Harness dsh-plugin — can call the same analysis code the
// desktop App uses, with zero logic duplication.
//
// The desktop Wails App, the (future) CLI and this headless server are all thin
// adapters over internal/service. They share one implementation and one SQLite
// database.
//
// Trust boundary: this API has NO authentication — it serves the complete
// local knowledge base (notes, todos, mined repo knowledge). It is safe only
// because cmd/server binds it to 127.0.0.1, so reachability is equivalent to
// "another process on this machine". The loopbackGuard middleware keeps that
// property honest against the one vector a bind address cannot stop — a
// browser reaching 127.0.0.1 via DNS rebinding — by rejecting any request
// whose Host or Origin does not name the local machine. Anyone embedding this
// handler (tests, plugins, future transports) must preserve that property:
// loopback-only, never 0.0.0.0, and never documented as a network service.
// Exposing it beyond loopback would publish the user's private code knowledge
// to the network.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"repotory/internal/bounds"
	"repotory/internal/service"
)

// New returns an http.Handler (ServeMux) that serves Cinder's capabilities
// as JSON endpoints. The supplied service must already be constructed with a
// valid database; callers do not need to invoke service.Startup for read-only
// endpoints. `bound` is the same object the desktop Wails App binds (its
// exported methods are reachable via /api/rpc for the browser/standalone
// frontend); pass nil to disable RPC. The handler is wrapped in loopbackGuard —
// see the package trust boundary comment.
func New(svc *service.Service, bound any) http.Handler {
	mux := http.NewServeMux()
	h := &handler{svc: svc, bound: reflect.ValueOf(bound)}

	// NOTE: every pattern here must be claimed by cmd/server's isAPIPath, or a
	// --web-dist run swallows it into the SPA index.html fallback. A test in
	// cmd/server parses this file and pins that invariant.
	mux.HandleFunc("/health", h.health)
	mux.HandleFunc("/api/ai_context", h.aiContext)
	mux.HandleFunc("/api/ai/ask-stream", h.aiAskStream)
	mux.HandleFunc("/api/search", h.search)
	mux.HandleFunc("/api/project/", h.project)
	mux.HandleFunc("/api/rpc", h.rpc)
	return loopbackGuard(mux)
}

type handler struct {
	svc   *service.Service
	bound reflect.Value
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	// Marshal BEFORE writing headers: a marshal failure after WriteHeader
	// left the client with an empty body and a success status code.
	data, err := json.Marshal(v)
	if err != nil {
		data = []byte(`{"error":"response encoding failed"}`)
		if status == http.StatusOK {
			status = http.StatusInternalServerError
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.svc.Health())
}

// aiContext returns the AI-readable knowledge-base markdown (llms.txt style).
// POST with an optional JSON body {"project_id": N} — project_id is accepted
// for forward compatibility but the current implementation returns the full
// local knowledge base, matching the desktop "Copy AI Context" action.
func (h *handler) aiContext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	markdown := h.svc.GenerateLLMsTxt()
	writeJSON(w, http.StatusOK, map[string]string{"markdown": markdown})
}

// aiAskStream serves POST /api/ai/ask-stream — the streaming form of the
// evidence-backed ask (AskAIWithEvidenceStream): the reply is pushed as
// Server-Sent Events instead of one JSON blob at the end, so a browser-mode
// frontend can render it as it forms instead of staring at a spinner for
// minutes. The desktop Wails app takes the event-channel path instead; this
// endpoint exists for the standalone/browser runtime and scripts.
//
// Wire format (one event = one data: line, blank-line terminated):
//
//	data: {"delta":"…"}         ← content, many
//	data: {"done":true,"reply":"…","evidence":{…},"truncated":bool}   ← last
//	data: {"error":"…"}         ← instead of done, on failure
//
// Mirrors askAIWithEvidence's JSON shape for done, so one client-side reader
// handles both askAIWithEvidence and this stream.
func (h *handler) aiAskStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		ProjectID int64  `json:"project_id"`
		Question  string `json:"question"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if strings.TrimSpace(req.Question) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "question is required"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported by this response writer"})
		return
	}

	writeEvent := func(v any) error {
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ans, err := h.svc.AskAIWithEvidenceStream(req.ProjectID, req.Question, service.DefaultEvidenceBudget, func(delta string) {
		_ = writeEvent(map[string]any{"delta": delta})
	}, func(_id, _name string) {}, func(_id string, _result any) {})
	if err != nil {
		_ = writeEvent(map[string]any{"error": err.Error()})
		return
	}
	_ = writeEvent(map[string]any{
		"done":      true,
		"reply":     ans.Reply,
		"evidence":  ans.Evidence,
		"truncated": ans.Truncated,
	})
}

// maxSearchQueryLen caps the ?q= parameter, mirroring the MCP layer's query
// bound. The query is escaped before it reaches SQLite, so this is not an
// injection guard — it stops a multi-megabyte value from turning every
// FTS/LIKE match into a multi-second scan. The trust boundary is loopback
// only, but any local process can hit this endpoint repeatedly.
const maxSearchQueryLen = bounds.MaxQueryLen

// search runs a full-text knowledge search across notes and todos.
// Query param: q (required). Use ?all=1 to also include todos (default notes).
func (h *handler) search(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing query param 'q'"})
		return
	}
	if len(q) > maxSearchQueryLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("query too long: %d bytes (max %d)", len(q), maxSearchQueryLen),
		})
		return
	}
	var hits any
	if r.URL.Query().Get("all") == "1" {
		hits = h.svc.SearchAll(q)
	} else {
		hits = h.svc.SearchNotes(q)
	}
	writeJSON(w, http.StatusOK, map[string]any{"query": q, "hits": hits})
}

// project dispatches /api/project/{id}/detail and /api/project/{id}/stats.
func (h *handler) project(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	// Strip prefix "/api/project/" → "{id}/detail" or "{id}/stats".
	rest := strings.TrimPrefix(r.URL.Path, "/api/project/")
	rest = strings.Trim(rest, "/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing project id"})
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid project id"})
		return
	}

	// notFoundOr500 keeps the status code honest: a service error that is the
	// documented "no such project" case maps to 404, and anything else (a
	// full disk, a locked database, a corrupt table) maps to 500 instead of
	// masquerading as "absent".
	notFoundOr500 := func(w http.ResponseWriter, err error) {
		if errors.Is(err, service.ErrProjectNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		log.Printf("project %d request failed: %v", id, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
	}

	switch {
	case len(parts) == 1 || parts[1] == "detail" || parts[1] == "":
		detail, err := h.svc.GetProjectDetail(id)
		if err != nil {
			notFoundOr500(w, err)
			return
		}
		writeJSON(w, http.StatusOK, detail)
	case parts[1] == "overview":
		ov, err := h.svc.GetProjectOverview(id)
		if err != nil {
			notFoundOr500(w, err)
			return
		}
		writeJSON(w, http.StatusOK, ov)
	case parts[1] == "stats":
		date := r.URL.Query().Get("date")
		stats := h.svc.GetProjectStats(id, date)
		writeJSON(w, http.StatusOK, map[string]any{"project_id": id, "date": date, "stats": stats})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown project subroute: " + parts[1]})
	}
}
