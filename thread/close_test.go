package thread_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// releasing is a Storage that offers the hold-release capability
// Close looks for, counting the calls.
type releasing struct {
	thread.Storage
	released atomic.Int32
	err      error
}

func (r *releasing) Release(_ context.Context, _ string) error {
	r.released.Add(1)
	return r.err
}

// blockingAgent returns an agent whose first turn calls a tool that
// blocks until release is closed — or its context ends — and the
// channel that reports the tool has started.
func blockingAgent(model *wefttest.Model) (agent *weft.Agent, started, release chan struct{}) {
	started = make(chan struct{})
	release = make(chan struct{})
	var once sync.Once
	tool := weft.Tool("block", "Block until released.",
		func(ctx context.Context, _ struct{}) (string, error) {
			once.Do(func() { close(started) })
			select {
			case <-release:
				return "ok", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		})
	return weft.New(model, tool), started, release
}

func waitStarted(t *testing.T, started chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the turn never started its tool")
	}
}

// Close on an idle session seals it: new work and every write fail
// with ErrClosed, reads keep answering, the storage's hold is released
// once, and a second Close is a no-op with the same answer.
func TestCloseIdleSession(t *testing.T) {
	ctx := context.Background()
	st := &releasing{Storage: thread.Memory()}
	model := wefttest.Script(wefttest.Say("hello"))
	s, err := thread.Create(ctx, st, weft.New(model))
	if err != nil {
		t.Fatal(err)
	}
	turn, _ := s.Send(ctx, weft.User("hi"))
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	entries := len(s.Entries())

	if err := s.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if n := st.released.Load(); n != 1 {
		t.Errorf("Release calls = %d, want 1", n)
	}
	for name, call := range map[string]func() error{
		"Send":          func() error { _, err := s.Send(ctx, weft.User("again")); return err },
		"Continue":      func() error { _, err := s.Continue(ctx); return err },
		"Label":         func() error { return s.Label(ctx, turn.ID(), "x") },
		"SetInfo":       func() error { return s.SetInfo(ctx, "title", nil) },
		"empty SetInfo": func() error { return s.SetInfo(ctx, "", nil) },
		"Custom":        func() error { return s.Custom(ctx, "k", nil) },
		"CustomMessage": func() error { return s.CustomMessage(ctx, "k", weft.User("x")) },
		"Branch":        func() error { return s.Branch(ctx, "") },
		"Grant":         func() error { return s.Grant(ctx, thread.Grant{Tool: "t"}) },
		"Compact":       func() error { return s.Compact(ctx) },
		"AppendPoolReceipt": func() error {
			_, err := s.AppendPoolReceipt(ctx, thread.PoolReceiptEntry{Status: thread.PoolAccepted})
			return err
		},
	} {
		if err := call(); !errors.Is(err, thread.ErrClosed) {
			if name == "Compact" && err != nil {
				continue // nothing to compact is an earlier, equally loud refusal
			}
			t.Errorf("%s after Close: err = %v, want ErrClosed", name, err)
		}
	}
	// Reads answer from the tree the session held.
	if got := len(s.Entries()); got != entries {
		t.Errorf("Entries after Close = %d, want %d — nothing written, nothing lost", got, entries)
	}
	if got := contextTexts(s); !equalStrings(got, []string{"hi", "hello"}) {
		t.Errorf("Context after Close = %v", got)
	}
	if s.Storage() != thread.Storage(st) {
		t.Error("Storage after Close is not the storage the session was created on")
	}
	if n := len(model.Requests()); n != 1 {
		t.Errorf("model calls = %d, want 1", n)
	}
	// Idempotent: the same answer, no second release.
	if err := s.Close(ctx); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if n := st.released.Load(); n != 1 {
		t.Errorf("Release calls after a second Close = %d, want 1", n)
	}
	// A fork still works — it reads this session and writes another —
	// and the stored session reopens.
	if _, err := s.Fork(ctx, s.Leaf()); err != nil {
		t.Errorf("Fork of a closed session: %v", err)
	}
	if _, err := thread.Open(ctx, st, s.ID(), weft.New(wefttest.Script())); err != nil {
		t.Errorf("Open after Close: %v", err)
	}
}

// The release's error is Close's result, every time.
func TestCloseReturnsReleaseError(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("lock file vanished")
	st := &releasing{Storage: thread.Memory(), err: boom}
	s, _ := thread.Create(ctx, st, weft.New(wefttest.Script()))
	for i := 0; i < 2; i++ {
		if err := s.Close(ctx); !errors.Is(err, boom) {
			t.Errorf("Close #%d: err = %v, want the release's error", i+1, err)
		}
	}
	if n := st.released.Load(); n != 1 {
		t.Errorf("Release calls = %d, want 1", n)
	}
}

