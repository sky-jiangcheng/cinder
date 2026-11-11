package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"repotory/internal/db"
)

func TestMetricsPersistedScopeAndToday(t *testing.T) {
	s, git := setupService(t)
	ctx := context.Background()
	p1 := seedProject(t, s.db, "single", fixturePath("/metrics/single"))
	r1 := seedRepo(t, s.db, fixturePath("/metrics/single"), p1)
	p2 := seedProject(t, s.db, "container", fixturePath("/metrics/group"))
	r2 := seedRepo(t, s.db, fixturePath("/metrics/group/a"), p2)
	r3 := seedRepo(t, s.db, fixturePath("/metrics/group/b"), p2)
	p3 := seedProject(t, s.db, "knowledge only", fixturePath("/metrics/notes"))
	orphanPath := fixturePath("/metrics/orphan")
	chatExec(t, s.db, "INSERT INTO repositories(path) VALUES (?)", orphanPath)
	var orphan int64
	if err := s.db.QueryRow("SELECT id FROM repositories WHERE path = ?", orphanPath).Scan(&orphan); err != nil {
		t.Fatal(err)
	}
	for i, id := range []int64{r1, r2, r3, orphan} {
		n := i + 1
		// The refresh path persists both all-author and personal rows. Only the
		// former count; yesterday/future data must not leak into today's metrics.
		for _, row := range []struct {
			date, author string
			multiplier   int
		}{
			{git.GetTodayDate(), "all", 1}, {git.GetTodayDate(), "me", 1},
			{git.GetYesterdayDate(), "all", 100}, {"2099-01-01", "all", 100},
		} {
			if err := db.UpsertDailyStat(s.db, id, row.date, row.author, 0, n*10*row.multiplier, n*row.multiplier, n*row.multiplier); err != nil {
				t.Fatal(err)
			}
		}
	}
	// DATE values from earlier versions can include a time component.
	chatExec(t, s.db, "UPDATE daily_stats SET stat_date = stat_date || ' 08:00:00' WHERE repository_id = ? AND author = 'all' AND stat_date = ?", r2, git.GetTodayDate())
	chatExec(t, s.db, "UPDATE repositories SET last_scanned_at = '2026-08-16 09:00:00' WHERE id = ?", r1)
	chatExec(t, s.db, "UPDATE repositories SET last_scanned_at = '2026-08-16 10:00:00' WHERE id = ?", r2)
	chatExec(t, s.db, "INSERT INTO repo_meta(repository_id, languages) VALUES (?, ?)", r1, `[{"language":"Go","count":120},{"language":"SQL","count":30}]`)
	for _, pid := range []int64{p1, p1, p2, p3} {
		chatExec(t, s.db, "INSERT INTO project_notes(project_id, content) VALUES (?, 'stored note')", pid)
	}
	for i, row := range []struct {
		project any
		status  string
	}{
		{p1, "pending"}, {p1, "approved"}, {p1, "rejected"}, {p2, "pending"}, {p2, "pending"}, {p3, "pending"}, {nil, "pending"},
	} {
		chatExec(t, s.db, "INSERT INTO wiki_pages(slug, title, kind, project_id, status) VALUES (?, 'page', 'concept', ?, ?)", string(rune('a'+i)), row.project, row.status)
	}
	lines := int64(150)
	early, late := "2026-08-16T09:00:00.000Z", "2026-08-16T10:00:00.000Z"
	// The default window equals "today", so each case's window sums must match
	// its legacy *Today trio — that identity is the legacy/new contract. The
	// helper fills the pointers from the trio; stale cases already set
	// Stale=true and keep their null counts.
	win := func(m Metrics) Metrics {
		m.Since, m.Until = git.GetTodayDate(), git.GetTodayDate()
		if m.Stale {
			return m
		}
		c, a, d := m.CommitsToday, m.AdditionsToday, m.DeletionsToday
		m.Commits, m.Additions, m.Deletions = &c, &a, &d
		return m
	}
	cases := []struct {
		slash string
		want  Metrics
	}{
		{"", win(Metrics{RepositoryCount: 4, CommitsToday: 10, AdditionsToday: 100, DeletionsToday: 10, NotesCount: 4, PendingReviewCount: 5, RefreshedAt: &late})},
		{"/metrics/single", win(Metrics{RepositoryCount: 1, TotalLines: &lines, CommitsToday: 1, AdditionsToday: 10, DeletionsToday: 1, NotesCount: 2, PendingReviewCount: 1, RefreshedAt: &early})},
		{"/metrics/single/src/../lib", win(Metrics{RepositoryCount: 1, TotalLines: &lines, CommitsToday: 1, AdditionsToday: 10, DeletionsToday: 1, NotesCount: 2, PendingReviewCount: 1, RefreshedAt: &early})},
		{"/metrics/group", win(Metrics{RepositoryCount: 2, CommitsToday: 5, AdditionsToday: 50, DeletionsToday: 5, NotesCount: 1, PendingReviewCount: 2, RefreshedAt: &late})},
		{"/metrics/group/a/src", win(Metrics{RepositoryCount: 1, CommitsToday: 2, AdditionsToday: 20, DeletionsToday: 2, NotesCount: 1, PendingReviewCount: 2, RefreshedAt: &late})},
		// No scan timestamp in scope: the window cannot be proven covered, so
		// it is stale with null counts even though legacy rows are visible.
		{"/metrics/orphan", win(Metrics{RepositoryCount: 1, CommitsToday: 4, AdditionsToday: 40, DeletionsToday: 4, Stale: true})},
		{"/metrics/notes", win(Metrics{NotesCount: 1, PendingReviewCount: 1, Stale: true})},
		{"/metrics/single-neighbor", Metrics{Since: git.GetTodayDate(), Until: git.GetTodayDate(), Stale: true}},
		{"/metrics/group/unknown", Metrics{Since: git.GetTodayDate(), Until: git.GetTodayDate(), Stale: true}}, // auto-grouped containers cannot adopt unknown children
		{"/never-seen", Metrics{Since: git.GetTodayDate(), Until: git.GetTodayDate(), Stale: true}},
	}
	for _, tc := range cases {
		t.Run(tc.slash, func(t *testing.T) {
			path := fixturePath(tc.slash)
			got, err := s.GetMetrics(ctx, path)
			if err != nil || !reflect.DeepEqual(got, &tc.want) {
				t.Fatalf("metrics(%q) = %+v, %v; want %+v", path, got, err, tc.want)
			}
			again, err := s.GetMetrics(ctx, path)
			if err != nil || !reflect.DeepEqual(got, again) {
				t.Fatalf("read refreshed data instead of observing it: %+v %v", again, err)
			}
		})
	}
}

