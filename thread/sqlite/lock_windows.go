//go:build windows

package sqlite

import (
	"golang.org/x/sys/windows"
)

// stillActive is the exit-code sentinel GetExitCodeProcess reports for
// a running process — 259, the value x/sys/windows does not name.
const stillActive = 259

// pidAlive reports whether pid names a live process on this machine —
// the question the lock row's takeover asks. OpenProcess fails for a
// gone pid (or one this user may not open: reads as gone here, the
// conservative direction would strand; but a process this user cannot
// open while sharing this database file is not a configuration this
// lock supports). GetExitCodeProcess still reporting the live sentinel
// means live — with the documented residue that a process which exited
// with exit code 259 reads alive and delays its takeover: conservative,
// never corrupting.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true // cannot ask: read as alive, the safe side
	}
	return code == stillActive
}
