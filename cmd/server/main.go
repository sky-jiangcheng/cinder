// Command server runs Repotory as a headless HTTP service, exposing the shared
// internal/service business logic over a JSON API. It is the bridge that lets
// the DeepSeek Harness dsh-plugin (and any other HTTP client) reuse the exact
// same analysis code the desktop App uses, without duplicating logic.
//
// Usage:
//
//	go build -o repotory-server ./cmd/server
//	./repotory-server --port 18765
//	REPOTORY_HTTP_PORT=18765 ./repotory-server
//
// (This is its own binary, not a subcommand: the desktop `repotory` executable at
// the repo root has no argument dispatch — it launches Wails and returns.)
//
// The server binds to 127.0.0.1 only — it is a local agent, not a public
// service. The dsh-plugin spawns this process and connects to the chosen port.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"repotory/internal/app"
	"repotory/internal/db"
	"repotory/internal/httpapi"
	"repotory/internal/platform"
	"repotory/internal/service"
	"repotory/internal/version"
)

// Server timeouts. ReadHeaderTimeout bounds how long a local process can hold
// a half-open connection before sending a request; without it, slowloris-style
// hangs accumulate unbounded goroutines. IdleTimeout reaps keep-alive
// connections a client stopped using (without it they live forever).
// WriteTimeout used to rest on the premise that every long job is dispatched to
// a background goroutine and returns at once. Ingest-time compilation broke that
// premise: it is a synchronous call that waits on a local LLM, and a 27B model
// can take minutes per note. With the old 60s ceiling the handler's work
// COMMITTED but the response could no longer be written — the client saw a
// connection error over a successful operation, which is the worst possible
// failure mode (it teaches users to distrust a tool that actually worked).
//
// So this must stay above service.DefaultBatchChatTimeout, and the pairing is
// enforced at runtime below rather than by a comment that decays.
//
// The real fix is an async job + polling surface, not an ever-longer synchronous
// timeout; that is proposed separately (ADR-0016) and this is the stopgap.
const (
	headerTimeout = 10 * time.Second
	// Must exceed the batch LLM ceiling; see the block above.
	writeTimeout = service.DefaultBatchChatTimeout + 60*time.Second
	idleTimeout  = 120 * time.Second
)

func main() {
	platform.SetPrivateUmask() // owner-only files: DB sidecars, logs, exports

	port := flag.String("port", envOr("REPOTORY_HTTP_PORT", "18765"), "HTTP port for the headless API (loopback only)")
	// webDist serves the built frontend (web/dist) from this process so a
	// browser can reach the SPA and the /api JSON-RPC bridge on a single
	// origin — no separate dev server or cross-origin proxy required. Used by
	// the rehearsal stack (.annotation-prep/rehearse) against a copy DB.
	// Empty (default) leaves the server API-only, exactly as production runs.
	webDist := flag.String("web-dist", envOr("REPOTORY_WEB_DIST", ""), "directory of the built web frontend to serve (optional, API-only when empty)")
	flag.Parse()

	log.Printf("Repotory headless server %s starting on 127.0.0.1:%s (memory-layer contract %s)",
		version.Version, *port, version.CoreVersion)

	database, err := openDatabase(platform.GetDbPath())
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	gitUser := platform.GetGitUserName()
	svc := service.New(database, gitUser)
	// This process IS the HTTP surface (ADR-0030): every model call it makes
	// arrives over the HTTP / JSON-RPC API, and the audit row says so.
	svc.SetAuditSurface(service.AISurfaceHTTP)

	// Seed default scan roots like the desktop App and MCP entry points do
	// (idempotent via the scan_roots_seeded flag): without it a fresh headless
	// install has no roots and TriggerScan fails until the user configures
	// one by hand.
	svc.EnsureDefaultScanRoots()

	// Run the same startup sequence the desktop App runs. Without it the
	// knowledge-source importers are never registered, so GetKnowledgeSources
	// returns an empty list and TriggerKnowledgeImport fails with
	// `unknown knowledge source`. Startup is sync.Once-guarded and also honours
	// the auto_import config key, so headless clients get the same import
	// behaviour as the desktop app instead of a silently reduced feature set.
	svc.Startup()

	// Bind the same App object the desktop Wails layer exposes, so the browser
	// frontend can reach every capability via /api/rpc (see httpapi/rpc.go).
	mux := httpapi.New(svc, app.New(svc))

	// When webDist is supplied, serve the built SPA from the same origin as the
	// API so the browser talks to /api/rpc directly (no node proxy, which this
	// runtime intercepts). /api/* always routes to the RPC mux; everything else
	// serves a real static file or falls back to index.html (client routing).
	root := newWebHandler(*webDist, mux)
	// Drift guard for the invariant stated above: if someone shortens either
	// ceiling, fail loudly at startup instead of silently reintroducing the
	// "committed but reported as failed" behaviour.
	if writeTimeout <= service.DefaultBatchChatTimeout {
		log.Fatalf("server misconfigured: WriteTimeout (%v) must exceed the batch chat ceiling (%v)",
			writeTimeout, service.DefaultBatchChatTimeout)
	}

	srv := &http.Server{
		Addr:              "127.0.0.1:" + *port,
		Handler:           root,
		ReadHeaderTimeout: headerTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	// Graceful shutdown on SIGINT/SIGTERM (the dsh-plugin stops the process
	// with SIGTERM): stop accepting, let in-flight handlers finish, then run
	// the service shutdown (cancels background work and waits for it) and
	// close the database. A SIGKILL has no such window and relies on SQLite's
	// WAL crash-safety, as documented in the service layer.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)
	go func() { serverErr <- srv.ListenAndServe() }()

	log.Printf("Repotory headless API listening at http://127.0.0.1:%s", *port)
	select {
	case err := <-serverErr:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	case <-ctx.Done():
		log.Printf("shutdown signal received; draining connections")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP shutdown: %v", err)
		}
	}

	svc.Shutdown()
	if err := svc.Close(); err != nil {
		log.Printf("close database: %v", err)
	}
	log.Printf("Repotory headless server stopped")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// openDatabase opens the knowledge base, retrying while another Repotory
// process holds the write lock.
//
// The desktop app and this server share one SQLite file, and OPENING it
// writes: the WAL pragma, the schema re-assertions (EnsureCompileJobs /
// EnsureWikiSchema / EnsureFTSIndex) and the migration step all take the
// write lock. With busy_timeout=5000 (internal/db/db.go) that is five
// seconds — and a desktop app in the middle of an import or of the daily
// snapshot holds the lock for longer than that. The server then died at
// startup with "database is locked", and the IDE plugins read that as
// "cannot reach the service": the service was never up, and nothing said
// why.
//
// So lock contention is retried with a growing wait, and the wait is
// logged. Every other failure stays fatal on the first try — a missing file
// or a broken schema must not be retried into a timeout.
func openDatabase(path string) (*sql.DB, error) {
	const attempts = 6
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		handle, initErr := db.InitDB(path)
		if initErr == nil {
			return handle, nil
		}
		err = initErr
		if !isLockContention(initErr) {
			return nil, err
		}
		if attempt == attempts {
			break
		}
		wait := time.Duration(attempt) * time.Second
		log.Printf("database is locked by another Repotory process (attempt %d/%d); retrying in %s", attempt, attempts, wait)
		time.Sleep(wait)
	}
	return nil, fmt.Errorf("database stayed locked after %d attempts: %w", attempts, err)
}

