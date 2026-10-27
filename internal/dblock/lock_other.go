//go:build !unix && !windows

package dblock

import (
	"io/fs"
	"os"
)

// No OS lock here (wasm, plan9): the rule is not enforced.
func lockFile(*os.File) error   { return nil }
func unlockFile(*os.File) error { return nil }

func checkOwner(fs.FileInfo) error { return nil }
func alive(pid int) bool           { return pid > 0 }
