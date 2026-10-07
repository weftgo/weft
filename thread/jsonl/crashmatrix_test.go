//go:build unix

// The crash matrix over jsonl: every session-layer
// write point, each process killed dead at its write and reopened —
// the shared harness in threadtest carries the assertions; this file
// wires it to this backend's storage and re-executed helper.

package jsonl_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/thread/threadtest"
)

// TestCrashMatrix walks every write point: the child process dies at
// each (SIGKILL, no cleanup), and the parent reopens the directory and
// asserts the point's invariant — the durable prefix Load returns, the
// batch the point wrote all there, and a session that still continues.
func TestCrashMatrix(t *testing.T) {
	dir := t.TempDir()
	threadtest.CrashMatrix(t, "TestCrashMatrixHelper",
		func(point string) string { return filepath.Join(dir, point) },
		func(path string) (thread.Storage, error) { return jsonl.Open(path) })
}

// TestCrashMatrixHelper is the re-executed child; the harness's env
// gates it.
func TestCrashMatrixHelper(t *testing.T) {
	dir := os.Getenv("WEFT_THREADTEST_CRASH_STORAGE")
	if dir == "" {
		return
	}
	threadtest.RunCrashMatrixChild(t, func() (thread.Storage, error) { return jsonl.Open(dir) })
}
