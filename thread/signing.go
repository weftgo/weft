package thread

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"time"
)

// A Keyring holds several HMAC keys by id; exactly one is active for
// signing and all of them verify (ADR 0021 §3): rotation without
// invalidating requests in flight — the new active key signs new
// challenges while the old keys vouch for decisions still arriving.
// Keys never touch the session file; only key ids do, so a leaked file
// cannot forge a decision. Construct one with NewKeyring and hand it
// to the sessions that verify signed decisions.
type Keyring struct {
	keys   map[string][]byte
	active string
}

// Key is one key in a Keyring: an id, the secret bytes (any non-empty
// length; HMAC-SHA256 derives its key from them), and whether it is
// the ring's one active signing key. At most one Key in a ring may be
// active; a ring with none signs nothing (verify-only).
type Key struct {
	ID     string
	Secret []byte
	Active bool
}

// NewKeyring builds a ring from keys: ids must be unique and secrets
// non-empty, and at most one key may be active. The ring is immutable
// from here — rotation is a new ring handed to new sessions, while
// the old ring keeps verifying the requests it minted.
func NewKeyring(keys ...Key) (*Keyring, error) {
	r := &Keyring{keys: make(map[string][]byte, len(keys))}
	for _, k := range keys {
		if k.ID == "" {
			return nil, fmt.Errorf("thread: keyring key with no id")
		}
		if len(k.Secret) == 0 {
			return nil, fmt.Errorf("thread: keyring key %q with empty secret", k.ID)
		}
		if _, dup := r.keys[k.ID]; dup {
			return nil, fmt.Errorf("thread: keyring key %q given twice", k.ID)
		}
		if k.Active {
			if r.active != "" {
				return nil, fmt.Errorf("thread: keyring keys %q and %q both active", r.active, k.ID)
			}
			r.active = k.ID
		}
		r.keys[k.ID] = slices.Clone(k.Secret)
	}
	return r, nil
}

// keyringOption carries the ring into a session's configuration.
type keyringOption struct{ r *Keyring }

func (o keyringOption) applySession(c *sessionConfig) {
	if o.r != nil {
		c.keyring = o.r
	}
}

// WithKeyring returns the SessionOption making the session verify
// signed decisions under r — and mint signed challenges from r's
// active key (s.Request). A nil ring is ignored.
func WithKeyring(r *Keyring) SessionOption { return keyringOption{r} }

type requireSignedOption struct{}

func (requireSignedOption) applySession(c *sessionConfig) { c.requireSigned = true }

// RequireSigned returns the SessionOption that closes the in-process
// door: Decide — the unsigned path — is rejected with
// ErrSignatureRequired, and only DecideSigned records decisions (the
// session's own machinery — the Approver, grants, expiry — still
// records, with its own audit trail; it is the caller-held unsigned
// Decide that closes).
func RequireSigned() SessionOption { return requireSignedOption{} }

// The challenge domain and the outcome set a challenge allows (ADR
// 0021 §3's "allowed outcomes"): every outcome Decide accepts. A
// future restriction ships as a new domain string, so old signatures
// never read as new restrictions.
const (
	challengeDomain   = "weft/approval-challenge/v1"
	challengeOutcomes = "approve,deny,resolve,resolve_error"
)

// challengeMAC is the canonical encoding the signer and the verifier
// both compute: every field length-prefixed and semicolon-closed, in
// this fixed order, under the domain string — no ambiguity between
// fields is constructible, whatever the bytes hold.
func challengeMAC(key []byte, session, callID, tool, argsSHA string, expiry time.Time, nonce, keyID string, kind Outcome, reason, content, who string) []byte {
	m := hmac.New(sha256.New, key)
	// The write never fails (hash.Hash.Write's contract); errcheck is
	// silenced once, here, rather than at every field.
	w := func(f string) { _, _ = fmt.Fprintf(m, "%d:%s;", len(f), f) }
	w(challengeDomain)
	w(challengeOutcomes)
	w(session)
	w(callID)
	w(tool)
	w(argsSHA)
	w(strconv.FormatInt(expiry.UnixNano(), 16))
	w(nonce)
	w(keyID)
	w(string(kind))
	w(reason)
	w(content)
	w(who)
	return m.Sum(nil)
}

// SignedDecision is a decision that crossed a process boundary (ADR
// 0021 §3): what the client decided over the challenge it was handed,
// vouched by the MAC the challenge's key computed. Build one with
// SignDecision on the signing side; hand it to Session.DecideSigned
// on the session side. The fields are the challenge's claims — the
// verifier checks them against the pending request, so a stale or
// retargeted signature fails loudly, each way its own error.
type SignedDecision struct {
	Session    string
	CallID     string
	Tool       string
	ArgsSHA256 string
	Expiry     time.Time
	Kind       Outcome
	Reason     string
	Content    string
	Who        string
	Nonce      string
	KeyID      string
	MAC        []byte
}

// SignDecision signs d over r's challenge under key — the client-side
// half of the exchange. r is the Request the session minted (its
// Nonce and KeyID name the challenge); d is what the human decided;
// the MAC covers the challenge fields and the decision together, so
// neither can be altered in flight without failing verification.
func SignDecision(key []byte, r Request, d Decision) SignedDecision {
	sd := SignedDecision{
		Session:    r.Session,
		CallID:     r.CallID,
		Tool:       r.Tool,
		ArgsSHA256: r.ArgsSHA256,
		Expiry:     r.Expiry,
		Kind:       d.Kind,
		Reason:     d.Reason,
		Content:    d.Content,
		Who:        d.Who,
		Nonce:      r.Nonce,
		KeyID:      r.KeyID,
	}
	if sd.Who == "" {
		sd.Who = "signer"
	}
	sd.MAC = challengeMAC(key, sd.Session, sd.CallID, sd.Tool, sd.ArgsSHA256, sd.Expiry, sd.Nonce, sd.KeyID, sd.Kind, sd.Reason, sd.Content, sd.Who)
	return sd
}

