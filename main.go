package main

import (
	"context"
	"embed"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"

	"repotory/internal/app"
	"repotory/internal/db"
	"repotory/internal/platform"
	"repotory/internal/service"
	"repotory/internal/version"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:web/dist
var assets embed.FS

func main() {
	platform.SetPrivateUmask() // owner-only files: DB sidecars, logs, exports
	log.Printf("Cinder %s starting... (memory-layer contract %s)", version.Version, version.CoreVersion)

	// Check storage safety before opening database
	dbPath := platform.GetDbPath()
	if safety := platform.CheckStorageSafety(dbPath); !safety.IsSafe {
		log.Printf("WARNING: 存储安全风险 [%s]: %s (路径: %s)", safety.RiskType, safety.Warning, safety.Path)
	}

	// Open database
	database, err := db.InitDB(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// Detect git user
	gitUser := platform.GetGitUserName()
	log.Printf("Git user for personal stats: %s", gitUser)

	// Create the service core and the thin Wails binding layer over it. Scan
	// roots are seeded through the service (shared with the MCP server) so the
	// first-run behaviour is identical on both entry points.
	svc := service.New(database, gitUser)
	// This process IS the desktop surface (ADR-0030): every model call it
	// makes was started by a human in the app, and the audit row says so.
	svc.SetAuditSurface(service.AISurfaceDesktop)
	svc.EnsureDefaultScanRoots()
	// The Wails runtime is wired here, at the desktop entry point, and nowhere
	// else. internal/app is also linked into the headless server, and a Wails
	// import in that package would drag the whole webview frontend stack into
	// a binary that never opens a window.
	a := app.New(svc,
		app.WithEmitter(func(ctx context.Context, event string, data any) {
			wailsruntime.EventsEmit(ctx, event, data)
		}),
		app.WithOpener(func(ctx context.Context, rawURL string) {
			wailsruntime.BrowserOpenURL(ctx, rawURL)
		}),
	)

	// Launch Wails
	err = wails.Run(&options.App{
		Title:     "Cinder Desktop: Local Git Knowledge Base",
		Width:     1360,
		Height:    860,
		MinWidth:  900,
		MinHeight: 600,
		Mac: &mac.Options{
			// The macOS title bar is hidden but the standard window controls
			// stay, so the shell's narrow left rail can sit underneath them
			// and own the traffic lights. Windows and Linux keep their native
			// frame (no per-platform options there), and the same rail reads
			// as a rail there too.
			TitleBar: mac.TitleBarHidden(),
		},
		// The application menu (ADR-0047 decision 1). The desktop app is no
		// longer resident — closing the window ends the process — so there is
		// no status bar to keep and no "Show Cinder" item.
		//
		// The Edit and Window role menus stay, and they are NOT optional:
		// Wails' darwin frontend always calls SetAsApplicationMenu, and with
		// no menu at all the bar would be empty — macOS webviews then have no
		// Cmd+C / Cmd+V. These are role items, so the system renders them and
		// they do not depend on any item of our own.
		Menu: appMenu(),
		AssetServer: &assetserver.Options{
			Assets:  assets,
			Handler: spaFallback{},
			Middleware: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// Security headers on every response.
					// script-src has no 'unsafe-inline': the built index.html
					// references a single external module bundle, and Wails
					// injects its runtime as a native user script (WKUserScript
					// on macOS, AddScriptToExecuteOnDocumentCreated on
					// Windows), which page CSP does not govern. worker-src
					// blob: covers Mermaid's ELK layout workers. connect-src
					// allows no remote host: the app makes no remote API calls,
					// and neither ws: nor wss: is listed because the shipped
					// frontend opens no WebSocket (vite HMR is a dev-server
					// concern and never runs behind this handler). Two channels
					// remain open by choice: img-src https: (remote note
					// images) and 'self' fetches — both can exfiltrate data to
					// a host the app already talks to, which is none, so an
					// injected script's practical exfil routes are the same
					// ones a <img src="https://..."> tag has; CSP narrows the
					// blast radius, it is not a hard exfil wall. unsafe-eval is
					// intentionally omitted; if dynamic eval is needed,
					// refactor to use explicit Function() calls with a nonce
					// instead.
					w.Header().Set("Content-Security-Policy",
						"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; font-src 'self'; connect-src 'self'; worker-src 'self' blob:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
					w.Header().Set("X-Content-Type-Options", "nosniff")
					w.Header().Set("X-Frame-Options", "DENY")
					w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
					w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), acceleration=()")
					next.ServeHTTP(w, r)
				})
			},
		},
		OnStartup: func(ctx context.Context) {
			a.Startup(ctx)
		},
		OnShutdown: a.Shutdown,
		Bind: []interface{}{
			a,
		},
	})
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
}

