package pool_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/thread/pool"
)

// Crash images: a parked delegation chain on jsonl, its files then cut
// back to a prefix — what a SIGKILL at that instant leaves, every
// session file being append-only and written in order — and a fresh
// pool in a "new process" recovers it (the kill loop of the torture
// tier found both shapes).

// crashChain builds lead → L1 → … over one pool, every delegation a
// sync Wrap ("level1", "level2", …). gated[l] makes level l call the
// gated refund in its first step, beside its delegation; fresh builds
// the levels of a process that already ran their first step (they only
// answer), as a restarted process's agents would carry on.
func crashChain(p *pool.Pool, gated []bool, fresh bool) (*core.Agent, *flags) {
	ran := &flags{}
	refund := core.Tool("refund", "", func(ctx context.Context, in struct{ OrderID string }) (string, error) {
		call, _ := core.CallFromContext(ctx)
		ran.add(call.Approved)
		return "refunded " + in.OrderID, nil
	}, core.RequireApproval())
	var below *core.Agent
	for l := len(gated) - 1; l >= 0; l-- {
		done := wefttest.Say("level" + string(rune('0'+l)) + " done")
		var turns []wefttest.Turn
		if fresh {
			var calls []wefttest.Call
			if gated[l] {
				calls = append(calls, wefttest.Call{Name: "refund", ID: "g", Args: `{"order_id":"` + string(rune('0'+l)) + `"}`})
			}
			if below != nil {
				calls = append(calls, wefttest.Call{Name: "level" + string(rune('1'+l)), ID: "d",
					Args: wefttest.Args(struct{ Prompt string }{"go"})})
			}
			turns = append(turns, wefttest.ToolCalls(calls...))
		}
		turns = append(turns, done)
		opts := []core.Option{refund}
		if below != nil {
			opts = append(opts, p.MustWrap("level"+string(rune('1'+l)), "the next level", below))
		}
		below = core.New(wefttest.Script(turns...), opts...)
	}
	return below, ran
}

