package wikireview

import (
	"context"
	"strings"
	"testing"

	"repotory/internal/db"
	"repotory/internal/service/wikigen"
)

const longBody = "这条页面记录了一个明确的事实：支付网关在超时后会进入退避重试，" +
	"退避窗口按失败次数递增，且同一笔订单的重复通知会被幂等键合并。"

func TestVeto(t *testing.T) {
	good := &db.WikiPage{Title: "Payment gateway", Slug: "payment-gateway", Content: longBody}
	if got := Veto(good); got != "" {
		t.Fatalf("valid page vetoed: %q", got)
	}
	for _, tc := range []struct {
		name string
		p    *db.WikiPage
	}{
		{"nil", nil},
		{"empty title", &db.WikiPage{Slug: "s", Content: longBody}},
		{"thin body", &db.WikiPage{Title: "t", Slug: "s", Content: "too short"}},
		{"todo marker", &db.WikiPage{Title: "t", Slug: "s", Content: longBody + "\nTODO: fill later"}},
		{"chinese marker", &db.WikiPage{Title: "t", Slug: "s", Content: longBody + "\n待补充示例"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Veto(tc.p); got == "" {
				t.Fatal("expected a hard veto")
			}
		})
	}
}

func TestParseReply(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		pass bool
		ok   bool
	}{
		{"bare approve", `{"verdict":"approve","reason":"内容具体"}`, true, true},
		{"fenced reject", "```json\n{\"verdict\":\"reject\",\"reason\":\"猜测当事实\"}\n```", false, true},
		{"unknown verdict", `{"verdict":"maybe","reason":"x"}`, false, false},
		{"no json", "I think it is fine", false, false},
		{"missing reason", `{"verdict":"approve"}`, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, ok := ParseReply(tc.raw)
			if ok != tc.ok || (ok && v.Pass != tc.pass) {
				t.Fatalf("ParseReply = %+v, %v; want pass=%v ok=%v", v, ok, tc.pass, tc.ok)
			}
		})
	}
	v, _ := ParseReply(`{"verdict":"approve","reason":"` + strings.Repeat("长", 300) + `"}`)
	if len([]rune(v.Reason)) > 241 {
		t.Fatalf("reason not clipped: %d runes", len([]rune(v.Reason)))
	}
}

type fakeChat struct {
	reply string
	err   error
}

func (f fakeChat) Available(context.Context) error { return nil }
func (f fakeChat) Chat(context.Context, []wikigen.Message) (wikigen.Reply, error) {
	return wikigen.Reply{Text: f.reply}, f.err
}

func TestJudgeNeedsExplicitApprove(t *testing.T) {
	p := &db.WikiPage{Title: "t", Slug: "s", Content: longBody}
	v, err := Judge(context.Background(), fakeChat{reply: `{"verdict":"approve","reason":"ok"}`}, p)
	if err != nil || !v.Pass {
		t.Fatalf("Judge = %+v, %v; want approve", v, err)
	}
	if _, err := Judge(context.Background(), fakeChat{reply: "not json"}, p); err == nil {
		t.Fatal("unparseable reply must fail closed")
	}
}

func TestGetConfigDefaultsAndClamp(t *testing.T) {
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	cfg, err := GetConfig(database)
	if err != nil || cfg.Enabled() {
		t.Fatalf("fresh config = %+v, %v; want all off", cfg, err)
	}
	for k, v := range map[string]string{
		"wiki_review_ai": "1", "wiki_review_auto": "1", "wiki_review_timeout_hours": "-5",
	} {
		if err := db.SetConfig(database, k, v); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err = GetConfig(database)
	if err != nil || !cfg.AI || !cfg.AutoApprove || cfg.TimeoutHours != 0 {
		t.Fatalf("config = %+v, %v; want AI+auto on, negative hours clamped", cfg, err)
	}
}
