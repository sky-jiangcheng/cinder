package app

import (
	"context"
	"strings"
	"testing"
)

// The two UI side effects are injected, so the App carries no Wails dependency
// (the headless server links this package — see cmd/server/wails_free_test.go).
// These tests pin both halves of that contract: the desktop's injected emitter
// still receives its events, and an App without one drops them silently
// instead of panicking or reaching for a runtime that is not there.

func TestEmitEventReachesInjectedEmitter(t *testing.T) {
	var gotEvent string
	var gotData any
	a := New(nil, WithEmitter(func(_ context.Context, event string, data any) {
		gotEvent, gotData = event, data
	}))
	a.ctx = context.Background()

	a.emitEvent("import.completed", map[string]any{"projects": 3})

	if gotEvent != "import.completed" {
		t.Fatalf("emitter got event %q, want %q", gotEvent, "import.completed")
	}
	data, ok := gotData.(map[string]any)
	if !ok || data["projects"] != 3 {
		t.Fatalf("emitter got payload %#v, want the one that was emitted", gotData)
	}
}

func TestEmitEventIsDroppedWithoutEmitter(t *testing.T) {
	a := New(nil)
	a.ctx = context.Background()

	// The headless App has no runtime to deliver to. Dropping is the outcome
	// the old nil-ctx check produced; the point is that it is silent, not loud.
	a.emitEvent("import.completed", map[string]any{"projects": 3})
}

func TestEmitEventIsDroppedWithoutRuntimeContext(t *testing.T) {
	called := false
	a := New(nil, WithEmitter(func(_ context.Context, _ string, _ any) {
		called = true
	}))
	// No ctx: the App was never started, so there is no runtime to talk to.
	a.emitEvent("import.completed", nil)

	if called {
		t.Fatal("emitter must not be called before the runtime context exists")
	}
}

func TestOpenBrowserURLUsesInjectedOpener(t *testing.T) {
	var gotURL string
	a := New(nil, WithOpener(func(_ context.Context, rawURL string) {
		gotURL = rawURL
	}))
	a.ctx = context.Background()

	if err := a.OpenBrowserURL("https://example.com/release"); err != nil {
		t.Fatalf("OpenBrowserURL failed: %v", err)
	}
	if gotURL != "https://example.com/release" {
		t.Fatalf("opener got %q, want the URL that was passed in", gotURL)
	}
}

func TestOpenBrowserURLWithoutOpenerReportsUnavailable(t *testing.T) {
	a := New(nil)

	err := a.OpenBrowserURL("https://example.com/release")
	if err == nil {
		t.Fatal("expected an error when no opener is injected")
	}
	if !strings.Contains(err.Error(), "browser open unavailable outside the desktop runtime") {
		t.Fatalf("error is %q, want the 'unavailable outside the desktop runtime' wording", err)
	}
}

func TestOpenBrowserURLRefusesNonHTTPSBeforeAnythingElse(t *testing.T) {
	called := false
	a := New(nil, WithOpener(func(_ context.Context, _ string) { called = true }))
	a.ctx = context.Background()

	err := a.OpenBrowserURL("http://example.com/release")
	if err == nil || !strings.Contains(err.Error(), "refusing to open non-https URL") {
		t.Fatalf("error is %v, want the non-https refusal", err)
	}
	if called {
		t.Fatal("the opener must not be called for a refused URL")
	}
}
