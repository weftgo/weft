//go:build !windows

package main

import (
	"errors"
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

// errNoGroup is a stop refused: the app has no process group of its
// own to signal.
var errNoGroup = errors.New("weft dev: the app has no process group of its own")

// group is the process group a stop signals: the app's own (Setpgid
// made its pid its pgid). Never a pgid ≤ 1 — kill(-0) is this
// process's own group, kill(-1) every process this user may signal —
// nor this process's own group (with no job control, a shell's or a
// test runner's), whatever pid the child holds.
func (c *child) group() (int, bool) {
	if c.pid <= 1 || c.pid == syscall.Getpgrp() {
		return 0, false
	}
	return c.pid, true
}

// signal sends sig to the app's process group.
func (c *child) signal(sig os.Signal) error {
	s, ok := sig.(syscall.Signal)
	if !ok {
		s = syscall.SIGTERM
	}
	g, ok := c.group()
	if !ok {
		return errNoGroup
	}
	return syscall.Kill(-g, s)
}

// kill sends SIGKILL to the app's process group.
func (c *child) kill() {
	if g, ok := c.group(); ok {
		_ = syscall.Kill(-g, syscall.SIGKILL)
	}
}

// groupAlive reports whether any process of the app's group remains.
func (c *child) groupAlive() bool {
	g, ok := c.group()
	return ok && syscall.Kill(-g, 0) == nil
}

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
