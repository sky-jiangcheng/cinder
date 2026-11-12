package wikigen

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"repotory/internal/db"
)

// The seam exists so these kernels can run without a service and can be tested
// without an HTTP server: a counting fake stands in for the transport. These
// tests pin the property ADR-0060 asked whether the split would preserve — the
// budget rules stay in the kernels, so "gate off / no endpoint / zero
// candidates" still means zero outbound calls — and they do it at the seam,
// where the transport is the thing being counted.

type countingChat struct {
	available bool
	calls     int
}

func (c *countingChat) Available(ctx context.Context) error {
	if !c.available {
		return ErrNotConfigured
	}
	return nil
}

func (c *countingChat) Chat(ctx context.Context, msgs []Message) (Reply, error) {
	c.calls++
	return Reply{Text: "[]"}, nil
}

func setupKernelDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

// Zero candidates, zero calls: a paid endpoint must not be pinged to be told
// there is nothing to judge (the ADR-0053 D2 property, restated for merge).
func TestSuggestMergesZeroCandidatesZeroCalls(t *testing.T) {
	database := setupKernelDB(t)
	if err := db.SetConfig(database, KeyLintLLM, "1"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetConfig(database, KeyMergeSuggest, "1"); err != nil {
		t.Fatal(err)
	}
	chat := &countingChat{available: true}

	findings, note := SuggestMerges(context.Background(), database, chat, nil)
	if len(findings) != 0 || note != "" {
		t.Fatalf("empty library: findings=%d note=%q, want none", len(findings), note)
	}
	if chat.calls != 0 {
		t.Fatalf("zero candidates cost %d call(s), want 0", chat.calls)
	}
}

// The gate runs before the candidate query, so a disabled merge pass does not
// even read the graph — let alone call a model.
func TestSuggestMergesGateOffNoCalls(t *testing.T) {
	database := setupKernelDB(t)
	chat := &countingChat{available: true}

	_, note := SuggestMerges(context.Background(), database, chat, nil)
	if chat.calls != 0 {
		t.Fatalf("gate off cost %d call(s), want 0", chat.calls)
	}
	if !strings.Contains(note, KeyLintLLM) {
		t.Fatalf("the note must name the gate that is off, got %q", note)
	}
}

// Compile's gate and endpoint check run before any note is read, so neither can
// produce a call; the endpoint case must leave the same note the pre-split code
// left, because "not configured" is not a failed call.
func TestCompileProjectNotesGatesBeforeAnyCall(t *testing.T) {
	database := setupKernelDB(t)
	chat := &countingChat{available: true}

	rep, err := CompileProjectNotes(context.Background(), database, chat, 7, 0)
	if err != nil {
		t.Fatal(err)
	}
	if chat.calls != 0 {
		t.Fatalf("compile gate off cost %d call(s), want 0", chat.calls)
	}
	if !strings.Contains(rep.LLMNote, KeyCompile) {
		t.Fatalf("the note must name the gate that is off, got %q", rep.LLMNote)
	}

	if err := db.SetConfig(database, KeyCompile, "1"); err != nil {
		t.Fatal(err)
	}
	chat = &countingChat{available: false}
	rep, err = CompileProjectNotes(context.Background(), database, chat, 7, 0)
	if err != nil {
		t.Fatal(err)
	}
	if chat.calls != 0 {
		t.Fatalf("no endpoint cost %d call(s), want 0", chat.calls)
	}
	if rep.LLMNote != "AI 端点未配置：无法编译" {
		t.Fatalf("no-endpoint note = %q", rep.LLMNote)
	}
}
