//go:build windows

package writer

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockFileExclusive takes a non-blocking exclusive byte-range lock over the
// whole file. Windows has no flock; LockFileEx is the equivalent, and like
// flock it is released by the OS when the handle closes — so a killed writer
// stops advertising itself just as fast as on the Unix path, which is what the
// recovery budget in ADR-0058 depends on.
func lockFileExclusive(f *os.File) error {
	var overlapped windows.Overlapped
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 0xFFFFFFFF, 0xFFFFFFFF, &overlapped)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return errWouldBlock
	}
	return err
}

func unlockAndClose(f *os.File) error {
	unlockErr := windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 0xFFFFFFFF, 0xFFFFFFFF, &windows.Overlapped{})
	closeErr := f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
