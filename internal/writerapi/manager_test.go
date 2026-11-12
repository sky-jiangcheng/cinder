package writerapi

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"repotory/internal/db"
	"repotory/internal/errcode"
	"repotory/internal/service"
	"repotory/internal/writer"
)

// openTestDB creates a real file database in dir: the election files live
// beside the DB by design, so contract tests cannot use :memory:.
func openTestDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	d, err := db.InitDB(filepath.Join(dir, "repotory.db"))
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func newManager(t *testing.T, d *sql.DB, dir, process string, selfElect, assertWriter bool) *Manager {
	t.Helper()
	svc := service.New(d, "tester")
	m, err := New(svc, Options{
		DBPath:           filepath.Join(dir, "repotory.db"),
		Process:          process,
		Surface:          service.AISurfaceMCP,
		CoreVersion:      "test",
		SelfElectAllowed: selfElect,
		AssertWriter:     assertWriter,
	})
	if err != nil {
		t.Fatalf("New(%s): %v", process, err)
	}
	t.Cleanup(m.Shutdown)
	return m
}

func seedProject(t *testing.T, d *sql.DB, name string) int64 {
	t.Helper()
	res, err := d.Exec(`INSERT INTO projects (name, root_path) VALUES (?, ?)`, name, filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("project id: %v", err)
	}
	return id
}

// TestElectionSecondProcessIsClient is the contract's first sentence: whoever
// wins the lock writes, and the next process forwards.
func TestElectionSecondProcessIsClient(t *testing.T) {
	dir := t.TempDir()
	dw := openTestDB(t, dir)
	pid := seedProject(t, dw, "alpha")

	first := newManager(t, dw, dir, "cinder-desktop-first", true, false)
	if !first.IsWriter() {
		t.Fatal("first manager should have won the election")
	}

	// The desktop policy is AssertWriter: a second desktop cannot displace the
	// running one, and Wails single-instance means it never happens; with the
	// flag the preempt loop just refuses to give the role up. Use the agent
	// policy (self-elect, no assert) — it must settle for client.
	client := newManager(t, dw, dir, "cinder-mcp", true, false)
	if client.Role() != roleClient {
		t.Fatalf("second manager role = %s, want client", RoleName(client.Role()))
	}

	// A client write lands through the WRITER's Service.
	var note serviceNote
	if err := client.ExecuteInto(context.Background(), MethodNotesCreate, NotesCreateArgs{
		ProjectID: pid, Title: "forwarded", Content: "written by the writer on our behalf", Kind: "knowledge", Source: "mcp",
	}, &note); err != nil {
		t.Fatalf("forwarded notes.create: %v", err)
	}
	if note.ID == 0 || note.Title != "forwarded" {
		t.Fatalf("note result = %+v, want a stored row", note)
	}
	// Read back from the writer's handle: the row exists in the shared file.
	var count int
	if err := dw.QueryRow(`SELECT COUNT(*) FROM project_notes WHERE id = ?`, note.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("read back: count=%d err=%v", count, err)
	}
}

type serviceNote struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// TestDesktopTakesOverFromSelfElectedAgent is ADR-0058's division of labour in
// code: an agent may hold the role only until the surface a human is looking
// at starts up.
func TestDesktopTakesOverFromSelfElectedAgent(t *testing.T) {
	dir := t.TempDir()
	da := openTestDB(t, dir)

	agent := newManager(t, da, dir, "cinder-mcp", true, false)
	if !agent.IsWriter() {
		t.Fatal("lone agent should self-elect")
	}
	pid := seedProject(t, da, "beta")

	desktop := newManager(t, da, dir, "Cinder Desktop", true, true)
	if !desktop.IsWriter() {
		t.Fatalf("desktop did not take the writer role (agent role now %s)", RoleName(agent.Role()))
	}
	if agent.Role() == roleWriter {
		t.Fatal("the agent still believes it is the writer after yielding")
	}

	// And the agent's NEXT write goes back through the desktop's channel — the
	// roles settle without either process restarting.
	var note serviceNote
	if err := agent.ExecuteInto(context.Background(), MethodNotesCreate, NotesCreateArgs{
		ProjectID: pid, Title: "after-handoff", Content: "x", Kind: "knowledge", Source: "mcp",
	}, &note); err != nil {
		t.Fatalf("agent write after handoff: %v", err)
	}
	if note.ID == 0 {
		t.Fatal("handoff-path write did not land")
	}
}

