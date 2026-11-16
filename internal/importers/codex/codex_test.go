package codex

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"repotory/internal/core/plugin"
	"repotory/internal/db"
	"repotory/internal/importers/imptest"
	"repotory/internal/importers/memsrc"
)

// fakeHome points os.UserHomeDir at an empty temp dir.
func fakeHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	imptest.SetHome(t, dir)
	return dir
}

// sampleRollout copies a rollout sample from testdata/ into dest. The samples
// live as files (ADR-0011 decision 5) so a drift in the on-disk format shows
// up as a diff on the sample, next to the test that broke — an inline
// fixture only shows a string that changed.
func sampleRollout(t *testing.T, sample, dest string) {
	t.Helper()
	imptest.CopySample(t, filepath.Join("testdata", sample), dest)
}

func TestExtractText(t *testing.T) {
	arr := []byte(`[{"type":"output_text","text":"a"},{"type":"refusal","text":""},{"type":"text","text":"b"}]`)
	if got := extractText(arr); got != "a\nb" {
		t.Errorf("array extract = %q, want %q", got, "a\nb")
	}
	if got := extractText([]byte(`"hello"`)); got != "hello" {
		t.Errorf("string extract = %q", got)
	}
	if got := extractText(nil); got != "" {
		t.Errorf("nil extract = %q", got)
	}
}

func TestParseRolloutFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-fixture.jsonl")
	sampleRollout(t, "rollout.jsonl", path)
	p, err := parseRolloutFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.cwd != "/Users/u/Workspace/Foo" {
		t.Errorf("cwd = %q", p.cwd)
	}
	if p.sessionID != "019d0a9b-8c9a-7f03" {
		t.Errorf("sessionID = %q", p.sessionID)
	}
	if p.date != "2026-03-20" {
		t.Errorf("date = %q", p.date)
	}
	if p.firstUser != "Fix the flaky test in auth" {
		t.Errorf("firstUser = %q", p.firstUser)
	}
	// The LAST assistant message (bare-string form) wins over the earlier one.
	if p.lastAssistant != "bare string reply" {
		t.Errorf("lastAssistant = %q", p.lastAssistant)
	}
}

func TestTitleForStableAndUnique(t *testing.T) {
	p := parsed{sessionID: "019d0a9b-8c9a-7f03", firstUser: "Fix the flaky test in auth", date: "2026-03-20"}
	got := titleFor(p)
	if got != "Fix the flaky test in auth · 7f03" {
		t.Errorf("title = %q", got)
	}
	if titleFor(p) != got {
		t.Error("titleFor must be deterministic for idempotent upsert")
	}
	if titleFor(parsed{}) != "Codex 会话" {
		t.Errorf("empty parsed title = %q", titleFor(parsed{}))
	}
}

// The old formula keyed the id on its first 8 hex characters, which in a
// UUIDv7 is the MILLISECOND TIMESTAMP: two sessions started in the same
// ~50-day window share it, and Codex opens most sessions with the same
// injected envelope — so the live library grew two notes titled
// "<environment_context> · 019f7f28" for unrelated sessions (2026-11-16).
// The new formula keys on the random segment; same timestamp head, same
// first line, different titles.
func TestTitleForResistsTimestampCollision(t *testing.T) {
	a := parsed{sessionID: "019f7f28-1121-7451-8596-496babdbe598", firstUser: "hello"}
	b := parsed{sessionID: "019f7f28-99ab-7cde-a123-456789abcdef01", firstUser: "hello"}
	if titleFor(a) == titleFor(b) {
		t.Errorf("sessions sharing the timestamp head still collide: %q", titleFor(a))
	}
	if !strings.Contains(titleFor(a), "7451") || !strings.Contains(titleFor(b), "7cde") {
		t.Errorf("title must carry the random segment: %q / %q", titleFor(a), titleFor(b))
	}
}

