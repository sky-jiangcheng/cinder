package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"repotory/internal/db"
	"repotory/internal/service/wikigen"
)

// slowChatStub answers like a local LLM: correctly, but slowly. The whole point of
// ADR-0016 is that this no longer blocks a caller, so the tests need a stub whose
// latency is observable rather than instant.
func slowChatStub(t *testing.T, delay time.Duration, ops string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var counter atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Add(1)
		time.Sleep(delay)
		w.Header().Set("Content-Type", "application/json")
		body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"content": ops},
		}}})
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &counter
}

func jobCorpus(t *testing.T, svc *Service) int64 {
	t.Helper()
	pid := seedProject(t, svc.db, "jobs", "/tmp/jobs")
	for _, title := range []string{"网关重试策略", "幂等键语义", "超时与 408"} {
		if _, err := db.CreateNoteEx(svc.db, pid, title,
			"支付网关最多重试三次，幂等键使用 invoice id；超时后返回 408。这段正文足够长，让编译有材料可编译，也避免薄页检查顺手把产出行拦掉。",
			"", "knowledge", "manual"); err != nil {
			t.Fatal(err)
		}
	}
	return pid
}

// Submission must return without doing work. A slow model is exactly the case
// this exists for, so the assertion is on wall-clock time, not on a mock.
func TestStartCompileJob_ReturnsImmediately(t *testing.T) {
	svc, _ := setupService(t)
	pid := jobCorpus(t, svc)
	ops := `[{"op":"create_page","slug":"gateway-retry","title":"Gateway Retry","kind":"concept","body":"支付网关最多重试三次，幂等键使用 invoice id。这条正文足够长以避开薄页阈值，从而让断言只关于异步而不是关于长度。"},{"op":"attach_note","slug":"gateway-retry","note_id":1}]`
	srv, calls := slowChatStub(t, 400*time.Millisecond, ops)
	configureCompile(t, svc, srv.URL)

	start := time.Now()
	id, err := svc.StartCompileJob(pid, 3)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("submission took %v; the whole point is that it does not wait on the model", elapsed)
	}
	if id <= 0 {
		t.Fatalf("no job id returned (%d)", id)
	}

	// Queued, not running: the worker picks it up on its own tick.
	j, err := svc.GetCompileJob(id)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != db.CompileJobQueued {
		t.Errorf("status right after submit = %s, want %s", j.Status, db.CompileJobQueued)
	}
	if calls.Load() != 0 {
		t.Error("the model was called during submission")
	}
}