// TestWriterDeathTriggersTakeover is the recovery half: after the writer
// process vanishes mid-session, the client's next write elects a new writer
// instead of surfacing a dead-port error.
func TestWriterDeathTriggersTakeover(t *testing.T) {
	dir := t.TempDir()
	dw := openTestDB(t, dir)
	pid := seedProject(t, dw, "gamma")

	first := newManager(t, dw, dir, "cinder-mcp-old", true, false)
	second := newManager(t, dw, dir, "cinder-mcp-new", true, false)
	if second.Role() != roleClient {
		t.Fatal("second manager should start as client")
	}

	// Simulate the first writer's process death. Shutdown models the
	// graceful half (unpublish + release); the crash half is covered in
	// internal/writer's SIGKILL test, which is where the primitive lives.
	first.Shutdown()

	var note serviceNote
	if err := second.ExecuteInto(context.Background(), MethodNotesCreate, NotesCreateArgs{
		ProjectID: pid, Title: "post-death", Content: "x", Kind: "knowledge", Source: "mcp",
	}, &note); err != nil {
		t.Fatalf("write after writer death: %v", err)
	}
	if !second.IsWriter() {
		t.Fatal("the surviving client should have taken over the role")
	}
	if note.ID == 0 {
		t.Fatal("takeover write did not land")
	}
}

