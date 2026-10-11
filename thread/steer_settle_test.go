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

// isSteeredMessage matches a run's step message that a steer put in
// the transcript: a user message with no run id (a prompt carries its
// run's).
func isSteeredMessage(e thread.Entry) bool {
	m, ok := e.(thread.MessageEntry)
	return ok && m.RunID == "" && m.Message.Role == core.RoleUser
}

// A steer the running turn drained lands in the run's transcript with
// the step that carries it; its delivered receipt joins the turn's end.
// A writer that dies between the two leaves a queued receipt nothing
// links back to — and the steer's message on the line: the message is
// the proof it ran, so a reopen does not restore it (it would run
// twice) and ClearQueue finds nothing to drop. The crash matrix's
// clear_queue point meets this shape by timing; this test cuts at it.
func TestDrainedSteerCrashBeforeTurnEndIsNotRestored(t *testing.T) {
	ctx := context.Background()
	tool, rel := blockingTool()
	defer rel.open()
	started := make(chan struct{})
	var once sync.Once
	agent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "wait"}),
		wefttest.Say("answered the steer"),
		wefttest.Say("next"),
	), tool, core.Tap(func(_ context.Context, ev core.Event) {
		if _, ok := ev.(core.ToolStart); ok {
			once.Do(func() { close(started) })
		}
	}))
	st := &faultStorage{Storage: thread.Memory()}
	var dead atomic.Bool
	st.failAppend = func(es []thread.Entry) bool {
		// The step batch carrying the steered message lands; the writer
		// dies right after it.
		if dead.Load() {
			return true
		}
		if hasEntry(es, isSteeredMessage) {
			dead.Store(true)
		}
		return false
	}
	s, err := thread.Create(ctx, st, agent)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Send(ctx, core.User("long work"))
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := s.Send(ctx, core.User("steer me"), thread.As(thread.Steer)); err != nil {
		t.Fatal(err)
	}
	rel.open()
	_, _ = first.Wait()
	_ = s.WaitIdle(ctx)
	if !dead.Load() {
		t.Fatal("the steer was never drained into the run")
	}
	again := reopenWith(t, ctx, st.Storage, s, agent)
	if q := again.Queue(); len(q) != 0 {
		t.Fatalf("the reopened Queue = %+v, want nothing: the steer's message is on the line", q)
	}
	if got := contextTexts(again); countText(got, "steer me") != 1 {
		t.Fatalf("the reopened Context = %v, want the steer once", got)
	}
	if n, err := again.ClearQueue(ctx); err != nil || n != 0 {
		t.Fatalf("ClearQueue after the crash: %d, %v", n, err)
	}
}

// A steer whose deferral at its turn's end the storage refused outright
// runs from memory — its Send succeeded, accepted input is durable
// input (T4) — and its queued receipt is settled in the same append as
// the follow-up's prompt: a reopen after the follow-up ran restores
// nothing, the steer runs once; a reopen before it ran restores the
// steer from its receipt.
func TestRefusedSteerDeferralSettlesWithTheFollowUpPrompt(t *testing.T) {
	steerAgent := func() (*core.Agent, chan struct{}, *release) {
		tool, rel := blockingTool()
		started := make(chan struct{})
		var once sync.Once
		agent := core.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "wait"}),
			wefttest.Say("followed"),
			wefttest.Say("again"),
		), tool, core.StopWhen(core.HasToolCall("wait")), core.Tap(func(_ context.Context, ev core.Event) {
			if _, ok := ev.(core.ToolStart); ok {
				once.Do(func() { close(started) })
			}
		}))
		return agent, started, rel
	}
	run := func(t *testing.T, st *faultStorage, agent *core.Agent, started chan struct{}, rel *release) (*thread.Session, *thread.Turn) {
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
		rel.open()
		if _, err := first.Wait(); err != nil {
			t.Fatal(err)
		}
		if steer.Outcome() != thread.TurnDeferred || steer.Next() == nil {
			t.Fatalf("steer = %v, next %v; want deferred with a follow-up", steer.Outcome(), steer.Next())
		}
		return s, steer.Next()
	}

	t.Run("reopen_after_it_ran", func(t *testing.T) {
		ctx := context.Background()
		agent, started, rel := steerAgent()
		defer rel.open()
		st := &faultStorage{Storage: thread.Memory()}
		st.failAppend = func(es []thread.Entry) bool { return hasEntry(es, isDeferredReceipt) && !hasEntry(es, isMessage) }
		s, follow := run(t, st, agent, started, rel)
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
		if err := s.Close(ctx); err != nil {
			t.Fatal(err)
		}
		again := reopenWith(t, ctx, st.Storage, s, agent)
		if q := again.Queue(); len(q) != 0 {
			t.Fatalf("the reopened Queue = %+v, want nothing: the steer ran", q)
		}
		if got := contextTexts(again); !equalStrings(got, live) {
			t.Errorf("the reopened Context = %v, want %v", got, live)
		}
	})

	t.Run("reopen_before_it_ran", func(t *testing.T) {
		ctx := context.Background()
		agent, started, rel := steerAgent()
		defer rel.open()
		st := &faultStorage{Storage: thread.Memory()}
		var dead atomic.Bool
		st.failAppend = func(es []thread.Entry) bool {
			if hasEntry(es, isDeferredReceipt) && !hasEntry(es, isMessage) {
				return true // the turn-end deferral, refused outright
			}
			if hasEntry(es, func(e thread.Entry) bool {
				m, ok := e.(thread.MessageEntry)
				return ok && m.RunID != "" && m.Message.Text() == "steer me"
			}) {
				dead.Store(true) // the writer dies at the follow-up's prompt
			}
			return dead.Load()
		}
		s, follow := run(t, st, agent, started, rel)
		_, _ = follow.Wait()
		_ = s.WaitIdle(ctx)
		again := reopenWith(t, ctx, st.Storage, s, agent)
		if q := again.Queue(); len(q) != 1 || q[0].Msg.Text() != "steer me" {
			t.Fatalf("the reopened Queue = %+v, want the steer restored", q)
		}
	})
}

