//go:build linux

package sqlite

import (
	"bytes"
	"os"
	"strconv"
)

// procStart returns a token for when the process wearing pid started,
// or "" when it cannot be read (no such process, or a /proc this
// process may not see). The token is the kernel's boot id plus the
// process's start time in clock ticks since boot (/proc/<pid>/stat,
// field 22): two processes that wear the same pid at different times
// get different tokens, across reboots too. It is compared for
// equality only — the lock's proof that a live pid is still the
// process that took the lock.
func procStart(pid int) string {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	ticks := statStartTime(stat)
	if ticks == "" {
		return ""
	}
	boot, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return string(bytes.TrimSpace(boot)) + ":" + ticks
}

// statStartTime pulls field 22 (starttime) out of a /proc/<pid>/stat
// line. Field 2, the command name, is parenthesised and may itself
// hold spaces and parentheses, so the fields are counted from the last
// ')' — the one that closes it.
func statStartTime(stat []byte) string {
	i := bytes.LastIndexByte(stat, ')')
	if i < 0 {
		return ""
	}
	fields := bytes.Fields(stat[i+1:])
	const startTime = 22 - 3 // fields[0] is field 3, the state
	if len(fields) <= startTime {
		return ""
	}
	return string(fields[startTime])
}
