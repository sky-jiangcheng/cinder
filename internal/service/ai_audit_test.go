package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"repotory/internal/aiaudit"
	"repotory/internal/aiclient"
	"strings"
	"testing"

	"repotory/internal/db"
)

// AI call audit and content review (ADR-0030).
//
// The behaviours pinned here are the ones ADR-0030 promised and that would
// be easy to break silently:
//
//  1. With every gate off, NOTHING changes — no audit row, no review, the
//     request goes out exactly as before. This is the promise that keeps the
//     plugin from being a product change.
//  2. The input review withholds the prompt and NO request is made.
//  3. The output review is weaker than the input review: a streamed reply's
//     head is already delivered and cannot be recalled, so the verdict is
//     "flagged" and only the tail is withheld.
//  4. The audit row stores a HASH, never the prompt text.

func newAuditTestService(t *testing.T, handler http.HandlerFunc) *Service {
	t.Helper()
	database, err := db.InitDB(t.TempDir() + "/audit.db")
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	svc := New(database, "tester")
	svc.Audit.SetSurface(aiaudit.SurfaceMCP)

	if handler != nil {
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)
		if err := svc.UpdateConfig("ai_chat_base_url", srv.URL+"/v1"); err != nil {
			t.Fatalf("set base url: %v", err)
		}
	}
	if err := svc.UpdateConfig("ai_chat_model", "test-model"); err != nil {
		t.Fatalf("set model: %v", err)
	}
	return svc
}

// mustCfg returns the resolved chat config, failing the test if it is not
// configured.
func mustCfg(t *testing.T, svc *Service) aiChatConfig {
	t.Helper()
	cfg, err := svc.aiChatConfig()
	if err != nil {
		t.Fatalf("chat config: %v", err)
	}
	return cfg
}

// okHandler answers every chat request with a fixed reply.
func okHandler(reply string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}
}

// TestAuditOffByDefaultChangesNothing is the regression that keeps ADR-0030's
// "the memory layer is unchanged" promise honest: with the gates closed, a
// model call leaves no audit row and the reply comes back untouched.
func TestAuditOffByDefaultChangesNothing(t *testing.T) {
	svc := newAuditTestService(t, okHandler("hello"))

	result, err := svc.chatWithAudit(mustCfg(t, svc), []aiclient.Message{{Role: "user", Content: "hi"}}, aiclient.ChatTimeout)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if result.Reply != "hello" {
		t.Errorf("reply = %q, want %q", result.Reply, "hello")
	}

	n, err := db.CountAICallLog(svc.db)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("audit rows = %d, want 0 — auditing must be off by default", n)
	}
}

// TestAuditRecordsCallWhenEnabled checks the row that IS written once the
// user turns auditing on: the metadata is there and the prompt text is not.
func TestAuditRecordsCallWhenEnabled(t *testing.T) {
	svc := newAuditTestService(t, okHandler("hello"))
	if err := svc.UpdateConfig("ai_audit_enabled", "1"); err != nil {
		t.Fatalf("enable audit: %v", err)
	}

	const secret = "客户身份证 110101199001011234"
	_, err := svc.chatWithAudit(mustCfg(t, svc), []aiclient.Message{{Role: "user", Content: secret}}, aiclient.ChatTimeout)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}

	rows, err := db.ListAICallLog(svc.db, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Surface != aiaudit.SurfaceMCP {
		t.Errorf("surface = %q, want %q", row.Surface, aiaudit.SurfaceMCP)
	}
	if row.Model != "test-model" {
		t.Errorf("model = %q, want test-model", row.Model)
	}
	if row.PromptChars == 0 {
		t.Error("prompt_chars = 0, want > 0")
	}
	if row.PromptHash == "" {
		t.Error("prompt_hash is empty, want a hash")
	}
	// The whole point of the default tier: the text itself is NOT stored.
	if strings.Contains(row.PromptHash, secret) || strings.Contains(row.PromptHash, "110101") {
		t.Errorf("prompt_hash leaks the prompt text: %q", row.PromptHash)
	}
	if row.ResponseChars != len("hello") {
		t.Errorf("response_chars = %d, want %d", row.ResponseChars, len("hello"))
	}
}

