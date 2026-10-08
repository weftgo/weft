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
	deathSignal(cmd.SysProcAttr)
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

// devSignals are the signals that stop weft dev (the app first): the
// terminal's Ctrl-C, a polite kill, and the terminal closing.
var devSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

// appSignal is the signal the app is stopped with for sig: the same
// one, except SIGHUP — which many servers take as "reload" — becomes
// SIGTERM.
func appSignal(sig os.Signal) os.Signal {
	if sig == syscall.SIGHUP {
		return syscall.SIGTERM
	}
	return sig
}
