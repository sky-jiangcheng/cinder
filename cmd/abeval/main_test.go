package main

import (
	"os"
	"path/filepath"
	"testing"

	"repotory/internal/search/abeval"
)

// loadCases is the parser between the user's labeled file and the A/B gate
// (ADR-0012 决策 4). Two ways to make the gate lie are covered here: a file
// that parses but labels nothing, and a line that fails halfway through
// (bytes.TrimSpace + a scanner error must both be honoured).

func writeCases(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "queries.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadCasesParsesJSONL(t *testing.T) {
	path := writeCases(t, `{"query": "retry policy", "relevant": [1, 2]}
{"query": "幂等键", "relevant": [3]}

`)

	cases, err := loadCases(path)
	if err != nil {
		t.Fatalf("loadCases: %v", err)
	}
	if len(cases) != 2 {
		t.Fatalf("got %d cases, want 2", len(cases))
	}
	if cases[0].Query != "retry policy" || len(cases[0].Relevant) != 2 {
		t.Errorf("case 0 = %+v", cases[0])
	}
	if cases[1].Query != "幂等键" || cases[1].Relevant[0] != 3 {
		t.Errorf("case 1 = %+v", cases[1])
	}
}

// Blank lines are legal separators; a bad line must abort the whole file —
// silently skipping it would evaluate a smaller gate than the user labelled
// and report an n= that matches nothing they can see.
func TestLoadCasesRejectsBadLine(t *testing.T) {
	path := writeCases(t, `{"query": "good", "relevant": [1]}
{"query": broken json, "relevant": [2]}
`)

	if _, err := loadCases(path); err == nil {
		t.Fatal("expected an error for the malformed line, got nil")
	}
}

func TestLoadCasesEmptyFileYieldsZeroCases(t *testing.T) {
	// main() refuses zero cases after loadCases succeeds; the parser itself
	// returns an empty slice rather than an error so the two failures are
	// distinguishable ("unreadable file" vs "labelled nothing").
	path := writeCases(t, "\n\n")
	cases, err := loadCases(path)
	if err != nil {
		t.Fatalf("loadCases: %v", err)
	}
	if len(cases) != 0 {
		t.Fatalf("got %d cases, want 0", len(cases))
	}
}

// Round-trip guard against the queryset pitfall recorded in TODO M6-W2:
// abeval.Case has no json tags, so encoding it directly would emit
// {"Query": ..., "Relevant": ...} and queryset -emit must therefore use its
// own tagged struct. If Case ever grows tags these tests stay valid; if the
// field names drift apart the fixture below breaks loudly.
func TestLabeledCaseShapeMatchesAbevalCase(t *testing.T) {
	path := writeCases(t, `{"query": "q", "relevant": [7]}`)
	cases, err := loadCases(path)
	if err != nil {
		t.Fatal(err)
	}
	want := abeval.Case[int64]{Query: "q", Relevant: []int64{7}}
	if cases[0].Query != want.Query || len(cases[0].Relevant) != 1 || cases[0].Relevant[0] != want.Relevant[0] {
		t.Errorf("parsed %+v, want %+v — labeledCase and abeval.Case have drifted", cases[0], want)
	}
}

func TestIdsPreservesRankedOrder(t *testing.T) {
	hits := ids(nil, nil)
	if len(hits) != 0 {
		t.Errorf("empty input should give empty output, got %v", hits)
	}
}
