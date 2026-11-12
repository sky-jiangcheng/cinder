package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"repotory/internal/db"
	"repotory/internal/platform"
)

// runCLI handles the subcommands that must run WITHOUT the desktop shell.
//
// Restoring a snapshot is the first one: it is the last resort after a
// corruption, it has to work when the app itself is the thing that is broken,
// and Wails would otherwise open a window and a database handle before
// anything got restored. Returns true when the process is done and main should
// not start the UI.
func runCLI(args []string) bool {
	if len(args) == 0 || args[0] != "restore" {
		return false
	}
	os.Exit(restoreCommand(args[1:], os.Stdout, os.Stderr, platform.GetDbPath()))
	return true
}

// restoreCommand copies a verified snapshot to a caller-chosen path
// (ADR-0057 D5): verify the source, write the copy atomically, then print
// where it landed. It is deliberately inert — it never starts the app, never
// opens the live database, and refuses to write over it.
//
// The refusal is the important half. A restore that overwrote the live file
// would be a destructive action taken by a command whose whole premise is "the
// current file is already suspect"; the user moves the restored copy into
// place themselves, once, deliberately.
func restoreCommand(args []string, stdout, stderr io.Writer, livePath string) int {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(stderr)
	from := fs.String("from", "", "snapshot file to restore from")
	to := fs.String("to", "", "path to write the restored database to (must not exist)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: cinder restore --from <snapshot.db> --to <new-path.db>")
		fmt.Fprintln(stderr, "  Writes a verified copy of a snapshot to a new file. The live database is never touched.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *from == "" || *to == "" {
		fs.Usage()
		return 2
	}
	if samePath(*to, livePath) {
		fmt.Fprintf(stderr, "restore: refusing to write over the live database (%s)\n", livePath)
		fmt.Fprintln(stderr, "  restore to another path, close Cinder, then move the file into place yourself.")
		return 1
	}
	n, err := db.RestoreBackup(*from, *to)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "restored %d bytes to %s\n", n, *to)
	fmt.Fprintln(stdout, "  Cinder is not running against this file: close the app before moving it into place.")
	return 0
}

// samePath compares two paths after resolving them, so "./x.db", "x.db" and
// the absolute form all count as the same file.
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return absA == absB
}
