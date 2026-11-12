package wikilint

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"repotory/internal/db"
	"repotory/internal/service/wikigen"
)

// The model half speaks the wikigen.Chat seam, so it can be tested without a
// service and without an HTTP server: a counting fake stands in for the
// transport. These tests pin the same property the wikigen kernels do — the gate
// order means "off / no endpoint" is zero outbound calls — at the lint seam.

type countingChat struct {
	available bool
	calls     int
}

func (c *countingChat) Available(ctx context.Context) error {
	if !c.available {
		return wikigen.ErrNotConfigured
	}
	return nil
}

func (c *countingChat) Chat(ctx context.Context, msgs []wikigen.Message) (wikigen.Reply, error) {
	c.calls++
	return wikigen.Reply{Text: "[]"}, nil
}

func setupLintDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	// Two pages, so the model half's "fewer than 2 pages is meaningless" early
	// return does not fire and the gate under test is actually reached.
	for _, slug := range []string{"alpha", "beta"} {
		if _, err := db.CreateWikiPageAs(database, db.WikiKindConcept, slug, slug, 1,
			"body text long enough to clear the thin-page threshold in this check", db.WikiStatusApproved, "human"); err != nil {
			t.Fatal(err)
		}
	}
	return database
}

func TestRunLLMHalfGateOffNoCalls(t *testing.T) {
	database := setupLintDB(t)
	chat := &countingChat{available: true}

	rep, err := Run(context.Background(), database, chat, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if chat.calls != 0 {
		t.Fatalf("gate off cost %d call(s), want 0", chat.calls)
	}
	if !strings.Contains(rep.LLMNote, wikigen.KeyLintLLM) {
		t.Fatalf("the note must name the gate that is off, got %q", rep.LLMNote)
	}
	if rep.LLMRan {
		t.Fatal("LLMRan must stay false when no model half ran")
	}
}

func TestRunLLMHalfNoEndpointNoCalls(t *testing.T) {
	database := setupLintDB(t)
	if err := db.SetConfig(database, wikigen.KeyLintLLM, "1"); err != nil {
		t.Fatal(err)
	}
	chat := &countingChat{available: false}

	rep, err := Run(context.Background(), database, chat, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if chat.calls != 0 {
		t.Fatalf("no endpoint cost %d call(s), want 0", chat.calls)
	}
	if !strings.Contains(rep.LLMNote, "AI 端点未配置") {
		t.Fatalf("the note must say the endpoint is missing, got %q", rep.LLMNote)
	}
}
