package pool_test

import (
	"context"
	"iter"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/pool"
)

// logLines collects the lines a logger is handed, level first.
type logLines struct {
	mu  sync.Mutex
	got []string
}

func (l *logLines) Enabled(context.Context, slog.Level) bool { return true }
func (l *logLines) Handle(_ context.Context, rec slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, rec.Level.String()+" "+rec.Message)
	return nil
}
func (l *logLines) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *logLines) WithGroup(string) slog.Handler      { return l }
func (l *logLines) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.got...)
}

// awaitParked polls s's receipts until n of them read parked.
func awaitParked(t *testing.T, s *thread.Session, n int) []pool.Receipt {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var parked []pool.Receipt
		for _, r := range pool.Receipts(s) {
			if r.State == pool.Parked {
				parked = append(parked, r)
			}
		}
		if len(parked) == n {
			return parked
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%d parked receipts never came: %+v", n, pool.Receipts(s))
	return nil
}

// hasParked reports whether one of rcs reads parked.
func hasParked(rcs []pool.Receipt) bool {
	for _, r := range rcs {
		if r.State == pool.Parked {
			return true
		}
	}
	return false
}

// followLead follows a lead's turn through its continuations to its
// end.
func followLead(t *testing.T, t1 *thread.Turn) *core.RunResult {
	t.Helper()
	res, err := t1.Wait()
	for err == nil && res != nil && len(res.Pending) > 0 && t1.Next() != nil {
		t1 = t1.Next()
		res, err = t1.Wait()
	}
	if err != nil {
		t.Fatalf("lead: %v", err)
	}
	return res
}

// siblingLead is a lead whose one step delegates to child (call la)
// beside slow (call ls), a plain tool whose handler is the test's.
func siblingLead(p *pool.Pool, child *core.Agent, slow func(context.Context) (string, error)) *core.Agent {
	slowTool := core.Tool("slow", "", func(ctx context.Context, in struct{}) (string, error) { return slow(ctx) })
	return core.New(wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "a", ID: "la", Args: wefttest.Args(struct{ Prompt string }{"x"})},
			wefttest.Call{Name: "slow", ID: "ls", Args: `{}`},
		),
		wefttest.Say("lead done"),
	), p.MustWrap("a", "", child), slowTool, core.Timeout(0))
}

const canceledA = `SUBAGENT_CANCELED: agent "a" was canceled before it finished`

