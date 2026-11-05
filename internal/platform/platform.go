// Package platform centralises OS-specific behaviour: default scan roots,
// user data locations (database, plugins, log file) and git user detection.
//
// User data paths are stable across versions: an installation that already
// has a legacy data directory (gitboard -> gitbuddy -> reponest) is renamed to
// dirName ("repotory") on first launch after the rename, and the database file
// itself (dashboard.db, a name carried over from the git-dashboard era) is
// renamed to repotory.db, so the database, plugins and logs carry over
// instead of being orphaned.
package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// dirName is the user data directory and the log file basename.
//
// The brand changed (Repotory -> Cinder, ADR-0049) but this string did not:
// the data directory is a frozen identifier, not a display name — renaming it
// would move every user's database, plugins and logs for no user-visible gain,
// and the migration chain handles one generation back per rename. The command
// line keeps the old word for the same reason (repotory-mcp, repotory_*).
const dirName = "repotory"

// legacyDirName is the data directory used before the RepoNest -> Repotory
// rename (which itself succeeded the GitBuddy era, and before that GitBoard).
// One generation back is handled per migration, exactly as each earlier
// rename did in its day. The spelling is intentional: it names data written
// by older builds and is only read by the migration below.
const legacyDirName = "reponest"

// DefaultScanRoots returns platform-specific default scan root directories.
// Windows: all drive letters except C: (the system drive).
// macOS / Linux: the user's home directory.
func DefaultScanRoots() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		// An empty home would seed the literal "" as a scan root — a dead
		// entry the user cannot remove later (every write re-validates).
		// Seed nothing instead; the user adds a root themselves.
		return nil
	}

	switch runtime.GOOS {
	case "windows":
		roots := []string{}
		for _, drive := range getWindowsDrives() {
			upper := strings.ToUpper(drive)
			if upper != "C:" && upper != "C:\\" {
				roots = append(roots, drive)
			}
		}
		if len(roots) == 0 {
			roots = append(roots, home)
		}
		return roots
	default: // darwin, linux and others
		return []string{home}
	}
}

// getWindowsDrives enumerates available drive letters on Windows.
// On non-Windows platforms, returns an empty slice.
func getWindowsDrives() []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	drives := []string{}
	for c := 'A'; c <= 'Z'; c++ {
		path := string(c) + ":\\"
		if _, err := os.Stat(path); err == nil {
			drives = append(drives, path)
		}
	}
	return drives
}

