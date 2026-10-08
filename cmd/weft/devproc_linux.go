//go:build linux

package main

import "syscall"

// deathSignal is the backstop when weft dev dies without stopping the
// app (SIGKILL): the kernel sends the app — `go run` itself, not the
// binary it started, which a SIGKILL of weft dev can still orphan —
// SIGTERM.
func deathSignal(a *syscall.SysProcAttr) { a.Pdeathsig = syscall.SIGTERM }
