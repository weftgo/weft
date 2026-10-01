//go:build darwin

package sqlite

import (
	"strconv"

	"golang.org/x/sys/unix"
)

// procStart returns a token for when the process wearing pid started —
// its kinfo_proc start time, to the microsecond — or "" when the
// kernel will not say. Compared for equality only: the lock's proof
// that a live pid is still the process that took the lock.
func procStart(pid int) string {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil {
		return ""
	}
	tv := kp.Proc.P_starttime
	if tv.Sec == 0 && tv.Usec == 0 {
		return ""
	}
	return strconv.FormatInt(tv.Sec, 10) + "." + strconv.FormatInt(int64(tv.Usec), 10)
}
