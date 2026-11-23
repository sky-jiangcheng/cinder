package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"repotory/internal/db"
	"repotory/internal/service"
)

type fakeBound struct{}

func (fakeBound) Echo(s string) string  { return s }
func (fakeBound) Add(a, b int64) int64  { return a + b }
func (fakeBound) Fail() error           { return errors.New("boom") }
func (fakeBound) Pair() (string, error) { return "ok", nil }
func (fakeBound) List() []int64         { return []int64{1, 2, 3} }

func newRPCHandler(t *testing.T) *handler {
	t.Helper()
	d, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return &handler{svc: service.New(d, "me"), bound: reflect.ValueOf(fakeBound{})}
}

func rpc(t *testing.T, h *handler, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/rpc", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.rpc(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestRPCHappyPaths(t *testing.T) {
	h := newRPCHandler(t)
	if code, out := rpc(t, h, `{"method":"Echo","args":["hi"]}`); code != 200 || out["result"] != "hi" {
		t.Fatalf("Echo: code=%d out=%v", code, out)
	}
	if code, out := rpc(t, h, `{"method":"Add","args":[2,3]}`); code != 200 || out["result"].(float64) != 5 {
		t.Fatalf("Add: code=%d out=%v", code, out)
	}
	if code, out := rpc(t, h, `{"method":"Pair"}`); code != 200 || out["result"] != "ok" {
		t.Fatalf("Pair: code=%d out=%v", code, out)
	}
	if code, out := rpc(t, h, `{"method":"List"}`); code != 200 {
		t.Fatalf("List code=%d", code)
	} else if _, ok := out["result"].([]any); !ok {
		t.Fatalf("List result not array: %v", out["result"])
	}
}

func TestRPCErrorPaths(t *testing.T) {
	h := newRPCHandler(t)
	// method returns error -> 422 with message.
	if code, out := rpc(t, h, `{"method":"Fail"}`); code != 422 || out["error"] != "boom" {
		t.Fatalf("Fail: code=%d out=%v", code, out)
	}
	// unknown method -> 404.
	if code, _ := rpc(t, h, `{"method":"Nope"}`); code != 404 {
		t.Fatalf("unknown method code=%d, want 404", code)
	}
	// bad arg type -> 400.
	if code, _ := rpc(t, h, `{"method":"Add","args":["x",2]}`); code != 400 {
		t.Fatalf("bad arg code=%d, want 400", code)
	}
	// GET not allowed -> 405.
	rec := httptest.NewRecorder()
	h.rpc(rec, httptest.NewRequest("GET", "/api/rpc", nil))
	if rec.Code != 405 {
		t.Fatalf("GET code=%d, want 405", rec.Code)
	}
}

// The bridge must never expose the Wails lifecycle hooks: a single POST
// {"method":"Shutdown"} used to close the database on the live server.
func TestRPCBlocksLifecycleMethods(t *testing.T) {
	h := newRPCHandler(t)
	for _, method := range []string{"Shutdown", "Startup", "Service"} {
		if code, out := rpc(t, h, `{"method":"`+method+`"}`); code != 403 {
			t.Fatalf("%s: code=%d out=%v, want 403", method, code, out)
		}
	}
	// StartAskStream is in the same blocklist for a DIFFERENT reason — it answers
	// over Wails events, so an HTTP caller would receive a stream id it can never
	// read. It was in the list untested, which is how a blocklist entry quietly
	// stops existing: delete the entry and nothing fails.
	if code, out := rpc(t, h, `{"method":"StartAskStream","args":[1,"q",false]}`); code != 403 {
		t.Fatalf("StartAskStream: code=%d out=%v, want 403 (it must not be reflexively callable)", code, out)
	}
}

// A JSON body is not enough: the Content-Type must also be application/json.
// Without it a remote page can POST a form-shaped body as a CORS "simple
// request" and reach write bindings without a preflight. The decoder below
// would happily accept it, so this gate is the only thing standing between a
// cross-origin page and UpdateConfig.
func TestRPCRejectsSimpleContentType(t *testing.T) {
	h := newRPCHandler(t)
	for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded", "application/json; charset=bogus; x"} {
		req := httptest.NewRequest("POST", "/api/rpc", strings.NewReader(`{"method":"Echo","args":["hi"]}`))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		rec := httptest.NewRecorder()
		h.rpc(rec, req)
		if rec.Code != 415 {
			t.Errorf("Content-Type %q: code=%d, want 415", ct, rec.Code)
		}
	}
	// A charset suffix is legitimate (fetch clients sometimes add one).
	req := httptest.NewRequest("POST", "/api/rpc", strings.NewReader(`{"method":"Echo","args":["hi"]}`))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	rec := httptest.NewRecorder()
	h.rpc(rec, req)
	if rec.Code != 200 {
		t.Errorf("application/json with charset: code=%d, want 200", rec.Code)
	}
}

// Oversized request bodies are rejected with 413 before any decoding work.
func TestRPCBodySizeLimit(t *testing.T) {
	h := newRPCHandler(t)
	big := `{"method":"Echo","args":["` + strings.Repeat("a", maxRPCBodyBytes+16) + `"]}`
	if code, _ := rpc(t, h, big); code != 413 {
		t.Fatalf("oversized body code=%d, want 413", code)
	}
}

