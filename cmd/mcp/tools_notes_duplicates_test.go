package main

import (
	"encoding/json"
	"testing"
)

// repotory_notes_duplicates is a READ-ONLY proposal surface: it names pairs
// and the terms they share, and nothing it returns can merge, edit or delete.
// These tests exercise it the way an agent does — through the registered tool
// table over a real in-memory database.

func TestNotesDuplicatesEmptyIsAnArray(t *testing.T) {
	ts := newTestServer(t)
	pid := ts.seedProject("alpha", "/home/me/alpha")
	if _, err := ts.svc.CreateNoteWithMeta(pid, "Only note", "nothing repeats here", "", "knowledge", "manual"); err != nil {
		t.Fatal(err)
	}

	text := ts.text(ts.call("repotory_notes_duplicates", map[string]any{"project_id": float64(pid)}))
	// An empty list is [] and never null: a client that cannot tell "no
	// duplicates" from "the field is absent" reports the second as a broken
	// contract.
	if text != "[]" {
		t.Fatalf("empty duplicate list = %q, want []", text)
	}
}

func TestNotesDuplicatesFindsARepeatedNote(t *testing.T) {
	ts := newTestServer(t)
	pid := ts.seedProject("alpha", "/home/me/alpha")
	body := "SQLite 迁移前先查 pragma table_info，确认列类型和默认值，再写 ALTER TABLE 并补回填脚本。" +
		"回滚路径是备份文件而不是逆向迁移，因为逆向迁移从未被真正跑过。"
	if _, err := ts.svc.CreateNoteWithMeta(pid, "Migration checklist", body, "", "knowledge", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.svc.CreateNoteWithMeta(pid, "Migration checklist (copy)", body, "", "knowledge", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.svc.CreateNoteWithMeta(pid, "Unrelated", "heatmap colors and goal ring sizing", "", "idea", "manual"); err != nil {
		t.Fatal(err)
	}

	text := ts.text(ts.call("repotory_notes_duplicates", map[string]any{"project_id": float64(pid)}))
	var pairs []struct {
		A struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"a"`
		B struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"b"`
		Score  float64  `json:"score"`
		Shared []string `json:"shared"`
	}
	if err := json.Unmarshal([]byte(text), &pairs); err != nil {
		t.Fatalf("duplicates are not JSON: %v\n%s", err, text)
	}
	if len(pairs) != 1 {
		t.Fatalf("want exactly the one repeated pair, got %d: %s", len(pairs), text)
	}
	pair := pairs[0]
	if pair.A.Title == pair.B.Title {
		t.Fatalf("a pair names two different notes: %s", text)
	}
	if pair.Score < 0.6 {
		t.Fatalf("score = %v, want >= 0.6", pair.Score)
	}
	// The shared terms are the evidence a human judges the pair by; a bare
	// percentage is a number nobody can check.
	if len(pair.Shared) == 0 {
		t.Fatalf("a pair carries no shared terms: %s", text)
	}
}

// path resolves server-side the same way repotory_context's does, so an agent
// that knows only its cwd never has to guess a numeric project id.
func TestNotesDuplicatesResolvesAWorkspacePath(t *testing.T) {
	ts := newTestServer(t)
	pid := ts.seedProject("alpha", "/home/me/alpha")
	body := "the retry budget is three attempts with jittered backoff and a hard ceiling of two seconds"
	for _, title := range []string{"Retry budget", "Retry budget again"} {
		if _, err := ts.svc.CreateNoteWithMeta(pid, title, body, "", "knowledge", "manual"); err != nil {
			t.Fatal(err)
		}
	}

	text := ts.text(ts.call("repotory_notes_duplicates", map[string]any{"path": "/home/me/alpha"}))
	if text == "[]" || text == "" {
		t.Fatalf("path did not resolve to the project: %s", text)
	}
}