func TestMetricsRangeWindowAndStaleness(t *testing.T) {
	s, git := setupService(t)
	ctx := context.Background()
	p1 := seedProject(t, s.db, "range", fixturePath("/range"))
	r1 := seedRepo(t, s.db, fixturePath("/range"), p1)
	// last_scanned_at lands on the fake "today" in UTC so the coverage prefix
	// matches: a window ending today is covered, a window ending later is not.
	chatExec(t, s.db, "UPDATE repositories SET last_scanned_at = '2026-08-16 10:00:00' WHERE id = ?", r1)
	if err := db.UpsertDailyStat(s.db, r1, git.GetTodayDate(), "all", 1, 100, 10, 5); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertDailyStat(s.db, r1, git.GetTodayDate(), "me", 9, 999, 99, 9); err != nil {
		t.Fatal(err) // personal rows never count toward window sums
	}
	if err := db.UpsertDailyStat(s.db, r1, git.GetYesterdayDate(), "all", 1, 40, 4, 2); err != nil {
		t.Fatal(err)
	}
	today, yest := git.GetTodayDate(), git.GetYesterdayDate()
	start, err := time.Parse("2006-01-02", today)
	if err != nil {
		t.Fatal(err)
	}

	// Multi-day window: both days, all-author rows only.
	got, err := s.GetMetricsRange(ctx, "", yest, today)
	if err != nil {
		t.Fatal(err)
	}
	if got.Since != yest || got.Until != today || got.Commits == nil || *got.Commits != 7 ||
		got.Additions == nil || *got.Additions != 140 || got.Deletions == nil || *got.Deletions != 14 || got.Stale {
		t.Fatalf("window = %+v", got)
	}
	// Single day back: yesterday only.
	got, err = s.GetMetricsRange(ctx, "", yest, yest)
	if err != nil {
		t.Fatal(err)
	}
	if got.Commits == nil || *got.Commits != 2 || got.Additions == nil || *got.Additions != 40 || got.Stale {
		t.Fatalf("yesterday = %+v", got)
	}
	// A window past the scan cannot be proven: null counts, stale flag. The
	// legacy trio keeps answering from what exists — its contract is pinned.
	// (Near-future, not 2099: the span cap rejects absurd windows first.)
	future := start.AddDate(0, 0, 4).Format("2006-01-02")
	got, err = s.GetMetricsRange(ctx, "", today, future)
	if err != nil {
		t.Fatal(err)
	}
	if got.Commits != nil || got.Additions != nil || got.Deletions != nil || !got.Stale {
		t.Fatalf("future window = %+v; want null counts, stale", got)
	}
	if got.CommitsToday != 5 || got.AdditionsToday != 100 {
		t.Fatalf("legacy trio moved with the window: %+v", got)
	}
	// Malformed and inverted windows are errors, not silent defaults.
	for _, bad := range [][2]string{{"not-a-date", ""}, {"", "16-08-2026"}, {today, yest}} {
		if _, err := s.GetMetricsRange(ctx, "", bad[0], bad[1]); err == nil {
			t.Fatalf("accepted window %+v", bad)
		}
	}
	// Span cap: 366 days is the widest window the backfill can ever have
	// filled; one day more must be refused, not answered from a partial cache.
	far := start.AddDate(0, 0, metricsRangeMaxDays).Format("2006-01-02")
	if _, err := s.GetMetricsRange(ctx, "", today, far); err == nil {
		t.Fatalf("accepted %d-day window beyond the %d cap", metricsRangeMaxDays+1, metricsRangeMaxDays)
	}
	if _, err := s.GetMetricsRange(ctx, "", start.AddDate(0, 0, -(metricsRangeMaxDays-1)).Format("2006-01-02"), today); err != nil {
		t.Fatalf("refused the max window: %v", err)
	}
	// Unknown path: window echoed, stale — "not in the library" is not "0
	// commits this week".
	got, err = s.GetMetricsRange(ctx, fixturePath("/never"), yest, today)
	if err != nil {
		t.Fatal(err)
	}
	if got.RepositoryCount != 0 || got.Commits != nil || !got.Stale || got.Since != yest || got.Until != today {
		t.Fatalf("unknown path = %+v", got)
	}
}

