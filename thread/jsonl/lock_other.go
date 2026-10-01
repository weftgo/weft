//go:build !unix && !windows

package jsonl

import (
	"errors"
	"fmt"
	"os"
)

// lockSupported reports that this platform has no advisory file lock:
// Open refuses to run without thread.NoLock, so the one-writer rule is
// never dropped silently.
const lockSupported = false

// lockFile has no lock to take here. It is unreachable through Open —
// a backend on this platform exists only with NoLock, which never
// calls it — and fails loudly if that ever changes.
func lockFile(_ *os.File, id string) error {
	return fmt.Errorf("jsonl: no file lock on this platform for session %s: %w", id, errors.ErrUnsupported)
}

// unlockFile mirrors lockFile: nothing to release.
func unlockFile(_ *os.File) error { return nil }
