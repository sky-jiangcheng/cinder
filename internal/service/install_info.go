package service

// Install information: where this build actually lives, and where the update
// bundles are written.
//
// Why it exists. A user who installs from a DMG ends up with an app in
// /Applications AND the disk image still mounted in the Finder sidebar — two
// icons that look like the same application — and nothing in the app ever
// says where it was installed. Both questions ("where did it install?" and
// "where did the download go?") now have one answer, and the answer comes
// from the running binary's own report about itself rather than from a
// hardcoded path.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"repotory/internal/version"
)

// defaultAppBundleName is the bundle's file name without the ".app" suffix,
// used when the running build does not live inside a bundle (a dev build run
// straight from the binary).
const defaultAppBundleName = "Repotory"

// InstallInfo describes where this build lives and where update bundles land.
type InstallInfo struct {
	// Version is the running build's version.
	Version string `json:"version"`
	// Platform is the OS this build runs on ("darwin", "windows", "linux").
	Platform string `json:"platform"`
	// InstallPath is the application bundle this build runs from
	// ("/Applications/Repotory.app"), or the executable's own directory when
	// the build does not live in a bundle.
	InstallPath string `json:"install_path"`
	// ExecutablePath is the running binary itself.
	ExecutablePath string `json:"executable_path"`
	// DownloadDir is the folder update bundles are written to.
	DownloadDir string `json:"download_dir"`
}

// InstallInfo reports where this build lives and where update bundles land.
func (s *Service) InstallInfo() *InstallInfo {
	info := InstallInfo{
		Version:  version.Version,
		Platform: runtime.GOOS,
	}
	if exe := runningExecutable(); exe != "" {
		info.ExecutablePath = exe
		info.InstallPath = appBundlePath(exe)
	}
	if dir, err := downloadDirectory(); err == nil {
		info.DownloadDir = dir
	}
	return &info
}

// runningExecutable returns the path of the running binary with symlinks
// resolved. Empty when the OS refuses to say.
func runningExecutable() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

// appBundlePath returns the .app bundle containing the given executable, or
// the executable's own directory when it does not live in one.
//
// The macOS layout is Repotory.app/Contents/MacOS/repotory, so three hops are
// enough; the walk is bounded so a deep symlink cannot turn into an
// unbounded climb.
func appBundlePath(exe string) string {
	if exe == "" {
		return ""
	}
	dir := filepath.Dir(exe)
	for i := 0; i < 4; i++ {
		if filepath.Ext(dir) == ".app" {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Dir(exe)
}

// installerBundleName returns the installer bundle's file name without the
// ".app" suffix ("Repotory"), derived from the bundle this build runs from.
//
// It is derived rather than hardcoded so a renamed bundle still matches the
// copy inside the mounted disk image.
func installerBundleName() string {
	bundle := appBundlePath(runningExecutable())
	if bundle == "" {
		return defaultAppBundleName
	}
	name := strings.TrimSuffix(filepath.Base(bundle), ".app")
	if name == "" || name == "." || name == string(filepath.Separator) {
		return defaultAppBundleName
	}
	return name
}
