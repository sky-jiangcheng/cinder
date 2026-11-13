package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"repotory/internal/db"
	"repotory/internal/service"
	"repotory/internal/writerapi"
)

// appExportedMethodNames lists *App's exported methods via reflection.
func appExportedMethodNames() []string {
	out := make([]string, 0, 140)
	typ := reflect.TypeOf(&App{})
	for i := 0; i < typ.NumMethod(); i++ {
		out = append(out, typ.Method(i).Name)
	}
	return out
}

// lifecycleBindings are the three lifecycle names the surface registry
// already carries; they are neither reads nor writes.
var lifecycleBindings = map[string]bool{
	"Startup": true, "Shutdown": true, "Service": true,
}

// serviceWriteMethods parses internal/service's non-test sources and collects
// the exported method names of *Service, so the verb check in
// TestBindingWritesAreComplete runs against the real method list rather than
// a hand-copied one.
func serviceWriteMethods(t *testing.T) map[string]bool {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile))) // internal/app -> internal -> repo root
	svcDir := filepath.Join(root, "internal", "service")
	entries, err := os.ReadDir(svcDir)
	if err != nil {
		t.Fatalf("read service dir: %v", err)
	}
	fset := token.NewFileSet()
	names := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(svcDir, e.Name()), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || !fd.Name.IsExported() {
				continue
			}
			// Receiver must be *Service.
			star, ok := fd.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			if ident, ok := star.X.(*ast.Ident); ok && ident.Name == "Service" {
				names[fd.Name.Name] = true
			}
		}
	}
	return names
}

// svcWrites reports whether the App method's body calls a Service method
// whose name begins with one of the write verbs. The parse is per bindings
// file, keyed by method name — cheap and stable.
func svcWrites(appMethod string, svcNames map[string]bool, verbs []string) bool {
	for _, svcName := range calledServiceMethods(appMethod) {
		if !svcNames[svcName] {
			continue
		}
		for _, v := range verbs {
			if strings.HasPrefix(svcName, v) {
				return true
			}
		}
	}
	return false
}

// calledServiceMethods extracts svc.<Name> calls inside the App method with
// the given name, from the bindings/app sources in this package.
func calledServiceMethods(appMethod string) []string {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		return nil
	}
	out := []string{}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Recv == nil || fd.Name.Name != appMethod {
					continue
				}
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					switch x := sel.X.(type) {
					case *ast.Ident:
						if x.Name == "svc" {
							out = append(out, sel.Sel.Name)
						}
					case *ast.SelectorExpr:
						// a.svc.MoveNote: the receiver field through App.
						if id, ok := x.X.(*ast.Ident); ok && id.Name == "a" && x.Sel.Name == "svc" {
							out = append(out, sel.Sel.Name)
						}
					}
					return true
				})
			}
		}
	}
	return out
}

// newRelayClientManager builds a client-role manager over a real file DB with
// a ghost endpoint published (an address nobody serves) — the headless
// server's "desktop holds the lock but is unreachable" situation.
func newRelayClientManager(t *testing.T, dir, dbPath string) *writerapi.Manager {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "repotory.writer.lock"), []byte("held elsewhere"), 0600); err != nil {
		t.Fatal(err)
	}
	// Hold the real lock via the writer package so TryLock in the manager
	// reports contention.
	lock, err := writerapi.TryLockForTest(dir)
	if err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	database, err := db.InitDB(dbPath)
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	svc := service.New(database, "tester")
	m, err := writerapi.New(svc, writerapi.Options{
		DBPath:           dbPath,
		Process:          "cinder-server",
		Surface:          service.AISurfaceHTTP,
		SelfElectAllowed: false,
		AssertWriter:     false,
	})
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	t.Cleanup(m.Shutdown)
	return m
}

var _ = writerapi.WriteLocal
