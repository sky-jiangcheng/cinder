package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"repotory/internal/db"
)

// The review pipeline is tested end-to-end against a real throwaway git repo
// instead of a mocked git: the two things most likely to break quietly are
// "which commits land in the window" and "which lines count as added", and
// both are answered by git itself. A fake would let a wrong --since format
// pass every test.

// reviewRepo builds a repo whose single commit is dated yesterday and contains
// a Feign client with an unbound POJO parameter.
func reviewRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02T12:00:00")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=Tester", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=Tester", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_AUTHOR_DATE="+yesterday, "GIT_COMMITTER_DATE="+yesterday,
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init", "-q")

	files := map[string]string{
		"src/main/java/a/PayClient.java": `package a;

@FeignClient(name = "pay")
public interface PayClient {

    @PostMapping("/pay/query")
    Result<PayInfo> query(PayQueryReq req);
}
`,
		"src/main/java/a/PayController.java": `package a;

@RestController
public class PayController {

    @PostMapping("/pay/query")
    Result<PayInfo> query(@RequestBody PayQueryReq req) { return null; }
}
`,
	}
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("commit", "-q", "-m", "add pay client")
	return dir
}

// collectedProject seeds a project marked collected plus one repository, which
// is what the default scope resolves to.
func collectedProject(t *testing.T, svc *Service, repoPath string) int64 {
	t.Helper()
	pid := seedProject(t, svc.db, "pay", filepath.Dir(repoPath))
	seedRepo(t, svc.db, repoPath, pid)
	if _, err := svc.db.Exec("UPDATE projects SET collected = TRUE WHERE id = ?", pid); err != nil {
		t.Fatal(err)
	}
	return pid
}

func TestRunReviewEndToEnd(t *testing.T) {
	svc, _ := setupService(t)
	dir := reviewRepo(t)
	collectedProject(t, svc, dir)

	yesterday := time.Now().AddDate(0, 0, -1)
	result, err := svc.RunReview(context.Background(), ReviewOptions{Date: yesterday})
	if err != nil {
		t.Fatalf("RunReview: %v", err)
	}
	if result.DryRun {
		t.Fatal("expected a real run")
	}
	if result.Run.Commits != 1 {
		t.Errorf("commits = %d, want 1", result.Run.Commits)
	}
	if result.Run.Files != 2 {
		t.Errorf("files = %d, want 2 (both Java files)", result.Run.Files)
	}
	if len(result.Stored) == 0 {
		t.Fatalf("no findings stored; candidates = %d", result.Run.Candidates)
	}

	var sawFeign bool
	for _, f := range result.Stored {
		if f.Rule == "feign_missing_body" {
			sawFeign = true
			if f.Verdict != "unverified" {
				t.Errorf("feign_missing_body must stay unverified, got %q", f.Verdict)
			}
			if f.Evidence == "" || !strings.Contains(f.Evidence, ">") {
				t.Errorf("evidence should mark the hit line, got %q", f.Evidence)
			}
		}
	}
	if !sawFeign {
		t.Errorf("expected a feign_missing_body finding, got %+v", result.Stored)
	}
	if result.Run.Status != db.ReviewStatusDone {
		t.Errorf("status = %q, want done", result.Run.Status)
	}
}

// A second run for the same day must not double the report (ADR-0053 验收 3):
// the unique key turns it into "already done", and --force replaces.
func TestRunReviewIsIdempotent(t *testing.T) {
	svc, _ := setupService(t)
	dir := reviewRepo(t)
	collectedProject(t, svc, dir)
	yesterday := time.Now().AddDate(0, 0, -1)

	first, err := svc.RunReview(context.Background(), ReviewOptions{Date: yesterday})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}

	second, err := svc.RunReview(context.Background(), ReviewOptions{Date: yesterday})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !second.AlreadyDone {
		t.Fatal("second run should report AlreadyDone")
	}
	if second.Run.ID != first.Run.ID {
		t.Errorf("second run created a new row: %d vs %d", second.Run.ID, first.Run.ID)
	}

	forced, err := svc.RunReview(context.Background(), ReviewOptions{Date: yesterday, Force: true})
	if err != nil {
		t.Fatalf("forced run: %v", err)
	}
	if len(forced.Stored) != len(first.Stored) {
		t.Errorf("forced re-run changed the finding count: %d -> %d", len(first.Stored), len(forced.Stored))
	}

	rows, err := db.ReviewFindings(svc.db, first.Run.ID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(first.Stored) {
		t.Errorf("findings accumulated across runs: %d rows for %d findings", len(rows), len(first.Stored))
	}
}

