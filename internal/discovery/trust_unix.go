//go:build unix

package discovery

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// openFlags opens without blocking: a FIFO swapped in after the Lstat
// cannot hang the reader.
const openFlags = syscall.O_NONBLOCK

// checkFile is the unix trust rule: mode 0600 and owned by this user.
// A file from a git checkout (0644) or another user's is never trusted.
func checkFile(fi fs.FileInfo) error {
	if perm := fi.Mode().Perm(); perm != 0o600 {
		return fmt.Errorf("mode %#o, not 0600", perm)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("owner unknown")
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("owned by uid %d, not this user (%d)", st.Uid, os.Getuid())
	}
	return nil
}

// alive reports whether pid names a running process: signal 0 is
// delivered (or refused for permission — the process exists).
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
