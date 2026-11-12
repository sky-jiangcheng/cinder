package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"repotory/internal/writerapi"
)

// This file is the gate ADR-0058's acceptance asks for, in its "must be able to
// go red" form: the single-writer contract only holds if no agent-facing code
// path reaches a database write around the catalogue. The rule is enforced by
// parsing this package's own source rather than by review discipline — the
// repo's stated habit for invariants that "must be remembered" is what keeps
// breaking.
//
// The test: every call `svc.<WriteMethod>` in cmd/mcp is a write performed by
// THIS process. That is only legal on the writer's own code path (which is
// internal/writerapi) or under an explicit, reviewed exemption. A new MCP tool
// that calls a service write directly — the shape every previous lock-contention
// bug started as — fails here, in the reviewer's face, not an agent's.

// writeServiceMethods are the Service methods that write the shared database.
// Reads (GetNote, SearchAll, …) are deliberately absent: they are not what the
// contract arbitrates.
var writeServiceMethods = map[string]bool{
	"CreateNoteWithMeta":        true,
	"UpdateNoteFull":            true,
	"CreateHandoffNote":         true,
	"CaptureClaudeSessionByCwd": true,
	"CreateChatThread":          true,
	"AppendChatMessage":         true,
	"RenameChatThread":          true,
	"ScanNow":                   true,
	"RunReview":                 true,
	"EnsureDefaultScanRoots":    true,
	"MoveNote":                  true,
	"DeleteNote":                true,
	"SetConfigValue":            true,
}

// writePathExemptions: file → why a direct call there is legitimate. Every
// entry must stay true or the gate should red; the reasons are load-bearing.
// Only non-handler startup paths qualify: a tools_*.go file is an agent-facing
// write path by definition and gets no exemption.
var writePathExemptions = map[string]string{
	// Startup seeding, guarded by `if writers.IsWriter()`: only the process
	// that holds the election lock touches the config row, and it is then
	// executing writes by definition.
	"main.go": "startup EnsureDefaultScanRoots under IsWriter()",
}

func TestNoDirectServiceWritesOutsideTheContract(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob package files: %v", err)
	}
	fset := token.NewFileSet()
	var violations []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		// Collect the local names bound to a *service.Service — `svc` today,
		// any alias tomorrow. Matching on the identifier `svc` alone would let
		// a rename step around the gate.
		svcNames := serviceReceivers(f)
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok || !svcNames[ident.Name] {
				return true
			}
			if !writeServiceMethods[sel.Sel.Name] {
				return true
			}
			if reason, exempt := writePathExemptions[name]; exempt && !strings.HasPrefix(name, "tools_") {
				_ = reason // the map entry IS the review record; unused here
				return true
			}
			pos := fset.Position(call.Pos())
			violations = append(violations, pos.String()+" svc."+sel.Sel.Name)
			return true
		})
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Errorf("ADR-0058: MCP handlers must route writes through the catalogue (w.Execute / ExecuteInto), not svc directly:\n  %s",
			strings.Join(violations, "\n  "))
	}
}

// serviceReceivers returns the identifiers in f whose declared type is
// *service.Service (parameters and local variables).
func serviceReceivers(f *ast.File) map[string]bool {
	names := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		var typ ast.Expr
		var id *ast.Ident
		switch d := n.(type) {
		case *ast.Field: // parameter / struct-less var decl in a func signature
			if len(d.Names) == 1 {
				id, typ = d.Names[0], d.Type
			}
		case *ast.ValueSpec:
			if len(d.Names) == 1 && d.Type != nil {
				id, typ = d.Names[0], d.Type
			}
		case *ast.AssignStmt: // svc := ..., and the w := writerapi.New form
			if len(d.Lhs) == 1 && len(d.Rhs) == 1 {
				if lhs, ok := d.Lhs[0].(*ast.Ident); ok && d.Tok == token.DEFINE {
					// `svc := service.New(...)` / `writerapi.New(svc…)` patterns:
					// recognise a constructor call returning *Service by name.
					if ce, ok := d.Rhs[0].(*ast.CallExpr); ok {
						if se, ok := ce.Fun.(*ast.SelectorExpr); ok &&
							se.Sel.Name == "New" {
							if pkg, ok := se.X.(*ast.Ident); ok && pkg.Name == "service" {
								names[lhs.Name] = true
							}
						}
					}
				}
			}
			return true
		}
		if id == nil || typ == nil {
			return true
		}
		star, ok := typ.(*ast.StarExpr)
		if !ok {
			return true
		}
		se, ok := star.X.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pkg, ok := se.X.(*ast.Ident); ok && pkg.Name == "service" && se.Sel.Name == "Service" {
			names[id.Name] = true
		}
		return true
	})
	return names
}

// TestWriteToolsCoveredByCatalogue pins the OTHER direction of the drift:
// catalogue methods that no tool reaches are dead contract surface. The map is
// the reviewed mapping of MCP tool → write catalogue method.
func TestWriteToolsCoveredByCatalogue(t *testing.T) {
	toolToMethod := map[string]string{
		"repotory_notes_create":  "notes.create",
		"repotory_notes_update":  "notes.update",
		"repotory_handoff":       "handoff.create",
		"repotory_chat":          "chat.create+chat.append+chat.rename",
		"repotory_scan":          "scan.run",
		"repotory_review_run":    "review.run",
		"(repotory-capture bin)": "capture.claude_session",
		"(ai audit rows)":        "audit.record",
	}
	covered := map[string]bool{}
	for _, m := range toolToMethod {
		for _, part := range strings.Split(m, "+") {
			covered[part] = true
		}
	}
	for _, name := range writerapi.Methods() {
		if !covered[name] {
			t.Errorf("catalogue method %q is reached by no tool: dead contract surface — delete it or wire it", name)
		}
	}
}
