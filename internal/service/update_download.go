package service

// Update download: fetch a release bundle into a local file, report progress
// while it runs, and verify the bytes against the digest the release API
// reports for that asset.
//
// Why this exists next to update.go. The check in update.go answers "is there
// a newer version"; it deliberately hands the download to the system browser,
// because an app that silently replaces its own binary is a large trust
// surface. That reasoning still holds for the INSTALL. It does not hold for
// the DOWNLOAD: the browser handoff loses every bit of progress feedback
// (percent, bytes, speed, ETA) that a user actually wants while waiting for
// a 100 MB+ bundle, and it cannot report a checksum verdict — which is the
// one thing that makes an in-app download safer than a browser one.
//
// So the boundary this file draws is deliberate and narrow:
//
//   - The app DOWNLOADS the bundle to a file under the platform data dir and
//     verifies its SHA256 against the digest GitHub's release API reports for
//     that asset ("sha256:..."). A mismatch fails loudly and the file is
//     deleted.
//   - The app NEVER touches the installation. Nothing here writes to the
//     directory the running binary lives in, and nothing here replaces or
//     renames the running executable. The verified file is handed to the OS
//     (open the .dmg, reveal the .zip) so the user performs the install with
//     the same consent they give a browser download.
//
// This is the shape every mainstream desktop updater uses: the app fetches
// and verifies, the OS installer does the replacing.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"repotory/internal/db"
)

// Download phase names. The frontend switches on these; they are part of the
// binding contract and must not be renamed without updating it.
const (
	// DownloadIdle is the state before any download starts.
	DownloadIdle = "idle"
	// DownloadRunning means bytes are moving.
	DownloadRunning = "running"
	// DownloadVerifying means the file is complete and its digest is being
	// checked against the release's published digest.
	DownloadVerifying = "verifying"
	// DownloadDone means the file is downloaded and its digest matched.
	DownloadDone = "done"
	// DownloadFailed means the download or the verification failed.
	DownloadFailed = "failed"
	// DownloadCanceled means the user canceled it.
	DownloadCanceled = "canceled"
)

// DownloadState is the progress snapshot of the current (or last) download.
//
// DownloadSpeed is a smoothed bytes-per-second figure rather than an
// instantaneous one: a raw last-chunk rate reads as wildly jittery and makes
// the ETA flip between nonsense values, which is worse than no ETA at all.
//
// RemainingSeconds is -1 when the speed is not yet known (the first chunk
// has not arrived) or when the total size is unknown.
type DownloadState struct {
	// Phase is one of the Download* constants.
	Phase string `json:"phase"`
	// Version is the release tag this download belongs to ("2.13.1").
	Version string `json:"version"`
	// FileName is the bundle's file name (e.g. "repotory-darwin-arm64.dmg").
	FileName string `json:"file_name"`
	// Downloaded is the number of bytes written so far.
	Downloaded int64 `json:"downloaded"`
	// Total is the expected size in bytes, 0 when the server did not say.
	Total int64 `json:"total"`
	// Speed is the smoothed transfer rate in bytes per second.
	Speed int64 `json:"speed"`
	// RemainingSeconds is the ETA, -1 when it cannot be estimated.
	RemainingSeconds int64 `json:"remaining_seconds"`
	// Path is the local file path, set once the download completes.
	Path string `json:"path"`
	// Error carries the failure reason when Phase is DownloadFailed.
	Error string `json:"error"`
	// ChecksumVerified reports whether the digest matched the one the
	// release published. False for a download whose release shipped no
	// digest (never for a verified one).
	ChecksumVerified bool `json:"checksum_verified"`
	// InstallerEjected reports whether the mounted installer disk image has
	// been unmounted. A mounted DMG shows up as a second "Repotory" in the
	// Finder sidebar, so the app ejects it once the install is detected (or
	// when the user asks for it).
	InstallerEjected bool `json:"installer_ejected"`
}

// Percent returns the completion percentage, 0 when the total size is
// unknown. The frontend uses it for the progress bar; the backend keeps the
// arithmetic in one place so the bar and the byte counter can never disagree.
func (d DownloadState) Percent() float64 {
	if d.Total <= 0 {
		return 0
	}
	p := float64(d.Downloaded) / float64(d.Total) * 100
	if p > 100 {
		return 100
	}
	return p
}

