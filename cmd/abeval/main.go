// Command abeval runs the retrieval A/B evaluation gates against the live
// knowledge base: it executes every labeled query through two arms of the real
// retriever, compares Recall@k / NDCG@k, and exits non-zero unless the candidate
// beats the baseline by the configured margin.
//
// Two gates, one scorer (internal/search/abeval):
//
//	-mode notes    (default, M3 / ADR-0012 决策 4)
//	    lexical (semantic_search off) vs hybrid (on), over project_notes.
//	    cases: {"query": "...", "relevant": [note_id,...]}
//
//	-mode evidence (M7 lane 2 / ADR-0018 条件 6)
//	    pure FTS evidence (wiki_graph_search off) vs graph-aware evidence
//	    (1-hop expansion + PPR on), over wiki_pages + project_notes.
//	    cases: {"query": "...", "relevant": [{"type":"page","id":3},...]}
//
//	-mode handoff (TODO 2.1 度量口径)
//	    the cross-session memory gate: derives one probe per recorded handoff
//	    and measures whether the next session can still find it, plus whether
//	    repotory_context opens with the newest handoff. No -cases file.
//
// Usage:
//
//	abeval -mode notes    -cases queries.jsonl -k 10 -min-recall 0.05
//	abeval -mode evidence -cases queries.evidence.jsonl -k 10 -min-recall 0.05
//	abeval -mode handoff -k 10
//
// Both gates run on a throwaway copy of the database, never the live one. The
// notes gate's hybrid arm needs the embedding endpoint configured
// (embedding_* settings); if it is absent, hybrid == lexical and the gate
// correctly fails (no evidence gained).
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"repotory/internal/db"
	"repotory/internal/domain"
	"repotory/internal/platform"
	"repotory/internal/search/abeval"
	"repotory/internal/service"
)

type labeledCase struct {
	Query    string  `json:"query"`
	Relevant []int64 `json:"relevant"`
}

