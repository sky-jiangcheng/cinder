// Command cinder-review runs the incremental commit review from a terminal.
//
// It is the same pipeline the desktop ticker and the MCP tools use — one
// implementation, three entry points — so a cron/launchd schedule and a
// "review yesterday" click produce byte-identical reports.
//
//	cinder-review                        # yesterday, collected projects
//	cinder-review --since 2026-11-07     # a specific day
//	cinder-review --dry-run --json       # compute, print, write nothing
//	cinder-review --no-ai                # rules only, zero external calls
//
// Exit codes: 0 report produced (even with findings), 1 failure, 2 usage.
// Findings are NOT an error: this is a report, not a gate (ADR-0053 D7).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"repotory/internal/db"
	"repotory/internal/platform"
	"repotory/internal/service"
)

func main() {
	var (
		since   = flag.String("since", "yesterday", "day to review: yesterday or YYYY-MM-DD")
		scope   = flag.String("scope", "", "collected | project:1,2 (default: config)")
		dbPath  = flag.String("db", "", "database path (default: the app's DB)")
		dryRun  = flag.Bool("dry-run", false, "compute the report without writing it")
		noAI    = flag.Bool("no-ai", false, "skip model review even if enabled")
		force   = flag.Bool("force", false, "re-run a day that already completed")
		asJSON  = flag.Bool("json", false, "print the report as JSON")
		sevFlag = flag.String("severity", "", "only show these severities (comma separated)")
	)
	flag.Parse()

	day, err := parseSince(*since)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cinder-review: %v\n", err)
		os.Exit(2)
	}

	path := *dbPath
	if path == "" {
		path = platform.GetDbPath()
	}
	database, err := db.InitDB(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cinder-review: open db: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	svc := service.New(database, "review-cli")
	result, err := svc.RunReview(context.Background(), service.ReviewOptions{
		Date:   day,
		Scope:  *scope,
		DryRun: *dryRun,
		Force:  *force,
		NoAI:   *noAI,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cinder-review: %v\n", err)
		os.Exit(1)
	}

	if result.AlreadyDone {
		fmt.Fprintf(os.Stderr, "cinder-review: %s already reviewed (use --force to re-run)\n",
			result.Run.RunDate)
		return
	}

	severities := splitList(*sevFlag)
	findings := displayedFindings(svc, result, severities)

	if *asJSON {
		out, err := json.MarshalIndent(map[string]any{
			"run":      result.Run,
			"findings": findings,
			"dry_run":  result.DryRun,
		}, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "cinder-review: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(out))
		return
	}

	printReport(result, findings)
}

// displayedFindings prefers the stored rows (they carry ids) and falls back to
// the in-memory result on a dry run, applying the severity filter itself
// because a dry run never reaches the database query that would do it. The
// fallback goes through service.StoredPreview — the one conversion both this
// CLI and the MCP tool use, so a preview can never render a different shape
// than a report.
func displayedFindings(svc *service.Service, result *service.ReviewResult, severities []string) []db.ReviewFinding {
	if !result.DryRun {
		_, stored, err := svc.ReviewReport(result.Run.RunDate, result.Run.Scope, severities, nil)
		if err == nil {
			return stored
		}
	}
	seen := map[string]bool{}
	for _, s := range severities {
		seen[s] = true
	}
	all := service.StoredPreview(result.Findings)
	if len(seen) == 0 {
		return all
	}
	out := make([]db.ReviewFinding, 0, len(all))
	for _, f := range all {
		if seen[f.Severity] {
			out = append(out, f)
		}
	}
	return out
}

// printReport renders the human view: counters first (so "nothing to report"
// is distinguishable from "the job did not run"), then findings worst-first.
func printReport(result *service.ReviewResult, findings []db.ReviewFinding) {
	run := result.Run
	label := run.RunDate
	if result.DryRun {
		label += " (dry run)"
	}
	fmt.Printf("review %s · scope %s · %d repos · %d commits · %d files\n",
		label, run.Scope, run.Repos, run.Commits, run.Files)
	fmt.Printf("candidates %d · stored %d · skipped %d · ai calls %d\n",
		run.Candidates, len(findings), run.Skipped, run.AICalls)

	if len(findings) == 0 {
		fmt.Println("no findings")
		return
	}
	for _, f := range findings {
		loc := f.FilePath
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.FilePath, f.Line)
		}
		fmt.Printf("\n[%s/%s] %s\n  %s\n", strings.ToUpper(f.Severity), f.Verdict, f.Rule, loc)
		fmt.Printf("  %s\n", f.Message)
		if f.Suggestion != "" {
			fmt.Printf("  → %s\n", f.Suggestion)
		}
		if f.Evidence != "" {
			for _, line := range strings.Split(f.Evidence, "\n") {
				fmt.Printf("    %s\n", line)
			}
		}
	}
}

// parseSince accepts "yesterday" or an explicit YYYY-MM-DD.
func parseSince(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "", "yesterday":
		return time.Now().AddDate(0, 0, -1), nil
	case "today":
		return time.Now(), nil
	}
	t, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("bad --since %q: want yesterday or YYYY-MM-DD", s)
	}
	return t, nil
}

func splitList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
