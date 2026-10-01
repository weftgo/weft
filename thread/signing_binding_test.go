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

// signedRefusals returns the audit trail's refused-signature entries.
func signedRefusals(s *thread.Session) []thread.ApprovalAuditEntry {
	var out []thread.ApprovalAuditEntry
	for _, e := range s.Audit() {
		if a, ok := e.(thread.ApprovalAuditEntry); ok && a.Step == thread.StepSigned {
			out = append(out, a)
		}
	}
	return out
}

// A signature answers one occurrence of a call, not its call id. A
// challenge minted for run t1's call_1 and never presented does not
// approve the call_1 a later run parks again with the same arguments:
// the request entry and the run it was signed over are not the pending
// ones. ErrNotPending, no decision recorded, the call not run.
func TestSignedDecisionBoundToItsOccurrence(t *testing.T) {
	ctx := context.Background()
	ring, secret := signerRing(t)
	same := wefttest.Call{Name: "refund", ID: "call_1", Args: `{"order_id":"1234"}`}
	agent, ran := refundAgent(
		wefttest.ToolCalls(same),
		wefttest.ToolCalls(same), // the resume's model asks again, same id, same arguments
		wefttest.Say("done"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithKeyring(ring))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	first, err := s.Request(call.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale := thread.SignDecision(secret, first, thread.Approve(call.ID)) // minted, never presented

	// The first occurrence is denied by hand; the resume parks call_1
	// again.
	rt, err := s.Decide(ctx, thread.Deny(call.ID, "not yet"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	pend := s.Pending()
	if len(pend) != 1 || pend[0].CallID != call.ID || pend[0].ArgsSHA256 != first.ArgsSHA256 {
		t.Fatalf("the second occurrence did not park as the first did: %+v", pend)
	}
	if pend[0].ID == first.ID || pend[0].RunID == first.RunID {
		t.Fatalf("two occurrences share a request entry or a run: %+v vs %+v", pend[0], first)
	}

	before := len(decisionsFor(s, call.ID))
	if rt, err := s.DecideSigned(ctx, stale); !errors.Is(err, thread.ErrNotPending) || rt != nil {
		t.Fatalf("a stale occurrence's signature: turn %v, err %v; want ErrNotPending", rt, err)
	}
	if got := len(decisionsFor(s, call.ID)); got != before {
		t.Fatalf("the stale signature recorded %d decision(s)", got-before)
	}
	if got := len(s.Pending()); got != 1 {
		t.Fatalf("Pending after the stale signature: %d, want 1", got)
	}
	if got := ran.snapshot(); len(got) != 0 {
		t.Fatalf("the call ran on a stale signature: %v", got)
	}
	// The refusal is on the trail, with no decision beside it.
	if got := signedRefusals(s); len(got) != 1 || got[0].Detail != "not pending" || got[0].CallID != call.ID {
		t.Fatalf("the refusal's audit: %+v", got)
	}

	// A challenge for the occurrence that is pending works.
	fresh, err := s.Request(call.ID)
	if err != nil {
		t.Fatal(err)
	}
	rt, err = s.DecideSigned(ctx, thread.SignDecision(secret, fresh, thread.Approve(call.ID)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Fatalf("the freshly signed approval: %v", got)
	}
}

// Only a challenge the session minted for the pending request
// verifies: a properly MAC'd decision over a nonce the signer made up
// — or left blank, which would switch the replay guard off — is a bad
// signature, however often it is presented, and records no decision.
func TestSignedDecisionNeedsAnIssuedNonce(t *testing.T) {
	ctx := context.Background()
	ring, secret := signerRing(t)
	agent, ran := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}), wefttest.Say("done"))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithKeyring(ring), thread.AutoResume(false))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	r, err := s.Request(call.ID)
	if err != nil {
		t.Fatal(err)
	}
	for name, nonce := range map[string]string{
		"blank":      "",
		"made up":    "0123456789abcdef0123456789abcdef.0123456789abcdef0123456789abcdef",
		"no tag":     "0123456789abcdef0123456789abcdef",
		"other salt": "ffffffffffffffffffffffffffffffff" + r.Nonce[32:],
	} {
		forged := r
		forged.Nonce = nonce
		sd := thread.SignDecision(secret, forged, thread.Approve(call.ID))
		for attempt := 0; attempt < 2; attempt++ { // a blank nonce must not be replayable either
			if rt, err := s.DecideSigned(ctx, sd); !errors.Is(err, thread.ErrBadSignature) || rt != nil {
				t.Fatalf("%s nonce, attempt %d: turn %v, err %v; want ErrBadSignature", name, attempt, rt, err)
			}
		}
	}
	if got := len(decisionsFor(s, call.ID)); got != 0 {
		t.Fatalf("forged nonces recorded %d decision(s)", got)
	}
	// The issued one verifies once, and replays after.
	sd := thread.SignDecision(secret, r, thread.Approve(call.ID))
	if _, err := s.DecideSigned(ctx, sd); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecideSigned(ctx, sd); !errors.Is(err, thread.ErrReplay) {
		t.Fatalf("replay: got %v, want ErrReplay", err)
	}
	if len(ran.snapshot()) != 0 {
		t.Fatal("AutoResume(false) ran the call")
	}
}

// The session enforces the request's own expiry, never the one a
// signature states: a signer who zeroes or extends the expiry gets a
// bad signature while the request lives, and ErrExpired once it has
// lapsed — with the request denied on the spot, like on every path.
func TestSignedDecisionCannotChooseItsExpiry(t *testing.T) {
	ctx := context.Background()
	ring, secret := signerRing(t)
	park := func(t *testing.T, d time.Duration) (*thread.Session, weft.ToolCallPart, thread.Request, *ranLog) {
		t.Helper()
		agent, ran := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund"}), wefttest.Say("done"))
		s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithKeyring(ring), thread.RequestExpiry(d))
		if err != nil {
			t.Fatal(err)
		}
		call := parkSend(t, s, ctx)
		r, err := s.Request(call.ID)
		if err != nil {
			t.Fatal(err)
		}
		return s, call, r, ran
	}

	t.Run("altered while the request lives", func(t *testing.T) {
		s, call, r, _ := park(t, time.Hour)
		for name, expiry := range map[string]time.Time{
			"zeroed":   {},
			"extended": r.Expiry.Add(24 * time.Hour),
		} {
			altered := r
			altered.Expiry = expiry
			sd := thread.SignDecision(secret, altered, thread.Approve(call.ID))
			if _, err := s.DecideSigned(ctx, sd); !errors.Is(err, thread.ErrBadSignature) {
				t.Fatalf("%s expiry: got %v, want ErrBadSignature", name, err)
			}
		}
		if got := len(s.Pending()); got != 1 {
			t.Fatalf("Pending: %d", got)
		}
	})

	t.Run("zeroed after the request lapsed", func(t *testing.T) {
		s, call, r, ran := park(t, 20*time.Millisecond)
		time.Sleep(time.Until(r.Expiry) + 5*time.Millisecond)
		altered := r
		altered.Expiry = time.Time{} // "never expires", says the client
		sd := thread.SignDecision(secret, altered, thread.Approve(call.ID))
		if _, err := s.DecideSigned(ctx, sd); !errors.Is(err, thread.ErrExpired) {
			t.Fatalf("a client-zeroed expiry on a lapsed request: got %v, want ErrExpired", err)
		}
		// That attempt denied the request on the spot: a second
		// signature finds nothing pending.
		honest := thread.SignDecision(secret, r, thread.Approve(call.ID))
		if _, err := s.DecideSigned(ctx, honest); !errors.Is(err, thread.ErrNotPending) {
			t.Fatalf("a signature after the expiry denial: got %v, want ErrNotPending", err)
		}
		// The request was denied with the expiry reason; the call never
		// ran; a challenge for it is no longer minted.
		counts := countApprovalEntries(s)
		if counts["audit:expiry:denied"] != 1 || counts["decision:"+call.ID+":deny:expiry"] != 1 {
			t.Fatalf("the lapsed request was not denied on the spot: %v", counts)
		}
		if counts["decision:"+call.ID+":approve:signed"] != 0 {
			t.Fatalf("a signed approval of a lapsed request was recorded: %v", counts)
		}
		if got := ran.snapshot(); len(got) != 0 {
			t.Fatalf("the lapsed request's call ran: %v", got)
		}
	})

	t.Run("an honest signature on a lapsed request is ErrExpired", func(t *testing.T) {
		s, call, r, _ := park(t, 20*time.Millisecond)
		time.Sleep(time.Until(r.Expiry) + 5*time.Millisecond)
		if _, err := s.Request(call.ID); !errors.Is(err, thread.ErrExpired) {
			t.Fatalf("Request on a lapsed request: %v, want ErrExpired", err)
		}
		honest := thread.SignDecision(secret, r, thread.Approve(call.ID))
		if _, err := s.DecideSigned(ctx, honest); !errors.Is(err, thread.ErrExpired) {
			t.Fatalf("got %v, want ErrExpired", err)
		}
		if got := signedRefusals(s); len(got) != 1 || got[0].Detail != "expired" {
			t.Fatalf("the refusal's audit: %+v", got)
		}
	})
}

