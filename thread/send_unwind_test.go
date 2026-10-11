package thread_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
)

// faultStorage fails what its predicates pick: the flush right after
// an Append flushFailAfter matches (once), and every Append failAppend
// matches. onFlushFail runs, under the storage's lock, when the armed
// flush fails — the scenarios arm their crash there.
type faultStorage struct {
	thread.Storage
	mu             sync.Mutex
	flushFailAfter func([]thread.Entry) bool
	failAppend     func([]thread.Entry) bool
	onFlushFail    func(*faultStorage)
	flushArmed     bool
}

func (f *faultStorage) Append(ctx context.Context, session string, entries ...thread.Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAppend != nil && f.failAppend(entries) {
		return errors.New("append refused")
	}
	if err := f.Storage.Append(ctx, session, entries...); err != nil {
		return err
	}
	if f.flushFailAfter != nil && f.flushFailAfter(entries) {
		f.flushFailAfter, f.flushArmed = nil, true
	}
	return nil
}

func (f *faultStorage) Flush(ctx context.Context, session string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.flushArmed {
		f.flushArmed = false
		if f.onFlushFail != nil {
			f.onFlushFail(f)
		}
		return errors.New("fsync failed")
	}
	return nil
}

func hasEntry(es []thread.Entry, match func(thread.Entry) bool) bool {
	for _, e := range es {
		if match(e) {
			return true
		}
	}
	return false
}

func isDeferredReceipt(e thread.Entry) bool {
	r, ok := e.(thread.ReceiptEntry)
	return ok && r.Status == thread.ReceiptDeferred
}

func isDroppedReceipt(e thread.Entry) bool {
	r, ok := e.(thread.ReceiptEntry)
	return ok && r.Status == thread.ReceiptDropped
}

func isMessage(e thread.Entry) bool {
	_, ok := e.(thread.MessageEntry)
	return ok
}

func countText(texts []string, want string) int {
	n := 0
	for _, s := range texts {
		if s == want {
			n++
		}
	}
	return n
}

func entryByID(s *thread.Session, id string) (thread.Entry, bool) {
	for _, e := range s.Entries() {
		if m, ok := e.(thread.MessageEntry); ok && m.ID == id {
			return e, true
		}
	}
	return nil, false
}

