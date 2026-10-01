package thread_test

import (
	"context"
	"testing"

	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/thread/threadtest"
)

// abandon ends the writer's hold on a session without closing any
// Session — what the writer's process dying does to its lease. It is
// how a test stands in a fresh process: a Session opened afterwards
// on the same Storage value is the session's one writer, where
// without it the Session left behind would still be (ErrLocked). The
// Session left behind is not sealed: its reads keep working, and a
// write through it is refused once the new one has written.
func abandon(t testing.TB, st thread.Storage, id string) {
	t.Helper()
	r, ok := st.(thread.Releaser)
	if !ok {
		return
	}
	if err := r.Release(context.Background(), id); err != nil {
		t.Fatalf("release %s: %v", id, err)
	}
}

// One Session per session id per Storage value writes — the
// in-process half of the one-writer rule (ADR 0011 §5): a second
// Session on the same Storage value opens and reads, and every write
// it makes is refused with ErrLocked until the first one closes. The
// rows are threadtest's Session-level lease table (RunOneWriter), here
// on the reference backend and the file backend; sqlite runs them in
// its own module.
func TestSecondOpenIsLocked(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		threadtest.RunOneWriter(t, func(*testing.T) thread.Storage { return thread.Memory() })
	})
	t.Run("jsonl", func(t *testing.T) {
		threadtest.RunOneWriter(t, func(t *testing.T) thread.Storage {
			st, err := jsonl.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			return st
		})
	})
}