// Close waits for the running turn and the queue behind it: both land
// in full before Close returns, and nothing new is admitted meanwhile.
func TestCloseDrainsRunningTurnAndQueue(t *testing.T) {
	ctx := context.Background()
	st := &releasing{Storage: thread.Memory()}
	agent, started, release := blockingAgent(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "block"}),
		wefttest.Say("first done"),
		wefttest.Say("second done"),
	))
	s, err := thread.Create(ctx, st, agent)
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("one"))
	if err != nil {
		t.Fatal(err)
	}
	waitStarted(t, started)
	t2, err := s.Send(ctx, weft.User("two")) // queued behind the running turn
	if err != nil {
		t.Fatal(err)
	}

	closed := make(chan error, 1)
	go func() { closed <- s.Close(ctx) }()
	// Admission stops as soon as Close is called, while it still waits.
	// (Continue is the side-effect-free probe for "Close has begun".)
	waitUntil(t, "Close never began", func() bool {
		_, err := s.Continue(ctx)
		return errors.Is(err, thread.ErrClosed)
	})
	if _, err := s.Send(ctx, weft.User("late")); !errors.Is(err, thread.ErrClosed) {
		t.Fatalf("Send while Close drains: err = %v, want ErrClosed", err)
	}
	select {
	case err := <-closed:
		t.Fatalf("Close returned (%v) while a turn was running", err)
	default:
	}
	if n := st.released.Load(); n != 0 {
		t.Fatalf("the storage was released while a turn was running")
	}

	close(release)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close never returned after the turn was released")
	}
	// Both turns were already decided when Close returned.
	for i, turn := range []*thread.Turn{t1, t2} {
		res, err := turn.Wait()
		if err != nil {
			t.Fatalf("turn %d: %v", i+1, err)
		}
		if want := []string{"first done", "second done"}[i]; res.Text() != want {
			t.Errorf("turn %d answered %q, want %q", i+1, res.Text(), want)
		}
	}
	if got := contextTexts(s); !equalStrings(got, []string{"one", "first done", "two", "second done"}) {
		t.Errorf("Context after the drain = %v", got)
	}
	if n := st.released.Load(); n != 1 {
		t.Errorf("Release calls = %d, want 1", n)
	}
	// Everything the drain wrote is in the storage.
	_, stored, _, err := st.Load(ctx, s.ID())
	if err != nil || len(stored) != len(s.Entries()) {
		t.Errorf("stored entries = %d (%v), in memory %d", len(stored), err, len(s.Entries()))
	}
}

// A Close whose context ends gives up: it cancels the running turn,
// ends the queued ones with ErrClosed, and returns the context's
// error. The session stays closed to new work; a second Close waits
// for the canceled turn to land, seals and releases.
func TestCloseContextEndCancelsTheTurn(t *testing.T) {
	ctx := context.Background()
	st := &releasing{Storage: thread.Memory()}
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "block"}),
		wefttest.Say("never said"),
		wefttest.Say("never said either"),
	)
	agent, started, _ := blockingAgent(model)
	s, err := thread.Create(ctx, st, agent)
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("one"))
	if err != nil {
		t.Fatal(err)
	}
	waitStarted(t, started)
	t2, err := s.Send(ctx, weft.User("two"))
	if err != nil {
		t.Fatal(err)
	}

	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := s.Close(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close under a dead context: err = %v, want DeadlineExceeded", err)
	}
	if _, err := t2.Wait(); !errors.Is(err, thread.ErrClosed) {
		t.Errorf("the queued turn: err = %v, want ErrClosed", err)
	}
	if _, err := t1.Wait(); !errors.Is(err, context.Canceled) {
		t.Errorf("the running turn: err = %v, want it canceled", err)
	}
	if _, err := s.Send(ctx, weft.User("late")); !errors.Is(err, thread.ErrClosed) {
		t.Errorf("Send after an abandoned Close: err = %v, want ErrClosed", err)
	}
	// Not yet sealed — the canceled turn may still be landing — but
	// already refusing the session-level appends a resume would need.
	if err := s.Label(ctx, t1.ID(), "late"); !errors.Is(err, thread.ErrClosed) {
		t.Errorf("Label after an abandoned Close: err = %v, want ErrClosed", err)
	}
	if n := st.released.Load(); n != 0 {
		t.Errorf("an abandoned Close released the storage (%d calls); the second Close does", n)
	}

	if err := s.Close(ctx); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if n := st.released.Load(); n != 1 {
		t.Errorf("Release calls = %d, want 1", n)
	}
	// The canceled turn is on the record, the queued prompt is not,
	// and the model was called exactly once.
	var canceled int
	for _, e := range s.Entries() {
		if te, ok := e.(thread.TurnEntry); ok && te.Canceled {
			canceled++
		}
	}
	if canceled != 1 {
		t.Errorf("canceled turn entries = %d, want 1", canceled)
	}
	if got := contextTexts(s); !equalStrings(got, []string{"one"}) {
		t.Errorf("Context = %v, want the first prompt alone", got)
	}
	if n := len(model.Requests()); n != 1 {
		t.Errorf("model calls = %d, want 1 — nothing may run after Close gave up", n)
	}
}

