// Command searchbench measures the LATENCY of each retrieval stage against a
// snapshot of the knowledge base, and prints per-stage percentiles.
//
// It is deliberately a separate tool from abeval: abeval measures retrieval
// QUALITY (recall@k / ndcg@k, ADR-0012 决策 4), and its output says nothing
// about how long a query takes. The performance questions this project needs
// answered — "is the graph expansion running the recursive CTE twice?", "how
// much of an evidence retrieval is the embedding round trip?" — are only
// answerable with per-stage timings.
//
// Stages measured (each against the same query set):
//
//	lexical    db.SearchNotes — FTS5 trigram path
//	embed      the remote embedding round trip for the query vector
//	knn        sqlite-vec KNN over the note vectors
//	notes      service.SearchNotes — the full end-to-end note search
//	graph      db.WikiGraphNeighbors — the recursive expansion CTE
//	adjacency  db.WikiAdjacency — the PPR subgraph load
//	ppr        graph.PPR — the ranking itself
//	evidence   service.GatherEvidence — the full AskAI evidence retrieval
//
// Usage:
//
//	searchbench -cases queries.jsonl -repeat 20
//	searchbench -cases queries.jsonl -project 1 -stage evidence
//
// The database is opened as a throwaway copy (db.CopyForTooling), never the
// live one: measuring must not migrate or rewrite what it measures. The copy
// is a snapshot, so the numbers do not move under a concurrent desktop session.
package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"repotory/internal/db"
	"repotory/internal/platform"
	"repotory/internal/search/graph"
	"repotory/internal/search/hybrid"
	"repotory/internal/service"
)

// stage is one measured step of the retrieval pipeline.
type stage struct {
	name string
	fn   func(query string) error
}

// sample is one timing observation.
type sample struct {
	ms float64
}

// stats summarises a set of timings.
type stats struct {
	name string
	n    int
	p50  float64
	p95  float64
	max  float64
	mean float64
}

// summarize computes percentiles over the collected samples. Percentiles use
// the nearest-rank method on the sorted slice: with the small sample counts a
// benchmark run produces, an interpolated percentile invents values that were
// never measured.
func summarize(name string, samples []sample) stats {
	st := stats{name: name, n: len(samples)}
	if len(samples) == 0 {
		return st
	}
	vals := make([]float64, len(samples))
	for i, s := range samples {
		vals[i] = s.ms
	}
	sort.Float64s(vals)
	st.p50 = percentile(vals, 0.50)
	st.p95 = percentile(vals, 0.95)
	st.max = vals[len(vals)-1]
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	st.mean = sum / float64(len(vals))
	return st
}

// percentile returns the nearest-rank percentile of a sorted slice.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(p*float64(len(sorted))+0.5) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func (s stats) String() string {
	return fmt.Sprintf("%-12s n=%-4d p50=%7.2fms p95=%7.2fms max=%7.2fms mean=%7.2fms",
		s.name, s.n, s.p50, s.p95, s.max, s.mean)
}

// queryCase is one line of the query file. The `relevant` field of abeval's
// labeled format is accepted and ignored: latency does not need labels.
type queryCase struct {
	Query    string  `json:"query"`
	Relevant []int64 `json:"relevant"`
}

func main() {
	platform.SetPrivateUmask()

	casesPath := flag.String("cases", "", "path to queries.jsonl (required)")
	dbPath := flag.String("db", "", "database path (default: the app's DB)")
	repeat := flag.Int("repeat", 10, "how many times to run each query (warm cache)")
	projectID := flag.Int64("project", 0, "project id for the evidence stage (0 = skip it)")
	only := flag.String("stage", "", "run only this stage (default: all)")
	flag.Parse()

	if *casesPath == "" {
		log.Fatal("searchbench: -cases is required")
	}
	cases, err := loadCases(*casesPath)
	if err != nil {
		log.Fatalf("searchbench: load cases: %v", err)
	}
	if len(cases) == 0 {
		log.Fatal("searchbench: no cases in file")
	}

	path := *dbPath
	if path == "" {
		path = platform.GetDbPath()
	}
	cp, err := db.CopyForTooling(path, db.OpenCopyOptions{})
	if err != nil {
		log.Fatalf("searchbench: copy db: %v", err)
	}
	defer func() { _ = cp.Close() }()
	database := cp.DB
	svc := service.New(database, "searchbench")

	queries := make([]string, len(cases))
	for i, c := range cases {
		queries[i] = c.Query
	}

	fmt.Printf("searchbench: %d queries x %d repeats on a snapshot of %s\n", len(queries), *repeat, path)

	stages := buildStages(database, svc, *projectID)
	if *only != "" {
		filtered := stages[:0]
		for _, st := range stages {
			if st.name == *only {
				filtered = append(filtered, st)
			}
		}
		if len(filtered) == 0 {
			log.Fatalf("searchbench: unknown stage %q", *only)
		}
		stages = filtered
	}

	results := make([]stats, 0, len(stages))
	for _, st := range stages {
		samples := make([]sample, 0, len(queries)*(*repeat))
		for r := 0; r < *repeat; r++ {
			for _, q := range queries {
				start := time.Now()
				if err := st.fn(q); err != nil {
					if !errors.Is(err, errSkipped) {
						log.Printf("searchbench: stage %s: %v", st.name, err)
					}
					continue
				}
				samples = append(samples, sample{ms: float64(time.Since(start).Microseconds()) / 1000})
			}
		}
		results = append(results, summarize(st.name, samples))
	}

	fmt.Println()
	for _, r := range results {
		fmt.Println(r)
	}
}

