package writerapi

import (
	"errors"
	"fmt"
	"log"
	"time"

	"repotory/internal/writer"
)

// Election is the writer election, taken BEFORE the database is opened.
//
// ADR-0058 (TODO 13.7): whether this process may write the shared database at
// open time depends on the role, and the role comes from the election lock —
// which lives beside the database file and needs no database to answer. So the
// election moves ahead of the open: the winner opens the database for writing
// and owns the schema; every other process opens it read-only and writes
// nothing at all.
//
// The lock is held from here on. The process that won it is the writer until it
// gives the role up (a desktop asking for it) or dies — the kernel releases
// the lock on death, which is what lets a peer take over.
type Election struct {
	dir  string
	lock *writer.Lock
}

// Dir is the directory holding the election files.
func (e *Election) Dir() string { return e.dir }

// Won reports whether this process holds the writer lock.
func (e *Election) Won() bool { return e != nil && e.lock != nil }

// Elect runs the election without touching the database.
//
// A desktop (AssertWriter) additionally asks a self-elected agent writer to
// yield and takes the role over. The wait is bounded, and a busy writer is
// left alone — the desktop then keeps forwarding, which the contract allows.
func Elect(opts Options) (*Election, error) {
	dir := writer.DirOf(opts.DBPath)
	lock, err := tryElect(dir, opts.SelfElectAllowed)
	switch {
	case err == nil:
		return &Election{dir: dir, lock: lock}, nil
	case errors.Is(err, writer.ErrLockedByAnotherProcess):
		if opts.AssertWriter {
			lock, perr := preemptLock(dir)
			switch {
			case perr == nil:
				return &Election{dir: dir, lock: lock}, nil
			case errors.Is(perr, writer.ErrLockedByAnotherProcess):
				// Still held: stay a client and forward.
			default:
				return nil, fmt.Errorf("writer: take election lock in %s: %w", dir, perr)
			}
		}
		return &Election{dir: dir}, nil
	default:
		// The lock file could not be created: the data directory is unusable,
		// which is the same diagnosis every later DB write would report.
		return nil, fmt.Errorf("writer: take election lock in %s: %w", dir, err)
	}
}

// preemptLock asks a self-elected agent writer to yield and then waits for the
// lock.
//
// Bounded and best-effort on purpose. If the agent is mid-scan the request is
// refused and the desktop keeps running as a client: the single-writer contract
// still holds, and "a human opened the app, so abandon the agent's long job"
// would be the worse product. A desktop that cannot take over is not a failure
// to report to the user beyond one log line.
func preemptLock(dir string) (*writer.Lock, error) {
	for attempt := 0; attempt < 3; attempt++ {
		ep, err := writer.ReadEndpoint(dir)
		if err != nil {
			return nil, err
		}
		if err := askYield(ep); err != nil {
			log.Printf("write path: Cinder Desktop asked %s to yield and was refused: %v", ep.Process, err)
		}
		// Wait on the lock, not on the endpoint file: the lock is the truth, and
		// the writer unpublishes before it closes.
		for tick := 0; tick < 25; tick++ {
			lock, err := tryElect(dir, true)
			if err == nil {
				log.Printf("write path: Cinder Desktop took the writer role from %s (pid %d)", ep.Process, ep.PID)
				return lock, nil
			}
			if !errors.Is(err, writer.ErrLockedByAnotherProcess) {
				return nil, err
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	log.Printf("write path: the current writer is busy, so Cinder Desktop keeps forwarding (ADR-0058)")
	return nil, writer.ErrLockedByAnotherProcess
}

// tryElect takes the lock only if this process is allowed to become the
// writer; otherwise the lock simply stays with whoever holds it.
func tryElect(dir string, selfElectAllowed bool) (*writer.Lock, error) {
	if !selfElectAllowed {
		return nil, writer.ErrLockedByAnotherProcess
	}
	return writer.TryLock(dir)
}
