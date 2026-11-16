package app

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// exportedAppMethods lists every method *App exposes to a binder or to the
// reflective HTTP bridge — the set the registry must cover.
func exportedAppMethods() []string {
	t := reflect.TypeOf(&App{})
	out := make([]string, 0, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		m := t.Method(i)
		if m.IsExported() {
			out = append(out, m.Name)
		}
	}
	sort.Strings(out)
	return out
}

// The registry is the P32 audit. It only earns that name if it cannot go stale,
// so completeness is asserted rather than maintained: add a binding without
// classifying it and this fails.
func TestBindingSurfaceRegistryIsComplete(t *testing.T) {
	methods := exportedAppMethods()

	var missing []string
	for _, m := range methods {
		if _, ok := bindingSurfaces[m]; !ok {
			missing = append(missing, m)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%d exported *App method(s) are not classified in bindingSurfaces: %v\n"+
			"Every binding must be declared mcp-backed (naming its tool), desktop-only "+
			"(with a reason), or lifecycle. That is the P32 audit; it is a table now so it "+
			"cannot silently disagree with the code the way the old comment block could.",
			len(missing), missing)
	}

	var stale []string
	for name := range bindingSurfaces {
		found := false
		for _, m := range methods {
			if m == name {
				found = true
				break
			}
		}
		if !found {
			stale = append(stale, name)
		}
	}
	if len(stale) > 0 {
		t.Fatalf("bindingSurfaces names %d method(s) that no longer exist on *App: %v (delete the entries; a stale audit entry is worse than none)",
			len(stale), stale)
	}
}

// Category rules, so the table cannot be satisfied by empty filler.
func TestBindingSurfaceEntriesAreWellFormed(t *testing.T) {
	for name, s := range bindingSurfaces {
		switch s.Category {
		case SurfaceMCPBacked:
			if s.MCPTool == "" {
				t.Errorf("%s: mcp-backed binding must name its tool", name)
			}
			if !strings.HasPrefix(s.MCPTool, "repotory_") {
				t.Errorf("%s: MCPTool %q is not a repotory_* tool name", name, s.MCPTool)
			}
		case SurfaceDesktopOnly, SurfaceLifecycle:
			if s.MCPTool != "" {
				t.Errorf("%s: %s entry must not name an MCP tool (that is the stale-audit shape): %q",
					name, s.Category, s.MCPTool)
			}
			if len(strings.TrimSpace(s.Reason)) < 20 {
				t.Errorf("%s: %s needs a real reason (>=20 chars), got %q — 'by design' is not a reason",
					name, s.Category, s.Reason)
			}
		default:
			t.Errorf("%s: unknown category %q", name, s.Category)
		}
	}
}

// The human gate is the reason this file exists. If any of these ever becomes
// agent-reachable, the audit has to be edited deliberately, not accidentally.
func TestBindingSurfaceHumanGateHolds(t *testing.T) {
	gated := []string{
		"CompileNote", "CompileProjectNotes", "StartCompileJob",
		"ApproveWikiPage", "RejectWikiPage", "DeleteCompiledPage",
		"PromoteWikiPageToGlobal", "AssignWikiPageProject",
		"MergeWikiPages", "RunCommunitySummary", "FileAnswerAsPage",
		// UpdateConfig was removed from this list on 2026-11-16 by deliberate
		// audit edit: the owner reclassified configuration writes from
		// human-gate to catalogue-covered (ADR-0063) so the IDE panels can
		// save AI provider settings over MCP. UpdateScanRoots stays gated —
		// granting new filesystem scope is a different decision.
		"UpdateScanRoots", "DeleteNote", "RestoreNoteVersion",
		"TriggerKnowledgeImport", "ImportClaudeMemory", "CaptureClaudeHandoff",
		"RebuildEmbeddings",
	}
	for _, name := range gated {
		s, ok := bindingSurfaces[name]
		if !ok {
			t.Fatalf("%s vanished from the registry", name)
		}
		if s.Category != SurfaceDesktopOnly || s.MCPTool != "" {
			t.Fatalf("%s must stay desktop-only with no MCP twin; got %+v", name, s)
		}
	}
}

// Lifecycle methods must be exactly the ones the HTTP bridge refuses. Asserted
// against a literal here rather than by importing httpapi, because importing it
// from this package's test would be an import cycle (httpapi takes an *App).
// httpapi/rpc_test.go pins the same set from its side.
func TestBindingSurfaceLifecycleMatchesExpectation(t *testing.T) {
	want := map[string]bool{"Startup": true, "Shutdown": true, "Service": true}
	got := map[string]bool{}
	for name, s := range bindingSurfaces {
		if s.Category == SurfaceLifecycle {
			got[name] = true
		}
	}
	for name := range want {
		if !got[name] {
			t.Errorf("%s should be classified lifecycle", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("%s is classified lifecycle but is not in the expected set", name)
		}
	}
	// StartAskStream is blocked on /api/rpc for a different reason (it answers over
	// Wails events), so it must be desktop-only, not lifecycle — otherwise the two
	// blocklists drift apart and one of them stops meaning anything.
	if s := bindingSurfaces["StartAskStream"]; s.Category != SurfaceDesktopOnly {
		t.Errorf("StartAskStream must be desktop-only with the streaming reason, got %+v", s)
	}
}

// A quick sanity check on the shape of the registry, so a mass-deletion cannot
// pass the completeness test on an accidentally empty method list.
func TestBindingSurfaceRegistryIsNotEmpty(t *testing.T) {
	methods := exportedAppMethods()
	if len(methods) < 60 {
		t.Fatalf("expected the desktop surface to have ~70 methods, got %d — reflection is broken", len(methods))
	}
	if len(bindingSurfaces) != len(methods) {
		t.Fatalf("registry has %d entries for %d methods", len(bindingSurfaces), len(methods))
	}
}
