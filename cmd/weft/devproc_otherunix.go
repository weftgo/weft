//go:build !windows && !linux

package main

import "syscall"

// deathSignal has no portable form outside Linux (no Pdeathsig): a
// SIGKILL of weft dev orphans the app's process group there.
func deathSignal(*syscall.SysProcAttr) {}