// appMenu builds the application menu.
//
// The desktop app is not resident (ADR-0047 decision 1): closing the window
// ends the process, so there is no status bar to keep and no item of our own
// to offer.
//
// The role menus are NOT optional, and their ORDER matters:
//
//   - The Edit role menu carries Cut / Copy / Paste, so it is what gives the
//     macOS webview its Cmd+C / Cmd+V. Wails' darwin frontend always calls
//     SetAsApplicationMenu, and with no menu at all the bar would be empty.
//   - macOS renders the FIRST menu item as the application menu and shows the
//     app's name there, so the first item must be one whose title the system
//     supplies. The AppMenu role is exactly that (its title is the app's
//     localized name, and it carries About / Hide / Quit). Putting Edit first
//     instead makes macOS render "Edit" as the application menu — the app
//     name then replaces the Edit title and the whole Edit group collapses
//     into the application menu, so the bar shows only "Cinder | Window"
//     and the copy/paste items are no longer where they belong.
func appMenu() *menu.Menu {
	m := menu.NewMenu()
	m.Append(menu.AppMenu())
	m.Append(menu.EditMenu())
	m.Append(menu.WindowMenu())
	return m
}

// spaFallback serves index.html for any GET request the embedded Assets could
// not resolve. The browser build may use real SPA paths, so a direct load or
// refresh of a deep link must receive the app shell. The desktop WebView uses
// hash navigation to avoid custom-origin history limitations.
type spaFallback struct{}

// ServeHTTP serves the SPA shell for any path not found in the embedded assets.
func (spaFallback) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	data, err := assets.ReadFile("web/dist/index.html")
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func init() {
	// Ensure PATH includes common binary directories (git may not be in PATH
	// when launched from Finder).
	ensurePath()
	// Redirect logs to a file so crashes can be diagnosed when the app is
	// launched outside a terminal.
	setupLogging()
}

func ensurePath() {
	// The motivating case is macOS: an app launched from Finder inherits a
	// minimal PATH, so git is not found. The standard dirs are Unix paths —
	// prepending them on Windows would just add permanent junk entries.
	if runtime.GOOS != "darwin" {
		return
	}
	path := os.Getenv("PATH")
	if path == "" {
		os.Setenv("PATH", "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin")
		return
	}
	// Prepend standard directories if not already present.
	standardDirs := []string{"/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"}
	seen := make(map[string]bool)
	for _, dir := range strings.Split(path, string(os.PathListSeparator)) {
		seen[dir] = true
	}
	var toAdd []string
	for _, dir := range standardDirs {
		if !seen[dir] {
			toAdd = append(toAdd, dir)
		}
	}
	if len(toAdd) > 0 {
		os.Setenv("PATH", strings.Join(toAdd, string(os.PathListSeparator))+string(os.PathListSeparator)+path)
	}
}

func setupLogging() {
	logFile := platform.GetLogPath()
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0640)
	if err == nil {
		log.SetOutput(f)
	}
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Printf("=== Cinder log started ===")
	log.Printf("log file: %s", logFile)
	log.Printf("PATH=%s", os.Getenv("PATH"))
}
