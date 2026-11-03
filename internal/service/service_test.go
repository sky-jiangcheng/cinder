package service

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"repotory/internal/core/git"
	"repotory/internal/db"
	"repotory/internal/domain"
	"repotory/internal/stats"
)

// fakeGit is an in-memory git.Provider used to test the stats refresh and
// enrichment paths without shelling out to git.
type fakeGit struct {
	git.Provider // embed for the methods we do not override

	ranges map[string][]stats.DailyEntry // "path|author" -> entries

	// filterAuthor each recent-commit call received. Pinned because the "mine"
	// author filter on the status strip and the overview's recent commits
	// blanked them entirely when git user.name matched no committer (status
	// strip showed "no commits" over actively committing repos, E2E 2026-10-21).
	recentCommitAuthor  string
	recentCommitsAuthor string
}

func (f *fakeGit) QueryStatsRange(repoPath, startDate, endDate, author string) ([]stats.DailyEntry, error) {
	return f.ranges[repoPath+"|"+author], nil
}

func (f *fakeGit) GetTodayDate() string      { return "2026-08-16" }
func (f *fakeGit) GetYesterdayDate() string  { return "2026-08-15" }
func (f *fakeGit) ValidateDate(string) error { return nil }
func (f *fakeGit) IsWorkday(string) bool     { return true }
func (f *fakeGit) GetRecentCommit(_ []string, filterAuthor string) (*stats.RecentCommit, error) {
	f.recentCommitAuthor = filterAuthor
	return &stats.RecentCommit{
		Hash: "abc123", Time: "2026-08-16 10:00:00", Message: "teammate work",
		Author: "teammate", Repo: "/code/star", Branch: "main",
	}, nil
}
func (f *fakeGit) GetRecentCommits(_ []string, filterAuthor string, _ int) ([]stats.RecentCommit, error) {
	f.recentCommitsAuthor = filterAuthor
	return nil, nil
}

func setupService(t *testing.T) (*Service, *fakeGit) {
	t.Helper()
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	// The last update download is persisted in the user's data dir at
	// runtime. A test must never read (or write) the real one — so the path
	// points at a temp file for the whole test.
	stateDir := t.TempDir()
	prevStatePath := downloadStatePathOverride()
	setDownloadStatePathForTest(func() (string, error) {
		return filepath.Join(stateDir, "update-download.json"), nil
	})
	t.Cleanup(func() { setDownloadStatePathForTest(prevStatePath) })
	fg := &fakeGit{ranges: map[string][]stats.DailyEntry{}}
	return NewWithDeps(database, fg, nil, "me"), fg
}

func seedProject(t *testing.T, database *sql.DB, name, root string) int64 {
	t.Helper()
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := db.SyncProjectTx(tx, name, root, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return pid
}

func seedRepo(t *testing.T, database *sql.DB, path string, pid int64) int64 {
	t.Helper()
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertRepositoryTx(tx, path, pid); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	repos, err := db.GetRepositoriesByProjectID(database, pid)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range repos {
		if r.Path == path {
			return r.ID
		}
	}
	t.Fatalf("seeded repo %s not found", path)
	return 0
}

func TestRefreshProjectHistoryUpsertsAllAndMine(t *testing.T) {
	svc, fg := setupService(t)
	database := svc.db

	pid := seedProject(t, database, "p", "/tmp/p")
	seedRepo(t, database, "/tmp/p/r1", pid)

	fg.ranges["/tmp/p/r1|"] = []stats.DailyEntry{
		{Date: "2026-08-10", FilesChanged: 2, LinesAdded: 40, LinesDeleted: 4},
		{Date: "2026-08-11", FilesChanged: 0, LinesAdded: 0, LinesDeleted: 0}, // zero row must be skipped
	}
	fg.ranges["/tmp/p/r1|me"] = []stats.DailyEntry{
		{Date: "2026-08-10", FilesChanged: 1, LinesAdded: 30, LinesDeleted: 2},
	}

	if err := svc.RefreshProjectHistory(context.Background(), pid); err != nil {
		t.Fatalf("RefreshProjectHistory: %v", err)
	}

	rows, err := db.GetStatsByProject(database, pid, "2026-08-10")
	if err != nil {
		t.Fatal(err)
	}
	byAuthor := map[string]domain.DailyStat{}
	for _, r := range rows {
		byAuthor[r.Author] = r
	}
	if byAuthor["all"].LinesAdded != 40 {
		t.Errorf("expected all-author added 40, got %+v", byAuthor["all"])
	}
	if byAuthor["me"].LinesAdded != 30 {
		t.Errorf("expected personal added 30, got %+v", byAuthor["me"])
	}
	if len(rows) != 2 {
		t.Errorf("zero-entry day must be skipped, got %d rows", len(rows))
	}
}

func TestRefreshProjectHistoryNoRepos(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "empty", "/tmp/empty")
	if err := svc.RefreshProjectHistory(context.Background(), pid); err == nil {
		t.Error("expected error for project without repos")
	}
}