func TestRunReviewDryRunWritesNothing(t *testing.T) {
	svc, _ := setupService(t)
	dir := reviewRepo(t)
	collectedProject(t, svc, dir)

	result, err := svc.RunReview(context.Background(), ReviewOptions{
		Date:   time.Now().AddDate(0, 0, -1),
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !result.DryRun || len(result.Findings) == 0 {
		t.Fatalf("dry run should compute findings in memory, got %+v", result)
	}
	if result.Stored != nil {
		t.Error("dry run must not return stored rows")
	}
	run, err := db.LatestReviewRun(svc.db)
	if err != nil {
		t.Fatal(err)
	}
	if run != nil {
		t.Errorf("dry run wrote a run row: %+v", run)
	}
}

// Oversized files are skipped AND counted — the counter is what tells the user
// the report is partial (ADR-0053 验收 1).
func TestRunReviewCountsSkippedFiles(t *testing.T) {
	svc, _ := setupService(t)
	dir := reviewRepo(t)
	collectedProject(t, svc, dir)
	if err := db.SetConfig(svc.db, reviewMaxDiffKey, "1"); err != nil {
		t.Fatal(err)
	}

	result, err := svc.RunReview(context.Background(), ReviewOptions{Date: time.Now().AddDate(0, 0, -1)})
	if err != nil {
		t.Fatalf("RunReview: %v", err)
	}
	if result.Run.Skipped == 0 {
		t.Error("expected skipped files to be counted")
	}
	if len(result.Stored) != 0 {
		t.Errorf("files over budget must not be reviewed, got %d findings", len(result.Stored))
	}
}

// With AI off — the default — no model call may happen, so ai_calls stays 0.
func TestRunReviewNoAICallsWithoutModel(t *testing.T) {
	svc, _ := setupService(t)
	dir := reviewRepo(t)
	collectedProject(t, svc, dir)

	result, err := svc.RunReview(context.Background(), ReviewOptions{
		Date: time.Now().AddDate(0, 0, -1),
		NoAI: true,
	})
	if err != nil {
		t.Fatalf("RunReview: %v", err)
	}
	if result.Run.AICalls != 0 {
		t.Errorf("ai_calls = %d, want 0", result.Run.AICalls)
	}
}

func TestReviewReposBadScope(t *testing.T) {
	svc, _ := setupService(t)
	if _, err := svc.reviewRepos("project:abc"); err == nil {
		t.Fatal("expected an error for a non-numeric project id")
	}
	if _, err := svc.reviewRepos("everything"); err == nil {
		t.Fatal("expected an error for an unknown scope")
	}
}

func TestReviewRuleSetHonoursOptIn(t *testing.T) {
	svc, _ := setupService(t)
	has := func(id string) bool {
		for _, r := range svc.reviewRuleSet() {
			if r.ID() == id {
				return true
			}
		}
		return false
	}
	if has("sensitive_log") {
		t.Error("sensitive_log must be off by default")
	}
	if !has("feign_missing_body") {
		t.Error("feign_missing_body must be on by default")
	}
	if err := db.SetConfig(svc.db, reviewOptInRuleKey+"sensitive_log", "1"); err != nil {
		t.Fatal(err)
	}
	if !has("sensitive_log") {
		t.Error("sensitive_log should turn on when review.rule.sensitive_log=1")
	}
}
