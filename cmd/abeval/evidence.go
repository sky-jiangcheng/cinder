// evidence.go — the M7 lane 2 gate (ADR-0018 条件 6): does graph-aware evidence
// actually beat pure FTS evidence?
//
// The claim lane 2 has to cash is "图感知取证 > 纯 FTS 取证". This file is the
// only thing that can cash it, and it measures it the same way the notes gate
// measures semantic search: both arms run the REAL retriever
// (GatherEvidence, budget and interleaving included), and the only difference
// between them is the switch under test.
//
//	baseline  wiki_graph_search=0 → evidencePages returns the FTS order verbatim
//	candidate wiki_graph_search=1 → STAGED: FTS hits (if any) keep their exact
//	                                 order as the prefix; page-vector recall seeds
//	                                 only when FTS finds nothing; whitelist-edge
//	                                 neighbours append behind the prefix, PPR-ordered
//
// The candidate arm is lane 2's pipeline with its seed stage completed (the
// "查询词→种子页" rescue), shaped to satisfy criterion 11②'s "k=2..20 all
// positive, no case regression": additive after the recall prefix, never a
// re-rank of it (both fused reset shapes were measured on 2026-10-21 and
// rejected — see the ADR promotion record).
//
// The candidate arm's vector stage needs the page vector index. The gate
// rebuilds both vector indexes IN THE COPY (never live) exactly like the notes
// gate rebuilds note embeddings for its hybrid arm, behind the same
// semantic_search consent the app itself uses. A snapshot taken AFTER the
// production pipeline was activated already carries both indexes, and the
// rebuild is re-runnable over them (clear-then-insert, like the note twin).
// With no embedding endpoint configured both rebuilds degrade: a snapshot that
// already carries vectors keeps them (the graph arm stays vector-seeded), and
// only a vector-less snapshot falls back to FTS-seeded expansion — the notices
// printed below state which shape applied instead of hiding it.
//
// Both arms read the same note ranking (semantic_search is set once up front,
// identically for both), so the only inter-arm difference remains the graph
// switch: the delta stays attributable to the lane-2 pipeline.
package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"repotory/internal/db"
	"repotory/internal/platform"
	"repotory/internal/search/abeval"
	"repotory/internal/service"
)

// graphSearchKey mirrors service.wikiGraphSearchKey. It is duplicated rather
// than exported because the constant is deliberately unexported in the service
// package (the switch is its business, not the gate's); if either side renames
// it, the gate reads "0" for both arms and reports delta 0, which the
// diagnostic below makes obvious instead of a silent pass.
const graphSearchKey = "wiki_graph_search"

// evidenceBudget is deliberately generous compared with
// service.DefaultEvidenceBudget (12 items / 4000 chars). The gate measures
// RANKING, and a tight budget truncates the tail before the metric ever sees it —
// with 12 items a k=10 cutoff measures the budget, not the retriever. This is
// the one place the gate deliberately does NOT use the production budget, and
// the reason is stated here because it looks like an oversight otherwise.
var evidenceBudget = service.EvidenceBudget{
	MaxItems: 60,
	MaxChars: 400000,
	Deadline: 30 * time.Second,
}

// evidenceCase is one labeled query over the combined page+note evidence space.
// Relevant entries are typed because wiki_pages.id and project_notes.id are
// independent sequences: labeling note 7 and retrieving page 7 is a miss, not a
// hit.
type evidenceCase struct {
	Query    string         `json:"query"`
	Relevant []abeval.DocID `json:"relevant"`
}

