//go:build windows

package jsonl

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"github.com/weftgo/weft/thread"
)

// lockSupported reports that this platform has the lock the one-writer
// rule needs.
const lockSupported = true

// The LockFileEx flags and the two errors a held lock answers with
// (winbase.h, winerror.h) — spelled here because the thread module
// imports the standard library only, and syscall does not name them.
const (
	lockfileFailImmediately = 0x00000001
	lockfileExclusiveLock   = 0x00000002

	errorLockViolation syscall.Errno = 33
	errorIOPending     syscall.Errno = 997
)

// lockRegion is the one byte the writer's lock covers: far beyond any
// real session's end (2^62). Windows byte-range locks are mandatory —
// a locked range cannot be read through another handle — so locking
// the file's data would lock readers out, and readers never lock. A
// byte no session will ever reach excludes other writers and nothing
// else.
func lockRegion() *syscall.Overlapped {
	return &syscall.Overlapped{OffsetHigh: 1 << 30}
}

// lockFile takes the session's writer lock exclusively and without
// blocking — the one-writer rule's teeth (ADR 0011 §5), the Windows
// counterpart of the unix flock. The lock belongs to the file handle
// and dies with it, so a writer that crashes never leaves a session
// stranded. A held lock is ErrLocked naming the session.
func lockFile(f *os.File, id string) error {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")
	if err := proc.Find(); err != nil {
		return err
	}
	r, _, err := proc.Call(f.Fd(),
		lockfileExclusiveLock|lockfileFailImmediately, 0, 1, 0,
		uintptr(unsafe.Pointer(lockRegion())))
	if r != 0 {
		return nil
	}
	if errors.Is(err, errorLockViolation) || errors.Is(err, errorIOPending) {
		return fmt.Errorf("%w: %s", thread.ErrLocked, id)
	}
	return err
}

// unlockFile releases the writer lock before the file closes.
func unlockFile(f *os.File) error {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("UnlockFileEx")
	if err := proc.Find(); err != nil {
		return err
	}
	r, _, err := proc.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(lockRegion())))
	if r != 0 {
		return nil
	}
	return err
}
