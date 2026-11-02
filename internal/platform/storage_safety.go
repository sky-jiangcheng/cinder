package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// StorageSafetyReport describes the safety assessment of the data directory.
type StorageSafetyReport struct {
	Path     string `json:"path"`
	IsSafe   bool   `json:"is_safe"`
	Warning  string `json:"warning,omitempty"`
	RiskType string `json:"risk_type,omitempty"` // "cloud_sync", "fuse_network", "unwritable"
}

// CheckStorageSafety inspects the given database or data directory path for
// storage environments known to endanger SQLite WAL and concurrent locking
// (such as FUSE, NFS, network shares, or active cloud sync directories like
// iCloud, OneDrive, Dropbox).
//
// The target may be a database file or the directory that holds it. When it
// does not exist yet, the parent decides: an existing parent directory means
// the target is a file inside it (a not-yet-created database), otherwise the
// target itself is the directory. Resolving it the other way round would make
// the writability probe fail on every fresh install.
func CheckStorageSafety(targetPath string) StorageSafetyReport {
	if targetPath == "" {
		targetPath = GetDbPath()
	}
	dir := targetPath
	if fi, err := os.Stat(targetPath); err == nil {
		if !fi.IsDir() {
			dir = filepath.Dir(targetPath)
		}
	} else if parent := filepath.Dir(targetPath); isExistingDir(parent) {
		// The target does not exist, and the parent IS a directory — so the
		// target was meant to be a file inside it. This is the fresh-install
		// case (GetDbPath creates the data directory but not the database file
		// yet) and the "data directory was removed" case. Resolving the
		// directory wrongly here means the writability probe below writes
		// "<db file>/.safety_probe_<pid>", which can never succeed, so every
		// fresh install reports a bogus "unwritable" storage risk.
		dir = parent
	}

	report := StorageSafetyReport{
		Path:   dir,
		IsSafe: true,
	}

	cleanDir := filepath.Clean(dir)
	// The patterns below are path text, so the separators have to be canonical
	// first: Windows spells them "\" and the multi-segment patterns are written
	// "a/b". Without this, an iCloud path handed to us on Windows matched
	// nothing and the probe fell through to a misleading "unwritable".
	cleanLower := strings.ToLower(filepath.ToSlash(cleanDir))

	// 1. Path-based pattern detection for cloud sync & virtual mounts (fast exit)
	cloudSyncPatterns := []struct {
		substr string
		name   string
	}{
		{"mobile documents/com~apple~clouddocs", "iCloud Drive"},
		{"library/mobile documents", "iCloud"},
		{"dropbox", "Dropbox"},
		{"onedrive", "OneDrive"},
		{"google drive", "Google Drive"},
		{"box sync", "Box"},
		{"nutstore", "Nutstore (坚果云)"},
		{"baidunetdisk", "BaiduNetdisk"},
	}

	for _, p := range cloudSyncPatterns {
		if strings.Contains(cleanLower, p.substr) {
			report.IsSafe = false
			report.RiskType = "cloud_sync"
			report.Warning = fmt.Sprintf("数据目录位于云同步服务 (%s) 同步路径中，多端异步同步易破坏 SQLite WAL 造成数据库损坏，建议迁移到纯本地目录。", p.name)
			return report
		}
	}

	// 2. FUSE / Network mount heuristics
	fusePatterns := []string{
		"/volumes/fuse",
		".fuse_hidden",
		"/gvfs/",
	}
	for _, p := range fusePatterns {
		if strings.Contains(cleanLower, p) {
			report.IsSafe = false
			report.RiskType = "fuse_network"
			report.Warning = "数据目录位于 FUSE 或网络文件系统，可能不支持 POSIX advisory locking，存在数据库页损坏风险。"
			return report
		}
	}

	// 3. Check directory writability / accessibility
	testFile := filepath.Join(dir, fmt.Sprintf(".safety_probe_%d", os.Getpid()))
	if f, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600); err != nil {
		report.IsSafe = false
		report.RiskType = "unwritable"
		report.Warning = fmt.Sprintf("数据目录不可写或无权访问: %v", err)
		return report
	} else {
		_ = f.Close()
		_ = os.Remove(testFile)
	}

	// Look for lingering .fuse_hidden files in the data directory
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".fuse_hidden") {
				report.IsSafe = false
				report.RiskType = "fuse_network"
				report.Warning = "数据目录检测到残留的 .fuse_hidden 文件，表明此前曾通过 FUSE 挂载或有未关闭的挂载句柄，存在 WAL 锁异常风险。"
				return report
			}
		}
	}

	return report
}

// isExistingDir reports whether path exists and is a directory.
func isExistingDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
