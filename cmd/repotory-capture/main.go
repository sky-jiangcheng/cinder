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
	"context"
	"database/sql"
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
	// The hook writes a handoff note, so it is inside the single-writer
	// contract (ADR-0058): it usually runs while an IDE holds the role, and a
	// direct INSERT there would either contend with the writer's lock or — with
	// the hook's `|| true` — vanish without a trace, which is the failure mode
	// that let handoffs idle for two weeks. Self-election is allowed so the
	// hook still captures on a machine where no Cinder is running.
	//
	// The election comes BEFORE the database is opened (TODO 13.7): a hook that
	// is not the writer opens the file read-only and writes nothing.
	writerOpts := writerapi.Options{
		DBPath:           path,
		Process:          "repotory-capture",
		Surface:          service.AISurfaceMCP,
		CoreVersion:      version.CoreVersion,
		SelfElectAllowed: true,
	}
	election, err := writerapi.Elect(writerOpts)
	if err != nil {
		recordPending(cwd, fmt.Sprintf("write contract: %v", err))
		log.Fatalf("repotory-capture: write contract: %v", err)
	}
	var database *sql.DB
	if election.Won() {
		database, err = db.InitDB(path)
	} else {
		database, err = db.OpenReader(path)
	}
	if err != nil {
		recordPending(cwd, fmt.Sprintf("open db: %v", err))
		log.Fatalf("repotory-capture: open db: %v", err)
	}
	defer database.Close()
	writerOpts.DB = database
	// The winner's open path did the schema work (InitDB); a client must not
	// do it, and inherits the duty only if it takes the role (ADR-0058).
	writerOpts.SchemaReady = election.Won()
	writerOpts.SchemaVersion = writerapi.CurrentSchemaVersion(database)

	svc := service.New(database, "claude-hook")
	writers, err := writerapi.NewWithElection(svc, writerOpts, election)
	if err != nil {
		recordPending(cwd, fmt.Sprintf("write contract: %v", err))
		log.Fatalf("repotory-capture: write contract: %v", err)
	}
	defer writers.Shutdown()
	svc.SetAuditForwarder(writers.RecordAudit)

	var report service.HandoffResult
	err = writers.ExecuteInto(context.Background(), writerapi.MethodCaptureClaude,
		writerapi.CaptureClaudeArgs{Cwd: cwd}, &report)
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
