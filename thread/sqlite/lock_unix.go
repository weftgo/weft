//go:build unix

package sqlite

import (
	"errors"
	"syscall"
)

// pidAlive reports whether pid names a live process on this machine —
// the question the lock row's takeover asks. Signal 0 performs no
// delivery: it only checks permission and existence. EPERM is a live
// process this user may not signal; ESRCH is no such process; any
// other failure reads as alive, because ErrLocked is the safe side and
// a false takeover is not.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false // never a holder; guard 0, which would signal the group
	}
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil:
		return true
	case errors.Is(err, syscall.EPERM):
		return true
	case errors.Is(err, syscall.ESRCH):
		return false
	default:
		return true
	}
}