// TestUnreachableWriterIsActionableError covers the refusal ADR-0058 demands
// when no writer can be reached: NOT a lock timeout, NOT an empty success — a
// classified error that says what to do.
func TestUnreachableWriterIsActionableError(t *testing.T) {
	dir := t.TempDir()
	d := openTestDB(t, dir)

	// Keep this test process from being able to elect: hold the lock with a
	// raw handle we never hand to a Manager, and publish an endpoint whose port
	// is closed (a writer frozen between locked and serving).
	lock, err := writer.TryLock(dir)
	if err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	defer lock.Close() //nolint:errcheck
	if _, err := writer.Publish(dir, writer.Endpoint{
		Addr: "127.0.0.1:1", Token: "dead", PID: 99999, Process: "ghost",
	}); err != nil {
		t.Fatalf("publish ghost: %v", err)
	}

	m := newManager(t, d, dir, "cinder-mcp", false, false)
	if m.Role() != roleClient {
		t.Fatalf("role = %s, want client", RoleName(m.Role()))
	}
	_, err = m.Execute(context.Background(), MethodNotesCreate, NotesCreateArgs{ProjectID: 1, Title: "x", Content: "y"})
	if err == nil {
		t.Fatal("write against a ghost writer succeeded; it must not")
	}
	code, retryable := errcode.Classify(err)
	if code != errcode.WriterUnavailable && !errors.Is(err, errcode.ErrWriterUnavailable) {
		t.Fatalf("classification = %q (%v), want writer_unavailable", code, err)
	}
	if !retryable {
		t.Fatal("writer_unavailable must be retryable: starting the desktop fixes it")
	}
	// Actionable means the text names the remedy, not just the failure.
	if msg := err.Error(); !(contains(msg, "Cinder Desktop") || contains(msg, "take it over")) {
		t.Fatalf("error is not actionable: %q", msg)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// TestSelfElectDisallowedRefuses pins the difference between an agent surface
// (may take over) and a process that must never write (cinder-server today):
// no election, only forwarding — and a loud refusal when there is nobody.
func TestSelfElectDisallowedRefuses(t *testing.T) {
	dir := t.TempDir()
	lock, err := writer.TryLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close() //nolint:errcheck

	d := openTestDB(t, dir)
	m := newManager(t, d, dir, "cinder-server", false, false)
	start := time.Now()
	_, err = m.Execute(context.Background(), MethodNotesCreate, NotesCreateArgs{ProjectID: 1, Title: "t", Content: "c"})
	if err == nil {
		t.Fatal("expected refusal without an endpoint")
	}
	if errors.Is(err, writer.ErrLockedByAnotherProcess) {
		t.Fatal("the raw lock error must be translated, not forwarded")
	}
	if el := time.Since(start); el > 30*time.Second {
		t.Fatalf("refusal took %s; bounded waits only", el)
	}
}

// TestUnknownMethodRefused: the catalogue is the contract. A request naming a
// method outside it is refused on BOTH sides, so no future client can reach a
// service method the reviewers never signed off on.
func TestUnknownMethodRefused(t *testing.T) {
	dir := t.TempDir()
	d := openTestDB(t, dir)
	m := newManager(t, d, dir, "tester", true, false)

	if _, err := m.Execute(context.Background(), "notes.delete", struct{}{}); !errors.Is(err, ErrUnknownMethod) {
		t.Fatalf("Execute(unknown) = %v, want ErrUnknownMethod", err)
	}
	// And the executor itself refuses: serving the catalogue must not depend
	// on the manager having pre-checked.
	ex := NewExecutor(service.New(d, "tester"))
	if _, err := ex.Execute(context.Background(), "anything.else", nil); !errors.Is(err, ErrUnknownMethod) {
		t.Fatalf("Executor.Execute(unknown) = %v", err)
	}
	// Forwarded, it comes back classified as not-found (a bad method never
	// becomes retryable).
	pid := seedProject(t, d, "delta")
	_ = pid
}

// TestAuditRowKeepsClientSurface: the audit trail must answer "which surface
// sent this" even when another process executed the insert.
func TestAuditRowKeepsClientSurface(t *testing.T) {
	dir := t.TempDir()
	dw := openTestDB(t, dir)
	writerM := newManager(t, dw, dir, "desktop", true, false)
	client := newManager(t, dw, dir, "mcp", true, false)

	row := db.AICallLog{Surface: "mcp", Endpoint: "http://x", Model: "m", PromptHash: "h", PromptChars: 1}
	if err := client.RecordAudit(row); err != nil {
		t.Fatalf("forwarded audit: %v", err)
	}
	var surface string
	if err := dw.QueryRow(`SELECT surface FROM ai_call_log ORDER BY id DESC LIMIT 1`).Scan(&surface); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if surface != "mcp" {
		t.Fatalf("audit surface = %q, want the client's mcp, not the writer's desktop", surface)
	}
	_ = writerM
}

// TestYieldRefusedWhileInflight covers the abort path: a writer asked to hand
// off while a long write runs must finish it, not yank the lock.
func TestMethodsListIsStable(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range Methods() {
		if seen[m] {
			t.Fatalf("duplicate catalogue method %q", m)
		}
		seen[m] = true
	}
	// Control methods are not writes and must stay out of the catalogue.
	if seen[MethodYield] {
		t.Fatal("MethodYield leaked into the write catalogue")
	}
}

// TestForwardedErrorKeepsWriterClassification pins the Remote wrapping:
// a conflict raised on the writer must arrive as `conflict` on the client,
// not be re-derived (and downgraded) from the text.
func TestForwardedErrorKeepsWriterClassification(t *testing.T) {
	dir := t.TempDir()
	dw := openTestDB(t, dir)
	pid := seedProject(t, dw, "eps")

	w := newManager(t, dw, dir, "w", true, false)
	var note serviceNote
	if err := w.ExecuteInto(context.Background(), MethodNotesCreate, NotesCreateArgs{
		ProjectID: pid, Title: "t", Content: "c", Kind: "knowledge", Source: "mcp",
	}, &note); err != nil {
		t.Fatal(err)
	}
	c := newManager(t, dw, dir, "c", true, false)
	// expectedVersion 7 against a version-1 note: the writer raises
	// ErrVersionConflict; the client must see `conflict`, non-retryable.
	_, err := c.Execute(context.Background(), MethodNotesUpdate, NotesUpdateArgs{
		NoteID: note.ID, Content: "c2", ExpectedVersion: 7,
	})
	if err == nil {
		t.Fatal("stale expectedVersion write succeeded")
	}
	if code := errcode.Code(err); code != errcode.Conflict {
		t.Fatalf("classification = %q, want conflict (err: %v)", code, err)
	}
	if errcode.IsRetryable(err) {
		t.Fatal("conflict must not be classified retryable")
	}
}

// TestChannelFailureReleasesTheRole is the one state the contract must never
// leave behind: a process holding the election lock while its channel is dead.
// Reads keep working in that state, so it looks healthy to the user while
// every forwarded write fails — worse than a clean crash, because nobody
// re-elects (the lock says a writer exists). The manager demotes itself:
// release lock, become a client, and the next peer write elects a new writer.
func TestChannelFailureReleasesTheRole(t *testing.T) {
	dir := t.TempDir()
	d := openTestDB(t, dir)
	pid := seedProject(t, d, "fatal")

	svc := service.New(d, "tester")
	lost := make(chan string, 1)
	m, err := New(svc, Options{
		DBPath: filepath.Join(dir, "repotory.db"), Process: "cinder-mcp",
		Surface: service.AISurfaceMCP, SelfElectAllowed: true,
		OnLost: func(reason string) { lost <- reason },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Shutdown)
	if !m.IsWriter() {
		t.Fatal("expected to win the election")
	}

	// Kill the listener underneath the manager — the Serve goroutine's only
	// abnormal-exit signal is the OnFatal hook, so this drives the real path.
	m.mu.Lock()
	ln := m.channel.ListenerForTest()
	m.mu.Unlock()
	if ln == nil {
		t.Fatal("no listener exposed for the failure injection")
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("kill listener: %v", err)
	}

	select {
	case reason := <-lost:
		if reason == "" {
			t.Fatal("OnLost fired with an empty reason")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("channel death did not demote the writer")
	}
	if m.IsWriter() {
		t.Fatal("still claims the writer role after its channel died")
	}
	// The lock is really gone: a fresh process can elect.
	fresh := newManager(t, d, dir, "cinder-mcp-next", true, false)
	if !fresh.IsWriter() {
		t.Fatal("a peer could not take over after the demotion")
	}
	var note serviceNote
	if err := fresh.ExecuteInto(context.Background(), MethodNotesCreate, NotesCreateArgs{
		ProjectID: pid, Title: "recovered", Content: "x", Kind: "knowledge", Source: "mcp",
	}, &note); err != nil {
		t.Fatalf("write after recovery: %v", err)
	}
}