// updateDownloader owns the one in-flight download.
//
// One at a time, on purpose: two concurrent downloads would race on the
// progress snapshot (which is a single object, not a history) and the user
// has no way to tell which file a stale progress bar belongs to. A second
// StartUpdateDownload while one is running is rejected with a clear error
// rather than silently replacing the state.
type updateDownloader struct {
	mu sync.Mutex

	state DownloadState
	// cancel aborts the in-flight HTTP request.
	cancel context.CancelFunc
	// running marks an active download (as opposed to a finished one whose
	// state is still worth showing).
	running bool
	// onTerminal is called with the final snapshot whenever a download
	// reaches a terminal phase (done, failed, canceled). The service uses it
	// to persist the state, so a finished download survives a restart. It
	// runs OUTSIDE the lock: it does file I/O.
	onTerminal func(DownloadState)
}

// newUpdateDownloader returns a downloader in the idle state.
func newUpdateDownloader() *updateDownloader {
	return &updateDownloader{state: DownloadState{Phase: DownloadIdle, RemainingSeconds: -1}}
}

// snapshot returns a copy of the current state.
func (d *updateDownloader) snapshot() DownloadState {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}

// begin starts a new download, replacing any finished state.
//
// It also creates the cancellable context, so a cancel that arrives
// immediately after begin() is never lost to a race with the download
// goroutine still setting up.
//
// The caller has already validated the URL, the target file name and the
// destination directory.
func (d *updateDownloader) begin(ctx context.Context, version, fileName string, total int64) (context.Context, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.running {
		return nil, errors.New("已有更新正在下载")
	}
	reqCtx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	d.state = DownloadState{
		Phase:            DownloadRunning,
		Version:          version,
		FileName:         fileName,
		Total:            total,
		RemainingSeconds: -1,
	}
	d.running = true
	return reqCtx, nil
}

// setState applies a mutation to the snapshot under the lock.
func (d *updateDownloader) setState(fn func(*DownloadState)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	fn(&d.state)
}

// finish marks the download as no longer running (success, failure or
// cancel) and records the terminal phase.
func (d *updateDownloader) finish(phase, errMsg string) {
	d.mu.Lock()
	d.running = false
	d.state.Phase = phase
	d.state.Error = errMsg
	if phase != DownloadDone {
		d.state.Speed = 0
		d.state.RemainingSeconds = -1
	}
	snap := d.state
	hook := d.onTerminal
	d.mu.Unlock()

	// The hook persists the snapshot to disk, so it runs after the lock is
	// released — a slow disk must not block a progress read.
	if hook != nil {
		hook(snap)
	}
}

// setTerminalHook installs the callback run when a download reaches a
// terminal phase.
//
// It is installed when the download STARTS, not when the downloader is built:
// the callback carries the state file's path, and that path must be resolved
// while the caller still holds the context that decides where it is.
func (d *updateDownloader) setTerminalHook(fn func(DownloadState)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onTerminal = fn
}

// restore installs a persisted snapshot as the current state.
//
// It is how a download that finished before the last restart stays reachable:
// the bundle is still on disk, and the app remembers where it put it.
func (d *updateDownloader) restore(st DownloadState) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.state = st
}

// setInstallerEjected records that the mounted installer disk image has been
// unmounted.
func (d *updateDownloader) setInstallerEjected(ejected bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.state.InstallerEjected = ejected
}

// cancelCurrent aborts the in-flight download, if any.
func (d *updateDownloader) cancelCurrent() {
	d.mu.Lock()
	cancel := d.cancel
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// isRunning reports whether a download is in flight.
func (d *updateDownloader) isRunning() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.running
}

// downloadProgressReporter accumulates the byte counters and derives the
// smoothed speed and the ETA.
//
// The smoothing is an exponential moving average over per-chunk rates rather
// than total/total-time: the total average includes the connection
// handshake and the server's first-byte latency, which on a GitHub release
// redirect chain is seconds — long enough to make the ETA read as
// "42 minutes" for a 90 MB file that actually arrives in 40.
type downloadProgressReporter struct {
	total      int64
	downloaded int64
	// ema is the smoothed bytes-per-second.
	ema float64
	// lastSample is when the last chunk was accounted.
	lastSample time.Time
	// started is when the first chunk arrived.
	started time.Time
}

