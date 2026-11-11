package service

import (
	"context"
	"encoding/json"
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
		// The job shape the 2026-11-09 labelling run found 11 times: a
		// scheduled task whose catch block logs and then raises an alarm.
		// The failure is handled out-of-band, so this is not a swallow, and
		// the end-to-end test asserts the rules stay quiet about it. Before
		// matchBrace was fixed, this very file produced a "catch 块为空"
		// candidate.
		"src/main/java/a/OcbcSyncJob.java": `package a;

@Component
public class OcbcSyncJob {

    public void execute(String param) {
        try {
            syncBiz.sync(param);
        } catch (Exception e) {
            log.error("ocbcSyncJob|执行失败", e);
            PlatformClientForAlarm.sendExceptionAlarmMessage("【CBiPay】OCBC 同步 Job 执行异常", e);
        }
    }
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
	if result.Run.Files != 3 {
		t.Errorf("files = %d, want 3 (the two Feign files and the job)", result.Run.Files)
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
	// The log-and-alarm job must not appear at all. This assertion is the
	// end-to-end half of the 2026-11-09 labelling fix: the same shape fired
	// 11 times in real repositories.
	for _, f := range result.Stored {
		if strings.Contains(f.FilePath, "OcbcSyncJob") {
			t.Errorf("handled catch reported as swallowed: %+v", f)
		}
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
	// The preview must carry locations. Until 2026-11-09 the CLI filled
	// empty strings and the MCP returned nil, so a preview listed findings
	// you could not open: the report was unreadable exactly when it was
	// most useful, before anything was written.
	for _, f := range result.Findings {
		if f.FilePath == "" || f.RepoPath == "" {
			t.Errorf("dry-run finding has no location: %+v", f)
		}
	}
	preview := StoredPreview(result.Findings)
	if len(preview) != len(result.Findings) {
		t.Fatalf("preview conversion dropped rows: %d -> %d", len(result.Findings), len(preview))
	}
	for _, f := range preview {
		if f.FilePath == "" || f.RepoPath == "" {
			t.Errorf("StoredPreview lost the location: %+v", f)
		}
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

// TestReviewResultJSONUsesSnakeCase pins the wire shape the frontend reads.
//
// ReviewResult crosses the Wails binding and the RPC bridge directly, so its
// json tags are the contract: without them Go emits "AlreadyDone" and the page's
// `result.already_done` is undefined — every idempotent re-run would then be
// reported as a fresh one. Cheap test, silent bug otherwise.
func TestReviewResultJSONUsesSnakeCase(t *testing.T) {
	blob, err := json.Marshal(ReviewResult{
		Run:         &db.ReviewRun{ID: 7, RunDate: "2026-11-07"},
		AlreadyDone: true,
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(blob)
	for _, want := range []string{`"run"`, `"already_done":true`, `"dry_run":true`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
	for _, unwanted := range []string{`"AlreadyDone"`, `"DryRun"`, `"Run"`} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("Go field name %s leaked into the wire shape: %s", unwanted, got)
		}
	}
}

// Every rule switch is real: turning one off removes it, and a fresh install
// that never wrote any switch still runs all nine non-opt-in rules.
func TestReviewRuleSetHonoursAllRuleSwitches(t *testing.T) {
	svc, _ := setupService(t)
	has := func(id string) bool {
		for _, r := range svc.reviewRuleSet() {
			if r.ID() == id {
				return true
			}
		}
		return false
	}
	if rules := svc.reviewRuleSet(); len(rules) != 9 {
		t.Fatalf("default config must keep 9 non-opt-in rules, got %d", len(rules))
	}
	if has("sensitive_log") {
		t.Fatal("sensitive_log is opt-in and must stay off by default")
	}
	if err := db.SetConfig(svc.db, reviewOptInRuleKey+"feign_missing_body", "0"); err != nil {
		t.Fatal(err)
	}
	if has("feign_missing_body") {
		t.Fatal("turning feign_missing_body off must remove it from the rule set")
	}
	if rules := svc.reviewRuleSet(); len(rules) != 8 {
		t.Fatalf("only the switched-off rule should disappear, got %d", len(rules))
	}
}

// crossRepoReviewPair builds two separate repositories: A has a commit dated
// yesterday with a Feign client missing @RequestBody, B has an old commit
// outside the review window with the provider that binds @RequestBody.
func crossRepoReviewPair(t *testing.T) (dirA, dirB string) {
	t.Helper()
	makeRepo := func(date string, files map[string]string) string {
		t.Helper()
		dir := t.TempDir()
		run := func(args ...string) {
			t.Helper()
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			cmd.Env = append(cmd.Environ(),
				"GIT_AUTHOR_NAME=Tester", "GIT_AUTHOR_EMAIL=t@example.com",
				"GIT_COMMITTER_NAME=Tester", "GIT_COMMITTER_EMAIL=t@example.com",
				"GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date,
			)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v (%s)", args, err, out)
			}
		}
		run("init", "-q")
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
		run("commit", "-q", "-m", "init")
		return dir
	}
	dirA = makeRepo(time.Now().AddDate(0, 0, -1).Format("2006-01-02T12:00:00"), map[string]string{
		"src/main/java/a/PayClient.java": `package a;

@FeignClient(name = "pay")
public interface PayClient {

    @PostMapping("/pay/query")
    Result<PayInfo> query(PayQueryReq req);
}
`,
	})
	// B's only commit is long before the window, so it produces no findings of
	// its own but its files must still be searchable from A.
	dirB = makeRepo("2020-01-01T12:00:00", map[string]string{
		"src/main/java/b/PayController.java": `package b;

@RestController
public class PayController {

    @PostMapping("/pay/query")
    Result<PayInfo> query(@RequestBody PayQueryReq req) { return null; }
}
`,
	})
	return
}

// ADR-0053 D1 end-to-end: A's Feign finding must carry B's provider evidence
// even though B had no commit in the window.
func TestRunReviewFindsCrossRepoProvider(t *testing.T) {
	svc, _ := setupService(t)
	dirA, dirB := crossRepoReviewPair(t)
	pid := seedProject(t, svc.db, "pair", t.TempDir())
	seedRepo(t, svc.db, dirA, pid)
	seedRepo(t, svc.db, dirB, pid)
	if _, err := svc.db.Exec("UPDATE projects SET collected = TRUE WHERE id = ?", pid); err != nil {
		t.Fatal(err)
	}

	result, err := svc.RunReview(context.Background(), ReviewOptions{Date: time.Now().AddDate(0, 0, -1)})
	if err != nil {
		t.Fatalf("RunReview: %v", err)
	}
	if result.Run.Commits != 1 {
		t.Errorf("commits = %d, want 1 (only A has a commit in the window)", result.Run.Commits)
	}
	var hit *db.ReviewFinding
	for i := range result.Stored {
		if result.Stored[i].Rule == "feign_missing_body" {
			hit = &result.Stored[i]
		}
	}
	if hit == nil {
		t.Fatalf("no feign_missing_body finding stored: %+v", result.Stored)
	}
	if !strings.Contains(hit.Evidence, dirB) {
		t.Errorf("evidence must name the provider repository B (%s): %q", dirB, hit.Evidence)
	}
	if !strings.Contains(hit.Evidence, "@RequestBody") {
		t.Errorf("evidence must carry the provider's @RequestBody: %q", hit.Evidence)
	}
	if hit.RepoPath != dirA {
		t.Errorf("finding must be stamped on A, got %q want %q", hit.RepoPath, dirA)
	}
	if hit.Verdict != "unverified" {
		t.Errorf("verdict must stay unverified, got %q", hit.Verdict)
	}
	// Encoded repo keys must never reach the database, CLI or model prompt.
	if strings.Contains(hit.Evidence, "\x00") || strings.Contains(hit.Message, "\x00") {
		t.Errorf("encoded key leaked into the report: evidence=%q message=%q", hit.Evidence, hit.Message)
	}
}
