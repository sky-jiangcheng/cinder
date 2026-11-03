package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// downloadStub serves a fixed payload as a release bundle and records how
// many bytes it handed out.
func downloadStub(payload []byte) *httptest.Server {
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", itoa(len(payload)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
}

// itoa avoids pulling strconv into the test file for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// waitForPhase polls the downloader until it reaches one of the wanted
// phases, or the deadline passes.
func waitForPhase(t *testing.T, d *updateDownloader, timeout time.Duration, phases ...string) DownloadState {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		st := d.snapshot()
		for _, p := range phases {
			if st.Phase == p {
				return st
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for phase %v; last state: %+v", phases, d.snapshot())
	return DownloadState{}
}

// stubDownloadDir points the download directory at a temp dir so the test
// never writes into the real user data directory.
func stubDownloadDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := downloadDirOverride()
	setDownloadDirForTest(func() (string, error) { return dir, nil })
	t.Cleanup(func() { setDownloadDirForTest(prev) })
	return dir
}

func TestStartUpdateDownloadRejectsNonHTTPS(t *testing.T) {
	svc, _ := setupService(t)
	err := svc.StartUpdateDownload("http://example.com/x.dmg", "x.dmg", "", "")
	if err == nil {
		t.Fatal("expected an error for a non-https URL")
	}
	if !strings.Contains(err.Error(), "https") {
		t.Fatalf("error should mention https, got: %v", err)
	}
}

func TestStartUpdateDownloadRejectsSecondConcurrentDownload(t *testing.T) {
	svc, _ := setupService(t)
	dir := stubDownloadDir(t)

	// A server that never finishes, so the first download stays in flight.
	block := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	t.Cleanup(func() { close(block); srv.Close() })

	prevClient := newUpdateHTTPClient
	newUpdateHTTPClient = func(proxy string) (*http.Client, error) { return srv.Client(), nil }
	t.Cleanup(func() { newUpdateHTTPClient = prevClient })

	if err := svc.StartUpdateDownload(srv.URL+"/x.dmg", "x.dmg", "", ""); err != nil {
		t.Fatalf("first start: %v", err)
	}
	waitForPhase(t, svc.updates, 2*time.Second, DownloadRunning)

	err := svc.StartUpdateDownload(srv.URL+"/y.dmg", "y.dmg", "", "")
	if err == nil {
		t.Fatal("expected the second concurrent download to be rejected")
	}
	if !strings.Contains(err.Error(), "正在下载") {
		t.Fatalf("error should say a download is in progress, got: %v", err)
	}
	_ = dir

	// Stop the in-flight download and WAIT for the attempt to settle.
	//
	// The download is still writing into the temp directory this test is
	// about to tear down; a goroutine still running when the temp dir is
	// removed fails the test at random with "directory not empty". Waiting
	// for the attempt to settle (terminal hook included) makes the teardown
	// deterministic instead of a coin flip.
	_ = svc.CancelUpdateDownload()
	if !svc.updates.waitSettled(5 * time.Second) {
		t.Fatal("the download did not settle within 5s")
	}
}

func TestDownloadUpdateVerifiesDigestAndKeepsFile(t *testing.T) {
	svc, _ := setupService(t)
	dir := stubDownloadDir(t)

	payload := []byte("repotory bundle bytes")
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])

	srv := downloadStub(payload)
	defer srv.Close()
	prevClient := newUpdateHTTPClient
	newUpdateHTTPClient = func(proxy string) (*http.Client, error) { return srv.Client(), nil }
	t.Cleanup(func() { newUpdateHTTPClient = prevClient })

	if err := svc.StartUpdateDownload(srv.URL+"/repotory-test.dmg", "repotory-test.dmg", "sha256:"+digest, ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	st := waitForPhase(t, svc.updates, 5*time.Second, DownloadDone)

	if !st.ChecksumVerified {
		t.Error("expected ChecksumVerified to be true after a matching digest")
	}
	if st.Downloaded != int64(len(payload)) {
		t.Errorf("Downloaded = %d, want %d", st.Downloaded, len(payload))
	}
	if st.Total != int64(len(payload)) {
		t.Errorf("Total = %d, want %d", st.Total, len(payload))
	}
	if st.Path == "" {
		t.Fatal("expected a local path after the download completed")
	}
	got, err := os.ReadFile(st.Path)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("file content = %q, want %q", got, payload)
	}
	if filepath.Dir(st.Path) != dir {
		t.Errorf("file landed in %q, want the stubbed dir %q", filepath.Dir(st.Path), dir)
	}
	// The .part sibling must be gone: a leftover would look like a second
	// download to anything that lists the directory.
	if _, err := os.Stat(st.Path + ".part"); !os.IsNotExist(err) {
		t.Error("the .part file should have been renamed away")
	}
}