func TestGetProjectsEnrichment(t *testing.T) {
	svc, _ := setupService(t)
	database := svc.db

	pid := seedProject(t, database, "p", "/tmp/p")
	repoID := seedRepo(t, database, "/tmp/p/r1", pid)
	if err := db.UpsertDailyStat(database, repoID, "2026-08-15", "me", 1, 600, 10, 2); err != nil {
		t.Fatal(err)
	}

	projects := svc.GetProjects("2026-08-15", false)
	// The dashboard list now also carries the seeded default ("Uncategorized")
	// landing project (ADR-0023), so the result is the repo-backed project plus
	// the default. Locate the seeded one by id rather than by index/count.
	if len(projects) != 2 {
		t.Fatalf("expected the seeded project plus the default landing project, got %d", len(projects))
	}
	var p ProjectResponse
	for _, x := range projects {
		if x.ID == pid {
			p = x
		}
	}
	if p.ID != pid {
		t.Fatalf("seeded project %d missing from GetProjects output: %+v", pid, projects)
	}
	if p.RepoCount != 1 || p.MyAdded != 600 || p.TotalAdded != 600 {
		t.Errorf("enrichment wrong: %+v", p)
	}
	if p.BelowStandard {
		t.Error("600 lines should not be below the default 500 standard")
	}
}

func TestGetProjectsBelowStandard(t *testing.T) {
	svc, _ := setupService(t)
	database := svc.db

	pid := seedProject(t, database, "p", "/tmp/p")
	repoID := seedRepo(t, database, "/tmp/p/r1", pid)
	if err := db.UpsertDailyStat(database, repoID, "2026-08-15", "me", 1, 10, 1, 1); err != nil {
		t.Fatal(err)
	}

	projects := svc.GetProjects("2026-08-15", false)
	if !projects[0].BelowStandard {
		t.Error("10 lines on a workday should be below the 500 standard")
	}
}

func TestGetHeatmapDataProjectFilter(t *testing.T) {
	svc, _ := setupService(t)
	database := svc.db

	pidA := seedProject(t, database, "a", "/tmp/a")
	pidB := seedProject(t, database, "b", "/tmp/b")
	repoA := seedRepo(t, database, "/tmp/a/r1", pidA)
	repoB := seedRepo(t, database, "/tmp/b/r2", pidB)
	_ = db.UpsertDailyStat(database, repoA, "2026-08-01", "me", 1, 10, 0, 3)
	_ = db.UpsertDailyStat(database, repoB, "2026-08-01", "me", 1, 5, 0, 1)

	global := svc.GetHeatmapData(0)
	if len(global.Days) != 1 || global.Days[0].LinesAdded != 15 {
		t.Fatalf("global heatmap wrong: %+v", global.Days)
	}
	if global.Days[0].Commits != 4 {
		t.Fatalf("global heatmap should sum commits (4), got %+v", global.Days[0])
	}
	// With a personal git user configured, only "me" rows are aggregated.
	projA := svc.GetHeatmapData(pidA)
	if len(projA.Days) != 1 || projA.Days[0].LinesAdded != 10 {
		t.Fatalf("project heatmap wrong: %+v", projA.Days)
	}
}

func TestUpdateProjectLevelValidation(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "p", "/tmp/p")
	if _, err := svc.UpdateProjectLevel(pid, "sideways", false); err == nil {
		t.Error("expected error for invalid direction")
	}
	if _, err := svc.UpdateProjectLevel(999, "up", true); err == nil {
		t.Error("expected error for missing project")
	}
}

func TestGetProjectSummary(t *testing.T) {
	svc, _ := setupService(t)
	database := svc.db
	pid := seedProject(t, database, "p", "/tmp/p")
	seedRepo(t, database, "/tmp/p/r1", pid)
	seedRepo(t, database, "/tmp/p/r2", pid)

	summary, err := svc.GetProjectSummary(pid)
	if err != nil {
		t.Fatalf("GetProjectSummary: %v", err)
	}
	if summary.RepoCount != 2 || len(summary.Repos) != 2 {
		t.Errorf("expected 2 repos, got %d", summary.RepoCount)
	}
}
