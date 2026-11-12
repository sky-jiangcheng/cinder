// Package writer implements the cross-process single-writer contract of
// ADR-0058 (TODO 13.1): at most one Cinder process writes the shared SQLite
// file, and the others hand their writes to it over a local channel.
//
// Why a channel and not a lock. Three entry points open one database file
// (desktop, cinder-mcp, cinder-server). SQLite serialises them with
// busy_timeout, so a long write (a snapshot, a batch compile, a vector
// backfill) turns another process's tool call into "database is locked".
// Busy timeouts do not decide WHO may write; this package does:
//
//   - an OS-level file lock elects one writer per data directory;
//   - the writer publishes a loopback endpoint + token next to the database;
//   - a process that did not win the lock forwards its writes there;
//   - when the writer disappears (crash, quit) the next write re-elects, so
//     a headless install keeps working without the desktop app.
//
// This package knows transport and election only. The catalogue of callable
// write methods lives in internal/writerapi, which is what both sides import.
package writer

import (
	"errors"
	"os"
)

// errWouldBlock is the platform shim's "someone else holds it right now"
// signal, folded into one sentinel so TryLock reads the same on every OS.
var errWouldBlock = errors.New("writer lock would block")

// Lock is the held election lock. Closing it releases the lock and removes
// the endpoint publication, which is what makes "the writer vanished" visible
// to every other process on its next write.
type Lock struct {
	f    *os.File
	held bool
}

// ErrLockedByAnotherProcess is what TryLock reports when some other process
// already holds the election lock. It is not a failure to report to a user:
// it is the signal that this process is a client, not the writer.
var ErrLockedByAnotherProcess = errors.New("another Cinder process holds the write lock")

// TryLock takes the election lock in dir without waiting. The lock is owned by
// the open file description, so a process that dies (SIGKILL included)
// releases it at once — that is the whole recovery story behind ADR-0058's
// "another writer resumes within seconds".
func TryLock(dir string) (*Lock, error) {
	path := lockPath(dir)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockFileExclusive(f); err != nil {
		f.Close() //nolint:errcheck // nothing to clean up beyond the handle
		if errors.Is(err, errWouldBlock) {
			return nil, ErrLockedByAnotherProcess
		}
		return nil, err
	}
	// Publish who we are: a reader of the lock file can name the process that
	// is blocking it, which turns "database is locked" into "pid 1234 holds it".
	// Truncate first — the file persists across holders, and without it a
	// previous, longer tag would leave its tail behind.
	if err := f.Truncate(0); err != nil {
		f.Close() //nolint:errcheck
		return nil, err
	}
	if _, err := f.Write(append([]byte("cinder-writer\n"), append([]byte(procTag()), '\n')...)); err != nil {
		f.Close() //nolint:errcheck
		return nil, err
	}
	return &Lock{f: f, held: true}, nil
}

// Close releases the lock. Idempotent: the client calls it after a voluntary
// hand-off too, where releasing is the point of the call.
func (l *Lock) Close() error {
	if l == nil || !l.held {
		return nil
	}
	l.held = false
	err := unlockAndClose(l.f)
	// The file itself stays: it is the rendezvous point, and removing it would
	// let a new process create a second one while others still hold the old
	// inode (the classic unlink-then-recreate lock race).
	return err
}

// Held reports whether this handle still owns the lock. Kept as a method so
// callers can ask after a hand-off without racing on an external flag.
func (l *Lock) Held() bool { return l != nil && l.held }

// LockPath is the file the election lock lives in. Exported so a test can
// assert both processes are contending for the same inode rather than for two
// files that happen to share a name.
func LockPath(dir string) string { return lockPath(dir) }