// TestInputReviewBlocksTheCall is the security-relevant behaviour: a rule
// match withholds the prompt and NO request reaches the endpoint.
func TestInputReviewBlocksTheCall(t *testing.T) {
	var called int
	svc := newAuditTestService(t, func(w http.ResponseWriter, r *http.Request) {
		called++
		okHandler("hello")(w, r)
	})
	if err := svc.UpdateConfig("ai_review_input", "1"); err != nil {
		t.Fatalf("enable input review: %v", err)
	}
	if err := svc.UpdateConfig("ai_review_rules", "身份证\n密码"); err != nil {
		t.Fatalf("set rules: %v", err)
	}

	_, err := svc.chatWithAudit(mustCfg(t, svc), []aiclient.Message{{Role: "user", Content: "查一下客户身份证号"}}, aiclient.ChatTimeout)
	if err == nil {
		t.Fatal("expected the input review to block the call")
	}
	if called != 0 {
		t.Errorf("endpoint was called %d times, want 0 — a blocked prompt must not be sent", called)
	}
	var blocked aiaudit.BlockedError
	if !strings.Contains(err.Error(), "身份证") {
		t.Errorf("error %q does not name the matched rule", err.Error())
	}
	_ = blocked
}

// TestInputReviewAllowsCleanPrompt is the other half: a clean prompt goes
// out, and the audit row records that the review actually ran.
func TestInputReviewAllowsCleanPrompt(t *testing.T) {
	svc := newAuditTestService(t, okHandler("hello"))
	if err := svc.UpdateConfig("ai_audit_enabled", "1"); err != nil {
		t.Fatalf("enable audit: %v", err)
	}
	if err := svc.UpdateConfig("ai_review_input", "1"); err != nil {
		t.Fatalf("enable input review: %v", err)
	}
	if err := svc.UpdateConfig("ai_review_rules", "身份证"); err != nil {
		t.Fatalf("set rules: %v", err)
	}

	if _, err := svc.chatWithAudit(mustCfg(t, svc), []aiclient.Message{{Role: "user", Content: "这个函数怎么写"}}, aiclient.ChatTimeout); err != nil {
		t.Fatalf("chat: %v", err)
	}
	rows, err := db.ListAICallLog(svc.db, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
	if rows[0].InputVerdict != aiaudit.VerdictAllow {
		t.Errorf("input_verdict = %q, want %q", rows[0].InputVerdict, aiaudit.VerdictAllow)
	}
}

// TestInputReviewOffMeansNotScreened pins the distinction the ADR insists
// on: "not screened" is not the same verdict as "screened and clean".
func TestInputReviewOffMeansNotScreened(t *testing.T) {
	svc := newAuditTestService(t, okHandler("hello"))
	if err := svc.UpdateConfig("ai_audit_enabled", "1"); err != nil {
		t.Fatalf("enable audit: %v", err)
	}
	if err := svc.UpdateConfig("ai_review_rules", "身份证"); err != nil {
		t.Fatalf("set rules: %v", err)
	}

	if _, err := svc.chatWithAudit(mustCfg(t, svc), []aiclient.Message{{Role: "user", Content: "查一下客户身份证号"}}, aiclient.ChatTimeout); err != nil {
		t.Fatalf("chat: %v", err)
	}
	rows, _ := db.ListAICallLog(svc.db, 10)
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
	if rows[0].InputVerdict != "" {
		t.Errorf("input_verdict = %q, want empty — a disabled review must not claim to have run", rows[0].InputVerdict)
	}
}

// TestOutputReviewFlagsStreamedReply pins the honest limit of streaming
// review: the head of the reply was already delivered and cannot be
// recalled, so the verdict is "flagged" and the tail is withheld.
func TestOutputReviewFlagsStreamedReply(t *testing.T) {
	svc := newAuditTestService(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Two chunks: the first is clean and gets delivered, the second trips
		// the rule.
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"safe prefix \"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"身份证 123\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	})
	if err := svc.UpdateConfig("ai_audit_enabled", "1"); err != nil {
		t.Fatalf("enable audit: %v", err)
	}
	if err := svc.UpdateConfig("ai_review_output", "1"); err != nil {
		t.Fatalf("enable output review: %v", err)
	}
	if err := svc.UpdateConfig("ai_review_rules", "身份证"); err != nil {
		t.Fatalf("set rules: %v", err)
	}

	var delivered strings.Builder
	_, _, err := svc.streamChatWithAudit(mustCfg(t, svc),
		[]aiclient.Message{{Role: "user", Content: "hi"}}, aiAskTimeout,
		func(d string) { delivered.WriteString(d) })
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	// The clean prefix WAS delivered — this is the honest limit, not a bug.
	if !strings.Contains(delivered.String(), "safe prefix") {
		t.Errorf("delivered = %q, want the clean prefix to have been delivered", delivered.String())
	}
	// The part that tripped the rule was withheld.
	if strings.Contains(delivered.String(), "身份证") {
		t.Errorf("delivered = %q, want the matching tail withheld", delivered.String())
	}

	rows, _ := db.ListAICallLog(svc.db, 10)
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
	if rows[0].OutputVerdict != aiaudit.VerdictFlagged {
		t.Errorf("output_verdict = %q, want %q — a streamed reply can only be flagged, not blocked",
			rows[0].OutputVerdict, aiaudit.VerdictFlagged)
	}
}

