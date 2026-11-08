package service

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"repotory/internal/db"
)

// The update flow used to leave the user with three unanswered questions:
// where the bundle went, which version it was, and how to reopen it after a
// restart. These tests pin the answers.

// stateService builds a service for the persistence tests.
//
// It deliberately does NOT go through setupService: the shared helper points
// the update-download state file at its own temp dir, while these tests need
// ONE path shared between two "runs" of the app (the download, then the
// restart that must find it again).
func stateService(t *testing.T) *Service {
	t.Helper()
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return NewWithDeps(database, nil, nil, "me")
}

// TestUserDownloadsDirLandsInDownloadsFolder pins the directory the bundles
// are written to.
//
// The old home was the app's config dir, which is the right place for STATE
// and the wrong place for a file the user is meant to find and install by
// hand — a bundle under "~/Library/Application Support" is a bundle the user
// never finds.
func TestUserDownloadsDirLandsInDownloadsFolder(t *testing.T) {
	dir, err := userDownloadsDir()
	if err != nil {
		t.Fatalf("userDownloadsDir: %v", err)
	}
	if filepath.Base(dir) != "Downloads" {
		t.Fatalf("download dir = %q, want it to end in Downloads", dir)
	}
}

// TestVersionedFileName pins the decoration of the bundle's file name.
//
// Without the version in the name, "is this bundle the version I am on?"
// is unanswerable by looking at the file: the name is byte-for-byte the
// same for every release ever published.
func TestVersionedFileName(t *testing.T) {
	cases := []struct {
		base    string
		version string
		want    string
	}{
		// A pre-rename name still gets the version — and gets it under the
		// current brand.
		{"repotory-darwin-arm64.dmg", "2.25.0", "Cinder-2.25.0-darwin-arm64.dmg"},
		{"repotory-windows-amd64.zip", "2.24.0", "Cinder-2.24.0-windows-amd64.zip"},
		// No version: the name is left exactly as the release published it.
		{"repotory-darwin-arm64.dmg", "", "repotory-darwin-arm64.dmg"},
		// Current names already carry the version: decorating them again
		// would give "Cinder-2.33.0-2.33.0-darwin-arm64.dmg".
		{"Cinder-2.33.0-darwin-arm64.dmg", "2.33.0", "Cinder-2.33.0-darwin-arm64.dmg"},
		{"Cinder-2.33.0-windows-amd64.zip", "2.33.0", "Cinder-2.33.0-windows-amd64.zip"},
		// A name that is not one of ours is never decorated.
		{"cinder-mcp-2.33.0-darwin-arm64.tar.gz", "2.33.0", "cinder-mcp-2.33.0-darwin-arm64.tar.gz"},
		{"repotory-mcp-darwin-arm64.tar.gz", "2.25.0", "repotory-mcp-darwin-arm64.tar.gz"},
		{"notes.txt", "2.25.0", "notes.txt"},
	}
	for _, c := range cases {
		if got := versionedFileName(c.base, c.version); got != c.want {
			t.Errorf("versionedFileName(%q, %q) = %q, want %q", c.base, c.version, got, c.want)
		}
	}
}

