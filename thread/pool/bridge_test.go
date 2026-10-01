package pool_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/pool"
	"github.com/weftgo/weft/wefttest"
)

// delegating builds a parent agent whose model delegates once to
// child through the wrap "research" — under an explicit call id —
// then concludes.
func delegating(p *pool.Pool, child *weft.Agent, opts ...pool.WrapOption) *weft.Agent {
	return weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", ID: "c-wrapper", Args: `{"prompt":"refund the orders"}`}),
		wefttest.Say("all done"),
	), p.MustWrap("research", "delegates the refund flow", child, opts...))
}

// receiptStates returns the status of every ledger entry of one
// receipt, in order.
func receiptStates(s *thread.Session, receiptID string) string {
	var out []string
	for _, e := range s.Entries() {
		if pr, ok := e.(thread.PoolReceiptEntry); ok && (pr.ID == receiptID || pr.Receipt == receiptID) {
			out = append(out, pr.Status)
		}
	}
	return strings.Join(out, " ")
}

// waitFor waits for the receipt to come to rest and fails the test
// unless it rests at want.
func waitFor(t *testing.T, p *pool.Pool, s *thread.Session, receiptID string, want pool.State) pool.Receipt {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rc, err := p.Wait(ctx, s, receiptID)
	if err != nil {
		t.Fatalf("Wait(%s): %v; receipts %+v", receiptID, err, pool.Receipts(s))
	}
	if rc.State != want {
		t.Fatalf("receipt %s rests at %s (%s), want %s", receiptID, rc.State, rc.Stop, want)
	}
	return rc
}

// park runs the parent's delegating turn and returns it with the
// receipt of the child it parked on.
func park(t *testing.T, s *thread.Session) (*thread.Turn, pool.Receipt) {
	t.Helper()
	t1, err := s.Send(context.Background(), weft.User("refund them"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	res, err := t1.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(res.Pending) != 1 {
		t.Fatalf("the parent parked %d calls, want the delegating call", len(res.Pending))
	}
	rs := pool.Receipts(s)
	if len(rs) != 1 || rs[0].State != pool.Parked {
		t.Fatalf("receipts = %+v, want one, parked", rs)
	}
	return t1, rs[0]
}

// A child that parks twice resumes twice (the P1): the pump used to
// replay a decision for every mirror the child ever had, the child's
// Decide refused the one for the call long resolved, and the receipt
// sat at running for good. Only the calls pending in the child now
// are replayed — also when the second park reuses the first's call
// id, which is all a scripted model ever mints.
func TestChildParksTwice(t *testing.T) {
	for _, ids := range [][2]string{{"c-a", "c-b"}, {"same-id", "same-id"}} {
		t.Run(ids[0]+"+"+ids[1], func(t *testing.T) {
			ctx := context.Background()
			p := pool.New(2)
			child, ran := gatedChild(
				wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: ids[0], Args: `{"order_id":"1"}`}),
				wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: ids[1], Args: `{"order_id":"2"}`}),
				wefttest.Say("both refunded"),
			)
			s, err := thread.Create(ctx, thread.Memory(), delegating(p, child))
			if err != nil {
				t.Fatal(err)
			}
			t1, rc := park(t, s)

			for round, id := range ids {
				pend := s.Pending()
				if len(pend) != 1 || pend[0].CallID != rc.Child+"/"+id || pend[0].Child != rc.Child {
					t.Fatalf("round %d: Pending = %+v, want the child's %s alone", round, pend, id)
				}
				if err := p.Decide(ctx, s, thread.Approve(pend[0].CallID)); err != nil {
					t.Fatalf("round %d: Decide: %v", round, err)
				}
				want := pool.Parked
				if round == 1 {
					want = pool.Done
				}
				waitFor(t, p, s, rc.ID, want)
			}
			if got := receiptStates(s, rc.ID); got != "accepted running parked running parked running done" {
				t.Errorf("the receipt's journey = %q", got)
			}
			final := pool.Receipts(s)[0]
			if final.Stop != "both refunded" {
				t.Errorf("settled receipt = %+v", final)
			}
			next := t1.Next()
			if next == nil {
				t.Fatal("the parent's call did not resolve")
			}
			res, err := next.Wait()
			if err != nil || res.Text() != "all done" {
				t.Fatalf("the parent's continuation: %v, %v", res, err)
			}
			if got := toolResultsOf(s.Context()); len(got) != 1 || got[0] != "both refunded" {
				t.Errorf("the delegating call's result = %v", got)
			}
			if got := ran.snapshot(); len(got) != 2 || !got[0] || !got[1] {
				t.Errorf("approved flags = %v, want both calls run once, approved", got)
			}
			if pend := s.Pending(); len(pend) != 0 {
				t.Errorf("Pending after the flow = %+v", pend)
			}
			// Both of the child's runs are on the bill: three model
			// steps, the two before the parks included.
			if u := s.Usage().Delegated; u.InputTokens != 30 || u.OutputTokens != 15 {
				t.Errorf("Delegated = %+v, want the child's three steps", u)
			}
		})
	}
}