// Drive the worker directly (deterministic, no ticker waiting) and require real
// progress and a terminal state with the totals a reviewer needs.
func TestRunNextCompileJob_CompletesWithProgress(t *testing.T) {
	svc, _ := setupService(t)
	pid := jobCorpus(t, svc)
	ops := `[{"op":"create_page","slug":"gateway-retry","title":"Gateway Retry","kind":"concept","body":"支付网关最多重试三次，幂等键使用 invoice id。这条正文足够长以避开薄页阈值，从而让断言只关于异步而不是关于长度。"}]`
	srv, _ := slowChatStub(t, 0, ops)
	configureCompile(t, svc, srv.URL)

	id, err := svc.StartCompileJob(pid, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !svc.runNextCompileJob(t.Context()) {
		t.Fatal("worker claimed nothing although a job was queued")
	}
	if svc.runNextCompileJob(t.Context()) {
		t.Error("worker claimed a second job when only one was queued (claim is not exclusive)")
	}

	j, err := svc.GetCompileJob(id)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != db.CompileJobDone {
		t.Fatalf("status = %s (error=%q note=%q), want %s", j.Status, j.Error, j.Note, db.CompileJobDone)
	}
	if j.NotesDone != 2 || j.NotesTotal != 2 {
		t.Errorf("progress = %d/%d, want 2/2", j.NotesDone, j.NotesTotal)
	}
	if j.PagesCreated == 0 {
		t.Error("job finished without recording what it produced")
	}
	if j.FinishedAt == "" {
		t.Error("a terminal state must carry a finish timestamp")
	}
	pending, err := db.ListWikiPagesByStatus(svc.db, db.WikiStatusPending, pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) == 0 {
		t.Error("the job produced no pending pages")
	}
	for _, p := range pending {
		if p.Source != compileSourceTag {
			t.Errorf("job-produced page %s has source %q", p.Slug, p.Source)
		}
	}
}

// Cancellation must stop further notes and leave no half-processed orphan. The
// ADR's own acceptance criterion asks for exactly this drill.
func TestCancelCompileJob_StopsBeforeNextNote(t *testing.T) {
	svc, _ := setupService(t)
	pid := jobCorpus(t, svc)
	srv, _ := slowChatStub(t, 0, `[{"op":"create_page","slug":"cancel-probe","title":"Cancel Probe","kind":"concept","body":"一条用于取消演练的正文，长度足够避开薄页阈值，好让下面的断言只关于取消而不是关于过滤。"}]`)
	configureCompile(t, svc, srv.URL)
	id, err := svc.StartCompileJob(pid, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CancelCompileJob(id); err != nil {
		t.Fatal(err)
	}
	// A canceled job must not be claimed at all.
	if svc.runNextCompileJob(t.Context()) {
		t.Error("worker claimed a canceled job")
	}
	j, err := svc.GetCompileJob(id)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != db.CompileJobCanceled {
		t.Errorf("status = %s, want %s", j.Status, db.CompileJobCanceled)
	}
	if j.NotesDone != 0 {
		t.Errorf("canceled job processed %d notes, want 0", j.NotesDone)
	}
}

// A progress write must not resurrect a job canceled mid-run: the loop checks
// between notes, but the last progress update could still land afterwards.
func TestUpdateCompileJobProgress_DoesNotUndoCancel(t *testing.T) {
	svc, _ := setupService(t)
	pid := jobCorpus(t, svc)
	id, err := db.CreateCompileJob(svc.db, pid, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`UPDATE compile_jobs SET status='running' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RequestCancelCompileJob(svc.db, id); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateCompileJobProgress(svc.db, id, &db.CompileJob{NotesDone: 1, NotesTotal: 3}); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetCompileJob(svc.db, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != db.CompileJobCanceled {
		t.Errorf("a late progress write moved the job back to %s", got.Status)
	}
}

// Crash recovery: 'running' from a dead process becomes an explicit failure. This
// runs at startup, so a stale row cannot be mistaken for live work forever.
func TestRecoverInterruptedCompileJobs(t *testing.T) {
	svc, _ := setupService(t)
	pid := jobCorpus(t, svc)
	stuck, err := db.CreateCompileJob(svc.db, pid, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`UPDATE compile_jobs SET status='running', started_at=CURRENT_TIMESTAMP WHERE id = ?`, stuck); err != nil {
		t.Fatal(err)
	}
	svc.recoverInterruptedCompileJobs()

	j, err := db.GetCompileJob(svc.db, stuck)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != db.CompileJobFailed {
		t.Errorf("status after recovery = %s, want %s", j.Status, db.CompileJobFailed)
	}
	if j.Error == "" {
		t.Error("a recovered job must explain itself, not just flip state")
	}
	if svc.runNextCompileJob(t.Context()) {
		t.Error("a recovered job was picked up again")
	}
}

// Rejected states: submission without the gate must fail loudly rather than
// enqueue work that would sit 'queued' forever.
func TestStartCompileJob_GateAndArgs(t *testing.T) {
	svc, _ := setupService(t)
	pid := jobCorpus(t, svc)
	if err := svc.UpdateConfig(wikiCompileKey, "0"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartCompileJob(pid, 2); err == nil {
		t.Error("a job was queued with compilation disabled; it would never run")
	}
	if _, err := svc.StartCompileJob(0, 2); err == nil {
		t.Error("a job without a project was accepted")
	}
	if _, err := svc.StartCompileJob(pid, 0); err == nil {
		t.Error("a zero-note job was accepted")
	}
	// The table is operational state: nothing should have been written by the
	// rejected calls above.
	var n int
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM compile_jobs").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d job row(s) written by rejected submissions", n)
	}
}

// The kernel must stay shared, not forked: the job path and the synchronous path
// have to agree on which notes a run covers, or the same "compile this project"
// means two different things depending on the button.
func TestCompileNoteTargets_SharesSelectionWithSyncPath(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "select", "/tmp/select")
	var ids []int64
	for i := 0; i < 4; i++ {
		n, err := db.CreateNoteEx(svc.db, pid, fmt.Sprintf("note %d", i), "body text long enough to be a real note body for selection", "", "knowledge", "manual")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, n.ID)
	}
	notes, err := db.ListNotes(svc.db, pid)
	if err != nil {
		t.Fatal(err)
	}
	// The selector is now the kernel's (wikigen.CompileNoteTargets); this test
	// pins that the job path takes the head — the newest note — not the tail.
	got := wikigen.CompileNoteTargets(notes, 2)
	if len(got) != 2 {
		t.Fatalf("targets = %d, want 2", len(got))
	}
	// db.ListNotes is newest-first; a run must take the head, not the tail.
	if got[0].ID != ids[len(ids)-1] {
		t.Errorf("first target = %d, want the newest note %d", got[0].ID, ids[len(ids)-1])
	}
}
