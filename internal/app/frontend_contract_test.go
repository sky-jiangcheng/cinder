package app

import (
	"os"
	"regexp"
	"sort"
	"testing"
)

// Frontend↔binding contract guard.
//
// Why this file exists. The browser frontend reaches every binding through one
// transport call, and the method name is a plain string:
//
//	call<SearchHit[]>({ method: 'SearchAll', args: [query] })
//
// TypeScript cannot check that string against the Go binding surface, so
// renaming or removing an *App method breaks the browser/standalone runtime at
// CALL time, not at build time — and the desktop Wails runtime shares the same
// method names, so both halves fail the same way: a runtime error in the
// console, found by whoever happens to click that button.
//
// api_contract_test.go already guards the other direction (the API reference
// must not promise a binding that does not exist). This file guards the one
// that actually breaks users: the bindings the frontend calls must exist.
//
// The frontend's transport is the single source of truth for the browser
// runtime, so its call list is the thing worth pinning — not a hand-kept
// duplicate of it.

const frontendEndpointsPath = "../../web/src/api/endpoints.ts"

// frontendMethodCall matches a transport method name. The frontend writes
// every one as a single-quoted literal (`method: 'SearchAll'`), so the pattern
// requires the quote — that is what keeps it from matching a helper
// parameter named `method` (jsonInit in endpoints.ts).
var frontendMethodCall = regexp.MustCompile(`method:\s*'([A-Za-z][A-Za-z0-9_]*)'`)

// frontendInitBlock matches the RequestInit the transport carries alongside
// the RPC method: `init: { method: 'POST' }`. Its own `method` key is an HTTP
// VERB, not a binding name — extracting it would ask the Go side for a
// binding called "POST". The blocks carry no nested braces today; the
// extractor fails loudly (the count sanity check) if that ever changes.
var frontendInitBlock = regexp.MustCompile(`init:\s*\{[^}]*\}`)

// frontendTransportMethods returns the distinct binding names the browser
// frontend calls over the transport.
//
// Every file in web/src/api that CALLS bindings is read, not just
// endpoints.ts: the feature slices (memory, code review, knowledge import)
// keep their calls in their own module, and a file that is not read is a set
// of bindings nothing checks. The review endpoints were unguarded for
// exactly that reason — a method that did not exist on *App would have been
// caught only by clicking it.
func frontendTransportMethods(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	var out []string
	for _, path := range frontendBindingFiles(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		src := frontendInitBlock.ReplaceAllString(string(raw), "")
		for _, m := range frontendMethodCall.FindAllStringSubmatch(src, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
	}
	sort.Strings(out)
	return out
}

// Every method the frontend calls must be a real exported *App method.
//
// The failure this catches: a binding renamed or deleted in Go while the
// frontend kept calling the old name. Nothing in either toolchain notices —
// the string is untyped on the TS side and simply missing on the Go side —
// and the user gets a runtime failure in the browser runtime and in the
// desktop app alike.
func TestFrontendTransportCallsResolveToRealBindings(t *testing.T) {
	real := map[string]bool{}
	for _, m := range exportedAppMethods() {
		real[m] = true
	}

	called := frontendTransportMethods(t)
	if len(called) < 50 {
		t.Fatalf("extracted only %d frontend method calls from %s — the extractor is stale, update it",
			len(called), frontendEndpointsPath)
	}
	var phantom []string
	for _, name := range called {
		if !real[name] {
			phantom = append(phantom, name)
		}
	}
	if len(phantom) > 0 {
		t.Fatalf("the frontend calls %d binding(s) that do not exist on *App: %v\n"+
			"The method name is a string on the TS side, so neither toolchain catches this. "+
			"Either the binding was renamed/removed in Go and the frontend was not updated, "+
			"or the frontend calls a method that was never implemented.",
			len(phantom), phantom)
	}
}

// The reverse direction is deliberately NOT asserted here: the frontend may
// legitimately call a subset of the bindings (the desktop-only ones it drives
// from the UI, plus the MCP-backed ones it reuses). What must hold is the
// direction above.
