//go:build unix

// The crash matrix (plan §10, step 7.1) over sqlite: every
// session-layer write point, each process killed dead at its write and
// reopened — the shared harness in threadtest carries the assertions;
// this file wires it to this backend's storage and re-executed helper.
// The reopen after each kill exercises the lock takeover too: the
// child died holding the session's lock row, and the parent must take
// it over from the dead holder to continue the session.

package sqlite_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/sqlite"
	"github.com/weftgo/weft/thread/threadtest"
)

// TestCrashMatrix walks every write point: the child process dies at
// each (SIGKILL, no cleanup), and the parent reopens the database and
// asserts the point's invariant — the durable prefix Load returns,
// each committed batch all there, and a session that still continues.
func TestCrashMatrix(t *testing.T) {
	dir := t.TempDir()
	threadtest.CrashMatrix(t, "TestCrashMatrixHelper",
		func(point string) string { return filepath.Join(dir, point+".db") },
		func(path string) (thread.Storage, error) { return sqlite.Open(path) })
}

// TestCrashMatrixHelper is the re-executed child; the harness's env
// gates it.
func TestCrashMatrixHelper(t *testing.T) {
	db := os.Getenv("WEFT_THREADTEST_CRASH_STORAGE")
	if db == "" {
		return
	}
	threadtest.RunCrashMatrixChild(t, func() (thread.Storage, error) {
		return sqlite.Open(db)
	})
}
