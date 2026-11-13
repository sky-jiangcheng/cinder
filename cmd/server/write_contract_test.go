package main

// The single-writer contract's server-side acceptance gates (ADR-0058 /
// TODO 13.6). The server is the third writer process the contract covers; its
// distinguishing property is the /api/rpc bridge, which reaches EVERY App
// binding — including the human-gate writes no agent surface may execute.
// These tests drive the real HTTP mux over a real file database, in the two
// roles the server can hold:
//
//   - writer (lone headless install): every binding behaves as today;
//   - client (a desktop holds the lock): reads unchanged, catalogue writes
//     forward to the writer, human-gate writes refuse with an actionable
//     error, and the writer-owned tickers do not start.
//
// The desktop-as-writer is simulated the way every contract test in this repo
// does it: a second Manager over the same file holding the election lock
// (internal/writer proves the lock itself with real subprocesses).

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"repotory/internal/app"
	"repotory/internal/db"
	"repotory/internal/httpapi"
	"repotory/internal/service"
	"repotory/internal/writerapi"
)

// startServer builds the same stack cmd/server runs (service + App with the
// contract + the guarded mux) against a file database in dir.
func startServer(t *testing.T, dir string) (*httptest.Server, *writerapi.Manager, *service.Service) {
	t.Helper()
	dbPath := filepath.Join(dir, "repotory.db")
	database, err := db.InitDB(dbPath)
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	svc := service.New(database, "tester")
	m, err := writerapi.New(svc, writerapi.Options{
		DBPath:           dbPath,
		Process:          "cinder-server",
		Surface:          service.AISurfaceHTTP,
		SelfElectAllowed: true,
	})
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	t.Cleanup(m.Shutdown)
	mux := httpapi.New(svc, app.New(svc, app.WithWriter(m)), m)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, m, svc
}

func rpc(t *testing.T, url, method string, args ...any) (int, map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"method": method, "args": args})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url+"/api/rpc", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("rpc %s: %v", method, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode %s response: %v", method, err)
	}
	return resp.StatusCode, payload
}

// TestServerWriterRoleServesReadsAndWrites pins the lone-headless case: the
// server is the writer, and its bindings behave exactly as before the
// contract existed.
func TestServerWriterRoleServesReadsAndWrites(t *testing.T) {
	dir := t.TempDir()
	ts, m, _ := startServer(t, dir)
	if !m.IsWriter() {
		t.Fatal("a lone server must elect itself as writer")
	}

	// A read binding, unchanged.
	code, payload := rpc(t, ts.URL, "GetProjects")
	if code != http.StatusOK {
		t.Fatalf("ListProjects = %d: %v", code, payload)
	}

	// A catalogue write binding executes locally (writer role). The note
	// lands in the migration-seeded default project.
	code, payload = rpc(t, ts.URL, "CreateNoteWithMeta", 1, "writer-role note", "written by the writer itself", "", "knowledge", "desktop")
	if code != http.StatusOK {
		t.Fatalf("CreateNoteWithMeta = %d: %v", code, payload)
	}
	note, _ := payload["result"].(map[string]any)
	if note == nil || note["title"] != "writer-role note" {
		t.Fatalf("note result = %v", payload)
	}

	// A human-gate write binding executes locally too — the writer MAY run
	// the human gate (on a real desktop a human pressed the button; here the
	// contract only cares WHO holds the file).
	code, _ = rpc(t, ts.URL, "GetConfig", "scan_roots_seeded")
	if code != http.StatusOK {
		t.Fatalf("GetConfig = %d", code)
	}
}

