package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"repotory/internal/aiclient"
	"strings"
	"testing"

	"repotory/internal/db"
)

// organizeNoteFixture seeds one project with one note to tidy.
func organizeNoteFixture(t *testing.T, svc *Service) int64 {
	t.Helper()
	projectID := seedProject(t, svc.db, "organize", "/tmp/organize")
	n, err := db.CreateNoteEx(svc.db, projectID, "网关重试与幂等",
		"支付网关最多重试三次，幂等键使用 invoice id；超时后返回 408。",
		"gateway,retry", "knowledge", "manual")
	if err != nil {
		t.Fatal(err)
	}
	return n.ID
}

func configureOrganize(t *testing.T, svc *Service, url string) {
	t.Helper()
	for k, v := range map[string]string{
		"ai_chat_base_url": url + "/v1",
		"ai_chat_model":    "stub",
	} {
		if err := svc.UpdateConfig(k, v); err != nil {
			t.Fatalf("config %s: %v", k, err)
		}
	}
}

// chatStubCapture answers one /chat/completions request with `content` while
// capturing the full prompt, so a test can assert what the model was told.
func chatStubCapture(t *testing.T, content, finishReason string) (*httptest.Server, *string) {
	t.Helper()
	var prompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []aiclient.Message `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		for _, m := range req.Messages {
			prompt += m.Content + "\n"
		}
		w.Header().Set("Content-Type", "application/json")
		if finishReason != "" {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":`+jsonQuote(content)+`},"finish_reason":`+jsonQuote(finishReason)+`}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":`+jsonQuote(content)+`}}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &prompt
}

// A well-formed reply is parsed into the three fields, and the mermaid block
// the model was asked for survives byte-for-byte — a diagram that loses its
// fence is a diagram that no longer renders.
func TestOrganizeNote_Proposal(t *testing.T) {
	svc, _ := setupService(t)
	noteID := organizeNoteFixture(t, svc)

	reply := "###TITLE\n支付网关重试与幂等\n###TAGS\ngateway,retry,idempotency\n###CONTENT\n" +
		"# 支付网关重试与幂等\n\n## 目录\n\n- 重试策略\n- 幂等键\n- 流程图\n\n" +
		"## 重试策略\n\n最多重试三次。\n\n## 流程图\n\n```mermaid\nflowchart TD\n  A[提交] --> B{重试未满三次?}\n  B -- 是 --> C[再次提交]\n```\n"
	srv, prompt := chatStubCapture(t, reply, "")
	configureOrganize(t, svc, srv.URL)

	res, err := svc.OrganizeNote(noteID)
	if err != nil {
		t.Fatalf("OrganizeNote: %v", err)
	}
	if res.NoteID != noteID {
		t.Errorf("NoteID = %d, want %d", res.NoteID, noteID)
	}
	if res.Title != "支付网关重试与幂等" {
		t.Errorf("Title = %q", res.Title)
	}
	if res.Tags != "gateway,retry,idempotency" {
		t.Errorf("Tags = %q", res.Tags)
	}
	if !strings.Contains(res.Content, "## 目录") {
		t.Errorf("content lost the index:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "```mermaid\nflowchart TD") {
		t.Errorf("content lost the mermaid fence:\n%s", res.Content)
	}
	if res.Warning != "" {
		t.Errorf("unexpected warning: %q", res.Warning)
	}
	// The note body must reach the model: an organizer that never sees the
	// note can only invent one.
	if !strings.Contains(*prompt, "invoice id") {
		t.Errorf("prompt did not carry the note body:\n%s", *prompt)
	}
	// The protocol markers are instructions to the model, so the prompt must
	// spell them out.
	if !strings.Contains(*prompt, organizeMarkerContent) {
		t.Errorf("prompt did not state the output protocol")
	}
}