// Cancel of a sync child parked while its parent's step still runs a
// sibling call returns at once, under its own context: the settlement
// is recorded before it returns, and the delegating call — not parked
// yet, its turn still in that step — resolves with SUBAGENT_CANCELED
// once the turn parks it (Wait waits for that). Before the fix Cancel
// waited for the parent's step to end, its own 300 ms context
// ignored: it held its caller for as long as the sibling ran.
func TestCancelParkedChildBesideARunningSibling(t *testing.T) {
	ctx := context.Background()
	p := pool.New(2)
	release := make(chan struct{})
	child, ran := gatedChild(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"A"}`}),
		wefttest.Say("child done"),
	)
	lead := siblingLead(p, child, func(ctx context.Context) (string, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return "slow done", nil
	})
	s, _ := thread.Create(ctx, thread.Memory(), lead)
	t1, _ := s.Send(ctx, core.User("go"))
	rc := awaitParked(t, s, 1)[0]
	cctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.Cancel(cctx, s, rc.ID) }()
	select {
	case err := <-done: // the sibling still blocks: Cancel did not wait for it
		if err != nil {
			t.Errorf("Cancel: %v", err)
		}
	case <-time.After(3 * time.Second):
		close(release)
		<-done
		t.Fatalf("Cancel(ctx 300ms) of a parked child still blocked after 3s, waiting for the parent's sibling call")
	}
	if got := pool.Receipts(s)[0]; got.State != pool.Canceled {
		t.Errorf("receipt when Cancel returned = %+v, want canceled", got)
	}
	close(release)
	waitFor(t, p, s, rc.ID, pool.Canceled)
	if res := followLead(t, t1); res.Text() != "lead done" {
		t.Errorf("lead ended %q with %d pending", res.Text(), len(res.Pending))
	}
	if got := toolResultsOf(s.Context()); len(got) != 2 || got[0] != canceledA {
		t.Errorf("lead tool results = %v, want la %q", got, canceledA)
	}
	if got := ran.snapshot(); len(got) != 0 {
		t.Errorf("the canceled child's refund ran %v", got)
	}
	within(t, 10*time.Second, "Close", func() { _ = p.Close(ctx) })
}

// The same Cancel made from inside the sibling's own handler — a tool
// that cancels a delegation of its own step — returns at once too.
// Before the fix it deadlocked: Cancel waited for the step, the step
// for the handler making the Cancel.
func TestCancelFromTheSiblingHandler(t *testing.T) {
	ctx := context.Background()
	p := pool.New(2)
	var s *thread.Session
	var cancelErr error
	var took time.Duration
	child, _ := gatedChild(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"A"}`}),
		wefttest.Say("child done"),
	)
	lead := siblingLead(p, child, func(tctx context.Context) (string, error) {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if rcs := pool.Receipts(s); len(rcs) == 1 && rcs[0].State == pool.Parked {
				cctx, cancel := context.WithTimeout(tctx, 500*time.Millisecond)
				start := time.Now()
				cancelErr = p.Cancel(cctx, s, rcs[0].ID)
				took = time.Since(start)
				cancel()
				return "canceled it", nil
			}
			time.Sleep(time.Millisecond)
		}
		return "never parked", nil
	})
	s, _ = thread.Create(ctx, thread.Memory(), lead)
	t1, _ := s.Send(ctx, core.User("go"))
	var res *core.RunResult
	within(t, 5*time.Second, "the lead's turn", func() {
		res, _ = t1.Wait()
	})
	if t.Failed() {
		cctx, c := context.WithTimeout(ctx, 3*time.Second)
		_ = p.Close(cctx)
		c()
		return
	}
	if cancelErr != nil || took > 3*time.Second {
		t.Errorf("Cancel from the sibling's handler = %v after %v", cancelErr, took)
	}
	rc := pool.Receipts(s)[0]
	waitFor(t, p, s, rc.ID, pool.Canceled)
	if len(res.Pending) > 0 {
		res = followLead(t, continuation(t, t1))
	}
	if res.Text() != "lead done" {
		t.Errorf("lead ended %q", res.Text())
	}
	if got := toolResultsOf(s.Context()); len(got) != 2 || got[0] != canceledA || got[1] != "canceled it" {
		t.Errorf("lead tool results = %v", got)
	}
	within(t, 10*time.Second, "Close", func() { _ = p.Close(ctx) })
}

