//go:build !windows

package main

import (
	"errors"
	"syscall"
	"testing"
)

// A stop never signals a group that is not the app's own: not pgid 0
// (kill(-0) is this process's group — with no job control, as on CI,
// the test binary's and the shell's), not -1 (every process), not this
// process's own group whatever pid the child holds. Were it to, the
// SIGTERM would end this test binary here.
func TestDevStopNeverSignalsOwnGroup(t *testing.T) {
	for _, pid := range []int{0, 1, syscall.Getpgrp()} {
		c := &child{pid: pid}
		if err := c.signal(syscall.SIGTERM); !errors.Is(err, errNoGroup) {
			t.Errorf("pid %d: signal = %v, want errNoGroup", pid, err)
		}
		if c.groupAlive() {
			t.Errorf("pid %d: groupAlive = true, want false (no group of its own)", pid)
		}
		c.kill()
	}
}

// A self-signal no code has armed a Notify for yet is absorbed by
// signalSelf's guard: the test that sent it fails on its own wait, and
// the test binary lives — it used to die of the default disposition
// (TestListenShutsDownGracefully's TCP readiness check passed before
// serveOn armed its Notify: CI's "signal: terminated").
func TestSignalSelfWithNoReceiverKillsNothing(t *testing.T) {
	signalSelf(t, syscall.SIGTERM)
	signalSelf(t, syscall.SIGHUP)
}
