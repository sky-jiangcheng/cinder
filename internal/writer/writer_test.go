package writer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestTryLockExclusiveAcrossHandles pins the election primitive: a second
// handle in ANY process (here: a child with its own fds, then an in-process
// second open) must get ErrLockedByAnotherProcess while the first holds it,
// and the lock must return after the holder releases. The whole single-writer
// contract (ADR-0058) stands on this being true.
func TestTryLockExclusiveAcrossHandles(t *testing.T) {
	dir := t.TempDir()

	first, err := TryLock(dir)
	if err != nil {
		t.Fatalf("first TryLock: %v", err)
	}
	if !first.Held() {
		t.Fatal("first lock reports !Held() while taken")
	}

	second, err := TryLock(dir)
	if !errors.Is(err, ErrLockedByAnotherProcess) {
		t.Fatalf("second TryLock = %v, want ErrLockedByAnotherProcess", err)
	}
	if second != nil {
		second.Close() //nolint:errcheck
		t.Fatal("second TryLock returned a handle while contended")
	}

	// The lock file names its holder, so "database is locked" can be answered
	// with "pid X holds the write path".
	raw, err := os.ReadFile(LockPath(dir))
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	if len(raw) == 0 || string(raw[:len("cinder-writer")]) != "cinder-writer" {
		t.Fatalf("lock file = %q, want it to start with cinder-writer", raw)
	}

	if err := first.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}
	third, err := TryLock(dir)
	if err != nil {
		t.Fatalf("TryLock after release: %v", err)
	}
	if err := third.Close(); err != nil {
		t.Fatalf("close third: %v", err)
	}
	// Close is idempotent: the hand-off path calls it defensively.
	if err := third.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

