package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

// Real-format fixtures, one per store, each reduced to the fields the parsers
// rely on. Formats are not published contracts; these pin the shapes observed
// on 2026-11-17 (WorkBuddy CodeBuddy-family envelope, Qoder's Claude envelope,
// Trae's memory summaries, DSH's zstd event log).

func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestLatestWorkBuddyParsesItsEnvelope(t *testing.T) {
	home := fakeHome(t)
	cwd := "/Users/tester/Script"
	dir := filepath.Join(home, ".workbuddy", "projects", strings.TrimPrefix(strings.ReplaceAll(cwd, "/", "-"), "-"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const transcript = `{"type":"session-meta","id":"01a","sessionId":"wb-1","timestamp":1794898063623}
{"id":"01b","timestamp":1794898058107,"type":"message","role":"user","content":[{"type":"input_text","text":"检查一下 handoff 闭环"}]}
{"id":"01c","type":"message","role":"assistant","content":[{"type":"output_text","text":"找到两边的 handoff 链路了。"}]}
{"id":"01d","type":"message","role":"user","content":[{"type":"input_text","text":"<system-reminder data-role=\"user-context\">bootstrap noise</system-reminder>"}]}
`
	if err := os.WriteFile(filepath.Join(dir, "wb-1.jsonl"), []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, ok, err := LatestForCwd(cwd, WorkBuddy)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if sess.SessionID != "wb-1" {
		t.Errorf("session id = %q", sess.SessionID)
	}
	if !strings.Contains(sess.FirstUserPrompt, "handoff 闭环") {
		t.Errorf("first prompt = %q", sess.FirstUserPrompt)
	}
	if sess.LastAssistantText != "找到两边的 handoff 链路了。" {
		t.Errorf("assistant = %q", sess.LastAssistantText)
	}
	if sess.HasContent() == false {
		t.Error("expected content")
	}
}

func TestLatestQoderReusesClaudeParserAndChecksCwd(t *testing.T) {
	home := fakeHome(t)
	cwd := "/Users/tester/Script"
	dir := filepath.Join(home, ".qoder", "projects", "-"+strings.TrimPrefix(strings.ReplaceAll(cwd, "/", "-"), "-"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const transcript = `{"type":"workspace-directories","sessionId":"q1","directories":["/Users/tester/Script"]}
{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"text","text":"跑一下 Qoder 的捕获"}]},"origin":{"kind":"human"},"cwd":"/Users/tester/Script","isSidechain":false}
{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"text","text":"捕获链路通了。"}]},"cwd":"/Users/tester/Script","isSidechain":false}
{"type":"user","uuid":"u2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"output"}]},"cwd":"/Users/tester/Script","isSidechain":false}
`
	if err := os.WriteFile(filepath.Join(dir, "q1.jsonl"), []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	// A sibling workspace whose slug extends ours: its sessions must NOT be
	// attributed to the shorter cwd's prefix.
	sibling := filepath.Join(home, ".qoder", "projects", "-"+strings.TrimPrefix(strings.ReplaceAll(cwd, "/", "-"), "-")+"-RepoNest")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	const siblingTranscript = `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"RepoNest 的会话"}]},"cwd":"/Users/tester/Script/RepoNest","isSidechain":false}`
	if err := os.WriteFile(filepath.Join(sibling, "rn.jsonl"), []byte(siblingTranscript), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(sibling, "rn.jsonl"), time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	sess, ok, err := LatestForCwd(cwd, Qoder)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if !strings.Contains(sess.FirstUserPrompt, "Qoder 的捕获") {
		t.Errorf("first prompt = %q", sess.FirstUserPrompt)
	}
	if sess.Cwd != cwd {
		t.Errorf("cwd = %q", sess.Cwd)
	}
	if strings.Contains(sess.FirstUserPrompt, "RepoNest") {
		t.Errorf("sibling session leaked into parent cwd: %q", sess.FirstUserPrompt)
	}
}

func TestLatestTraeMatchesWorkspaceSuffixAndNotChildren(t *testing.T) {
	home := fakeHome(t)
	cwd := "/Users/tester/Script"
	// Real Trae slug shape: "-<dashed path>--p2-<hash>".
	dir := filepath.Join(home, ".trae-cn", "memory", "projects",
		"-"+strings.TrimPrefix(strings.ReplaceAll(cwd, "/", "-"), "-")+"--p2-0329e698a95a2b1b4d8c")
	if err := os.MkdirAll(filepath.Join(dir, "20261112"), 0o755); err != nil {
		t.Fatal(err)
	}
	const memory = `{"intent":"将评估发现的缺口补充到仓库的追踪体系中","actions":["探索并确认仓库现有追踪惯例","在roadmap.md中新增3条工程缺口"],"outcome":"完成所有符合条件的评估缺口的文档落地"}`
	if err := os.WriteFile(filepath.Join(dir, "20261112", "session_memory_7fa344cf.jsonl"), []byte(memory), 0o644); err != nil {
		t.Fatal(err)
	}
	// Child workspace: must not match the parent's cwd.
	child := filepath.Join(home, ".trae-cn", "memory", "projects",
		"-"+strings.TrimPrefix(strings.ReplaceAll(cwd+"/RepoNest", "/", "-"), "-")+"--p2-abc")
	if err := os.MkdirAll(filepath.Join(child, "20261113"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "20261113", "session_memory_child.jsonl"),
		[]byte(`{"intent":"RepoNest 自己的会话","outcome":"done"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, ok, err := LatestForCwd(cwd, Trae)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if sess.SessionID != "7fa344cf" {
		t.Errorf("session id = %q", sess.SessionID)
	}
	if sess.FirstUserPrompt != "将评估发现的缺口补充到仓库的追踪体系中" {
		t.Errorf("intent = %q", sess.FirstUserPrompt)
	}
	if len(sess.Actions) != 2 {
		t.Errorf("actions = %v", sess.Actions)
	}
	if sess.LastAssistantText != "完成所有符合条件的评估缺口的文档落地" {
		t.Errorf("outcome = %q", sess.LastAssistantText)
	}
}

func TestLatestDSHDecompressesAndSkipsHarnessReminders(t *testing.T) {
	home := fakeHome(t)
	cwd := "/Users/tester/Script"
	dir := filepath.Join(home, ".dsh", "sessions", "-"+strings.ReplaceAll(cwd, "/", "-")+"--")
	sessDir := filepath.Join(dir, "session-d1")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const events = `{"type":"session","version":4,"id":"session-d1","cwd":"/Users/tester/Script"}
{"type":"user/message","seq":8,"data":{"content":[{"type":"text","text":"检查一下CInder和Warden的handoff机制闭环的问题"}],"source":{"kind":"user"}}}
{"type":"user/message","seq":9,"data":{"content":[{"type":"text","text":"<system-reminder>The following information describes your current session</system-reminder>"}],"source":{"kind":"user"}}}
{"type":"assistant/attempt","seq":16,"data":{"stream":[{"type":"chunk","chunk":{"type":"text","text":"闭环已确认。"}}]}}
`
	if err := writeZstd(filepath.Join(sessDir, "session.v4.jsonl.zstd"), events); err != nil {
		t.Fatal(err)
	}
	sess, ok, err := LatestForCwd(cwd, DSH)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if sess.SessionID != "session-d1" {
		t.Errorf("session id = %q", sess.SessionID)
	}
	if sess.Cwd != cwd {
		t.Errorf("cwd = %q", sess.Cwd)
	}
	if sess.FirstUserPrompt != "检查一下CInder和Warden的handoff机制闭环的问题" {
		t.Errorf("first prompt = %q", sess.FirstUserPrompt)
	}
	if sess.LastAssistantText != "闭环已确认。" {
		t.Errorf("assistant = %q", sess.LastAssistantText)
	}
}

// A successful DSH attempt is what the local store has never produced (every
// session on this machine ends in "transport error", no text chunk at all), so
// the shapes below are written from the event schema: a known text chunk must
// win, reasoning must never be mistaken for prose, and an unknown-but-textual
// chunk must still fill the summary rather than leave it blank.
func TestDSHAssistantTextSurvivesSchemaDrift(t *testing.T) {
	cases := []struct {
		name  string
		event string
		want  string
	}{
		{
			name:  "known text chunk",
			event: `{"type":"assistant/attempt","seq":1,"data":{"stream":[{"type":"chunk","chunk":{"type":"text-delta","text":"闭环已确认。"}}]}}`,
			want:  "闭环已确认。",
		},
		{
			name: "reasoning is not prose",
			event: `{"type":"assistant/attempt","seq":1,"data":{"stream":[` +
				`{"type":"chunk","chunk":{"type":"reasoning","text":"想想怎么写"}},` +
				`{"type":"chunk","chunk":{"type":"text","text":"最终答案"}},` +
				`{"type":"chunk","chunk":{"type":"reasoning","text":"再想想"}}]}}`,
			want: "最终答案",
		},
		{
			name: "unknown type still yields text",
			event: `{"type":"assistant/attempt","seq":1,"data":{"stream":[` +
				`{"type":"chunk","chunk":{"type":"delta","text":"漂移后的形状"}}]}}`,
			want: "漂移后的形状",
		},
		{
			name:  "transport error carries no text",
			event: `{"type":"assistant/attempt","seq":1,"data":{"stream":[{"type":"chunk","chunk":{"type":"finish","text":""}}]}}`,
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := fakeHome(t)
			cwd := "/Users/tester/Script"
			sessDir := filepath.Join(home, ".dsh", "sessions", "-"+strings.ReplaceAll(cwd, "/", "-")+"--", "session-d2")
			if err := os.MkdirAll(sessDir, 0o755); err != nil {
				t.Fatal(err)
			}
			events := `{"type":"session","version":4,"id":"session-d2","cwd":"` + cwd + `"}` + "\n" +
				`{"type":"user/message","seq":2,"data":{"content":[{"type":"text","text":"看一下 DSH"}],"source":{"kind":"user"}}}` + "\n" +
				tc.event + "\n"
			if err := writeZstd(filepath.Join(sessDir, "session.v4.jsonl.zstd"), events); err != nil {
				t.Fatal(err)
			}
			sess, ok, err := LatestForCwd(cwd, DSH)
			if err != nil || !ok {
				t.Fatalf("ok=%v err=%v", ok, err)
			}
			if sess.LastAssistantText != tc.want {
				t.Errorf("assistant text = %q, want %q", sess.LastAssistantText, tc.want)
			}
		})
	}
}

func TestLatestAnyPicksTheNewestStore(t *testing.T) {
	home := fakeHome(t)
	cwd := "/Users/tester/Script"
	write := func(rel, name, body string) string {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old := write(".workbuddy/projects/"+strings.TrimPrefix(strings.ReplaceAll(cwd, "/", "-"), "-")+"/wb.jsonl",
		"", `{"type":"session-meta","sessionId":"wb-old"}`+"\n"+`{"type":"message","role":"user","content":[{"type":"input_text","text":"旧会话"}]}`)
	if err := os.Chtimes(old, time.Now().Add(-24*time.Hour), time.Now().Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	newer := write(".qoder/projects/-"+strings.TrimPrefix(strings.ReplaceAll(cwd, "/", "-"), "-")+"/q.jsonl",
		"", `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"新会话"}]},"cwd":"`+cwd+`","isSidechain":false}`)
	if err := os.Chtimes(newer, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	sess, ok, err := LatestAny(cwd)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if sess.Agent != Qoder {
		t.Errorf("auto picked %s, want %s", sess.Agent, Qoder)
	}
	if !strings.Contains(sess.FirstUserPrompt, "新会话") {
		t.Errorf("first prompt = %q", sess.FirstUserPrompt)
	}
}

func TestUnknownAgentIsAnErrorNotASilentMiss(t *testing.T) {
	_, _, err := LatestForCwd("/x", "cursor")
	if err == nil {
		t.Fatal("expected an error for an unknown agent")
	}
}

// helpers

func writeZstd(path, body string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc, err := zstd.NewWriter(f)
	if err != nil {
		return err
	}
	if _, err := enc.Write([]byte(body)); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return f.Close()
}
