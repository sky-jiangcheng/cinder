// Command abevaldiag prints the per-case evidence-gate comparison: for every
// labeled query, what the pure-FTS arm returned, what the graph-aware arm
// returned, and which documents moved. A gate that prints only the aggregate
// delta cannot tell "the graph found the right neighbour" apart from "the graph
// reordered by luck", and those demand different reactions — one is a working
// feature, the other is a number to distrust.
//
// Usage:
//
//	abevaldiag -mode evidence -cases queries.evidence.jsonl -k 10 -db copy.db
//	abevaldiag -mode notes    -cases queries.jsonl        -k 10 -db copy.db
package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"repotory/internal/db"
	"repotory/internal/domain"
	"repotory/internal/platform"
	"repotory/internal/search/abeval"
	"repotory/internal/service"
)

const graphSearchKey = "wiki_graph_search"

// diagBudget is generous compared with service.DefaultEvidenceBudget on
// purpose: the diagnosis is about which documents each arm retrieved, and a
// tight budget would hide the tail where graph-expanded neighbours land.
var diagBudget = service.EvidenceBudget{MaxItems: 60, MaxChars: 400000, Deadline: 30 * time.Second}

type noteCase struct {
	Query    string  `json:"query"`
	Relevant []int64 `json:"relevant"`
}

type evidenceCase struct {
	Query    string         `json:"query"`
	Relevant []abeval.DocID `json:"relevant"`
}

func main() {
	platform.SetPrivateUmask()

	mode := flag.String("mode", "evidence", "which gate to diagnose: evidence | notes")
	casesPath := flag.String("cases", "", "labeled cases jsonl (required)")
	k := flag.Int("k", 10, "cutoff")
	dbPath := flag.String("db", "", "database path")
	onlyMiss := flag.Bool("only-miss", false, "print only cases where the candidate arm missed something relevant")
	rebuild := flag.Bool("rebuild-embeddings", false, "rebuild the note vector index first (notes mode; evidence mode always rebuilds both indexes)")
	flag.Parse()

	if *casesPath == "" {
		log.Fatal("abevaldiag: -cases is required")
	}
	path := *dbPath
	if path == "" {
		path = platform.GetDbPath()
	}
	cp, err := db.CopyForTooling(path, db.OpenCopyOptions{})
	if err != nil {
		log.Fatalf("abevaldiag: copy db: %v", err)
	}
	defer func() { _ = cp.Close() }()
	database := cp.DB
	svc := service.New(database, "abevaldiag")

	// Without this the vector index is empty and every "hybrid" arm is really
	// lexical — the harness then reports a confident number about a retriever
	// that was never exercised. Rebuild once, before any arm runs.
	if *rebuild {
		if n, err := svc.RebuildEmbeddings(); err != nil {
			fmt.Printf("abevaldiag: note embedding rebuild unavailable (%v)\n", err)
		} else {
			fmt.Printf("abevaldiag: embedded %d notes\n", n)
		}
	}

	if *mode == "evidence" {
		// The evidence gate's candidate arm seeds PPR from FTS hits PLUS
		// page-vector recall (lane 2's 向量粗筛 → 图精排), behind the same
		// semantic_search consent the gate sets in its own copy. The diagnosis
		// must reproduce that setup exactly, or it describes a different
		// retriever than the gate it claims to explain — so evidence mode
		// always rebuilds both indexes here, on the throwaway copy, never live.
		if err := db.SetConfig(database, "semantic_search", "1"); err != nil {
			fmt.Printf("abevaldiag: could not enable semantic_search in the copy: %v\n", err)
		}
		if n, err := svc.RebuildEmbeddings(); err != nil {
			fmt.Printf("abevaldiag: note embedding rebuild unavailable (%v)\n", err)
		} else {
			fmt.Printf("abevaldiag: embedded %d notes\n", n)
		}
		if n, err := svc.RebuildPageEmbeddings(); err != nil {
			have := -1
			if qerr := database.QueryRow("SELECT count(*) FROM page_embeddings").Scan(&have); qerr != nil {
				have = -1
			}
			if have > 0 {
				fmt.Printf("abevaldiag: page embedding rebuild unavailable (%v) — graph arm seeds from the %d page vector(s) already in the snapshot\n", err, have)
			} else {
				fmt.Printf("abevaldiag: page embedding rebuild unavailable (%v) — graph arm seeds from FTS only (no page vectors in the snapshot)\n", err)
			}
		} else {
			fmt.Printf("abevaldiag: embedded %d pages\n", n)
		}
		runEvidence(database, svc, *casesPath, *k, *onlyMiss)
		return
	}
	runNotes(database, svc, *casesPath, *k, *onlyMiss)
}

