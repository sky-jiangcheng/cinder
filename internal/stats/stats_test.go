package stats

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateDate_Valid(t *testing.T) {
	dates := []string{
		"2024-01-01",
		"2024-12-31",
		"2026-02-28",
		"2024-02-29", // leap year
	}
	for _, d := range dates {
		if err := ValidateDate(d); err != nil {
			t.Errorf("ValidateDate(%s) = %v, want nil", d, err)
		}
	}
}

func TestValidateDate_Invalid(t *testing.T) {
	dates := []string{
		"",
		"2024-13-01",
		"2024-00-01",
		"2024-01-32",
		"2024/01/01",
		"01-01-2024",
		"not-a-date",
		"2024-1-1",
		"; rm -rf /",
		"$(curl evil.com)",
		"`id`",
		"' OR '1'='1",
		"2024-01-01\\nrm -rf /",
		"0000-00-00",
	}
	for _, d := range dates {
		if err := ValidateDate(d); err == nil {
			t.Errorf("ValidateDate(%s) should have returned error", d)
		}
	}
}

func TestValidateAuthor_Valid(t *testing.T) {
	authors := []string{
		"",
		"John Doe",
		"user@example.com",
		"firstname.lastname",
		"user-name_123",
	}
	for _, a := range authors {
		if err := ValidateAuthor(a); err != nil {
			t.Errorf("ValidateAuthor(%s) = %v, want nil", a, err)
		}
	}
}

func TestValidateAuthor_Invalid(t *testing.T) {
	authors := []string{
		"$(curl evil.com)",
		"`id`",
		"'; DROP TABLE users; --",
		"\n",
		"\t",
	}
	for _, a := range authors {
		if err := ValidateAuthor(a); err == nil {
			t.Errorf("ValidateAuthor(%s) should have returned error", a)
		}
	}
}

func TestParseShortStat(t *testing.T) {
	input := `
a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0
 3 files changed, 15 insertions(+), 7 deletions(-)
fedcba0987654321fedcba0987654321fedcba09
 1 file changed, 100 insertions(+)
aabbccddeeff00112233445566778899aabbccdd
 2 files changed, 0 insertions(+), 50 deletions(-)
`
	result, err := parseShortStat(input)
	if err != nil {
		t.Fatalf("parseShortStat error: %v", err)
	}
	if result.FilesChanged != 6 {
		t.Errorf("FilesChanged = %d, want 6", result.FilesChanged)
	}
	if result.LinesAdded != 115 {
		t.Errorf("LinesAdded = %d, want 115", result.LinesAdded)
	}
	if result.LinesDeleted != 57 {
		t.Errorf("LinesDeleted = %d, want 57", result.LinesDeleted)
	}
}

func TestParseShortStat_Empty(t *testing.T) {
	result, err := parseShortStat("")
	if err != nil {
		t.Fatalf("parseShortStat error: %v", err)
	}
	if result.FilesChanged != 0 || result.LinesAdded != 0 || result.LinesDeleted != 0 {
		t.Error("parseShortStat should return zero Result for empty input")
	}
}

func TestGetTodayDate(t *testing.T) {
	date := GetTodayDate()
	if err := ValidateDate(date); err != nil {
		t.Errorf("GetTodayDate returned invalid date: %s", date)
	}
}

func TestGetYesterdayDate(t *testing.T) {
	date := GetYesterdayDate()
	if err := ValidateDate(date); err != nil {
		t.Errorf("GetYesterdayDate returned invalid date: %s", date)
	}
}

func TestDaysBefore(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"2026-10-20", 6, "2026-10-14"},
		{"2026-10-20", 0, "2026-10-20"},
		{"2026-01-02", 1, "2026-01-01"},   // crosses a month boundary
		{"2026-03-01", 1, "2026-02-28"},   // non-leap February
		{"2024-03-01", 1, "2024-02-29"},   // leap February
		{"2026-10-20", 400, "2025-09-15"}, // crosses a year boundary
	}
	for _, c := range cases {
		if got := DaysBefore(c.in, c.n); got != c.want {
			t.Errorf("DaysBefore(%q, %d) = %q; want %q", c.in, c.n, got, c.want)
		}
	}
}

// An unparsable date must not silently become an arbitrary one: a bad anchor
// would shift the whole activity window instead of degrading to "same day".
func TestDaysBefore_UnparsableDateReturnsInput(t *testing.T) {
	for _, in := range []string{"", "not-a-date", "2026-13-01", "2026-10-20 10:00:00"} {
		if got := DaysBefore(in, 6); got != in {
			t.Errorf("DaysBefore(%q, 6) = %q; want it returned unchanged", in, got)
		}
	}
}

func TestIsWorkday(t *testing.T) {
	// Monday 2024-01-01
	if !IsWorkday("2024-01-01") {
		t.Error("Monday should be a workday")
	}
	// Saturday 2024-01-06
	if IsWorkday("2024-01-06") {
		t.Error("Saturday should not be a workday")
	}
	// Sunday 2024-01-07
	if IsWorkday("2024-01-07") {
		t.Error("Sunday should not be a workday")
	}
}

