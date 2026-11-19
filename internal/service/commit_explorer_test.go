package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// explorerRepo builds a real git repository with one deterministic commit per
// step. Dates are pinned so the merged log's sort order never depends on how
// fast the fixture ran.
func explorerRepo(t *testing.T, steps []struct{ msg, author, content string }) (string, []string) {
	t.Helper()
	root := t.TempDir()
	run := func(env []string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	baseEnv := []string{
		"GIT_COMMITTER_NAME=tester", "GIT_COMMITTER_EMAIL=tester@example.com",
	}
	run(baseEnv, "init", "-q", "-b", "main")
	run(baseEnv, "config", "user.name", "tester")
	run(baseEnv, "config", "user.email", "tester@example.com")

	hashes := make([]string, 0, len(steps))
	for i, st := range steps {
		if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte(st.content), 0640); err != nil {
			t.Fatal(err)
		}
		run(baseEnv, "add", ".")
		env := append([]string{}, baseEnv...)
		env = append(env,
			"GIT_AUTHOR_NAME="+st.author, "GIT_AUTHOR_EMAIL="+st.author+"@example.com",
			fmt.Sprintf("GIT_AUTHOR_DATE=2026-11-0%d 10:00:00 +0800", i+1),
			fmt.Sprintf("GIT_COMMITTER_DATE=2026-11-0%d 10:00:00 +0800", i+1),
		)
		run(env, "commit", "-q", "-m", st.msg)
		hashes = append(hashes, run(baseEnv, "rev-parse", "HEAD"))
	}
	return root, hashes
}

func explorerSteps() []struct{ msg, author, content string } {
	return []struct{ msg, author, content string }{
		{"feat: first feature", "alice", "one\n"},
		{"fix: second bug", "bob", "one\ntwo\n"},
		{"feat: third feature", "alice", "one\ntwo\nthree\n"},
	}
}

func TestSearchCommitsFilterAndPaging(t *testing.T) {
	svc, _ := setupService(t)
	repo, hashes := explorerRepo(t, explorerSteps())
	pid := seedProject(t, svc.db, "p", filepath.Dir(repo))
	rid := seedRepo(t, svc.db, repo, pid)

	page, err := svc.SearchCommits(pid, 0, "", 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || len(page.Items) != 2 {
		t.Fatalf("first page: total=%d items=%d, want 3/2", page.Total, len(page.Items))
	}
	if page.Items[0].Message != "feat: third feature" {
		t.Errorf("newest first: got %q", page.Items[0].Message)
	}

	last, err := svc.SearchCommits(pid, 0, "", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if last.Total != 3 || len(last.Items) != 1 || last.Items[0].Message != "feat: first feature" {
		t.Fatalf("last page returned %+v", last)
	}

	byMessage, err := svc.SearchCommits(pid, rid, "BUG", 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if byMessage.Total != 1 || byMessage.Items[0].Message != "fix: second bug" {
		t.Fatalf("case-insensitive subject search returned %+v", byMessage)
	}

	byAuthor, err := svc.SearchCommits(pid, rid, "alice", 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if byAuthor.Total != 2 {
		t.Fatalf("author search total=%d, want 2", byAuthor.Total)
	}

	byHash, err := svc.SearchCommits(pid, rid, hashes[1][:7], 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if byHash.Total != 1 || byHash.Items[0].Hash != hashes[1] {
		t.Fatalf("hash-prefix search returned %+v", byHash)
	}
}

func TestCommitDiffAndFileSnapshot(t *testing.T) {
	svc, _ := setupService(t)
	repo, hashes := explorerRepo(t, explorerSteps())
	pid := seedProject(t, svc.db, "p", filepath.Dir(repo))
	rid := seedRepo(t, svc.db, repo, pid)

	diff, err := svc.GetCommitDiff(rid, hashes[2])
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Files) != 1 || diff.Files[0].Path != "notes.txt" {
		t.Fatalf("diff files = %+v", diff.Files)
	}
	if diff.Files[0].Added != 1 || diff.Files[0].Deleted != 0 {
		t.Fatalf("change counts = +%d/-%d, want +1/-0", diff.Files[0].Added, diff.Files[0].Deleted)
	}

	after, err := svc.GetCommitFile(rid, hashes[2], "notes.txt", false)
	if err != nil || !after.Exists || after.Content != "one\ntwo\nthree\n" {
		t.Fatalf("after = %+v err=%v", after, err)
	}
	before, err := svc.GetCommitFile(rid, hashes[2], "notes.txt", true)
	if err != nil || !before.Exists || before.Content != "one\ntwo\n" {
		t.Fatalf("before = %+v err=%v", before, err)
	}

	// First commit has no parent: the UI needs an honest "file created" side,
	// not an error that blanks the whole popup.
	root, err := svc.GetCommitFile(rid, hashes[0], "notes.txt", true)
	if err != nil || root.Exists {
		t.Fatalf("root parent = %+v err=%v", root, err)
	}

	if _, err := svc.GetCommitDiff(rid, "not-a-sha; rm -rf /"); err == nil {
		t.Fatal("invalid sha must be refused")
	}
	if _, err := svc.GetCommitFile(rid, hashes[2], "../escape", false); err == nil {
		t.Fatal("parent-escaping path must be refused")
	}
}
