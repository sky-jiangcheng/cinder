package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitFixtureRepo creates a real git repo with one commit on a fixed date, so
// stats.QueryStats sees deterministic numbers without touching live history.
func gitFixtureRepo(t *testing.T, parent, name, date string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "fixture@example.com")
	run("config", "user.name", "Fixture")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	run("add", "a.txt")
	// GIT_AUTHOR_DATE/GIT_COMMITTER_DATE pin the commit into QueryStats' window.
	cmd := exec.Command("git", "commit", "-qm", "fixture")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE="+date+"T12:00:00 +0800",
		"GIT_COMMITTER_DATE="+date+"T12:00:00 +0800")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	return dir
}

func TestResolveStatsRepoPathsSingleRepoUnchanged(t *testing.T) {
	root := t.TempDir()
	repo := gitFixtureRepo(t, root, "repo", "2026-11-18")

	paths, err := resolveStatsRepoPaths(context.Background(), repo)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(paths) != 1 || paths[0] != repo {
		t.Fatalf("single repo = %v, want [%s]", paths, repo)
	}
}

func TestResolveStatsRepoPathsDirectoryExpandsAndDedups(t *testing.T) {
	root := t.TempDir()
	r1 := gitFixtureRepo(t, root, "a", "2026-11-18")
	r2 := gitFixtureRepo(t, root, "b", "2026-11-18")

	paths, err := resolveStatsRepoPaths(context.Background(), root)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// scanner resolves a symlinked root (/var -> /private/var on macOS), so
	// compare canonical paths to avoid a platform-specific false failure.
	canon := func(p string) string {
		resolved, err := filepath.EvalSymlinks(p)
		if err != nil {
			return p
		}
		return resolved
	}
	want := map[string]bool{canon(r1): true, canon(r2): true}
	if len(paths) != len(want) {
		t.Fatalf("got %d paths %v, want %d", len(paths), paths, len(want))
	}
	seen := map[string]bool{}
	for _, p := range paths {
		if !want[p] {
			t.Errorf("unexpected path %s", p)
		}
		if seen[p] {
			t.Errorf("duplicate path %s", p)
		}
		seen[p] = true
	}
}

func TestResolveStatsRepoPathsNoRepoErrors(t *testing.T) {
	root := t.TempDir()
	if _, err := resolveStatsRepoPaths(context.Background(), root); err == nil {
		t.Fatal("directory with no git repos must error, not return empty stats")
	}
}

func TestQueryDailyStatsDirectoryAggregates(t *testing.T) {
	root := t.TempDir()
	date := "2026-11-18"
	gitFixtureRepo(t, root, "a", date)
	gitFixtureRepo(t, root, "b", date)

	svc, _ := setupService(t)
	got, err := svc.toolQueryDailyStats(context.Background(), map[string]any{
		"repo_path": root,
		"date":      date,
	})
	if err != nil {
		t.Fatalf("toolQueryDailyStats: %v", err)
	}
	out := got.(map[string]any)
	if out["repo_count"] != 2 {
		t.Fatalf("repo_count = %v, want 2", out["repo_count"])
	}
	if out["commits"] != 2 {
		t.Errorf("commits = %v, want 2 (one per repo)", out["commits"])
	}
	if out["lines_added"] != 2 {
		t.Errorf("lines_added = %v, want 2", out["lines_added"])
	}
	repos, ok := out["repos"].([]map[string]any)
	if !ok || len(repos) != 2 {
		t.Fatalf("repos = %#v, want 2 entries", out["repos"])
	}
}