func runEvidenceGate(casesPath, dbPath string, k int, minRecall float64) int {
	cases, err := loadEvidenceCases(casesPath)
	if err != nil {
		log.Fatalf("abeval: load evidence cases: %v", err)
	}
	if len(cases) == 0 {
		log.Fatal("abeval: no evidence cases in file")
	}

	path := dbPath
	if path == "" {
		path = platform.GetDbPath()
	}
	// Same discipline as the notes gate: measure on a copy, never on the live DB.
	cp, err := db.CopyForTooling(path, db.OpenCopyOptions{})
	if err != nil {
		log.Fatalf("abeval: copy db: %v", err)
	}
	defer func() { _ = cp.Close() }()
	database := cp.DB
	svc := service.New(database, "abeval")

	// graphSearchEnabled() reads app_config on EVERY call, so the arms must flip
	// the key immediately before searching — toggling once up front left both
	// arms on the same setting and the delta was structurally 0.
	// db.GetConfig returns ("", err) for a missing key, so an empty original means
	// "was unset" — matching how the notes gate restores semantic_search.
	original, _ := db.GetConfig(database, graphSearchKey)
	originalSemantic, _ := db.GetConfig(database, "semantic_search")
	restore := func() {
		if original == "" {
			_ = db.DeleteConfig(database, graphSearchKey)
			return
		}
		_ = db.SetConfig(database, graphSearchKey, original)
		if originalSemantic == "" {
			_ = db.DeleteConfig(database, "semantic_search")
			return
		}
		_ = db.SetConfig(database, "semantic_search", originalSemantic)
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ch
		restore()
		os.Exit(130)
	}()

	// Build both vector indexes in the copy so the candidate arm's seed stage has
	// vectors to recall (see the header comment). Consent is set before the page
	// rebuild because RebuildPageEmbeddings checks semantic_search — the same
	// consent gate the app enforces. Notes first: it persists embedding_dim,
	// which the page rebuild reads instead of re-probing.
	if err := db.SetConfig(database, "semantic_search", "1"); err != nil {
		log.Printf("abeval: could not enable semantic_search in the copy: %v", err)
	}
	if n, err := svc.RebuildEmbeddings(); err != nil {
		fmt.Printf("abeval: note embedding rebuild unavailable (%v) — note ranking falls back to lexical, identically in both arms\n", err)
	} else {
		fmt.Printf("abeval: embedded %d notes for both arms\n", n)
	}
	if n, err := svc.RebuildPageEmbeddings(); err != nil {
		if have := scalarInt(database, "SELECT count(*) FROM page_embeddings"); have > 0 {
			fmt.Printf("abeval: page embedding rebuild unavailable (%v) — graph arm seeds from the %d page vector(s) already in the snapshot\n", err, have)
		} else {
			fmt.Printf("abeval: page embedding rebuild unavailable (%v) — graph arm seeds from FTS only (no page vectors in the snapshot)\n", err)
		}
	} else {
		fmt.Printf("abeval: embedded %d pages for the vector-seeded graph arm\n", n)
	}

	baseline := func(q string) []abeval.DocID {
		_ = db.SetConfig(database, graphSearchKey, "0")
		return evidenceDocIDs(svc.GatherEvidence(context.Background(), 0, q, evidenceBudget))
	}
	candidate := func(q string) []abeval.DocID {
		_ = db.SetConfig(database, graphSearchKey, "1")
		return evidenceDocIDs(svc.GatherEvidence(context.Background(), 0, q, evidenceBudget))
	}

	base, cand, dRecall, dNDCG := abeval.Compare(cases, k, baseline, candidate)
	restore()

	fmt.Println(base.SummaryLine("evidence/FTS "))
	fmt.Println(cand.SummaryLine("evidence/graph"))
	fmt.Printf("delta: recall@%d=%+.3f ndcg@%d=%+.3f\n", k, dRecall, k, dNDCG)
	reportGraphDensity(database)

	if dRecall < minRecall {
		fmt.Printf("GATE FAIL: recall@%d gain %.3f < required %.3f — graph-aware evidence is not yet demonstrated on this corpus\n",
			k, dRecall, minRecall)
		return 1
	}
	fmt.Printf("GATE PASS: recall@%d gain %.3f >= %.3f\n", k, dRecall, minRecall)
	return 0
}

// evidenceDocIDs flattens evidence items into the typed id space, preserving
// the interleaved order GatherEvidence produced (that order IS the ranking the
// metric scores).
func evidenceDocIDs(ev *service.Evidence) []abeval.DocID {
	if ev == nil {
		return nil
	}
	out := make([]abeval.DocID, 0, len(ev.Items))
	for _, it := range ev.Items {
		out = append(out, abeval.DocID{Type: it.Type, ID: it.ID})
	}
	return out
}

// reportGraphDensity prints the corpus facts that decide whether a delta is even
// possible. Expansion can only surface a neighbour of a seed, so with no edges
// the candidate arm is mathematically identical to the baseline and delta 0 is
// the correct answer rather than a bug. Without this line a reader has to guess
// which of the two they are looking at.
//
// The counts come from local SQL rather than new db.* helpers: this is a
// diagnostic print in a CLI tool, not a retrieval path, and adding exported
// database API for it would widen the package surface for one line of output.
func reportGraphDensity(database *sql.DB) {
	pages := scalarInt(database, "SELECT count(*) FROM wiki_pages WHERE status = 'approved'")
	edges := scalarInt(database, "SELECT count(*) FROM page_links")
	fmt.Printf("corpus: %d approved page(s), %d graph edge(s)\n", pages, edges)
	if edges == 0 {
		fmt.Println("note: 0 edges → expansion has nothing to traverse; delta 0 is structural, not a retrieval failure")
	}
}

// scalarInt runs a counting query for the diagnostic line above. A failure
// prints -1 rather than aborting the gate: the density report is context for the
// verdict, and a diagnostic must never be the thing that changes the exit code.
func scalarInt(database *sql.DB, query string) int {
	var n int
	if err := database.QueryRow(query).Scan(&n); err != nil {
		return -1
	}
	return n
}

// loadEvidenceCases reads the typed case file. Unlike the notes format, a bare
// number would be ambiguous here, so the loader rejects it outright: a
// mislabeled case that silently scores against the wrong id space is worse than
// a file that will not load.
func loadEvidenceCases(path string) ([]abeval.Case[abeval.DocID], error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []abeval.Case[abeval.DocID]
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || bytes.HasPrefix(line, []byte("#")) {
			continue
		}
		var c evidenceCase
		if err := json.Unmarshal(line, &c); err != nil {
			return nil, fmt.Errorf("bad line %q: %w", string(line), err)
		}
		if c.Query == "" {
			return nil, fmt.Errorf("case with empty query: %q", string(line))
		}
		for _, d := range c.Relevant {
			if d.Type != "page" && d.Type != "note" {
				return nil, fmt.Errorf("case %q: relevant type must be page|note, got %q", c.Query, d.Type)
			}
		}
		out = append(out, abeval.Case[abeval.DocID]{Query: c.Query, Relevant: c.Relevant})
	}
	return out, sc.Err()
}