func main() {
	platform.SetPrivateUmask()

	mode := flag.String("mode", "notes", "gate to run: notes (M3 semantic) | evidence (M7 graph-aware) | handoff (cross-session memory)")
	casesPath := flag.String("cases", "", "path to labeled queries.jsonl (required for notes/evidence)")
	k := flag.Int("k", 10, "cutoff for recall@k / ndcg@k")
	minRecall := flag.Float64("min-recall", 0.05, "required recall@k gain (candidate-baseline) to pass the gate")
	dbPath := flag.String("db", "", "database path (default: the app's DB)")
	flag.Parse()

	// The handoff gate derives its own cases from the database, so it needs no
	// -cases file. Dispatch it before the -cases requirement.
	if *mode == "handoff" {
		os.Exit(runHandoffGate(*dbPath, *k))
	}

	if *casesPath == "" {
		log.Fatal("abeval: -cases is required")
	}

	// The evidence gate is a separate measurement with its own case format and
	// its own config key; it shares the scorer but nothing else. Dispatch before
	// the notes-only setup so neither gate has to carry the other's flags.
	if *mode == "evidence" {
		os.Exit(runEvidenceGate(*casesPath, *dbPath, *k, *minRecall))
	}
	if *mode != "notes" {
		log.Fatalf("abeval: unknown -mode %q (want notes|evidence|handoff)", *mode)
	}

	cases, err := loadCases(*casesPath)
	if err != nil {
		log.Fatalf("abeval: load cases: %v", err)
	}
	if len(cases) == 0 {
		log.Fatal("abeval: no cases in file")
	}

	path := *dbPath
	if path == "" {
		path = platform.GetDbPath()
	}
	// Work on a throwaway copy, never on the user's database. abeval is not a
	// read-only tool in the ordinary sense — with an embedding endpoint it calls
	// RebuildEmbeddings, which rewrites the whole index and persists embedding_dim —
	// and even the lexical-only arm used to run InitDB against -db, whose default is
	// the LIVE application database, so measuring retrieval silently migrated it.
	// An A/B gate that mutates what it measures is not a measurement.
	cp, err := db.CopyForTooling(path, db.OpenCopyOptions{})
	if err != nil {
		log.Fatalf("abeval: copy db: %v", err)
	}
	defer func() { _ = cp.Close() }()
	database := cp.DB
	svc := service.New(database, "abeval")

	// semanticEnabled() reads app_config on EVERY search, and db.SetConfig
	// writes straight through to the user's database. An evaluation tool must
	// not outlive its run: remember the original value and restore it when
	// abeval exits, pass or fail (os.Exit below skips defers, so the restore
	// is registered before any exit path).
	original, _ := db.GetConfig(database, "semantic_search")
	restore := func() {
		if original == "" {
			_ = db.DeleteConfig(database, "semantic_search")
		} else {
			_ = db.SetConfig(database, "semantic_search", original)
		}
	}
	if err := restoreOnSignal(restore); err != nil {
		log.Printf("abeval: signal restore not installed: %v", err)
	}

	// The two arms MUST differ at call time, not at setup time: each closure
	// flips the config it depends on right before searching. Toggling once up
	// front (the pre-fix wiring) left both arms running with semantic_search
	// on — lexical was silently hybrid, the recall delta was always 0, and
	// the gate could never pass.
	//
	// BOTH arms run svc.SearchAll — the PRODUCTION path. That is the whole
	// point: every real query (five frontend entry points, the MCP
	// repotory_notes_search / repotory_ask tools, the HTTP
	// /api/search?all=1 endpoint) goes through SearchAll, while this gate used
	// to measure svc.SearchNotes. SearchAll used to skip the vector fusion
	// entirely, so the ADR-0012 / ADR-0018 gains this gate reported were
	// never reaching a single user query — the gate certified a path the
	// product did not take. Measuring the production function removes that
	// class of false green permanently.
	//
	// The note/todo split is why noteIDs() filters by type: SearchAll returns
	// notes AND todos, and a todo's id lives in project_todos while the
	// labeled cases carry project_notes ids — id 1 exists in both tables.
	// Counting a todo hit as a note hit would inflate recall with false
	// positives.
	lexical := func(q string) []int64 {
		_ = db.SetConfig(database, "semantic_search", "0")
		return noteIDs(svc.SearchAll(q))
	}

	// Hybrid arm: enable + rebuild the vector index once, then search with
	// semantic on.
	if err := db.SetConfig(database, "semantic_search", "1"); err != nil {
		log.Fatalf("abeval: enable semantic: %v", err)
	}
	if n, err := svc.RebuildEmbeddings(); err != nil {
		fmt.Printf("abeval: semantic rebuild unavailable (%v) — hybrid will equal lexical\n", err)
	} else {
		fmt.Printf("abeval: embedded %d notes for the hybrid arm\n", n)
	}
	hybrid := func(q string) []int64 {
		_ = db.SetConfig(database, "semantic_search", "1")
		return noteIDs(svc.SearchAll(q))
	}

	lex, hyb, dRecall, dNDCG := abeval.Compare(cases, *k, lexical, hybrid)
	fmt.Println(lex.SummaryLine("lexical (production path)"))
	fmt.Println(hyb.SummaryLine("hybrid  (production path)"))
	fmt.Printf("delta: recall@%d=%+.3f ndcg@%d=%+.3f\n", *k, dRecall, *k, dNDCG)

	// Continuity line: the same comparison through svc.SearchNotes, which is
	// what every historical gate number in the ADRs and CHANGELOG was
	// measured on. It is printed, not gated on — the production path is what
	// decides. If the two diverge, the production path moved and the older
	// number is the one that needs re-measuring.
	notesOnly := func(q string) []int64 {
		_ = db.SetConfig(database, "semantic_search", "1")
		return ids(svc.SearchNotes(q))
	}
	_, hybNotes, dRecallNotes, dNDCGNotes := abeval.Compare(cases, *k, lexical, notesOnly)
	fmt.Println(hybNotes.SummaryLine("hybrid  (SearchNotes, continuity)"))
	fmt.Printf("delta (SearchNotes): recall@%d=%+.3f ndcg@%d=%+.3f\n", *k, dRecallNotes, *k, dNDCGNotes)

	restore()

	if dRecall < *minRecall {
		fmt.Printf("GATE FAIL: recall@%d gain %.3f < required %.3f — semantic search not yet worth enabling\n",
			*k, dRecall, *minRecall)
		os.Exit(1)
	}
	fmt.Printf("GATE PASS: recall@%d gain %.3f >= %.3f\n", *k, dRecall, *minRecall)
}

// restoreOnSignal best-effort restores the original semantic_search value on
// SIGINT/SIGTERM too (the gate can run against a live database for minutes).
// SIGKILL is unrecoverable by design; the next RebuildEmbeddings or manual
// toggle repairs the config either way.
func restoreOnSignal(restore func()) error {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ch
		restore()
		os.Exit(130)
	}()
	return nil
}

func loadCases(path string) ([]abeval.Case[int64], error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []abeval.Case[int64]
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var c labeledCase
		if err := json.Unmarshal(line, &c); err != nil {
			return nil, fmt.Errorf("bad line %q: %w", string(line), err)
		}
		out = append(out, abeval.Case[int64]{Query: c.Query, Relevant: c.Relevant})
	}
	return out, sc.Err()
}

// ids extracts hit ids from search hits, preserving ranked order.
func ids(hits []domain.SearchHit, err error) []int64 {
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

// noteIDs extracts only the NOTE hits, preserving ranked order.
//
// Needed whenever the search path returns notes AND todos (SearchAll): the
// labeled cases carry project_notes ids, but a todo's id comes from
// project_todos — id 1 exists in both tables. Without the type filter a todo
// hit would be counted as a note hit and inflate recall with false positives.
func noteIDs(hits []domain.SearchHit, err error) []int64 {
	if err != nil {
		log.Printf("eval search: %v", err)
		return nil
	}
	out := make([]int64, 0, len(hits))
	for _, h := range hits {
		if h.Type == "todo" {
			continue
		}
		out = append(out, h.ID)
	}
	return out
}
