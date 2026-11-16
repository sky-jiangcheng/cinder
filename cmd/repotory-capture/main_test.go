package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"repotory/internal/db"
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
	if _, err := db.InitDB(dbPath); err != nil {
		t.Fatalf("InitDB: %v", err)
	}

	args := []string{"-db", dbPath, "-cwd", "/definitely/not/a/cinder/project"}
	if code := run(args); code != 0 {
		t.Errorf("run() without --strict = %d, want 0 (a failed capture never breaks session teardown, ADR-0010)", code)
	}
	if code := run(append(args, "-strict")); code != 1 {
		t.Errorf("run(--strict) = %d, want 1 (the hook falls through to its model-driven fallback)", code)
	}
}
