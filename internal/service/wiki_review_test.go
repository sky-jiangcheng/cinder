package service

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"repotory/internal/db"
	"repotory/internal/service/wikigen"
	"repotory/internal/service/wikireview"
)

const reviewBody = "这条页面记录了一个明确的事实：支付网关在超时后会进入退避重试，" +
	"退避窗口按失败次数递增，且同一笔订单的重复通知会被幂等键合并。"

type reviewChat struct {
	reply string
	err   error
	calls int
}

func (c *reviewChat) Available(context.Context) error { return nil }
func (c *reviewChat) Chat(context.Context, []wikigen.Message) (wikigen.Reply, error) {
	c.calls++
	return wikigen.Reply{Text: c.reply}, c.err
}

func reviewTestPage(t *testing.T, svc *Service, slug, body string) *db.WikiPage {
	t.Helper()
	p, err := db.CreateWikiPageAs(svc.db, db.WikiKindConcept, slug, slug, 0,
		body, db.WikiStatusPending, "wiki-compile")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func reviewConfig(t *testing.T, svc *Service, ai, auto bool, hours int) wikireview.Config {
	t.Helper()
	for k, v := range map[string]string{
		"wiki_review_ai":            boolFlag(ai),
		"wiki_review_auto":          boolFlag(auto),
		"wiki_review_timeout_hours": "0",
	} {
		if err := db.SetConfig(svc.db, k, v); err != nil {
			t.Fatal(err)
		}
	}
	if hours > 0 {
		if err := db.SetConfig(svc.db, "wiki_review_timeout_hours", strconv.Itoa(hours)); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := wikireview.GetConfig(svc.db)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func boolFlag(on bool) string {
	if on {
		return "1"
	}
	return "0"
}

func pageReview(t *testing.T, svc *Service, id int64) *db.WikiPage {
	t.Helper()
	p, err := db.GetWikiPageByID(svc.db, id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWikiReviewAnnotationOnly(t *testing.T) {
	svc, _ := setupService(t)
	p := reviewTestPage(t, svc, "annotate-only", reviewBody)
	chat := &reviewChat{reply: `{"verdict":"reject","reason":"猜测当事实"}`}
	cfg := reviewConfig(t, svc, true, false, 0)

	svc.reviewOnePendingPage(context.Background(), chat, p, cfg, false)

	got := pageReview(t, svc, p.ID)
	if got.Status != db.WikiStatusPending || got.ReviewVerdict != wikireview.VerdictReject ||
		got.ReviewSource != wikireview.SourceAI || got.ReviewReason != "猜测当事实" {
		t.Fatalf("annotation unexpected: %+v", got)
	}
	// A second tick must not re-send the same body to a paid endpoint.
	svc.reviewOnePendingPage(context.Background(), chat, p, cfg, false)
	if chat.calls != 1 {
		t.Fatalf("model called %d times for one verdict", chat.calls)
	}
}

func TestWikiReviewAutoApproveRequiresModelApprove(t *testing.T) {
	svc, _ := setupService(t)
	chat := &reviewChat{reply: `{"verdict":"approve","reason":"内容具体"}`}
	cfg := reviewConfig(t, svc, true, true, 0)

	rejectedBody := reviewBody
	p := reviewTestPage(t, svc, "ai-reject", rejectedBody)
	chat.reply = `{"verdict":"reject","reason":"结论不可靠"}`
	svc.reviewOnePendingPage(context.Background(), chat, p, cfg, false)
	if got := pageReview(t, svc, p.ID); got.Status != db.WikiStatusPending {
		t.Fatalf("AI reject was published: status=%s", got.Status)
	}

	chat.reply = `{"verdict":"approve","reason":"内容具体"}`
	approved := reviewTestPage(t, svc, "ai-approve", reviewBody)
	svc.reviewOnePendingPage(context.Background(), chat, approved, cfg, false)
	got := pageReview(t, svc, approved.ID)
	if got.Status != db.WikiStatusApproved || got.ReviewSource != wikireview.SourceAuto ||
		got.ReviewVerdict != wikireview.VerdictApprove {
		t.Fatalf("model approve did not publish: %+v", got)
	}
}

func TestWikiReviewHardVetoBeatsTimeoutAndAI(t *testing.T) {
	svc, _ := setupService(t)
	p := reviewTestPage(t, svc, "thin-page", "TODO")
	chat := &reviewChat{reply: `{"verdict":"approve","reason":"ok"}`}
	cfg := reviewConfig(t, svc, true, true, 24)

	svc.reviewOnePendingPage(context.Background(), chat, p, cfg, true)
	got := pageReview(t, svc, p.ID)
	if got.Status != db.WikiStatusPending || got.ReviewVerdict != wikireview.VerdictReject ||
		got.ReviewSource != wikireview.SourceRule || chat.calls != 0 {
		t.Fatalf("hard veto did not win: %+v, calls=%d", got, chat.calls)
	}
}

func TestWikiReviewTimeoutPublishesWithoutAI(t *testing.T) {
	svc, _ := setupService(t)
	p := reviewTestPage(t, svc, "timed-out", reviewBody)
	chat := &reviewChat{err: errors.New("endpoint down")}
	cfg := reviewConfig(t, svc, false, false, 24)

	svc.reviewOnePendingPage(context.Background(), chat, p, cfg, true)
	got := pageReview(t, svc, p.ID)
	if got.Status != db.WikiStatusApproved || got.ReviewSource != wikireview.SourceTimeout ||
		chat.calls != 0 {
		t.Fatalf("timeout did not publish: %+v, calls=%d", got, chat.calls)
	}
}

func TestWikiReviewVerdictInvalidatedByEdit(t *testing.T) {
	svc, _ := setupService(t)
	p := reviewTestPage(t, svc, "edited-after-review", reviewBody)
	chat := &reviewChat{reply: `{"verdict":"reject","reason":"旧理由"}`}
	cfg := reviewConfig(t, svc, true, false, 0)

	svc.reviewOnePendingPage(context.Background(), chat, p, cfg, false)
	if got := pageReview(t, svc, p.ID); got.ReviewVerdict != wikireview.VerdictReject {
		t.Fatalf("precondition: verdict not stored: %+v", got)
	}
	if err := db.UpdateWikiPage(svc.db, p.ID, p.Title, p.Kind, reviewBody+" 补充事实。"); err != nil {
		t.Fatal(err)
	}
	if got := pageReview(t, svc, p.ID); got.ReviewVerdict != "" || got.ReviewReason != "" {
		t.Fatalf("stale verdict survived an edit: %+v", got)
	}
}

func TestWikiReviewOffDoesNothing(t *testing.T) {
	svc, _ := setupService(t)
	p := reviewTestPage(t, svc, "untouched", reviewBody)
	cfg := reviewConfig(t, svc, false, false, 0)
	chat := &reviewChat{reply: `{"verdict":"approve","reason":"ok"}`}

	svc.reviewOnePendingPage(context.Background(), chat, p, cfg, false)
	got := pageReview(t, svc, p.ID)
	if got.Status != db.WikiStatusPending || got.ReviewVerdict != "" || chat.calls != 0 {
		t.Fatalf("default-off install changed a page: %+v, calls=%d", got, chat.calls)
	}
}