// TestAuditLogIsAppendOnly proves the audit trail cannot be quietly edited:
// the guards are triggers, so this is a structural fact, not a convention.
func TestAuditLogIsAppendOnly(t *testing.T) {
	svc := newAuditTestService(t, nil)
	if err := db.InsertAICallLog(svc.db, db.AICallLog{Surface: aiaudit.SurfaceMCP, Model: "m"}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := svc.db.Exec("UPDATE ai_call_log SET model = 'edited'"); err == nil {
		t.Error("UPDATE on ai_call_log succeeded, want it rejected — the trail must not be editable")
	}
	if _, err := svc.db.Exec("DELETE FROM ai_call_log"); err == nil {
		t.Error("DELETE on ai_call_log succeeded, want it rejected — the trail must not be prunable")
	}
}

// TestInvalidReviewRuleIsSkippedNotFatal keeps one broken regex from taking
// the whole review down: otherwise the user cannot tell "nothing matched"
// from "the gate is broken".
func TestInvalidReviewRuleIsSkippedNotFatal(t *testing.T) {
	svc := newAuditTestService(t, nil)
	if err := svc.UpdateConfig("ai_review_rules", "(((\n身份证"); err != nil {
		t.Fatalf("set rules: %v", err)
	}
	rules := svc.Audit.Rules()
	if len(rules) != 1 {
		t.Fatalf("rules = %d, want 1 — the broken line is skipped, the good one survives", len(rules))
	}
	if rules[0].String() != "身份证" {
		t.Errorf("rule = %q, want 身份证", rules[0].String())
	}
}

// TestRulesAreCaseInsensitive matches how the rules are written: people
// think in terms of content, not regex flags.
func TestRulesAreCaseInsensitive(t *testing.T) {
	svc := newAuditTestService(t, nil)
	if err := svc.UpdateConfig("ai_review_rules", "password"); err != nil {
		t.Fatalf("set rules: %v", err)
	}
	rules := svc.Audit.Rules()
	if len(rules) != 1 {
		t.Fatalf("rules = %d, want 1", len(rules))
	}
	if got := aiaudit.MatchRule("my PASSWORD is 123", rules); got == "" {
		t.Error("case-insensitive match failed")
	}
}