// The hidden wrapper takes no decision (the P2): approving it used to
// re-run the delegation — a second child session, the side effects
// twice.
func TestWrapperNotDecidable(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	p := pool.New(2)
	child, ran := gatedChild(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c-a", Args: `{"order_id":"1"}`}),
		wefttest.Say("refunded"),
	)
	s, _ := thread.Create(ctx, st, delegating(p, child))
	t1, rc := park(t, s)
	for _, d := range []thread.Decision{thread.Approve("c-wrapper"), thread.Deny("c-wrapper", "no"), thread.Resolve("c-wrapper", "forged")} {
		if _, err := s.Decide(ctx, d); !errors.Is(err, thread.ErrDelegated) {
			t.Fatalf("Session.Decide(%s) on the wrapper: %v, want ErrDelegated", d.Kind, err)
		}
		if err := p.Decide(ctx, s, d); !errors.Is(err, thread.ErrDelegated) {
			t.Fatalf("Pool.Decide(%s) on the wrapper: %v, want ErrDelegated", d.Kind, err)
		}
	}
	if t1.Next() != nil {
		t.Fatal("a refused decision resumed the parent")
	}
	if n := len(mustList(ctx, t, st)); n != 2 {
		t.Fatalf("%d sessions exist, want the parent and its one child", n)
	}
	// The delegation still completes the one way it can.
	if err := p.Decide(ctx, s, thread.Approve(rc.Child+"/c-a")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, p, s, rc.ID, pool.Done)
	if n := len(mustList(ctx, t, st)); n != 2 {
		t.Fatalf("%d sessions exist after the flow, want 2", n)
	}
	if got := ran.snapshot(); len(got) != 1 {
		t.Fatalf("the gated tool ran %d times", len(got))
	}
}

// A resumed child is pool work (the P2): it queues for a slot like a
// first run, and Decide — which only records and arms — returns
// without waiting for it. The resume used to run outside the
// semaphore, on the caller's goroutine, deaf to its context.
func TestResumeHoldsSlot(t *testing.T) {
	ctx := context.Background()
	p := pool.New(1)
	s, _ := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
	child, ran := gatedChild(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c-a", Args: `{"order_id":"1"}`}),
		wefttest.Say("refunded"),
	)
	r, err := p.Submit(ctx, s, child, "refund order 1")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, p, s, r.ID, pool.Parked)

	// Another child takes the only slot and keeps it.
	started, release := make(chan struct{}), make(chan struct{})
	busy, err := p.Submit(ctx, s, weft.New(blocking{release: release, text: "busy",
		onStart: func() { close(started) }}), "hold the slot")
	if err != nil {
		t.Fatal(err)
	}
	<-started

	pend := s.Pending()
	if len(pend) != 1 {
		t.Fatalf("Pending = %+v", pend)
	}
	begun := time.Now()
	if err := p.Decide(ctx, s, thread.Approve(pend[0].CallID)); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if took := time.Since(begun); took > 2*time.Second {
		t.Fatalf("Decide took %v: it waited for the child", took)
	}
	// The resume is queued behind the busy child: nothing of it runs,
	// and the ledger still says parked.
	time.Sleep(30 * time.Millisecond)
	if got := ran.snapshot(); len(got) != 0 {
		t.Fatalf("the resumed child ran outside the pool's bound: %v", got)
	}
	if got := receiptStates(s, r.ID); got != "accepted running parked" {
		t.Fatalf("the queued resume's receipt = %q", got)
	}
	// A pump while the resume is queued arms nothing twice.
	if err := p.Decide(ctx, s); err != nil {
		t.Fatalf("pump: %v", err)
	}
	close(release)
	waitFor(t, p, s, busy.ID, pool.Done)
	waitFor(t, p, s, r.ID, pool.Done)
	if got := receiptStates(s, r.ID); got != "accepted running parked running done" {
		t.Errorf("the receipt's journey = %q", got)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Errorf("approved flags = %v", got)
	}
	// Decide honours its context: an ended one records nothing.
	dead, cancel := context.WithCancel(ctx)
	cancel()
	if err := p.Decide(dead, s); !errors.Is(err, context.Canceled) {
		t.Errorf("Decide on a canceled context: %v", err)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Decide(ctx, s); !errors.Is(err, pool.ErrClosed) {
		t.Errorf("Decide on a closed pool: %v, want ErrClosed", err)
	}
}

