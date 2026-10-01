package threadtest

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/thread"
)

// writer is a lease holder of the table's own: a pointer nobody else
// has, which is all a holder is.
type writer struct{ name string }

// leaser returns fresh storage and its Leaser capability, skipping the
// row for a backend without it.
func leaser(t *testing.T, open func(t *testing.T) thread.Storage) (thread.Storage, thread.Leaser) {
	t.Helper()
	st := open(t)
	l, ok := st.(thread.Leaser)
	if !ok {
		t.Skipf("%T does not implement thread.Leaser", st)
	}
	return st, l
}

// held creates a session holding n message entries.
func held(t *testing.T, st thread.Storage, id string, n int) thread.Header {
	t.Helper()
	h := header(id)
	if err := st.Create(ctx(), h); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if err := st.Append(ctx(), h.ID, msg("seed")); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

// lockedNaming fails the row unless err is ErrLocked naming the
// session and no filesystem path.
func lockedNaming(t *testing.T, what, id string, err error) {
	t.Helper()
	if !errors.Is(err, thread.ErrLocked) {
		t.Fatalf("%s: err = %v, want ErrLocked", what, err)
	}
	if !strings.Contains(err.Error(), id) || strings.ContainsAny(err.Error(), `/\`) {
		t.Errorf("%s: ErrLocked says %q, want the session id and no path", what, err)
	}
}

// RunLeaser is the conformance sub-table for the Leaser capability —
// the writer lease that keeps two Sessions on one Storage value to one
// writer (ADR 0011 §5). Run calls it for a backend that implements
// thread.Leaser: Acquire is idempotent for its holder and reports the
// session's complete entry lines; a second holder is refused with
// ErrLocked naming the session; Yield hands the lease over and never
// ends another holder's; Release and Delete through the Storage value
// end it; readers are never refused; and the count is the signal a
// stale writer is caught by — it follows every append, and counts
// neither a torn tail nor the header; beside it Acquire reports the
// header's Created, which tells a session created again under its id
// from the one it replaced. The Sessions subtest is
// RunOneWriter: the same rule as two Session values meet it.
func RunLeaser(t *testing.T, open func(t *testing.T) thread.Storage) {
	t.Helper()
	t.Run("AcquireIsIdempotent", func(t *testing.T) {
		st, l := leaser(t, open)
		h := held(t, st, "s_lease", 0)
		a := &writer{"a"}
		for i := 0; i < 3; i++ {
			if n, _, err := l.Acquire(ctx(), h.ID, a); err != nil || n != 0 {
				t.Fatalf("Acquire #%d on a fresh session = %d, %v; want 0", i+1, n, err)
			}
		}
		// The holder's own appends move the count; asking again costs
		// nothing and answers the new length.
		if err := st.Append(ctx(), h.ID, batch(h.ID)...); err != nil {
			t.Fatalf("Append under the lease: %v", err)
		}
		want := len(batch(h.ID))
		for i := 0; i < 2; i++ {
			if n, _, err := l.Acquire(ctx(), h.ID, a); err != nil || n != want {
				t.Fatalf("Acquire after an append = %d, %v; want %d", n, err, want)
			}
		}
		// An empty append is not a line.
		if err := st.Append(ctx(), h.ID); err != nil {
			t.Fatal(err)
		}
		if n, _, err := l.Acquire(ctx(), h.ID, a); err != nil || n != want {
			t.Fatalf("Acquire after an empty append = %d, %v; want %d", n, err, want)
		}
	})
	t.Run("AcquireReportsCreated", func(t *testing.T) {
		st, l := leaser(t, open)
		h := held(t, st, "s_lease_created", 1)
		a := &writer{"a"}
		// The header's Created, to the nanosecond, on the acquire that
		// takes the lease and on every one after it.
		for i := 0; i < 2; i++ {
			_, created, err := l.Acquire(ctx(), h.ID, a)
			if err != nil || !created.Equal(h.Created) {
				t.Fatalf("Acquire #%d reports created %v, %v; want the header's %v", i+1, created, err, h.Created)
			}
		}
		// A session created again under the id is told apart by it.
		if err := st.Delete(ctx(), h.ID); err != nil {
			t.Fatal(err)
		}
		h2 := h
		h2.Created = h.Created.Add(time.Second)
		if err := st.Create(ctx(), h2); err != nil {
			t.Fatal(err)
		}
		_, created, err := l.Acquire(ctx(), h.ID, a)
		if err != nil || !created.Equal(h2.Created) {
			t.Fatalf("Acquire on the recreated session reports created %v, %v; want %v", created, err, h2.Created)
		}
	})
	t.Run("RefusedArguments", func(t *testing.T) {
		st, l := leaser(t, open)
		a := &writer{"a"}
		if _, _, err := l.Acquire(ctx(), "s_missing", a); !errors.Is(err, thread.ErrNotFound) {
			t.Errorf("Acquire on a missing session: err = %v, want ErrNotFound", err)
		}
		if err := l.Yield(ctx(), "s_missing", a); !errors.Is(err, thread.ErrNotFound) {
			t.Errorf("Yield on a missing session: err = %v, want ErrNotFound", err)
		}
		h := held(t, st, "s_lease_args", 1)
		if _, _, err := l.Acquire(ctx(), h.ID, nil); err == nil {
			t.Error("Acquire with a nil holder succeeded")
		}
		if err := l.Yield(ctx(), h.ID, nil); err == nil {
			t.Error("Yield with a nil holder succeeded")
		}
		cctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := l.Acquire(cctx, h.ID, a); !errors.Is(err, context.Canceled) {
			t.Errorf("Acquire under a canceled context: err = %v, want context.Canceled", err)
		}
		if err := l.Yield(cctx, h.ID, a); !errors.Is(err, context.Canceled) {
			t.Errorf("Yield under a canceled context: err = %v, want context.Canceled", err)
		}
		// Neither refusal took the lease: any holder still can.
		if n, _, err := l.Acquire(ctx(), h.ID, &writer{"b"}); err != nil || n != 1 {
			t.Errorf("Acquire after the refusals = %d, %v; want 1", n, err)
		}
	})
	t.Run("SecondHolderIsLocked", func(t *testing.T) {
		st, l := leaser(t, open)
		h := held(t, st, "s_lease_two", 2)
		a, b := &writer{"a"}, &writer{"b"}
		if n, _, err := l.Acquire(ctx(), h.ID, a); err != nil || n != 2 {
			t.Fatalf("Acquire = %d, %v; want 2", n, err)
		}
		for i := 0; i < 2; i++ {
			_, _, err := l.Acquire(ctx(), h.ID, b)
			lockedNaming(t, "a second holder's Acquire", h.ID, err)
		}
		// The refusals changed nothing for the holder.
		if n, _, err := l.Acquire(ctx(), h.ID, a); err != nil || n != 2 {
			t.Fatalf("the holder's Acquire after the refusals = %d, %v; want 2", n, err)
		}
		// Two holders with equal contents are still two holders.
		if _, _, err := l.Acquire(ctx(), h.ID, &writer{"a"}); !errors.Is(err, thread.ErrLocked) {
			t.Errorf("a look-alike holder: err = %v, want ErrLocked", err)
		}
	})
	t.Run("YieldHandsOver", func(t *testing.T) {
		st, l := leaser(t, open)
		h := held(t, st, "s_lease_yield", 1)
		a, b := &writer{"a"}, &writer{"b"}
		if _, _, err := l.Acquire(ctx(), h.ID, a); err != nil {
			t.Fatal(err)
		}
		// A non-holder's Yield ends nothing.
		if err := l.Yield(ctx(), h.ID, b); err != nil {
			t.Fatalf("Yield by a non-holder: %v", err)
		}
		if _, _, err := l.Acquire(ctx(), h.ID, b); !errors.Is(err, thread.ErrLocked) {
			t.Fatalf("a non-holder's Yield freed the lease: err = %v, want ErrLocked", err)
		}
		if err := st.Append(ctx(), h.ID, msg("a writes")); err != nil {
			t.Fatal(err)
		}
		// The holder's does, and is idempotent.
		for i := 0; i < 2; i++ {
			if err := l.Yield(ctx(), h.ID, a); err != nil {
				t.Fatalf("Yield #%d by the holder: %v", i+1, err)
			}
		}
		// The next holder is told what the session now holds.
		if n, _, err := l.Acquire(ctx(), h.ID, b); err != nil || n != 2 {
			t.Fatalf("Acquire after the hand-over = %d, %v; want 2", n, err)
		}
		_, _, err := l.Acquire(ctx(), h.ID, a)
		lockedNaming(t, "the old holder against the new", h.ID, err)
		// Release speaks for the whole Storage value: it ends the
		// lease whoever holds it.
		if r, ok := st.(thread.Releaser); ok {
			if err := r.Release(ctx(), h.ID); err != nil {
				t.Fatal(err)
			}
			if n, _, err := l.Acquire(ctx(), h.ID, a); err != nil || n != 2 {
				t.Fatalf("Acquire after Release = %d, %v; want 2", n, err)
			}
		}
	})
	t.Run("YieldWithoutAHolderReleases", func(t *testing.T) {
		st, l := leaser(t, open)
		// The storage's own hold — Create's, an Append's — with no
		// lease on it is let go by any writer's Yield, as Release
		// would: nothing written is lost, and the session is taken
		// again by whoever writes next.
		h := held(t, st, "s_lease_bare", 2)
		if err := l.Yield(ctx(), h.ID, &writer{"a"}); err != nil {
			t.Fatalf("Yield on an unleased session: %v", err)
		}
		if n, _, err := l.Acquire(ctx(), h.ID, &writer{"b"}); err != nil || n != 2 {
			t.Fatalf("Acquire after it = %d, %v; want 2", n, err)
		}
	})
	t.Run("ReadersAreNeverRefused", func(t *testing.T) {
		st, l := leaser(t, open)
		h := held(t, st, "s_lease_read", 2)
		if _, _, err := l.Acquire(ctx(), h.ID, &writer{"a"}); err != nil {
			t.Fatal(err)
		}
		if _, loaded, report, err := st.Load(ctx(), h.ID); err != nil || report != nil || len(loaded) != 2 {
			t.Errorf("Load under a lease: %d entries, report %+v, err %v", len(loaded), report, err)
		}
		if p, err := st.List(ctx(), thread.Query{}); err != nil || p.Total != 1 {
			t.Errorf("List under a lease: total %d, err %v", p.Total, err)
		}
		if w, ok := st.(thread.Watcher); ok {
			wctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			seq, err := w.Watch(wctx, h.ID, "")
			if err != nil {
				t.Fatalf("Watch under a lease: %v", err)
			}
			got, _ := collect(seq)
			awaitCount(t, got, 2)
		}
	})
	t.Run("DeleteEndsTheLease", func(t *testing.T) {
		st, l := leaser(t, open)
		h := held(t, st, "s_lease_delete", 1)
		a, b := &writer{"a"}, &writer{"b"}
		if _, _, err := l.Acquire(ctx(), h.ID, a); err != nil {
			t.Fatal(err)
		}
		// Through the holder's own Storage value Delete is not
		// refused by the lease, and takes it along.
		if err := st.Delete(ctx(), h.ID); err != nil {
			t.Fatalf("Delete of a leased session: %v", err)
		}
		if _, _, err := l.Acquire(ctx(), h.ID, a); !errors.Is(err, thread.ErrNotFound) {
			t.Errorf("the holder's Acquire after Delete: err = %v, want ErrNotFound", err)
		}
		if err := l.Yield(ctx(), h.ID, a); !errors.Is(err, thread.ErrNotFound) {
			t.Errorf("the holder's Yield after Delete: err = %v, want ErrNotFound", err)
		}
		// The name is free, and so is its lease.
		if err := st.Create(ctx(), h); err != nil {
			t.Fatalf("Create after Delete: %v", err)
		}
		if n, _, err := l.Acquire(ctx(), h.ID, b); err != nil || n != 0 {
			t.Errorf("Acquire on the new session = %d, %v; want 0", n, err)
		}
	})
	t.Run("CountIsTheStaleSignal", func(t *testing.T) {
		st, l := leaser(t, open)
		h := held(t, st, "s_lease_count", 0)
		a, b := &writer{"a"}, &writer{"b"}
		// A writer that loaded the empty session and one that writes
		// three entries and leaves: the first is told 3, not the 0 it
		// saw — which is how it knows its view is stale.
		if n, _, err := l.Acquire(ctx(), h.ID, b); err != nil || n != 0 {
			t.Fatalf("Acquire = %d, %v; want 0", n, err)
		}
		if err := st.Append(ctx(), h.ID, batch(h.ID)...); err != nil {
			t.Fatal(err)
		}
		if err := l.Yield(ctx(), h.ID, b); err != nil {
			t.Fatal(err)
		}
		want := len(batch(h.ID))
		if n, _, err := l.Acquire(ctx(), h.ID, a); err != nil || n != want {
			t.Fatalf("Acquire after another writer's entries = %d, %v; want %d", n, err, want)
		}
		_, loaded, _, err := st.Load(ctx(), h.ID)
		if err != nil || len(loaded) != want {
			t.Fatalf("Load: %d entries, err %v; the count must be what a Load accounts for", len(loaded), err)
		}
		inj, ok := st.(RawInjector)
		if !ok {
			return
		}
		// A torn tail is not a line: the count is the complete ones.
		if err := inj.Inject(ctx(), h.ID, []byte(`{"type":"message","id":"e_t`)); err != nil {
			t.Fatal(err)
		}
		if n, _, err := l.Acquire(ctx(), h.ID, a); err != nil || n != want {
			t.Fatalf("Acquire over a torn tail = %d, %v; want %d", n, err, want)
		}
		// The repair before the next append does not change it; the
		// append does.
		if err := st.Append(ctx(), h.ID, msg("after the tear")); err != nil {
			t.Fatal(err)
		}
		if n, _, err := l.Acquire(ctx(), h.ID, a); err != nil || n != want+1 {
			t.Fatalf("Acquire after the repair and an append = %d, %v; want %d", n, err, want+1)
		}
		// A complete line that does not decode is a line all the
		// same — the one a Load fails on, or skips under Salvage.
		if err := inj.Inject(ctx(), h.ID, []byte("{not json}\n")); err != nil {
			t.Fatal(err)
		}
		if n, _, err := l.Acquire(ctx(), h.ID, a); err != nil || n != want+2 {
			t.Fatalf("Acquire over a malformed line = %d, %v; want %d", n, err, want+2)
		}
	})
	t.Run("Sessions", func(t *testing.T) { RunOneWriter(t, open) })
	t.Run("ConcurrentHoldersOneWins", func(t *testing.T) {
		st, l := leaser(t, open)
		h := held(t, st, "s_lease_race", 1)
		const holders = 8
		var wg sync.WaitGroup
		errs := make([]error, holders)
		start := make(chan struct{})
		for i := 0; i < holders; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				w := &writer{"racer"}
				<-start
				for n := 0; n < 20; n++ { // a holder keeps it; a loser keeps losing
					if _, _, err := l.Acquire(ctx(), h.ID, w); err != nil {
						errs[i] = err
						return
					}
				}
			}()
		}
		close(start)
		wg.Wait()
		won := 0
		for i, err := range errs {
			switch {
			case err == nil:
				won++
			case !errors.Is(err, thread.ErrLocked):
				t.Errorf("holder %d: %v", i, err)
			}
		}
		if won != 1 {
			t.Errorf("%d holders took the lease, want exactly 1", won)
		}
	})
}

