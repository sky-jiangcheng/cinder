package app

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Frontend↔binding ARITY guard.
//
// Why this file exists. The name check (frontend_contract_test.go) answers
// "does this binding exist?"; it cannot answer "does the frontend call it
// with the right number of arguments?", because the argument list is an
// untyped array on the TS side and a reflect.Call on the Go side.
//
// The Wails runtime is exact about this: boundMethod.ParseArgs refuses a call
// whose argument count differs from the method's parameter count, with
//
//	received 0 arguments to method 'app.App.LatestReviewReport', expected 1
//
// So a filter that is "empty" on one side and "absent" on the other is not a
// type error, not a lint error, and not a test failure anywhere — it is a
// broken button that the user finds by clicking it. That is exactly what
// happened to the code-review page.
//
// The HTTP bridge is lenient (it fills missing arguments with zero values),
// which is why the browser runtime kept working while the desktop app — the
// one the user actually clicks — refused.
//
// This test compares the two sides for EVERY binding call site in
// web/src/api/*.ts. The frontend writes its argument lists as literal arrays
// (`args: [id, date]`), so they can be counted; a call site whose argument
// list is not a literal array is reported as unparseable rather than
// silently skipped.

const frontendAPIDir = "../../web/src/api"

// frontendCallSite is one binding call in the frontend.
type frontendCallSite struct {
	method string
	args   int
	file   string
	line   int
}

// frontendBindingFiles lists the files in web/src/api that CALL bindings.
//
// Two are excluded on purpose:
//
//   - transport.ts is the transport itself. Its `method:` literals are HTTP
//     verbs (`method: 'POST'` on a fetch), and it reaches bindings
//     positionally through wail()/rpc(), not through the `call({ method })`
//     form this test reads.
//   - *.test.ts files assert the contract; they are not the contract.
func frontendBindingFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(frontendAPIDir)
	if err != nil {
		t.Fatalf("read %s: %v", frontendAPIDir, err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".ts") {
			continue
		}
		if strings.HasSuffix(name, ".test.ts") || name == "transport.ts" {
			continue
		}
		out = append(out, filepath.Join(frontendAPIDir, name))
	}
	sort.Strings(out)
	return out
}

// frontendCallSites returns every binding call the frontend makes, with the
// number of arguments it passes.
func frontendCallSites(t *testing.T) []frontendCallSite {
	t.Helper()
	var out []frontendCallSite
	for _, path := range frontendBindingFiles(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		src := frontendInitBlock.ReplaceAllString(string(raw), "")
		matches := frontendMethodCall.FindAllStringSubmatchIndex(src, -1)
		for i, m := range matches {
			name := src[m[2]:m[3]]
			// The call's argument list belongs to this method name: it sits
			// between this name and the next one.
			end := len(src)
			if i+1 < len(matches) {
				end = matches[i+1][0]
			}
			seg := src[m[1]:end]
			args := 0
			if at := strings.Index(seg, "args:"); at >= 0 {
				n, ok := countArrayElements(src[m[1]+at:], end-(m[1]+at))
				if !ok {
					t.Fatalf("%s: the argument list of %s is not a literal array — "+
						"this extractor cannot count it, update the extractor", path, name)
				}
				args = n
			}
			out = append(out, frontendCallSite{
				method: name,
				args:   args,
				file:   filepath.Base(path),
				line:   strings.Count(src[:m[0]], "\n") + 1,
			})
		}
	}
	return out
}

// countArrayElements counts the elements of the array literal that starts in
// src, and reports whether it found one at all.
//
// The counting is bracket- and string-aware: an argument may itself be an
// array, an object literal or a string containing brackets (`args: ['a(b']`),
// and a comma inside one of those is not an argument separator.
func countArrayElements(src string, limit int) (int, bool) {
	start := strings.IndexByte(src, '[')
	if start < 0 || start >= limit {
		return 0, false
	}
	depth := 0
	count := 0
	has := false
	for i := start; i < limit && i < len(src); i++ {
		switch c := src[i]; c {
		case '\'', '"', '`':
			end := skipStringLiteral(src, i)
			if depth == 1 {
				has = true
			}
			i = end - 1
		case '[', '(', '{':
			depth++
			if depth > 1 {
				has = true
			}
		case ']', ')', '}':
			depth--
			if depth == 0 {
				if has {
					count++
				}
				return count, true
			}
		case ',':
			if depth == 1 {
				if has {
					count++
				}
				has = false
			}
		default:
			if depth == 1 && c != ' ' && c != '\t' && c != '\n' && c != '\r' {
				has = true
			}
		}
	}
	return 0, false
}

// skipStringLiteral returns the index of the closing quote of the literal
// that starts at src[start].
func skipStringLiteral(src string, start int) int {
	quote := src[start]
	for i := start + 1; i < len(src); i++ {
		if src[i] == '\\' {
			i++
			continue
		}
		if src[i] == quote {
			return i + 1
		}
	}
	return len(src)
}

// appBindingArity maps every exported *App method to its parameter count.
func appBindingArity() map[string]int {
	typ := reflect.TypeOf(&App{})
	out := make(map[string]int, typ.NumMethod())
	for i := 0; i < typ.NumMethod(); i++ {
		m := typ.Method(i)
		if !m.IsExported() {
			continue
		}
		// For a non-interface type, reflect.Type.Method describes a function
		// WITH the receiver — "func(*App) (T, error)" — so the receiver is
		// one of the inputs and has to come off before the count means
		// "arguments the caller passes".
		out[m.Name] = m.Type.NumIn() - 1
	}
	return out
}

// Every frontend call must pass exactly as many arguments as the binding
// takes.
//
// The failure this catches: a call site that omits an argument the binding
// requires, or passes one it does not. The Wails runtime rejects both with
// "received N arguments to method 'app.App.X', expected M" — at click time,
// in the desktop app, with the HTTP bridge silently papering over it.
func TestFrontendTransportCallArityMatchesBindings(t *testing.T) {
	calls := frontendCallSites(t)
	if len(calls) < 100 {
		t.Fatalf("extracted only %d frontend call sites — the extractor is stale, update it", len(calls))
	}

	arity := appBindingArity()
	var bad []string
	for _, c := range calls {
		want, ok := arity[c.method]
		if !ok {
			bad = append(bad, fmt.Sprintf("%s:%d %s — no such binding on *App", c.file, c.line, c.method))
			continue
		}
		if c.args != want {
			bad = append(bad, fmt.Sprintf("%s:%d %s — passes %d argument(s), the binding takes %d",
				c.file, c.line, c.method, c.args, want))
		}
	}
	if len(bad) > 0 {
		t.Fatalf("%d frontend call(s) do not match their binding signature:\n  %s\n"+
			"The Wails runtime refuses a call whose argument count differs from the method's "+
			"parameter count, and it does so at click time. Pass every argument, "+
			"an empty value included.",
			len(bad), strings.Join(bad, "\n  "))
	}
}
