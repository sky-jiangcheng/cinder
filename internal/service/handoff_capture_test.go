package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"repotory/internal/db"
	"repotory/internal/importers/claude"
)

func TestHandoffFromSessionFillsSections(t *testing.T) {
	sess := claude.Session{
		SessionID:         "abcdef123456789",
		GitBranch:         "main",
		LastAssistantText: "Refactored the parser; tests green.",
		ToolsUsed:         []string{"Edit", "Bash"},
	}
	in := handoffFromClaudeSession(sess)
	if in.Agent != "claude-code" {
		t.Errorf("agent = %q", in.Agent)
	}
	if in.Tags != "auto-captured" {
		t.Errorf("tags = %q", in.Tags)
	}
	if in.Summary != "Refactored the parser; tests green." {
		t.Errorf("summary = %q", in.Summary)
	}
	// Must satisfy CreateHandoffNote's "at least one section" rule.
	if len(in.Changes) == 0 {
		t.Fatal("Changes empty; handoff would be rejected")
	}
	joined := strings.Join(in.Changes, "\n")
	if !strings.Contains(joined, "abcdef12") || !strings.Contains(joined, "main") {
		t.Errorf("changes header missing id/branch: %q", joined)
	}
	if !strings.Contains(joined, "使用工具 Edit") || !strings.Contains(joined, "使用工具 Bash") {
		t.Errorf("changes missing tools: %q", joined)
	}
}

func TestHandoffFromSessionFallsBackToPrompt(t *testing.T) {
	// No assistant text: summary falls back to the first prompt, and Changes
	// still carries the header line so the handoff is valid.
	in := handoffFromClaudeSession(claude.Session{FirstUserPrompt: "Build the cache", SessionID: "sid"})
	if in.Summary != "Build the cache" {
		t.Errorf("summary fallback = %q", in.Summary)
	}
	if len(in.Changes) == 0 {
		t.Error("expected a header change line even with no tools")
	}
}

func TestHandoffFromSessionCapsToolItems(t *testing.T) {
	tools := make([]string, 50)
	for i := range tools {
		tools[i] = "t" + string(rune('a'+i%26)) + strings.Repeat("x", i)
	}
	in := handoffFromClaudeSession(claude.Session{LastAssistantText: "s", ToolsUsed: tools})
	if len(in.Changes) > maxHandoffItems {
		t.Fatalf("changes %d exceeds max %d", len(in.Changes), maxHandoffItems)
	}
	if !strings.Contains(strings.Join(in.Changes, "\n"), "共 50 种工具") {
		t.Error("expected overflow summary bullet")
	}
}

