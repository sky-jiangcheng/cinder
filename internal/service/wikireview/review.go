// Package wikireview is the policy half of AI-assisted page review.
//
// It deliberately owns no write path: it reads a pending page, applies the
// deterministic veto rules, and asks the configured model for one structured
// verdict. The service layer decides what to do with that verdict (annotate,
// auto-approve, or leave it to a human). Keeping policy here makes the rule
// set testable without a database or an endpoint, which is exactly what the
// human-gate discussion needs.
package wikireview

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"repotory/internal/db"
	"repotory/internal/service/wikigen"
	"repotory/internal/service/wikipage"
)

// Verdicts and sources are stored on the page, not recomputed: a reviewer must
// see WHY a page was auto-approved, and the ticker must not burn tokens on the
// same page every five minutes.
const (
	VerdictApprove = "approve"
	VerdictReject  = "reject"
	VerdictError   = "error"

	SourceRule    = "rule"
	SourceAI      = "ai"
	SourceAuto    = "auto"
	SourceTimeout = "timeout"
)

// Config is the three settings that govern the feature. Every one defaults to
// OFF/ZERO: the existing human gate stays byte-for-byte unchanged until the
// user opts in from Settings → AI.
type Config struct {
	AI           bool
	AutoApprove  bool
	TimeoutHours int
}

// GetConfig reads the live settings. A missing row is an absent row, which
// db.GetConfig maps to "", so every default here is the product default.
func GetConfig(database *sql.DB) (Config, error) {
	ai, err := db.GetConfig(database, "wiki_review_ai")
	if err != nil {
		return Config{}, err
	}
	auto, err := db.GetConfig(database, "wiki_review_auto")
	if err != nil {
		return Config{}, err
	}
	hours, err := db.GetConfig(database, "wiki_review_timeout_hours")
	if err != nil {
		return Config{}, err
	}
	n, _ := strconv.Atoi(strings.TrimSpace(hours))
	if n < 0 || n > 24*365 {
		n = 0 // nonsense input must never shorten the human window
	}
	return Config{
		AI:           ai == "1",
		AutoApprove:  auto == "1",
		TimeoutHours: n,
	}, nil
}

// Enabled reports whether the ticker has any work at all.
func (c Config) Enabled() bool {
	return c.AI || c.AutoApprove || c.TimeoutHours > 0
}

// vetoMarkers are unmistakable signs that the compiler produced a skeleton
// rather than knowledge. They are a hard veto: no model verdict, no timeout,
// no implicit trust can publish a page carrying one of them.
var vetoMarkers = []string{
	"待补充", "待完善", "待填写", "占位", "TODO", "TBD", "FIXME", "PLACEHOLDER",
	"此页面由本地模型生成，经人工审核判定为不合格",
}

// Veto applies the deterministic rules. A non-empty return value blocks every
// automatic path, including the timeout. The list is intentionally short: each
// entry must be a signal that no reasonable reviewer would publish, otherwise
// the rule would veto real knowledge and quietly defeat the feature.
func Veto(p *db.WikiPage) string {
	if p == nil {
		return "页面不存在"
	}
	if strings.TrimSpace(p.Title) == "" || strings.TrimSpace(p.Slug) == "" {
		return "标题或 slug 为空"
	}
	body := strings.TrimSpace(p.Content)
	if len([]rune(body)) < 40 {
		return "正文不足 40 字，无法构成可检索的知识"
	}
	upper := strings.ToUpper(body)
	for _, marker := range vetoMarkers {
		if strings.Contains(upper, strings.ToUpper(marker)) {
			return "正文含未完成标记：" + marker
		}
	}
	return ""
}

// Verdict is the model's structured answer. Pass is true only for an explicit
// approve — silence, prose, an unparseable reply and "needs review" all keep
// the page in the human queue.
type Verdict struct {
	Pass   bool
	Reason string
}

type replyPayload struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
}

// ParseReply extracts the JSON object from a model reply. Fenced code blocks
// are common even when the prompt forbids them, so the parser looks for the
// first braces rather than demanding a bare JSON document.
func ParseReply(raw string) (Verdict, bool) {
	start, end := strings.IndexByte(raw, '{'), strings.LastIndexByte(raw, '}')
	if start < 0 || end <= start {
		return Verdict{}, false
	}
	var payload replyPayload
	if err := json.Unmarshal([]byte(raw[start:end+1]), &payload); err != nil {
		return Verdict{}, false
	}
	switch strings.ToLower(strings.TrimSpace(payload.Verdict)) {
	case VerdictApprove, "pass", "yes":
		return Verdict{Pass: true, Reason: clipReason(payload.Reason)}, true
	case VerdictReject, "no":
		return Verdict{Pass: false, Reason: clipReason(payload.Reason)}, true
	}
	return Verdict{}, false
}

func clipReason(s string) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	r := []rune(s)
	if len(r) > 240 {
		return string(r[:240]) + "…"
	}
	return s
}

// Judge asks the configured model for one review verdict. The chat seam is
// the audited transport already used by compile/lint, so input review, output
// review and the ai_call_log row apply here too.
func Judge(ctx context.Context, chat wikigen.Chat, p *db.WikiPage) (Verdict, error) {
	if err := chat.Available(ctx); err != nil {
		return Verdict{}, err
	}
	body := wikipage.ClipRunes(p.Content, 6000)
	prompt := fmt.Sprintf(`你审核一条从笔记编译出的知识页，判断是否可以直接发布到知识库。
只发布内容具体、自洽、能独立说明一件事的页面。空泛、猜测当事实、自相矛盾、含未完成标记的页面应拒绝。
不要因为写法或措辞不够好而拒绝；只判断内容是否可作为知识被检索。

标题：%s
slug：%s
正文：
%s

只输出一个 JSON 对象，不要 Markdown 代码围栏、不要解释：
{"verdict":"approve或reject","reason":"不超过80字的中文理由"}`, p.Title, p.Slug, body)

	reply, err := chat.Chat(ctx, []wikigen.Message{{Role: "user", Content: prompt}})
	if err != nil {
		return Verdict{}, err
	}
	verdict, ok := ParseReply(reply.Text)
	if !ok {
		return Verdict{}, fmt.Errorf("AI 审核结论无法解析：%s", wikigen.TruncatedHint(reply.FinishReason))
	}
	if verdict.Reason == "" {
		verdict.Reason = "AI 未给出理由"
	}
	return verdict, nil
}
