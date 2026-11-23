// Package temporal parses date expressions out of a search query and turns
// them into a time window (ADR-0070). It is the third recall arm beside FTS5
// and vector KNN: "11月17号那天在解决什么问题" carries a structural signal — a
// calendar day — that neither trigrams nor embeddings represent, so both arms
// provably miss notes whose only match is their creation date.
//
// Deliberately narrow: pure string patterns, zero dependencies, deterministic
// for a fixed anchor. No NLP, no dictionary, no model. Shapes it does NOT
// parse (time-of-day, month-only, ranges, 最近N天, English dates) fall through
// to the existing arms unchanged.
package temporal

import (
	"context"
	"regexp"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"
)

// Window is a half-open instant range [From, To) in UTC. Calendar days are
// resolved in the anchor's location first, then converted: notes store
// created_at via SQLite CURRENT_TIMESTAMP (UTC), so a local day boundary is
// not a UTC day boundary.
type Window struct {
	From, To time.Time
}

type anchorKey struct{}

// WithAnchor pins "today" for deterministic replay (evaluation tools pin the
// annotation date so relative expressions like 上周五 keep their meaning).
func WithAnchor(ctx context.Context, t time.Time) context.Context {
	return context.WithValue(ctx, anchorKey{}, t)
}

// Anchor returns the pinned anchor, or time.Now() on the production path.
func Anchor(ctx context.Context) time.Time {
	if t, ok := ctx.Value(anchorKey{}).(time.Time); ok {
		return t
	}
	return time.Now()
}

// One combined pattern, leftmost match wins. Alternation order matters only
// at the same start position: the prefixed week form (上周五) must be tried
// before the bare weekday form (周五), or the bare form would win inside it.
var dateRe = regexp.MustCompile(
	`(20\d{2})[/-](\d{1,2})[/-](\d{1,2})` + // 1,2,3  ISO / slash date
		`|(20\d{2})年(\d{1,2})月(\d{1,2})[日号]?` + // 4,5,6  full Chinese date
		`|(\d{1,2})月(\d{1,2})[日号]` + // 7,8    month-day (日/号 required)
		`|(今天|今日|昨天|昨日|前天)` + // 9      relative day
		`|(上|本|这)(?:个)?(?:周|星期)([一二三四五六日天1-7])?` + // 10,11  prefixed week
		`|(?:周|星期)([一二三四五六日天1-7])`) // 12     bare weekday

var weekdayNum = map[string]int{
	"一": 1, "二": 2, "三": 3, "四": 4, "五": 5, "六": 6, "日": 7, "天": 7,
	"1": 1, "2": 2, "3": 3, "4": 4, "5": 5, "6": 6, "7": 7,
}

// Parse finds the first date expression in query and resolves it against
// anchor. ok=false means "no temporal signal" — the caller must leave its
// result untouched.
func Parse(query string, anchor time.Time) (Window, bool) {
	for _, idx := range dateRe.FindAllStringSubmatchIndex(query, -1) {
		m := make([]string, len(idx)/2)
		for i := range m {
			if idx[2*i] >= 0 {
				m[i] = query[idx[2*i]:idx[2*i+1]]
			}
		}
		// 版本周期 / 资本周转 contain 本周 but are not date expressions.
		// 上 is exempt: 查上周五 is legitimate with a CJK char before it.
		if (m[10] == "本" || m[10] == "这") && idx[0] > 0 {
			if r, _ := utf8.DecodeLastRuneInString(query[:idx[0]]); unicode.Is(unicode.Han, r) {
				continue
			}
		}
		if w, ok := resolve(m, anchor); ok {
			return w, true
		}
	}
	return Window{}, false
}

func resolve(m []string, anchor time.Time) (Window, bool) {
	loc := anchor.Location()
	day := func(y, mo, d int) (Window, bool) {
		if mo < 1 || mo > 12 || d < 1 || d > 31 {
			return Window{}, false
		}
		t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, loc)
		// Reject overflow like 2月30号 (time.Date normalises it into March).
		if t.Day() != d || int(t.Month()) != mo {
			return Window{}, false
		}
		return dayWindow(t), true
	}
	switch {
	case m[1] != "":
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		return day(y, mo, d)
	case m[4] != "":
		y, _ := strconv.Atoi(m[4])
		mo, _ := strconv.Atoi(m[5])
		d, _ := strconv.Atoi(m[6])
		return day(y, mo, d)
	case m[7] != "":
		mo, _ := strconv.Atoi(m[7])
		d, _ := strconv.Atoi(m[8])
		return day(anchor.Year(), mo, d)
	case m[9] != "":
		base := startOfDay(anchor)
		switch m[9] {
		case "今天", "今日":
			return dayWindow(base), true
		case "昨天", "昨日":
			return dayWindow(base.AddDate(0, 0, -1)), true
		case "前天":
			return dayWindow(base.AddDate(0, 0, -2)), true
		}
	case m[10] != "":
		ws := weekStart(anchor)
		if m[10] == "上" {
			ws = ws.AddDate(0, 0, -7)
		}
		if m[11] == "" {
			return Window{From: ws.UTC(), To: ws.AddDate(0, 0, 7).UTC()}, true
		}
		return dayWindow(ws.AddDate(0, 0, weekdayNum[m[11]]-1)), true
	case m[12] != "":
		// Bare weekday: the most recent occurrence on or before the anchor.
		// "周五" said on a Monday means last Friday, not the coming one.
		d := weekStart(anchor).AddDate(0, 0, weekdayNum[m[12]]-1)
		if d.After(startOfDay(anchor)) {
			d = d.AddDate(0, 0, -7)
		}
		return dayWindow(d), true
	}
	return Window{}, false
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func dayWindow(t time.Time) Window {
	return Window{From: t.UTC(), To: t.AddDate(0, 0, 1).UTC()}
}

// weekStart returns Monday 00:00 of the week containing t (Chinese convention).
func weekStart(t time.Time) time.Time {
	d := startOfDay(t)
	wd := int(d.Weekday())
	if wd == 0 {
		wd = 7
	}
	return d.AddDate(0, 0, -(wd - 1))
}