package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"repotory/internal/agentinit"
)

// The init / uninstall subcommands (ADR-0067) host the agentinit wiring on the
// binary every agent-side install already carries, so "one command wires every
// client" needs no new release asset. They dispatch before the capture flag
// set: `cinder-capture init --all` is a setup command, not a session capture,
// and it never opens the database — claude_session_capture stays default-off
// (ADR-0010); installing the hooks does not enable transcript reading.

func runInit(args []string) int {
	fs := flag.NewFlagSet("repotory-capture init", flag.ExitOnError)
	all := fs.Bool("all", false, "wire everything: all clients + session hooks + AGENTS.md/CLAUDE.md block")
	clientFlag := fs.String("client", "", "comma-separated subset of claude,cursor,vscode,windsurf (default: all)")
	binFlag := fs.String("bin", "", "explicit path to the cinder-mcp binary (skips detection)")
	withHook := fs.Bool("with-hook", false, "also install the Claude Code session hooks (SessionEnd handoff + SessionStart brief)")
	withInstructions := fs.Bool("with-instructions", false, "also write the managed block into AGENTS.md / CLAUDE.md")
	cwdFlag := fs.String("cwd", "", "project directory to wire (default: the working directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	target, err := initTargetDir(*cwdFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "repotory-capture init: %v\n", err)
		return 1
	}
	clients, err := parseClientList(*clientFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "repotory-capture init: %v\n", err)
		return 2
	}

	bin, err := agentinit.FindBinary(*binFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, `repotory-capture init: %v

Install the MCP server first (brew install cinder-mcp, scoop install cinder-mcp,
or download from https://github.com/sky-jiangcheng/cinder/releases), then re-run
with -bin /path/to/cinder-mcp.`, err)
		return 1
	}

	report, err := agentinit.Register(agentinit.Options{
		TargetDir:        target,
		BinPath:          bin,
		Clients:          clients,
		WithHook:         *withHook || *all,
		WithInstructions: *withInstructions || *all,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "repotory-capture init: %v\n", err)
		return 1
	}
	fmt.Printf("repotory-capture init: wiring %s\nMCP binary: %s\n", target, report.Binary)
	return printInitReport(report)
}

func runUninstall(args []string) int {
	fs := flag.NewFlagSet("repotory-capture uninstall", flag.ExitOnError)
	clientFlag := fs.String("client", "", "comma-separated subset of claude,cursor,vscode,windsurf (default: all)")
	cwdFlag := fs.String("cwd", "", "project directory to unwire (default: the working directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	target, err := initTargetDir(*cwdFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "repotory-capture uninstall: %v\n", err)
		return 1
	}
	clients, err := parseClientList(*clientFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "repotory-capture uninstall: %v\n", err)
		return 2
	}

	report, err := agentinit.Unregister(agentinit.Options{TargetDir: target, Clients: clients})
	if err != nil {
		fmt.Fprintf(os.Stderr, "repotory-capture uninstall: %v\n", err)
		return 1
	}
	fmt.Printf("repotory-capture uninstall: unwiring %s\n", target)
	code := printInitReport(report)
	fmt.Println("The database, notes and binaries are untouched — only the client wiring was removed.")
	return code
}

// initTargetDir resolves the project directory. Unlike the capture path this
// never reads stdin: init is a command the user types, not a hook invocation,
// and a piped stdin must not silently redirect where the configs land.
func initTargetDir(flagVal string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("cannot determine the working directory: %w", err)
	}
	return wd, nil
}

func parseClientList(raw string) ([]agentinit.Client, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil // agentinit's "all clients" default
	}
	var out []agentinit.Client
	for _, name := range strings.Split(raw, ",") {
		c, err := agentinit.ParseClient(name)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// printInitReport renders one machine status per file. A failed entry makes
// the command exit 1; warnings alone (e.g. a user-modified hook script left in
// place) do not — they are information, not a broken uninstall.
func printInitReport(report *agentinit.Report) int {
	failed := false
	printFile := func(f agentinit.FileResult) {
		line := fmt.Sprintf("  %-8s %s: %s", f.Client, f.Path, f.Status)
		if f.Note != "" {
			line += " (" + f.Note + ")"
		}
		fmt.Println(line)
		if f.Status == agentinit.StatusFailed {
			failed = true
		}
	}
	for _, f := range report.Files {
		printFile(f)
	}
	for _, f := range []*agentinit.FileResult{report.Hook, report.BriefHook, report.HookSettings} {
		if f != nil {
			printFile(*f)
		}
	}
	for _, f := range report.Instructions {
		printFile(f)
	}
	for _, w := range report.Warnings {
		fmt.Fprintf(os.Stderr, "  warning: %s\n", w)
	}
	if failed {
		return 1
	}
	return 0
}
