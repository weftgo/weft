//go:build unix

package dblock

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// openNoFollow refuses a symlink at the lock path at open time.
const openNoFollow = unix.O_NOFOLLOW

func lockFile(f *os.File) error {
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.EWOULDBLOCK), errors.Is(err, unix.EAGAIN):
			return ErrHeld
		}
		return err
	}
}

func unlockFile(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_UN) }

// checkOwner is the unix trust rule for the lock file: mode 0600 and
// owned by this user.
func checkOwner(fi fs.FileInfo) error {
	if perm := fi.Mode().Perm(); perm != 0o600 {
		return fmt.Errorf("mode %#o, not 0600", perm)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Getuid() {
		return errors.New("not this user's")
	}
	return nil
}

// alive reports whether pid runs: signal 0 delivered, or refused for
// permission (the process exists).
func alive(pid int) bool {
	err := unix.Kill(pid, 0)
	return err == nil || errors.Is(err, unix.EPERM)
}
