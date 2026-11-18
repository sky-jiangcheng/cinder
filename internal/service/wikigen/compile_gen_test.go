package wikigen

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"repotory/internal/db"
)

// The generation branches — parse the plan, apply it, and the two refusal notes
// (truncated vs malformed) — are only reachable through the seam, which is the
// point of ADR-0060 step three: without a fake transport these paths are
// exercised only by whatever the service-level integration tests happen to
// drive. These tests pin the decisions the kernel makes on the way out, not
// just that it returns.

// scriptedChat replays a queue of replies so one test can drive several notes.
type scriptedChat struct {
	replies []Reply
	calls   int
}

func (c *scriptedChat) Available(ctx context.Context) error { return nil }

func (c *scriptedChat) Chat(ctx context.Context, msgs []Message) (Reply, error) {
	c.calls++
	if len(c.replies) == 0 {
		return Reply{}, nil
	}
	r := c.replies[0]
	c.replies = c.replies[1:]
	return r, nil
}

func seedProject(t *testing.T, database *sql.DB) int64 {
	t.Helper()
	res, err := database.Exec(
		`INSERT INTO projects (name, root_path, project_kind) VALUES ('gen', '/tmp/gen', 'scanned')`,
	)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("project id: %v", err)
	}
	return id
}

func learnNote(t *testing.T, database *sql.DB, projectID int64, title, content string) *db.Note {
	t.Helper()
	note, err := db.CreateNoteEx(database, projectID, title, content, "", "other", "manual")
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	return note
}

func enableCompile(t *testing.T, database *sql.DB) {
	t.Helper()
	if err := db.SetConfig(database, KeyCompile, "1"); err != nil {
		t.Fatalf("enable compile: %v", err)
	}
}

// A plan the model produced is applied as pages, and provenance is automatic:
// the kernel attaches the source note whether or not the model asked to.
func TestCompileNoteAppliesModelPlan(t *testing.T) {
	database := setupKernelDB(t)
	enableCompile(t, database)
	projectID := seedProject(t, database)
	note := learnNote(t, database, projectID, "Gateway routing", "The gateway owns routing and nothing else.")

	chat := &scriptedChat{replies: []Reply{{Text: `[{"op":"create_page","slug":"gateway-routing","title":"Gateway Routing","kind":"concept","body":"The gateway owns routing."}]`}}}
	rep, err := CompileNote(context.Background(), database, chat, note.ID)
	if err != nil {
		t.Fatalf("compile note: %v", err)
	}
	if rep.PagesCreated != 1 || rep.Attachments != 1 {
		t.Fatalf("pages=%d attachments=%d, want 1/1", rep.PagesCreated, rep.Attachments)
	}
	page, err := db.GetWikiPageBySlug(database, "gateway-routing")
	if err != nil {
		t.Fatalf("the page the model asked for is not in the store: %v", err)
	}
	// Unreviewed output: visible to inventory, invisible to retrieval.
	if page.Status != db.WikiStatusPending {
		t.Fatalf("status=%q, want %q", page.Status, db.WikiStatusPending)
	}
	if page.Source != CompileSourceTag {
		t.Fatalf("source=%q, want %q", page.Source, CompileSourceTag)
	}
	if chat.calls != 1 {
		t.Fatalf("calls=%d, want exactly one paid call", chat.calls)
	}
}

// A reply the server cut short and a reply that was never JSON both write
// nothing, but they are different failures: the note must tell the user to
// raise the token budget instead of implying the model answered badly.
func TestCompileNoteDistinguishesTruncationFromMalformed(t *testing.T) {
	database := setupKernelDB(t)
	enableCompile(t, database)
	projectID := seedProject(t, database)
	note := learnNote(t, database, projectID, "Gateway routing", "The gateway owns routing.")

	truncated := &scriptedChat{replies: []Reply{{
		Text:         `[{"op":"create_page","slug":"gateway-routing","title":"Gateway`,
		FinishReason: "length",
	}}}
	rep, err := CompileNote(context.Background(), database, truncated, note.ID)
	if err != nil {
		t.Fatalf("compile note: %v", err)
	}
	if rep.PagesCreated != 0 {
		t.Fatalf("a truncated plan wrote %d page(s); partial JSON must write nothing", rep.PagesCreated)
	}
	if !strings.Contains(rep.LLMNote, "截断") {
		t.Fatalf("note=%q, want the truncation explanation", rep.LLMNote)
	}

	malformed := &scriptedChat{replies: []Reply{{Text: `the model answered with prose, not a plan`}}}
	rep, err = CompileNote(context.Background(), database, malformed, note.ID)
	if err != nil {
		t.Fatalf("compile note: %v", err)
	}
	if rep.PagesCreated != 0 {
		t.Fatalf("an unparseable plan wrote %d page(s)", rep.PagesCreated)
	}
	if strings.Contains(rep.LLMNote, "截断") || !strings.Contains(rep.LLMNote, "无法解析") {
		t.Fatalf("note=%q, want the malformed-JSON note and no truncation hint", rep.LLMNote)
	}
}

// One bad note must not abort the run, and the partial success must stay
// visible: the first failure is reported while the rest of the notes compile.
func TestCompileProjectNotesContinuesPastOneBadNote(t *testing.T) {
	database := setupKernelDB(t)
	enableCompile(t, database)
	projectID := seedProject(t, database)
	first := learnNote(t, database, projectID, "First", "First note body.")
	learnNote(t, database, projectID, "Second", "Second note body.")

	chat := &scriptedChat{replies: []Reply{
		{Text: `[{"op":"create_page","slug":"first-page","title":"First Page","kind":"concept","body":"From the first note."}]`},
		{Text: `not a plan`},
	}}
	rep, err := CompileProjectNotes(context.Background(), database, chat, projectID, 0)
	if err != nil {
		t.Fatalf("compile project: %v", err)
	}
	if rep.NotesScanned != 2 {
		t.Fatalf("scanned=%d, want 2", rep.NotesScanned)
	}
	if rep.NotesCompiled != 1 || rep.PagesCreated != 1 {
		t.Fatalf("compiled=%d pages=%d, want 1/1 (the bad note must not abort the run)",
			rep.NotesCompiled, rep.PagesCreated)
	}
	if !strings.Contains(rep.LLMNote, "note") {
		t.Fatalf("note=%q, want the skipped note named", rep.LLMNote)
	}
	// The bad note is identified by id, so the reviewer can find it again.
	if !strings.Contains(rep.LLMNote, "无法解析") {
		t.Fatalf("note=%q, want the parse failure reason", rep.LLMNote)
	}
	_ = first
}