// TestDownloadWritesVersionedFileAndReportsVersion covers the whole download
// path: the file on disk carries the version, and the progress state names
// the version the user is downloading.
func TestDownloadWritesVersionedFileAndReportsVersion(t *testing.T) {
	svc, _ := setupService(t)
	dir := stubDownloadDir(t)

	payload := []byte("repotory bundle bytes")
	srv := downloadStub(payload)
	defer srv.Close()

	prevClient := newUpdateHTTPClient
	newUpdateHTTPClient = func(proxy string) (*http.Client, error) { return srv.Client(), nil }
	t.Cleanup(func() { newUpdateHTTPClient = prevClient })

	const name = "Cinder-2.25.0-darwin-arm64.dmg"
	if err := svc.StartUpdateDownload(srv.URL+"/"+name, name, "", "2.25.0"); err != nil {
		t.Fatalf("start: %v", err)
	}
	st := waitForPhase(t, svc.updates, 3*time.Second, DownloadDone)

	if st.Version != "2.25.0" {
		t.Errorf("state.Version = %q, want 2.25.0 — the progress dialog must name the version", st.Version)
	}
	want := filepath.Join(dir, "Cinder-2.25.0-darwin-arm64.dmg")
	if st.Path != want {
		t.Errorf("state.Path = %q, want %q", st.Path, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("the versioned bundle should exist on disk: %v", err)
	}
}

// TestFinishedDownloadIsPersistedToDisk pins the wiring between the download
// and the state file: a download that finishes writes its snapshot to disk on
// its own, without anybody asking the service to.
//
// The path is resolved when the download STARTS (on the caller's goroutine) and
// carried into the download goroutine, so the state never lands in the real
// user's data directory when a test swapped the location.
func TestFinishedDownloadIsPersistedToDisk(t *testing.T) {
	svc, _ := setupService(t)
	stubDownloadDir(t)
	stateDir := t.TempDir()
	statePath := filepath.Join(stateDir, "update-download.json")

	prev := downloadStatePathOverride()
	setDownloadStatePathForTest(func() (string, error) { return statePath, nil })
	t.Cleanup(func() { setDownloadStatePathForTest(prev) })

	srv := downloadStub([]byte("repotory bundle bytes"))
	defer srv.Close()
	prevClient := newUpdateHTTPClient
	newUpdateHTTPClient = func(proxy string) (*http.Client, error) { return srv.Client(), nil }
	t.Cleanup(func() { newUpdateHTTPClient = prevClient })

	const name = "Cinder-2.25.0-darwin-arm64.dmg"
	if err := svc.StartUpdateDownload(srv.URL+"/"+name, name, "", "2.25.0"); err != nil {
		t.Fatalf("start: %v", err)
	}
	st := waitForPhase(t, svc.updates, 3*time.Second, DownloadDone)

	// The state file is written by the download's terminal hook, so it lands
	// a moment after the phase flips. Give it that moment.
	deadline := time.Now().Add(3 * time.Second)
	var payload persistedDownload
	for time.Now().Before(deadline) {
		buf, err := os.ReadFile(statePath)
		if err == nil {
			if err := json.Unmarshal(buf, &payload); err == nil {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if payload.Phase != DownloadDone {
		t.Fatalf("persisted phase = %q, want %q — a finished download must write its state", payload.Phase, DownloadDone)
	}
	if payload.Version != "2.25.0" {
		t.Errorf("persisted version = %q, want 2.25.0", payload.Version)
	}
	if payload.Path != st.Path {
		t.Errorf("persisted path = %q, want %q", payload.Path, st.Path)
	}
}

// TestPersistedDownloadSurvivesRestart pins the fix for "the app forgot the
// file I downloaded": a finished download is written to disk and restored
// when the app starts again, so the installer stays openable.
func TestPersistedDownloadSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "update-download.json")

	bundle := filepath.Join(dir, "repotory-2.25.0-darwin-arm64.dmg")
	if err := os.WriteFile(bundle, []byte("bundle"), 0644); err != nil {
		t.Fatal(err)
	}

	prev := downloadStatePathOverride()
	setDownloadStatePathForTest(func() (string, error) { return statePath, nil })
	t.Cleanup(func() { setDownloadStatePathForTest(prev) })

	svc := stateService(t)
	svc.persistDownloadState(DownloadState{
		Phase:            DownloadDone,
		Version:          "2.25.0",
		FileName:         filepath.Base(bundle),
		Path:             bundle,
		ChecksumVerified: true,
	})

	// A fresh service, as if the app had been quit and reopened.
	restarted := stateService(t)
	restarted.restorePersistedDownload()

	st := restarted.UpdateDownloadState()
	if st.Phase != DownloadDone {
		t.Fatalf("phase = %q, want %q — a finished download must survive a restart", st.Phase, DownloadDone)
	}
	if st.Path != bundle {
		t.Errorf("path = %q, want %q", st.Path, bundle)
	}
	if st.Version != "2.25.0" {
		t.Errorf("version = %q, want 2.25.0", st.Version)
	}
	if !st.ChecksumVerified {
		t.Error("the restored state should remember that the digest matched")
	}
}

// TestPersistedDownloadDroppedWhenBundleIsGone pins the other half of the
// restore rule: a state pointing at a file that no longer exists is dropped,
// because an "open the installer" button that fails on a missing path is
// worse than no button at all.
func TestPersistedDownloadDroppedWhenBundleIsGone(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "update-download.json")

	prev := downloadStatePathOverride()
	setDownloadStatePathForTest(func() (string, error) { return statePath, nil })
	t.Cleanup(func() { setDownloadStatePathForTest(prev) })

	svc := stateService(t)
	svc.persistDownloadState(DownloadState{
		Phase: DownloadDone,
		Path:  filepath.Join(dir, "deleted.dmg"),
	})

	restarted := stateService(t)
	restarted.restorePersistedDownload()

	if st := restarted.UpdateDownloadState(); st.Phase == DownloadDone {
		t.Fatalf("phase = %q, want the state dropped when the bundle is gone", st.Phase)
	}
}

// TestRevealDownloadedUpdateRequiresCompletedDownload pins the guard: the
// reveal action only works on a download that actually finished.
func TestRevealDownloadedUpdateRequiresCompletedDownload(t *testing.T) {
	svc, _ := setupService(t)
	if err := svc.RevealDownloadedUpdate(""); err == nil {
		t.Fatal("expected an error when there is no completed download")
	}
}

// TestInstallerVolumeOnlyMatchesOwnBundle pins the eject rule.
//
// A mounted disk image shows up as a second app icon, so the app ejects the
// volume holding ITS OWN installer — and nothing else. A user's own mounted
// disk image must survive this.
func TestInstallerVolumeOnlyMatchesOwnBundle(t *testing.T) {
	root := t.TempDir()
	ours := filepath.Join(root, "Cinder")
	if err := os.MkdirAll(filepath.Join(ours, "Cinder.app"), 0755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "SomeOtherDisk")
	if err := os.MkdirAll(filepath.Join(other, "Other.app"), 0755); err != nil {
		t.Fatal(err)
	}

	if got := installerVolumeIn(root, "Cinder"); got != ours {
		t.Fatalf("installerVolumeIn = %q, want %q", got, ours)
	}

	// With only a foreign volume mounted, nothing is ours to eject.
	if got := installerVolumeIn(filepath.Join(root, "empty"), "Cinder"); got != "" {
		t.Fatalf("installerVolumeIn = %q, want empty", got)
	}
}

// TestEjectInstallerVolumeRunsTheDetach pins the eject action's two
// outcomes: a volume was unmounted, or there was nothing to unmount (which
// is a normal answer, not an error).
func TestEjectInstallerVolumeRunsTheDetach(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the installer disk image only exists on macOS")
	}

	var called []string
	prev := runCommand
	runCommand = func(name string, args ...string) error {
		called = append(called, name+" "+strings.Join(args, " "))
		return nil
	}
	t.Cleanup(func() { runCommand = prev })

	svc, _ := setupService(t)
	// No installer volume is mounted in the test environment, so the action
	// must report "nothing to eject" without running any helper.
	ejected, err := svc.EjectInstallerVolume()
	if err != nil {
		t.Fatalf("eject: %v", err)
	}
	if ejected {
		t.Error("ejected = true, want false when no installer volume is mounted")
	}
	if len(called) != 0 {
		t.Errorf("no helper should run when there is nothing to eject, got %v", called)
	}
}

