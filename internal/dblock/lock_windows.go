//go:build windows

package dblock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockRange is the locked byte: far past the holder JSON, so a
// refused start can still read the file (a Windows lock is mandatory
// on the bytes it covers).
func lockRange() *windows.Overlapped { return &windows.Overlapped{OffsetHigh: 0x40000000} }

func lockFile(f *os.File) error {
	ol := lockRange()
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return ErrHeld
	}
	return err
}

func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, lockRange())
}
