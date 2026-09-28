//go:build !unix

package jsonl

import "os"

// lockFile is the platform without advisory file locks: there is no
// flock to take, so cross-process exclusion is unenforced — the
// instance mutex still serializes writers within one process, and the
// documented contract (one writer per session) is all that protects a
// shared directory. The unix build carries the real lock.
func lockFile(f *os.File) error { return nil }

// unlockFile mirrors lockFile: nothing to release.
func unlockFile(f *os.File) error { return nil }
