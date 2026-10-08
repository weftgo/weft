//go:build windows

package main

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// ownGroup is a no-op on Windows: there is no process group to signal.
// A stop is best effort — taskkill /T ends the app's process tree.
func ownGroup(*exec.Cmd) {}

// signal ends the app's process tree: Windows has no SIGTERM to send.
func (c *child) signal(os.Signal) error {
	c.kill()
	return nil
}

// kill ends the app's process tree (taskkill /T /F), else the process.
func (c *child) kill() {
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(c.pid)).Run(); err != nil {
		_ = c.cmd.Process.Kill()
	}
}

// groupAlive has no group to ask about on Windows.
func (c *child) groupAlive() bool { return false }

// devSignals are the signals that stop weft dev (the app first).
var devSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}

// appSignal is the signal the app is stopped with (Windows ends the
// tree whatever it is).
func appSignal(sig os.Signal) os.Signal { return sig }