// parkCrashChain runs the chain's first turn to its park on a jsonl
// store in dir and closes everything; it returns the top's id.
func parkCrashChain(t *testing.T, dir string, gated []bool) string {
	t.Helper()
	ctx := context.Background()
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := pool.New(4)
	lead, _ := crashChain(p, gated, true)
	s, err := thread.Create(ctx, st, lead)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, core.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	if len(s.Pending()) == 0 {
		t.Fatal("the chain did not park")
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	return s.ID()
}

// crashLine is one line of a jsonl session file and its entry type.
type crashLine struct {
	raw, kind string
}

func crashLines(t *testing.T, path string) []crashLine {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []crashLine
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var m struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(sc.Bytes(), &m)
		out = append(out, crashLine{raw: sc.Text(), kind: m.Type})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// cutBefore keeps a session file's lines up to (not including) the
// first one cut matches — the crash image of a kill right before that
// append.
func cutBefore(t *testing.T, path string, cut func(crashLine) bool) {
	t.Helper()
	ls := crashLines(t, path)
	for i, l := range ls {
		if cut(l) {
			var b strings.Builder
			for _, x := range ls[:i] {
				b.WriteString(x.raw + "\n")
			}
			if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("%s: no line to cut at", filepath.Base(path))
}

// after matches the line after the first one first matches.
func after(first func(crashLine) bool) func(crashLine) bool {
	seen := false
	return func(l crashLine) bool {
		if seen {
			return true
		}
		seen = first(l)
		return false
	}
}

func kind(k string) func(crashLine) bool {
	return func(l crashLine) bool { return l.kind == k }
}

// firstChild names the child session of a session's first receipt.
func firstChild(t *testing.T, st thread.Storage, id string) string {
	t.Helper()
	_, entries, _, err := st.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if pr, ok := e.(thread.PoolReceiptEntry); ok && pr.Child != "" {
			return pr.Child
		}
	}
	t.Fatalf("%s delegated nothing", id)
	return ""
}

// readOnly opens a session to read it, under an agent that never runs.
func readOnly(t *testing.T, st thread.Storage, id string) *thread.Session {
	t.Helper()
	s, err := thread.Open(context.Background(), st, id, core.New(wefttest.Script()))
	if err != nil {
		t.Fatalf("open %s: %v", id, err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}

// F1 (torture tier): a kill while L1's run is in flight — L2 parked
// and mirrored into L1, L1's own park not yet written. Recover settled
// L1 failed ("no turn was recorded") and left L1's delegation to L2
// running, L2's request undecidable: the top no longer offered it.
// Settling a child as over now ends its own sync delegations first,
// as Cancel and Close do: L2 is canceled at its boundary and settles
// canceled in L1's ledger, its mirror in L1 denied.
func TestRecoverEndsAFailedChildsSubtree(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	gated := []bool{false, false, true} // only L2 parks
	top := parkCrashChain(t, dir, gated)
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	l1 := firstChild(t, st, top)
	l2 := firstChild(t, st, l1)
	// L2 complete (parked); L1 through the mirror of L2's request,
	// before its own park; the top before L1's mirror.
	cutBefore(t, filepath.Join(dir, l1+".jsonl"), after(kind("approval_request")))
	cutBefore(t, filepath.Join(dir, top+".jsonl"), kind("approval_request"))

	p := pool.New(4)
	defer func() { _ = p.Close(ctx) }()
	lead, ran := crashChain(p, gated, false)
	s, err := thread.Open(ctx, st, top, lead)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(ctx) }()
	if err := p.Recover(ctx, s); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	rs := pool.Receipts(s)
	if len(rs) != 1 || rs[0].State != pool.Failed || !strings.Contains(rs[0].Stop, "did not survive a restart") {
		t.Fatalf("top receipts = %+v, want L1 settled failed", rs)
	}
	if pend := s.Pending(); len(pend) != 0 {
		t.Errorf("the top offers %+v", pend)
	}
	l1s := readOnly(t, st, l1)
	sub := pool.Receipts(l1s)
	if len(sub) != 1 || sub[0].Child != l2 || sub[0].State != pool.Canceled {
		t.Fatalf("L1's receipts = %+v, want its delegation to L2 settled canceled", sub)
	}
	if pend := l1s.Pending(); len(pend) != 0 {
		t.Errorf("L1 still offers L2's request: %+v", pend)
	}
	l2s := readOnly(t, st, l2)
	if pend := l2s.Pending(); len(pend) != 0 {
		t.Errorf("L2 still holds %d undecidable request(s)", len(pend))
	}
	if len(ran.snapshot()) != 0 {
		t.Error("the gated call ran")
	}
	// Once: a second Recover records nothing more.
	before := len(l1s.Entries())
	if err := p.Recover(ctx, s); err != nil {
		t.Fatalf("a second Recover: %v", err)
	}
	if again := len(readOnlyFresh(t, st, l1).Entries()); again != before {
		t.Errorf("a second Recover wrote to L1: %d entries, were %d", again, before)
	}
}

// readOnlyFresh is readOnly on a session's current file, closed at once.
func readOnlyFresh(t *testing.T, st thread.Storage, id string) *thread.Session {
	t.Helper()
	s, err := thread.Open(context.Background(), st, id, core.New(wefttest.Script()))
	if err != nil {
		t.Fatalf("open %s: %v", id, err)
	}
	_ = s.Close(context.Background())
	return s
}

// F2 (torture tier): a kill after the top's step mirrored L1's park
// but before the top's turn entry (its park record). The top's
// delegating call is no approval boundary — nothing parked it, the
// next run's input repair answers it — yet Recover reattached L1 under
// it: L1's request was offered and decidable, and the resume waited
// for a park of the call that never comes (Close stalled on it for its
// 30 s bound). The reattached child now delegates by no call: the
// decision resumes it, the approved call runs, and its answer settles
// the receipt.
func TestRecoverOrphanedDelegatingCallResumesTheChild(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	gated := []bool{true, true} // the top parks its own gated call beside L1's park
	top := parkCrashChain(t, dir, gated)
	cutBefore(t, filepath.Join(dir, top+".jsonl"), kind("turn"))

	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := pool.New(4)
	lead, ran := crashChain(p, gated, false)
	s, err := thread.Open(ctx, st, top, lead)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(ctx) }()
	if err := p.Recover(ctx, s); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	pend := s.Pending()
	if len(pend) != 1 || pend[0].Child == "" {
		t.Fatalf("top Pending = %+v, want L1's request", pend)
	}
	if err := p.Decide(ctx, s, thread.Approve(pend[0].CallID)); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	rc := waitFor(t, p, s, pool.Receipts(s)[0].ID, pool.Done)
	if rc.Stop != "level1 done" {
		t.Errorf("receipt = %+v, want L1's answer", rc)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Errorf("approved runs = %v, want the approved call run once", got)
	}
	start := time.Now()
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("Close took %v", d)
	}
	// The top's next turn repairs its unparked calls and completes.
	turn, err := s.Send(ctx, core.User("carry on"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := turn.Wait()
	if err != nil || res.Text() != "level0 done" {
		t.Fatalf("the top's next turn: %v, %v", res, err)
	}
	if len(s.Pending()) != 0 {
		t.Errorf("the top still offers %+v", s.Pending())
	}
}

// F1 at depth: L1 and L2 both cut mid-run (each through the mirror of
// its child's request, before its own park), L3 parked. Recover ends
// the subtree depth first: L3 canceled at its boundary in L2's
// ledger, L2 settled failed in L1's, L1 failed in the top's.
func TestRecoverEndsAFailedChildsSubtreeDeep(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	gated := []bool{false, false, false, true} // only L3 parks
	top := parkCrashChain(t, dir, gated)
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	l1 := firstChild(t, st, top)
	l2 := firstChild(t, st, l1)
	l3 := firstChild(t, st, l2)
	cutBefore(t, filepath.Join(dir, l2+".jsonl"), after(kind("approval_request")))
	cutBefore(t, filepath.Join(dir, l1+".jsonl"), kind("approval_request"))
	cutBefore(t, filepath.Join(dir, top+".jsonl"), kind("approval_request"))

	p := pool.New(4)
	defer func() { _ = p.Close(ctx) }()
	lead, ran := crashChain(p, gated, false)
	s, err := thread.Open(ctx, st, top, lead)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(ctx) }()
	if err := p.Recover(ctx, s); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	want := []struct {
		session, child string
		state          pool.State
	}{{top, l1, pool.Failed}, {l1, l2, pool.Failed}, {l2, l3, pool.Canceled}}
	for _, w := range want {
		var rs []pool.Receipt
		if w.session == top {
			rs = pool.Receipts(s)
		} else {
			rs = pool.Receipts(readOnly(t, st, w.session))
		}
		if len(rs) != 1 || rs[0].Child != w.child || rs[0].State != w.state {
			t.Errorf("%s's receipts = %+v, want %s %s", w.session, rs, w.child, w.state)
		}
	}
	if pend := readOnly(t, st, l3).Pending(); len(pend) != 0 {
		t.Errorf("L3 still holds %d undecidable request(s)", len(pend))
	}
	if len(ran.snapshot()) != 0 {
		t.Error("the gated call ran")
	}
}

// A kill inside Recover's own cascade, between a grandchild's
// settlement in the middle child's ledger and the denial of its
// requests there: the next Recover finds the receipt settled and still
// denies the requests, rather than leave them offered in a session
// nothing resumes.
func TestRecoverEndsASubtreeCutMidCascade(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	gated := []bool{false, false, false, true}
	top := parkCrashChain(t, dir, gated)
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	l1 := firstChild(t, st, top)
	l2 := firstChild(t, st, l1)
	cutBefore(t, filepath.Join(dir, l2+".jsonl"), after(kind("approval_request")))
	cutBefore(t, filepath.Join(dir, l1+".jsonl"), kind("approval_request"))
	cutBefore(t, filepath.Join(dir, top+".jsonl"), kind("approval_request"))

	recoverOnce := func() {
		t.Helper()
		p := pool.New(4)
		lead, _ := crashChain(p, gated, false)
		s, err := thread.Open(ctx, st, top, lead)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Recover(ctx, s); err != nil {
			t.Fatalf("Recover: %v", err)
		}
		_ = p.Close(ctx)
		_ = s.Close(ctx)
	}
	recoverOnce()
	// The crash image of a kill right after L3's canceled receipt in L2:
	// L2 through that receipt, L1 and the top before their settlements.
	cutBefore(t, filepath.Join(dir, l2+".jsonl"), after(func(l crashLine) bool {
		return l.kind == "pool_receipt" && strings.Contains(l.raw, `"status":"canceled"`)
	}))
	cutBefore(t, filepath.Join(dir, l1+".jsonl"), func(l crashLine) bool {
		return l.kind == "pool_receipt" && strings.Contains(l.raw, `"status":"failed"`)
	})
	cutBefore(t, filepath.Join(dir, top+".jsonl"), func(l crashLine) bool {
		return l.kind == "pool_receipt" && strings.Contains(l.raw, `"status":"failed"`)
	})
	if len(readOnlyFresh(t, st, l2).Pending()) == 0 {
		t.Fatal("the image is not the window: L2 offers nothing")
	}
	recoverOnce()
	if pend := readOnlyFresh(t, st, l2).Pending(); len(pend) != 0 {
		t.Errorf("L2 still offers %+v", pend)
	}
	if rs := pool.Receipts(readOnlyFresh(t, st, l1)); len(rs) != 1 || rs[0].State != pool.Failed {
		t.Errorf("L1's receipts = %+v", rs)
	}
}
