//go:build unix

package threadtest

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/weftgo/weft/thread"
)

// dieAfterQueuedStorage is a writer that dies right after the steer's
// queued receipt lands: every later append is lost.
type dieAfterQueuedStorage struct {
	thread.Storage
	mu   sync.Mutex
	dead atomic.Bool
}

func (d *dieAfterQueuedStorage) Append(ctx context.Context, session string, entries ...thread.Entry) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dead.Load() {
		return errors.New("the writer is dead")
	}
	if err := d.Storage.Append(ctx, session, entries...); err != nil {
		return err
	}
	for _, e := range entries {
		if r, ok := e.(thread.ReceiptEntry); ok && r.Status == thread.ReceiptQueued {
			d.dead.Store(true)
		}
	}
	return nil
}

// The clear_queue point's third crash state: the writer dies right
// after the steer's queued receipt — before ClearQueue's drop and
// before the drain's message reached the tree. The reopen restores the
// steer (its Send was accepted, T4), ClearQueue drops exactly it, and
// the continued turn runs without it. The matrix meets this timing
// only by chance (the nightly soak did); this cuts at it.
func TestClearQueueCrashRightAfterTheQueuedReceipt(t *testing.T) {
	ctx := context.Background()
	live := &dieAfterQueuedStorage{Storage: thread.Memory()}
	crashMatrixSteerSession(live) // the live session stays blocked in its model, as the child did
	if !live.dead.Load() {
		t.Fatal("the steer's queued receipt never landed")
	}
	// The next process: a fresh storage holding exactly what landed —
	// no lease of the dead writer's.
	h, entries, _, err := live.Load(ctx, CrashMatrixSteerID)
	if err != nil {
		t.Fatal(err)
	}
	st := thread.Memory()
	if err := st.Create(ctx, h); err != nil {
		t.Fatal(err)
	}
	if err := st.Append(ctx, CrashMatrixSteerID, entries...); err != nil {
		t.Fatal(err)
	}
	assertCrashPoint(t, "clear_queue", st)
}