// GetGitUserName returns the git user.name from the user's GLOBAL config.
// It deliberately ignores repo-local config: the value splits "mine" vs "all"
// across every scanned repository, so a per-repo setting is meaningless here —
// and reading the ambient config made the result depend on the process's
// working directory, which is arbitrary for the headless server (spawned by a
// plugin) and could disagree with the desktop app on the same machine.
func GetGitUserName() string {
	cmd := exec.Command("git", "config", "--global", "user.name")
	out, err := cmd.Output()
	if err != nil {
		// fallback to OS username
		if u, e := os.UserHomeDir(); e == nil && u != "" {
			return filepath.Base(u)
		}
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

var migrateLegacyOnce sync.Once

// migrateLegacyData renames the pre-rename "reponest" data directory to
// dirName, moving the database and plugins across. It is a no-op on a fresh
// install or once the rename has happened. Failures are ignored on purpose:
// a move that cannot complete (permissions, cross-device) must not stop the
// app from starting, and the old directory stays in place to be moved by hand.
func migrateLegacyData() {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return
	}
	old := filepath.Join(configDir, legacyDirName)
	if _, err := os.Stat(old); err != nil {
		return
	}
	current := filepath.Join(configDir, dirName)
	if _, err := os.Stat(current); err == nil {
		return
	}
	_ = os.Rename(old, current)
}

// fallbackDir returns a private (0700) directory for degraded environments
// where the platform config dir is unavailable. os.MkdirTemp creates the
// directory with an unpredictable name, so another local process cannot
// pre-create it to plant files (a fixed /tmp/repotory* path could be —
// CWE-379). It is created once per process and reused, so the DB and plugin
// paths stay stable within a run.
var fallbackDirOnce struct {
	sync.Once
	path string
}

func fallbackDir() string {
	fallbackDirOnce.Do(func() {
		dir, err := os.MkdirTemp("", dirName+"-")
		if err != nil {
			// MkdirTemp failing means the temp root itself is unusable; keep
			// going with a best-effort name rather than failing the app.
			// The pid suffix keeps the path unpredictable per process — the
			// fixed spelling this replaces is exactly the pre-created path the
			// comment above (CWE-379) warns about.
			dir = filepath.Join(os.TempDir(), fmt.Sprintf("%s-degraded-%d", dirName, os.Getpid()))
		}
		fallbackDirOnce.path = dir
	})
	return fallbackDirOnce.path
}

// GetDbPath returns the path to the SQLite database file.
func GetDbPath() string {
	migrateLegacyOnce.Do(migrateLegacyData)

	if configDir, err := os.UserConfigDir(); err == nil {
		dir := filepath.Join(configDir, dirName)
		if err := os.MkdirAll(dir, 0750); err == nil {
			migrateLegacyDbFile(dir)
			return filepath.Join(dir, "repotory.db")
		}
	}
	return filepath.Join(fallbackDir(), "repotory.db")
}

// migrateLegacyDbFile renames the database file left behind by earlier
// versions (dashboard.db) to repotory.db, including any SQLite sidecar files
// (-wal/-shm). It runs after the data directory is known and is a no-op on a
// fresh install, once the rename has happened, or when a repotory.db already
// exists (the new name always wins; the old file is left in place to be
// removed by hand). Same failure posture as migrateLegacyData: a rename that
// cannot complete must not stop the app from starting.
func migrateLegacyDbFile(dir string) {
	current := filepath.Join(dir, dirName+".db")
	if _, err := os.Stat(current); err == nil {
		return
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		old := filepath.Join(dir, "dashboard.db"+suffix)
		if _, err := os.Stat(old); err != nil {
			continue
		}
		_ = os.Rename(old, filepath.Join(dir, dirName+".db"+suffix))
	}
}

// GetPluginsDir was removed with the yaegi script-plugin runtime (C11 方案B):
// there are no user plugin directories anymore. The legacy-data rename it used
// to trigger defensively is already guaranteed by GetDbPath, the only DB-path
// entry point.

// migrateLegacyLog renames the log file left behind by earlier versions
// (gitboard.log -> repotory.log). It runs after the log directory is known,
// which covers every platform: on Windows the log lives inside the config
// directory that migrateLegacyData already renamed, so only the file name is
// left to fix there.
func migrateLegacyLog(dir string) {
	old := filepath.Join(dir, legacyDirName+".log")
	if _, err := os.Stat(old); err != nil {
		return
	}
	current := filepath.Join(dir, dirName+".log")
	if _, err := os.Stat(current); err == nil {
		return
	}
	_ = os.Rename(old, current)
}

// GetLogPath returns the platform-appropriate log file path.
//
//	darwin:  ~/Library/Logs/repotory.log
//	windows: %APPDATA%\repotory\logs\repotory.log
//	linux:   $XDG_STATE_HOME/repotory/repotory.log (default ~/.local/state/...)
func GetLogPath() string {
	var dir string
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join(os.TempDir(), dirName+".log")
		}
		dir = filepath.Join(home, "Library", "Logs")
	case "windows":
		appData, err := os.UserConfigDir()
		if err != nil {
			return filepath.Join(os.TempDir(), dirName+".log")
		}
		dir = filepath.Join(appData, dirName, "logs")
	default:
		state := os.Getenv("XDG_STATE_HOME")
		if state == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return filepath.Join(os.TempDir(), dirName+".log")
			}
			state = filepath.Join(home, ".local", "state")
		}
		dir = filepath.Join(state, dirName)
	}
	if err := os.MkdirAll(dir, 0750); err != nil {
		return filepath.Join(os.TempDir(), dirName+".log")
	}
	migrateLegacyLog(dir)
	return filepath.Join(dir, dirName+".log")
}
