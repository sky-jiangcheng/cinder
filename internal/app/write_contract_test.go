package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"repotory/internal/writerapi"
)

// TestBindingWritesAreComplete is the drift gate for bindingWrites (ADR-0058 /
// TODO 13.6): the registry is hand-reviewed, so it can go stale in two
// directions. This test catches both mechanically.
//
//  1. A binding method that is MISSING from the registry: caught by scanning
//     the service layer's write entry points. The proxy is deliberately
//     name-based — every App write binding delegates to a Service method whose
//     name begins with a write verb, so a new write binding whose service call
//     does not match a known verb still needs a human decision, but it will
//     surface here as an unclassified candidate instead of slipping through.
//  2. A registry entry that no longer names an App method: the registry lies.
func TestBindingWritesAreComplete(t *testing.T) {
	appType := appMethodNames(t)

	// Direction 2: every entry names a real method.
	for name := range bindingWrites {
		if !appType[name] {
			t.Errorf("bindingWrites names %q, but App has no such method — the registry lies", name)
		}
	}

	// Direction 1: every App method calling a write-shaped Service method is
	// classified. The verbs are the reviewed write-entry-point shapes; a
	// service method that writes but is named like a read (deliberately, e.g.
	// CaptureClaudeHandoff) must be added to the registry by hand, and the
	// reviewer list below documents the ones that exist today.
	writeVerbs := []string{
		"Create", "Update", "Delete", "Set", "Move", "Pin", "Restore", "Toggle",
		"Reorder", "Approve", "Reject", "Promote", "Assign", "Merge", "Run",
		"Start", "Cancel", "Trigger", "Import", "Rebuild", "Refresh", "Capture",
		"File", "Bind", "Reassign", "Install", "Setup", "Compile", "Generate",
		"Export", "Eject", "Open", "Reveal",
	}
	// Bindings allowed to call write-verb service methods without being in
	// bindingWrites — each with its reason, so the list is the review record.
	exempt := map[string]string{
		// Returns an AI proposal; the save is the human's repotory_notes_update.
		"OrganizeNote": "writes nothing (ADR-0015 proposal tool)",
		// One GET against GitHub; persists nothing (state is only written when
		// a download starts).
		"CheckUpdate": "pure read",
		// Assembles a string from the DB; no write.
		"GenerateLLMsTxt": "pure read",
		// Inspects a candidate backup directory; no write.
		"CheckBackupCopyTarget": "pure read",
		// OS affordances: no DB write (download state travels through
		// StartUpdateDownload / UpdateDownloadState / CancelUpdateDownload).
		"OpenBrowserURL":         "no DB write",
		"OpenDownloadedUpdate":   "no DB write",
		"RevealDownloadedUpdate": "no DB write",
		"EjectInstallerVolume":   "no DB write",
		// Streams to the frontend over Wails events; blocked on the bridge.
		"StartAskStream": "no DB write (Wails events)",
		// String assemblers: they read the DB and format; the FILE write (if
		// any) happens in a native save dialog on the frontend side.
		"ExportNoteAsMarkdown": "assembles a string; no DB write",
		"ExportMemoryJSON":     "assembles a string; no DB write",
		// Reads install paths; the verb matcher tripped on InstallInfo's name.
		"GetInstallInfo": "pure read",
		// Reads another tool's transcript — but the write it drives is
		// CreateHandoffNote, and the binding is classified; the read itself
		// carries no DB write. Listed here because its service name starts
		// with a read-shaped verb — this exemption documents that the
		// classifier looked at it.
		"CaptureClaudeHandoff": "write via handoff.create; classified",
		// Same: the import triggers write through the importers; they are
		// classified, and their names match the verb list — no exemption
		// actually needed, kept for symmetry with the reviewer notes.
	}

	svcNames := serviceWriteMethods(t)
	for name := range appType {
		if _, isLifecycle := lifecycleBindings[name]; isLifecycle {
			continue
		}
		if _, classified := bindingWrites[name]; classified {
			continue
		}
		if _, ok := exempt[name]; ok {
			continue
		}
		if svcWrites(name, svcNames, writeVerbs) {
			t.Errorf("App.%s appears to execute a write (its service call matches a write verb) but is not in bindingWrites — classify it or exempt it with a reason", name)
		}
	}
}

// TestBindingWritesRouteDecidesCorrectly pins the DecideWrite behaviour the
// RPC bridge relies on, against the fixture's contract.
func TestBindingWritesRouteDecidesCorrectly(t *testing.T) {
	// The MCP-twin cross-check map stays in sync with the registry itself:
	// every entry names a tool, none is empty (the same invariant cmd/mcp's
	// registry test asserts from the other side).
	for binding, tool := range mcpBackedBindings() {
		if tool == "" {
			t.Errorf("mcpBackedBindings[%q] has an empty tool name", binding)
		}
	}
	if len(mcpBackedBindings()) == 0 {
		t.Fatal("mcpBackedBindings is empty: the MCP twin cross-check lost its data source")
	}

	// The exported lookup the bridge calls: a write, a read, a lifecycle name.
	if !BindingIsWrite("ApproveWikiPage") {
		t.Fatal("ApproveWikiPage must classify as a write")
	}
	if BindingIsWrite("GetProjects") {
		t.Fatal("GetProjects must not classify as a write")
	}
	if BindingIsWrite("Startup") {
		t.Fatal("lifecycle names are not in the write registry")
	}

	dir := t.TempDir()
	// Build a manager in client role against a ghost writer, exactly like the
	// headless server that starts while a desktop runs but is unreachable
	// (the fixture's TestUnreachableWriterIsActionableError shape).
	m := relayClientManager(t, dir)

	// Catalogue-covered write: forward.
	if got := m.DecideWrite("CreateNoteWithMeta"); got != writerapi.WriteForward {
		t.Errorf("CreateNoteWithMeta = %v, want WriteForward", got)
	}
	// Human-gate write: refuse (the desktop owns the gate).
	for _, name := range []string{"ApproveWikiPage", "MergeWikiPages", "UpdateConfig", "MoveNote"} {
		if got := m.DecideWrite(name); got != writerapi.WriteRefuse {
			t.Errorf("%s = %v, want WriteRefuse from a client role", name, got)
		}
		if err := m.GuardedLocal(name); err == nil {
			t.Errorf("GuardedLocal(%s) succeeded from a client role; it must refuse", name)
		} else if !strings.Contains(err.Error(), "Cinder Desktop") {
			t.Errorf("GuardedLocal(%s) error is not actionable: %v", name, err)
		}
	}
}

// appMethodNames reflects the exported methods of *App for the completeness
// direction. Reflection over the type keeps this in sync with the compiler.
func appMethodNames(t *testing.T) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	for _, name := range appExportedMethodNames() {
		names[name] = true
	}
	return names
}

// relayClientManager builds a client-role manager against a ghost writer
// endpoint (a published address nobody serves), mirroring the server's
// "desktop runs but is unreachable" situation.
func relayClientManager(t *testing.T, dir string) *writerapi.Manager {
	t.Helper()
	return newRelayClientManager(t, dir, filepath.Join(dir, "relay.db"))
}

var _ = os.Getpid
