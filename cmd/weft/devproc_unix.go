//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// ownGroup puts the app in a process group of its own: a stop signals
// the whole group (`go run` and the binary it built), and the
// terminal's Ctrl-C reaches weft dev only, which stops the app first.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// signal sends sig to the app's process group.
func (c *child) signal(sig os.Signal) error {
	s, ok := sig.(syscall.Signal)
	if !ok {
		s = syscall.SIGTERM
	}
	return syscall.Kill(-c.pid, s)
}

// kill sends SIGKILL to the app's process group.
func (c *child) kill() { _ = syscall.Kill(-c.pid, syscall.SIGKILL) }

// groupAlive reports whether any process of the app's group remains.
func (c *child) groupAlive() bool { return syscall.Kill(-c.pid, 0) == nil }
