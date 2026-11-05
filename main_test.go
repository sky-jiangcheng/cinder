package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// The desktop app must not become resident again.
//
// Why this file exists. The resident build (ADR-0045) shipped a desktop app
// that could not be quit and could not be brought back: OnBeforeClose
// intercepted the close and returned true, so every quit path — the window's
// close button, Cmd+Q, the Dock's Quit — was vetoed; the close itself hid the
// window with WindowHide, which orders the window out of the screen list, so
// the Dock icon had nothing to bring back (Wails v2 has no
// applicationShouldHandleReopen). ADR-0047 decision 1 removed the residency:
// closing the window ends the process, and the Dock's Quit works because
// nothing intercepts it.
//
// Nothing stops the next person from adding a "close but keep running" hook
// back — it looks like a reasonable feature ("keep the bot warm while the
// user browses the web"), and it is exactly what produced the bug above. The
// desktop app has one main window and one window-lifecycle story; this test
// pins it.
//
// The check parses main.go with go/parser rather than grepping text: a
// commented-out OnBeforeClose or a mention inside a string would otherwise
// decide whether the gate is red.
func TestDesktopAppMustNotBecomeResident(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("main.go could not be parsed: %v", err)
	}

	var violations []string
	appLiteralFound := false

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CompositeLit:
			sel, ok := node.Type.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			switch {
			case pkg.Name == "options" && sel.Sel.Name == "App":
				appLiteralFound = true
				violations = append(violations, compositeKeys(node, fset)...)
			case pkg.Name == "options" && sel.Sel.Name == "SingleInstanceLock":
				violations = append(violations, "options.SingleInstanceLock{...} is set")
			case pkg.Name == "mac" && sel.Sel.Name == "Options":
				violations = append(violations, compositeKeys(node, fset)...)
			}
		case *ast.CallExpr:
			sel, ok := node.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if !strings.HasSuffix(pkg.Name, "runtime") {
				return true
			}
			switch sel.Sel.Name {
			case "WindowHide", "WindowMinimise":
				violations = append(violations, pkg.Name+"."+sel.Sel.Name+"() is called")
			}
		}
		return true
	})

	if !appLiteralFound {
		t.Fatal("no options.App literal was found in main.go — the guard is not looking at " +
			"the desktop entry point, so its silence proves nothing")
	}

	if len(violations) > 0 {
		t.Fatalf("the desktop app is resident again (ADR-0047 decision 1):\n  %s\n\n"+
			"Closing the window must end the process. A resident app cannot be quit "+
			"(OnBeforeClose vetoes every quit path) and cannot be brought back "+
			"(the hidden window is ordered out of the screen list, and Wails v2 has no "+
			"applicationShouldHandleReopen to bring it back). The resident assistant is a "+
			"separate product now — see ADR-0047.",
			strings.Join(violations, "\n  "))
	}
}

// compositeKeys returns the field names of a composite literal that are on the
// forbidden list, with the position they were found at.
func compositeKeys(lit *ast.CompositeLit, fset *token.FileSet) []string {
	forbidden := map[string]string{
		"OnBeforeClose":      "OnBeforeClose intercepts the close and vetoes every quit path",
		"SingleInstanceLock": "SingleInstanceLock hands the second launch to a first instance that may have no visible window",
		"HideWindowOnClose":  "HideWindowOnClose hides the app instead of ending it",
	}

	var out []string
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		why, banned := forbidden[key.Name]
		if !banned {
			continue
		}
		out = append(out, fset.Position(kv.Pos()).String()+" sets "+key.Name+" — "+why)
	}
	return out
}