// expiryChain builds lead → levels of sync wraps → a leaf whose refund
// asks approval once, every level's step delegating one call (the
// level's name, the call "c"+name) and — at the level named sibling —
// a 150 ms plain tool beside it. A request expiry set on the lead's
// session reaches every level (thread.InheritApprovals), so the
// leaf's request — mirrored up the chain — can lapse at any level
// while that level's step still runs.
func expiryChain(p *pool.Pool, levels int, sibling string) (*core.Agent, *flags) {
	leaf, ran := gatedChild(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"A"}`}),
		wefttest.Say("leaf done"),
	)
	slow := core.Tool("slow", "", func(ctx context.Context, in struct{}) (string, error) {
		select {
		case <-time.After(150 * time.Millisecond):
		case <-ctx.Done():
		}
		return "slow done", nil
	})
	agent, name := leaf, "leaf"
	for lvl := levels - 1; lvl >= 0; lvl-- {
		here := "mid" + string(rune('0'+lvl))
		text := here + " done"
		if lvl == 0 {
			here, text = "lead", "lead done"
		}
		calls := []wefttest.Call{{Name: name, ID: "c" + name, Args: wefttest.Args(struct{ Prompt string }{"go"})}}
		opts := []core.Option{p.MustWrap(name, "", agent)}
		if sibling == here {
			calls = append(calls, wefttest.Call{Name: "slow", ID: "s" + here, Args: `{}`})
			opts = append(opts, slow)
		}
		agent = core.New(wefttest.Script(wefttest.ToolCalls(calls...), wefttest.Say(text)), opts...)
		name = here
	}
	return agent, ran
}

// Under RequestExpiry a delegating call is never offered as an
// ordinary call, never lapses as one and never runs its delegation
// twice — whichever level the leaf's request lapses at first: in a
// middle child whose step still runs (the mirror decided by its expiry
// before the middle parks), in the lead whose step still runs, or
// after every level has parked. Before the fix a middle whose mirrored
// request lapsed before it parked parked with nothing to mirror: the
// lead's Pending offered the delegating call itself (Child ""), the
// middle's later answer failed "does not delegate to child session",
// the lead stayed parked for good, and approving the offered call ran
// the delegation a second time.
func TestNestedExpiryNeverOffersTheDelegatingCall(t *testing.T) {
	cases := []struct {
		name    string
		levels  int
		sibling string
	}{
		{"mid lapses first", 2, "mid1"},
		{"lead lapses first", 2, "lead"},
		{"all parked", 2, ""},
		{"inner mid lapses first", 3, "mid2"},
		{"outer mid lapses first", 3, "mid1"},
	}
	for _, c := range cases {
		for _, approve := range []bool{false, true} {
			name := c.name + map[bool]string{false: "/wait", true: "/approve offered"}[approve]
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				p := pool.New(2)
				defer func() { _ = p.Close(ctx) }()
				lead, ran := expiryChain(p, c.levels, c.sibling)
				s, _ := thread.Create(ctx, thread.Memory(), lead, thread.RequestExpiry(50*time.Millisecond))
				t1, _ := s.Send(ctx, core.User("go"))
				res, err := t1.Wait()
				if err != nil {
					t.Fatalf("lead's first turn: %v", err)
				}
				offered := func() {
					for _, r := range s.Pending() {
						if r.Child == "" {
							t.Errorf("the lead offers its own call %s (%s) as decidable: a delegating call (ADR 0022 §D)", r.CallID, r.Tool)
						}
					}
				}
				offered()
				if approve {
					for _, r := range s.Pending() {
						_ = p.Decide(ctx, s, thread.Approve(r.CallID)) // a lapsed one refuses: ErrExpired
					}
				}
				deadline := time.Now().Add(5 * time.Second)
				for {
					offered()
					rcs := pool.Receipts(s)
					if len(rcs) == 1 && rcs[0].Settled() {
						waitFor(t, p, s, rcs[0].ID, rcs[0].State)
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("the lead's delegation never settled: receipts %+v, pending %+v", rcs, s.Pending())
					}
					_ = p.Decide(ctx, s) // a touch: the lead's expiry sweep, then the pump
					time.Sleep(5 * time.Millisecond)
				}
				if len(res.Pending) > 0 {
					res = followLead(t, continuation(t, t1))
				}
				if res.Text() != "lead done" || len(s.Pending()) > 0 {
					t.Errorf("lead ended %q, %d pending", res.Text(), len(s.Pending()))
				}
				var bad []string
				unterminal(ctx, s.Storage(), s, false, 0, &bad)
				if len(bad) > 0 {
					t.Errorf("unterminal receipts: %v", bad)
				}
				if got := ran.snapshot(); len(got) > 1 || (!approve && len(got) > 0) {
					t.Errorf("the leaf's refund ran %v", got)
				}
			})
		}
	}
}

// hurryModel answers a delegating child's turns from its transcript:
// the task delegates to each of tools in one step, the results are
// answered, and a "hurry" steer — counted — is answered too.
type hurryModel struct {
	tools   []string
	steered *atomic.Int32
}

func (m hurryModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	hurried, answered := false, false
	for _, msg := range req.Messages {
		answered = answered || msg.Role == core.RoleTool
		for _, part := range msg.Content {
			if tp, ok := part.(core.TextPart); ok && msg.Role == core.RoleUser && strings.Contains(tp.Text, "hurry") {
				hurried = true
			}
		}
	}
	var calls []wefttest.Call
	for _, tool := range m.tools {
		calls = append(calls, wefttest.Call{Name: tool, ID: "c" + tool, Args: wefttest.Args(struct{ Prompt string }{"go"})})
	}
	turn := wefttest.ToolCalls(calls...)
	switch {
	case hurried:
		m.steered.Add(1)
		turn = wefttest.Say("hurried")
	case answered:
		turn = wefttest.Say("child done")
	}
	return wefttest.Script(turn).Stream(ctx, req)
}

// A steer Forward sent to a running child that the run never drained
// — Cancel or Close ended the run first — defers to a follow-up turn,
// and that turn never works: it runs on the steer's context, which the
// ending run's cancel ends too. Before the fix the follow-up ran on
// the Forward caller's context while the ending child was still
// ending its parked grandchild, after its run had given its slot up
// (the regain failed under the canceled run): a model call beside the
// pool's bound.
func TestForwardedSteerDoesNotOutliveACanceledRun(t *testing.T) {
	for _, end := range []string{"Cancel", "Close"} {
		t.Run(end, func(t *testing.T) {
			ctx := context.Background()
			p := pool.New(2)
			parked, _ := gatedChild(
				wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"P"}`}),
				wefttest.Say("parked done"),
			)
			started := make(chan struct{})
			var once sync.Once
			block := core.Tool("block", "", func(ctx context.Context, in struct{}) (string, error) {
				once.Do(func() { close(started) })
				<-ctx.Done()
				return "", ctx.Err()
			})
			grand := core.New(wefttest.Script(
				wefttest.ToolCalls(wefttest.Call{Name: "block", Args: `{}`}),
				wefttest.Say("grand done"),
			), block)
			var steered atomic.Int32
			child := core.New(hurryModel{tools: []string{"p", "g"}, steered: &steered},
				p.MustWrap("p", "", parked), p.MustWrap("g", "", grand))
			lead := core.New(wefttest.Script(
				wefttest.ToolCalls(wefttest.Call{Name: "c", ID: "lc", Args: wefttest.Args(struct{ Prompt string }{"go"})}),
				wefttest.Say("lead done"),
			), p.MustWrap("c", "", child))
			s, _ := thread.Create(ctx, thread.Memory(), lead)
			t1, _ := s.Send(ctx, core.User("go"))
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("the grandchild never started")
			}
			rc := pool.Receipts(s)[0]
			deadline := time.Now().Add(5 * time.Second)
			for !hasParked(pool.Receipts(mustOpen(ctx, t, s.Storage(), rc.Child))) {
				if time.Now().After(deadline) {
					t.Fatal("the parked grandchild never parked")
				}
				time.Sleep(time.Millisecond)
			}
			if _, err := p.Forward(ctx, s, rc.ID, core.User("hurry")); err != nil {
				t.Fatalf("Forward: %v", err)
			}
			switch end {
			case "Cancel":
				if err := p.Cancel(ctx, s, rc.ID); err != nil {
					t.Fatal(err)
				}
				waitFor(t, p, s, rc.ID, pool.Canceled)
				if res := followLead(t, t1); res.Text() != "lead done" {
					t.Errorf("lead ended %q", res.Text())
				}
				within(t, 10*time.Second, "Close", func() { _ = p.Close(ctx) })
			case "Close":
				within(t, 10*time.Second, "Close", func() { _ = p.Close(ctx) })
				waitFor(t, p, s, rc.ID, pool.Canceled)
			}
			if n := steered.Load(); n != 0 {
				t.Errorf("the forwarded steer's follow-up called the model %d times after its run was canceled", n)
			}
		})
	}
}

