package thread_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// waitChain waits a turn and its auto-resumed follow-up.
func waitChain(t *testing.T, turn *thread.Turn) {
	t.Helper()
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	if next := turn.Next(); next != nil {
		if _, err := next.Wait(); err != nil {
			t.Fatal(err)
		}
	}
}

// TestGrantCaseFoldedKeyFailsClosed: encoding/json binds "Command" to
// the tool's command field case-insensitively, last key winning, so a
// grant reading "/command" exactly would approve "go test" while the
// tool ran "rm -rf /". A case-folded twin on the pointer's path is no
// match (post-0.7 review).
func TestGrantCaseFoldedKeyFailsClosed(t *testing.T) {
	ctx := context.Background()
	agent, ran := runAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"go test ./...","Command":"rm -rf /"}`}),
		wefttest.Say("done"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(ctx, thread.Grant{Tool: "run", Args: []thread.Arg{thread.ArgGlob("/command", "go test*")}}); err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, weft.User("x"))
	if err != nil {
		t.Fatal(err)
	}
	waitChain(t, turn)
	for _, c := range *ran {
		if c != "go test ./..." {
			t.Fatalf("grant for `go test*` executed %q", c)
		}
	}
}

// TestGrantMaxUsesCountsItsBatch: MaxUses counts the chain's own batch
// — three parallel calls under MaxUses 1 run one and park two; before,
// the audits of the batch landed after every call matched, and all
// three ran (post-0.7 review).
func TestGrantMaxUsesCountsItsBatch(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	var ran []string
	tool := weft.Tool("run", "Run a command.",
		func(ctx context.Context, in struct {
			Command string `json:"command"`
		}) (string, error) {
			mu.Lock()
			ran = append(ran, in.Command)
			mu.Unlock()
			return "ran", nil
		}, weft.RequireApproval())
	agent := weft.New(wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "run", Args: `{"command":"a"}`},
			wefttest.Call{Name: "run", Args: `{"command":"b"}`},
			wefttest.Call{Name: "run", Args: `{"command":"c"}`},
		),
		wefttest.Say("done"),
	), tool)
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(ctx, thread.Grant{Tool: "run", MaxUses: 1}); err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, weft.User("x"))
	if err != nil {
		t.Fatal(err)
	}
	waitChain(t, turn)
	mu.Lock()
	defer mu.Unlock()
	if len(ran) > 1 {
		t.Fatalf("MaxUses=1 grant approved %d calls: %v (pending %d)", len(ran), ran, len(s.Pending()))
	}
}

// TestDecideSignedExpiryWrapRejected: the v1 challenge encoded the
// expiry as UnixNano, which wraps every 2^64ns — a captured expired
// signature with its expiry moved 2^64ns on verified and ran the call.
// The v2 encoding does not wrap: the forgery fails the MAC.
func TestDecideSignedExpiryWrapRejected(t *testing.T) {
	ctx := context.Background()
	ring, secret := signerRing(t)
	agent, ran := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}), wefttest.Say("done"))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithKeyring(ring), thread.RequestExpiry(20*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	r, err := s.Request(call.ID)
	if err != nil {
		t.Fatal(err)
	}
	sd := thread.SignDecision(secret, r, thread.Approve(call.ID))
	time.Sleep(50 * time.Millisecond)
	if _, err := s.DecideSigned(ctx, sd); !errors.Is(err, thread.ErrExpired) {
		t.Fatalf("baseline: got %v, want ErrExpired", err)
	}
	for range 4 {
		sd.Expiry = sd.Expiry.Add(1 << 62) // 4 * 2^62 = 2^64 ns
	}
	if sd.Expiry.UnixNano() != r.Expiry.UnixNano() {
		t.Fatal("the forgery does not wrap to the signed UnixNano")
	}
	turn, err := s.DecideSigned(ctx, sd)
	if err == nil && turn != nil {
		_, _ = turn.Wait()
	}
	if !errors.Is(err, thread.ErrBadSignature) {
		t.Fatalf("forged expiry: err=%v, want ErrBadSignature", err)
	}
	if got := ran.snapshot(); len(got) != 0 {
		t.Fatalf("expired signature revived by expiry wrap: ran=%v", got)
	}
}

// TestDecideSignedStaleRunRejected: call ids repeat across runs; a
// signed approval minted for run 1's call_1 (never submitted — the
// boundary was denied through another channel) must not approve run
// 2's call_1 with the same arguments. The challenge binds the run.
func TestDecideSignedStaleRunRejected(t *testing.T) {
	ctx := context.Background()
	ring, secret := signerRing(t)
	agent, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}),
		wefttest.Say("denied noted"),
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}),
		wefttest.Say("done"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithKeyring(ring))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	r1, err := s.Request(call.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale := thread.SignDecision(secret, r1, thread.Approve(call.ID))
	// The boundary is denied through another channel.
	turn, err := s.Decide(ctx, thread.Deny(call.ID, "no"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	call2 := parkSend(t, s, ctx)
	if call2.ID != call.ID {
		t.Skipf("ids differ: %s %s", call.ID, call2.ID)
	}
	turn, err = s.DecideSigned(ctx, stale)
	if err == nil && turn != nil {
		_, _ = turn.Wait()
	}
	if !errors.Is(err, thread.ErrNotPending) {
		t.Fatalf("a signature minted for run 1's request answered run 2's call: err=%v ran=%v", err, ran.snapshot())
	}
}

// TestDecideAfterExpiryRejected: an approval of a request past its
// expiry is ErrExpired and records nothing — before, only Resume's
// sweep read the expiry, and a late Decide ran the call.
func TestDecideAfterExpiryRejected(t *testing.T) {
	ctx := context.Background()
	agent, ran := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}), wefttest.Say("done"))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.RequestExpiry(20*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	time.Sleep(50 * time.Millisecond)
	turn, err := s.Decide(ctx, thread.Approve(call.ID))
	if err == nil && turn != nil {
		_, _ = turn.Wait()
	}
	if !errors.Is(err, thread.ErrExpired) {
		t.Fatalf("late approval: err=%v, want ErrExpired", err)
	}
	if got := ran.snapshot(); len(got) != 0 {
		t.Fatalf("an approval after the request's expiry ran the call: ran=%v", got)
	}
	// A deny still lands, and Resume's sweep still works.
	if _, err := s.Decide(ctx, thread.Deny(call.ID, "late")); err != nil {
		t.Fatalf("deny of an expired request: %v", err)
	}
}

// TestInterruptUnderRequireSigned: the Interrupt policy denies a
// parked boundary through the session's own machinery, which
// RequireSigned does not close — before, it went through the public
// Decide, failed, and the follow-up waited behind the boundary.
func TestInterruptUnderRequireSigned(t *testing.T) {
	ctx := context.Background()
	ring, _ := signerRing(t)
	agent, _ := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}),
		wefttest.Say("after"),
		wefttest.Say("after2"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithKeyring(ring), thread.RequireSigned(), thread.BusyPolicy(thread.Interrupt))
	if err != nil {
		t.Fatal(err)
	}
	parkSend(t, s, ctx)
	t2, err := s.Send(ctx, weft.User("forget it"))
	if err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := t2.Wait(); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("follow-up: %v", err)
		}
	case <-wctx.Done():
		t.Fatalf("Interrupt under RequireSigned: follow-up never ran; pending=%d", len(s.Pending()))
	}
}