func runEvidence(database *sql.DB, svc *service.Service, path string, k int, onlyMiss bool) {
	cases, err := loadEvidenceCases(path)
	if err != nil {
		log.Fatalf("abevaldiag: %v", err)
	}
	// The switch MUST be flipped inside each closure, right before the search.
	// Setting it once out here left both closures sharing the final value ("1"),
	// so both arms ran graph-aware: the baseline silently became the candidate
	// and every delta read 0. This is the same trap the notes gate documents at
	// its own arm wiring, and a diagnosis that cannot tell the arms apart is
	// worse than no diagnosis — it reports "no difference" for "I removed the
	// difference".
	baseArm := func(q string) []abeval.DocID {
		_ = db.SetConfig(database, graphSearchKey, "0")
		return docIDs(svc.GatherEvidence(0, q, diagBudget))
	}
	candArm := func(q string) []abeval.DocID {
		_ = db.SetConfig(database, graphSearchKey, "1")
		return docIDs(svc.GatherEvidence(0, q, diagBudget))
	}

	var lexSum, candSum float64
	full := 0
	for _, c := range cases {
		b, a := baseArm(c.Query), candArm(c.Query)
		br, ar := abeval.RecallAtK(b, c.Relevant, k), abeval.RecallAtK(a, c.Relevant, k)
		lexSum += br
		candSum += ar
		if ar == 1 && len(c.Relevant) > 0 {
			full++
		}
		if onlyMiss && ar == 1 {
			continue
		}
		mark := "  "
		if ar > br {
			mark = "+>"
		} else if ar < br {
			mark = "<<"
		}
		fmt.Printf("%s lex=%.2f hyb=%.2f | %s\n", mark, br, ar, c.Query)
		fmt.Printf("     labeled: %s\n", docList(c.Relevant))
		fmt.Printf("     FTS   : %s\n", docList(b))
		fmt.Printf("     graph : %s\n", docList(a))
		for _, d := range missing(a, c.Relevant, k) {
			fmt.Printf("     MISS  : %s\n", d)
		}
	}
	n := float64(len(cases))
	fmt.Printf("\nn=%d  meanFTS=%.3f  meanGraph=%.3f  graph perfect=%d/%d\n", len(cases), lexSum/n, candSum/n, full, len(cases))
}