// Sends queued behind an approval boundary nobody decides cannot
// drain: Close ends them with ErrClosed instead of waiting forever,
// and the parked boundary stays in the file for the next Open.
func TestCloseEndsSendsQueuedBehindABoundary(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	refund := weft.Tool("refund", "Refund an order.",
		func(context.Context, struct{}) (string, error) { return "refunded", nil },
		weft.RequireApproval())
	model := wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "refund"}), wefttest.Say("resumed"))
	agent := weft.New(model, refund)
	s, err := thread.Create(ctx, st, agent)
	if err != nil {
		t.Fatal(err)
	}
	turn, _ := s.Send(ctx, weft.User("refund it"))
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	if n := len(s.Pending()); n != 1 {
		t.Fatalf("Pending = %d, want the parked call", n)
	}
	queued, err := s.Send(ctx, weft.User("and then?"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := queued.Wait(); !errors.Is(err, thread.ErrClosed) {
		t.Errorf("the queued turn: err = %v, want ErrClosed", err)
	}
	// A decision on the sealed session records nothing and runs nothing.
	if _, err := s.Decide(ctx, thread.Approve(s.Pending()[0].CallID)); !errors.Is(err, thread.ErrClosed) {
		t.Errorf("Decide after Close: err = %v, want ErrClosed", err)
	}
	if rt, err := s.Resume(ctx); err == nil {
		if _, werr := rt.Wait(); !errors.Is(werr, thread.ErrClosed) {
			t.Errorf("Resume after Close ran: %v", werr)
		}
	}
	if n := len(model.Requests()); n != 1 {
		t.Errorf("model calls = %d, want 1", n)
	}
	open, err := thread.Open(ctx, st, s.ID(), agent)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(open.Pending()); n != 1 {
		t.Errorf("Pending after reopen = %d, want the boundary still parked", n)
	}
}

// Close is safe to call from many goroutines at once, with Sends and
// reads racing it: every Close returns nil, the release happens once,
// every accepted turn is decided, and every refused Send says
// ErrClosed.
func TestCloseConcurrent(t *testing.T) {
	ctx := context.Background()
	st := &releasing{Storage: thread.Memory()}
	turns := make([]wefttest.Turn, 64)
	for i := range turns {
		turns[i] = wefttest.Say("ok")
	}
	s, err := thread.Create(ctx, st, weft.New(wefttest.Script(turns...)))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 4; j++ {
				turn, err := s.Send(ctx, weft.User("go"))
				if err != nil {
					if !errors.Is(err, thread.ErrClosed) {
						t.Errorf("Send: %v", err)
					}
					return
				}
				if _, err := turn.Wait(); err != nil && !errors.Is(err, thread.ErrClosed) {
					t.Errorf("turn: %v", err)
				}
				_ = s.Entries()
				_ = s.Context()
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Close(ctx); err != nil {
				t.Errorf("Close: %v", err)
			}
		}()
	}
	wg.Wait()
	if n := st.released.Load(); n != 1 {
		t.Errorf("Release calls = %d, want 1", n)
	}
	_, stored, _, err := st.Load(ctx, s.ID())
	if err != nil || len(stored) != len(s.Entries()) {
		t.Errorf("stored entries = %d (%v), in memory %d", len(stored), err, len(s.Entries()))
	}
}