// Close of a middle child waiting on its grandchildren — one parked at
// its approval, one still running — cancels the parked one at its
// boundary with the middle, as the middle's own end would: it settles
// canceled in the middle's ledger, its call never runs. Before the
// fix Close left it parked under a middle that settled canceled:
// nothing could ever resume or settle it (Recover of the lead does not
// descend into a settled middle).
func TestCloseCancelsParkedGrandchildUnderAnEndingMiddle(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	p := pool.New(3)
	parked, ran := gatedChild(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"P"}`}),
		wefttest.Say("parked done"),
	)
	started := make(chan struct{})
	var once sync.Once
	block := core.Tool("block", "", func(ctx context.Context, in struct{}) (string, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return "", ctx.Err()
	})
	busy := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "block", Args: `{}`}),
		wefttest.Say("busy done"),
	), block)
	mid := core.New(wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "parked", ID: "mp", Args: wefttest.Args(struct{ Prompt string }{"p"})},
			wefttest.Call{Name: "busy", ID: "mb", Args: wefttest.Args(struct{ Prompt string }{"b"})},
		),
		wefttest.Say("mid done"),
	), p.MustWrap("parked", "", parked), p.MustWrap("busy", "", busy))
	lead := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "mid", ID: "lm", Args: wefttest.Args(struct{ Prompt string }{"m"})}),
		wefttest.Say("lead done"),
	), p.MustWrap("mid", "", mid))
	s, _ := thread.Create(ctx, st, lead)
	t1, _ := s.Send(ctx, core.User("go"))
	<-started
	rc := pool.Receipts(s)[0]
	deadline := time.Now().Add(5 * time.Second)
	for !hasParked(pool.Receipts(mustOpen(ctx, t, st, rc.Child))) {
		if time.Now().After(deadline) {
			t.Fatal("the grandchild never parked")
		}
		time.Sleep(time.Millisecond)
	}
	within(t, 10*time.Second, "Close", func() {
		if err := p.Close(ctx); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if got := pool.Receipts(s)[0]; got.State != pool.Canceled {
		t.Errorf("mid receipt = %+v, want canceled", got)
	}
	for _, g := range pool.Receipts(mustOpen(ctx, t, st, rc.Child)) {
		if g.State != pool.Canceled {
			t.Errorf("grandchild receipt %s (call %s) reads %s after its middle settled canceled", g.ID, g.Call, g.State)
		}
	}
	if got := ran.snapshot(); len(got) != 0 {
		t.Errorf("the parked grandchild's refund ran %v", got)
	}
	if res := followLead(t, t1); res.Text() != "lead done" {
		t.Errorf("lead ended %q", res.Text())
	}
	_ = s.Close(ctx)
}

// A resumed sync child that Close cancels before its parent's turn has
// parked the delegating call (the parent's step still running a
// sibling) resolves that call once the turn parks it — Close's drain
// waits for it — with SUBAGENT_CANCELED, and logs no warning. Before
// the fix the closed pool's context made the wait return at once: the
// resolution came ahead of the park ("delegating call no longer
// pending" WARN), and the lead stayed parked on the call until a
// Recover.
func TestCloseResolvesACanceledResumeOnceTheParentParks(t *testing.T) {
	ctx := context.Background()
	p := pool.New(2)
	logs := &logLines{}
	release := make(chan struct{})
	child := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"A"}`}),
		wefttest.Say("child done"),
	), core.Tool("refund", "", func(context.Context, struct{ OrderID string }) (string, error) {
		return "refunded", nil
	}, core.RequireApproval()), core.Logger(slog.New(logs)))
	lead := siblingLead(p, child, func(ctx context.Context) (string, error) {
		<-release // the lead is no pool child: Close does not end its step
		return "slow done", nil
	})
	s, _ := thread.Create(ctx, thread.Memory(), lead)
	t1, _ := s.Send(ctx, core.User("go"))
	rc := awaitParked(t, s, 1)[0]
	// Approve the child's request: its resume waits for the lead's turn
	// to park la, and Close cancels it there.
	if err := p.Decide(ctx, s, thread.Approve(s.Pending()[0].CallID)); err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- p.Close(ctx) }()
	time.Sleep(50 * time.Millisecond)
	select {
	case err := <-closed:
		t.Fatalf("Close returned %v before the lead's turn parked the canceled child's call", err)
	default:
	}
	close(release)
	select {
	case err := <-closed:
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close never returned")
	}
	if got := pool.Receipts(s)[0]; got.ID != rc.ID || got.State != pool.Canceled {
		t.Errorf("receipt = %+v, want canceled", got)
	}
	if res := followLead(t, t1); res.Text() != "lead done" {
		t.Errorf("lead ended %q with %d pending", res.Text(), len(res.Pending))
	}
	if got := toolResultsOf(s.Context()); len(got) != 2 || got[0] != canceledA {
		t.Errorf("lead tool results = %v, want la %q", got, canceledA)
	}
	for _, l := range logs.snapshot() {
		if (strings.HasPrefix(l, "WARN") || strings.HasPrefix(l, "ERROR")) && strings.Contains(l, "thread/pool") {
			t.Errorf("log: %s", l)
		}
	}
	_ = s.Close(ctx)
}