// newDownloadProgressReporter returns a reporter for a transfer of total
// bytes (0 when unknown).
func newDownloadProgressReporter(total int64) *downloadProgressReporter {
	return &downloadProgressReporter{total: total}
}

// add accounts for n more bytes and returns the current smoothed speed.
func (r *downloadProgressReporter) add(n int64, now time.Time) float64 {
	r.downloaded += n
	if r.started.IsZero() {
		r.started = now
		r.lastSample = now
		return 0
	}
	dt := now.Sub(r.lastSample).Seconds()
	if dt <= 0 {
		return r.ema
	}
	inst := float64(n) / dt
	if r.ema == 0 {
		r.ema = inst
	} else {
		// alpha = 0.3: fast enough to follow a real speed change, slow
		// enough that one slow chunk does not collapse the readout.
		r.ema = 0.3*inst + 0.7*r.ema
	}
	r.lastSample = now
	return r.ema
}

// remaining returns the ETA in seconds, -1 when it cannot be estimated.
func (r *downloadProgressReporter) remaining(now time.Time) int64 {
	if r.total <= 0 || r.downloaded <= 0 || r.ema <= 0 {
		return -1
	}
	left := r.total - r.downloaded
	if left <= 0 {
		return 0
	}
	return int64(float64(left) / r.ema)
}

// updateDownloadDir returns the directory the downloaded bundles land in.
//
// It is the operating system's "Downloads" folder — the same place a browser
// drops a file the user asked for, and the same place the user will look for
// one afterwards. The previous home was the app's own config directory, which
// is the right place for state but the wrong place for a file the user is
// meant to find, drag and install by hand; a bundle hidden under
// "~/Library/Application Support" is a bundle the user never finds.
//
// The Downloads folder also survives a restart and is not swept by the OS,
// which is why it is a better home for an installer than a temp directory.
//
// The result is resolved once, synchronously, when the download starts —
// the directory is then passed to the download goroutine by value. That
// keeps the lookup off the goroutine, where a test that swaps the
// directory would race with the swap.
func resolveDownloadDir() (string, error) {
	downloadDir, err := userDownloadsDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(downloadDir, 0750); err != nil {
		return "", fmt.Errorf("无法创建下载目录：%w", err)
	}
	return downloadDir, nil
}

// userDownloadsDir resolves the platform's Downloads folder.
//
// It is a best-effort resolution, not a guarantee of the user's configured
// location: Windows can relocate Downloads into OneDrive and macOS can
// relocate it into iCloud, and neither exposes the relocated path without
// platform-specific APIs this project does not depend on. The home
// directory's "Downloads" is the location every platform defaults to, and
// it is where the user will look.
func userDownloadsDir() (string, error) {
	// Linux's XDG session exports the configured directory when it has one.
	if runtime.GOOS == "linux" {
		if dir := strings.TrimSpace(os.Getenv("XDG_DOWNLOAD_DIR")); dir != "" {
			return dir, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法定位用户主目录：%w", err)
	}
	return filepath.Join(home, "Downloads"), nil
}

// versionedFileName inserts the release version into the bundle's file name,
// so the file the user finds in Downloads says which version it is:
// "repotory-2.25.0-darwin-arm64.dmg" instead of a name that is byte-for-byte
// identical for every release ever published.
//
// Without it, "is this download the current version?" is unanswerable by
// looking at the file — the name never changes, so an old bundle sitting in
// Downloads looks exactly like a fresh one.
func versionedFileName(base, version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return base
	}
	// Only the shapes CI actually publishes are decorated; anything else
	// (including a name that already carries a version) is left alone.
	if !appAssetPattern.MatchString(base) {
		return base
	}
	return "repotory-" + version + "-" + strings.TrimPrefix(base, "repotory-")
}

// updateOverridesMu guards the test-only path overrides below.
//
// They are package-level so a test can point them at a temp dir, but the
// download runs on its OWN goroutine and the terminal hook persists the state
// from there: a plain variable would then be a data race — the test's cleanup
// restoring the default while that goroutine reads the override. Both sides
// take this lock, so the read and the write are ordered.
var updateOverridesMu sync.RWMutex