func runNotes(database *sql.DB, svc *service.Service, path string, k int, onlyMiss bool) {
	cases, err := loadNoteCases(path)
	if err != nil {
		log.Fatalf("abevaldiag: %v", err)
	}
	baseArm := func(q string) []int64 {
		_ = db.SetConfig(database, "semantic_search", "0")
		return noteIDs(svc.SearchNotes(q))
	}
	candArm := func(q string) []int64 {
		_ = db.SetConfig(database, "semantic_search", "1")
		return noteIDs(svc.SearchNotes(q))
	}
	var lexSum, hybSum float64
	full := 0
	for _, c := range cases {
		b, a := baseArm(c.Query), candArm(c.Query)
		br, ar := abeval.RecallAtK(b, c.Relevant, k), abeval.RecallAtK(a, c.Relevant, k)
		lexSum += br
		hybSum += ar
		if ar == 1 && len(c.Relevant) > 0 {
			full++
		}
		if onlyMiss && ar == 1 {
			continue
		}
		mark := "  "
		if ar > br {
			mark = "+>"
		} else if ar < br {
			mark = "<<"
		}
		fmt.Printf("%s lex=%.2f hyb=%.2f | %s\n", mark, br, ar, c.Query)
		fmt.Printf("     labeled: %v\n", c.Relevant)
		fmt.Printf("     lexTop : %v\n", b)
		fmt.Printf("     hybTop : %v\n", a)
		for _, m := range missingNotes(a, c.Relevant, k) {
			fmt.Printf("     MISS   : note %d\n", m)
		}
	}
	n := float64(len(cases))
	fmt.Printf("\nn=%d  meanLex=%.3f  meanHyb=%.3f  hybrid perfect=%d/%d\n", len(cases), lexSum/n, hybSum/n, full, len(cases))
}

func missing(ranked, relevant []abeval.DocID, k int) []string {
	set := map[abeval.DocID]bool{}
	for _, d := range relevant {
		set[d] = true
	}
	got := map[abeval.DocID]bool{}
	for i, d := range ranked {
		if i >= k {
			break
		}
		got[d] = true
	}
	var out []string
	for d := range set {
		if !got[d] {
			out = append(out, fmt.Sprintf("%s %d", d.Type, d.ID))
		}
	}
	return out
}

func missingNotes(ranked, relevant []int64, k int) []int64 {
	set := map[int64]bool{}
	for _, d := range relevant {
		set[d] = true
	}
	got := map[int64]bool{}
	for i, d := range ranked {
		if i >= k {
			break
		}
		got[d] = true
	}
	var out []int64
	for _, d := range relevant {
		if !got[d] {
			out = append(out, d)
		}
	}
	return out
}

func docList(ds []abeval.DocID) string {
	if len(ds) == 0 {
		return "(empty)"
	}
	parts := make([]string, 0, len(ds))
	for _, d := range ds {
		parts = append(parts, fmt.Sprintf("%s:%d", d.Type, d.ID))
	}
	return strings.Join(parts, " ")
}

func docIDs(ev *service.Evidence) []abeval.DocID {
	if ev == nil {
		return nil
	}
	out := make([]abeval.DocID, 0, len(ev.Items))
	for _, it := range ev.Items {
		out = append(out, abeval.DocID{Type: it.Type, ID: it.ID})
	}
	return out
}

func noteIDs(hits []domain.SearchHit, err error) []int64 {
	if err != nil {
		log.Printf("eval search: %v", err)
		return nil
	}
	out := make([]int64, len(hits))
	for i, h := range hits {
		out[i] = h.ID
	}
	return out
}

func loadEvidenceCases(path string) ([]abeval.Case[abeval.DocID], error) {
	var out []abeval.Case[abeval.DocID]
	err := eachLine(path, func(line []byte) error {
		var c evidenceCase
		if err := json.Unmarshal(line, &c); err != nil {
			return err
		}
		out = append(out, abeval.Case[abeval.DocID]{Query: c.Query, Relevant: c.Relevant})
		return nil
	})
	return out, err
}

func loadNoteCases(path string) ([]abeval.Case[int64], error) {
	var out []abeval.Case[int64]
	err := eachLine(path, func(line []byte) error {
		var c noteCase
		if err := json.Unmarshal(line, &c); err != nil {
			return err
		}
		out = append(out, abeval.Case[int64]{Query: c.Query, Relevant: c.Relevant})
		return nil
	})
	return out, err
}

func eachLine(path string, fn func([]byte) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || bytes.HasPrefix(line, []byte("#")) {
			continue
		}
		if err := fn(line); err != nil {
			return fmt.Errorf("%q: %w", string(line), err)
		}
	}
	return sc.Err()
}
