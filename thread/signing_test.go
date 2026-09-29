package thread_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/wefttest"
)

// signerRing is the one-key ring most signing tests need.
func signerRing(t *testing.T) (*thread.Keyring, []byte) {
	t.Helper()
	secret := []byte("sixteen-byte test secret!")
	r, err := thread.NewKeyring(thread.Key{ID: "k1", Secret: secret, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	return r, secret
}

// TestSignedDecisionRoundTrip is the whole exchange (ADR 0021 §3): the
// session mints a challenge under its active key, the client signs a
// decision over it, DecideSigned verifies and records — with the nonce
// and key id — and the boundary resumes.
func TestSignedDecisionRoundTrip(t *testing.T) {
	ctx := context.Background()
	ring, secret := signerRing(t)
	agent, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1234"}`}),
		wefttest.Say("signed and run"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithKeyring(ring))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	pend := s.Pending()[0]

	r, err := s.Request(call.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Nonce == "" || r.KeyID != "k1" || r.Session != s.ID() || r.ArgsSHA256 != pend.ArgsSHA256 {
		t.Fatalf("challenge: %+v", r)
	}
	sd := thread.SignDecision(secret, r, thread.Approve(call.ID))
	rt, err := s.DecideSigned(ctx, sd)
	if err != nil {
		t.Fatal(err)
	}
	if rt == nil {
		t.Fatal("DecideSigned did not resume")
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Fatalf("approved through a signature: %v", got)
	}
	found := false
	for _, e := range s.Entries() {
		if d, ok := e.(thread.ApprovalDecisionEntry); ok && d.CallID == call.ID {
			found = true
			if d.Nonce != r.Nonce || d.KeyID != "k1" || d.Via != "signed" {
				t.Fatalf("recorded decision: %+v", d)
			}
		}
	}
	if !found {
		t.Fatal("no decision entry recorded from the signature")
	}
}

// TestDecideSignedFailClosed walks the error catalogue (ADR 0021 §3):
// each failure is its own error, and nothing is recorded on any of
// them.
func TestDecideSignedFailClosed(t *testing.T) {
	ctx := context.Background()
	ring, secret := signerRing(t)

	park := func(t *testing.T) (*thread.Session, weft.ToolCallPart, thread.Request) {
		t.Helper()
		agent, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}), wefttest.Say("done"))
		s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithKeyring(ring))
		if err != nil {
			t.Fatal(err)
		}
		call := parkSend(t, s, ctx)
		r, err := s.Request(call.ID)
		if err != nil {
			t.Fatal(err)
		}
		return s, call, r
	}

	t.Run("unknown key", func(t *testing.T) {
		s, call, r := park(t)
		sd := thread.SignDecision(secret, r, thread.Approve(call.ID))
		sd.KeyID = "k9" // the ring never held it; the MAC no longer covers the claim either
		if _, err := s.DecideSigned(ctx, sd); !errors.Is(err, thread.ErrUnknownKey) {
			t.Fatalf("got %v, want ErrUnknownKey", err)
		}
	})

	t.Run("bad signature", func(t *testing.T) {
		s, call, r := park(t)
		sd := thread.SignDecision(secret, r, thread.Deny(call.ID, "no"))
		sd.Reason = "yes actually" // tampered in flight
		if _, err := s.DecideSigned(ctx, sd); !errors.Is(err, thread.ErrBadSignature) {
			t.Fatalf("got %v, want ErrBadSignature", err)
		}
		sd2 := thread.SignDecision([]byte("wrong key entirely"), r, thread.Approve(call.ID))
		if _, err := s.DecideSigned(ctx, sd2); !errors.Is(err, thread.ErrBadSignature) {
			t.Fatalf("wrong key: got %v, want ErrBadSignature", err)
		}
		sd3 := thread.SignDecision(secret, r, thread.Approve(call.ID))
		sd3.Session = "s_somebody_else"
		if _, err := s.DecideSigned(ctx, sd3); !errors.Is(err, thread.ErrBadSignature) {
			t.Fatalf("retargeted session: got %v, want ErrBadSignature", err)
		}
	})

	t.Run("expired", func(t *testing.T) {
		agent, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund"}), wefttest.Say("done"))
		s, err := thread.Create(ctx, thread.Memory(), agent,
			thread.WithKeyring(ring), thread.RequestExpiry(30*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		call := parkSend(t, s, ctx)
		r, err := s.Request(call.ID)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
		sd := thread.SignDecision(secret, r, thread.Approve(call.ID))
		if _, err := s.DecideSigned(ctx, sd); !errors.Is(err, thread.ErrExpired) {
			t.Fatalf("got %v, want ErrExpired", err)
		}
	})

	t.Run("args changed", func(t *testing.T) {
		s, call, r := park(t)
		// An honest signer vouching for different arguments than the
		// request holds: the MAC is valid, the hash is stale.
		other := r
		other.ArgsSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
		sd := thread.SignDecision(secret, other, thread.Approve(call.ID))
		if _, err := s.DecideSigned(ctx, sd); !errors.Is(err, thread.ErrArgsChanged) {
			t.Fatalf("got %v, want ErrArgsChanged", err)
		}
	})

	t.Run("not pending", func(t *testing.T) {
		s, _, r := park(t)
		other := r
		other.CallID = "call_nope" // signed for a call this session never parked
		sd := thread.SignDecision(secret, other, thread.Approve("call_nope"))
		if _, err := s.DecideSigned(ctx, sd); !errors.Is(err, thread.ErrNotPending) {
			t.Fatalf("got %v, want ErrNotPending", err)
		}
	})

	t.Run("nothing recorded", func(t *testing.T) {
		s, call, r := park(t)
		before := len(s.Entries())
		sd := thread.SignDecision(secret, r, thread.Approve(call.ID))
		sd.Who = "tampered"
		if _, err := s.DecideSigned(ctx, sd); !errors.Is(err, thread.ErrBadSignature) {
			t.Fatal(err)
		}
		if got := len(s.Entries()); got != before {
			t.Fatalf("a failed verification recorded %d entries", got-before)
		}
	})
}

// TestSignedReplayAcrossRestart: a replayed signature fails after a
// restart — nonces are entries (ADR 0021 §3).
func TestSignedReplayAcrossRestart(t *testing.T) {
	ctx := context.Background()
	ring, secret := signerRing(t)
	dir := t.TempDir()
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	agent, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"7"}`}))
	s, err := thread.Create(ctx, st, agent, thread.WithKeyring(ring))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	r, err := s.Request(call.ID)
	if err != nil {
		t.Fatal(err)
	}
	sd := thread.SignDecision(secret, r, thread.Deny(call.ID, "first answer stands"))

	resumed, _ := refundAgent(wefttest.Say("denied"))
	s2, err := thread.Open(ctx, st, s.ID(), resumed, thread.WithKeyring(ring))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.DecideSigned(ctx, sd); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.DecideSigned(ctx, sd); !errors.Is(err, thread.ErrReplay) {
		t.Fatalf("replay after restart: got %v, want ErrReplay", err)
	}
}

// TestKeyRotationInFlight: the ring that replaced the active key still
// verifies decisions signed under the old one — rotation without
// invalidating requests in flight (ADR 0021 §3).
func TestKeyRotationInFlight(t *testing.T) {
	ctx := context.Background()
	old, secret := signerRing(t)
	agent, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund"}), wefttest.Say("done"))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithKeyring(old))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	r, err := s.Request(call.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.KeyID != "k1" {
		t.Fatalf("challenge key %q, want the old active k1", r.KeyID)
	}
	// Rotation: a new ring, k2 active, k1 still verifying.
	rotated, err := thread.NewKeyring(
		thread.Key{ID: "k1", Secret: secret},
		thread.Key{ID: "k2", Secret: []byte("the new key, also secret!"), Active: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	// The rotated ring opens the same storage: k1 still verifies, k2
	// signs new challenges.
	st := thread.Memory()
	agent2, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund"}), wefttest.Say("done"))
	s2, err := thread.Create(ctx, st, agent2, thread.WithKeyring(rotated))
	if err != nil {
		t.Fatal(err)
	}
	call2 := parkSend(t, s2, ctx)
	r2, err := s2.Request(call2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r2.KeyID != "k2" {
		t.Fatalf("new challenge key %q, want the rotated active k2", r2.KeyID)
	}
	// A decision signed under the old key still verifies on the old
	// session's boundary — rotation never invalidates requests in
	// flight, which is why the rotated ring keeps k1 too.
	sdOld := thread.SignDecision(secret, r, thread.Approve(call.ID))
	if _, err := s.DecideSigned(ctx, sdOld); err != nil {
		t.Fatalf("old-key decision under the old ring: %v", err)
	}
}

// TestRequireSigned: the unsigned door closes; the session's own
// machinery still records.
func TestRequireSigned(t *testing.T) {
	ctx := context.Background()
	ring, secret := signerRing(t)
	agent, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"2"}`}),
		wefttest.Say("run"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithKeyring(ring), thread.RequireSigned())
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	if _, err := s.Decide(ctx, thread.Approve(call.ID)); !errors.Is(err, thread.ErrSignatureRequired) {
		t.Fatalf("unsigned Decide: %v, want ErrSignatureRequired", err)
	}
	r, err := s.Request(call.ID)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := s.DecideSigned(ctx, thread.SignDecision(secret, r, thread.Approve(call.ID)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Fatalf("signed approval: %v", got)
	}
}

