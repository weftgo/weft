//go:build windows

package sqlite

import (
	"errors"
	"strconv"

	"golang.org/x/sys/windows"
)

// stillActive is the exit-code sentinel GetExitCodeProcess reports for
// a running process — 259, the value x/sys/windows does not name.
const stillActive = 259

// pidAlive reports whether pid names a live process on this machine —
// the question the lock row's takeover asks. OpenProcess fails with
// ERROR_INVALID_PARAMETER for a pid no process wears: dead. Any other
// failure — access denied above all: a live process this user may not
// open — reads as alive, because ErrLocked is the safe side and a
// false takeover is not. GetExitCodeProcess still reporting the live
// sentinel means live — with the documented residue that a process
// which exited with exit code 259 reads alive until its last handle
// closes: conservative, never corrupting.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return !errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true // cannot ask: read as alive, the safe side
	}
	return code == stillActive
}

// procStart returns a token for when the process wearing pid started —
// its creation time from GetProcessTimes, in 100ns units — or "" when
// the process cannot be opened or asked. Compared for equality only:
// the lock's proof that a live pid is still the process that took the
// lock.
func procStart(pid int) string {
	if pid <= 0 {
		return ""
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return ""
	}
	return strconv.FormatInt(creation.Nanoseconds(), 10)
}
