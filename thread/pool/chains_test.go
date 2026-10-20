package pool_test

import (
	"context"
	"fmt"
	"iter"
	"math/rand"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/pool"
)

// chainModel decides its turn from the transcript: before rounds
// tool-result rounds it calls tool fan times in one step, then it says
// text. A gauge, when set, counts its calls in flight.
type chainModel struct {
	tool   string
	fan    int
	rounds int
	text   string
	args   string
	g      *gauge
}

func (m chainModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	if m.g != nil {
		m.g.enter()
	}
	results := 0
	for _, msg := range req.Messages {
		if msg.Role == core.RoleTool {
			results++
		}
	}
	turn := wefttest.Say(m.text)
	if results < m.rounds {
		calls := make([]wefttest.Call, 0, m.fan)
		for i := range m.fan {
			calls = append(calls, wefttest.Call{Name: m.tool, ID: fmt.Sprintf("c%d_%d", results, i), Args: m.args})
		}
		turn = wefttest.ToolCalls(calls...)
	}
	inner := wefttest.Script(turn).Stream(ctx, req)
	return func(yield func(core.ModelEvent, error) bool) {
		if m.g != nil {
			defer m.g.leave()
		}
		for ev, err := range inner {
			if !yield(ev, err) {
				return
			}
		}
	}
}

// randomChain builds a lead over depth-1 levels of sync wraps of p, each
// level fanning out 1–2 (1–3 on a short chain) calls of the next, over
// a leaf whose refund asks 1–2 approvals in a row. Every pool child's
// model calls are counted by g; the lead is no pool child.
func randomChain(p *pool.Pool, rng *rand.Rand, depth int, g *gauge, sfx string) (*core.Agent, string) {
	refund := core.Tool("refund", "", func(context.Context, struct{ OrderID string }) (string, error) {
		if time.Now().UnixNano()%3 == 0 {
			time.Sleep(time.Millisecond)
		}
		return "refunded", nil
	}, core.RequireApproval())
	reparks := 1 + rng.Intn(2)
	agent := core.New(chainModel{tool: "refund", fan: 1, rounds: reparks, text: "leaf done", g: g, args: `{"order_id":"B"}`}, refund)
	desc := fmt.Sprintf("leaf(reparks=%d)", reparks)
	for lvl := depth - 1; lvl >= 1; lvl-- {
		fan := 1 + rng.Intn(2)
		if depth <= 2 {
			fan = 1 + rng.Intn(3)
		}
		name := "w" + strconv.Itoa(lvl) + sfx
		agent = core.New(chainModel{tool: name, fan: fan, rounds: 1, text: "mid done " + strconv.Itoa(lvl), g: g,
			args: wefttest.Args(struct{ Prompt string }{"go"})}, p.MustWrap(name, "", agent))
		desc = fmt.Sprintf("L%d(fan=%d) > %s", lvl, fan, desc)
	}
	fan := 1 + rng.Intn(2)
	name := "w0" + sfx
	lead := core.New(chainModel{tool: name, fan: fan, rounds: 1, text: "lead done",
		args: wefttest.Args(struct{ Prompt string }{"go"})}, p.MustWrap(name, "", agent))
	return lead, fmt.Sprintf("lead(fan=%d) > %s", fan, desc)
}

// unterminal walks s's delegation tree in st and lists every receipt
// that is not settled — a parked one allowed only when the pool was
// closed (Close leaves a parked child parked) — and every delegating
// call that two receipts of one session name (a delegation that ran
// twice).
func unterminal(ctx context.Context, st thread.Storage, s *thread.Session, closed bool, lvl int, out *[]string) {
	calls := map[string]string{}
	for _, r := range pool.Receipts(s) {
		if !atRest(r, closed) {
			*out = append(*out, fmt.Sprintf("L%d receipt in L%d %s: %s (%s)", lvl+1, lvl, s.ID(), r.State, r.Stop))
		}
		if r.Call != "" {
			if prev, ok := calls[r.Call]; ok {
				*out = append(*out, fmt.Sprintf("call %s in L%d %s delegated twice: receipts %s and %s", r.Call, lvl, s.ID(), prev, r.ID))
			}
			calls[r.Call] = r.ID
		}
		c, err := thread.Open(ctx, st, r.Child, core.New(wefttest.Script()))
		if err != nil {
			*out = append(*out, "open "+r.Child+": "+err.Error())
			continue
		}
		unterminal(ctx, st, c, closed, lvl+1, out)
	}
}

