package wikilint

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"repotory/internal/db"
	"repotory/internal/service/wikigen"
	"repotory/internal/service/wikipage"
)

// runWikiLintLLM is the model-backed half: contradictions, stale claims and
// synthesis-vs-members coherence (community-incoherent, ADR-0020). Gated
// on its own key plus a configured chat endpoint. It returns findings only and has
// no code path to page writes.
//
// It speaks the wikigen.Chat seam rather than a lint-specific one: this call has
// the same shape as merge-suggest (a batch of pages in, findings plus a "why it
// did not run" note out) and merge-suggest is called from this same pass, so the
// two model halves share one transport. The gates run before anything is
// serialised, which is what keeps "off means zero outbound bytes" true.
func runWikiLintLLM(ctx context.Context, database *sql.DB, chat wikigen.Chat, pages []db.WikiPage) ([]WikiFinding, string) {
	if len(pages) < 2 {
		return nil, "少于 2 张页面，矛盾/过时检查没有意义"
	}
	if !wikigen.LintLLMEnabled(database) {
		return nil, "未开启（wiki_lint_llm 需显式设为 1）：跳过矛盾与过时检查"
	}
	if err := chat.Available(ctx); err != nil {
		return nil, "AI 端点未配置：跳过矛盾与过时检查"
	}
	list := pages
	if len(list) > lintMaxLLMPages {
		list = list[:lintMaxLLMPages]
	}

	// ADR-0020 follow-up: synthesis pages get their member slugs attached so the
	// model can judge "community-incoherent" against what the page actually
	// synthesizes, not against the whole corpus. Members come from the same edge
	// table the structural half reads; nothing here can write.
	members := map[int64][]string{}
	if edges, err := db.ListPageLinks(database); err == nil {
		kindOf := make(map[int64]string, len(pages))
		slugOf := make(map[int64]string, len(pages))
		for _, p := range pages {
			kindOf[p.ID] = p.Kind
			slugOf[p.ID] = p.Slug
		}
		for _, e := range edges {
			if kindOf[e.FromID] != "synthesis" || e.FromID == e.ToID {
				continue
			}
			if slug, ok := slugOf[e.ToID]; ok {
				members[e.FromID] = append(members[e.FromID], slug)
			}
		}
	}

	arr := make([]map[string]any, 0, len(list))
	for _, p := range list {
		entry := map[string]any{
			"page_id": p.ID, "slug": p.Slug, "kind": p.Kind, "title": p.Title,
			"updated": p.UpdatedAt, "excerpt": wikipage.TruncateBytes(p.Content, lintExcerptBytes),
		}
		if m := members[p.ID]; len(m) > 0 {
			entry["members"] = m
		}
		arr = append(arr, entry)
	}
	encoded, err := json.Marshal(arr)
	if err != nil {
		return nil, "页面序列化失败：跳过 LLM 检查"
	}
	var prompt strings.Builder
	prompt.WriteString("以下是若干互相链接的知识页（JSON 数组，含各自更新时间；synthesis 页带 members 成员页清单）。找出三类问题：\n" +
		"1. contradiction：两页对同一事实陈述冲突；\n" +
		"2. stale：某页断言被更新时间更晚的页覆盖或已过时；\n" +
		"3. community-incoherent：synthesis 综述页与它的 members 成员页内容明显不一致、遗漏或歪曲了成员页的关键结论。\n" +
		`只输出 JSON 数组，元素形如 {"type":"contradiction|stale|community-incoherent","page_id":<id>,"other_id":<id或null>,"detail":"<一句话>"}。` +
		"没有发现就输出 []。不要提改写方案，不要输出 JSON 以外的字符。\n\n")
	prompt.Write(encoded)

	// Page content is untrusted data and the model reply is untrusted data too: an
	// instruction buried in a page cannot become an action here, because this path
	// can only produce findings.
	reply, err := chat.Chat(ctx, []wikigen.Message{
		{Role: "system", Content: "你只输出 JSON，只做只读分析，不要求改动任何内容。"},
		{Role: "user", Content: prompt.String()},
	})
	if err != nil {
		return nil, fmt.Sprintf("LLM 调用失败，跳过矛盾与过时检查（%v）", err)
	}
	items, ok := parseLintReply(reply.Text)
	if !ok {
		// An unparseable reply yields no findings rather than a best-effort guess:
		// a hallucinated "contradiction" todo is noise the user clears by hand.
		//
		// Truncation is reported separately because the remedy differs: raise the
		// output budget or switch model, rather than re-running the same call.
		if hint := wikigen.TruncatedHint(reply.FinishReason); hint != "" {
			return nil, "LLM 回复被截断，未能解析为 JSON：本次不采信任何建议" + hint
		}
		return nil, "LLM 回复无法解析为 JSON：本次不采信任何建议"
	}
	byID := make(map[int64]db.WikiPage, len(pages))
	for _, p := range pages {
		byID[p.ID] = p
	}
	var out []WikiFinding
	for _, it := range items {
		p, ok := byID[it.PageID]
		if !ok {
			continue // the model named a page that does not exist; never address it
		}
		// Unknown types degrade to "stale" (skeptical by default);
		// community-incoherent gets its own check name so the todo reads as a
		// synthesis-consistency question, not a staleness one.
		check := "stale"
		switch it.Type {
		case "contradiction":
			check = "contradiction"
		case "community-incoherent":
			check = "community-incoherent"
		}
		other := ""
		if o, ok := byID[it.OtherID]; ok && it.OtherID != it.PageID {
			other = fmt.Sprintf("，涉及 [[%s]]", o.Slug)
		}
		out = append(out, WikiFinding{
			Check: check, Severity: "warn", ProjectID: p.ProjectID, PageID: p.ID,
			Detail: fmt.Sprintf("（模型建议，需人工判断）[[%s]] %s%s", p.Slug,
				wikipage.ClipRunes(strings.TrimSpace(it.Detail), 200), other),
		})
	}
	return out, ""
}

type lintReplyItem struct {
	Type    string `json:"type"`
	PageID  int64  `json:"page_id"`
	OtherID int64  `json:"other_id"`
	Detail  string `json:"detail"`
}

// parseLintReply tolerates the fenced/prose shapes models emit, but refuses to
// guess at structure: an unparseable reply means no findings at all.
func parseLintReply(raw string) ([]lintReplyItem, bool) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "["); i > 0 {
		s = s[i:]
	}
	if j := strings.LastIndex(s, "]"); j >= 0 && j+1 < len(s) {
		s = s[:j+1]
	}
	var items []lintReplyItem
	if err := json.Unmarshal([]byte(s), &items); err != nil {
		return nil, false
	}
	return items, true
}