// A method whose context parameter is omitted gets the request context, not a
// nil interface (which would panic the method on first ctx use).
func TestRPCNilContextBecomesRequestContext(t *testing.T) {
	h := newRPCHandler(t)
	if code, out := rpc(t, h, `{"method":"CtxEcho"}`); code != 200 || out["result"] == "" {
		t.Fatalf("CtxEcho: code=%d out=%v", code, out)
	}
}

func (fakeBound) CtxEcho(ctx context.Context) string {
	if ctx == nil {
		return "nil"
	}
	if ctx.Err() != nil {
		return "err"
	}
	return "ctx"
}

// A future signature drift (non-error second return) must degrade to 501, not
// panic the handler.
func TestRPCUnsupportedSignature(t *testing.T) {
	h := newRPCHandler(t)
	if code, _ := rpc(t, h, `{"method":"BadSignature"}`); code != 501 {
		t.Fatalf("BadSignature code=%d, want 501", code)
	}
}

func (fakeBound) BadSignature() (string, int) { return "x", 1 }

// The blocklist is the HTTP bridge's policy, and it has two failure modes
// that no other test caught:
//
//   - An entry ADDED without thinking. The browser/standalone runtime reaches
//     every binding through this bridge, so blocking a method the frontend
//     calls breaks that runtime in production while the desktop app keeps
//     working (it goes through Wails, not /api/rpc). The two halves then
//     disagree about which capabilities exist.
//
//   - An entry REMOVED. Deleting "Shutdown" from the list would let one POST
//     close the live database. TestRPCBlocksLifecycleMethods covers the
//     lifecycle three by name; this pins the list's SHAPE, so a stray entry
//     or a silently dropped one fails.
//
// The exact set is therefore asserted in both directions, and the frontend's
// transport calls are checked against it.
func TestRPCBlocklistIsExactlyTheReviewedSet(t *testing.T) {
	want := map[string]bool{
		"Startup":        true,
		"Shutdown":       true,
		"Service":        true,
		"StartAskStream": true,
	}
	got := map[string]bool{}
	for name := range rpcBlockedMethods {
		got[name] = true
	}
	for name := range want {
		if !got[name] {
			t.Errorf("%s must stay on the RPC blocklist", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("%s is on the RPC blocklist but is not part of the reviewed set — "+
				"blocking a method breaks the browser runtime if the frontend calls it, "+
				"and every extra entry needs a stated reason", name)
		}
	}
}

// Full-stack version of the audited PoC: a page on another loopback port
// (Vite's dev server, or any local tab) posts a simple-request body to the
// real handler. The Host/Origin guard alone lets it through — the
// application/json gate is what stops it. A remote page reporting itself as
// cross-site is refused even earlier.
func TestRPCBridgeRejectsCrossOriginSimpleRequest(t *testing.T) {
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	h := New(service.New(database, "me"), fakeBound{}, nil)

	post := func(ct, site string) int {
		req := httptest.NewRequest("POST", "/api/rpc", strings.NewReader(`{"method":"Echo","args":["hi"]}`))
		req.Host = "127.0.0.1:18765"
		req.Header.Set("Origin", "http://localhost:3000")
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		if site != "" {
			req.Header.Set("Sec-Fetch-Site", site)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := post("text/plain", ""); code != 415 {
		t.Errorf("cross-origin simple request: code=%d, want 415", code)
	}
	if code := post("application/json", "cross-site"); code != 403 {
		t.Errorf("cross-site JSON request: code=%d, want 403", code)
	}
	// The frontend's own same-origin call still works.
	if code := post("application/json", "same-origin"); code != 200 {
		t.Errorf("same-origin JSON request: code=%d, want 200", code)
	}
}

// The frontend must not call a blocked method. This is the check that turns
// "someone added a blocklist entry" from a production-only bug into a build
// failure.
//
// The frontend's method name is a plain string on the TS side, so TypeScript
// cannot know the bridge refuses it — the call would simply 403 at runtime.
var frontendMethodCall = regexp.MustCompile(`method:\s*'([A-Za-z][A-Za-z0-9_]*)'`)

// frontendInitBlock matches the RequestInit the transport carries alongside
// the RPC method (`init: { method: 'POST' }`) — its `method` key is an HTTP
// verb, not a binding name.
var frontendInitBlock = regexp.MustCompile(`init:\s*\{[^}]*\}`)

func TestFrontendCallsNoBlockedRPCMethod(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "api", "endpoints.ts"))
	if err != nil {
		t.Fatalf("read frontend endpoints: %v", err)
	}
	src := frontendInitBlock.ReplaceAllString(string(raw), "")

	seen := map[string]bool{}
	for _, m := range frontendMethodCall.FindAllStringSubmatch(src, -1) {
		seen[m[1]] = true
	}
	if len(seen) < 50 {
		t.Fatalf("extracted only %d frontend method calls — the extractor is stale, update it", len(seen))
	}
	for name := range seen {
		if rpcBlockedMethods[name] {
			t.Errorf("the frontend calls %q, which /api/rpc refuses with 403 — "+
				"the browser runtime would break at call time with no type error on either side", name)
		}
	}
}
