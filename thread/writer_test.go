package thread_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"

	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/thread/threadtest"
)

// One Session writes a session. A second Session on the same storage
// opens and reads, but its writes are refused until the writer
// closes — and by then the session has moved on, so the reader opens
// it again to write from what it now holds.
func ExampleSession_Close_handOver() {
	ctx := context.Background()
	st := thread.Memory()
	agent := core.New(wefttest.Script())

	writer, _ := thread.Create(ctx, st, agent)
	_ = writer.SetInfo(ctx, "Order 1234", nil)

	reader, _ := thread.Open(ctx, st, writer.ID(), agent)
	fmt.Println("the reader sees:", reader.Title())
	err := reader.SetInfo(ctx, "Order 1234 (refunded)", nil)
	fmt.Println("locked while the writer is open:", errors.Is(err, thread.ErrLocked))

	_ = writer.SetInfo(ctx, "Order 1234 — shipped", nil)
	_ = writer.Close(ctx)

	err = reader.SetInfo(ctx, "Order 1234 (refunded)", nil)
	fmt.Println("stale after the writer wrote and closed:", errors.Is(err, thread.ErrStale))
	_ = reader.Close(ctx)

	again, _ := thread.Open(ctx, st, writer.ID(), agent)
	fmt.Println("reopened:", again.Title())
	fmt.Println("writes:", again.SetInfo(ctx, "Order 1234 (refunded)", nil))
	// Output:
	// the reader sees: Order 1234
	// locked while the writer is open: true
	// stale after the writer wrote and closed: true
	// reopened: Order 1234 — shipped
	// writes: <nil>
}

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