// buildStages wires the measurable stages against the snapshot. A stage that
// cannot run (no embedding endpoint configured, no vector index) is reported
// through a skipped sample instead of aborting the whole benchmark.
func buildStages(database *sql.DB, svc *service.Service, projectID int64) []stage {
	stages := []stage{
		{
			name: "lexical",
			fn: func(q string) error {
				_, err := db.SearchNotes(database, q)
				return err
			},
		},
	}

	emb, embOK := embedderFor(database)
	if embOK {
		stages = append(stages, stage{
			name: "embed",
			fn: func(q string) error {
				_, err := emb.Embed([]string{q})
				return err
			},
		})
	} else {
		fmt.Println("searchbench: no embedding endpoint configured — skipping the embed stage")
	}

	stages = append(stages, stage{
		name: "knn",
		fn: func(q string) error {
			if !embOK {
				return errSkipped
			}
			vecs, err := emb.Embed([]string{q})
			if err != nil || len(vecs) == 0 {
				return errSkipped
			}
			_, err = db.KnnNoteIDs(database, vecs[0], 20)
			return err
		},
	})

	stages = append(stages, stage{
		name: "notes",
		fn: func(q string) error {
			_, err := svc.SearchNotes(q)
			return err
		},
	})

	// The graph stages need a seed set. The lexical hits are the natural seed
	// (that is what the real evidence path uses), so they are computed once per
	// query and reused by the three graph stages.
	seeds := map[string][]int64{}
	seedOf := func(q string) []int64 {
		if s, ok := seeds[q]; ok {
			return s
		}
		hits, err := db.SearchNotes(database, q)
		if err != nil {
			return nil
		}
		var s []int64
		for _, h := range hits {
			s = append(s, h.ID)
		}
		seeds[q] = s
		return s
	}

	stages = append(stages,
		stage{
			name: "graph",
			fn: func(q string) error {
				_, err := db.WikiGraphNeighbors(database, seedOf(q), 1)
				return err
			},
		},
		stage{
			name: "adjacency",
			fn: func(q string) error {
				_, err := db.WikiAdjacency(database, seedOf(q), 1, 2000)
				return err
			},
		},
		stage{
			name: "ppr",
			fn: func(q string) error {
				adj, err := db.WikiAdjacency(database, seedOf(q), 1, 2000)
				if err != nil {
					return err
				}
				seed := seedOf(q)
				reset := make(map[int64]float64, len(seed))
				for _, id := range seed {
					reset[id] = 1
				}
				graph.PPR(adj, reset, 0.85, 20, len(seed))
				return nil
			},
		},
	)

	if projectID > 0 {
		stages = append(stages, stage{
			name: "evidence",
			fn: func(q string) error {
				svc.GatherEvidence(projectID, q, service.DefaultEvidenceBudget)
				return nil
			},
		})
	}
	return stages
}

// errSkipped marks a stage that could not run for this query. It is not
// reported as an error because a missing vector index is a configuration
// state, not a benchmark failure.
var errSkipped = errors.New("skipped")

// embedderFor builds a remote embedder from the snapshot's config, mirroring
// what the service does on the search path.
func embedderFor(database *sql.DB) (*hybrid.RemoteEmbedder, bool) {
	base, _ := db.GetConfig(database, "embedding_base_url")
	model, _ := db.GetConfig(database, "embedding_model")
	if strings.TrimSpace(base) == "" || strings.TrimSpace(model) == "" {
		return nil, false
	}
	key, _ := db.GetConfig(database, "embedding_api_key")
	dimStr, _ := db.GetConfig(database, "embedding_dim")
	dim, _ := strconv.Atoi(dimStr)
	return &hybrid.RemoteEmbedder{BaseURL: base, Model: model, APIKey: key, Dim: dim}, true
}

// loadCases parses the JSONL query file. A line that fails to parse aborts
// the load: a benchmark over a silently shortened query set reports
// percentiles for the wrong population.
func loadCases(path string) ([]queryCase, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []queryCase
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var c queryCase
		if err := json.Unmarshal(line, &c); err != nil {
			return nil, fmt.Errorf("bad line %q: %w", string(line), err)
		}
		out = append(out, c)
	}
	return out, sc.Err()
}