// Request returns the pending request for callID with a fresh
// challenge minted under the keyring's active key: a nonce this
// session has never issued, the key's id, and everything a signer
// needs to vouch for a decision over exactly this request (ADR 0021
// §3). The nonce makes the challenge single-use — DecideSigned
// records it, and a replay of the same signature fails with
// ErrReplay, across restarts, because nonces are entries.
func (s *Session) Request(callID string) (Request, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Request{}, fmt.Errorf("thread: challenge nonce: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.keyring == nil || s.cfg.keyring.active == "" {
		return Request{}, fmt.Errorf("thread: Request needs a keyring with an active key")
	}
	for _, r := range s.pendingLocked() {
		if r.CallID == callID {
			r.Nonce = hex.EncodeToString(nonce[:])
			r.KeyID = s.cfg.keyring.active
			return r, nil
		}
	}
	return Request{}, fmt.Errorf("%w: call %q", ErrNotPending, callID)
}

// DecideSigned verifies sd fail-closed, then records it as the
// request's decision — with the nonce and key id, so a replay of the
// same signature is detectable from the file — and resumes when the
// boundary completes (AutoResume, like Decide).
//
// The verification order is the error catalogue (ADR 0021 §3): a key
// the ring does not hold is ErrUnknownKey; a MAC that does not match
// the canonical encoding of the claimed fields — including the
// session, the tool, and the outcome — is ErrBadSignature, compared
// constant-time; a challenge past its expiry is ErrExpired; a nonce a
// recorded decision already answered is ErrReplay; and a signature
// whose arguments hash names different arguments than the pending
// request holds is ErrArgsChanged — the call re-parked with new
// arguments, and the decision was about the old ones. Nothing is
// recorded until every check passes; ErrNotPending keeps Decide's
// rule, raised before any run starts.
func (s *Session) DecideSigned(ctx context.Context, sd SignedDecision) (*Turn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.keyring == nil {
		return nil, fmt.Errorf("thread: DecideSigned needs a keyring")
	}
	key, known := s.cfg.keyring.keys[sd.KeyID]
	if !known {
		return nil, fmt.Errorf("%w: key %q", ErrUnknownKey, sd.KeyID)
	}
	want := challengeMAC(key, sd.Session, sd.CallID, sd.Tool, sd.ArgsSHA256, sd.Expiry, sd.Nonce, sd.KeyID, sd.Kind, sd.Reason, sd.Content, sd.Who)
	if !hmac.Equal(want, sd.MAC) {
		return nil, fmt.Errorf("%w: call %q", ErrBadSignature, sd.CallID)
	}
	if !validOutcome(sd.Kind) {
		return nil, fmt.Errorf("%w: outcome %q", ErrBadSignature, sd.Kind)
	}
	if sd.Session != s.header.ID {
		return nil, fmt.Errorf("%w: challenge names session %q, this is %q", ErrBadSignature, sd.Session, s.header.ID)
	}
	if !sd.Expiry.IsZero() && time.Now().UTC().After(sd.Expiry) {
		return nil, fmt.Errorf("%w: call %q", ErrExpired, sd.CallID)
	}
	// The replay guard runs before the pending check: a replayed
	// signature is a replay whatever became of the call it answered —
	// the boundary may have resolved and moved on, and the answer to a
	// replay is the replay error, not "not pending". Nonces are
	// entries, so the guard survives restarts.
	for _, e := range s.order {
		if d, ok := e.(ApprovalDecisionEntry); ok && d.Nonce != "" && d.Nonce == sd.Nonce {
			return nil, fmt.Errorf("%w: nonce already decided call %q", ErrReplay, d.CallID)
		}
	}
	// The pending request the signature answers: its arguments must be
	// the ones signed, its call the one named.
	var req *Request
	pending := s.pendingLocked()
	for i := range pending {
		if pending[i].CallID == sd.CallID {
			req = &pending[i]
			break
		}
	}
	if req == nil {
		return nil, fmt.Errorf("%w: call %q", ErrNotPending, sd.CallID)
	}
	if req.ArgsSHA256 != sd.ArgsSHA256 {
		return nil, fmt.Errorf("%w: call %q", ErrArgsChanged, sd.CallID)
	}
	if req.Tool != sd.Tool {
		return nil, fmt.Errorf("%w: call %q names tool %q, the request holds %q", ErrBadSignature, sd.CallID, sd.Tool, req.Tool)
	}
	d := Decision{
		CallID:  sd.CallID,
		Kind:    sd.Kind,
		Reason:  sd.Reason,
		Content: sd.Content,
		Who:     sd.Who,
		Via:     "signed",
	}
	e := ApprovalDecisionEntry{
		CallID: d.CallID, Outcome: d.Kind, Reason: d.Reason, Content: d.Content,
		Who: d.Who, Via: d.Via, RunID: req.RunID, Nonce: sd.Nonce, KeyID: sd.KeyID,
	}
	e.ID, e.ParentID, e.Created = s.mintIDLocked(), s.leaf, time.Now().UTC()
	if err := s.st.Append(ctx, s.header.ID, e); err != nil {
		return nil, err
	}
	s.adoptLocked(e)
	if s.cfg.autoResume && s.boundaryLocked() && len(s.pendingLocked()) == 0 {
		return s.armResumeLocked(ctx)
	}
	return nil, nil
}
