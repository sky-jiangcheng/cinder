package main

import (
	"context"
	"embed"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync/atomic"

	"repotory/internal/app"
	"repotory/internal/db"
	"repotory/internal/platform"
	"repotory/internal/service"
	"repotory/internal/version"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:web/dist
var assets embed.FS

func main() {
	platform.SetPrivateUmask() // owner-only files: DB sidecars, logs, exports
	log.Printf("Repotory %s starting...", version.Version)

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
	a := app.New(svc)

	// Launch Wails
	err = wails.Run(&options.App{
		Title:     "Repotory: Local Git Knowledge Base",
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
		// The application menu (ADR-0045 decision 2). On macOS this IS the
		// status bar: the app keeps running with no visible window, and
		// these items are how the user gets it back.
		Menu: appMenu(),
		// Only one process may own the database. A second launch wakes the
		// running one instead of starting a rival process over the same
		// SQLite file (ADR-0045 decision 1).
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "com.repotory.desktop",
			OnSecondInstanceLaunch: func(_ options.SecondInstanceData) {
				// The user asked for the app again: bring it forward and
				// open the bot — the bot is the app's front door now.
				showWindow()
				askBot()
			},
		},
		// Closing the window does NOT end the process (ADR-0045 decision 1).
		// OnBeforeClose intercepts the close: macOS hides the window so the
		// menu bar keeps the app's items, and Windows and Linux minimise it
		// so the taskbar keeps an icon. Only the "Quit Repotory" menu item
		// really ends the process.
		OnBeforeClose: func(ctx context.Context) bool {
			if quitting.Load() {
				return false // the explicit quit gets through
			}
			// The hide runs on its own goroutine: on Windows the close
			// callback arrives ON the UI thread, and the window calls there
			// post messages to that same thread.
			go func() {
				if runtime.GOOS == "darwin" {
					wailsruntime.WindowHide(ctx)
				} else {
					wailsruntime.WindowMinimise(ctx)
				}
			}()
			return true
		},
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
			// The menu callbacks get no context of their own, so the app
			// context is kept here for them to reach the runtime with.
			appCtx.Store(ctx)
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

// --- the resident app (ADR-0045) ---------------------------------------------
//
// Closing the window does not end the process. The bot stays alive, and on
// macOS the menu bar keeps the app's items, so the app is still reachable
// with no visible window.
//
// Wails v2 has no system tray: pkg/menu carries a TrayMenu type, but no
// platform frontend renders it and no public API reaches it. So on macOS
// the MENU BAR is the status bar, and on Windows and Linux the taskbar
// icon is. The manual says so out loud.

// quitting is set by the "Quit Repotory" menu item, so the close handler
// can tell "the window was closed" from "the app is leaving".
var quitting atomic.Bool

// appCtx holds the Wails context (a context.Context, stored atomically).
var appCtx atomic.Value

// The event the menu bar's "Ask Repotory…" item fires; the frontend opens
// the bot panel when it sees it.
const eventAskBot = "app.ask-bot"

// appMenu builds the application menu.
//
// On macOS this IS the status bar: the app runs with no visible window and
// these items are how the user gets it back. The Edit menu is not optional
// — without it macOS has no Cmd+C / Cmd+V in the webview.
func appMenu() *menu.Menu {
	m := menu.NewMenu()

	// The app's own menu comes FIRST: macOS reads the first item as the
	// application menu, and shows the app's name there.
	app := m.AddSubmenu("Repotory")
	app.AddText("Show Repotory", nil, func(_ *menu.CallbackData) {
		showWindow()
	})
	app.AddText("Ask Repotory…", keys.CmdOrCtrl("k"), func(_ *menu.CallbackData) {
		showWindow()
		askBot()
	})
	app.AddSeparator()
	app.AddText("Quit Repotory", keys.CmdOrCtrl("q"), func(_ *menu.CallbackData) {
		quitApp()
	})

	m.Append(menu.EditMenu())
	m.Append(menu.WindowMenu())
	return m
}

// runtimeContext returns the app context, or nil before the app started.
func runtimeContext() context.Context {
	ctx, _ := appCtx.Load().(context.Context)
	return ctx
}

// showWindow brings the window back and focuses it.
func showWindow() {
	ctx := runtimeContext()
	if ctx == nil {
		return
	}
	wailsruntime.WindowShow(ctx)
}

// askBot shows the window and asks the frontend to open the bot panel.
func askBot() {
	ctx := runtimeContext()
	if ctx == nil {
		return
	}
	wailsruntime.EventsEmit(ctx, eventAskBot)
}

// quitApp ends the process for real — the only way out of the resident app.
func quitApp() {
	ctx := runtimeContext()
	if ctx == nil {
		return
	}
	quitting.Store(true)
	wailsruntime.Quit(ctx)
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
	log.Printf("=== Repotory log started ===")
	log.Printf("log file: %s", logFile)
	log.Printf("PATH=%s", os.Getenv("PATH"))
}