// TestLockSurvivesProcessDeath is the SIGKILL half of ADR-0058's recovery
// budget: the lock is owned by the file description, so a holder that dies —
// with no cleanup, unpublish, or unlock running — releases it at once. A real
// child process (a re-run of this test binary) is the only honest way to prove
// cross-process behaviour; a second handle in this process proves less.
func TestLockSurvivesProcessDeath(t *testing.T) {
	if os.Getenv("CS_TEST_HOLD_LOCK") != "" {
		// Child: take the lock — retrying, because the parent is polling and
		// may hold it for a moment — then SIGKILL ourselves. No Close, no
		// Unlock: this models the crash.
		statusPath := filepath.Join(os.Getenv("CS_TEST_HOLD_LOCK"), "child.status")
		_ = os.WriteFile(statusPath, []byte("started"), 0600)
		var lock *Lock
		var err error
		for i := 0; i < 500; i++ {
			lock, err = TryLock(os.Getenv("CS_TEST_HOLD_LOCK"))
			if err == nil {
				break
			}
			if !errors.Is(err, ErrLockedByAnotherProcess) {
				fmt.Fprintf(os.Stderr, "child: %v\n", err)
				os.Exit(1)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if lock == nil {
			_ = os.WriteFile(statusPath, []byte("never-won: "+err.Error()), 0600)
			fmt.Fprintln(os.Stderr, "child: never won the lock")
			os.Exit(1)
		}
		_ = os.WriteFile(statusPath, []byte("holding"), 0600)
		// Hold long enough for the parent to OBSERVE the contention before the
		// kill: without this the hold is microseconds and the poll cannot land
		// inside it.
		time.Sleep(1500 * time.Millisecond)
		p, _ := os.FindProcess(os.Getpid())
		_ = p.Kill()
		time.Sleep(time.Second)
		os.Exit(2) // unreachable: kill already landed
	}

	dir := t.TempDir()
	// go test runs the parent inside a temp package dir and passes its own
	// flags through os.Args; re-running this binary with -test.run is the
	// standard subprocess pattern. Stderr is piped so a child that fails to
	// take the lock says why in the parent's output instead of timing out.
	cmd := exec.Command(os.Args[0], "-test.run=TestLockSurvivesProcessDeath", "-test.v")
	cmd.Env = append(os.Environ(), "CS_TEST_HOLD_LOCK="+dir)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start holder: %v", err)
	}
	go func() {
		msg, _ := io.ReadAll(stderr)
		if len(msg) > 0 {
			t.Logf("holder child: %s", msg)
		}
	}()
	// Wait for the child to actually take the lock: contention proves the hold.
	// Stage 1: the child announces it holds the lock.
	deadline := time.Now().Add(10 * time.Second)
	announced := false
	for time.Now().Before(deadline) && !announced {
		if msg, err := os.ReadFile(filepath.Join(dir, "child.status")); err == nil && string(msg) == "holding" {
			announced = true
		} else {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !announced {
		_ = cmd.Process.Kill()
		cmd.Wait() //nolint:errcheck
		t.Fatal("child never announced the hold")
	}

	// Stage 2: contention is visible to this process — the election excludes a
	// second Cinder, which is the whole point of the lock.
	held := false
	for attempt := 0; attempt < 20 && !held; attempt++ {
		probe, err := TryLock(dir)
		switch {
		case errors.Is(err, ErrLockedByAnotherProcess):
			held = true
		default:
			// The child should have been holding it; hand the probe back and
			// try once more before declaring the election broken.
			_ = probe.Close()
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !held {
		_ = cmd.Process.Kill()
		cmd.Wait() //nolint:errcheck
		if msg, err := os.ReadFile(filepath.Join(dir, "child.status")); err == nil {
			t.Fatalf("child never held the lock (child status: %s)", msg)
		}
		t.Fatal("child never held the lock (child never started)")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill holder: %v", err)
	}
	cmd.Wait() //nolint:errcheck // exit status is the kill itself

	// The kernel released the lock with the process: a new writer is electable
	// immediately — this is ADR-0058's "SIGKILL, then writable again" budget.
	lock, err := TryLock(dir)
	if err != nil {
		t.Fatalf("re-election after SIGKILL: %v", err)
	}
	_ = lock.Close()
}

func TestServeAndPostRoundTrip(t *testing.T) {
	dir := t.TempDir()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req Request
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.Method == "echo" {
			writeJSONBody(w, &Response{OK: true, Result: req.Args})
			return
		}
		writeJSONBody(w, &Response{Error: &ResponseError{Code: "not_found", Message: "no such method"}})
	})
	srv, err := Serve(ListenSpec{Dir: dir, CoreVer: "test", Process: "unit", Handler: handler})
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	defer srv.Close() //nolint:errcheck

	ep, err := ReadEndpoint(dir)
	if err != nil {
		t.Fatalf("endpoint after Serve: %v", err)
	}
	if ep.Addr != srv.Addr() || ep.Token != srv.Token() {
		t.Fatalf("published endpoint %+v does not match server", ep)
	}

	resp, err := Post(context.Background(), ep, "/write", []byte(`{"method":"echo","args":{"n":1}}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if !resp.OK || string(resp.Result) != `{"n":1}` {
		t.Fatalf("round trip = %+v", resp)
	}

	// Wrong token: 404, not 401 — the port is reachable by every local process
	// and a 401 confirms "right door, wrong key".
	bad := ep
	bad.Token = "not-the-token"
	_, err = Post(context.Background(), bad, "/write", []byte(`{"method":"echo"}`))
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("bad-token post err = %v, want a 404", err)
	}

	// Serve's Close unpublishes: after the writer exits, the file is gone and
	// the next ReadEndpoint answers "no writer", which is what triggers
	// re-election upstream.
	srv.Close() //nolint:errcheck
	if _, err := ReadEndpoint(dir); !errors.Is(err, ErrNoEndpointPublished) {
		t.Fatalf("after Close = %v, want ErrNoEndpointPublished", err)
	}
}

func TestPostToDeadPortIsPreExecutionStale(t *testing.T) {
	dir := t.TempDir()
	// Publish by hand: an addr nobody listens on, like a writer that died
	// before its file could be removed.
	if _, err := Publish(dir, Endpoint{Addr: "127.0.0.1:1", Token: "x", PID: 99999, Process: "ghost"}); err != nil {
		t.Fatal(err)
	}
	ep, err := ReadEndpoint(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Post(context.Background(), ep, "/write", []byte(`{"method":"echo"}`))
	if !IsStale(err) {
		t.Fatalf("dead-port post = %v, want StaleError", err)
	}
	var stale *StaleError
	if !errors.As(err, &stale) || !stale.PreExecution {
		t.Fatalf("dead-port stale = %+v, want PreExecution (the write never landed, retry is safe)", stale)
	}
}

func writeJSONBody(w http.ResponseWriter, v any) {
	body, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func TestEndpointRoundTrip(t *testing.T) {
	dir := t.TempDir()

	if _, err := ReadEndpoint(dir); !errors.Is(err, ErrNoEndpointPublished) {
		t.Fatalf("ReadEndpoint on empty dir = %v, want ErrNoEndpointPublished", err)
	}

	ep := Endpoint{Addr: "127.0.0.1:1", Token: "t0ken", PID: 42, Process: "cinder-mcp"}
	if _, err := Publish(dir, ep); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got, err := ReadEndpoint(dir)
	if err != nil {
		t.Fatalf("ReadEndpoint: %v", err)
	}
	if got.Addr != ep.Addr || got.Token != ep.Token || got.PID != ep.PID {
		t.Fatalf("round trip = %+v, want %+v", got, ep)
	}

	// 0600: the token is the channel's only access control, so the group /
	// other bits must be empty regardless of umask. Windows does not enforce
	// POSIX mode bits, so the assertion only applies where they are meaningful
	// (the same guard backup_test.go and backup_copy_test.go use).
	fi, err := os.Stat(EndpointPath(dir))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if runtime.GOOS != "windows" {
		if perm := fi.Mode().Perm(); perm != 0600 {
			t.Fatalf("endpoint file mode = %o, want 600", perm)
		}
	}

	// Unpublish only removes THIS writer's publication.
	if err := Unpublish(dir, "127.0.0.1:9999"); err != nil {
		t.Fatalf("Unpublish other addr: %v", err)
	}
	if _, err := ReadEndpoint(dir); err != nil {
		t.Fatalf("endpoint vanished under a foreign Unpublish: %v", err)
	}
	if err := Unpublish(dir, ep.Addr); err != nil {
		t.Fatalf("Unpublish own addr: %v", err)
	}
	if _, err := ReadEndpoint(dir); !errors.Is(err, ErrNoEndpointPublished) {
		t.Fatalf("after Unpublish = %v, want ErrNoEndpointPublished", err)
	}
	// Unpublish on nothing is a no-op: shutdown runs it defensively.
	if err := Unpublish(dir, ep.Addr); err != nil {
		t.Fatalf("Unpublish missing file: %v", err)
	}
}

func TestReadEndpointRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, endpointName), []byte("{trunc"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEndpoint(dir); err == nil || errors.Is(err, ErrNoEndpointPublished) {
		t.Fatalf("garbage endpoint file = %v, want an unreadable-file error (not \"no writer\")", err)
	}
}

func TestDirOf(t *testing.T) {
	if got := DirOf(filepath.Join("/data", "repotory.db")); got != "/data" {
		t.Fatalf("DirOf = %q", got)
	}
}
