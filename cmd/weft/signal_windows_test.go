//go:build windows

package main

import (
	"syscall"
	"testing"
)

// signalSelf has no Windows form (no kill(2)); the tests that need it
// skip first through skipWithoutSelfSignal.
func signalSelf(t *testing.T, _ syscall.Signal) {
	t.Helper()
	t.Skip("signalling this process needs kill(2)")
}

// skipWithoutSelfSignal skips a test that stops its server by
// signalling this process, before it starts anything.
func skipWithoutSelfSignal(t *testing.T) {
	t.Helper()
	t.Skip("signalling this process needs kill(2)")
}
