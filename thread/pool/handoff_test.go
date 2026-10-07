package pool_test

import (
	"context"
	"errors"
	"fmt"
	"iter"
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

// stateless is a Model that answers from the request alone, so one
// agent can serve any number of sessions at once — a scripted model
// is positional, and a fan-out shares the agent. It also mints
// distinct call ids: wefttest.Script's call_1-per-step hides every
// bug that needs two ids to tell apart.
type stateless struct {
	answer func(req core.ModelRequest) []core.ModelEvent
	gauge  *gauge
}

func (m stateless) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return func(yield func(core.ModelEvent, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		if m.gauge != nil {
			m.gauge.enter()
			defer m.gauge.leave()
		}
		for _, ev := range m.answer(req) {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

// gauge measures how many model calls are in flight at once — the
// work the pool's slots bound.
type gauge struct {
	mu        sync.Mutex
	now, peak int
}

func (g *gauge) enter() {
	g.mu.Lock()
	g.now++
	g.peak = max(g.peak, g.now)
	g.mu.Unlock()
	time.Sleep(200 * time.Microsecond) // widen the overlap a broken bound would show
}

func (g *gauge) leave() {
	g.mu.Lock()
	g.now--
	g.mu.Unlock()
}

func (g *gauge) Peak() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.peak
}

var callSeq atomic.Int64

func usage() core.Usage { return core.Usage{InputTokens: 10, OutputTokens: 5} }

// say is a stateless model that answers text.
func say(g *gauge, text string) stateless {
	return stateless{gauge: g, answer: func(core.ModelRequest) []core.ModelEvent {
		return []core.ModelEvent{
			core.ModelTextDelta{Text: text},
			core.ModelFinish{Reason: core.StopEndTurn, Usage: usage()},
		}
	}}
}

// fanOut is a stateless model that, given a prompt, calls tool n
// times in one step — every call under an id of its own — and, given
// the results, answers with them joined: what the calls returned is
// what the model says, so a test reads the whole tree's results off
// the root.
func fanOut(g *gauge, tool string, n int) stateless {
	return stateless{gauge: g, answer: func(req core.ModelRequest) []core.ModelEvent {
		last := req.Messages[len(req.Messages)-1]
		if last.Role == core.RoleTool {
			var parts []string
			for _, part := range last.Content {
				if tr, ok := part.(core.ToolResultPart); ok {
					parts = append(parts, tr.Content)
				}
			}
			return []core.ModelEvent{
				core.ModelTextDelta{Text: "(" + strings.Join(parts, " ") + ")"},
				core.ModelFinish{Reason: core.StopEndTurn, Usage: usage()},
			}
		}
		var evs []core.ModelEvent
		for i := 0; i < n; i++ {
			evs = append(evs, core.ModelToolCall{
				ID:   fmt.Sprintf("%s-%d", tool, callSeq.Add(1)),
				Name: tool, Args: []byte(`{"prompt":"go"}`),
			})
		}
		return append(evs, core.ModelFinish{Reason: core.StopToolCalls, Usage: usage()})
	}}
}

// within fails the test when fn has not returned in time — the
// deadline that turns a deadlock into a failure instead of a hung
// test binary.
func within(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not finish within %v: the pool is deadlocked", what, d)
	}
}

// tree builds a delegation chain depth levels deep under one pool,
// every level calling the next fan times in one step: the root tool
// to hand a parent, and the answer the root reads when all of it ran.
func tree(p *pool.Pool, g *gauge, depth, fan int) (root *core.ToolDef, rootAgent *core.Agent, answer string) {
	agent := core.New(say(g, "leaf"))
	answer = "leaf"
	for level := depth; level >= 1; level-- {
		name := fmt.Sprintf("level%d", level)
		tool := p.MustWrap(name, "", agent)
		if level == 1 {
			return tool, agent, answer
		}
		agent = core.New(fanOut(g, name, fan), tool)
		answer = "(" + strings.TrimSuffix(strings.Repeat(answer+" ", fan), " ") + ")"
	}
	panic("unreachable")
}

// Fan-out times depth never deadlocks the pool, on any max (the P1):
// every sync level used to hold its slot while it waited, so three
// children each delegating once filled New(3) with waiters and none
// of their children could ever start. A waiting parent now holds no
// slot — and the bound still holds: never more than max child runs at
// work at once.
func TestHandoffNoDeadlock(t *testing.T) {
	const fan = 3
	for _, max := range []int{1, 3} {
		for _, depth := range []int{2, 3} {
			t.Run(fmt.Sprintf("sync/max%d/depth%d", max, depth), func(t *testing.T) {
				ctx := context.Background()
				g := &gauge{}
				p := pool.New(max)
				tool, _, answer := tree(p, g, depth, fan)
				// The parent is no pool child: its own model calls are
				// not the pool's to count.
				parent := core.New(fanOut(nil, "level1", fan), tool)
				s, err := thread.Create(ctx, thread.Memory(), parent)
				if err != nil {
					t.Fatal(err)
				}
				turn, err := s.Send(ctx, core.User("go"))
				if err != nil {
					t.Fatal(err)
				}
				var res *core.RunResult
				within(t, 30*time.Second, "the delegating turn", func() { res, err = turn.Wait() })
				if err != nil {
					t.Fatalf("Wait: %v", err)
				}
				want := "(" + strings.TrimSuffix(strings.Repeat(answer+" ", fan), " ") + ")"
				if res.Text() != want {
					t.Fatalf("the tree's answer = %q, want %q", res.Text(), want)
				}
				rs := pool.Receipts(s)
				if len(rs) != fan {
					t.Fatalf("receipts = %+v, want %d", rs, fan)
				}
				for _, r := range rs {
					if r.State != pool.Done {
						t.Errorf("receipt %s = %s (%s)", r.ID, r.State, r.Stop)
					}
				}
				if peak := g.Peak(); peak > max {
					t.Errorf("%d child runs at work at once under pool.New(%d)", peak, max)
				}
				within(t, 10*time.Second, "Close", func() { err = p.Close(ctx) })
				if err != nil {
					t.Fatalf("Close: %v", err)
				}
			})
			t.Run(fmt.Sprintf("async/max%d/depth%d", max, depth), func(t *testing.T) {
				ctx := context.Background()
				g := &gauge{}
				p := pool.New(max)
				// The submitted children are the tree's first level:
				// each delegates on, sync, below it.
				_, top, answer := tree(p, g, depth, fan)
				s, err := thread.Create(ctx, thread.Memory(), core.New(wefttest.Script()))
				if err != nil {
					t.Fatal(err)
				}
				var ids []string
				for i := 0; i < fan; i++ {
					r, err := p.Submit(ctx, s, top, "go")
					if err != nil {
						t.Fatalf("Submit: %v", err)
					}
					ids = append(ids, r.ID)
				}
				wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				for _, id := range ids {
					rc, err := p.Wait(wctx, s, id)
					if err != nil {
						t.Fatalf("Wait(%s): %v — the pool is deadlocked; receipts %+v", id, err, pool.Receipts(s))
					}
					if rc.State != pool.Done || rc.Stop != answer {
						t.Errorf("receipt = %+v, want done with %q", rc, answer)
					}
				}
				if peak := g.Peak(); peak > max {
					t.Errorf("%d child runs at work at once under pool.New(%d)", peak, max)
				}
				within(t, 10*time.Second, "Close", func() { err = p.Close(ctx) })
				if err != nil {
					t.Fatalf("Close: %v", err)
				}
			})
		}
	}
}

// The same hand-off on the bare path: wrapped tools called outside
// any session nest under one slot each, and a waiting outer call
// holds none.
func TestHandoffBarePath(t *testing.T) {
	ctx := context.Background()
	g := &gauge{}
	p := pool.New(1)
	tool, _, answer := tree(p, g, 3, 2)
	parent := core.New(fanOut(nil, "level1", 2), tool)
	var res *core.RunResult
	var err error
	within(t, 30*time.Second, "the bare Generate", func() { res, err = parent.Generate(ctx, core.Prompt("go")) })
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if want := "(" + answer + " " + answer + ")"; res.Text() != want {
		t.Fatalf("answer = %q, want %q", res.Text(), want)
	}
	if peak := g.Peak(); peak > 1 {
		t.Errorf("%d child runs at work at once under pool.New(1)", peak)
	}
}

// Admission is first in, first out, in the order work was accepted —
// the queue's own order, not the scheduler's: Submit takes its place
// before it returns, so three children queued behind a busy slot
// start in the order they were submitted, every time.
func TestFIFO(t *testing.T) {
	ctx := context.Background()
	s, _ := thread.Create(ctx, thread.Memory(), core.New(wefttest.Script()))
	p := pool.New(1)
	const queued = 6
	var mu sync.Mutex
	var started []int
	releases := make([]chan struct{}, queued+1)
	for i := range releases {
		releases[i] = make(chan struct{})
	}
	submit := func(i int) *pool.Receipt {
		mdl := blocking{release: releases[i], text: fmt.Sprintf("child %d", i),
			onStart: func() {
				mu.Lock()
				started = append(started, i)
				mu.Unlock()
			}}
		r, err := p.Submit(ctx, s, core.New(mdl), "go")
		if err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
		return r
	}
	rs := []*pool.Receipt{submit(0)} // holds the only slot
	waitStarted(t, &mu, &started, 1)
	for i := 1; i <= queued; i++ {
		rs = append(rs, submit(i))
	}
	mu.Lock()
	if len(started) != 1 {
		mu.Unlock()
		t.Fatalf("queued children started: %v", started)
	}
	mu.Unlock()
	for i := 0; i <= queued; i++ {
		waitStarted(t, &mu, &started, i+1)
		mu.Lock()
		got := started[i]
		mu.Unlock()
		if got != i {
			t.Fatalf("admission %d was child %d: %v — not first in, first out", i, got, started)
		}
		close(releases[i])
		if rc, err := p.Wait(ctx, s, rs[i].ID); err != nil || rc.State != pool.Done {
			t.Fatalf("child %d: %+v, %v", i, rc, err)
		}
	}
	if err := p.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// lastToolResult returns the last tool result in msgs.
func lastToolResult(msgs []core.Message) string {
	out := ""
	for _, m := range msgs {
		for _, part := range m.Content {
			if tp, ok := part.(core.ToolResultPart); ok {
				out = tp.Content
			}
		}
	}
	return out
}

// The depth limit is its own bound with its own, accurate text: a
// chain of three distinct agents is no cycle, runs on one slot, and
// is refused only where it would exceed MaxDepth — with
// SUBAGENT_DEPTH naming the depth and the limit, never the
// SUBAGENT_CYCLE the old max-as-depth guard reported for A→B→C.
func TestDepthLimit(t *testing.T) {
	ctx := context.Background()
	run := func(t *testing.T, opts ...pool.Option) string {
		p := pool.New(1, opts...)
		tool, _, _ := tree(p, nil, 3, 1)
		s, err := thread.Create(ctx, thread.Memory(), core.New(fanOut(nil, "level1", 1), tool))
		if err != nil {
			t.Fatal(err)
		}
		turn, err := s.Send(ctx, core.User("go"))
		if err != nil {
			t.Fatal(err)
		}
		var res *core.RunResult
		within(t, 30*time.Second, "the chain", func() { res, err = turn.Wait() })
		if err != nil {
			t.Fatal(err)
		}
		return res.Text()
	}
	if got := run(t); got != "(((leaf)))" {
		t.Errorf("three levels on pool.New(1), default depth: %q, want the leaf's answer", got)
	}
	if got := run(t, pool.MaxDepth(3)); got != "(((leaf)))" {
		t.Errorf("three levels under MaxDepth(3): %q", got)
	}
	// MaxDepth(2): the third level is refused where it is asked for —
	// inside the second-level child — and the refusal is data its
	// model reads and reports up.
	want := "(((SUBAGENT_DEPTH: delegation depth 3 exceeds the pool's limit 2)))"
	if got := run(t, pool.MaxDepth(2)); got != want {
		t.Errorf("three levels under MaxDepth(2):\n got %q\nwant %q", got, want)
	}
	if strings.Contains(run(t, pool.MaxDepth(2)), "SUBAGENT_CYCLE") {
		t.Error("a depth refusal reported a cycle")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("MaxDepth(0) did not panic")
			}
		}()
		pool.New(1, pool.MaxDepth(0))
	}()
}

// SUBAGENT_CYCLE is for cycles, and the pool checks the real
// ancestry: the core's guard lives in the Subagent handler the
// middleware never calls, so an agent wrapping itself used to recurse
// all the way to the depth limit.
func TestCycle(t *testing.T) {
	ctx := context.Background()
	t.Run("self", func(t *testing.T) {
		p := pool.New(2)
		var self *core.ToolDef
		var runs atomic.Int64
		model := fanOut(nil, "again", 1)
		counted := stateless{answer: func(req core.ModelRequest) []core.ModelEvent {
			if len(req.Messages) == 1 {
				runs.Add(1)
			}
			return model.answer(req)
		}}
		a := core.New(counted, core.ToolSource(func() []*core.ToolDef { return []*core.ToolDef{self} }))
		self = p.MustWrap("again", "", a)
		s, _ := thread.Create(ctx, thread.Memory(), a)
		turn, err := s.Send(ctx, core.User("go"))
		if err != nil {
			t.Fatal(err)
		}
		res, err := turn.Wait()
		if err != nil {
			t.Fatal(err)
		}
		want := `(SUBAGENT_CYCLE: agent "again" is already running in this call chain)`
		if res.Text() != want {
			t.Errorf("a self-delegation:\n got %q\nwant %q", res.Text(), want)
		}
		if n := runs.Load(); n != 1 {
			t.Errorf("the agent ran %d times; the cycle must be refused before any child starts", n)
		}
		if rs := pool.Receipts(s); len(rs) != 0 {
			t.Errorf("a refused cycle left receipts: %+v", rs)
		}
	})
	t.Run("A-B-A", func(t *testing.T) {
		p := pool.New(2)
		var toA *core.ToolDef
		b := core.New(fanOut(nil, "to_a", 1), core.ToolSource(func() []*core.ToolDef { return []*core.ToolDef{toA} }))
		a := core.New(fanOut(nil, "to_b", 1), p.MustWrap("to_b", "", b))
		toA = p.MustWrap("to_a", "", a)
		s, _ := thread.Create(ctx, thread.Memory(), a)
		turn, _ := s.Send(ctx, core.User("go"))
		res, err := turn.Wait()
		if err != nil {
			t.Fatal(err)
		}
		want := `((SUBAGENT_CYCLE: agent "to_a" is already running in this call chain))`
		if res.Text() != want {
			t.Errorf("A→B→A:\n got %q\nwant %q", res.Text(), want)
		}
	})
	t.Run("Submit", func(t *testing.T) {
		// Submit from inside a run counts from that run: the same two
		// refusals, as Go errors.
		p := pool.New(2, pool.MaxDepth(1))
		var agent *core.Agent
		other := core.New(say(nil, "other"))
		var cycleErr, depthErr error
		probe := core.Tool("probe", "", func(ctx context.Context, _ struct{}) (string, error) {
			s := thread.SessionFromContext(ctx)
			_, cycleErr = p.Submit(ctx, s, agent, "again")
			_, depthErr = p.Submit(ctx, s, other, "deeper")
			return "probed", nil
		})
		agent = core.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "probe", ID: "c-probe"}),
			wefttest.Say("done"),
		), probe)
		s, _ := thread.Create(ctx, thread.Memory(), core.New(wefttest.Script()))
		r, err := p.Submit(ctx, s, agent, "go")
		if err != nil {
			t.Fatal(err)
		}
		if rc, err := p.Wait(ctx, s, r.ID); err != nil || rc.State != pool.Done {
			t.Fatalf("the probing child: %+v, %v", rc, err)
		}
		if !errors.Is(cycleErr, pool.ErrCycle) {
			t.Errorf("Submit of the running agent: %v, want ErrCycle", cycleErr)
		}
		if !errors.Is(depthErr, pool.ErrDepth) {
			t.Errorf("Submit past MaxDepth(1): %v, want ErrDepth", depthErr)
		}
	})
}