// Open never runs anything, whatever the file holds: a steer the
// writer accepted and never settled waits in the queue, and is
// delivered into the next turn the caller starts.
func TestOpenStartsNoRun(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		model := wefttest.Script(wefttest.Say("first"), wefttest.Say("after the steer"))
		agent := weft.New(model)
		s, _ := thread.Create(ctx, st, agent)
		msg := weft.User("accepted before the crash")
		if err := st.Append(ctx, s.ID(), thread.ReceiptEntry{
			ID: "e_steer", Created: time.Unix(1, 0).UTC(), Status: thread.ReceiptQueued, Msg: &msg,
		}); err != nil {
			t.Fatal(err)
		}
		cctx, cancel := context.WithCancel(ctx)
		abandon(t, st, s.ID())
		open, err := thread.Open(cctx, st, s.ID(), agent)
		cancel() // Open's context ending must not strand what it restored
		if err != nil {
			t.Fatal(err)
		}
		if n := len(model.Requests()); n != 0 {
			t.Fatalf("Open ran the model %d times", n)
		}
		if n := len(open.Entries()); n != 1 {
			t.Fatalf("Open wrote entries: %d, want the 1 stored", n)
		}
		if q := open.Queue(); len(q) != 1 || q[0].Receipt != "e_steer" {
			t.Fatalf("Queue = %+v, want the restored steer", q)
		}
		// The caller's Send is what runs: the steer is delivered into
		// that turn at its drain point, and the receipt says so.
		turn, err := open.Send(ctx, weft.User("hello"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := turn.Wait(); err != nil {
			t.Fatal(err)
		}
		if got := contextTexts(open); !equalStrings(got, []string{"hello", "first", "accepted before the crash", "after the steer"}) {
			t.Errorf("Context = %v", got)
		}
		if got := receiptStatus(receipts(open))["e_steer"]; got != thread.ReceiptDelivered {
			t.Errorf("the restored steer's receipt = %q, want delivered", got)
		}
		// Settled: a reopen restores nothing.
		if q := reopenWith(t, ctx, st, open, agent).Queue(); len(q) != 0 {
			t.Errorf("Queue after the steer settled = %+v", q)
		}
	})
}

// A restored steer the caller does not want is dropped with
// ClearQueue, and never reaches the model.
func TestClearQueueDropsRestoredSteer(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	model := wefttest.Script(wefttest.Say("only the prompt"))
	agent := weft.New(model)
	s, _ := thread.Create(ctx, st, agent)
	msg := weft.User("stale steer")
	if err := st.Append(ctx, s.ID(), thread.ReceiptEntry{
		ID: "e_steer", Created: time.Unix(1, 0).UTC(), Status: thread.ReceiptQueued, Msg: &msg,
	}); err != nil {
		t.Fatal(err)
	}
	abandon(t, st, s.ID())
	open, err := thread.Open(ctx, st, s.ID(), agent)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := open.ClearQueue(ctx); err != nil || n != 1 {
		t.Fatalf("ClearQueue = %d, %v; want 1", n, err)
	}
	if turn, err := open.Continue(ctx); err != nil || turn != nil {
		t.Fatalf("Continue after ClearQueue = %v, %v; want nothing to run", turn, err)
	}
	turn, _ := open.Send(ctx, weft.User("hello"))
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	if steerInContext(open, "stale steer") {
		t.Error("the dropped steer reached the model")
	}
	if got := receiptStatus(receipts(open))["e_steer"]; got != thread.ReceiptDropped {
		t.Errorf("receipt = %q, want dropped", got)
	}
}

// A Close that gives up while an approval resume is running fells the
// resume and nothing takes its place: no re-armed resume, no model
// call, whatever the boundary's state.
func TestCloseContextEndDuringResume(t *testing.T) {
	ctx := context.Background()
	st := &releasing{Storage: thread.Memory()}
	started := make(chan struct{})
	var once sync.Once
	refund := weft.Tool("refund", "Refund an order.",
		func(ctx context.Context, _ struct{}) (string, error) {
			once.Do(func() { close(started) })
			<-ctx.Done() // runs only once approved; ends only with its run
			return "", ctx.Err()
		},
		weft.RequireApproval())
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund"}),
		wefttest.Say("never said"),
		wefttest.Say("never said either"),
	)
	s, err := thread.Create(ctx, st, weft.New(model, refund))
	if err != nil {
		t.Fatal(err)
	}
	turn, _ := s.Send(ctx, weft.User("refund it"))
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	resume, err := s.Decide(ctx, thread.Approve(s.Pending()[0].CallID))
	if err != nil || resume == nil {
		t.Fatalf("Decide = %v, %v; want the resume turn", resume, err)
	}
	waitStarted(t, started)

	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := s.Close(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close under a dead context: err = %v, want DeadlineExceeded", err)
	}
	if _, err := resume.Wait(); err == nil {
		t.Error("the resume ran to a successful end after Close gave up")
	}
	if err := s.Close(ctx); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if n := len(model.Requests()); n != 1 {
		t.Errorf("model calls = %d, want 1 — the parked turn's, and nothing after Close gave up", n)
	}
	if n := st.released.Load(); n != 1 {
		t.Errorf("Release calls = %d, want 1", n)
	}
	// Whatever the canceled resume left, a later decision or resume on
	// the closed session starts nothing.
	if rt, err := s.Resume(ctx); err == nil {
		if _, werr := rt.Wait(); werr == nil {
			t.Error("Resume after Close ran")
		}
	}
	if n := len(model.Requests()); n != 1 {
		t.Errorf("model calls after a late Resume = %d, want 1", n)
	}
}
