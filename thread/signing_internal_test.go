package thread

import (
	"context"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
)

// TestChallengeCanonical: two claim sets that a plain concatenation
// could not tell apart produce different MACs, because every field is
// length-prefixed; the same claims always produce the same MAC; and
// every field the challenge covers moves the MAC — leave one out and
// it shows. The request entry id and the run id are among them: they
// are what bind a signature to one occurrence of a call.
func TestChallengeCanonical(t *testing.T) {
	key := []byte("canonical-test-key")
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	base := SignedDecision{
		Session: "s", RequestID: "e_req", RunID: "s-t1", CallID: "c", Tool: "tool",
		ArgsSHA256: "h1", Expiry: at, Kind: OutcomeApprove, Who: "avi",
		Nonce: "n1", KeyID: "k1",
	}
	mac := func(edit func(*SignedDecision)) string {
		sd := base
		if edit != nil {
			edit(&sd)
		}
		return string(challengeMAC(key, sd))
	}
	// The split ambiguity: ("ab","c") vs ("a","bc") differ.
	a := mac(func(sd *SignedDecision) { sd.Session, sd.RequestID = "ab", "c" })
	b := mac(func(sd *SignedDecision) { sd.Session, sd.RequestID = "a", "bc" })
	if a == b {
		t.Fatal("field-split claims share a MAC")
	}
	// Determinism: the same claims, the same MAC.
	if again := mac(nil); again != string(challengeMAC(key, base)) {
		t.Fatal("same claims, different MACs")
	}
	// Every field matters.
	variants := map[string]func(*SignedDecision){
		"session": func(sd *SignedDecision) { sd.Session = "s2" },
		"request": func(sd *SignedDecision) { sd.RequestID = "e_req2" },
		"run":     func(sd *SignedDecision) { sd.RunID = "s-t2" },
		"call":    func(sd *SignedDecision) { sd.CallID = "c2" },
		"tool":    func(sd *SignedDecision) { sd.Tool = "tool2" },
		"args":    func(sd *SignedDecision) { sd.ArgsSHA256 = "h2" },
		"expiry":  func(sd *SignedDecision) { sd.Expiry = at.Add(time.Second) },
		"nonce":   func(sd *SignedDecision) { sd.Nonce = "n2" },
		"keyid":   func(sd *SignedDecision) { sd.KeyID = "k2" },
		"kind":    func(sd *SignedDecision) { sd.Kind = OutcomeDeny },
		"reason":  func(sd *SignedDecision) { sd.Reason = "why" },
		"content": func(sd *SignedDecision) { sd.Content = "what" },
		"who":     func(sd *SignedDecision) { sd.Who = "bee" },
		// The always-grant flag moves it: an "approve and always
		// allow" cannot be downgraded to a plain approve in flight.
		"always": func(sd *SignedDecision) { sd.Always = true },
	}
	for name, edit := range variants {
		if mac(edit) == mac(nil) {
			t.Errorf("the %s field does not move the MAC", name)
		}
	}
}

// TestNonceBoundToRequest: a nonce is recognised only for the request
// it was minted for, under the key it was minted under — a made-up
// nonce, another request's, or an empty one is not issued.
func TestNonceBoundToRequest(t *testing.T) {
	key := []byte("nonce-test-key")
	r := Request{Session: "s", ID: "e_req", RunID: "s-t1", CallID: "call_1"}
	n, err := mintNonce(key, r)
	if err != nil {
		t.Fatal(err)
	}
	if !nonceIssued(key, n, r) {
		t.Fatal("a minted nonce is not recognised")
	}
	n2, err := mintNonce(key, r)
	if err != nil {
		t.Fatal(err)
	}
	if n == n2 {
		t.Fatal("two challenges share a nonce")
	}
	other := r
	other.ID, other.RunID = "e_req2", "s-t2" // the same call id, parked again
	if nonceIssued(key, n, other) {
		t.Fatal("a nonce verifies for another occurrence of its call")
	}
	if nonceIssued([]byte("another key"), n, r) {
		t.Fatal("a nonce verifies under a key it was not minted under")
	}
	for _, bad := range []string{"", ".", "abc", "abc.", ".abc", "00000000000000000000000000000000.00000000000000000000000000000000"} {
		if nonceIssued(key, bad, r) {
			t.Errorf("nonce %q recognised", bad)
		}
	}
}

// TestForkInheritsRequireSigned pins the helper a Fork calls for the
// new session's configuration: the origin's signing rule and keyring
// carry over, the rule is written into the fork's header, and a fork
// cannot shed it by passing no option.
func TestForkInheritsRequireSigned(t *testing.T) {
	ring, err := NewKeyring(Key{ID: "k1", Secret: []byte("fork-secret"), Active: true})
	if err != nil {
		t.Fatal(err)
	}
	origin := resolveSession(WithKeyring(ring), RequireSigned())
	fork := resolveSession() // Fork(ctx, entryID) with no options
	h := Header{ID: "s_fork"}
	if err := fork.inheritApprovals(&origin, &h); err != nil {
		t.Fatal(err)
	}
	if !fork.requireSigned || fork.keyring != ring {
		t.Fatalf("fork config: requireSigned=%v keyring=%p", fork.requireSigned, fork.keyring)
	}
	if h.Meta[metaRequireSigned] != "true" {
		t.Fatalf("fork header meta: %v", h.Meta)
	}
	// An unsigned origin leaves the fork as its own options say.
	plain := resolveSession()
	free := resolveSession()
	h2 := Header{ID: "s_fork2"}
	if err := free.inheritApprovals(&plain, &h2); err != nil {
		t.Fatal(err)
	}
	if free.requireSigned || len(h2.Meta) != 0 {
		t.Fatalf("an unsigned origin tightened its fork: %v %v", free.requireSigned, h2.Meta)
	}
}

// The expiry sweep is idempotent: every arming path runs it, and a
// lapsed request is denied by the first one only.
func TestSweepWritesOneDenialPerExpiredRequest(t *testing.T) {
	ctx := context.Background()
	tool := core.Tool("refund", "Refund an order.",
		func(context.Context, struct{}) (string, error) { return "refunded", nil },
		core.RequireApproval())
	agent := core.New(
		wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "refund"}), wefttest.Say("resumed")),
		tool)
	s, err := Create(ctx, Memory(), agent, AutoResume(false), RequestExpiry(time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, core.User("refund it"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond) // strictly past a one-nanosecond expiry
	s.mu.Lock()
	first, err1 := s.resolveExpiredLocked(ctx)
	second, err2 := s.resolveExpiredLocked(ctx)
	s.mu.Unlock()
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if len(first) != 1 || len(second) != 0 {
		t.Fatalf("sweeps denied %d then %d requests, want 1 then 0", len(first), len(second))
	}
}