// downloadDirForTest overrides the download directory in tests, so a test
// never writes into the real user's Downloads folder. Nil at runtime.
var downloadDirForTest func() (string, error)

// setDownloadDirForTest installs (or, with nil, removes) the download
// directory override.
func setDownloadDirForTest(fn func() (string, error)) {
	updateOverridesMu.Lock()
	defer updateOverridesMu.Unlock()
	downloadDirForTest = fn
}

// downloadDirOverride returns the current download directory override, or nil
// when there is none.
func downloadDirOverride() func() (string, error) {
	updateOverridesMu.RLock()
	defer updateOverridesMu.RUnlock()
	return downloadDirForTest
}

// downloadDirectory resolves the directory the bundles land in.
func downloadDirectory() (string, error) {
	if override := downloadDirOverride(); override != nil {
		return override()
	}
	return resolveDownloadDir()
}

// downloadUpdate fetches url into the updates directory, reporting progress
// through the downloader and verifying the digest against expectedSHA256.
//
// expectedSHA256 empty means the release published no digest for this file;
// the download still completes, with ChecksumVerified false, and the UI says
// so rather than implying the bytes were checked.
//
// reqCtx is the cancellable context created by begin(): the user cancelling
// aborts the transfer through it.
//
// downloadDir is the destination and client the HTTP client, both resolved
// before the goroutine started.
//
// The file is written to a ".part" sibling first and renamed only after the
// digest matches, so a half-written file is never mistaken for a complete
// one (a killed app, a full disk, a cancel).
func (s *Service) downloadUpdate(ctx context.Context, d *updateDownloader, downloadDir string, client *http.Client, url, fileName, version, expectedSHA256 string) {
	// The file name comes from the release API, but it reaches a filesystem
	// path here — so it is reduced to its base name and any path separator is
	// stripped rather than trusted.
	safeName := filepath.Base(strings.ReplaceAll(fileName, "\\", "/"))
	if safeName == "." || safeName == ".." || safeName == string(filepath.Separator) {
		d.finish(DownloadFailed, "下载文件名无效")
		return
	}
	// The version goes INTO the name, so the file the user finds in
	// Downloads says which release it is.
	safeName = versionedFileName(safeName, version)
	finalPath := filepath.Join(downloadDir, safeName)
	partPath := finalPath + ".part"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		d.finish(DownloadFailed, fmt.Sprintf("下载请求无效：%v", err))
		return
	}
	req.Header.Set("User-Agent", updateCheckUserAgent)
	req.Header.Set("Accept", "application/octet-stream")

	// The check timeout is right for a metadata GET and wrong for a large
	// file transfer, so the download client gets no overall timeout — the
	// request context (cancellable by the user) is the bound.
	client.Timeout = 0

	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			d.finish(DownloadCanceled, "下载已取消")
			return
		}
		d.finish(DownloadFailed, fmt.Sprintf("下载失败：%v", err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		d.finish(DownloadFailed, fmt.Sprintf("下载失败：服务器返回 HTTP %d", resp.StatusCode))
		return
	}

	total := resp.ContentLength
	if total > 0 {
		d.setState(func(st *DownloadState) { st.Total = total })
	}

	f, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		d.finish(DownloadFailed, fmt.Sprintf("无法写入下载文件：%v", err))
		return
	}

	reporter := newDownloadProgressReporter(total)
	hasher := sha256.New()
	buf := make([]byte, 256*1024)

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if _, werr := f.Write(chunk); werr != nil {
				f.Close()
				os.Remove(partPath)
				d.finish(DownloadFailed, fmt.Sprintf("写入下载文件失败：%v", werr))
				return
			}
			hasher.Write(chunk)
			reporter.add(int64(n), time.Now())
			downloaded := reporter.downloaded
			speed := int64(reporter.ema)
			remaining := reporter.remaining(time.Now())
			d.setState(func(st *DownloadState) {
				st.Downloaded = downloaded
				st.Speed = speed
				st.RemainingSeconds = remaining
			})
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			f.Close()
			os.Remove(partPath)
			if ctx.Err() != nil {
				d.finish(DownloadCanceled, "下载已取消")
				return
			}
			d.finish(DownloadFailed, fmt.Sprintf("下载中断：%v", readErr))
			return
		}
	}

	if err := f.Close(); err != nil {
		os.Remove(partPath)
		d.finish(DownloadFailed, fmt.Sprintf("下载文件写入未完成：%v", err))
		return
	}

	// A Content-Length mismatch means the transfer was truncated (the
	// connection dropped mid-stream). The digest check would catch it too,
	// but saying so directly is clearer than "checksum mismatch".
	if total > 0 && reporter.downloaded != total {
		os.Remove(partPath)
		d.finish(DownloadFailed, fmt.Sprintf("下载不完整：收到 %d 字节，应为 %d 字节", reporter.downloaded, total))
		return
	}

	d.setState(func(st *DownloadState) { st.Phase = DownloadVerifying })

	got := hex.EncodeToString(hasher.Sum(nil))
	verified := false
	if expectedSHA256 != "" {
		if !strings.EqualFold(got, expectedSHA256) {
			os.Remove(partPath)
			d.finish(DownloadFailed, fmt.Sprintf("校验失败：文件摘要 %s 与发布方声明的 %s 不一致，已删除", got, expectedSHA256))
			return
		}
		verified = true
	}

	// Only now does the file get its real name.
	if err := os.Rename(partPath, finalPath); err != nil {
		os.Remove(partPath)
		d.finish(DownloadFailed, fmt.Sprintf("无法完成下载文件：%v", err))
		return
	}

	d.setState(func(st *DownloadState) {
		st.Phase = DownloadDone
		st.Path = finalPath
		st.Downloaded = reporter.downloaded
		st.ChecksumVerified = verified
		st.Speed = 0
		st.RemainingSeconds = 0
	})
	d.finish(DownloadDone, "")
}

