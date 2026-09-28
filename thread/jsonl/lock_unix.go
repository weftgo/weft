//go:build unix

package jsonl

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"github.com/weftgo/weft/thread"
)

// lockFile takes the session's advisory lock exclusively and without
// blocking — the one-writer rule's teeth (ADR 0011 §5). The lock is
// held on the open file description until unlockFile or process exit,
// so a writer that crashes never leaves a session stranded.
func lockFile(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("%w: %s", thread.ErrLocked, f.Name())
		}
		return err
	}
	return nil
}

// unlockFile releases the advisory lock before the file closes.
func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