// isLockContention reports whether the failure is "another process holds the
// write lock" rather than a genuine defect.
func isLockContention(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked") ||
		strings.Contains(msg, "sqlite_busy")
}

// isAPIPath reports whether a request must reach the RPC mux even when static
// hosting is on. It mirrors httpapi.New's registrations: every route lives
// under /api except /health. Without the /health clause the SPA fallback would
// swallow it into index.html (200 text/html instead of the JSON health
// payload). TestNewWebHandlerRouting pins this against httpapi's route set.
func isAPIPath(p string) bool {
	return p == "/api" || strings.HasPrefix(p, "/api/") || p == "/health"
}

// newWebHandler wraps the RPC mux with optional static SPA serving.
//
// webDist == ""  -> API only (production behaviour, unchanged).
// webDist != ""  -> API paths (see isAPIPath) route to the mux; all other
//
//	paths serve a file from webDist, falling back to
//	index.html for client-side routes (React Router). Path
//	traversal is rejected: any resolved path escaping webDist
//	is forced to the SPA root.
//	A miss on a path that looks like a static asset (it has a
//	non-.html extension) is a real 404 instead: falling back
//	there would return HTML with 200 for a .js request and the
//	browser would fail the module on MIME type — a frontend
//	phantom that hides the actual "file missing" cause.
func newWebHandler(webDist string, api http.Handler) http.Handler {
	if webDist == "" {
		return api
	}
	// Resolve once at startup: relative --web-dist values stop depending on
	// the process working directory staying unchanged for the server lifetime.
	cleanRoot, err := filepath.Abs(webDist)
	if err != nil {
		log.Fatalf("invalid --web-dist %q: %v", webDist, err)
	}
	log.Printf("serving web dist from %s", cleanRoot)
	fileServer := http.FileServer(http.Dir(cleanRoot))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAPIPath(r.URL.Path) {
			api.ServeHTTP(w, r)
			return
		}
		rel := strings.TrimPrefix(r.URL.Path, "/")
		full := filepath.Join(cleanRoot, filepath.Clean("/"+rel))
		if !strings.HasPrefix(full, cleanRoot) {
			serveIndex(w, r, cleanRoot)
			return
		}
		if info, statErr := os.Stat(full); statErr == nil && !info.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}
		if ext := filepath.Ext(r.URL.Path); ext != "" && ext != ".html" {
			http.NotFound(w, r)
			return
		}
		serveIndex(w, r, cleanRoot)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, dist string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeFile(w, r, filepath.Join(dist, "index.html"))
}
