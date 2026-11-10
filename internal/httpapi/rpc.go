package httpapi

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"reflect"
	"strconv"
	"strings"
)

// /api/rpc is a generic JSON-RPC bridge to the SAME bound object the desktop
// Wails App exposes (internal/app.App). The web frontend's transport calls it
// in browser/standalone mode, so every Wails binding works over HTTP without a
// hand-written REST route per method — and without changing the existing
// REST endpoints the dsh-plugin/agents rely on.
//
// It is loopback-only (wrapped by loopbackGuard like the rest of this server),
// so it carries the same trust boundary as the direct bindings: it can do
// anything the desktop UI can. Lifecycle methods are the one exception — they
// exist for the Wails runtime, not for callers over the wire.

var errorType = reflect.TypeOf((*error)(nil)).Elem()

// contextType is matched against method parameters so a missing/null arg on a
// context-taking method becomes the request's context instead of a nil
// interface (which would panic the method on first ctx use).
var contextType = reflect.TypeOf((*context.Context)(nil)).Elem()

// rpcBlockedMethods are Wails lifecycle hooks on App that the HTTP bridge must
// never expose. MethodByName has no notion of "internal", and without this
// filter a single POST {"method":"Shutdown"} would close the database — the
// process stays up and listening, but every later request fails with
// "database is closed" and there is no crash to investigate.
var rpcBlockedMethods = map[string]bool{
	"Startup":  true,
	"Shutdown": true,
	"Service":  true,
	// StartAskStream is desktop-tailored: it replies over Wails runtime events,
	// which an HTTP caller has no way to observe. The browser/standalone
	// runtime uses the dedicated SSE endpoint (/api/ai/ask-stream) instead —
	// exposing the binding over RPC would hand out a stream id nobody can read.
	"StartAskStream": true,
}

// maxRPCBodyBytes caps the request body. Args decode fully into memory, and
// the largest legitimate payload (a handoff note) is bounded far below this by
// db.MaxNoteContentLen; anything bigger is a local process trying to OOM the
// service through its own loopback port.
const maxRPCBodyBytes = 1 << 20 // 1 MiB

type rpcRequest struct {
	Method string            `json:"method"`
	Args   []json.RawMessage `json:"args"`
}

func (h *handler) rpc(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if r.ContentLength > maxRPCBodyBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRPCBodyBytes)
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
			return
		}
		log.Printf("rpc: decode request: %v", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if rpcBlockedMethods[req.Method] {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "method not callable over rpc: " + req.Method})
		return
	}
	if !h.bound.IsValid() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "rpc not available"})
		return
	}
	m := h.bound.MethodByName(req.Method)
	if !m.IsValid() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown method: " + req.Method})
		return
	}
	mt := m.Type()
	if mt.IsVariadic() {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "variadic methods not supported"})
		return
	}
	// The result handling below assumes (T, error) / (T) / (error) shapes.
	// Verifying here keeps a future signature change from panicking the
	// handler (out[1].IsNil() on a non-nilable type) — it degrades to a 501
	// like the variadic case instead.
	if mt.NumOut() > 2 || (mt.NumOut() == 2 && mt.Out(1) != errorType) {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "unsupported method signature: " + req.Method})
		return
	}
	in := make([]reflect.Value, mt.NumIn())
	for i := 0; i < mt.NumIn(); i++ {
		p := reflect.New(mt.In(i))
		if i < len(req.Args) && len(req.Args[i]) > 0 && string(req.Args[i]) != "null" {
			if err := json.Unmarshal(req.Args[i], p.Interface()); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "arg " + strconv.Itoa(i) + ": " + err.Error()})
				return
			}
		} else if mt.In(i) == contextType {
			// A nil context.Context would panic the method on first use; hand
			// it the request's context so lifecycle semantics stay correct.
			p.Elem().Set(reflect.ValueOf(r.Context()))
		}
		in[i] = p.Elem()
	}

	out := m.Call(in)
	var result any
	switch {
	case len(out) == 1 && out[0].Type() == errorType:
		if !out[0].IsNil() {
			log.Printf("rpc: %s failed: %v", req.Method, out[0].Interface().(error))
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": out[0].Interface().(error).Error()})
			return
		}
	case len(out) == 2: // (T, error), signature verified above
		if !out[1].IsNil() {
			log.Printf("rpc: %s failed: %v", req.Method, out[1].Interface().(error))
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": out[1].Interface().(error).Error()})
			return
		}
		result = out[0].Interface()
	case len(out) == 1:
		result = out[0].Interface()
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": result})
}
