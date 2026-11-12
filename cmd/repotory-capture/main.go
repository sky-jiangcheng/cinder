// Command repotory-capture is the B-end Claude Code SessionEnd hook target
// (ADR-0010): it captures the just-closed session as a handoff note.
//
// Claude Code runs a SessionEnd hook as an external command with the session's
// working directory. Resolve cwd from (in order): -cwd flag, a JSON object on
// stdin with a "cwd" field, then the process's own working dir. Then resolve the
// matching Cinder project and run the same capture the desktop button does.
//
// It is inert unless `claude_session_capture=1` AND the project matches — so a
// hook installed but not enabled is a harmless no-op, and reading transcripts
// never happens implicitly (ADR-0010 决策 1/2).
//
// A failed capture writes stderr (the hook no longer discards it) AND a durable
// pending record in the platform data dir, never the knowledge layer; the
// next repotory_context surfaces it. Failures never break session teardown.
//
// Hook config (via scripts/repotory-init or manually):
//
//	"SessionEnd": [{ "hooks": [{ "type": "command", "command": "repotory-capture" }] }]
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"repotory/internal/db"
	"repotory/internal/platform"
	"repotory/internal/service"
)

func main() {
	cwdFlag := flag.String("cwd", "", "session working directory (default: stdin .cwd, else os.Getwd)")
	dbPath := flag.String("db", "", "database path (default: the app's DB)")
	flag.Parse()

	cwd := resolveCwd(*cwdFlag)
	if cwd == "" {
		recordPending("", "cannot determine session cwd")
		log.Fatalf("repotory-capture: cannot determine session cwd")
	}

	path := *dbPath
	if path == "" {
		path = platform.GetDbPath()
	}
	database, err := db.InitDB(path)
	if err != nil {
		recordPending(cwd, fmt.Sprintf("open db: %v", err))
		log.Fatalf("repotory-capture: open db: %v", err)
	}
	defer database.Close()

	svc := service.New(database, "claude-hook")
	report, err := svc.CaptureClaudeSessionByCwd(cwd)
	if err != nil {
		// Disabled-by-config or no-matching-project are expected, non-fatal for a
		// hook; still surface them and leave a pending record, but exit 0 so a
		// failed capture never breaks the user's Claude session teardown.
		fmt.Fprintf(os.Stderr, "repotory-capture: %v\n", err)
		recordPending(cwd, err.Error())
		return
	}
	platform.ClearCapturePending()
	fmt.Printf("repotory-capture: wrote handoff note %d (%q)\n", report.NoteID, report.Title)
}

// recordPending leaves a durable trace of a failed capture; stderr alone is
// lost once Claude Code tears the session down.
func recordPending(cwd, reason string) {
	if err := platform.AppendCapturePending(cwd, reason); err != nil {
		fmt.Fprintf(os.Stderr, "repotory-capture: could not record pending capture: %v\n", err)
	}
}

// resolveCwd prefers the flag, then a {"cwd": "..."} stdin object (Claude Code
// feeds hook data on stdin), then the process working dir.
func resolveCwd(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if fi, err := os.Stdin.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) == 0 {
		if data, err := io.ReadAll(bufio.NewReader(os.Stdin)); err == nil && len(data) > 0 {
			var p struct {
				Cwd string `json:"cwd"`
			}
			if json.Unmarshal(data, &p) == nil && p.Cwd != "" {
				return p.Cwd
			}
		}
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return ""
}