// A steer settled at its turn's end whose deferral was written but not
// flushed was accepted by a Send that succeeded: it is the session's
// to keep. The written receipts stay on the line and the follow-up
// runs under the turn the accepted receipt names — so a writer that
// dies before the follow-up starts restores it on a reopen, and one
// that lives runs it once, a later reopen restoring nothing.
func TestSteerDeferralFlushFailureKeepsTheFollowUp(t *testing.T) {
	steerAgent := func() (*core.Agent, chan struct{}, *release) {
		tool, rel := blockingTool()
		started := make(chan struct{})
		var once sync.Once
		agent := core.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "wait"}),
			wefttest.Say("followed"),
		), tool, core.StopWhen(core.HasToolCall("wait")), core.Tap(func(_ context.Context, ev core.Event) {
			if _, ok := ev.(core.ToolStart); ok {
				once.Do(func() { close(started) })
			}
		}))
		return agent, started, rel
	}
	// steerThenEnd holds a turn, steers into it, arms st and lets the
	// turn end at its StopWhen — the steer defers there.
	steerThenEnd := func(t *testing.T, st *faultStorage, agent *core.Agent, started chan struct{}, rel *release) (*thread.Session, *thread.Turn) {
		t.Helper()
		ctx := context.Background()
		s, err := thread.Create(ctx, st, agent)
		if err != nil {
			t.Fatal(err)
		}
		first, err := s.Send(ctx, core.User("long work"))
		if err != nil {
			t.Fatal(err)
		}
		<-started
		steer, err := s.Send(ctx, core.User("steer me"), thread.As(thread.Steer))
		if err != nil {
			t.Fatal(err)
		}
		st.mu.Lock()
		st.flushFailAfter = func(es []thread.Entry) bool { return hasEntry(es, isDeferredReceipt) }
		st.mu.Unlock()
		rel.open()
		if _, err := first.Wait(); err != nil {
			t.Fatal(err)
		}
		if _, err := steer.Wait(); err != nil || steer.Outcome() != thread.TurnDeferred || steer.Next() == nil {
			t.Fatalf("steer = %v, %v, next %v; want deferred with a follow-up", steer.Outcome(), err, steer.Next())
		}
		return s, steer
	}

	t.Run("crash_before_the_follow_up", func(t *testing.T) {
		ctx := context.Background()
		agent, started, rel := steerAgent()
		defer rel.open()
		st := &faultStorage{Storage: thread.Memory()}
		var dead atomic.Bool
		st.onFlushFail = func(f *faultStorage) {
			// The writer dies at the follow-up's prompt: nothing it
			// writes from then on lands.
			f.failAppend = func(es []thread.Entry) bool {
				if hasEntry(es, isMessage) {
					dead.Store(true)
				}
				return dead.Load()
			}
		}
		s, steer := steerThenEnd(t, st, agent, started, rel)
		follow := steer.Next()
		if _, err := follow.Wait(); err == nil {
			t.Fatal("the follow-up ran on a dead storage")
		}
		_ = s.WaitIdle(ctx)
		again := reopenWith(t, ctx, st.Storage, s, agent)
		q := again.Queue()
		if len(q) != 1 || q[0].Msg.Text() != "steer me" || q[0].Receipt != follow.ID() {
			t.Fatalf("the reopened Queue = %+v, want the steer's follow-up %q", q, follow.ID())
		}
	})

	t.Run("the_follow_up_runs_once", func(t *testing.T) {
		ctx := context.Background()
		agent, started, rel := steerAgent()
		defer rel.open()
		st := &faultStorage{Storage: thread.Memory()}
		st.onFlushFail = func(f *faultStorage) {
			// A drop of the follow-up's receipt cannot land: one that
			// were attempted would leave the receipt live behind a
			// follow-up that ran under another turn.
			f.failAppend = func(es []thread.Entry) bool { return hasEntry(es, isDroppedReceipt) }
		}
		s, steer := steerThenEnd(t, st, agent, started, rel)
		follow := steer.Next()
		if _, err := follow.Wait(); err != nil {
			t.Fatal(err)
		}
		if err := s.WaitIdle(ctx); err != nil {
			t.Fatal(err)
		}
		live := contextTexts(s)
		if countText(live, "steer me") != 1 || countText(live, "followed") != 1 {
			t.Fatalf("Context = %v, want the steer and its answer once", live)
		}
		if _, ok := entryByID(s, follow.ID()); !ok {
			t.Fatalf("no prompt entry under the follow-up's id %q", follow.ID())
		}
		leaf := s.Leaf()
		if err := s.Close(ctx); err != nil {
			t.Fatal(err)
		}
		again := reopenWith(t, ctx, st.Storage, s, agent)
		if q := again.Queue(); len(q) != 0 {
			t.Fatalf("the reopened Queue = %+v, want nothing: the follow-up ran", q)
		}
		if got := contextTexts(again); !equalStrings(got, live) {
			t.Errorf("the reopened Context = %v, want %v", got, live)
		}
		if again.Leaf() != leaf {
			t.Errorf("the reopened Leaf = %q, want %q", again.Leaf(), leaf)
		}
	})
}