func TestParseTimestamp(t *testing.T) {
	utc := func(y, mo, d, h, mi, s int) int64 {
		return time.Date(y, time.Month(mo), d, h, mi, s, 0, time.UTC).Unix()
	}
	cst := time.FixedZone("CST", 8*3600)
	tests := []struct {
		name string
		in   string
		want int64
	}{
		{"bare unix seconds (git %at)", "1700000000", 1700000000},
		{"internal space format", "2024-01-02 03:04:05", utc(2024, 1, 2, 3, 4, 5)},
		{"iso8601 Z", "2024-06-15T08:30:00Z", utc(2024, 6, 15, 8, 30, 0)},
		{"iso8601 offset", "2024-06-15T08:30:00+08:00", time.Date(2024, 6, 15, 8, 30, 0, 0, cst).Unix()},
		{"iso8601 no zone", "2024-06-15T08:30:00", utc(2024, 6, 15, 8, 30, 0)},
		{"git %ai with zone", "2024-06-15 08:30:00 +0800", time.Date(2024, 6, 15, 8, 30, 0, 0, cst).Unix()},
		{"date-only (git %ad --date=short)", "2024-06-15", utc(2024, 6, 15, 0, 0, 0)},
		{"git default author date + zone", "Sat Jun 15 08:30:00 2024 +0800", time.Date(2024, 6, 15, 8, 30, 0, 0, cst).Unix()},
		{"git default space-padded day", "Fri Jan  3 12:34:56 2025 +0000", utc(2025, 1, 3, 12, 34, 56)},
		{"surrounding whitespace/newline", "  2024-06-15T08:30:00Z\n", utc(2024, 6, 15, 8, 30, 0)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseTimestamp(tc.in); got != tc.want {
				t.Errorf("parseTimestamp(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseTimestamp_Unparseable(t *testing.T) {
	// Must not panic and must return 0 so latest-commit comparisons stay sane.
	for _, in := range []string{"", "   ", "not-a-date", "2024-13-45 99:99:99"} {
		if got := parseTimestamp(in); got != 0 {
			t.Errorf("parseTimestamp(%q) = %d, want 0", in, got)
		}
	}
}

// QueryStats against a real repository with a MERGE in the window — the
// CBiUnion shape (2026-11-16): a release branch merges a feature branch whose
// commits are days old. Two defects lived here together:
//   - --first-parent made git render the merge's diff against its first
//     parent, so the whole merged branch (+5430 lines measured live) was
//     counted as if the person who pressed merge had written it today;
//   - the rev-list count omitted HEAD, which exits 129 on git 2.33, the error
//     was swallowed, and every row this path wrote carried commits = 0.
//
// The assertion pins both halves: lines count ONLY what was really committed
// today (the merge contributes zero), and commits counts the merge like
// `git log` does.
func TestQueryStatsMergeWindow(t *testing.T) {
	root := t.TempDir()
	today := time.Now().Format("2006-01-02")
	old := time.Now().AddDate(0, 0, -3).Format("2006-01-02")

	run := func(date string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=tester", "GIT_AUTHOR_EMAIL=tester@example.com",
			"GIT_COMMITTER_NAME=tester", "GIT_COMMITTER_EMAIL=tester@example.com",
			"GIT_AUTHOR_DATE="+date+"T12:00:00", "GIT_COMMITTER_DATE="+date+"T12:00:00",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0640); err != nil {
			t.Fatal(err)
		}
	}

	run(today, "init", "-q", "-b", "main")
	run(today, "config", "user.name", "tester")
	run(today, "config", "user.email", "tester@example.com")

	// A real edit TODAY: 2 added lines.
	write("base.txt", "one\ntwo\n")
	run(today, "add", ".")
	run(today, "commit", "-q", "-m", "base")

	// The feature branch: 50 lines, committed THREE DAYS AGO (author and
	// committer dates both old — what a real feature branch looks like).
	run(old, "checkout", "-q", "-b", "feature")
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString("feature line\n")
	}
	write("feature.txt", sb.String())
	run(old, "add", ".")
	run(old, "commit", "-q", "-m", "feature work")

	// TODAY: merge the branch into main (--no-ff keeps a merge commit).
	run(today, "checkout", "-q", "main")
	run(today, "merge", "-q", "--no-ff", "-m", "Merge branch 'feature'", "feature")

	res, err := QueryStats(root, today, "")
	if err != nil {
		t.Fatalf("QueryStats: %v", err)
	}
	// The 50 feature lines were written days ago; they must NOT be attributed
	// to today through the merge's diff. Only the base commit's 2 lines count.
	if res.LinesAdded != 2 {
		t.Errorf("LinesAdded = %d, want 2 (the merge must not donate the branch's 50 lines)", res.LinesAdded)
	}
	// base + merge, counted the way `git log` counts them. The old command
	// (rev-list without HEAD) failed outright and reported 0.
	if res.Commits != 2 {
		t.Errorf("Commits = %d, want 2 (base + merge; a broken count silently reports 0)", res.Commits)
	}
}
