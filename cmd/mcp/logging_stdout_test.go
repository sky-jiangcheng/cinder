package main

import (
	"io"
	"log"
	"log/slog"
	"os"
	"strings"
	"testing"

	"repotory/internal/platform"
)

// MCP speaks JSON-RPC on stdout; a single log line there corrupts the protocol
// stream. SetupLogging only ever targets a per-component file or os.Stderr.
// This test pins that by swapping stdout for a pipe and writing probes through
// both logging APIs: none of the sentinel text may appear on stdout.
func TestMCPLoggingReservesStdout(t *testing.T) {
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = orig
		_ = r.Close()
	})

	// toFile=false uses os.Stderr, avoiding a real log file in the user dir.
	platform.SetupLogging("mcp", false)
	log.Printf("STDOUT_SENTINEL_9f3a_stdlog")
	slog.Info("STDOUT_SENTINEL_9f3a_slog")

	_ = w.Close()
	os.Stdout = orig
	out, _ := io.ReadAll(r)
	if strings.Contains(string(out), "STDOUT_SENTINEL_9f3a") {
		t.Fatalf("log output leaked onto stdout (the MCP protocol channel): %q", out)
	}
}
