//go:build unix

package jsonl

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"github.com/weftgo/weft/thread"
)

// lockSupported reports that this platform has the advisory lock the
// one-writer rule needs.
const lockSupported = true

// lockFile takes the session's advisory lock exclusively and without
// blocking — the one-writer rule's teeth (ADR 0011 §5). The lock is
// held on the open file description until unlockFile, the file's
// close, or process exit, so a writer that crashes never leaves a
// session stranded. A held lock is ErrLocked naming the session.
func lockFile(f *os.File, id string) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("%w: %s", thread.ErrLocked, id)
		}
		return err
	}
	return nil
}

// unlockFile releases the advisory lock before the file closes.
func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