func TestTruncateBytesRuneBoundary(t *testing.T) {
	cjk := strings.Repeat("汉", 100) // 3 bytes each = 300 bytes
	got := truncateBytes(cjk, 10)   // 10 is not a rune boundary
	if len(got) > 10 {
		t.Fatalf("len = %d > max 10", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("cut produced invalid UTF-8: %q", got)
	}
	if len(got)%3 != 0 {
		t.Errorf("expected whole runes, got %d bytes", len(got))
	}
	// Under max is a no-op.
	if truncateBytes("abc", 10) != "abc" {
		t.Error("short string should be unchanged")
	}
}

const hookSession = `{"type":"session_meta","sessionId":"sess-hook","timestamp":"2026-01-02T00:00:00Z","cwd":"/Users/tester/ProjX","gitBranch":"main"}
{"type":"user","sessionId":"sess-hook","message":{"role":"user","content":[{"type":"text","text":"fix the thing"}]}}
{"type":"assistant","sessionId":"sess-hook","message":{"role":"assistant","content":[{"type":"text","text":"done and tested"}]}}
`

func TestCaptureClaudeSessionByCwd(t *testing.T) {
	svc, _ := setupService(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Windows reads %USERPROFILE%, not HOME, so both must point at the fake home
	// or the capture reads the developer's real ~/.claude.
	t.Setenv("USERPROFILE", home)
	root := "/Users/tester/ProjX"
	pid := seedProject(t, svc.db, "ProjX", root)
	if pid == 0 {
		t.Fatal("seed project failed")
	}
	// Latest session transcript at the project's slug dir.
	slug := strings.ReplaceAll(root, "/", "-")
	dir := filepath.Join(home, ".claude", "projects", slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sess.jsonl"), []byte(hookSession), 0o644); err != nil {
		t.Fatal(err)
	}

	// Disabled by default -> capture refuses (privacy gate).
	if _, err := svc.CaptureClaudeSessionByCwd(root); err == nil {
		t.Fatal("expected error when claude_session_capture is off")
	}
	// No matching project -> error, independent of the gate.
	if err := svc.UpdateConfig("claude_session_capture", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CaptureClaudeSessionByCwd("/no/where/NoSuchProject"); err == nil {
		t.Error("expected error for unmatched cwd")
	}
	// A cwd that merely ends in the project's short name must NOT resolve:
	// ResolveProjectByPath matches real paths, not last-segment substrings.
	if _, err := svc.CaptureClaudeSessionByCwd("/whatever/ProjX"); err == nil {
		t.Error("short-segment cwd must not resolve to the project")
	}
	// A shared ancestor is ambiguous: refuse rather than guess a project.
	if _, err := svc.CaptureClaudeSessionByCwd("/Users/tester"); err == nil {
		t.Error("ambiguous cwd must not guess a project")
	}
	// Enabled + exact project path -> handoff written.
	res, err := svc.CaptureClaudeSessionByCwd(root)
	if err != nil {
		t.Fatalf("capture by cwd: %v", err)
	}
	if res.NoteID == 0 {
		t.Errorf("expected a persisted note, got %+v", res)
	}
}

func TestCaptureAgentSessionByCwdWorkBuddy(t *testing.T) {
	// The multi-agent one-shot path: same gate, same cwd-vs-root lookup, but
	// the session comes from WorkBuddy's store and the note records the
	// WorkBuddy agent name.
	svc, _ := setupService(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := "/Users/tester/ProjX"
	pid := seedProject(t, svc.db, "ProjX", root)
	if pid == 0 {
		t.Fatal("seed project failed")
	}
	dir := filepath.Join(home, ".workbuddy", "projects", strings.TrimPrefix(strings.ReplaceAll(root, "/", "-"), "-"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const transcript = `{"type":"session-meta","sessionId":"wb-t1"}
{"type":"message","role":"user","content":[{"type":"input_text","text":"wb cwd 捕获"}]}
{"type":"message","role":"assistant","content":[{"type":"output_text","text":"写好了"}]}
`
	if err := os.WriteFile(filepath.Join(dir, "wb-t1.jsonl"), []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateConfig("claude_session_capture", "1"); err != nil {
		t.Fatal(err)
	}
	res, err := svc.CaptureAgentSessionByCwd("workbuddy", root)
	if err != nil {
		t.Fatalf("capture by cwd (workbuddy): %v", err)
	}
	if res.NoteID == 0 {
		t.Fatalf("expected a persisted note, got %+v", res)
	}
	var content string
	if err := svc.db.QueryRow("SELECT content FROM project_notes WHERE id = ?", res.NoteID).Scan(&content); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "Written by `workbuddy`") {
		t.Errorf("note content missing workbuddy agent line: %q", content[:200])
	}
	// Unknown agents fail loudly, not silently.
	if _, err := svc.CaptureAgentSessionByCwd("cursor", root); err == nil {
		t.Error("expected an error for an unknown agent")
	}
	// Empty agent keeps the hook's historical default: claude.
	if _, err := svc.CaptureAgentSessionByCwd("", root); err != nil && !strings.Contains(err.Error(), "no capturable") {
		t.Errorf("empty agent should behave as claude, got: %v", err)
	}
}

func TestCaptureClaudeSessionByCwdLooksUpTranscriptByCwd(t *testing.T) {
	// The live-data shape that broke the hook: a repository row points at a
	// container auto-group project whose root is the repo's PARENT, so tier-1
	// resolution attributes the cwd to a project whose root has no slug dir.
	// The transcript must be looked up by the cwd itself, and the note still
	// lands in the resolved project's bucket.
	svc, _ := setupService(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	repo := "/Users/tester/MiddlePlatform/midplatform-data-monitor"
	container := "/Users/tester/MiddlePlatform"
	cpid := seedProject(t, svc.db, "MiddlePlatform", container)
	if cpid == 0 {
		t.Fatal("seed container project failed")
	}
	if err := db.UpsertRepository(svc.db, repo, cpid); err != nil {
		t.Fatal(err)
	}

	slug := strings.ReplaceAll(repo, "/", "-")
	dir := filepath.Join(home, ".claude", "projects", slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sess.jsonl"), []byte(hookSession), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateConfig("claude_session_capture", "1"); err != nil {
		t.Fatal(err)
	}

	res, err := svc.CaptureClaudeSessionByCwd(repo)
	if err != nil {
		t.Fatalf("capture with repo-under-container cwd: %v", err)
	}
	if res.NoteID == 0 {
		t.Fatalf("expected a persisted note, got %+v", res)
	}
	var projID int64
	if err := svc.db.QueryRow("SELECT project_id FROM project_notes WHERE id = ?", res.NoteID).Scan(&projID); err != nil {
		t.Fatal(err)
	}
	if projID != cpid {
		t.Errorf("note %d attributed to project %d, want container %d", res.NoteID, projID, cpid)
	}
}
