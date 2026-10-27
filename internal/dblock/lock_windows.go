//go:build windows

package dblock

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/sys/windows"
)

// lockRange is the locked byte: far past the holder JSON, so a
// refused start can still read the file (a Windows lock is mandatory
// on the bytes it covers).
func lockRange() *windows.Overlapped { return &windows.Overlapped{OffsetHigh: 0x40000000} }

// openNoFollow: no such open flag here; Acquire's check after the open
// is the guard.
const openNoFollow = 0

// singleLink: no link count is checked here (Acquire's doc says why).
func singleLink(fs.FileInfo) bool { return true }

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

// checkOwner: Windows skips the owner check (the file lives beside the
// database, under the user's own ACLs); the url and pid rules still
// apply.
func checkOwner(fs.FileInfo) error { return nil }

// alive reports whether pid runs: it opens and has not exited.
func alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if windows.GetExitCodeProcess(h, &code) != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}
