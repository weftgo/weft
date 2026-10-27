//go:build !unix && !windows

package dblock

import (
	"io/fs"
	"os"
)

// No OS lock here (wasm, plan9): the rule is not enforced.
// openNoFollow: no such open flag here; Acquire's check after the open
// is the guard.
const openNoFollow = 0

func lockFile(*os.File) error   { return nil }
func unlockFile(*os.File) error { return nil }

func checkOwner(fs.FileInfo) error { return nil }
func alive(pid int) bool           { return pid > 0 }
