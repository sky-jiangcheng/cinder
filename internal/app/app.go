// Package app is the shared binding surface: every exported method on App is
// callable from a frontend. Methods are one-to-three-line delegations to
// internal/service — all business logic lives there and is shared with the
// CLI and MCP server.
//
// Two binders consume this surface. The desktop Wails runtime binds it
// directly, and the headless HTTP bridge reflects over the same object
// (internal/httpapi/rpc.go), so the two can never drift apart.
//
// This package must not import the Wails runtime. cmd/server links it, and a
// Wails import here drags the whole webview frontend stack (13 packages) into
// a binary that never opens a window. The two UI side effects — emitting an
// event, opening a URL in the system browser — are therefore injected
// (WithEmitter / WithOpener), and their Wails implementations live in the
// desktop entry point.
package app

import (
	"context"
	"log"

	"repotory/internal/service"
)

// Emitter delivers a UI event (name plus payload) to the runtime that asked
// for the work.
type Emitter func(ctx context.Context, event string, data any)

// Opener opens a URL in the user's system browser.
type Opener func(ctx context.Context, rawURL string)

// Option configures an App at construction time.
type Option func(*App)

// WithEmitter sets the UI event sink. Without one, events are dropped.
func WithEmitter(e Emitter) Option {
	return func(a *App) { a.emit = e }
}

// WithOpener sets the system-browser opener. Without one, OpenBrowserURL
// reports that it is unavailable outside the desktop runtime.
func WithOpener(o Opener) Option {
	return func(a *App) { a.open = o }
}

// App is the main application struct whose public methods are exposed to the
// frontend via Wails Bind. The ctx is set during OnStartup.
type App struct {
	ctx  context.Context
	svc  *service.Service
	emit Emitter
	open Opener
}

// New creates a new App backed by the given service.
func New(svc *service.Service, opts ...Option) *App {
	a := &App{svc: svc}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Service exposes the underlying service (used by main for lifecycle calls).
func (a *App) Service() *service.Service { return a.svc }

// emitEvent delivers an event to the UI runtime, if there is one. A headless
// App has no runtime to deliver to, so the event is dropped — the same
// outcome the old nil-ctx check produced.
func (a *App) emitEvent(event string, data any) {
	if a.emit == nil || a.ctx == nil {
		return
	}
	a.emit(a.ctx, event, data)
}

// startup is called at application startup: it wires the import-event
// forwarder (service → UI event) and initialises the service runtime.
// Exported because Wails options require a package-level callable; guarded
// against double invocation via sync.Once in the service.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.svc.SetImportEventHandler(func(payload service.ImportEventPayload) {
		a.emitEvent("import.completed", payload)
	})
	a.svc.Startup()
}

// Shutdown is called when the application exits.
func (a *App) Shutdown(_ context.Context) {
	a.svc.Shutdown()
	if err := a.svc.Close(); err != nil {
		log.Printf("close database: %v", err)
	}
}

// Health returns a health-check payload for the frontend.
func (a *App) Health() map[string]any { return a.svc.Health() }
