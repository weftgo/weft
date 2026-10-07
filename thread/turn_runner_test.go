package thread_test

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
)

// soloModel wraps a model and counts overlapping Stream calls: a
// session runs one model call at a time, whatever races around it.
type soloModel struct {
	inner   core.Model
	inside  atomic.Int32
	overlap atomic.Bool
}

func (m *soloModel) Info() core.ModelInfo { return core.InfoOf(m.inner) }

func (m *soloModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return func(yield func(core.ModelEvent, error) bool) {
		if m.inside.Add(1) > 1 {
			m.overlap.Store(true)
		}
		defer m.inside.Add(-1)
		time.Sleep(200 * time.Microsecond) // widen the window a second runner would land in
		for ev, err := range m.inner.Stream(ctx, req) {
			if !yield(ev, err) {
				return
			}
		}
	}
}

// The runner picks a settled boundary up on its own — here the parked
// request lapses between the park and the runner's next look — and the
// resume it starts is in flight like any turn: Branch and a Reject
// Send read busy, and no second runner starts beside it.
func TestSettledBoundaryPickupIsInFlight(t *testing.T) {
	ctx := context.Background()
	script := wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "dangerous", ID: "call_d"}))
	model := &blockingModel{script: script, block: make(chan struct{})}
	agent := core.New(model,
		core.Tool("dangerous", "", func(context.Context, struct{}) (string, error) { return "ran", nil }, core.RequireApproval()))
	// Every reading of the clock is two minutes after the last: the
	// request's one-minute lifetime is over by the time the runner
	// looks at the boundary again.
	var mu sync.Mutex
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	clock := thread.Clock(func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(2 * time.Minute)
		return now
	})
	s, err := thread.Create(ctx, thread.Memory(), agent, clock, thread.RequestExpiry(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, core.User("do it"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := t1.Wait()
	if err != nil || len(res.Pending) != 1 {
		t.Fatalf("the parking turn: %v, %v", res, err)
	}
	// The runner's pickup arms the resume; its model call blocks.
	waitUntil(t, "the runner never picked the settled boundary up", func() bool { return model.calls.Load() >= 2 })
	resume := t1.Next()
	if resume == nil {
		t.Fatal("the pickup's resume is not linked from the parked turn")
	}
	if err := s.CheckBusyInvariant(); err != nil {
		t.Error(err)
	}
	if err := s.Branch(ctx, t1.ID()); !errors.Is(err, thread.ErrBusy) {
		t.Errorf("Branch during the pickup's resume = %v, want ErrBusy", err)
	}
	if turn, err := s.Send(ctx, core.User("me too"), thread.As(thread.Reject)); !errors.Is(err, thread.ErrBusy) {
		t.Errorf("a Reject Send during the pickup's resume = %v, %v; want ErrBusy", turn, err)
	}
	close(model.block)
	if _, err := resume.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	if n := model.calls.Load(); n != 2 {
		t.Errorf("%d model calls, want 2: the parking step and the one resume", n)
	}
	if err := s.CheckBusyInvariant(); err != nil {
		t.Error(err)
	}
}

// Sends under every policy, Branch attempts and reads race a session
// that keeps running turns: the runner slot's invariant holds at every
// look, and no two model calls ever overlap.
func TestBusyInvariantUnderRaces(t *testing.T) {
	ctx := context.Background()
	const turns = 400
	steps := make([]wefttest.Turn, turns)
	for i := range steps {
		steps[i] = wefttest.Say("ok")
	}
	model := &soloModel{inner: wefttest.Script(steps...)}
	s, err := thread.Create(ctx, thread.Memory(), core.New(model))
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var probes sync.WaitGroup
	probes.Add(2)
	go func() { // the invariant, read as often as the lock allows
		defer probes.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := s.CheckBusyInvariant(); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() { // navigation: refused while a turn is in flight, harmless between
		defer probes.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := s.Branch(ctx, s.Leaf()); err != nil && !errors.Is(err, thread.ErrBusy) {
				t.Errorf("Branch: %v", err)
				return
			}
			time.Sleep(50 * time.Microsecond)
		}
	}()
	var senders sync.WaitGroup
	for g := 0; g < 4; g++ {
		senders.Add(1)
		go func() {
			defer senders.Done()
			policies := []thread.Policy{thread.Queue, thread.Reject, thread.Steer, thread.Queue}
			for i := 0; i < 20; i++ {
				turn, err := s.Send(ctx, core.User(fmt.Sprintf("m%d-%d", g, i)), thread.As(policies[(g+i)%len(policies)]))
				if errors.Is(err, thread.ErrBusy) {
					continue
				}
				if err != nil {
					t.Errorf("Send: %v", err)
					return
				}
				if i%3 == 0 {
					if _, err := turn.Wait(); err != nil {
						t.Errorf("Wait: %v", err)
						return
					}
				}
			}
		}()
	}
	senders.Wait()
	if err := s.WaitIdle(deadline(t, 20*time.Second)); err != nil {
		t.Fatalf("the session never went idle: %v", err)
	}
	close(stop)
	probes.Wait()
	if model.overlap.Load() {
		t.Error("two model calls overlapped: a second runner started beside the first")
	}
	if err := s.CheckBusyInvariant(); err != nil {
		t.Error(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

// trip is a switch a test flips to make a session callback panic.
type trip struct {
	armed atomic.Bool
	at    atomic.Int64 // panic on the at-th call (1-based); 0 = on every call while armed
	n     atomic.Int64
}

func (p *trip) hit(what string) {
	n := p.n.Add(1)
	if !p.armed.Load() {
		return
	}
	if at := p.at.Load(); at == 0 || at == n {
		panic(what + " blew up")
	}
}

// The Clock function runs under the session's lock. One that panics
// while the runner writes — the turn's end, a queued send's prompt —
// must cost that turn, not the session: the lock is released, the turn
// ends with ErrTurnPanicked, and the next Send runs.
func TestClockPanicUnderTheLockIsContained(t *testing.T) {
	ctx := context.Background()
	var clockTrip trip
	clock := thread.Clock(func() time.Time {
		clockTrip.hit("the clock")
		return time.Now()
	})
	agent, _, started, rel := heldAgent("first", "second", "third")
	s, err := thread.Create(ctx, thread.Memory(), agent, clock)
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, core.User("one"))
	if err != nil {
		t.Fatal(err)
	}
	<-started
	queued, err := s.Send(ctx, core.User("two"))
	if err != nil {
		t.Fatal(err)
	}
	clockTrip.armed.Store(true) // every write from here on panics in the clock
	rel.open()
	for name, turn := range map[string]*thread.Turn{"the running turn": t1, "the queued turn": queued} {
		_, err := turn.WaitContext(deadline(t, 5*time.Second))
		if !errors.Is(err, thread.ErrTurnPanicked) {
			t.Fatalf("%s: %v, want ErrTurnPanicked (a deadline here is the runner deadlocked on its own lock)", name, err)
		}
		if got := turn.Outcome(); got != thread.TurnFailed {
			t.Errorf("%s: Outcome = %v, want failed", name, got)
		}
	}
	if err := s.WaitIdle(deadline(t, 5*time.Second)); err != nil {
		t.Fatalf("the runner never let go: %v", err)
	}
	if err := s.CheckBusyInvariant(); err != nil {
		t.Error(err)
	}
	// The session works again once the clock does.
	clockTrip.armed.Store(false)
	t3, err := s.Send(ctx, core.User("three"))
	if err != nil {
		t.Fatal(err)
	}
	if res, err := t3.WaitContext(deadline(t, 5*time.Second)); err != nil || res.Text() == "" {
		t.Fatalf("the turn after the panics: %v, %v", res, err)
	}
	if err := s.Close(deadline(t, 5*time.Second)); err != nil {
		t.Fatal(err)
	}
}

// Wherever in a busy turn's life an IDs or Clock call panics — a step
// append, the turn's end, a steer's settlement, the runner's item
// boundary — nothing deadlocks: every Turn ends, the runner lets go,
// and the session takes the next Send.
func TestCallbackPanicNeverWedgesTheSession(t *testing.T) {
	for _, which := range []string{"ids", "clock"} {
		for at := int64(1); at <= 28; at++ {
			t.Run(fmt.Sprintf("%s_call_%d", which, at), func(t *testing.T) {
				ctx := context.Background()
				var tr trip
				tr.at.Store(at)
				ids := thread.IDs(func() string {
					if which == "ids" {
						tr.hit("the ids function")
					}
					return thread.NewEntryID()
				})
				clock := thread.Clock(func() time.Time {
					if which == "clock" {
						tr.hit("the clock")
					}
					return time.Now()
				})
				// A tool step, a steer sent while the tool runs, a second
				// step, then a queued send: the paths that mint under
				// the lock.
				echo := core.Tool("echo", "", func(context.Context, struct{}) (string, error) { return "ok", nil })
				model := wefttest.Script(
					wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
					wefttest.Say("one"), wefttest.Say("two"), wefttest.Say("three"), wefttest.Say("four"),
				)
				var ref *thread.Session
				var once sync.Once
				agent := core.New(model, echo, core.Tap(func(_ context.Context, ev core.Event) {
					if _, ok := ev.(core.ToolStart); ok {
						once.Do(func() { _, _ = ref.Send(ctx, core.User("a steer"), thread.As(thread.Steer)) })
					}
				}))
				s, err := thread.Create(ctx, thread.Memory(), agent, ids, clock)
				if err != nil {
					t.Fatal(err)
				}
				ref = s
				tr.n.Store(0)
				tr.armed.Store(true)
				send := func(text string) (turn *thread.Turn) {
					defer func() { _ = recover() }() // a panic on the caller's own goroutine is the caller's
					turn, _ = s.Send(ctx, core.User(text))
					return turn
				}
				var held []*thread.Turn
				for _, text := range []string{"first", "second"} {
					if turn := send(text); turn != nil {
						held = append(held, turn)
					}
				}
				for _, turn := range held {
					if _, err := turn.WaitContext(deadline(t, 5*time.Second)); errors.Is(err, context.DeadlineExceeded) {
						t.Fatalf("a turn never ended: the session is wedged (call %d of %s)", at, which)
					}
				}
				if err := s.WaitIdle(deadline(t, 5*time.Second)); err != nil {
					t.Fatalf("the runner never let go: %v", err)
				}
				if err := s.CheckBusyInvariant(); err != nil {
					t.Error(err)
				}
				tr.armed.Store(false)
				last, err := s.Send(ctx, core.User("after"))
				if err != nil {
					t.Fatalf("Send after the panic: %v", err)
				}
				if _, err := last.WaitContext(deadline(t, 5*time.Second)); errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("the turn after the panic never ended")
				}
				if err := s.Close(deadline(t, 5*time.Second)); err != nil {
					t.Fatalf("Close: %v", err)
				}
			})
		}
	}
}

// Close waits on the runner's own signals — the turn landing, the
// runner taking its next item or exiting — and returns as soon as the
// queue has drained.
func TestCloseReturnsWhenTheRunnerExits(t *testing.T) {
	ctx := context.Background()
	agent, _, started, rel := heldAgent("first", "second", "third")
	s, _ := thread.Create(ctx, thread.Memory(), agent)
	var turns []*thread.Turn
	t1, _ := s.Send(ctx, core.User("one"))
	<-started
	turns = append(turns, t1)
	for _, text := range []string{"two", "three"} {
		turn, err := s.Send(ctx, core.User(text))
		if err != nil {
			t.Fatal(err)
		}
		turns = append(turns, turn)
	}
	closed := make(chan error, 1)
	go func() { closed <- s.Close(ctx) }()
	select {
	case err := <-closed:
		t.Fatalf("Close returned %v while a turn was running", err)
	case <-time.After(20 * time.Millisecond):
	}
	rel.open()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close never returned")
	}
	for i, turn := range turns {
		if _, err := turn.Wait(); err != nil {
			t.Errorf("turn %d of a drained Close: %v", i+1, err)
		}
	}
	if err := s.CheckBusyInvariant(); err != nil {
		t.Error(err)
	}
}

// The runner's item boundary writes too — a Rollback's branch, a
// steer's deferral — and so calls IDs and Clock under the lock. A
// panic there cannot fail a turn (it has landed): the runner exits
// with its slot free and the queue intact, and the next Send restarts
// it at the queue's head.
func TestEpiloguePanicFreesTheRunner(t *testing.T) {
	ctx := context.Background()
	var tr trip
	ids := thread.IDs(func() string {
		tr.hit("the ids function")
		return thread.NewEntryID()
	})
	agent, _, started, rel := heldAgent("the follow-up's reply", "the last reply")
	defer rel.open()
	s, err := thread.Create(ctx, thread.Memory(), agent, ids)
	if err != nil {
		t.Fatal(err)
	}
	t1, _ := s.Send(ctx, core.User("one"))
	<-started
	followUp, err := s.Send(ctx, core.User("instead, this"), thread.As(thread.Rollback))
	if err != nil {
		t.Fatal(err)
	}
	// From here every id the session mints panics: the interrupted
	// turn's end, and then the rollback's branch at the item boundary.
	tr.armed.Store(true)
	if _, err := t1.WaitContext(deadline(t, 5*time.Second)); !errors.Is(err, thread.ErrTurnPanicked) {
		t.Fatalf("the interrupted turn: %v, want ErrTurnPanicked", err)
	}
	if err := s.WaitIdle(deadline(t, 5*time.Second)); err != nil {
		t.Fatalf("the runner never let go: %v", err)
	}
	if err := s.CheckBusyInvariant(); err != nil {
		t.Error(err)
	}
	if q := s.Queue(); len(q) != 1 || q[0].Receipt != followUp.ID() {
		t.Fatalf("Queue after the runner's exit = %+v, want the follow-up still queued", q)
	}
	tr.armed.Store(false)
	last, err := s.Send(ctx, core.User("and then"))
	if err != nil {
		t.Fatal(err)
	}
	if res, err := followUp.WaitContext(deadline(t, 5*time.Second)); err != nil || res.Text() != "the follow-up's reply" {
		t.Fatalf("the follow-up: %v, %v", res, err)
	}
	if res, err := last.WaitContext(deadline(t, 5*time.Second)); err != nil || res.Text() != "the last reply" {
		t.Fatalf("the send that restarted the runner: %v, %v", res, err)
	}
}
