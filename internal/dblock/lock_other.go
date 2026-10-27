//go:build !unix && !windows

package dblock

import "os"

// No OS lock here (wasm, plan9): the rule is not enforced.
func lockFile(*os.File) error   { return nil }
func unlockFile(*os.File) error { return nil }