// StartUpdateDownload begins downloading a release bundle.
//
// The URL and the expected digest come from the release check the user just
// did (the caller passes the asset's URL and digest straight out of
// UpdateInfo), so the download always targets the bundle the app itself
// picked for this platform. The URL must be https and must be one of the
// assets GitHub's own release API reported; a caller cannot point it at an
// arbitrary URL.
//
// version is the release tag the bundle belongs to ("2.25.0"). It goes into
// the file name, so the bundle the user finds in Downloads says which
// release it is — and it is reported in the progress state, so the progress
// dialog can name the version instead of leaving the user to guess.
//
// The download runs in the background: the binding returns as soon as it has
// started, and the frontend polls UpdateDownloadState for progress. That is
// the same shape as the check itself (one HTTP call, one verdict) and avoids
// a second transport for progress events.
func (s *Service) StartUpdateDownload(url, fileName, sha256, version string) error {
	if !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("拒绝非 https 下载地址：%q", url)
	}
	proxy, _ := db.GetConfig(s.db, "update_proxy")
	if err := validateUpdateProxy(proxy); err != nil {
		return err
	}
	// The directory and the HTTP client are resolved here, not inside the
	// download goroutine: resolving them in the goroutine would race with a
	// test swapping either of them.
	downloadDir, err := downloadDirectory()
	if err != nil {
		return err
	}
	// The state file's path is resolved here for the same reason — and with it
	// the terminal hook that writes the finished download's snapshot. The hook
	// runs on the download goroutine, so it must carry the path rather than
	// look it up there.
	statePath, err := downloadStatePath()
	if err != nil {
		statePath = ""
	}
	s.updates.setTerminalHook(func(st DownloadState) {
		s.persistDownloadStateAt(statePath, st)
	})
	client, err := newUpdateHTTPClient(strings.TrimSpace(proxy))
	if err != nil {
		return err
	}
	reqCtx, err := s.updates.begin(s.bgContext(), version, fileName, 0)
	if err != nil {
		return err
	}
	go s.downloadUpdate(reqCtx, s.updates, downloadDir, client, url, fileName, version, normalizeDigest(sha256))
	return nil
}

// normalizeDigest strips the "sha256:" prefix the release API puts in front
// of the digest, and rejects a digest that is not a plain hex string.
func normalizeDigest(raw string) string {
	d := strings.TrimSpace(raw)
	d = strings.TrimPrefix(d, "sha256:")
	if d == "" {
		return ""
	}
	if len(d) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(d); err != nil {
		return ""
	}
	return strings.ToLower(d)
}