func TestMetricsTotalLinesRequiresCompleteCache(t *testing.T) {
	s, _ := setupService(t)
	ctx := context.Background()
	p1 := seedProject(t, s.db, "one", fixturePath("/loc/one"))
	r1 := seedRepo(t, s.db, fixturePath("/loc/one"), p1)
	p2 := seedProject(t, s.db, "two", fixturePath("/loc/two"))
	r2 := seedRepo(t, s.db, fixturePath("/loc/two"), p2)
	chatExec(t, s.db, "INSERT INTO repo_meta(repository_id, languages) VALUES (?, ?)", r1, `[{"language":"Go","count":10}]`)
	check := func(slash string, want *int64) {
		t.Helper()
		got, err := s.GetMetrics(ctx, fixturePath(slash))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.TotalLines, want) {
			t.Fatalf("path %q: LOC=%v, want %v", slash, got.TotalLines, want)
		}
	}
	check("", nil) // a known partial sum must never be presented as the total
	ten, thirty, zero := int64(10), int64(30), int64(0)
	check("/loc/one", &ten)
	for _, cache := range []string{"", "{}", "[]", "null", "invalid", `[{"language":"Go"}]`, `[{"language":"Go","count":null}]`, `[{"language":"Go","count":-1}]`, `[{"count":20}]`, `[{"language":"Go","count":1.5}]`, `[{"language":"Go","count":9223372036854775808}]`, `[{"language":"Go","count":9223372036854775807},{"language":"SQL","count":1}]`} {
		chatExec(t, s.db, "INSERT INTO repo_meta(repository_id, languages) VALUES (?, ?) ON CONFLICT(repository_id) DO UPDATE SET languages = excluded.languages", r2, cache)
		check("", nil)
		check("/loc/two", nil)
	}
	chatExec(t, s.db, "UPDATE repo_meta SET languages = ? WHERE repository_id = ?", `[{"language":"Go","count":20}]`, r2)
	check("", &thirty)
	chatExec(t, s.db, "UPDATE repo_meta SET languages = ? WHERE repository_id = ?", `[{"language":"Go","count":0}]`, r2)
	check("/loc/two", &zero) // recorded zero is distinct from missing
	check("", &ten)
	// Old caches may describe the whole container rather than this repository.
	chatExec(t, s.db, "UPDATE projects SET root_path = ? WHERE id = ?", fixturePath("/loc"), p2)
	check("/loc/two", nil)
	chatExec(t, s.db, "UPDATE projects SET root_path = ? WHERE id = ?", fixturePath("/loc/two"), p2)
	seedRepo(t, s.db, fixturePath("/loc/two/sibling"), p2)
	check("/loc/two", nil)
	check("", nil)
}

func TestMetricsEmptyValidationAndStorageErrors(t *testing.T) {
	s, git := setupService(t)
	ctx := context.Background()
	// An empty library is a window nobody scanned: stale, never a confident 0.
	empty := &Metrics{Since: git.GetTodayDate(), Until: git.GetTodayDate(), Stale: true}
	got, err := s.GetMetrics(ctx, "")
	if err != nil || !reflect.DeepEqual(got, empty) {
		t.Fatalf("empty: %+v, want %+v (%v)", got, empty, err)
	}
	for _, path := range []string{"relative", "  ", "/bad\x00", "/bad\n", "/bad\xff", "/" + strings.Repeat("x", MaxChatPathLen)} {
		if _, err := s.GetMetrics(ctx, path); err == nil {
			t.Fatalf("accepted invalid path %q", path)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	unknown := fixturePath("/unknown")
	for _, path := range []string{"", unknown} {
		if _, err := s.GetMetrics(cancelled, path); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled: %v", err)
		}
	}
	// Do not let a failed path-resolution query masquerade as unknown/empty.
	chatExec(t, s.db, "ALTER TABLE projects RENAME TO unavailable_projects")
	if _, err := s.GetMetrics(ctx, unknown); err == nil {
		t.Fatal("path resolution swallowed DB error")
	}
	_ = s.db.Close()
	for _, path := range []string{"", unknown} {
		if _, err := s.GetMetrics(ctx, path); err == nil {
			t.Fatal("closed DB returned metrics")
		}
	}
}