// Decide reports every child it could not arm, joined, and arms the
// rest — it used to return the first error and keep quiet about the
// others.
func TestDecideJoinsErrors(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	p := pool.New(3)
	s, _ := thread.Create(ctx, st, weft.New(wefttest.Script()))
	var rs []*pool.Receipt
	for i := 0; i < 3; i++ {
		child, _ := gatedChild(wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: fmt.Sprintf("c-%d", i), Args: `{"order_id":"1"}`}))
		r, err := p.Submit(ctx, s, child, "refund")
		if err != nil {
			t.Fatal(err)
		}
		rs = append(rs, r)
	}
	for _, r := range rs {
		waitFor(t, p, s, r.ID, pool.Parked)
	}
	// The restart: the old process lets go, a new pool knows one of
	// the three children.
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	open, err := thread.Open(ctx, st, s.ID(), weft.New(wefttest.Script()))
	if err != nil {
		t.Fatal(err)
	}
	p2 := pool.New(3)
	resumed := func() *weft.Agent {
		a, _ := gatedChild(wefttest.Say("resumed"))
		return a
	}
	if err := p2.Register(rs[1].Child, resumed()); err != nil {
		t.Fatal(err)
	}
	var ds []thread.Decision
	for _, r := range open.Pending() {
		ds = append(ds, thread.Approve(r.CallID))
	}
	if len(ds) != 3 {
		t.Fatalf("Pending after the restart = %+v", open.Pending())
	}
	err = p2.Decide(ctx, open, ds...)
	if !errors.Is(err, pool.ErrNoAgent) {
		t.Fatalf("Decide = %v, want ErrNoAgent", err)
	}
	for _, i := range []int{0, 2} {
		if !strings.Contains(err.Error(), rs[i].Child) {
			t.Errorf("the error does not name child %d (%s): %v", i, rs[i].Child, err)
		}
	}
	if strings.Contains(err.Error(), rs[1].Child) {
		t.Errorf("the error names the child that was armed: %v", err)
	}
	// The decisions are recorded; the known child ran regardless.
	waitFor(t, p2, open, rs[1].ID, pool.Done)
	if pend := open.Pending(); len(pend) != 0 {
		t.Errorf("Pending = %+v, want every request decided", pend)
	}
	// The others resume once their agents are known: a pump is enough.
	for _, i := range []int{0, 2} {
		if err := p2.Register(rs[i].Child, resumed()); err != nil {
			t.Fatal(err)
		}
	}
	if err := p2.Decide(ctx, open); err != nil {
		t.Fatalf("pump after Register: %v", err)
	}
	for _, r := range rs {
		waitFor(t, p2, open, r.ID, pool.Done)
	}
	// Register's own contract: errors, not silence.
	if err := p2.Register("", resumed()); err == nil {
		t.Error("Register with an empty session id: no error")
	}
	if err := p2.Register("s_x", nil); err == nil {
		t.Error("Register with a nil agent: no error")
	}
}

// flaky is a storage whose appends of mirrored requests fail on
// demand — the parent's file refusing exactly the write a park needs.
type flaky struct {
	thread.Storage
	failMirrors atomic.Bool
}

func (f *flaky) Append(ctx context.Context, session string, entries ...thread.Entry) error {
	if f.failMirrors.Load() {
		for _, e := range entries {
			if re, ok := e.(thread.ApprovalRequestEntry); ok && re.Child != "" {
				return errors.New("disk full")
			}
		}
	}
	return f.Storage.Append(ctx, session, entries...)
}