// A queued send whose prompt, written by the runner, could not be
// flushed does not run: its receipt is dropped and the leaf goes back
// to where the prompt attached — the same line a failed Send leaves.
func TestQueuedPromptFlushFailureLeavesTheLeaf(t *testing.T) {
	ctx := context.Background()
	st := &faultStorage{Storage: thread.Memory()}
	agent, _, started, rel := heldAgent("answered", "never")
	defer rel.open()
	s, err := thread.Create(ctx, st, agent)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Send(ctx, core.User("long work"))
	if err != nil {
		t.Fatal(err)
	}
	<-started
	queued, err := s.Send(ctx, core.User("queued"))
	if err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	st.flushFailAfter = func(es []thread.Entry) bool {
		return hasEntry(es, func(e thread.Entry) bool { m, ok := e.(thread.MessageEntry); return ok && m.ID == queued.ID() })
	}
	st.mu.Unlock()
	rel.open()
	if _, err := first.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := queued.Wait(); !errors.Is(err, thread.ErrNotRun) {
		t.Fatalf("the queued turn = %v, want ErrNotRun", err)
	}
	if err := s.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	e, ok := entryByID(s, queued.ID())
	if !ok {
		t.Fatal("the unflushed prompt is not in the tree")
	}
	if want := e.(thread.MessageEntry).ParentID; s.Leaf() != want {
		t.Errorf("the leaf after the failed prompt = %q, want it back at %q", s.Leaf(), want)
	}
	if got := contextTexts(s); countText(got, "queued") != 0 {
		t.Errorf("Context = %v: the failed prompt is on the line", got)
	}
	leaf := s.Leaf()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	again := reopenWith(t, ctx, st.Storage, s, agent)
	if q := again.Queue(); len(q) != 0 {
		t.Errorf("the reopened Queue = %+v, want nothing", q)
	}
	if again.Leaf() != leaf {
		t.Errorf("the reopened Leaf = %q, want %q", again.Leaf(), leaf)
	}
}

// An Interrupt that meets only a parked boundary and cannot record its
// denial fails, and the send it had accepted leaves again: its receipt
// is dropped and the leaf goes back to where it stood.
func TestInterruptDenialFailureLeavesTheLeaf(t *testing.T) {
	ctx := context.Background()
	st := &faultStorage{Storage: thread.Memory()}
	agent, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1234"}`}))
	s, err := thread.Create(ctx, st, agent)
	if err != nil {
		t.Fatal(err)
	}
	parkSend(t, s, ctx)
	if err := s.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	leaf := s.Leaf()
	st.mu.Lock()
	st.failAppend = func(es []thread.Entry) bool {
		return hasEntry(es, func(e thread.Entry) bool { _, ok := e.(thread.ApprovalDecisionEntry); return ok })
	}
	st.mu.Unlock()
	if turn, err := s.Send(ctx, core.User("never mind"), thread.As(thread.Interrupt)); err == nil || turn != nil {
		t.Fatalf("an Interrupt whose denial fails = %v, %v; want an error", turn, err)
	}
	if got := s.Leaf(); got != leaf {
		t.Errorf("the leaf after the failed Interrupt = %q, want it back at %q", got, leaf)
	}
	if q := s.Queue(); len(q) != 0 {
		t.Errorf("Queue = %+v, want nothing", q)
	}
	st.mu.Lock()
	st.failAppend = nil
	st.mu.Unlock()
	again := reopenWith(t, ctx, st.Storage, s, agent)
	if q := again.Queue(); len(q) != 0 {
		t.Errorf("the reopened Queue = %+v, want nothing", q)
	}
	if again.Leaf() != leaf {
		t.Errorf("the reopened Leaf = %q, want %q", again.Leaf(), leaf)
	}
}