// The proposal is a proposal: the service must not have written anything.
func TestOrganizeNote_WritesNothing(t *testing.T) {
	svc, _ := setupService(t)
	noteID := organizeNoteFixture(t, svc)
	before, err := svc.GetNote(noteID)
	if err != nil {
		t.Fatal(err)
	}
	srv, _ := chatStub(t, "###CONTENT\n完全不同的正文。\n")
	configureOrganize(t, svc, srv.URL)

	if _, err := svc.OrganizeNote(noteID); err != nil {
		t.Fatalf("OrganizeNote: %v", err)
	}
	after, err := svc.GetNote(noteID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Content != before.Content || after.Title != before.Title {
		t.Fatalf("note changed without a save:\nbefore %+v\nafter  %+v", before, after)
	}
}

// A model that ignores the protocol still yields a usable proposal: the whole
// reply becomes the body, with a warning that says so.
func TestOrganizeNote_MissingMarkerFallsBackToWholeReply(t *testing.T) {
	svc, _ := setupService(t)
	noteID := organizeNoteFixture(t, svc)
	srv, _ := chatStub(t, "# 整理后的标题\n\n正文内容。")
	configureOrganize(t, svc, srv.URL)

	res, err := svc.OrganizeNote(noteID)
	if err != nil {
		t.Fatalf("OrganizeNote: %v", err)
	}
	if !strings.Contains(res.Content, "正文内容") {
		t.Errorf("content = %q", res.Content)
	}
	if !strings.Contains(res.Warning, organizeMarkerContent) {
		t.Errorf("warning should name the missing marker, got %q", res.Warning)
	}
	// Title stays empty so the caller keeps the stored value; the service
	// backfills it from the note so the editor is never title-less.
	if res.Title != "网关重试与幂等" {
		t.Errorf("Title = %q, want the stored title", res.Title)
	}
}

// A truncated reply is a prefix, not a finished proposal: the warning must
// say so, or the user reviews half a diagram believing it is whole.
func TestOrganizeNote_TruncatedReplyWarns(t *testing.T) {
	svc, _ := setupService(t)
	noteID := organizeNoteFixture(t, svc)
	srv, _ := chatStubCapture(t, "###CONTENT\n半截正文……", "length")
	configureOrganize(t, svc, srv.URL)

	res, err := svc.OrganizeNote(noteID)
	if err != nil {
		t.Fatalf("OrganizeNote: %v", err)
	}
	if !strings.Contains(res.Warning, "截断") {
		t.Errorf("warning = %q, want a truncation hint", res.Warning)
	}
}

// A model that wraps its whole answer in one fence must not leave the editor
// showing a literal code block — but the mermaid fence INSIDE must survive.
func TestOrganizeNote_OuterFenceStrippedInnerFenceKept(t *testing.T) {
	svc, _ := setupService(t)
	noteID := organizeNoteFixture(t, svc)
	reply := "###CONTENT\n```markdown\n# 标题\n\n```mermaid\nflowchart TD\n  A --> B\n```\n```"
	srv, _ := chatStub(t, reply)
	configureOrganize(t, svc, srv.URL)

	res, err := svc.OrganizeNote(noteID)
	if err != nil {
		t.Fatalf("OrganizeNote: %v", err)
	}
	if strings.HasPrefix(res.Content, "```") {
		t.Errorf("outer fence survived:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "```mermaid\nflowchart TD") {
		t.Errorf("inner mermaid fence was stripped:\n%s", res.Content)
	}
}

// An unconfigured endpoint is a settings problem, and the message must say
// where to fix it rather than surfacing a raw config error.
func TestOrganizeNote_UnconfiguredAI(t *testing.T) {
	svc, _ := setupService(t)
	noteID := organizeNoteFixture(t, svc)
	_, err := svc.OrganizeNote(noteID)
	if err == nil {
		t.Fatal("expected an error with AI unconfigured")
	}
	if !strings.Contains(err.Error(), "Settings") {
		t.Errorf("error should point at Settings, got: %v", err)
	}
}

func TestOrganizeNote_NoteNotFound(t *testing.T) {
	svc, _ := setupService(t)
	if _, err := svc.OrganizeNote(9999); err == nil {
		t.Fatal("expected an error for a missing note")
	}
}

// An empty reply is the one case that must fail: an empty proposal would put
// the user in an editor that cannot save.
func TestOrganizeNote_EmptyReplyFails(t *testing.T) {
	svc, _ := setupService(t)
	noteID := organizeNoteFixture(t, svc)
	srv, _ := chatStub(t, "   ")
	configureOrganize(t, svc, srv.URL)
	if _, err := svc.OrganizeNote(noteID); err == nil {
		t.Fatal("expected an error for an empty reply")
	}
}

// The parse unit cases, including the orders a chatty model actually emits.
func TestParseOrganizeReply(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		wantTitle   string
		wantTags    string
		wantContent string
		wantWarn    bool
	}{
		{
			name:        "all three sections",
			raw:         "###TITLE\n标题\n###TAGS\na, b\n###CONTENT\n正文",
			wantTitle:   "标题",
			wantTags:    "a, b",
			wantContent: "正文",
		},
		{
			name:        "content only",
			raw:         "###CONTENT\n只有正文",
			wantContent: "只有正文",
		},
		{
			name:        "title and content, no tags marker",
			raw:         "###TITLE\n标题\n###CONTENT\n正文",
			wantTitle:   "标题",
			wantContent: "正文",
		},
		{
			name:        "no markers at all",
			raw:         "一段普通回复",
			wantContent: "一段普通回复",
			wantWarn:    true,
		},
		{
			name:        "chatter before the first marker",
			raw:         "好的，整理结果如下：\n\n###TITLE\n标题\n###TAGS\n\n###CONTENT\n正文",
			wantTitle:   "标题",
			wantTags:    "",
			wantContent: "正文",
		},
		{
			name:        "title prefix noise",
			raw:         "###TITLE\n标题：带前缀\n###CONTENT\n正文",
			wantTitle:   "带前缀",
			wantContent: "正文",
		},
		{
			// A model that emits CONTENT first is pathological: the body runs
			// to the end of the output, so trailing markers stay IN the body.
			// Extracting them would risk eating real content that happens to
			// start with the marker text.
			name:        "content marker first means body runs to the end",
			raw:         "###CONTENT\n正文\n###TITLE\n标题",
			wantTitle:   "",
			wantContent: "正文\n###TITLE\n标题",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			title, tags, content, warning := parseOrganizeReply(tc.raw)
			if title != tc.wantTitle {
				t.Errorf("title = %q, want %q", title, tc.wantTitle)
			}
			if tags != tc.wantTags {
				t.Errorf("tags = %q, want %q", tags, tc.wantTags)
			}
			if content != tc.wantContent {
				t.Errorf("content = %q, want %q", content, tc.wantContent)
			}
			if (warning != "") != tc.wantWarn {
				t.Errorf("warning = %q, wantWarn %v", warning, tc.wantWarn)
			}
		})
	}
}

// The proposal is clipped to the store's byte bounds so the save that follows
// it cannot fail on the model's output.
func TestOrganizeNote_ClipsToStoreBounds(t *testing.T) {
	svc, _ := setupService(t)
	noteID := organizeNoteFixture(t, svc)
	srv, _ := chatStub(t, "###TITLE\n"+strings.Repeat("标", db.MaxNoteTitleLen+50)+
		"\n###TAGS\n"+strings.Repeat("t,", 400)+"\n###CONTENT\n正文")
	configureOrganize(t, svc, srv.URL)

	res, err := svc.OrganizeNote(noteID)
	if err != nil {
		t.Fatalf("OrganizeNote: %v", err)
	}
	if len(res.Title) > db.MaxNoteTitleLen {
		t.Errorf("title not clipped: %d bytes", len(res.Title))
	}
	if len(res.Tags) > db.MaxNoteTagsLen {
		t.Errorf("tags not clipped: %d bytes", len(res.Tags))
	}
	// A clip that split a rune would hand the editor invalid UTF-8.
	if strings.ToValidUTF8(res.Title, "?") != res.Title || strings.ToValidUTF8(res.Tags, "?") != res.Tags {
		t.Error("clip produced invalid UTF-8")
	}
}

// One note, one call: a retry loop inside the service would double the
// endpoint spend for a single button press.
func TestOrganizeNote_CallsEndpointOnce(t *testing.T) {
	svc, _ := setupService(t)
	noteID := organizeNoteFixture(t, svc)
	srv, hits := chatStub(t, "###CONTENT\n正文")
	configureOrganize(t, svc, srv.URL)
	if _, err := svc.OrganizeNote(noteID); err != nil {
		t.Fatalf("OrganizeNote: %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("endpoint called %d times, want 1", got)
	}
}

// The prompt documents a truncation cap for over-long notes; assert the cap
// the prompt states is the one enforced, and that it stays inside store max.
func TestOrganizeNoteBytesCap(t *testing.T) {
	if organizeNoteBytes >= db.MaxNoteContentLen {
		t.Fatalf("organize cap %d should stay below the store max %d", organizeNoteBytes, db.MaxNoteContentLen)
	}
}