// A park the parent cannot record fails the delegation (the P2): the
// child's requests used to stay unmirrored, the receipt at running,
// and the sync wrapper parked anyway — offered to the operator as an
// ordinary request whose approval re-ran the delegation.
func TestMirrorFailureFailsDelegation(t *testing.T) {
	ctx := context.Background()
	t.Run("sync", func(t *testing.T) {
		st := &flaky{Storage: thread.Memory()}
		st.failMirrors.Store(true)
		p := pool.New(1)
		child, ran := gatedChild(wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c-a", Args: `{"order_id":"1"}`}))
		s, _ := thread.Create(ctx, st, delegating(p, child))
		t1, err := s.Send(ctx, weft.User("refund them"))
		if err != nil {
			t.Fatal(err)
		}
		res, err := t1.Wait()
		if err != nil {
			t.Fatalf("a failed delegation must not fail the parent turn: %v", err)
		}
		if len(res.Pending) != 0 {
			t.Fatalf("the delegating call parked with nothing mirrored: %+v", res.Pending)
		}
		if res.Text() != "all done" {
			t.Errorf("the parent's reply = %q", res.Text())
		}
		want := `SUBAGENT_FAILED: agent "research" parked at an approval the pool could not surface: disk full`
		if got := lastToolResult(res.Messages); got != want {
			t.Errorf("the delegating call's result:\n got %q\nwant %q", got, want)
		}
		rs := pool.Receipts(s)
		if len(rs) != 1 || rs[0].State != pool.Failed || !strings.Contains(rs[0].Stop, "not mirrored: disk full") {
			t.Fatalf("receipts = %+v, want failed with the cause", rs)
		}
		if pend := s.Pending(); len(pend) != 0 {
			t.Errorf("Pending = %+v", pend)
		}
		if len(ran.snapshot()) != 0 {
			t.Error("the gated tool ran")
		}
	})
	t.Run("async", func(t *testing.T) {
		st := &flaky{Storage: thread.Memory()}
		st.failMirrors.Store(true)
		p := pool.New(1)
		child, _ := gatedChild(wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c-a", Args: `{"order_id":"1"}`}))
		s, _ := thread.Create(ctx, st, weft.New(wefttest.Script()))
		r, err := p.Submit(ctx, s, child, "refund")
		if err != nil {
			t.Fatal(err)
		}
		rc := waitFor(t, p, s, r.ID, pool.Failed)
		if !strings.Contains(rc.Stop, "not mirrored: disk full") {
			t.Errorf("the failed receipt's cause = %q", rc.Stop)
		}
	})
}

// Nested approvals expire (the P2): the child inherits the parent's
// request lifetime and clock, the mirror carries the real expiry, the
// parent's sweep denies the lapsed mirror, and the pump resumes the
// child with the denial — it used to wait for ever on a request with
// no expiry at all.
func TestNestedExpiry(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	var mu sync.Mutex
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	p := pool.New(1)
	child, ran := gatedChild(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c-a", Args: `{"order_id":"1"}`}),
		wefttest.Say("no refund: the approval lapsed"),
	)
	s, err := thread.Create(ctx, st, delegating(p, child), thread.Clock(clock), thread.RequestExpiry(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	t1, rc := park(t, s)
	pend := s.Pending()
	if len(pend) != 1 || !pend[0].Expiry.Equal(now.Add(time.Hour)) {
		t.Fatalf("the mirrored request = %+v, want the child's expiry, one hour on the parent's clock", pend)
	}
	// Not yet: a pump before the expiry arms nothing.
	if err := p.Decide(ctx, s); err != nil {
		t.Fatal(err)
	}
	waitFor(t, p, s, rc.ID, pool.Parked)

	mu.Lock()
	now = now.Add(2 * time.Hour)
	mu.Unlock()
	// An approval now is too late, and says so.
	if err := p.Decide(ctx, s, thread.Approve(pend[0].CallID)); !errors.Is(err, thread.ErrExpired) {
		t.Fatalf("approving a lapsed nested request: %v, want ErrExpired", err)
	}
	// The sweep denied the mirror; the pump carries the denial down.
	if err := p.Decide(ctx, s); err != nil {
		t.Fatal(err)
	}
	final := waitFor(t, p, s, rc.ID, pool.Done)
	if final.Stop != "no refund: the approval lapsed" {
		t.Errorf("settled receipt = %+v", final)
	}
	if len(ran.snapshot()) != 0 {
		t.Fatal("the lapsed call ran")
	}
	next := t1.Next()
	if next == nil {
		t.Fatal("the parent did not resume")
	}
	if res, err := next.Wait(); err != nil || res.Text() != "all done" {
		t.Fatalf("the parent's continuation: %v, %v", res, err)
	}
	if got := toolResultsOf(s.Context()); len(got) != 1 || got[0] != "no refund: the approval lapsed" {
		t.Errorf("the delegating call's result = %v, want the child's answer, not an expiry of its own", got)
	}
	// The child's model read the expiry's own denial.
	childSess, err := thread.Open(ctx, st, rc.Child, child)
	if err != nil {
		t.Fatal(err)
	}
	var denial string
	for _, r := range toolResultsOf(childSess.Context()) {
		if strings.HasPrefix(r, "DENIED: ") {
			denial = r
		}
	}
	if !strings.Contains(denial, "expired") {
		t.Errorf("the child's denial = %q, want the expiry's stated reason", denial)
	}
	var via []string
	for _, e := range childSess.Entries() {
		if d, ok := e.(thread.ApprovalDecisionEntry); ok {
			via = append(via, d.Via)
		}
	}
	if len(via) != 1 || via[0] != "expiry" {
		t.Errorf("the child's decisions arrived via %v, want its own expiry sweep alone", via)
	}
}

// The child takes the parent's signing rule: a caller holding the
// child session cannot decide its parked call through the unsigned
// door the parent closed.
func TestChildInheritsSigning(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	secret := []byte("sixteen-byte test secret!")
	ring, err := thread.NewKeyring(thread.Key{ID: "k1", Secret: secret, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	p := pool.New(1)
	child, ran := gatedChild(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c-a", Args: `{"order_id":"1"}`}),
		wefttest.Say("refunded under signature"),
	)
	s, err := thread.Create(ctx, st, delegating(p, child), thread.WithKeyring(ring), thread.RequireSigned())
	if err != nil {
		t.Fatal(err)
	}
	t1, rc := park(t, s)
	// Another handle on the child — what an application listing
	// sessions would open. The signing rule is in the child's header.
	side, err := thread.Open(ctx, st, rc.Child, child, thread.WithKeyring(ring))
	if err != nil {
		t.Fatalf("Open the child: %v", err)
	}
	if _, err := side.Decide(ctx, thread.Approve("c-a")); !errors.Is(err, thread.ErrSignatureRequired) {
		t.Fatalf("the unsigned door on the child: %v, want ErrSignatureRequired", err)
	}
	if len(ran.snapshot()) != 0 {
		t.Fatal("the gated tool ran on an unsigned decision")
	}
	// The signed decision on the parent, through the pool.
	pend := s.Pending()
	challenge, err := s.Request(pend[0].CallID)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.DecideSigned(ctx, s, thread.SignDecision(secret, challenge, thread.Approve(pend[0].CallID))); err != nil {
		t.Fatalf("DecideSigned: %v", err)
	}
	waitFor(t, p, s, rc.ID, pool.Done)
	if res, err := t1.Next().Wait(); err != nil || res.Text() != "all done" {
		t.Fatalf("the parent's continuation: %v, %v", res, err)
	}
	// The child's record keeps the signer's key and names the channel
	// honestly: the parent's decision, not a signature of its own.
	after, err := thread.Open(ctx, st, rc.Child, child, thread.WithKeyring(ring))
	if err != nil {
		t.Fatal(err)
	}
	var got []thread.ApprovalDecisionEntry
	for _, e := range after.Entries() {
		if d, ok := e.(thread.ApprovalDecisionEntry); ok {
			got = append(got, d)
		}
	}
	if len(got) != 1 || got[0].Via != "parent" || got[0].KeyID != "k1" || got[0].Nonce != "" {
		t.Fatalf("the child's replayed decision = %+v, want Via parent under key k1, no nonce", got)
	}
}

// Cancel on a parked receipt cancels it (it used to refuse): the
// child's boundary is denied, nothing resumes, the receipt settles
// canceled, the mirrors leave Pending, and a sync delegation's parent
// reads SUBAGENT_CANCELED.
func TestCancelParked(t *testing.T) {
	ctx := context.Background()
	t.Run("sync", func(t *testing.T) {
		st := thread.Memory()
		p := pool.New(1)
		child, ran := gatedChild(
			wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c-a", Args: `{"order_id":"1"}`}),
			wefttest.Say("never said"),
		)
		s, _ := thread.Create(ctx, st, delegating(p, child))
		t1, rc := park(t, s)
		if err := p.Cancel(ctx, s, rc.ID); err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		// Settled before Cancel returns.
		got, _ := p.Wait(ctx, s, rc.ID)
		if got.State != pool.Canceled || got.Stop != "canceled while parked at an approval" {
			t.Fatalf("receipt = %+v", got)
		}
		if got := receiptStates(s, rc.ID); got != "accepted running parked canceled" {
			t.Errorf("the receipt's journey = %q", got)
		}
		if pend := s.Pending(); len(pend) != 0 {
			t.Errorf("Pending after the cancel = %+v", pend)
		}
		next := t1.Next()
		if next == nil {
			t.Fatal("the parent's call did not resolve")
		}
		if res, err := next.Wait(); err != nil || res.Text() != "all done" {
			t.Fatalf("the parent's continuation: %v, %v", res, err)
		}
		want := `SUBAGENT_CANCELED: agent "research" was canceled before it finished`
		if got := toolResultsOf(s.Context()); len(got) != 1 || got[0] != want {
			t.Errorf("the delegating call's result:\n got %q\nwant %q", got, want)
		}
		// The child did no more work, and its parked call is denied
		// for good.
		if len(ran.snapshot()) != 0 {
			t.Error("the canceled child's gated tool ran")
		}
		childSess, err := thread.Open(ctx, st, rc.Child, child)
		if err != nil {
			t.Fatal(err)
		}
		if pend := childSess.Pending(); len(pend) != 0 {
			t.Errorf("the canceled child still has pending calls: %+v", pend)
		}
		var denied []thread.ApprovalDecisionEntry
		for _, e := range childSess.Entries() {
			if d, ok := e.(thread.ApprovalDecisionEntry); ok {
				denied = append(denied, d)
			}
		}
		if len(denied) != 1 || denied[0].Outcome != thread.OutcomeDeny ||
			denied[0].Reason != "the delegation was canceled while awaiting approval" {
			t.Errorf("the child's denial = %+v", denied)
		}
		// Twice is a StateError, not a second settlement.
		err = p.Cancel(ctx, s, rc.ID)
		var se *pool.StateError
		if !errors.As(err, &se) || se.State != pool.Canceled {
			t.Errorf("a second Cancel = %v", err)
		}
		if n := strings.Count(receiptStates(s, rc.ID), "canceled"); n != 1 {
			t.Errorf("%d settlements recorded", n)
		}
	})
	t.Run("async", func(t *testing.T) {
		p := pool.New(1)
		s, _ := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
		child, ran := gatedChild(wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c-a", Args: `{"order_id":"1"}`}))
		r, err := p.Submit(ctx, s, child, "refund")
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, p, s, r.ID, pool.Parked)
		if err := p.Cancel(ctx, s, r.ID); err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		waitFor(t, p, s, r.ID, pool.Canceled)
		if pend := s.Pending(); len(pend) != 0 {
			t.Errorf("Pending after the cancel = %+v", pend)
		}
		// A decision now has nothing to address, and a pump nothing to
		// resume.
		if err := p.Decide(ctx, s, thread.Approve(r.Child+"/c-a")); !errors.Is(err, thread.ErrNotPending) {
			t.Errorf("deciding a canceled child's request: %v, want ErrNotPending", err)
		}
		if len(ran.snapshot()) != 0 {
			t.Error("the canceled child's gated tool ran")
		}
	})
}

// A pool Cancel of a running sync child is data its parent's model
// reads under a pinned code — it used to surface as the raw
// "weft: run failed at step 0: model stream: context canceled".
func TestCancelRunningSyncChild(t *testing.T) {
	ctx := context.Background()
	p := pool.New(1)
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	child := weft.New(blocking{release: release, text: "late", onStart: func() { close(started) }})
	s, _ := thread.Create(ctx, thread.Memory(), delegating(p, child))
	t1, err := s.Send(ctx, weft.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	<-started
	rs := pool.Receipts(s)
	if len(rs) != 1 || rs[0].State != pool.Running {
		t.Fatalf("receipts = %+v", rs)
	}
	if err := p.Cancel(ctx, s, rs[0].ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	res, err := t1.Wait()
	if err != nil {
		t.Fatalf("a canceled child must not fail the parent's turn: %v", err)
	}
	if res.Text() != "all done" {
		t.Errorf("the parent's reply = %q", res.Text())
	}
	want := `SUBAGENT_CANCELED: agent "research" was canceled before it finished`
	if got := lastToolResult(res.Messages); got != want {
		t.Errorf("the delegating call's result:\n got %q\nwant %q", got, want)
	}
	if rc := pool.Receipts(s)[0]; rc.State != pool.Canceled {
		t.Errorf("receipt = %+v", rc)
	}
}

// The delegating call's own cancellation is not the pool's: the
// parent's run ends with its error, and nothing is dressed up as a
// tool result.
func TestParentCancellationIsNotSubagentCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := pool.New(1)
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	child := weft.New(blocking{release: release, text: "late", onStart: func() { close(started) }})
	s, _ := thread.Create(ctx, thread.Memory(), delegating(p, child))
	t1, err := s.Send(ctx, weft.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	<-started
	cancel()
	if _, err := t1.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("the parent's turn: %v, want its own cancellation", err)
	}
	waitState(t, s, pool.Canceled)
	if strings.Contains(transcriptText(s.Context()), "SUBAGENT_CANCELED") {
		t.Error("the parent's own cancellation was reported as the pool's")
	}
}

// Forward and Cancel tell an operator what is wrong: a receipt in the
// wrong state says which state, and a wrong id is its own error.
func TestStateErrors(t *testing.T) {
	ctx := context.Background()
	p := pool.New(1)
	s, _ := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
	other, _ := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))

	parkedChild, _ := gatedChild(wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c-a", Args: `{"order_id":"1"}`}))
	parked, err := p.Submit(ctx, s, parkedChild, "refund")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, p, s, parked.ID, pool.Parked)

	started, release := make(chan struct{}), make(chan struct{})
	running, err := p.Submit(ctx, s, weft.New(blocking{release: release, text: "ran",
		onStart: func() { close(started) }}), "run")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	queued, err := p.Submit(ctx, s, weft.New(wefttest.Script(wefttest.Say("queued ran"))), "queue")
	if err != nil {
		t.Fatal(err)
	}

	check := func(id string, want pool.State) {
		t.Helper()
		_, err := p.Forward(ctx, s, id, weft.User("steer"))
		var se *pool.StateError
		if !errors.As(err, &se) || !errors.Is(err, pool.ErrNotRunning) || se.State != want || se.Receipt != id || se.Orphan {
			t.Errorf("Forward to a %s receipt = %v, want a StateError naming it", want, err)
		}
		if errors.Is(err, pool.ErrUnknownReceipt) {
			t.Errorf("a %s receipt read as unknown: %v", want, err)
		}
	}
	check(parked.ID, pool.Parked)
	check(queued.ID, pool.Accepted)
	for name, call := range map[string]func(id string) error{
		"Forward": func(id string) error { _, err := p.Forward(ctx, s, id, weft.User("x")); return err },
		"Cancel":  func(id string) error { return p.Cancel(ctx, s, id) },
		"Wait":    func(id string) error { _, err := p.Wait(ctx, s, id); return err },
	} {
		if err := call("e_nope"); !errors.Is(err, pool.ErrUnknownReceipt) || errors.Is(err, pool.ErrNotRunning) {
			t.Errorf("%s of an unknown receipt = %v, want ErrUnknownReceipt", name, err)
		}
	}
	// Another session's receipt is unknown to this one.
	if _, err := p.Forward(ctx, other, running.ID, weft.User("x")); !errors.Is(err, pool.ErrUnknownReceipt) {
		t.Errorf("Forward through the wrong parent = %v, want ErrUnknownReceipt", err)
	}
	close(release)
	waitFor(t, p, s, running.ID, pool.Done)
	waitFor(t, p, s, queued.ID, pool.Done)
	check(running.ID, pool.Done)
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

// Close covers every way the pool runs work: a wrapped tool called
// outside any session after Close is refused — it used to run, the
// fallback path checking nothing — and one in flight is canceled and
// waited for. A parked child is not canceled: its session is closed,
// its receipt stays parked for the next pool.
func TestCloseCoversEverything(t *testing.T) {
	ctx := context.Background()
	t.Run("bare call after Close", func(t *testing.T) {
		p := pool.New(1)
		var ran atomic.Bool
		child := weft.New(stateless{answer: func(weft.ModelRequest) []weft.ModelEvent {
			ran.Store(true)
			return say(nil, "ran").answer(weft.ModelRequest{})
		}})
		parent := weft.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "ask", ID: "c-ask", Args: `{"prompt":"go"}`}),
			wefttest.Say("noted"),
		), p.MustWrap("ask", "", child))
		if err := p.Close(ctx); err != nil {
			t.Fatal(err)
		}
		res, err := parent.Generate(ctx, weft.Prompt("go"))
		if err != nil {
			t.Fatal(err)
		}
		if got := lastToolResult(res.Messages); got != "thread/pool: pool is closed" {
			t.Errorf("a wrapped call on a closed pool = %q", got)
		}
		if ran.Load() {
			t.Error("the child ran on a closed pool")
		}
	})
	t.Run("bare call in flight", func(t *testing.T) {
		p := pool.New(1)
		started, release := make(chan struct{}), make(chan struct{})
		defer close(release)
		child := weft.New(blocking{release: release, text: "late", onStart: func() { close(started) }})
		parent := weft.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "ask", ID: "c-ask", Args: `{"prompt":"go"}`}),
			wefttest.Say("noted"),
		), p.MustWrap("ask", "", child))
		done := make(chan *weft.RunResult, 1)
		go func() {
			res, _ := parent.Generate(ctx, weft.Prompt("go"))
			done <- res
		}()
		<-started
		within(t, 10*time.Second, "Close", func() {
			if err := p.Close(ctx); err != nil {
				t.Errorf("Close: %v", err)
			}
		})
		res := <-done
		if res == nil || !strings.Contains(lastToolResult(res.Messages), "context canceled") {
			t.Errorf("the in-flight bare call was not canceled by Close: %+v", res)
		}
	})
	t.Run("parked child", func(t *testing.T) {
		st := thread.Memory()
		p := pool.New(1)
		s, _ := thread.Create(ctx, st, weft.New(wefttest.Script()))
		child, _ := gatedChild(wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c-a", Args: `{"order_id":"1"}`}))
		r, err := p.Submit(ctx, s, child, "refund")
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, p, s, r.ID, pool.Parked)
		if err := p.Close(ctx); err != nil {
			t.Fatal(err)
		}
		if rc := pool.Receipts(s)[0]; rc.State != pool.Parked {
			t.Errorf("a parked receipt after Close = %+v, want it left parked", rc)
		}
		if len(s.Pending()) != 1 {
			t.Errorf("the parked child's request left Pending: %+v", s.Pending())
		}
	})
}