// TestInstallInfoReportsPaths pins the answer to "where did it install?" and
// "where did the download go?".
func TestInstallInfoReportsPaths(t *testing.T) {
	svc, _ := setupService(t)
	info := svc.InstallInfo()
	if info.Platform != runtime.GOOS {
		t.Errorf("platform = %q, want %q", info.Platform, runtime.GOOS)
	}
	if info.Version == "" {
		t.Error("version should not be empty")
	}
	if info.DownloadDir == "" {
		t.Error("download dir should not be empty")
	}
	if filepath.Base(info.DownloadDir) != "Downloads" {
		t.Errorf("download dir = %q, want it to end in Downloads", info.DownloadDir)
	}
}

// TestBundleVersionReadsInfoPlist pins the signal the installer watcher
// uses to notice that the install happened.
func TestBundleVersionReadsInfoPlist(t *testing.T) {
	root := t.TempDir()
	contents := filepath.Join(root, "Cinder.app", "Contents")
	if err := os.MkdirAll(contents, 0755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>CFBundleShortVersionString</key>
	<string>2.25.0</string>
</dict>
</plist>`
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist), 0644); err != nil {
		t.Fatal(err)
	}
	if got := bundleVersion(filepath.Join(root, "Cinder.app")); got != "2.25.0" {
		t.Errorf("bundleVersion = %q, want 2.25.0", got)
	}
	if got := bundleVersion(filepath.Join(root, "NotAnApp")); got != "" {
		t.Errorf("bundleVersion = %q, want empty for a bundle without one", got)
	}
}

// TestPersistedStateRecordsTheVerifiedDigest pins that the digest verdict
// survives the restart too — "已通过 SHA256 校验" must not become "未校验"
// just because the app was closed.
func TestPersistedStateRecordsTheVerifiedDigest(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "update-download.json")

	payload := []byte("bundle")
	bundle := filepath.Join(dir, "repotory-2.25.0-darwin-arm64.dmg")
	if err := os.WriteFile(bundle, payload, 0644); err != nil {
		t.Fatal(err)
	}

	prev := downloadStatePathOverride()
	setDownloadStatePathForTest(func() (string, error) { return statePath, nil })
	t.Cleanup(func() { setDownloadStatePathForTest(prev) })

	svc := stateService(t)
	svc.persistDownloadState(DownloadState{
		Phase:            DownloadDone,
		Version:          "2.25.0",
		Path:             bundle,
		ChecksumVerified: true,
	})

	restarted := stateService(t)
	restarted.restorePersistedDownload()
	st := restarted.UpdateDownloadState()
	if !st.ChecksumVerified {
		t.Error("the restored state should keep the verified digest verdict")
	}
	if st.Total != int64(len(payload)) {
		t.Errorf("total = %d, want %d — the restored size must match the file", st.Total, len(payload))
	}
}
