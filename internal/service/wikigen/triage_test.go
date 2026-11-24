package wikigen

import (
	"context"
	"strings"
	"testing"

	"repotory/internal/db"
)

// Deliberately free of decision markers: the transcript must be refused for
// its shape, not rescued by an unrelated acronym in the body.
const triageBody = "The gateway owns routing and the retry whitelist stays in force across all projects."

func TestCompileTriageRules(t *testing.T) {
	cases := []struct {
		name   string
		title  string
		kind   string
		body   string
		keep   bool
		reason string
	}{
		{"knowledge with substance", "Gateway routing", "knowledge", triageBody, true, ""},
		{"manual note", "Retry policy", "manual", triageBody, true, ""},
		{"session log", "opencode · session", "log", triageBody, false, "kind=log"},
		{"environment context", "<environment_context> · 019f7f28", "log", triageBody, false, "标题匹配"},
		{"AGENTS snapshot", "# AGENTS.md instructions for /Users/x", "log", triageBody, false, "标题匹配"},
		{"handoff capture", "Session Handoff · 2026-11-20", "log", triageBody, false, "标题匹配"},
		{"log carrying a decision", "<environment_context> · decision", "log", "决策: retry whitelist stays", true, ""},
		{"log with ADR mark", "session 42", "log", "设计决策: use weighted RRF", true, ""},
		{"short but real note", "Gateway", "knowledge", "The gateway owns routing.", true, ""},
		{"empty kind is not a transcript", "Gateway", "", triageBody, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			note := &db.Note{Title: c.title, Kind: c.kind, Content: c.body}
			got, reason := CompileTriage(note)
			if got != c.keep {
				t.Errorf("keep = %v, want %v (reason %q)", got, c.keep, reason)
			}
			if !got && !strings.Contains(reason, c.reason) {
				t.Errorf("reason = %q, want substring %q", reason, c.reason)
			}
		})
	}
}

func TestTriageNotesDisabledIsPassThrough(t *testing.T) {
	notes := []db.Note{
		{Title: "<environment_context> · 1", Kind: "log", Content: triageBody},
		{Title: "Gateway", Kind: "knowledge", Content: triageBody},
	}
	kept, skipped := TriageNotes(notes, false)
	if len(kept) != 2 || skipped != 0 {
		t.Fatalf("disabled: kept=%d skipped=%d, want 2/0", len(kept), skipped)
	}
	kept, skipped = TriageNotes(notes, true)
	if len(kept) != 1 || skipped != 1 || kept[0].Title != "Gateway" {
		t.Fatalf("enabled: kept=%v skipped=%d", kept, skipped)
	}
}

// The kill switch is the whole contract of this gate: absent / empty / garbage
// all mean ON, only an explicit "0" turns routing off.
func TestCompileTriageEnabledDefaultsOn(t *testing.T) {
	database := setupKernelDB(t)
	if !CompileTriageEnabled(database) {
		t.Fatal("default should be ON")
	}
	db.SetConfig(database, KeyCompileTriage, "garbage")
	if !CompileTriageEnabled(database) {
		t.Fatal("garbage should still be ON")
	}
	db.SetConfig(database, KeyCompileTriage, "0")
	if CompileTriageEnabled(database) {
		t.Fatal("explicit 0 should be OFF")
	}
}

// A skipped note must NOT reach the model — this is the cost win, asserted at
// the seam rather than inferred from a report string.
func TestCompileNoteSkipsTranscriptBeforeModelCall(t *testing.T) {
	database := setupKernelDB(t)
	enableCompile(t, database)
	projectID := seedProject(t, database)
	note, err := db.CreateNoteEx(database, projectID, "<environment_context> · 1", triageBody, "", "log", "import:codex")
	if err != nil {
		t.Fatal(err)
	}
	chat := &scriptedChat{}
	rep, err := CompileNote(context.Background(), database, chat, note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if chat.calls != 0 {
		t.Errorf("model calls = %d, want 0", chat.calls)
	}
	if !strings.Contains(rep.LLMNote, "分流跳过") {
		t.Errorf("note = %q, want the triage reason", rep.LLMNote)
	}
}

func TestCompileNoteTriageOffCallsModel(t *testing.T) {
	database := setupKernelDB(t)
	enableCompile(t, database)
	db.SetConfig(database, KeyCompileTriage, "0")
	projectID := seedProject(t, database)
	note, err := db.CreateNoteEx(database, projectID, "<environment_context> · 1", triageBody, "", "log", "import:codex")
	if err != nil {
		t.Fatal(err)
	}
	chat := &scriptedChat{replies: []Reply{{Text: "[]"}}}
	if _, err := CompileNote(context.Background(), database, chat, note.ID); err != nil {
		t.Fatal(err)
	}
	if chat.calls != 1 {
		t.Errorf("model calls = %d, want 1 with triage off", chat.calls)
	}
}