// Wrap's contract is errors, like Submit's and Register's: a nil
// agent, an empty name, and one name for two agents — which used to
// overwrite the first silently, so a restart resumed its children
// under the wrong agent. MustWrap panics on the same.
func TestWrapErrors(t *testing.T) {
	p := pool.New(1)
	a, b := weft.New(wefttest.Script()), weft.New(wefttest.Script())
	if _, err := p.Wrap("x", "", nil); err == nil {
		t.Error("Wrap with a nil agent: no error")
	}
	if _, err := p.Wrap("", "", a); err == nil {
		t.Error("Wrap with an empty name: no error")
	}
	if _, err := p.Wrap("research", "", a); err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	if _, err := p.Wrap("research", "", a, pool.Async()); err != nil {
		t.Errorf("wrapping the same agent again under its name: %v", err)
	}
	if _, err := p.Wrap("research", "", b); !errors.Is(err, pool.ErrDuplicateWrap) {
		t.Errorf("wrapping another agent under a held name: %v, want ErrDuplicateWrap", err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("MustWrap did not panic on a duplicate name")
			}
		}()
		p.MustWrap("research", "", b)
	}()
}

// A decision can arrive while the delegating turn is still running:
// the child's requests are mirrored the moment it parks, the parent's
// turn parks its call only when the step's other tools have returned.
// A child resumed inside that window would finish and resolve a call
// that is not parked yet — and the parent would then park on it for
// good. The resume waits for the parent's turn to land.
func TestDecisionDuringTheDelegatingTurn(t *testing.T) {
	ctx := context.Background()
	p := pool.New(2)
	child, ran := gatedChild(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c-a", Args: `{"order_id":"1"}`}),
		wefttest.Say("refunded"),
	)
	entered, release := make(chan struct{}), make(chan struct{})
	slow := weft.Tool("slow", "", func(ctx context.Context, _ struct{}) (string, error) {
		close(entered)
		select {
		case <-release:
			return "slow done", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	// One step, two calls: the delegation and a slow sibling that
	// keeps the turn running after the child has parked.
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "research", ID: "c-wrapper", Args: `{"prompt":"refund"}`},
			wefttest.Call{Name: "slow", ID: "c-slow"},
		),
		wefttest.Say("all done"),
	), p.MustWrap("research", "", child), slow)
	s, _ := thread.Create(ctx, thread.Memory(), parent)
	t1, err := s.Send(ctx, weft.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	// The mirror is on the parent while its turn still runs.
	var mirror string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		for _, r := range s.Pending() {
			if r.Child != "" {
				mirror = r.CallID
			}
		}
		if mirror != "" {
			break
		}
	}
	if mirror == "" {
		t.Fatalf("the child's request never surfaced: %+v", s.Pending())
	}
	if err := p.Decide(ctx, s, thread.Approve(mirror)); err != nil {
		t.Fatalf("Decide during the delegating turn: %v", err)
	}
	// The decision is recorded; the child must not run on it yet.
	time.Sleep(30 * time.Millisecond)
	if got := ran.snapshot(); len(got) != 0 {
		t.Fatalf("the child resumed before the parent's call was parked: %v", got)
	}
	close(release)
	res, err := t1.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pending) != 1 || res.Pending[0].ID != "c-wrapper" {
		t.Fatalf("the delegating turn parked %+v", res.Pending)
	}
	rc := waitFor(t, p, s, pool.Receipts(s)[0].ID, pool.Done)
	if rc.Stop != "refunded" {
		t.Errorf("settled = %+v", rc)
	}
	next := t1.Next()
	if next == nil {
		t.Fatal("the parent never resumed: the child's answer resolved nothing")
	}
	if final, err := next.Wait(); err != nil || final.Text() != "all done" {
		t.Fatalf("the parent's continuation: %v, %v", final, err)
	}
	// The delegating call resolved with the child's answer — read off
	// the decision the pool recorded, the one record of it.
	var resolved []thread.ApprovalDecisionEntry
	for _, e := range s.Entries() {
		if d, ok := e.(thread.ApprovalDecisionEntry); ok && d.CallID == "c-wrapper" {
			resolved = append(resolved, d)
		}
	}
	if len(resolved) != 1 || resolved[0].Outcome != thread.OutcomeResolve ||
		resolved[0].Content != "refunded" || resolved[0].Via != "child" {
		t.Errorf("the delegating call's resolution = %+v", resolved)
	}
}