// A steer the running turn drained whose turn-end batch the storage
// refused: the steered message is already on the line (written with
// its step), so the steer is delivered — not deferred to a follow-up
// that would put the message on the line a second time. Its delivered
// receipt is written on its own (the read-side rule covers it when
// that append fails too), and a reopen agrees.
func TestDrainedSteerWithRefusedTurnEndIsNotDeferred(t *testing.T) {
	ctx := context.Background()
	tool, rel := blockingTool()
	defer rel.open()
	started := make(chan struct{})
	var once sync.Once
	agent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "wait"}),
		wefttest.Say("answered the steer"),
		wefttest.Say("again"),
		wefttest.Say("and again"),
	), tool, core.Tap(func(_ context.Context, ev core.Event) {
		if _, ok := ev.(core.ToolStart); ok {
			once.Do(func() { close(started) })
		}
	}))
	st := &faultStorage{Storage: thread.Memory()}
	var stepped atomic.Bool
	st.failAppend = func(es []thread.Entry) bool {
		if hasEntry(es, isSteeredMessage) {
			stepped.Store(true)
		}
		// Every turn-end batch is refused once the steered step landed.
		return stepped.Load() && hasEntry(es, func(e thread.Entry) bool { _, ok := e.(thread.TurnEntry); return ok })
	}
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
	rel.open()
	_, _ = first.Wait()
	if err := s.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	if !stepped.Load() {
		t.Fatal("the steer was never drained into a persisted step")
	}
	if got := steer.Outcome(); got != thread.TurnDelivered || steer.Next() != nil {
		t.Errorf("steer = %v, follow-up %v; want delivered with no follow-up", got, steer.Next() != nil)
	}
	if live := contextTexts(s); countText(live, "steer me") != 1 {
		t.Errorf("Context = %v, want the steer once", live)
	}
	if q := s.Queue(); len(q) != 0 {
		t.Errorf("Queue = %+v, want nothing", q)
	}
	if !steerSettledOnce(s) {
		t.Errorf("receipts = %+v, want the steer's delivered receipt once", receipts(s))
	}
	st.mu.Lock()
	st.failAppend = nil
	st.mu.Unlock()
	again := reopenWith(t, ctx, st.Storage, s, agent)
	if q := again.Queue(); len(q) != 0 {
		t.Errorf("the reopened Queue = %+v, want nothing", q)
	}
	if got := contextTexts(again); countText(got, "steer me") != 1 {
		t.Errorf("the reopened Context = %v, want the steer once", got)
	}
}

// A steer the running turn drained whose message is in no tree — the
// step that carried it and the turn-end batch both refused — must not
// finish as delivered into nothing: accepted input acknowledged then
// lost is the scar ADR 0011 §1 carries from Codex #40805. It defers,
// and its follow-up puts the message on the line once; a reopen agrees.
func TestDrainedSteerInNoTreeIsDeferred(t *testing.T) {
	ctx := context.Background()
	tool, rel := blockingTool()
	defer rel.open()
	started := make(chan struct{})
	var once sync.Once
	agent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "wait"}),
		wefttest.Say("answered the steer"),
		wefttest.Say("followed"),
	), tool, core.Tap(func(_ context.Context, ev core.Event) {
		if _, ok := ev.(core.ToolStart); ok {
			once.Do(func() { close(started) })
		}
	}))
	st := &faultStorage{Storage: thread.Memory()}
	var turnEndRefused atomic.Bool
	st.failAppend = func(es []thread.Entry) bool {
		steered := hasEntry(es, isSteeredMessage) // the steered message's step, every time
		if hasEntry(es, func(e thread.Entry) bool { _, ok := e.(thread.TurnEntry); return ok }) {
			// The first turn's end — the batch that would carry the
			// held steered message — is refused; later ones are not.
			return turnEndRefused.CompareAndSwap(false, true) || steered
		}
		return steered
	}
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
	rel.open()
	if _, err := first.Wait(); !errors.Is(err, thread.ErrNotPersisted) {
		t.Fatalf("the first turn's Wait = %v, want ErrNotPersisted", err)
	}
	if steer.Outcome() != thread.TurnDeferred || steer.Next() == nil {
		t.Fatalf("steer = %v, follow-up %v; want deferred with a follow-up", steer.Outcome(), steer.Next() != nil)
	}
	if _, err := steer.Next().Wait(); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	live := contextTexts(s)
	if countText(live, "steer me") != 1 {
		t.Fatalf("Context = %v, want the steer once", live)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	again := reopenWith(t, ctx, st.Storage, s, agent)
	if q := again.Queue(); len(q) != 0 {
		t.Errorf("the reopened Queue = %+v, want nothing", q)
	}
	if got := contextTexts(again); !equalStrings(got, live) {
		t.Errorf("the reopened Context = %v, want %v", got, live)
	}
}
