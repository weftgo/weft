package pool_test

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/pool"
)

// fanModel calls each of its tools once in its first step, then says
// "lead done".
type fanModel struct{ tools []string }

func (m fanModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	for _, msg := range req.Messages {
		if msg.Role == core.RoleTool {
			return wefttest.Script(wefttest.Say("lead done")).Stream(ctx, req)
		}
	}
	calls := make([]wefttest.Call, 0, len(m.tools))
	for i, n := range m.tools {
		calls = append(calls, wefttest.Call{Name: n, ID: "c" + string(rune('a'+i)), Args: `{"prompt":"go"}`})
	}
	return wefttest.Script(wefttest.ToolCalls(calls...)).Stream(ctx, req)
}

// Two pools delegating from one parent session: each pool watches the
// parent, so every decision recorded there pumps both. A pool skips
// the receipts whose child another pool watching the parent holds — no
// ErrNoAgent from its Decide, no ERROR from its watch — and each
// delegation completes through its own pool, whichever pool the
// decisions were handed to.
func TestTwoPoolsOneParent(t *testing.T) {
	iters := 100
	if testing.Short() {
		iters = 20
	}
	for it := range iters {
		ctx := context.Background()
		logs := &logLines{}
		pa, pb := pool.New(1), pool.New(1)
		lead := core.New(fanModel{tools: []string{"wa", "wb"}},
			pa.MustWrap("wa", "", asyncLeaf(nil, 1)), pb.MustWrap("wb", "", asyncLeaf(nil, 1)),
			core.Logger(slog.New(logs)))
		s, err := thread.Create(ctx, thread.Memory(), lead)
		if err != nil {
			t.Fatal(err)
		}
		t1, err := s.Send(ctx, core.User("go"))
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for len(s.Pending()) < 2 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		var ds []thread.Decision
		for _, r := range s.Pending() {
			ds = append(ds, thread.Approve(r.CallID))
		}
		if len(ds) != 2 {
			t.Fatalf("iter %d: %d mirrored requests, want both children's", it, len(ds))
		}
		via, other := pa, pb
		if it%2 == 1 {
			via, other = pb, pa
		}
		if err := via.Decide(ctx, s, ds...); err != nil {
			t.Errorf("iter %d: Decide through one pool: %v", it, err)
		}
		if err := other.Decide(ctx, s); err != nil {
			t.Errorf("iter %d: the other pool's pump: %v", it, err)
		}
		for _, r := range pool.Receipts(s) {
			for _, p := range []*pool.Pool{pa, pb} {
				wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				rc, err := p.Wait(wctx, s, r.ID)
				for err == nil && !rc.Settled() {
					time.Sleep(time.Millisecond)
					rc, err = p.Wait(wctx, s, r.ID)
				}
				cancel()
				if err != nil || rc.State != pool.Done {
					t.Errorf("iter %d: Wait %s: %+v %v", it, r.ID, rc, err)
				}
			}
		}
		res, err := t1.Wait()
		for err == nil && len(res.Pending) > 0 {
			n := t1.Next()
			if n == nil {
				break
			}
			t1 = n
			res, err = n.Wait()
		}
		if err != nil || res.Text() != "lead done" {
			t.Errorf("iter %d: the lead ended %v %q; pending %+v receipts %+v", it, err, res.Text(), s.Pending(), pool.Receipts(s))
		}
		_ = pa.Close(ctx)
		_ = pb.Close(ctx)
		_ = s.Close(ctx)
		for _, l := range logs.snapshot() {
			if strings.HasPrefix(l, "ERROR") || strings.HasPrefix(l, "WARN") && strings.Contains(l, "thread/pool") {
				t.Errorf("iter %d: logged %s", it, l)
			}
		}
		if t.Failed() {
			return
		}
	}
}

// The same after a restart: a pool watches the parent from its first
// Recover (or Decide) on it, so the first pool's Recover reports the
// other pool's parked child with ErrNoAgent and leaves it alone; the
// other pool's Recover reattaches it, and from then on each pool skips
// the other's children — a second Recover and every pump return nil,
// and the delegations complete through their own pools.
func TestTwoPoolsOneParentRecover(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	logs := &logLines{}
	leadOver := func(pa, pb *pool.Pool) *core.Agent {
		return core.New(fanModel{tools: []string{"wa", "wb"}},
			pa.MustWrap("wa", "", asyncLeaf(nil, 1)), pb.MustWrap("wb", "", asyncLeaf(nil, 1)),
			core.Logger(slog.New(logs)))
	}
	pa, pb := pool.New(1), pool.New(1)
	s, err := thread.Create(ctx, st, leadOver(pa, pb))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, core.User("go")); err != nil {
		t.Fatal(err)
	}
	awaitParked(t, s, 2)
	deadline := time.Now().Add(5 * time.Second)
	for len(s.Pending()) < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := s.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	_ = pa.Close(ctx)
	_ = pb.Close(ctx)
	_ = s.Close(ctx)

	pa2, pb2 := pool.New(1), pool.New(1)
	s2, err := thread.Open(ctx, st, s.ID(), leadOver(pa2, pb2))
	if err != nil {
		t.Fatal(err)
	}
	if err := pa2.Recover(ctx, s2); err != nil && !errors.Is(err, pool.ErrNoAgent) {
		t.Fatalf("the first pool's Recover: %v", err)
	}
	if err := pb2.Recover(ctx, s2); err != nil {
		t.Fatalf("the second pool's Recover: %v", err)
	}
	if err := pa2.Recover(ctx, s2); err != nil {
		t.Fatalf("the first pool's second Recover: %v", err)
	}
	var ds []thread.Decision
	for _, r := range s2.Pending() {
		ds = append(ds, thread.Approve(r.CallID))
	}
	if len(ds) != 2 {
		t.Fatalf("%d requests offered after the restart, want both children's: %+v", len(ds), s2.Pending())
	}
	if err := pa2.Decide(ctx, s2, ds...); err != nil {
		t.Errorf("Decide through one pool: %v", err)
	}
	if err := pb2.Decide(ctx, s2); err != nil {
		t.Errorf("the other pool's pump: %v", err)
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, r := range pool.Receipts(s2) {
		owner := pa2 // a sync delegation's Wait promises its parent's resume armed: its own pool's
		if r.Call == "cb" {
			owner = pb2
		}
		rc, err := owner.Wait(wctx, s2, r.ID)
		for err == nil && !rc.Settled() {
			time.Sleep(time.Millisecond)
			rc, err = owner.Wait(wctx, s2, r.ID)
		}
		if err != nil || rc.State != pool.Done {
			t.Errorf("Wait %s: %+v %v", r.ID, rc, err)
		}
	}
	if err := s2.WaitIdle(wctx); err != nil {
		t.Fatal(err)
	}
	if last := lastText(s2); last != "lead done" || len(s2.Pending()) > 0 {
		t.Errorf("the reopened lead ended %q with %d pending; receipts %+v", last, len(s2.Pending()), pool.Receipts(s2))
	}
	_ = pa2.Close(ctx)
	_ = pb2.Close(ctx)
	_ = s2.Close(ctx)
	for _, l := range logs.snapshot() {
		if strings.HasPrefix(l, "ERROR") || strings.HasPrefix(l, "WARN") && strings.Contains(l, "thread/pool") {
			t.Errorf("logged %s", l)
		}
	}
}