// A Steer that meets only a parked boundary defers at once; when that
// deferral was written but not flushed the Send fails, and nothing it
// wrote may run: the follow-up's receipt is dropped, the leaf goes back
// to before the steer, and a reopen restores nothing.
func TestSteerAtABoundaryDeferralFlushFailureLeavesTheLeaf(t *testing.T) {
	ctx := context.Background()
	st := &faultStorage{Storage: thread.Memory()}
	agent, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1234"}`}))
	s, err := thread.Create(ctx, st, agent)
	if err != nil {
		t.Fatal(err)
	}
	parkSend(t, s, ctx)
	if err := s.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	leaf := s.Leaf()
	st.mu.Lock()
	st.flushFailAfter = func(es []thread.Entry) bool { return hasEntry(es, isDeferredReceipt) }
	st.mu.Unlock()
	if turn, err := s.Send(ctx, core.User("steer me"), thread.As(thread.Steer)); err == nil || turn != nil {
		t.Fatalf("a Steer whose deferral flush fails = %v, %v; want an error", turn, err)
	}
	if got := s.Leaf(); got != leaf {
		t.Errorf("the leaf after the failed Steer = %q, want it back at %q", got, leaf)
	}
	if q := s.Queue(); len(q) != 0 {
		t.Errorf("Queue = %+v, want nothing", q)
	}
	again := reopenWith(t, ctx, st.Storage, s, agent)
	if q := again.Queue(); len(q) != 0 {
		t.Errorf("the reopened Queue = %+v, want nothing", q)
	}
	if again.Leaf() != leaf {
		t.Errorf("the reopened Leaf = %q, want %q", again.Leaf(), leaf)
	}
}

// commitStorage holds appends until a flush commits them to the
// storage underneath — the durable state a reopen after a crash reads
// (thread.FsyncOnFlush's shape). flushFailAfter arms one failed flush,
// which commits nothing: the entries stay held for the next flush.
type commitStorage struct {
	thread.Storage
	mu             sync.Mutex
	held           []thread.Entry
	flushFailAfter func([]thread.Entry) bool
	flushArmed     bool
}

func (c *commitStorage) Append(_ context.Context, _ string, entries ...thread.Entry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.held = append(c.held, entries...)
	if c.flushFailAfter != nil && c.flushFailAfter(entries) {
		c.flushFailAfter, c.flushArmed = nil, true
	}
	return nil
}

func (c *commitStorage) Flush(ctx context.Context, session string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.flushArmed {
		c.flushArmed = false
		return errors.New("fsync failed")
	}
	if len(c.held) == 0 {
		return nil
	}
	if err := c.Storage.Append(ctx, session, c.held...); err != nil {
		return err
	}
	c.held = nil
	return nil
}

// A failed send's drop and leaf move are one append, flushed: under a
// storage that makes only flushed entries durable, the crash right
// after the failure reopens with the send dropped and the leaf back —
// not with the accepted receipt alone, which would run a send its
// caller was told failed.
func TestFailedSendDropIsFlushedWithTheLeafMove(t *testing.T) {
	ctx := context.Background()
	st := &commitStorage{Storage: thread.Memory()}
	agent, _, started, rel := heldAgent("answered", "never")
	defer rel.open()
	s, err := thread.Create(ctx, st, agent)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Send(ctx, core.User("long work"))
	if err != nil {
		t.Fatal(err)
	}
	<-started
	queued, err := s.Send(ctx, core.User("queued"))
	if err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	st.flushFailAfter = func(es []thread.Entry) bool {
		return hasEntry(es, func(e thread.Entry) bool { m, ok := e.(thread.MessageEntry); return ok && m.ID == queued.ID() })
	}
	st.mu.Unlock()
	rel.open()
	if _, err := first.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := queued.Wait(); !errors.Is(err, thread.ErrNotRun) {
		t.Fatalf("the queued turn = %v, want ErrNotRun", err)
	}
	if err := s.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	leaf := s.Leaf()
	// The crash: what was never flushed is gone.
	again := reopenWith(t, ctx, st.Storage, s, agent)
	if q := again.Queue(); len(q) != 0 {
		t.Fatalf("the reopened Queue = %+v, want nothing: the send failed", q)
	}
	if again.Leaf() != leaf {
		t.Errorf("the reopened Leaf = %q, want %q", again.Leaf(), leaf)
	}
	if got := contextTexts(again); countText(got, "queued") != 0 {
		t.Errorf("the reopened Context = %v: the failed prompt is on the line", got)
	}
}
