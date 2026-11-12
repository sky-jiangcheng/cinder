package platform

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// maxLogBytes is the size at which a log file is rotated. One previous file is
// kept, so a component's on-disk logs stay bounded near 2x this value.
const maxLogBytes = 5 << 20 // 5 MiB

var setupLoggingOnce sync.Once

// SetupLogging installs one JSON slog handler for the whole process and
// bridges the standard library log package to it: slog.SetDefault also makes
// log.Print/Printf flow through the same handler, so the existing log.Printf
// calls need no edits. It is idempotent: a second call is a no-op, which keeps
// tests that call it repeatedly safe.
//
// stdout is never a log target. The MCP entry point speaks JSON-RPC on stdout,
// and one stray log line there corrupts the protocol stream; logs go to a
// per-component file (toFile) or to stderr (otherwise, and on any file error).
func SetupLogging(component string, toFile bool) {
	setupLoggingOnce.Do(func() {
		installLogging(component, toFile, filepath.Dir(GetLogPath()), os.Stderr)
	})
}

// installLogging wires the sink into slog. dir/fallback are parameters so
// tests can exercise both the file and the degraded-stderr paths without
// touching the real config directory.
func installLogging(component string, toFile bool, dir string, fallback io.Writer) io.Writer {
	w := newLogWriter(component, toFile, dir, fallback)
	logger := slog.New(slog.NewJSONHandler(w, nil))
	if component != "" {
		logger = logger.With(slog.String("component", component))
	}
	// SetDefault also bridges the standard library log package to the handler.
	slog.SetDefault(logger)
	return w
}

// newLogWriter returns the log sink, degrading to fallback on any file error
// so a read-only or missing log directory can never stop an entry point.
func newLogWriter(component string, toFile bool, dir string, fallback io.Writer) io.Writer {
	if !toFile {
		return fallback
	}
	rw, err := newRotatingFile(filepath.Join(dir, logFileName(component)), maxLogBytes, fallback)
	if err != nil {
		return fallback
	}
	return rw
}

// logFileName gives each component its own file. The desktop process keeps the
// historical repotory.log name; headless entry points get a suffix.
func logFileName(component string) string {
	if component == "" || component == "desktop" {
		return dirName + ".log"
	}
	return dirName + "-" + component + ".log"
}

// rotatingFile is a size-rotating io.Writer that keeps one backup file.
type rotatingFile struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	fallback io.Writer
	file     *os.File
	size     int64
}

func newRotatingFile(path string, maxBytes int64, fallback io.Writer) (*rotatingFile, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &rotatingFile{path: path, maxBytes: maxBytes, fallback: fallback, file: f, size: info.Size()}, nil
}

func (w *rotatingFile) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil && w.size+int64(len(p)) > w.maxBytes {
		// A failed rotation degrades this and all later writes to fallback;
		// logging must never take the app down.
		_ = w.rotate()
	}
	if w.file == nil {
		return w.fallback.Write(p)
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate keeps exactly one previous file, overwriting path+".1" each time.
func (w *rotatingFile) rotate() error {
	_ = w.file.Close()
	w.file = nil
	old := w.path + ".1"
	_ = os.Remove(old) // a stale backup must not block the rename
	if err := os.Rename(w.path, old); err != nil {
		return err
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	w.file = f
	w.size = 0
	return nil
}
