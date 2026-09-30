package thread_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// FuzzDecideSigned (step 7.1): the signed-decision verifier over
// hostile input never panics, answers only from its error catalogue,
// and records nothing unless every check passed. Each input builds its
// own parked session — the challenge (session id, nonce) is minted per
// process, so a seed signed elsewhere could never verify — and derives
// the decision from a properly signed one: mask picks which claims the
// input overrides, resign says whether the MAC is recomputed over them
// (a signer with the key vouching for a wrong claim — the checks after
// the MAC) or left stale (a client tampering in flight — the MAC's
// door), and pre decides the boundary once first, so the same nonce
// reads as a replay and a fresh one as not pending. Every catalogue
// entry and the accepted path are reachable from the seeds.
func FuzzDecideSigned(f *testing.F) {
	const (
		mSession = 1 << iota
		mCall
		mTool
		mArgs
		mNonce
		mKey
		mKind
		mExpiry
	)
	f.Add(uint16(0), false, false, "", "", "", "", "", "", "", int64(0), false) // accepted
	f.Add(uint16(mNonce), false, false, "", "", "", "", "x", "", "", int64(0), false)
	f.Add(uint16(mKey), true, false, "", "", "", "", "", "k9", "", int64(0), false)          // ErrUnknownKey
	f.Add(uint16(mSession), true, false, "s_other", "", "", "", "", "", "", int64(0), false) // session mismatch
	f.Add(uint16(mExpiry), true, false, "", "", "", "", "", "", "", int64(-60), false)       // ErrExpired
	f.Add(uint16(0), false, true, "", "", "", "", "", "", "", int64(0), false)               // ErrReplay
	f.Add(uint16(mNonce), true, true, "", "", "", "", "fresh", "", "", int64(0), false)      // ErrNotPending
	f.Add(uint16(mCall), true, false, "", "call_nope", "", "", "", "", "", int64(0), false)  // ErrNotPending
	f.Add(uint16(mArgs), true, false, "", "", "", "deadbeef", "", "", "", int64(0), false)   // ErrArgsChanged
	f.Add(uint16(mTool), true, false, "", "", "other", "", "", "", "", int64(0), false)      // tool mismatch
	f.Add(uint16(mKind), true, false, "", "", "", "", "", "", "bogus", int64(0), false)      // bad outcome
	f.Add(uint16(mKind), true, false, "", "", "", "", "", "", string(thread.OutcomeDeny), int64(0), true)

	f.Fuzz(func(t *testing.T, mask uint16, resign, pre bool,
		session, callID, tool, argsSHA, nonce, keyID, kind string, expirySec int64, always bool) {
		ctx := context.Background()
		key := []byte("fuzz-key-material")
		ring, err := thread.NewKeyring(thread.Key{ID: "k1", Secret: key, Active: true})
		if err != nil {
			t.Fatal(err)
		}
		agent, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}))
		s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithKeyring(ring), thread.AutoResume(false),
			thread.ContextWindow(200_000)) // a known window: no per-input compaction warning
		if err != nil {
			t.Fatal(err)
		}
		turn, err := s.Send(ctx, weft.User("refund it"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := turn.Wait(); err != nil {
			t.Fatal(err)
		}
		pend := s.Pending()
		if len(pend) != 1 {
			t.Fatalf("the parked call never parked: %+v", pend)
		}
		req, err := s.Request(pend[0].CallID)
		if err != nil {
			t.Fatal(err)
		}
		if pre {
			if _, err := s.DecideSigned(ctx, thread.SignDecision(key, req, thread.Approve(req.CallID))); err != nil {
				t.Fatalf("the pre-decision: %v", err)
			}
		}

		// The claims, each overridden when its mask bit is set.
		r, kindV := req, thread.OutcomeApprove
		set := func(bit uint16, dst *string, v string) {
			if mask&bit != 0 {
				*dst = v
			}
		}
		set(mSession, &r.Session, session)
		set(mCall, &r.CallID, callID)
		set(mTool, &r.Tool, tool)
		set(mArgs, &r.ArgsSHA256, argsSHA)
		set(mNonce, &r.Nonce, nonce)
		set(mKey, &r.KeyID, keyID)
		if mask&mKind != 0 {
			kindV = thread.Outcome(kind)
		}
		if mask&mExpiry != 0 {
			r.Expiry = time.Now().UTC().Add(time.Duration(expirySec%(1<<20)) * time.Second)
		}
		d := thread.Decision{CallID: r.CallID, Kind: kindV, Always: always}
		var sd thread.SignedDecision
		if resign {
			sd = thread.SignDecision(key, r, d)
		} else {
			// Signed over the true challenge, then the claims swapped
			// in flight — the MAC no longer covers them.
			sd = thread.SignDecision(key, req, thread.Decision{CallID: req.CallID, Kind: thread.OutcomeApprove, Always: always})
			sd.Session, sd.CallID, sd.Tool, sd.ArgsSHA256 = r.Session, r.CallID, r.Tool, r.ArgsSHA256
			sd.Nonce, sd.KeyID, sd.Expiry, sd.Kind = r.Nonce, r.KeyID, r.Expiry, kindV
		}

		before, leaf := len(s.Entries()), s.Leaf()
		_, err = s.DecideSigned(ctx, sd)
		switch {
		case err == nil:
			if p := s.Pending(); len(p) != 0 {
				t.Fatalf("a verified decision left the boundary open: %+v", p)
			}
			return
		case errors.Is(err, thread.ErrUnknownKey),
			errors.Is(err, thread.ErrBadSignature),
			errors.Is(err, thread.ErrExpired),
			errors.Is(err, thread.ErrReplay),
			errors.Is(err, thread.ErrNotPending),
			errors.Is(err, thread.ErrArgsChanged):
			// the catalogue, in the order DecideSigned checks
		default:
			t.Fatalf("an error outside the catalogue: %v", err)
		}
		if n := len(s.Entries()); n != before || s.Leaf() != leaf {
			t.Fatalf("a rejected decision (%v) recorded: entries %d → %d, leaf %q → %q", err, before, n, leaf, s.Leaf())
		}
	})
}