// atRest reports whether a receipt is where a finished chain leaves
// it: settled, or parked when the pool was closed.
func atRest(r pool.Receipt, closed bool) bool {
	return r.Settled() || (closed && r.State == pool.Parked)
}

// envInt reads an integer knob of the chains test.
func envInt(name string, def int64) int64 {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

// asyncLeaf is a child for Submit that parks once or twice before it
// answers, its model calls counted by g.
func asyncLeaf(g *gauge, reparks int) *core.Agent {
	refund := core.Tool("refund", "", func(context.Context, struct{ OrderID string }) (string, error) {
		return "refunded", nil
	}, core.RequireApproval())
	return core.New(chainModel{tool: "refund", fan: 1, rounds: reparks, text: "async done", g: g, args: `{"order_id":"Z"}`}, refund)
}

// dumpSession logs a session's entries, for a failing iteration.
func dumpSession(t *testing.T, label string, s *thread.Session) {
	t.Logf("=== %s %s", label, s.ID())
	for _, e := range s.Entries() {
		line := fmt.Sprintf("%T %+v", e, e)
		if len(line) > 400 {
			line = line[:400]
		}
		t.Logf("  %s", line)
	}
}

// The modes of TestNestedRandomChains: what ends an iteration's
// chains half-way, if anything.
const (
	chainsClose   = iota // the pool's Close, racing a Decide
	chainsCancel         // a Cancel of a lead receipt, racing a Decide on the same lead
	chainsRestart        // Close, the leads closed and reopened on a second pool, Recover, then on to the end
	chainsDecide         // decide to the end
)

var chainsModeNames = [...]string{"close", "cancel", "restart", "decide"}

// Random delegation chains — depth 2 to 4, fan-out 1 to 3, leaves that
// park once or twice, one or two chains on a pool of one or two, zero
// to two Submit children of each lead beside them, a request expiry of
// 3–42 ms on one iteration in six (inherited down every chain), a
// Forward to a running receipt now and then — are driven to their end
// by deciding whatever the leads offer (a quarter denied). In some
// iterations something ends or interrupts them half-way: the pool's
// Close racing a Decide; a Cancel of a random lead receipt racing a
// Decide on the same lead; or a restart — Close, the leads closed,
// reopened on a second pool that Recovers them, and driven on to the
// end there. Every iteration must hold the pool's promises: every Wait
// returns, and a Cancel returns promptly; the leads end "lead done"
// (never parked with nothing left to resume them) unless the pool
// closed for good; model calls in flight never exceed the bound; once
// the leads are done and the pool closed every receipt at every depth
// is settled (a parked one only after a final Close), and no session
// holds two receipts for one delegating call (a delegation never runs
// twice); and no goroutine is left behind. The last line logs how many
// iterations ran in each mode, and with expiry, Submit children and
// Forward.
//
// Knobs: POOL_CHAINS_ITERS the iteration count (default 30, 10 under
// -short); POOL_CHAINS_SEED the seed (the log names the one used; the
// decisions' timing still varies run to run); POOL_CHAINS_MODE one
// mode for every iteration (close, cancel, restart, decide);
// POOL_CHAINS_EXPIRY=1 an expiry on every iteration, =0 on none;
// POOL_CHAINS_NOFORWARD=1 no Forward; POOL_CHAINS_NOASYNC=1 no Submit
// children; POOL_CHAINS_NOCANCEL=1 turns the cancel iterations into
// plain ones.
func TestNestedRandomChains(t *testing.T) {
	iters := envInt("POOL_CHAINS_ITERS", 30)
	if testing.Short() && os.Getenv("POOL_CHAINS_ITERS") == "" {
		iters = 10
	}
	seed := envInt("POOL_CHAINS_SEED", time.Now().UnixNano())
	t.Logf("seed %d, %d iterations", seed, iters)
	rng := rand.New(rand.NewSource(seed))
	var rmu sync.Mutex // rng is shared with the racing goroutines
	rint := func(n int) int { rmu.Lock(); defer rmu.Unlock(); return rng.Intn(n) }
	fixedMode := -1
	if m := os.Getenv("POOL_CHAINS_MODE"); m != "" {
		for i, name := range chainsModeNames {
			if name == m {
				fixedMode = i
			}
		}
		if fixedMode < 0 {
			t.Fatalf("POOL_CHAINS_MODE=%q: want one of %v", m, chainsModeNames)
		}
	}
	var modes [len(chainsModeNames)]int
	var withExpiry, withAsync, withForward, passed int
	defer func() {
		t.Logf("modes: close %d, cancel %d, restart %d, decide %d; expiry %d, async %d, forward %d; %d of %d iterations passed",
			modes[chainsClose], modes[chainsCancel], modes[chainsRestart], modes[chainsDecide],
			withExpiry, withAsync, withForward, passed, iters)
	}()
	for it := range iters {
		ctx := context.Background()
		before := runtime.NumGoroutine()
		max := 1 + rint(2)
		depth := 2 + rint(3)
		p := pool.New(max)
		g := &gauge{limit: max}
		st := thread.Memory()
		expiry := time.Duration(0)
		switch os.Getenv("POOL_CHAINS_EXPIRY") {
		case "1":
			expiry = time.Duration(3+rint(40)) * time.Millisecond
		case "0":
		default:
			if rint(6) == 0 {
				expiry = time.Duration(3+rint(40)) * time.Millisecond
			}
		}
		mode := chainsDecide
		switch r := rint(10); {
		case r == 0:
			mode = chainsClose
		case r <= 2:
			mode = chainsCancel
		case r == 3:
			mode = chainsRestart
		}
		if fixedMode >= 0 {
			mode = fixedMode
		}
		if os.Getenv("POOL_CHAINS_NOCANCEL") != "" && mode == chainsCancel {
			mode = chainsDecide
		}
		modes[mode]++
		if expiry > 0 {
			withExpiry++
		}
		type chain struct {
			s     *thread.Session
			t1    *thread.Turn // nil once the lead was reopened: read its end off the session
			desc  string
			cseed int64
			async map[string]int // Submit receipt → the child's reparks
		}
		var chains []chain
		var opts []thread.SessionOption
		if expiry > 0 {
			opts = append(opts, thread.RequestExpiry(expiry))
		}
		forwarded, async := false, false
		for c := range 1 + rint(2) {
			rmu.Lock()
			cseed := rng.Int63()
			rmu.Unlock()
			lead, desc := randomChain(p, rand.New(rand.NewSource(cseed)), depth, g, "_"+strconv.Itoa(c))
			s, err := thread.Create(ctx, st, lead, opts...)
			if err != nil {
				t.Fatal(err)
			}
			t1, err := s.Send(ctx, core.User("go"))
			if err != nil {
				t.Fatal(err)
			}
			ch := chain{s: s, t1: t1, desc: desc, cseed: cseed, async: map[string]int{}}
			if os.Getenv("POOL_CHAINS_NOASYNC") == "" {
				for range rint(3) {
					reparks := 1 + rint(2)
					rc, err := p.Submit(ctx, s, asyncLeaf(g, reparks), "async")
					if err != nil {
						t.Fatalf("Submit: %v", err)
					}
					ch.async[rc.ID] = reparks
					async = true
				}
			}
			chains = append(chains, ch)
		}
		if async {
			withAsync++
		}
		where := func() string {
			return fmt.Sprintf("iter %d (%s, max %d, %s, expiry %v, async %d)",
				it, chains[0].desc, max, chainsModeNames[mode], expiry, len(chains[0].async))
		}
		closed, acted, restarted := false, false, false
		var bg sync.WaitGroup
		deadline := time.Now().Add(30 * time.Second)
		for round := 0; ; {
			if time.Now().After(deadline) {
				for _, c := range chains {
					dumpSession(t, "lead", c.s)
				}
				buf := make([]byte, 1<<21)
				t.Fatalf("%s: no progress; receipts %+v pending %+v\n%s", where(), pool.Receipts(chains[0].s), chains[0].s.Pending(),
					buf[:runtime.Stack(buf, true)])
			}
			allDone, delegated := true, true
			for ci := range chains {
				c := &chains[ci]
				rcs := pool.Receipts(c.s)
				if len(rcs) == len(c.async) && !leadEnded(c.s) {
					// The lead's first turn has not delegated yet.
					delegated = false
					if !closed {
						allDone = false
					}
				}
				for _, r := range rcs {
					if !atRest(r, closed) {
						allDone = false
					}
				}
				if len(rcs) > 0 && rint(8) == 0 && os.Getenv("POOL_CHAINS_NOFORWARD") == "" {
					if r := rcs[rint(len(rcs))]; r.State == pool.Running && !closed {
						if _, err := p.Forward(ctx, c.s, r.ID, core.User("hurry")); err == nil {
							forwarded = true
						}
					}
				}
				pend := c.s.Pending()
				if len(pend) == 0 || closed {
					continue
				}
				var ds []thread.Decision
				for _, r := range pend {
					if rint(4) == 0 {
						ds = append(ds, thread.Deny(r.CallID, "no"))
					} else {
						ds = append(ds, thread.Approve(r.CallID))
					}
				}
				round++
				if acted || round < 1+rint(3) || mode == chainsDecide || (mode == chainsRestart && !delegated) {
					if err := p.Decide(ctx, c.s, ds...); err != nil {
						t.Logf("%s: Decide: %v", where(), err)
					}
					continue
				}
				acted = true
				switch mode {
				case chainsClose:
					closed = true
					bg.Add(1)
					go func() {
						defer bg.Done()
						time.Sleep(time.Duration(rint(500)) * time.Microsecond)
						_ = p.Decide(ctx, c.s, ds...)
					}()
					within(t, 40*time.Second, "Close", func() {
						if err := p.Close(ctx); err != nil {
							t.Errorf("%s: Close: %v", where(), err)
						}
					})
				case chainsCancel:
					r := rcs[rint(len(rcs))]
					bg.Add(1)
					go func() {
						defer bg.Done()
						if err := p.Decide(ctx, c.s, ds...); err != nil {
							t.Logf("%s: racing Decide: %v", where(), err)
						}
					}()
					time.Sleep(time.Duration(rint(300)) * time.Microsecond)
					cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
					start := time.Now()
					if err := p.Cancel(cctx, c.s, r.ID); err != nil {
						t.Logf("%s: Cancel %s at %s: %v", where(), r.ID, r.State, err)
					}
					if d := time.Since(start); d > 10*time.Second {
						t.Errorf("%s: Cancel of a %s receipt took %v", where(), r.State, d)
					}
					cancel()
				case chainsRestart:
					if err := p.Decide(ctx, c.s, ds...); err != nil {
						t.Logf("%s: Decide: %v", where(), err)
					}
					time.Sleep(time.Duration(rint(2000)) * time.Microsecond)
					bg.Wait()
					within(t, 40*time.Second, "Close before the restart", func() {
						if err := p.Close(ctx); err != nil {
							t.Errorf("%s: Close: %v", where(), err)
						}
					})
					p2 := pool.New(max)
					g2 := &gauge{limit: max}
					for cj := range chains {
						cc := &chains[cj]
						within(t, 40*time.Second, "lead Close", func() { _ = cc.s.Close(ctx) })
						lead2, _ := randomChain(p2, rand.New(rand.NewSource(cc.cseed)), depth, g2, "_"+strconv.Itoa(cj))
						s2, err := thread.Open(ctx, st, cc.s.ID(), lead2, opts...)
						if err != nil {
							t.Fatalf("%s: reopen: %v", where(), err)
						}
						for _, r := range pool.Receipts(s2) {
							if reparks, ok := cc.async[r.ID]; ok {
								_ = p2.Register(r.Child, asyncLeaf(g2, reparks))
							}
						}
						if err := p2.Recover(ctx, s2); err != nil {
							t.Logf("%s: Recover: %v", where(), err)
						}
						cc.s, cc.t1 = s2, nil
					}
					if peak := g.Peak(); peak > max {
						t.Errorf("%s: model calls in flight peaked at %d on a pool of %d before the restart", where(), peak, max)
					}
					p, g, restarted = p2, g2, true
				}
			}
			if allDone {
				break
			}
			time.Sleep(time.Millisecond)
		}
		if forwarded {
			withForward++
		}
		bg.Wait()
		for _, c := range chains {
			for _, r := range pool.Receipts(c.s) {
				wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				if _, err := p.Wait(wctx, c.s, r.ID); err != nil {
					t.Errorf("%s: Wait %s: %v", where(), r.ID, err)
				}
				cancel()
			}
			if closed {
				continue
			}
			if c.t1 == nil { // reopened: the lead's last turn is the session's
				wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				if err := c.s.WaitIdle(wctx); err != nil {
					t.Errorf("%s: WaitIdle: %v", where(), err)
				}
				cancel()
				if last := lastText(c.s); last != "lead done" || len(c.s.Pending()) > 0 {
					t.Errorf("%s: the reopened lead ended %q with %d calls pending; receipts %+v",
						where(), last, len(c.s.Pending()), pool.Receipts(c.s))
				}
				continue
			}
			res, err := c.t1.Wait()
			for err == nil && res != nil && len(res.Pending) > 0 {
				next := c.t1.Next()
				if next == nil {
					break
				}
				c.t1 = next
				res, err = next.Wait()
			}
			switch {
			case err != nil:
				t.Errorf("%s: lead: %v", where(), err)
			case res.Text() != "lead done":
				t.Errorf("%s: the lead ended %q with %d calls pending and nothing to resume them; receipts %+v",
					where(), res.Text(), len(res.Pending), pool.Receipts(c.s))
			}
		}
		if peak := g.Peak(); peak > max {
			t.Errorf("%s: model calls in flight peaked at %d on a pool of %d (restarted %v)\n%s", where(), peak, max, restarted, g.Stack())
		}
		within(t, 40*time.Second, "Close", func() { _ = p.Close(ctx) })
		for _, c := range chains {
			var bad []string
			unterminal(ctx, st, c.s, closed, 0, &bad)
			if len(bad) > 0 {
				t.Errorf("%s: unterminal receipts: %v", where(), bad)
			}
			_ = c.s.Close(ctx)
		}
		dl := time.Now().Add(5 * time.Second)
		for time.Now().Before(dl) && runtime.NumGoroutine() > before+2 {
			time.Sleep(5 * time.Millisecond)
		}
		if n := runtime.NumGoroutine(); n > before+2 {
			buf := make([]byte, 1<<21)
			t.Fatalf("%s: %d goroutines, %d before:\n%s", where(), n, before, buf[:runtime.Stack(buf, true)])
		}
		if t.Failed() {
			return
		}
		passed++
	}
}

// leadEnded reports whether a lead's last turn ended with nothing
// pending — a lead whose delegations a closed pool refused ends so.
func leadEnded(s *thread.Session) bool {
	for _, e := range slices.Backward(s.Entries()) {
		if te, ok := e.(thread.TurnEntry); ok {
			return len(te.Pending) == 0
		}
	}
	return false
}

// lastText is the text of a session's last message.
func lastText(s *thread.Session) string {
	msgs := s.Context()
	if len(msgs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, part := range msgs[len(msgs)-1].Content {
		if tp, ok := part.(core.TextPart); ok {
			b.WriteString(tp.Text)
		}
	}
	return b.String()
}
