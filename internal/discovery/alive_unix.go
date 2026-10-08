//go:build unix

package discovery

import (
	"errors"
	"syscall"
)

// alive reports whether pid names a running process: signal 0 is
// delivered (or refused for permission — the process exists).
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
