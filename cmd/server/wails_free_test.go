package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The headless server must not link the Wails runtime.
//
// Why this file exists. cmd/server imports internal/app for the binding
// surface, and internal/app used to import github.com/wailsapp/wails/v2/pkg/
// runtime for two side effects — emit a UI event, open a URL in the browser.
// That put 13 Wails packages (including the whole webview frontend stack) into
// a binary that never opens a window. The side effects are injected now
// (app.WithEmitter / app.WithOpener) and their Wails implementations live in
// the desktop entry point, so internal/app has no Wails import.
//
// This test pins that, because the next person who needs an event in the App
// will reach for the runtime import again — and nothing else would stop them.
//
// The check walks the module's own import graph starting at cmd/server, so it
// catches a Wails import anywhere in the server's dependency chain, not just
// in the package the import was removed from. It parses source with go/parser
// rather than shelling out to `go list`: no toolchain, no network, no module
// cache, and it sees imports that build constraints would exclude on this OS.
func TestHeadlessServerDoesNotLinkWails(t *testing.T) {
	root := moduleRoot(t)
	module := modulePath(t, root)

	found := wailsImports(t, root, module, filepath.Join(root, "cmd", "server"))
	if len(found) > 0 {
		t.Fatalf("the headless server links the Wails runtime through %d import(s):\n  %s\n\n"+
			"internal/app must stay free of Wails: it is linked into a binary that never "+
			"opens a window, and the runtime drags the whole webview frontend stack with it. "+
			"Inject the side effects instead — app.WithEmitter / app.WithOpener — and keep "+
			"the Wails implementations in the desktop entry point (main.go).",
			len(found), strings.Join(found, "\n  "))
	}
}

// TestWailsImportWalkerFindsTheDesktopsWailsImport is the positive control
// for the walk above. The desktop entry point DOES import the Wails runtime,
// so walking from the module root must find it — without this, the guard
// could pass by walking nothing at all (a wrong module path, an unreadable
// directory, a renamed package).
func TestWailsImportWalkerFindsTheDesktopsWailsImport(t *testing.T) {
	root := moduleRoot(t)
	module := modulePath(t, root)

	found := wailsImports(t, root, module, root)
	if len(found) == 0 {
		t.Fatal("the walk found no Wails import even though the desktop entry point imports the runtime — " +
			"the guard is not walking anything, so its silence proves nothing")
	}
}

// moduleRoot returns the module root directory (two levels up from cmd/server).
func moduleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	return root
}

// modulePath reads the module path from go.mod.
func modulePath(t *testing.T, root string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	t.Fatal("go.mod has no module line")
	return ""
}

// wailsPrefix is the import path prefix of the Wails framework.
const wailsPrefix = "github.com/wailsapp/"

// wailsImports walks the module's own import graph from startDir and returns
// every wailsapp import it reaches, each annotated with the file it came from.
//
// Test files are skipped: they are not linked into the binary, and a test that
// reaches for the runtime to assert on it would not make the server heavier.
func wailsImports(t *testing.T, root, module, startDir string) []string {
	t.Helper()
	var found []string
	visited := map[string]bool{}

	var walk func(dir string)
	walk = func(dir string) {
		if visited[dir] {
			return
		}
		visited[dir] = true

		entries, err := os.ReadDir(dir)
		if err != nil {
			// A directory that cannot be read cannot be imported either.
			return
		}
		fset := token.NewFileSet()
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			file := filepath.Join(dir, name)
			parsed, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parse %s: %v", file, err)
			}
			for _, imp := range parsed.Imports {
				path, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					continue
				}
				if strings.HasPrefix(path, wailsPrefix) {
					found = append(found, path+" (from "+file+")")
					continue
				}
				switch {
				case path == module:
					walk(root)
				case strings.HasPrefix(path, module+"/"):
					sub := filepath.FromSlash(strings.TrimPrefix(path, module+"/"))
					walk(filepath.Join(root, sub))
				}
			}
		}
	}
	walk(startDir)

	return found
}