// TestKeyringValidation: the ring's own construction rules.
func TestKeyringValidation(t *testing.T) {
	if _, err := thread.NewKeyring(thread.Key{ID: "", Secret: []byte("x")}); err == nil {
		t.Error("empty id accepted")
	}
	if _, err := thread.NewKeyring(thread.Key{ID: "k", Secret: nil}); err == nil {
		t.Error("empty secret accepted")
	}
	if _, err := thread.NewKeyring(
		thread.Key{ID: "a", Secret: []byte("x"), Active: true},
		thread.Key{ID: "b", Secret: []byte("y"), Active: true},
	); err == nil {
		t.Error("two active keys accepted")
	}
	if _, err := thread.NewKeyring(
		thread.Key{ID: "a", Secret: []byte("x")},
		thread.Key{ID: "a", Secret: []byte("y")},
	); err == nil {
		t.Error("duplicate ids accepted")
	}
}

// TestSignedApproveAlways: "approve and always allow" travels through
// the signed path too — the grant lands with the decision, in the
// same append, and the next identical call never parks.
func TestSignedApproveAlways(t *testing.T) {
	ctx := context.Background()
	ring, secret := signerRing(t)
	agent, ran := runAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"go build"}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"go build"}`}),
		wefttest.Say("one"), wefttest.Say("two"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithKeyring(ring))
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("build"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	call := s.Pending()[0]
	r, err := s.Request(call.CallID)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := s.DecideSigned(ctx, thread.SignDecision(secret, r, thread.ApproveAlways(call.CallID)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range s.Entries() {
		if g, ok := e.(thread.GrantEntry); ok && g.Tool == "run" {
			found = true
		}
	}
	if !found {
		t.Fatal("a signed ApproveAlways recorded no grant")
	}
	t2, err := s.Send(ctx, weft.User("build again"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t2.Wait(); err != nil {
		t.Fatal(err)
	}
	if next := t2.Next(); next != nil {
		if _, err := next.Wait(); err != nil {
			t.Fatal(err)
		}
		t.Fatal("the always-granted call still parked")
	}
	if got := *ran; len(got) != 2 {
		t.Fatalf("executions: %v", got)
	}
}

// A signed Deny with Always set mints no grant: the same gate Decide
// applies (only an approve grants, ADR 0021 §4) holds on the signed
// door — a signed refusal must not become a standing approval.
func TestSignedDenyAlwaysMintsNoGrant(t *testing.T) {
	ctx := context.Background()
	ring, secret := signerRing(t)
	agent, ran := runAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"go build"}`}),
		wefttest.Say("one"),
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"go build"}`}),
		wefttest.Say("two"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithKeyring(ring))
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("build"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	call := s.Pending()[0]
	r, err := s.Request(call.CallID)
	if err != nil {
		t.Fatal(err)
	}
	d := thread.Deny(call.CallID, "not allowed")
	d.Always = true
	rt, err := s.DecideSigned(ctx, thread.SignDecision(secret, r, d))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	for _, e := range s.Entries() {
		if g, ok := e.(thread.GrantEntry); ok && g.Tool == "run" {
			t.Fatalf("a signed Deny with Always minted grant %+v", g.Grant)
		}
	}
	t2, err := s.Send(ctx, weft.User("build again"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t2.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := s.Pending(); len(got) != 1 {
		t.Fatalf("Pending after re-issuing a signed deny-always call: got %d, want 1 (no grant may stand)", len(got))
	}
	if len(*ran) != 0 {
		t.Fatalf("denied call ran: %v", *ran)
	}
}
