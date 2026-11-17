// Command repotory-capture is the B-end SessionEnd hook target (ADR-0010) and
// the one-shot capture CLI for every agent store Cinder can read: Claude Code
// (default), WorkBuddy, Qoder, Trae CN, DeepSeek Harness (DSH), or whichever
// store has the newest session for the cwd (--agent auto).
//
// Claude Code runs a SessionEnd hook as an external command with the session's
// working directory. Resolve cwd from (in order): -cwd flag, a JSON object on
// stdin with a "cwd" field, then the process's own working dir. Then resolve the
// matching Cinder project and run the same capture the desktop button does.
// Other agents have no hook mechanism, so their capture is one-shot by
// invocation: `repotory-capture --agent qoder --cwd <dir>` after a session.
//
// It is inert unless `claude_session_capture=1` AND the project matches — so a
// hook installed but not enabled is a harmless no-op, and reading transcripts
// never happens implicitly (ADR-0010 决策 1/2).
//
// A failed capture writes stderr (the hook no longer discards it) AND a durable
// pending record in the platform data dir, never the knowledge layer; the
// next repotory_context surfaces it. Failures never break session teardown.
//
// --strict is the hook's contract, not a user-facing default: it turns a
// capture that wrote nothing (config off, no matching project) into a non-zero
// exit so the SessionEnd hook's `if ... && repotory-capture --strict` falls
// through to its model-driven fallback. Without it the CLI keeps ADR-0010's
// "never break session teardown" exit-0 behavior.
//
// Hook config (via scripts/repotory-init or manually):
//
//	"SessionEnd": [{ "hooks": [{ "type": "command", "command": "repotory-capture" }] }]
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"repotory/internal/db"
	"repotory/internal/platform"
	"repotory/internal/service"
	"repotory/internal/version"
	"repotory/internal/writerapi"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run is the body of the CLI, split out so the exit code is testable and the
// deferred writer/database cleanup always runs before the process exits with
// the code this returns (os.Exit would skip the defers).
func run(args []string) int {
	fs := flag.NewFlagSet("repotory-capture", flag.ExitOnError)
	cwdFlag := fs.String("cwd", "", "session working directory (default: stdin .cwd, else os.Getwd)")
	agentFlag := fs.String("agent", "claude", "agent session store to read: claude, workbuddy, qoder, trae, dsh, or auto (newest across all)")
	dbPath := fs.String("db", "", "database path (default: the app's DB)")
	strict := fs.Bool("strict", false, "exit non-zero when no handoff note was written (for the SessionEnd hook's fallback)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

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
	// The hook writes a handoff note, so it is inside the single-writer
	// contract (ADR-0058): it usually runs while an IDE holds the role, and a
	// direct INSERT there would either contend with the writer's lock or — with
	// the hook's `|| true` — vanish without a trace, which is the failure mode
	// that let handoffs idle for two weeks. Self-election is allowed so the
	// hook still captures on a machine where no Cinder is running.
	writers, err := writerapi.New(svc, writerapi.Options{
		DBPath:           path,
		Process:          "repotory-capture",
		Surface:          service.AISurfaceMCP,
		CoreVersion:      version.CoreVersion,
		SchemaVersion:    writerapi.CurrentSchemaVersion(database),
		SelfElectAllowed: true,
	})
	if err != nil {
		recordPending(cwd, fmt.Sprintf("write contract: %v", err))
		log.Fatalf("repotory-capture: write contract: %v", err)
	}
	defer writers.Shutdown()
	svc.SetAuditForwarder(writers.RecordAudit)

	var report service.HandoffResult
	err = writers.ExecuteInto(context.Background(), writerapi.MethodCaptureClaude,
		writerapi.CaptureClaudeArgs{Cwd: cwd, Agent: *agentFlag}, &report)
	if err != nil {
		// Disabled-by-config or no-matching-project are expected, non-fatal for a
		// hook; still surface them and leave a pending record. The default exit
		// code stays 0 so a failed capture never breaks the user's Claude session
		// teardown (ADR-0010); --strict lets the hook distinguish "wrote nothing"
		// and fall through to its model-driven fallback.
		fmt.Fprintf(os.Stderr, "repotory-capture: %v\n", err)
		recordPending(cwd, err.Error())
		if *strict {
			return 1
		}
		return 0
	}
	platform.ClearCapturePending()
	fmt.Printf("repotory-capture: wrote handoff note %d (%q)\n", report.NoteID, report.Title)
	return 0
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