// LegacyTitles carries the pre-2.39 title for the same session file so the
// runtime renames the old row instead of duplicating it. Both shapes the old
// formula could have produced (raw first line / envelope-stripped first line)
// must be listed when they differ.
func TestLegacyCandidatesCoverBothRevisions(t *testing.T) {
	p := parsed{
		sessionID:    "019f7f28-1121-7451-8596-496babdbe598",
		firstUserRaw: "<environment_context>\n  <cwd>/a</cwd>\n</environment_context>\n真正的指令",
		firstUser:    "真正的指令",
	}
	cands := legacyCandidates(p)
	if len(cands) != 2 {
		t.Fatalf("candidates = %v, want the raw-line and stripped-line legacy shapes", cands)
	}
	if cands[0] != "<environment_context> · 019f7f28" {
		t.Errorf("first candidate = %q, want the pre-2026-10-21 title", cands[0])
	}
	if cands[1] != "真正的指令 · 019f7f28" {
		t.Errorf("second candidate = %q, want the post-2026-10-21 legacy title", cands[1])
	}
	// A session whose first message was already a real prompt has one shape.
	q := parsed{sessionID: "019d0a9b-8c9a-7f03", firstUserRaw: "Fix the flaky test", firstUser: "Fix the flaky test"}
	if got := legacyCandidates(q); len(got) != 1 || got[0] != "Fix the flaky test · 019d0a9b" {
		t.Errorf("single-shape candidates = %v", got)
	}
}

func TestStripBoilerplate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"environment context only", "<environment_context>\n  <cwd>/a</cwd>\n</environment_context>", ""},
		{"plain prompt untouched", "Fix the flaky test", "Fix the flaky test"},
		{"agents heading alone", "# AGENTS.md instructions for /a\n", ""},
		{
			"heading then instructions then context",
			"# AGENTS.md instructions for /a\n\n<INSTRUCTIONS>\nrules\n</INSTRUCTIONS>\n<environment_context>\n  <cwd>/a</cwd>\n</environment_context>\n\n真正的指令",
			"真正的指令",
		},
		{"unclosed envelope is left alone", "<environment_context>\n  <cwd>/a", "<environment_context>\n  <cwd>/a"},
		{"mid-text tag is not stripped", "see the <user_instructions> docs for details", "see the <user_instructions> docs for details"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripBoilerplate(tc.in); got != tc.want {
				t.Errorf("stripBoilerplate = %q, want %q", got, tc.want)
			}
		})
	}
}

// A session's first user message is usually an injected envelope, not an
// instruction. The importer must keep looking until it finds a real prompt —
// otherwise every note is titled "<environment_context> · <id>" and its body
// is a sandbox-permission XML dump (observed on all 50 sampled notes).
func TestParseRolloutFileSkipsInjectedEnvelopes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-envelope.jsonl")
	sampleRollout(t, "rollout-envelope.jsonl", path)
	p, err := parseRolloutFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.HasPrefix(p.firstUser, "真正的指令") {
		t.Errorf("firstUser = %q, want the real instruction", p.firstUser)
	}
	if strings.Contains(p.firstUser, "environment_context") || strings.Contains(p.firstUser, "AGENTS.md") {
		t.Errorf("firstUser still carries an envelope: %q", p.firstUser)
	}
	if title := titleFor(p); strings.HasPrefix(title, "<") || strings.HasPrefix(title, "#") {
		t.Errorf("title = %q, must not be built from an envelope tag", title)
	}
}

func TestImportProducesNote(t *testing.T) {
	home := fakeHome(t)
	sess := filepath.Join(home, ".codex", "sessions", "2026", "03", "20")
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	sampleRollout(t, "rollout.jsonl", filepath.Join(sess, "rollout-a.jsonl"))
	// A non-jsonl file and a message-less file must be ignored.
	if err := os.WriteFile(filepath.Join(sess, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	docs, err := New(database).Import()
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}
	d := docs[0]
	if d.Source != "codex" || d.Kind != "log" {
		t.Errorf("source/kind = %q/%q", d.Source, d.Kind)
	}
	if !strings.Contains(d.Content, "/Users/u/Workspace/Foo") {
		t.Errorf("content missing cwd: %q", d.Content)
	}
	if !strings.Contains(d.Content, "Fix the flaky test in auth") {
		t.Errorf("content missing prompt: %q", d.Content)
	}
	if d.ProjectID != 0 {
		t.Errorf("empty db should match no project, got %d", d.ProjectID)
	}
}

func TestImportMissingDirIsNoop(t *testing.T) {
	fakeHome(t)
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	docs, err := New(database).Import()
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected no docs, got %d", len(docs))
	}
}

// The project-matching used by the importer is the shared matcher; verify it
// resolves a session cwd's last path segment to a project.
func TestMatchProjectViaSharedMatcher(t *testing.T) {
	projects := []db.Project{{ID: 7, Name: "Foo"}}
	if got := memsrc.MatchProject(memsrc.LastPathSegment("/Users/u/Workspace/Foo"), projects, nil); got != 7 {
		t.Errorf("match = %d, want 7", got)
	}
}

func TestImporterImplementsInterface(t *testing.T) {
	var _ plugin.KnowledgeImporter = New((*sql.DB)(nil))
}