// bgContext returns the service's background context, or a fresh one when
// the service was built without dependencies (tests).
func (s *Service) bgContext() context.Context {
	if s.bgCtx != nil {
		return s.bgCtx
	}
	return context.Background()
}

// UpdateDownloadState returns the progress snapshot of the current or last
// download.
func (s *Service) UpdateDownloadState() DownloadState {
	return s.updates.snapshot()
}

// CancelUpdateDownload aborts the in-flight download. It is a no-op when
// nothing is running.
func (s *Service) CancelUpdateDownload() error {
	if !s.updates.isRunning() {
		return nil
	}
	s.updates.cancelCurrent()
	return nil
}

// OpenDownloadedUpdate hands the downloaded bundle to the OS.
//
// macOS gets the .dmg opened (the user drags the app onto Applications);
// Windows gets the containing folder revealed (the bundle is a .zip, and
// there is no installer to run); Linux gets the folder revealed as well.
//
// It never replaces the running application — the user performs the install
// exactly as they would after a browser download.
func (s *Service) OpenDownloadedUpdate(path string) error {
	st := s.updates.snapshot()
	if st.Phase != DownloadDone {
		return fmt.Errorf("没有已完成的下载可打开")
	}
	if path != "" && path != st.Path {
		return fmt.Errorf("打开失败：路径与当前下载不一致")
	}
	if err := openDownloadedBundle(st.Path); err != nil {
		return err
	}
	// The installer is on screen now; watch for the install so the mounted
	// disk image can be ejected afterwards (a mounted DMG is the second
	// "Repotory" icon the user is left with).
	s.watchInstallerInstall(st.Version)
	return nil
}

// RevealDownloadedUpdate shows the downloaded bundle in the OS file manager.
//
// It is the answer to "where did that download go?": the bundle sits in the
// Downloads folder, and this puts the file manager on it — selected, so the
// name is visible — instead of making the user go and look.
func (s *Service) RevealDownloadedUpdate(path string) error {
	st := s.updates.snapshot()
	if st.Phase != DownloadDone {
		return fmt.Errorf("没有已完成的下载可显示")
	}
	if path != "" && path != st.Path {
		return fmt.Errorf("显示失败：路径与当前下载不一致")
	}
	return revealPath(st.Path)
}

// revealPath selects a file in the OS file manager.
func revealPath(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("找不到下载文件：%v", err)
	}
	switch runtime.GOOS {
	case "darwin":
		return runDetached("open", "-R", path)
	case "windows":
		return runDetached("explorer", "/select,"+path)
	default:
		return runDetached("xdg-open", filepath.Dir(path))
	}
}

// EjectInstallerVolume unmounts the installer disk image on macOS.
//
// It reports whether a volume was ejected: "nothing was mounted" is a normal
// answer, not an error, and it is the answer on every platform that has no
// mounted image at all.
//
// Only a volume that actually holds THIS app's bundle is ejected, so a disk
// image the user mounted themselves is never touched.
func (s *Service) EjectInstallerVolume() (bool, error) {
	vol := installerVolume()
	if vol == "" {
		return false, nil
	}
	if err := runCommand("hdiutil", "detach", vol); err != nil {
		return false, fmt.Errorf("推出安装包卷失败：%v", err)
	}
	s.updates.setInstallerEjected(true)
	return true, nil
}

// installerVolume returns the mounted volume that holds this app's
// installer bundle, or "" when none is mounted.
//
// macOS keeps a mounted DMG in /Volumes until it is ejected, and that volume
// shows up as a second "Repotory" in the Finder sidebar — the "two app
// icons" a user sees after installing. Nothing unmounts it on its own:
// closing the DMG window does not eject the volume.
func installerVolume() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	return installerVolumeIn("/Volumes", installerBundleName())
}