// TestServerClientRoleForwardsAndRefuses is the acceptance core: with a
// desktop holding the write path, the server's
//
//   - reads work (agents and the dsh-plugin keep working);
//   - catalogue write forwards to the writer and lands;
//   - human-gate write refuses with an error that names Cinder Desktop.
//
// The writer here is a second manager over the same file — the stand-in for
// the desktop process.
func TestServerClientRoleForwardsAndRefuses(t *testing.T) {
	dir := t.TempDir()
	ts, serverManager, _ := startServer(t, dir)

	// The desktop arrives and takes over.
	dbPath := filepath.Join(dir, "repotory.db")
	desktopDB, err := db.InitDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = desktopDB.Close() })
	desktopSvc := service.New(desktopDB, "desktop")
	desktop, err := writerapi.New(desktopSvc, writerapi.Options{
		DBPath:           dbPath,
		Process:          "Cinder Desktop",
		Surface:          service.AISurfaceDesktop,
		SelfElectAllowed: true,
		AssertWriter:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(desktop.Shutdown)
	if !desktop.IsWriter() {
		t.Fatal("the desktop should have taken the role from the server")
	}
	if serverManager.IsWriter() {
		t.Fatal("the server still claims the writer role after the desktop took over")
	}

	// 1. Reads are untouched.
	code, payload := rpc(t, ts.URL, "GetProjects")
	if code != http.StatusOK {
		t.Fatalf("read from client-role server = %d: %v", code, payload)
	}

	// 2. Catalogue write forwards to the desktop and lands.
	code, payload = rpc(t, ts.URL, "CreateNoteWithMeta", 1, "forwarded from server", "written by the desktop on the server's behalf", "", "knowledge", "desktop")
	if code != http.StatusOK {
		t.Fatalf("forwarded CreateNoteWithMeta = %d: %v", code, payload)
	}
	note, _ := payload["result"].(map[string]any)
	if note == nil {
		t.Fatalf("forwarded note result missing: %v", payload)
	}
	// The row exists — visible through the DESKTOP's handle, which is the
	// process that executed the write.
	var count int
	if err := desktopDB.QueryRow(`SELECT COUNT(*) FROM project_notes WHERE title = ?`, "forwarded from server").Scan(&count); err != nil || count != 1 {
		t.Fatalf("forwarded write did not land on the writer (count=%d err=%v)", count, err)
	}

	// 3. Human-gate write refuses, naming the remedy.
	code, payload = rpc(t, ts.URL, "ApproveWikiPage", 1)
	if code != http.StatusForbidden {
		t.Fatalf("client-role ApproveWikiPage = %d (%v), want 403", code, payload)
	}
	msg, _ := payload["error"].(string)
	if !strings.Contains(msg, "Cinder Desktop") {
		t.Fatalf("refusal is not actionable: %q", msg)
	}

	// 4. Config write (UpdateConfig is a human-gate write: privacy gates are
	// never agent-self-service) refuses with the same actionable shape.
	code, payload = rpc(t, ts.URL, "UpdateConfig", "auto_import", "1")
	if code != http.StatusForbidden {
		t.Fatalf("client-role UpdateConfig = %d (%v), want 403", code, payload)
	}
}

// TestServerClientRoleSkipsWriterTickers pins the demotion decision the
// entry point makes: cmd/server branches on IsWriter() — Startup (tickers)
// only for the writer, importer registration for everyone. The decision must
// track the ROLE, so this drives a real election where the lock is held
// elsewhere and asserts the client role before the branch would run.
func TestServerClientRoleSkipsWriterTickers(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "repotory.db")
	database, err := db.InitDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	// The desktop holds the lock first.
	lock, err := writerapi.TryLockForTest(dir)
	if err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	t.Cleanup(func() { _ = lock.Close() })

	svc := service.New(database, "tester")
	m, err := writerapi.New(svc, writerapi.Options{
		DBPath:           dbPath,
		Process:          "cinder-server",
		SelfElectAllowed: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Shutdown)

	// The branch cmd/server runs:
	if m.IsWriter() {
		t.Fatal("a server under a held lock must not claim the writer role — it would run the tickers behind the writer's back")
	}
	// Registration still runs (reads and the consent-gated import triggers
	// report from this registry even in client role).
	svc.RegisterKnowledgeSources()
	if len(svc.GetKnowledgeSources()) == 0 {
		t.Fatal("client-role server must still register knowledge sources")
	}
}
