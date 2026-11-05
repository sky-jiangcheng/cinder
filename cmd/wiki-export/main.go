// Command wiki-export renders Cinder's derived wiki layer into a Markdown tree
// (ADR-0014 决策 2: one-way export; TODO M6-W1b).
//
// It is its own binary because the desktop `repotory` executable has no
// subcommand dispatch at all — it launches Wails and returns — so the "repotory
// wiki export" phrasing in the ADR would be a lie. Same pattern as cmd/vector-init
// and cmd/abeval.
//
//	go build -o repotory-wiki-export ./cmd/wiki-export
//	./repotory-wiki-export -out ./wiki-out            # refuses a non-empty target
//	./repotory-wiki-export -out ./wiki-out -force     # overwrite our own subtree
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"repotory/internal/db"
	"repotory/internal/platform"
	"repotory/internal/service"
	"repotory/internal/version"
)

func main() {
	log.SetPrefix("wiki-export ")
	out := flag.String("out", "", "output directory (a wiki/ subtree is created inside it)")
	dbPath := flag.String("db", "", "database path (default: the app's DB)")
	projectID := flag.Int64("project", 0, "restrict the export to one project id (0 = all)")
	force := flag.Bool("force", false, "overwrite an existing target directory")
	flag.Parse()

	if *out == "" {
		fmt.Fprintln(os.Stderr, "wiki-export: --out is required")
		flag.Usage()
		os.Exit(2)
	}

	path := *dbPath
	if path == "" {
		path = platform.GetDbPath()
	}
	// The export is one-way and must stay that way (ADR-0014 decision 2: the export
	// tree is a read-only derivation). Opening through InitDB broke that promise on
	// the DATABASE side — it migrated the live store as a side effect of exporting.
	cp, err := db.CopyForTooling(path, db.OpenCopyOptions{})
	if err != nil {
		log.Fatalf("copy database %s: %v", path, err)
	}
	defer func() { _ = cp.Close() }()
	database := cp.DB

	svc := service.New(database, "")
	report, err := svc.ExportWikiTree(*out, *projectID, *force)
	if err != nil {
		log.Fatalf("export failed: %v", err)
	}

	fmt.Printf("Cinder %s — exported %d page(s) to %s\n", version.Version, report.Pages, report.Root)
	fmt.Printf("  files=%d skipped_pages=%d dangling_links=%d\n",
		len(report.Files), report.SkippedPages, report.DanglingLinks)
	for _, f := range report.Files {
		fmt.Printf("  %-52s %6d B  %s\n", f.Path, f.Bytes, f.SHA256[:12])
	}
	if report.DanglingLinks > 0 {
		fmt.Println("  note: some [[wikilinks]] in page bodies point at pages that do not exist")
	}
	if len(report.StaleFiles) > 0 {
		fmt.Printf("  stale files under wiki/ (written by an earlier export, not by this one — remove them if you want a clean tree):\n")
		for _, f := range report.StaleFiles {
			fmt.Printf("    %s\n", f)
		}
	}
}
