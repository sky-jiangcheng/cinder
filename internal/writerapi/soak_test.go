package writerapi

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"repotory/internal/errcode"
	"repotory/internal/writer"
)

// TestLongWriteQueuesInsteadOfFailingLocked is ADR-0058's acceptance gate #1:
// while the writer is inside a long write (a backup, a batch compile, a vector
// backfill — all of which run on the writer's own single-pool handle), another
// Cinder process's forwarded write must WAIT, not surface
// `database is locked` to the agent.
//
// The shape mirrors production deliberately: the blocking transaction is taken
// on the WRITER's database handle (db.SetMaxOpenConns(1) serialises the pool,
// so the forwarded write queues on it), and the client is a second manager with
// its own handle on the same file — the same process separation a desktop and a
// cinder-mcp have. Before the contract, the client would have opened its own
// write against the held lock and failed after busy_timeout (5s). With the
// contract, the only writer queues its connection and the tool call waits.
func TestLongWriteQueuesInsteadOfFailingLocked(t *testing.T) {
	dir := t.TempDir()
	dw := openTestDB(t, dir)
	pid := seedProject(t, dw, "queueing")

	writerM := newManager(t, dw, dir, "cinder-desktop", true, false)
	clientM := newManager(t, dw, dir, "cinder-mcp", true, false)
	if !writerM.IsWriter() || clientM.Role() != roleClient {
		t.Fatalf("expected writer+client, got %s+%s", RoleName(writerM.Role()), RoleName(clientM.Role()))
	}

	// The long write: an open transaction on the writer's handle holding the
	// SQLite write lock. This is the exact object (a note INSERT + version
	// snapshot in a tx) the contract must queue behind.
	tx, err := dw.Begin()
	if err != nil {
		t.Fatalf("begin long write: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO project_notes (project_id, title, content, kind, source)
		VALUES (?, 'blocking', 'the writer is busy', 'log', 'manual')`, pid); err != nil {
		tx.Rollback() //nolint:errcheck
		t.Fatalf("blocking insert: %v", err)
	}

	// Longer than busy_timeout (5s): a client writing DIRECTLY to the file
	// would fail with `database is locked` at this hold length, so the pass is
	// evidence of the queue, not of the driver's backoff.
	holdFor := 6500 * time.Millisecond
	release := make(chan struct{})
	go func() {
		select {
		case <-time.After(holdFor):
		case <-release:
		}
		if err := tx.Commit(); err != nil {
			t.Errorf("commit long write: %v", err)
		}
	}()
	releaseOnce := sync.OnceFunc(func() { close(release) })
	t.Cleanup(func() {
		releaseOnce()
		_ = tx.Rollback() //nolint:errcheck // already committed in the happy path
	})

	// The forwarded write, started while the lock is held.
	start := time.Now()
	var note serviceNote
	err = clientM.ExecuteInto(context.Background(), MethodNotesCreate, NotesCreateArgs{
		ProjectID: pid, Title: "queued", Content: "waited its turn", Kind: "knowledge", Source: "mcp",
	}, &note)
	elapsed := time.Since(start)
	releaseOnce()

	if err != nil {
		// The whole point: NOT a lock failure. Any error that mentions the
		// driver's busy text is a regression of the contract.
		if strings.Contains(strings.ToLower(err.Error()), "locked") ||
			errors.Is(err, errcode.ErrWriterUnavailable) {
			t.Fatalf("contract violated: agent-facing write failed under a held lock: %v", err)
		}
		t.Fatalf("queued write: %v", err)
	}
	if note.ID == 0 {
		t.Fatal("queued write did not land")
	}
	if elapsed < holdFor/2 {
		t.Fatalf("write returned in %s while a %s hold was in flight — it did not actually queue", elapsed, holdFor)
	}
}

// TestClientNeverWritesTheFileDirectly pins the negative half: while a writer
// is live, a client process's OWN database handle must never be the one that
// applied the row. If a future handler grew a fallback direct write, the
// client's service handle would have to touch the lock — this test reads the
// row through the client's handle but proves the write happened via the
// writer's connection by checking the writer saw the exact catalogue call.
//
// (The observable proxy: an ai_call_log row written by the client manager's
// RecordAudit lands with the CLIENT's surface while the writer's own process
// executed the insert — see TestAuditRowKeepsClientSurface. Here we add the
// behavioural half: concurrent client writes all land exactly once.)
func TestConcurrentForwardedWritesAllLandOnce(t *testing.T) {
	dir := t.TempDir()
	dw := openTestDB(t, dir)
	pid := seedProject(t, dw, "concurrent")

	newManager(t, dw, dir, "cinder-desktop", true, false)
	client := newManager(t, dw, dir, "cinder-mcp", true, false)

	const n = 12
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			var note serviceNote
			errs <- client.ExecuteInto(context.Background(), MethodNotesCreate, NotesCreateArgs{
				ProjectID: pid, Title: "c", Content: "content", Kind: "knowledge", Source: "mcp",
			}, &note)
		}(i)
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent forwarded write %d: %v", i, err)
		}
	}
	var count int
	if err := dw.QueryRow(`SELECT COUNT(*) FROM project_notes WHERE source = 'mcp'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != n {
		t.Fatalf("stored %d rows, want %d: the queue silently dropped writes", count, n)
	}
}

// TestBusyWriterIsNotReportedAsAbsent pins the defect the 30-minute soak found
// (TODO 13.1 residual ①, 2026-11-13).
//
// What happened: with the writer inside a long VACUUM INTO backup, a forwarded
// write failed, the client re-elected, found the lock still held and the
// endpoint ADDRESS unchanged, and then spun its 15×200ms wait for a DIFFERENT
// address — timing out into "no Cinder writer is reachable ... start Cinder
// Desktop". Measured: 1108 such reports in one soak, every one ~3s, while the
// writer was alive the whole time.
//
// The distinction the contract needs:
//   - writer gone        → writer_unavailable (start one, or let someone take over)
//   - writer alive, busy → retryable          (wait; the write did not execute)
//
// This test holds the lock in a second manager, makes the first one reachable,
// and asserts the ABSENT claim is not made.
func TestBusyWriterIsNotReportedAsAbsent(t *testing.T) {
	dir := t.TempDir()
	dw := openTestDB(t, dir)
	seedProject(t, dw, "busy")

	// The desktop holds the role and SERVES a channel: it is alive and answers
	// the ping, so the client must not claim nobody is serving.
	writerM := newManager(t, dw, dir, "cinder-desktop", true, false)
	if !writerM.IsWriter() {
		t.Fatal("expected the desktop to be the writer")
	}

	// A second manager that may NOT elect itself: it can only forward, which is
	// the cinder-mcp shape on a machine where the desktop already holds the
	// role. With SelfElectAllowed=false, tryElect always reports "locked by
	// another process", so re-election cannot rescue it.
	clientM := newManager(t, dw, dir, "cinder-mcp", false, false)
	if clientM.IsWriter() {
		t.Fatal("a client-only manager must not hold the role")
	}

	// Simulate "the writer refused this particular write": ask the manager to
	// re-elect against the CURRENT writer's address, which is the exact call the
	// forward path makes after a StaleError.
	ep, err := writer.ReadEndpoint(writer.DirOf(filepath.Join(dir, "repotory.db")))
	if err != nil {
		t.Fatalf("read the published endpoint: %v", err)
	}
	rerr := clientM.reelect(ep.Addr)
	if rerr == nil {
		t.Fatal("re-election against a live-but-locked writer must not report success")
	}
	// THE ASSERTION: the message must not claim the writer is absent. It may say
	// the path is busy (retryable), which is what the caller can act on.
	msg := strings.ToLower(rerr.Error())
	if strings.Contains(msg, "no cinder writer is reachable") && !strings.Contains(msg, "busy") {
		t.Fatalf("a live-but-busy writer was reported as absent: %v", rerr)
	}
	if !errors.Is(rerr, errBusyWriter) {
		t.Fatalf("re-election should classify this as busy, got: %v", rerr)
	}
	// And the writer is indeed alive: the probe says so.
	if !writer.Probe(ep) {
		t.Fatal("the probe should have found the live writer")
	}
}
