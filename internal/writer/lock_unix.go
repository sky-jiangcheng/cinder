//go:build !windows

package writer

import (
	"os"
	"syscall"
)

// lockFileExclusive takes a non-blocking exclusive flock. flock(2) is owned by
// the open file description, so the lock dies with the process — including on
// SIGKILL, which is exactly the recovery case ADR-0058 has to answer.
//
// It is preferred over fcntl/POSIX locks here for one reason: an fcntl lock is
// per (process, file) and is released by ANY close of that file in the process,
// so an unrelated close() elsewhere would silently drop the write contract.
func lockFileExclusive(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return errWouldBlock
		}
		return err
	}
	return nil
}

func unlockAndClose(f *os.File) error {
	// The fd must stay valid for the unlock, so the unlock error cannot
	// shortcut the close: a leaked fd is worse than a lock the OS drops at
	// exit anyway.
	unlockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	closeErr := f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
