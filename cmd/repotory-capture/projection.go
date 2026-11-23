package main

import (
	"flag"
	"fmt"
	"os"

	"repotory/internal/db"
	"repotory/internal/platform"
	"repotory/internal/service"
	"repotory/internal/service/wikipage"
)

// runProjectPages is the explicit projection refresh (ADR-0069): rebuild the
// approved-only Markdown mirror from the database. The automatic refresh
// rides every page mutation in the desktop; this subcommand is the catch-all
// for the cases no page-level hook sees — a backup restore, a project
// deletion whose cascade removed pages, or a store the desktop never opened.
//
// Like --brief, it opens the database READ-ONLY and calls no model. Unlike
// the automatic refresh it ignores the pages_projection kill switch: running
// the command IS the explicit intent, the switch governs automation only.
func runProjectPages(args []string) int {
	fs := flag.NewFlagSet("repotory-capture project-pages", flag.ExitOnError)
	dbPath := fs.String("db", "", "database path (default: the app's DB)")
	out := fs.String("out", "", "projection directory (default: pages/ next to the database file)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	path := *dbPath
	if path == "" {
		path = platform.GetDbPath()
	}
	database, err := db.OpenReadOnly(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "repotory-capture: project-pages: %v\n", err)
		return 1
	}
	defer database.Close()

	dir := *out
	if dir == "" {
		dir = service.New(database, "pages-projection").PagesProjectionDir()
	}
	report, err := wikipage.ProjectApprovedPages(database, dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "repotory-capture: project-pages: %v\n", err)
		return 1
	}
	fmt.Printf("project-pages: %d approved page(s) -> %s\n", report.Pages, report.Root)
	fmt.Printf("  files=%d skipped=%d dangling_links=%d pruned=%d\n",
		len(report.Files), report.SkippedPages, report.DanglingLinks, len(report.Pruned))
	for _, p := range report.Pruned {
		fmt.Printf("  pruned %s\n", p)
	}
	return 0
}
