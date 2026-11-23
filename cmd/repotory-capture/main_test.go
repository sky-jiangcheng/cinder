package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"repotory/internal/db"
	"repotory/internal/service"
)

// resolveCwd has a documented precedence: -cwd flag, then a {"cwd": "..."}
// JSON object on stdin (what Claude Code feeds a SessionEnd hook), then the
// process working directory. Getting the order wrong silently captures the
// wrong project — the hook target's whole job is resolving the right cwd.

func TestResolveCwdFlagWins(t *testing.T) {
	got := resolveCwd("/explicit/flag/path")
	if got != "/explicit/flag/path" {
		t.Errorf("flag should win: got %q", got)
	}
}

func TestResolveCwdFallsBackToWorkingDir(t *testing.T) {
	// No flag, no piped stdin (go test runs with /dev/null or a pipe on
	// stdin; either way there is no {"cwd": ...} object to parse).
	got := resolveCwd("")
	wd, _ := os.Getwd()
	if got == "" {
		t.Fatal("expected the process working dir, got empty string")
	}
	if !strings.HasSuffix(filepath.Clean(got), filepath.Base(filepath.Clean(wd))) {
		t.Errorf("cwd %q does not look like the test working dir %q", got, wd)
	}
}

// The stdin shape is dictated by Claude Code hooks: a JSON object with a "cwd"
// field. These cases go through the real os.Stdin path, so each subtest swaps
// the file in, runs resolveCwd, and restores it. Where stdin yields nothing,
// resolveCwd falls through to the process working dir, so expectations use
// that as the fallback value.
func TestResolveCwdStdinVariants(t *testing.T) {
	origStdin := os.Stdin
	t.Cleanup(func() { os.Stdin = origStdin })
	wd, _ := os.Getwd()

	swapStdin := func(t *testing.T, content string) {
		t.Helper()
		f, err := os.CreateTemp(t.TempDir(), "stdin")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(content); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.Close() })
		os.Stdin = f
	}

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"hook json object", `{"cwd": "/hook/provided/path"}`, "/hook/provided/path"},
		// Junk / unusable stdin falls through to the process working dir.
		{"junk on stdin falls back to wd", "not json at all", wd},
		{"empty cwd field falls back to wd", `{"cwd": ""}`, wd},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			swapStdin(t, tt.content)
			if got := resolveCwd(""); got != tt.want {
				t.Errorf("resolveCwd() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A malformed stdin object must not crash the hook: Claude Code tears down the
// session regardless, and a panic here would be printed into the user's
// terminal on every session end. A truncated object falls through to the
// process working dir.
func TestResolveCwdStdinTruncatedJSONDoesNotPanic(t *testing.T) {
	origStdin := os.Stdin
	t.Cleanup(func() { os.Stdin = origStdin })
	wd, _ := os.Getwd()

	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"cwd": "unterminated`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	os.Stdin = f

	if got := resolveCwd(""); got != wd {
		t.Errorf("truncated JSON should fall back to the working dir %q, got %q", wd, got)
	}
}

// The SessionEnd hook's fallback decision (ADR-0054 D3) depends on one bit:
// did the capture write a note or not. run() must turn that bit into the exit
// code ONLY under --strict — without it the CLI keeps ADR-0010's exit-0
// "never break session teardown" behavior. The failure here is the real one a
// hook sees most often: a cwd that resolves to no Cinder project at all.
//
// HOME is redirected for the whole test: recordPending writes the durable
// failure trace into the platform data dir, which must land in the test's
// temp tree rather than the developer's real config directory.
func TestRunStrictExitCodeObservesCaptureFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dbPath := filepath.Join(t.TempDir(), "capture-test.db")
	// The schema-creating handle is closed right away: run() opens its own
	// per-invocation handle, and an extra open handle would keep the file
	// locked — Windows cannot RemoveAll a file another process holds, so
	// t.TempDir cleanup would fail the test after its assertions passed.
	database, err := db.InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close schema handle: %v", err)
	}

	args := []string{"-db", dbPath, "-cwd", "/definitely/not/a/cinder/project"}
	if code := run(args); code != 0 {
		t.Errorf("run() without --strict = %d, want 0 (a failed capture never breaks session teardown, ADR-0010)", code)
	}
	if code := run(append(args, "-strict")); code != 1 {
		t.Errorf("run(--strict) = %d, want 1 (the hook falls through to its model-driven fallback)", code)
	}
}

// captureStdout swaps os.Stdout for a temp file around fn and returns what
// was printed — the brief's whole output contract is stdout, because Claude
// Code injects SessionStart stdout into the session context.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = f
	defer func() { os.Stdout = orig }()
	fn()
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	return string(out)
}

// The SessionStart path (ADR-0066) must print the brief for the cwd's project
// and leave the database file byte-identical: it opens read-only, joins no
// writer contract, and writes no pending record.
func TestRunBriefPrintsHandoffAndNeverWrites(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	root := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "brief-test.db")
	database, err := db.InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	svc := service.New(database, "test")
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := db.SyncProjectTx(tx, "demo", root, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateHandoffNote(service.HandoffInput{
		ProjectID: pid, Summary: "brief cli test", Gotchas: []string{"BRIEF-CLI-MARK"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	var code int
	out := captureStdout(t, func() {
		code = run([]string{"-brief", "-db", dbPath, "-cwd", root})
	})
	if code != 0 {
		t.Errorf("run(--brief) = %d, want 0", code)
	}
	if !strings.Contains(out, "BRIEF-CLI-MARK") {
		t.Errorf("brief stdout missing the handoff gotcha:\n%s", out)
	}
	after, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("the brief path modified the database file; it must be read-only")
	}
}

// A machine with no database yet (fresh install, hook already wired) must get
// a silent exit 0 — and the brief must NOT create the database: InitDB's
// create-on-open behavior is exactly what the read-only opener exists to
// avoid on a session-start path.
func TestRunBriefMissingDBIsSilentAndCreatesNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dbPath := filepath.Join(t.TempDir(), "does-not-exist.db")

	var code int
	out := captureStdout(t, func() {
		code = run([]string{"-brief", "-db", dbPath, "-cwd", t.TempDir()})
	})
	if code != 0 {
		t.Errorf("run(--brief) with missing db = %d, want 0 (session start is never broken)", code)
	}
	if out != "" {
		t.Errorf("expected silent stdout, got:\n%s", out)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Errorf("the brief path created the database file (stat err = %v)", err)
	}
}