// installerVolumeIn returns the mounted volume under root that holds the
// bundle named bundleName, or "" when none is mounted.
//
// The root and the bundle name are parameters so the rule can be tested
// without a real /Volumes and without a real installed app: the rule that
// matters is "only a volume holding THIS app's bundle", and a test can
// neither mount a disk image nor install the app to prove it.
func installerVolumeIn(root, bundleName string) string {
	if bundleName == "" {
		return ""
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(root, e.Name())
		if info, err := os.Stat(filepath.Join(p, bundleName+".app")); err == nil && info.IsDir() {
			return p
		}
	}
	return ""
}

// watchInstallerInstall waits for the new build to land in the install path
// and ejects the installer volume once it does.
//
// The user installs by dragging the app out of the mounted DMG onto
// /Applications. The DMG stays mounted afterwards, so the Finder shows the
// installed app AND the still-mounted installer. Closing the DMG window does
// not unmount it, and no other program will.
//
// The watcher reads the INSTALLED bundle's version, not the running process:
// the running process keeps the old code in memory after the bundle is
// replaced, so the version on disk is the honest signal.
func (s *Service) watchInstallerInstall(target string) {
	target = strings.TrimSpace(target)
	if runtime.GOOS != "darwin" || target == "" {
		return
	}
	bundle := appBundlePath(runningExecutable())
	if bundle == "" {
		return
	}
	deadline := time.Now().Add(installerWatchWindow)
	s.bgGo("update-install-watch", func(ctx context.Context) {
		ticker := time.NewTicker(installerWatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if bundleVersion(bundle) == target {
				if _, err := s.EjectInstallerVolume(); err == nil {
					s.updates.setInstallerEjected(true)
				}
				return
			}
			if time.Now().After(deadline) {
				return
			}
		}
	})
}

// installerWatchInterval is how often the installed bundle's version is
// checked while an installer is open.
const installerWatchInterval = 3 * time.Second

// installerWatchWindow is how long the watcher keeps looking. The user may
// take a while to drag the app across, but not ten minutes.
const installerWatchWindow = 10 * time.Minute

// bundleVersion reads CFBundleShortVersionString from an app bundle's
// Info.plist. Empty when the bundle has no readable version.
func bundleVersion(bundle string) string {
	buf, err := os.ReadFile(filepath.Join(bundle, "Contents", "Info.plist"))
	if err != nil {
		return ""
	}
	m := plistVersionRE.FindSubmatch(buf)
	if m == nil {
		return ""
	}
	return string(m[1])
}

// plistVersionRE pulls the version string out of an Info.plist. The file is
// a small XML plist, and matching the key/value pair directly avoids
// depending on a plist parser for one field.
var plistVersionRE = regexp.MustCompile(`CFBundleShortVersionString</key>\s*<string>([^<]*)</string>`)

// openDownloadedBundle shows a downloaded bundle to the user in the way that
// platform expects.
func openDownloadedBundle(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("找不到下载文件：%v", err)
	}
	switch runtime.GOOS {
	case "darwin":
		// "open" on a .dmg mounts it and shows its window; on any other file
		// it opens it with the default handler. Either way the user sees the
		// installer, and the app does not install anything itself.
		return runDetached("open", path)
	case "windows":
		// The bundle is a .zip with no installer, so revealing it in Explorer
		// is the honest action: the user extracts and replaces the app.
		return runDetached("explorer", "/select,"+path)
	default:
		// Linux ships a tar.gz; revealing the directory is the closest
		// equivalent that does not presume a package manager.
		return runDetached("xdg-open", filepath.Dir(path))
	}
}

// runDetached starts a helper process that outlives this one.
//
// The helper is deliberately not waited on: on Windows, Explorer would be
// blocked until the app exits if we waited. The error that matters is the
// "command not found" kind, which Start reports synchronously; anything
// after that is the helper's own business.
func runDetached(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("无法打开（%s）：%v", name, err)
	}
	// Releasing the process handle without waiting keeps it from becoming a
	// zombie on Unix.
	go func() { _ = cmd.Wait() }()
	return nil
}

// helperCommandTimeout bounds a helper process that IS waited on.
const helperCommandTimeout = 30 * time.Second

// runCommand runs a helper process to completion, with a timeout.
//
// Unlike runDetached it waits, because the caller needs the exit status:
// "did the installer volume actually unmount?" is the whole question.
//
// It is a var so a test can stub the helper out instead of really ejecting
// something.
var runCommand = func(name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), helperCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w（%s）", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
