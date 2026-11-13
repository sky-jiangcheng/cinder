package platform

import (
	"bytes"
	"encoding/json"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogFileName(t *testing.T) {
	cases := map[string]string{
		"":        "repotory.log",
		"desktop": "repotory.log",
		"mcp":     "repotory-mcp.log",
		"server":  "repotory-server.log",
	}
	for component, want := range cases {
		if got := logFileName(component); got != want {
			t.Errorf("logFileName(%q) = %q, want %q", component, got, want)
		}
	}
}

func TestLogWriterFileSink(t *testing.T) {
	dir := t.TempDir()
	var fallback bytes.Buffer
	w := newLogWriter("mcp", true, dir, &fallback)
	rw, ok := w.(*rotatingFile)
	if !ok {
		t.Fatalf("expected a rotating file sink, got %T", w)
	}
	// Close before the TempDir cleanup runs: on Windows an open file cannot be
	// unlinked, so leaving the handle open fails cleanup with "being used by
	// another process".
	t.Cleanup(func() { _ = rw.Close() })
	if _, err := w.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "repotory-mcp.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello\n" {
		t.Errorf("file = %q", data)
	}
	if fallback.Len() != 0 {
		t.Errorf("fallback must stay unused, got %q", fallback.String())
	}
}

// A file error must degrade to the fallback instead of failing the app.
func TestLogWriterDegradesToFileError(t *testing.T) {
	var fallback bytes.Buffer
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := newLogWriter("server", true, blocked, &fallback)
	if _, err := w.Write([]byte("degraded\n")); err != nil {
		t.Fatal(err)
	}
	if got := fallback.String(); got != "degraded\n" {
		t.Errorf("fallback = %q, want the log line", got)
	}
}

// Rotation keeps the current file plus exactly one backup, never a .2.
func TestRotatingFileKeepsOneBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repotory-test.log")
	var fallback bytes.Buffer
	const max = 512
	rw, err := newRotatingFile(path, max, &fallback)
	if err != nil {
		t.Fatal(err)
	}
	// Close before TempDir cleanup (see TestLogWriterFileSink): an open log file
	// cannot be unlinked on Windows.
	t.Cleanup(func() { _ = rw.Close() })
	line := strings.Repeat("x", 100) + "\n"
	for i := 0; i < 30; i++ {
		if _, err := rw.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("current log missing: %v", err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf("one backup missing: %v", err)
	}
	if _, err := os.Stat(path + ".2"); err == nil {
		t.Error("only one backup must be kept, found .2")
	}
	if info, err := os.Stat(path); err == nil && info.Size() > max+int64(len(line)) {
		t.Errorf("current file grew unbounded: %d bytes", info.Size())
	}
	if fallback.Len() != 0 {
		t.Errorf("rotation degraded to fallback unexpectedly: %q", fallback.String())
	}
}

// slog.SetDefault must bridge the standard log package into the same JSON
// handler, so the existing log.Printf calls become structured records too.
func TestInstallLoggingBridgesStdLog(t *testing.T) {
	oldDefault := slog.Default()
	oldWriter := log.Writer()
	oldFlags := log.Flags()
	t.Cleanup(func() {
		slog.SetDefault(oldDefault)
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
	})

	var buf bytes.Buffer
	installLogging("mcp", false, t.TempDir(), &buf)
	log.Printf("legacy line")
	slog.Info("structured line")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d log lines, want 2 (slog + bridged std log): %q", len(lines), buf.String())
	}
	for _, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("not JSON: %q: %v", line, err)
		}
		if rec["component"] != "mcp" {
			t.Errorf("component attribute missing: %v", rec)
		}
	}
}

// SetupLogging is idempotent and must never install stdout as a log sink: the
// MCP entry point owns stdout for the JSON-RPC protocol.
func TestSetupLoggingIdempotentAndNeverStdout(t *testing.T) {
	oldDefault := slog.Default()
	oldWriter := log.Writer()
	oldFlags := log.Flags()
	t.Cleanup(func() {
		slog.SetDefault(oldDefault)
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
	})

	SetupLogging("mcp", false)
	SetupLogging("server", true) // second call is a no-op, not a panic
	if got := log.Writer(); got == os.Stdout {
		t.Fatal("standard log output must never be stdout")
	}
}