// leaseAcrossInstances is RunTwoWriters' lease row: the backend's own
// lock is what a lease on another Storage value meets — ErrLocked —
// and a hand-over through Yield tells the next instance's holder what
// the first one wrote.
func leaseAcrossInstances(t *testing.T, first, second thread.Storage) {
	fl, ok1 := first.(thread.Leaser)
	sl, ok2 := second.(thread.Leaser)
	if !ok1 || !ok2 {
		t.Skipf("%T does not implement thread.Leaser", first)
	}
	h := held(t, first, "s_lease_instances", 1)
	a, b := &writer{"a"}, &writer{"b"}
	if n, _, err := fl.Acquire(ctx(), h.ID, a); err != nil || n != 1 {
		t.Fatalf("Acquire = %d, %v; want 1", n, err)
	}
	_, _, err := sl.Acquire(ctx(), h.ID, b)
	lockedNaming(t, "Acquire on a second Storage", h.ID, err)
	// The same holder through another Storage value is another writer.
	if _, _, err := sl.Acquire(ctx(), h.ID, a); !errors.Is(err, thread.ErrLocked) {
		t.Errorf("the holder through a second Storage: err = %v, want ErrLocked", err)
	}
	// A Yield there ends nothing here.
	if err := sl.Yield(ctx(), h.ID, b); err != nil {
		t.Errorf("Yield on a Storage that holds nothing: %v", err)
	}
	if err := first.Append(ctx(), h.ID, msg("first writes")); err != nil {
		t.Fatalf("the holder's Append: %v", err)
	}
	if err := fl.Yield(ctx(), h.ID, a); err != nil {
		t.Fatal(err)
	}
	if n, _, err := sl.Acquire(ctx(), h.ID, b); err != nil || n != 2 {
		t.Fatalf("Acquire on the second Storage after the hand-over = %d, %v; want 2", n, err)
	}
	if _, _, err := fl.Acquire(ctx(), h.ID, a); !errors.Is(err, thread.ErrLocked) {
		t.Errorf("the first Storage against the new holder: err = %v, want ErrLocked", err)
	}
}