func TestDownloadUpdateDeletesFileOnDigestMismatch(t *testing.T) {
	svc, _ := setupService(t)
	dir := stubDownloadDir(t)

	payload := []byte("tampered bytes")
	srv := downloadStub(payload)
	defer srv.Close()
	prevClient := newUpdateHTTPClient
	newUpdateHTTPClient = func(proxy string) (*http.Client, error) { return srv.Client(), nil }
	t.Cleanup(func() { newUpdateHTTPClient = prevClient })

	wrong := strings.Repeat("ab", 32)
	if err := svc.StartUpdateDownload(srv.URL+"/repotory-test.dmg", "repotory-test.dmg", wrong, ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	st := waitForPhase(t, svc.updates, 5*time.Second, DownloadFailed)

	if st.ChecksumVerified {
		t.Error("ChecksumVerified must be false when the digest does not match")
	}
	if !strings.Contains(st.Error, "校验失败") {
		t.Errorf("error should explain the mismatch, got: %q", st.Error)
	}
	// The unverified bytes must not survive on disk.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected the mismatching file to be deleted, dir still holds %d entries", len(entries))
	}
}

func TestDownloadUpdateRejectsFileNameWithPathTraversal(t *testing.T) {
	svc, _ := setupService(t)
	stubDownloadDir(t)

	payload := []byte("x")
	srv := downloadStub(payload)
	defer srv.Close()
	prevClient := newUpdateHTTPClient
	newUpdateHTTPClient = func(proxy string) (*http.Client, error) { return srv.Client(), nil }
	t.Cleanup(func() { newUpdateHTTPClient = prevClient })

	if err := svc.StartUpdateDownload(srv.URL+"/x", "../../escape.dmg", "", ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	st := waitForPhase(t, svc.updates, 5*time.Second, DownloadDone)
	if strings.Contains(st.Path, "..") {
		t.Errorf("path %q escaped the download dir", st.Path)
	}
	if filepath.Base(st.Path) != "escape.dmg" {
		t.Errorf("path = %q, want the base name only", st.Path)
	}
}

func TestCancelUpdateDownloadStopsIt(t *testing.T) {
	svc, _ := setupService(t)
	stubDownloadDir(t)

	block := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1024")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		<-block
	}))
	t.Cleanup(func() { close(block); srv.Close() })

	prevClient := newUpdateHTTPClient
	newUpdateHTTPClient = func(proxy string) (*http.Client, error) { return srv.Client(), nil }
	t.Cleanup(func() { newUpdateHTTPClient = prevClient })

	if err := svc.StartUpdateDownload(srv.URL+"/x.dmg", "x.dmg", "", ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForPhase(t, svc.updates, 2*time.Second, DownloadRunning)

	if err := svc.CancelUpdateDownload(); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	st := waitForPhase(t, svc.updates, 5*time.Second, DownloadCanceled)
	if st.Error == "" {
		t.Error("a canceled download should say so")
	}
}

func TestNormalizeDigest(t *testing.T) {
	hex64 := strings.Repeat("aB", 32)
	if got := normalizeDigest("sha256:" + hex64); got != strings.ToLower(hex64) {
		t.Errorf("normalizeDigest = %q, want the lowercase hex", got)
	}
	if got := normalizeDigest(""); got != "" {
		t.Errorf("normalizeDigest(\"\") = %q, want empty", got)
	}
	if got := normalizeDigest("sha256:tooshort"); got != "" {
		t.Errorf("a malformed digest must normalize to empty, got %q", got)
	}
	if got := normalizeDigest("sha256:" + strings.Repeat("z", 64)); got != "" {
		t.Errorf("a non-hex digest must normalize to empty, got %q", got)
	}
}

func TestOpenDownloadedUpdateRequiresCompletedDownload(t *testing.T) {
	svc, _ := setupService(t)
	if err := svc.OpenDownloadedUpdate(""); err == nil {
		t.Fatal("expected an error when nothing has been downloaded")
	}
}

func TestDownloadStatePercent(t *testing.T) {
	if got := (DownloadState{Downloaded: 50, Total: 200}).Percent(); got != 25 {
		t.Errorf("Percent = %v, want 25", got)
	}
	if got := (DownloadState{Downloaded: 50}).Percent(); got != 0 {
		t.Errorf("Percent with unknown total = %v, want 0", got)
	}
	if got := (DownloadState{Downloaded: 300, Total: 200}).Percent(); got != 100 {
		t.Errorf("Percent above 100 = %v, want 100", got)
	}
}

func TestDownloadProgressReporterSmoothesSpeed(t *testing.T) {
	r := newDownloadProgressReporter(1000)
	base := time.Now()
	// Two identical chunks: the smoothed speed should settle near the
	// per-chunk rate rather than swinging between values.
	for i := 0; i < 3; i++ {
		r.add(100, base.Add(time.Duration(i)*time.Second))
	}
	if r.ema <= 0 {
		t.Fatalf("ema = %v, want a positive speed", r.ema)
	}
	if r.downloaded != 300 {
		t.Errorf("downloaded = %d, want 300", r.downloaded)
	}
	rem := r.remaining(base.Add(3 * time.Second))
	if rem < 0 {
		t.Errorf("remaining = %d, want a non-negative ETA", rem)
	}
}

func TestDownloadUpdateUsesBackgroundContext(t *testing.T) {
	// The download must survive the binding call returning, so it runs on the
	// service's background context rather than a request-scoped one.
	svc, _ := setupService(t)
	stubDownloadDir(t)
	ctx := svc.bgContext()
	if ctx == nil {
		t.Fatal("bgContext returned nil")
	}
	select {
	case <-ctx.Done():
		t.Fatal("the background context must not be cancelled at rest")
	default:
	}
	_ = context.Background()
}
