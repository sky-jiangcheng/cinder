package platform

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GetDataDir returns the user data directory that holds the database.
func GetDataDir() string {
	return filepath.Dir(GetDbPath())
}

// CapturePendingPath is the JSONL file of failed SessionEnd auto-captures.
// It lives in the data dir, never in the knowledge layer, so a failed hook
// leaves a durable trace the next repotory_context can surface.
func CapturePendingPath() string {
	return filepath.Join(GetDataDir(), "capture-pending.jsonl")
}

// AppendCapturePending records one failed capture with cwd, time and error.
func AppendCapturePending(cwd, errMsg string) error {
	path := CapturePendingPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	line, err := json.Marshal(map[string]string{
		"cwd":   cwd,
		"time":  time.Now().Format(time.RFC3339),
		"error": errMsg,
	})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// CountCapturePending returns how many uncleared failure records exist.
func CountCapturePending() int {
	f, err := os.Open(CapturePendingPath())
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			n++
		}
	}
	return n
}

// ClearCapturePending removes the pending file after a successful capture.
func ClearCapturePending() {
	_ = os.Remove(CapturePendingPath())
}