// RequireSigned is the session's rule, kept in its header: a later
// Open enforces it without being told, an Open may tighten a session
// created without it, and a configuration that could never decide —
// no keyring, or a verify-only one — fails at Create or Open.
func TestRequireSignedIsDurable(t *testing.T) {
	ctx := context.Background()
	ring, secret := signerRing(t)
	st, err := jsonl.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	agent, ran := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund"}), wefttest.Say("done"))
	s, err := thread.Create(ctx, st, agent, thread.WithKeyring(ring), thread.RequireSigned())
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)

	// Reopened without the option: still signed.
	again, err := thread.Open(ctx, st, s.ID(), agent, thread.WithKeyring(ring))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := again.Decide(ctx, thread.Approve(call.ID)); !errors.Is(err, thread.ErrSignatureRequired) {
		t.Fatalf("unsigned Decide after a reopen without RequireSigned: %v, want ErrSignatureRequired", err)
	}
	// Reopened with no keyring at all: a session nobody could decide.
	if _, err := thread.Open(ctx, st, s.ID(), agent); err == nil {
		t.Fatal("Open of a signed session without a keyring succeeded")
	}
	verifyOnly, err := thread.NewKeyring(thread.Key{ID: "k1", Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := thread.Open(ctx, st, s.ID(), agent, thread.WithKeyring(verifyOnly)); err == nil {
		t.Fatal("Open of a signed session with a verify-only keyring succeeded")
	}
	// The signed door still works on the reopened session.
	r, err := again.Request(call.ID)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := again.DecideSigned(ctx, thread.SignDecision(secret, r, thread.Approve(call.ID)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Fatalf("signed approval after the reopen: %v", got)
	}

	// Create without a way to decide fails, loudly.
	if _, err := thread.Create(ctx, st, agent, thread.RequireSigned()); err == nil {
		t.Fatal("Create with RequireSigned and no keyring succeeded")
	}
	if _, err := thread.Create(ctx, st, agent, thread.RequireSigned(), thread.WithKeyring(verifyOnly)); err == nil {
		t.Fatal("Create with RequireSigned and a verify-only keyring succeeded")
	}

	// Open tightens a session created without the rule; the next plain
	// Open is unsigned again — the header never said otherwise.
	agent2, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund"}), wefttest.Say("done"))
	plain, err := thread.Create(ctx, st, agent2)
	if err != nil {
		t.Fatal(err)
	}
	call2 := parkSend(t, plain, ctx)
	tight, err := thread.Open(ctx, st, plain.ID(), agent2, thread.WithKeyring(ring), thread.RequireSigned())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tight.Decide(ctx, thread.Approve(call2.ID)); !errors.Is(err, thread.ErrSignatureRequired) {
		t.Fatalf("a tightened Open: %v, want ErrSignatureRequired", err)
	}
}

// Under Quorum a signed approval counts as its key: one key signing
// as "alice" and then as "bob" is one approver, and the call stays
// pending until a second key approves.
func TestSignedQuorumCountsKeys(t *testing.T) {
	ctx := context.Background()
	alice := thread.Key{ID: "alice-key", Secret: []byte("alice's own secret key"), Active: true}
	bob := thread.Key{ID: "bob-key", Secret: []byte("bob's own secret key!!")}
	ring, err := thread.NewKeyring(alice, bob)
	if err != nil {
		t.Fatal(err)
	}
	agent, ran := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"9"}`}), wefttest.Say("done"))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithKeyring(ring), thread.RequireSigned(), thread.Quorum(2))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	sign := func(k thread.Key, who string) thread.SignedDecision {
		t.Helper()
		r, err := s.Request(call.ID)
		if err != nil {
			t.Fatal(err)
		}
		d := thread.Approve(call.ID)
		d.Who = who
		sd, err := k.Sign(r, d)
		if err != nil {
			t.Fatal(err)
		}
		return sd
	}
	for _, who := range []string{"alice", "bob"} { // one key, two names
		if rt, err := s.DecideSigned(ctx, sign(alice, who)); err != nil || rt != nil {
			t.Fatalf("alice's key signing as %q: turn %v, err %v", who, rt, err)
		}
	}
	if got := len(s.Pending()); got != 1 {
		t.Fatalf("one key under two names satisfied Quorum(2): Pending=%d", got)
	}
	if len(ran.snapshot()) != 0 {
		t.Fatal("the call ran on one approver")
	}
	rt, err := s.DecideSigned(ctx, sign(bob, "alice")) // a second key, whatever it calls itself
	if err != nil {
		t.Fatal(err)
	}
	if rt == nil {
		t.Fatal("two keys did not complete Quorum(2)")
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Fatalf("the call after two keys approved: %v", got)
	}
}

// Keyring.Sign signs under the key the challenge names — no caller
// maps key ids to secrets — and tells the ring's holder when it lacks
// that key; a Pending view is not a challenge.
func TestKeyringSign(t *testing.T) {
	ctx := context.Background()
	ring, _ := signerRing(t)
	agent, ran := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund"}), wefttest.Say("done"))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithKeyring(ring))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	if _, err := ring.Sign(s.Pending()[0], thread.Approve(call.ID)); err == nil {
		t.Fatal("Sign accepted a Pending view with no nonce")
	}
	r, err := s.Request(call.ID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := thread.NewKeyring(thread.Key{ID: "k7", Secret: []byte("someone else's ring"), Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Sign(r, thread.Approve(call.ID)); !errors.Is(err, thread.ErrUnknownKey) {
		t.Fatalf("Sign on a ring without the challenge's key: %v, want ErrUnknownKey", err)
	}
	sd, err := ring.Sign(r, thread.Approve(call.ID))
	if err != nil {
		t.Fatal(err)
	}
	if sd.Who != "k1" {
		t.Fatalf("an unnamed signer is recorded as %q, want the key id", sd.Who)
	}
	rt, err := s.DecideSigned(ctx, sd)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Fatalf("approved through Keyring.Sign: %v", got)
	}
	if _, err := (thread.Key{ID: "k", Secret: nil}).Sign(r, thread.Approve(call.ID)); err == nil {
		t.Fatal("Key.Sign accepted a key with no secret")
	}
}

// A ring is one or more keys; with none active it verifies and mints
// nothing.
func TestKeyringShapes(t *testing.T) {
	ctx := context.Background()
	if _, err := thread.NewKeyring(); err == nil {
		t.Fatal("a ring with no keys was accepted")
	}
	verifyOnly, err := thread.NewKeyring(thread.Key{ID: "k1", Secret: []byte("verify only")})
	if err != nil {
		t.Fatalf("a ring with no active key is verify-only, not an error: %v", err)
	}
	agent, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund"}), wefttest.Say("done"))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithKeyring(verifyOnly))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	if _, err := s.Request(call.ID); err == nil {
		t.Fatal("a verify-only ring minted a challenge")
	}
}

// Rotation for real: the session reopened under the rotated ring —
// a new active key, the old one kept — still accepts a decision
// signed over a challenge the old ring minted.
func TestKeyRotationAcrossReopen(t *testing.T) {
	ctx := context.Background()
	old, secret := signerRing(t)
	st := thread.Memory()
	agent, ran := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund"}), wefttest.Say("done"))
	s, err := thread.Create(ctx, st, agent, thread.WithKeyring(old))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	r, err := s.Request(call.ID)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := thread.NewKeyring(
		thread.Key{ID: "k1", Secret: secret},
		thread.Key{ID: "k2", Secret: []byte("the new key, also secret!"), Active: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := thread.Open(ctx, st, s.ID(), agent, thread.WithKeyring(rotated))
	if err != nil {
		t.Fatal(err)
	}
	rt, err := s2.DecideSigned(ctx, thread.SignDecision(secret, r, thread.Approve(call.ID)))
	if err != nil {
		t.Fatalf("an in-flight challenge after rotation: %v", err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Fatalf("approved across the rotation: %v", got)
	}
}

// Refused signed decisions are audited — each with its reason and no
// decision — within bounds: a signature that fails its MAC and names
// no pending call leaves nothing, and a request keeps at most 16
// refusals however many arrive.
func TestSignedRefusalsAreAuditedWithinBounds(t *testing.T) {
	ctx := context.Background()
	ring, secret := signerRing(t)
	agent, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}), wefttest.Say("done"))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithKeyring(ring), thread.AutoResume(false))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	r, err := s.Request(call.ID)
	if err != nil {
		t.Fatal(err)
	}

	// An unverifiable signature about a call nobody parked: nothing.
	stranger := r
	stranger.CallID = "call_nobody_parked"
	before := len(s.Entries())
	if _, err := s.DecideSigned(ctx, thread.SignDecision([]byte("not a ring key"), stranger, thread.Approve("call_nobody_parked"))); !errors.Is(err, thread.ErrBadSignature) {
		t.Fatal(err)
	}
	if got := len(s.Entries()); got != before {
		t.Fatalf("an unverifiable signature about no pending call wrote %d entries", got-before)
	}

	// Args changed, then the accepted decision, then its replay.
	changed := r
	changed.ArgsSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := s.DecideSigned(ctx, thread.SignDecision(secret, changed, thread.Approve(call.ID))); !errors.Is(err, thread.ErrArgsChanged) {
		t.Fatal(err)
	}
	good := thread.SignDecision(secret, r, thread.Approve(call.ID))
	if _, err := s.DecideSigned(ctx, good); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecideSigned(ctx, good); !errors.Is(err, thread.ErrReplay) {
		t.Fatal(err)
	}
	var details []string
	for _, a := range signedRefusals(s) {
		if a.Outcome != "refused" || a.CallID != call.ID || a.KeyID != "k1" {
			t.Fatalf("refusal entry: %+v", a)
		}
		details = append(details, a.Detail)
	}
	if len(details) != 2 || details[0] != "args changed" || details[1] != "replayed" {
		t.Fatalf("refusal reasons: %v, want [args changed, replayed]", details)
	}
	if got := len(decisionsFor(s, call.ID)); got != 1 {
		t.Fatalf("decisions recorded: %d, want the one accepted", got)
	}

	// A flood of replays stops being written at the bound.
	for i := 0; i < 40; i++ {
		if _, err := s.DecideSigned(ctx, good); !errors.Is(err, thread.ErrReplay) {
			t.Fatal(err)
		}
	}
	if got := len(signedRefusals(s)); got != 16 {
		t.Fatalf("refusals kept for one request: %d, want the bound of 16", got)
	}
}
