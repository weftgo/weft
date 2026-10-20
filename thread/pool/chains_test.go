package pool_test

import (
	"context"
	"fmt"
	"iter"
	"math/rand"
	"os"
	"runtime"
	"strconv"
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
// closed (Close leaves a parked child parked).
func unterminal(ctx context.Context, st thread.Storage, s *thread.Session, closed bool, lvl int, out *[]string) {
	for _, r := range pool.Receipts(s) {
		if !atRest(r, closed) {
			*out = append(*out, fmt.Sprintf("L%d receipt in L%d %s: %s (%s)", lvl+1, lvl, s.ID(), r.State, r.Stop))
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

// Random delegation chains — depth 2 to 4, fan-out 1 to 3, leaves that
// park once or twice, one or two chains on a pool of one or two — are
// driven to their end by deciding whatever the leads offer (a quarter
// denied), and in three iterations of ten something ends them half-way:
// a Cancel of a random lead receipt, or the pool's Close. Every
// iteration must hold the pool's promises: every Wait returns; the
// leads' turns end "lead done" (never parked with nothing left to
// resume them) unless the pool closed; model calls in flight never
// exceed the bound; once the leads are done and the pool closed every
// receipt at every depth is settled (a parked one only after Close);
// and no goroutine is left behind.
//
// POOL_CHAINS_ITERS sets the iteration count (default 30, 10 under
// -short), POOL_CHAINS_SEED the seed (the log names the one used; the
// decisions' timing still varies run to run), POOL_CHAINS_NOCANCEL=1
// turns the Cancel iterations into plain ones.
func TestNestedRandomChains(t *testing.T) {
	iters := envInt("POOL_CHAINS_ITERS", 30)
	if testing.Short() && os.Getenv("POOL_CHAINS_ITERS") == "" {
		iters = 10
	}
	seed := envInt("POOL_CHAINS_SEED", time.Now().UnixNano())
	t.Logf("seed %d, %d iterations", seed, iters)
	rng := rand.New(rand.NewSource(seed))
	for it := range iters {
		ctx := context.Background()
		before := runtime.NumGoroutine()
		max := 1 + rng.Intn(2)
		depth := 2 + rng.Intn(3)
		p := pool.New(max)
		g := &gauge{}
		st := thread.Memory()
		type chain struct {
			s    *thread.Session
			t1   *thread.Turn
			desc string
		}
		var chains []chain
		for c := range 1 + rng.Intn(2) {
			lead, desc := randomChain(p, rng, depth, g, "_"+strconv.Itoa(c))
			s, err := thread.Create(ctx, st, lead)
			if err != nil {
				t.Fatal(err)
			}
			t1, err := s.Send(ctx, core.User("go"))
			if err != nil {
				t.Fatal(err)
			}
			chains = append(chains, chain{s, t1, desc})
		}
		mode := rng.Intn(10) // 0: Close half-way; 1, 2: Cancel a lead receipt half-way; else decide to the end
		if os.Getenv("POOL_CHAINS_NOCANCEL") != "" && (mode == 1 || mode == 2) {
			mode = 5
		}
		where := func() string {
			return fmt.Sprintf("iter %d (%s, max %d, mode %d)", it, chains[0].desc, max, mode)
		}
		closed, acted := false, false
		deadline := time.Now().Add(30 * time.Second)
		for round := 0; ; {
			if time.Now().After(deadline) {
				buf := make([]byte, 1<<21)
				t.Fatalf("%s: no progress; receipts %+v\n%s", where(), pool.Receipts(chains[0].s), buf[:runtime.Stack(buf, true)])
			}
			allDone := true
			for _, c := range chains {
				rcs := pool.Receipts(c.s)
				if len(rcs) == 0 {
					allDone = false
					continue
				}
				for _, r := range rcs {
					if !atRest(r, closed) {
						allDone = false
					}
				}
				pend := c.s.Pending()
				if len(pend) == 0 || closed {
					continue
				}
				var ds []thread.Decision
				for _, r := range pend {
					if rng.Intn(4) == 0 {
						ds = append(ds, thread.Deny(r.CallID, "no"))
					} else {
						ds = append(ds, thread.Approve(r.CallID))
					}
				}
				if err := p.Decide(ctx, c.s, ds...); err != nil {
					t.Logf("%s: Decide: %v", where(), err)
				}
				round++
				if acted || round < 1+rng.Intn(3) {
					continue
				}
				time.Sleep(time.Duration(rng.Intn(3000)) * time.Microsecond)
				switch {
				case mode == 0:
					acted, closed = true, true
					within(t, 40*time.Second, "Close", func() {
						if err := p.Close(ctx); err != nil {
							t.Errorf("%s: Close: %v", where(), err)
						}
					})
				case mode <= 2:
					acted = true
					r := rcs[rng.Intn(len(rcs))]
					if err := p.Cancel(ctx, c.s, r.ID); err != nil {
						t.Logf("%s: Cancel %s at %s: %v", where(), r.ID, r.State, err)
					}
				}
			}
			if allDone {
				break
			}
			time.Sleep(time.Millisecond)
		}
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
			t.Errorf("%s: model calls in flight peaked at %d on a pool of %d", where(), peak, max)
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
	}
}
