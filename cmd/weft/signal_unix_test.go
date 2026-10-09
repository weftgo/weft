//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// signalSelf delivers sig to this test process: serveOn's (or weft
// dev's) signal.Notify catches it before the default disposition. Unix
// only (signal_windows_test.go skips): Windows has no kill(2).
//
// A guard channel is notified for sig across the delivery, so a signal
// sent before the code under test armed its own Notify — a readiness
// check that passed early — is absorbed and the test fails visibly
// (its "never returned" wait), instead of the default disposition
// killing the whole test binary ("signal: terminated", every test's
// result lost). It returns once the signal reached the channels: Go
// hands one signal to every notified channel at once.
func signalSelf(t *testing.T, sig syscall.Signal) {
	t.Helper()
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, sig)
	defer signal.Stop(guard)
	if err := syscall.Kill(os.Getpid(), sig); err != nil {
		t.Fatal(err)
	}
	select {
	case <-guard:
	case <-time.After(10 * time.Second):
		t.Fatalf("%v sent to this process was never delivered", sig)
	}
}

// skipWithoutSelfSignal is a no-op where signalSelf works.
func skipWithoutSelfSignal(*testing.T) {}
