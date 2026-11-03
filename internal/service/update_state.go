package service

// Persisted update-download state.
//
// Why this file exists. The download's progress lived in memory only, so a
// finished download became unreachable the moment the app restarted: the
// bundle was still in Downloads, but the app no longer knew it, and the
// button that opens the installer answered "没有已完成的下载可打开". The user
// was left with a file on disk and an app that could not open it.
//
// The state file lives in the app's own data dir, NOT next to the bundle in
// Downloads: that folder belongs to the user, and a stray JSON file in it is
// litter in the one place the user goes looking for their downloads.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// persistedDownload is the on-disk shape of the last download.
type persistedDownload struct {
	Phase            string `json:"phase"`
	Version          string `json:"version"`
	FileName         string `json:"file_name"`
	Path             string `json:"path"`
	ChecksumVerified bool   `json:"checksum_verified"`
	CompletedAt      string `json:"completed_at"`
}

// downloadStatePath returns the file the last download's state is kept in.
func downloadStatePath() (string, error) {
	if override := downloadStatePathOverride(); override != nil {
		return override()
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("无法定位用户数据目录：%w", err)
	}
	return filepath.Join(configDir, "repotory", "update-download.json"), nil
}

// downloadStatePathForTest overrides the state file's location in tests, so a
// test never reads or writes the real user's state. Nil at runtime.
//
// Guarded by updateOverridesMu: the download's terminal hook persists the
// state from the download goroutine, so the override is read off the main
// goroutine too.
var downloadStatePathForTest func() (string, error)

// setDownloadStatePathForTest installs (or, with nil, removes) the state file
// location override.
func setDownloadStatePathForTest(fn func() (string, error)) {
	updateOverridesMu.Lock()
	defer updateOverridesMu.Unlock()
	downloadStatePathForTest = fn
}

// downloadStatePathOverride returns the current state path override, or nil
// when there is none.
func downloadStatePathOverride() func() (string, error) {
	updateOverridesMu.RLock()
	defer updateOverridesMu.RUnlock()
	return downloadStatePathForTest
}

// persistDownloadState writes a finished download's snapshot to disk.
//
// It is called from the downloader's terminal hook, which runs outside the
// downloader's lock: a slow disk must not block a progress read.
//
// The path is resolved HERE, on the caller's goroutine, and handed down — the
// download's terminal hook runs on the download goroutine, and resolving the
// path there would race with a test swapping the override, and could write
// the state into the real user's data dir after the override is gone.
func (s *Service) persistDownloadState(st DownloadState) {
	path, err := downloadStatePath()
	if err != nil {
		return
	}
	s.persistDownloadStateAt(path, st)
}

// persistDownloadStateAt writes a finished download's snapshot to a known
// path. An empty path means the caller could not resolve one, and nothing is
// written.
func (s *Service) persistDownloadStateAt(path string, st DownloadState) {
	if st.Phase != DownloadDone && st.Phase != DownloadFailed {
		return
	}
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return
	}
	payload := persistedDownload{
		Phase:            st.Phase,
		Version:          st.Version,
		FileName:         st.FileName,
		Path:             st.Path,
		ChecksumVerified: st.ChecksumVerified,
		CompletedAt:      time.Now().UTC().Format(time.RFC3339),
	}
	buf, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return
	}
	// Written to a sibling first and renamed: a half-written state file would
	// be read back as garbage and lose the user's pointer to the bundle.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// restorePersistedDownload reads the last download back at startup.
//
// A state whose bundle has since been moved or deleted is DROPPED rather than
// restored: the "open the installer" button would then fail on a path that no
// longer exists, which is worse than the button not being there at all.
func (s *Service) restorePersistedDownload() {
	path, err := downloadStatePath()
	if err != nil {
		return
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var p persistedDownload
	if err := json.Unmarshal(buf, &p); err != nil {
		return
	}
	if p.Phase != DownloadDone || p.Path == "" {
		return
	}
	info, err := os.Stat(p.Path)
	if err != nil || info.IsDir() {
		return
	}
	s.updates.restore(DownloadState{
		Phase:            DownloadDone,
		Version:          p.Version,
		FileName:         p.FileName,
		Path:             p.Path,
		Downloaded:       info.Size(),
		Total:            info.Size(),
		ChecksumVerified: p.ChecksumVerified,
		RemainingSeconds: 0,
	})
}
