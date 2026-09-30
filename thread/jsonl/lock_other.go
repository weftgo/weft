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

// removeLocked is Delete's release. There is no lock to order against,
// and Windows refuses to remove a file that is still open (Go opens
// without FILE_SHARE_DELETE) — so the handle closes first, then the
// name goes.
func removeLocked(f *os.File, path string) error {
	_ = f.Close()
	return os.Remove(path)
}
