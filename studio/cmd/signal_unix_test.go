//go:build !windows

package main

import (
	"os"
	"syscall"
	"testing"
)

// signalSelf delivers sig to this test process: serveOn's
// signal.Notify catches it before the default disposition. Unix only
// (signal_windows_test.go skips): Windows has no kill(2).
func signalSelf(t *testing.T, sig syscall.Signal) {
	t.Helper()
	if err := syscall.Kill(os.Getpid(), sig); err != nil {
		t.Fatal(err)
	}
}

// skipWithoutSelfSignal is a no-op where signalSelf works.
func skipWithoutSelfSignal(*testing.T) {}
