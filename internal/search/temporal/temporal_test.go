package temporal

import (
	"context"
	"testing"
	"time"
)

// Anchor: Monday 2026-11-23 15:00 in UTC+8, the annotation date of the v2
// evaluation set. Week (Monday-start) containing it: 11-23 .. 11-29.
var testAnchor = time.Date(2026, 11, 23, 15, 0, 0, 0, time.FixedZone("CST", 8*3600))

func localDay(y int, mo time.Month, d int) time.Time {
	return time.Date(y, mo, d, 0, 0, 0, 0, testAnchor.Location())
}

func TestParse(t *testing.T) {
	cases := []struct {
		name     string
		query    string
		wantFrom time.Time // local midnight; zero means "no window expected"
		wantTo   time.Time
	}{
		{"iso", "2026-11-17 那天有哪些会话交接？", localDay(2026, 11, 17), localDay(2026, 11, 18)},
		{"iso slash", "查 2026/11/17 的记录", localDay(2026, 11, 17), localDay(2026, 11, 18)},
		{"full chinese", "2026年11月17日在做什么", localDay(2026, 11, 17), localDay(2026, 11, 18)},
		{"month day 号", "11月17号那天 MiddlePlatform 项目在解决什么问题？", localDay(2026, 11, 17), localDay(2026, 11, 18)},
		{"month day 日", "11月17日的交接", localDay(2026, 11, 17), localDay(2026, 11, 18)},
		{"today", "今天记了什么", localDay(2026, 11, 23), localDay(2026, 11, 24)},
		{"yesterday", "昨天的会话", localDay(2026, 11, 22), localDay(2026, 11, 23)},
		{"昨日", "昨日代码提交量", localDay(2026, 11, 22), localDay(2026, 11, 23)},
		{"前天", "前天修了什么", localDay(2026, 11, 21), localDay(2026, 11, 22)},
		{"上周五", "上周五的会话交接记录了什么？", localDay(2026, 11, 20), localDay(2026, 11, 21)},
		{"上星期五行", "上星期五的交接", localDay(2026, 11, 20), localDay(2026, 11, 21)},
		{"本周一", "本周一干了什么", localDay(2026, 11, 23), localDay(2026, 11, 24)},
		{"这周五", "这周五的计划", localDay(2026, 11, 27), localDay(2026, 11, 28)},
		{"bare weekday past", "周五的会话", localDay(2026, 11, 20), localDay(2026, 11, 21)},
		{"bare weekday today", "周一的会话", localDay(2026, 11, 23), localDay(2026, 11, 24)},
		{"上周 whole", "上周都做了什么", localDay(2026, 11, 16), localDay(2026, 11, 23)},
		{"本周 whole", "本周的计划", localDay(2026, 11, 23), localDay(2026, 11, 30)},
		{"iso with evening", "2026-11-18 晚上那次会话修复了什么问题？", localDay(2026, 11, 18), localDay(2026, 11, 19)},
		{"no date", "看板页面的中英文切换按钮会切换哪些文案", time.Time{}, time.Time{}},
		{"版本周期 false positive", "版本周期任务怎么配置", time.Time{}, time.Time{}},
		{"资本周转 false positive", "资本周转率是什么", time.Time{}, time.Time{}},
		{"invalid day", "2月30号发生了什么", time.Time{}, time.Time{}},
		{"invalid month", "13月1号发生了什么", time.Time{}, time.Time{}},
		{"month only not parsed", "11月都在忙什么", time.Time{}, time.Time{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, ok := Parse(c.query, testAnchor)
			if c.wantFrom.IsZero() {
				if ok {
					t.Fatalf("Parse(%q) = %+v, want no window", c.query, w)
				}
				return
			}
			if !ok {
				t.Fatalf("Parse(%q) found no window", c.query)
			}
			if !w.From.Equal(c.wantFrom.UTC()) || !w.To.Equal(c.wantTo.UTC()) {
				t.Fatalf("Parse(%q) = [%s, %s), want [%s, %s)",
					c.query, w.From, w.To, c.wantFrom.UTC(), c.wantTo.UTC())
			}
		})
	}
}

// A local day in UTC+8 is NOT a UTC day: the window must start at 16:00Z of
// the previous day, or notes created in the local morning fall outside it.
func TestWindowConvertsLocalDayToUTC(t *testing.T) {
	w, ok := Parse("11月17号的交接", testAnchor)
	if !ok {
		t.Fatal("no window")
	}
	if got := w.From.UTC().Format("2006-01-02 15:04:05"); got != "2026-11-16 16:00:00" {
		t.Fatalf("From = %s, want 2026-11-16 16:00:00 (local midnight in UTC)", got)
	}
}

func TestAnchorContext(t *testing.T) {
	if got := Anchor(context.Background()); time.Since(got) > time.Minute {
		t.Fatalf("unpinned anchor should be ~now, got %s", got)
	}
	ctx := WithAnchor(context.Background(), testAnchor)
	if got := Anchor(ctx); !got.Equal(testAnchor) {
		t.Fatalf("pinned anchor = %s, want %s", got, testAnchor)
	}
}

// Determinism: same query + same anchor → byte-identical window, the property
// the evaluation gate relies on.
func TestParseDeterministic(t *testing.T) {
	q := "上周五的会话交接记录了什么？"
	a, _ := Parse(q, testAnchor)
	for i := 0; i < 10; i++ {
		b, _ := Parse(q, testAnchor)
		if a != b {
			t.Fatalf("run %d: %+v != %+v", i, a, b)
		}
	}
}
