package thread

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A Keyring holds HMAC keys by id (ADR 0021 §3). Every key verifies;
// at most one is active, and the active key is the one new challenges
// are minted under — rotation without invalidating requests in flight:
// the new active key signs new challenges while the old keys vouch for
// decisions still arriving. A ring with no active key is verify-only:
// it checks decisions over challenges minted earlier and mints none,
// so Session.Request fails on it and RequireSigned refuses it. Keys
// never touch the session file; only key ids do, so a leaked file
// cannot forge a decision.
//
// One key is one approver: under Quorum a signed approval counts as
// its key, whatever Who it carries — give each approver their own key
// and the session a ring holding all of them.
//
// Construct one with NewKeyring and hand it to the sessions that
// verify signed decisions (WithKeyring). The signing side signs with
// what it holds: an approver with a key of their own signs with
// Key.Sign; a process holding the whole ring signs with Keyring.Sign
// under the key the challenge names; SignDecision is the same under a
// bare secret. A Keyring is immutable and safe for concurrent use.
type Keyring struct {
	keys   map[string][]byte
	active string
}

// Key is one key in a Keyring: an id, the secret bytes (any non-empty
// length; HMAC-SHA256 derives its key from them), and whether it is
// the ring's active key — the one challenges are minted under. At
// most one Key in a ring may be active. A Key is also what an
// approver holds on the signing side: Sign signs a decision as this
// key.
type Key struct {
	ID     string
	Secret []byte
	Active bool
}

// Sign signs d over the challenge req carries as this key — the
// approver's half of the exchange when each approver holds a key of
// their own: the signature names k.ID, whichever key the challenge
// was minted under, so under Quorum it counts as this approver and no
// other. The session verifying it must hold k in its ring. req must
// be a challenge, the value Session.Request returned: Sign fails on
// one without a nonce, and on a key with no id or no secret.
func (k Key) Sign(req Request, d Decision) (SignedDecision, error) {
	if k.ID == "" || len(k.Secret) == 0 {
		return SignedDecision{}, fmt.Errorf("thread: Sign with a key that has no id or no secret")
	}
	if req.Nonce == "" {
		return SignedDecision{}, fmt.Errorf("thread: Sign needs a challenge: the Request from Session.Request, not a Pending view")
	}
	req.KeyID = k.ID
	return SignDecision(k.Secret, req, d), nil
}

// NewKeyring builds a ring from keys: at least one key, ids unique
// and non-empty, secrets non-empty, at most one key active. A ring
// with no active key is verify-only (see Keyring). The ring is
// immutable from here — rotation is a new ring handed to new
// sessions, holding the old keys so the requests they minted keep
// verifying.
func NewKeyring(keys ...Key) (*Keyring, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("thread: keyring with no keys")
	}
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

// Sign signs d over the challenge req carries — the signing side's
// half of the exchange for a caller that holds the ring: the key is
// the one req.KeyID names (the key the challenge was minted under),
// looked up here, so no caller maps key ids to secrets by hand. req must be a challenge, the value
// Session.Request returned (a Pending view carries no nonce): Sign
// fails on one without a nonce, and with ErrUnknownKey when the ring
// does not hold the challenge's key — the ring's holder may know
// which keys it has; DecideSigned tells a remote caller nothing of
// the kind.
func (r *Keyring) Sign(req Request, d Decision) (SignedDecision, error) {
	if r == nil {
		return SignedDecision{}, fmt.Errorf("thread: Sign on a nil keyring")
	}
	if req.Nonce == "" {
		return SignedDecision{}, fmt.Errorf("thread: Sign needs a challenge: the Request from Session.Request, not a Pending view")
	}
	key, ok := r.keys[req.KeyID]
	if !ok {
		return SignedDecision{}, fmt.Errorf("%w: key %q", ErrUnknownKey, req.KeyID)
	}
	return SignDecision(key, req, d), nil
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
// ErrSignatureRequired, and only DecideSigned records a caller's
// decision. The session's own machinery still records, each path with
// its own audit trail: grants, the Approver, expiry, the denial an
// interrupting Send makes, a pool delegation's resolution.
//
// The rule is the session's, not the process's: Create writes it into
// the session's header (metadata key "weft.require_signed"), so every
// later Open enforces it whether or not it passes the option. Open
// with RequireSigned on a session created without it tightens that
// one Session value; nothing loosens a session created with it.
//
// It needs a way to decide: Create and Open fail when RequireSigned
// is in force and the session has no keyring (WithKeyring), or a ring
// with no active key — the session could mint no challenge, and no
// call it parked could ever be decided.
func RequireSigned() SessionOption { return requireSignedOption{} }

// metaRequireSigned is the header metadata key that makes
// RequireSigned durable: "true" on a session created with the option.
// The header is immutable, so the rule cannot be edited away later —
// an info entry's metadata overlays what Session.Meta reports, never
// what this reads.
const metaRequireSigned = "weft.require_signed"

// checkApprovals fails a configuration that would build a session
// unable to do what it was asked: an Approver with no time to answer
// in, or RequireSigned with nothing to mint a challenge under.
func (c *sessionConfig) checkApprovals() error {
	if c.approver != nil && c.approverTimeout <= 0 {
		return fmt.Errorf("thread: WithApprover needs a positive timeout, got %v", c.approverTimeout)
	}
	if c.requireSigned {
		if c.keyring == nil {
			return fmt.Errorf("thread: RequireSigned needs WithKeyring: without a keyring no decision could be verified")
		}
		if c.keyring.active == "" {
			return fmt.Errorf("thread: RequireSigned needs a keyring with an active key: a verify-only ring mints no challenge")
		}
	}
	return nil
}

// sealApprovals is Create's approvals step: the configuration is
// checked and, under RequireSigned, the rule is written into the
// header about to be created — where every later Open finds it. A
// header that already carries the key (WithMeta) turns the rule on.
func (c *sessionConfig) sealApprovals(h *Header) error {
	c.requireSigned = c.requireSigned || h.Meta[metaRequireSigned] == "true"
	if err := c.checkApprovals(); err != nil {
		return err
	}
	if c.requireSigned {
		if h.Meta == nil {
			h.Meta = map[string]string{}
		}
		h.Meta[metaRequireSigned] = "true"
	}
	return nil
}

// adoptApprovals is Open's approvals step: RequireSigned is in force
// when the option asks for it or the header was created with it —
// the header only ever tightens — and the resulting configuration is
// checked.
func (c *sessionConfig) adoptApprovals(h Header) error {
	c.requireSigned = c.requireSigned || h.Meta[metaRequireSigned] == "true"
	return c.checkApprovals()
}

// inheritApprovals is the approvals step for a session created from
// another (a Fork): the new session takes the origin's signing rule —
// a fork of a session that requires signed decisions requires them
// too — and, when it was given no ring of its own, the origin's
// keyring; then it is sealed like any created session.
func (c *sessionConfig) inheritApprovals(origin *sessionConfig, h *Header) error {
	c.requireSigned = c.requireSigned || origin.requireSigned
	if c.keyring == nil {
		c.keyring = origin.keyring
	}
	return c.sealApprovals(h)
}

// The domains a challenge's MAC and its nonce are computed under. A
// change to what either covers ships as a new domain string, so a
// signature made under an old layout never verifies under a new one.
const (
	challengeDomain = "weft/approval-challenge/v2"
	nonceDomain     = "weft/approval-nonce/v1"
)

// macFields writes fields into an HMAC under key in the canonical
// encoding: every field length-prefixed and semicolon-closed, in the
// order given — no ambiguity between fields is constructible,
// whatever the bytes hold.
func macFields(key []byte, fields ...string) []byte {
	m := hmac.New(sha256.New, key)
	for _, f := range fields {
		// The write never fails (hash.Hash.Write's contract); errcheck
		// is silenced once, here, rather than at every field.
		_, _ = fmt.Fprintf(m, "%d:%s;", len(f), f)
	}
	return m.Sum(nil)
}

// challengeMAC is what the signer and the verifier both compute over
// a signed decision: the challenge — session, request entry, run,
// call, tool, arguments hash, expiry, nonce, key id — and the
// decision made over it, in this fixed order under the challenge
// domain. The request entry and the run are what bind a signature to
// one occurrence of a call: call ids repeat across turns, those two
// never do.
func challengeMAC(key []byte, sd SignedDecision) []byte {
	kindTok := string(sd.Kind)
	if sd.Always {
		kindTok += "+always"
	}
	return macFields(key,
		challengeDomain,
		sd.Session,
		sd.RequestID,
		sd.RunID,
		sd.CallID,
		sd.Tool,
		sd.ArgsSHA256,
		strconv.FormatInt(sd.Expiry.UnixNano(), 16),
		sd.Nonce,
		sd.KeyID,
		kindTok,
		sd.Reason,
		sd.Content,
		sd.Who,
	)
}

// mintNonce returns a fresh challenge nonce for one request: 16
// random bytes and, after a dot, a tag — the MAC of those bytes with
// the session, the request entry, the run and the call under the
// signing key. The tag is what lets the session recognise a nonce as
// one it issued for this request without storing it: a nonce made up
// elsewhere, or lifted from another request's challenge, does not
// carry it.
func mintNonce(key []byte, r Request) (string, error) {
	var salt [16]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return "", fmt.Errorf("thread: challenge nonce: %w", err)
	}
	s := hex.EncodeToString(salt[:])
	return s + "." + nonceTag(key, s, r), nil
}

// nonceTag is the tag half of a nonce minted for r with salt.
func nonceTag(key []byte, salt string, r Request) string {
	return hex.EncodeToString(macFields(key, nonceDomain, r.Session, r.ID, r.RunID, r.CallID, salt)[:16])
}

// nonceIssued reports whether nonce is one mintNonce made for r under
// key — compared in constant time.
func nonceIssued(key []byte, nonce string, r Request) bool {
	salt, tag, ok := strings.Cut(nonce, ".")
	if !ok || salt == "" {
		return false
	}
	return hmac.Equal([]byte(tag), []byte(nonceTag(key, salt, r)))
}

// issued reports whether nonce was minted for r under some key the
// ring holds: the key a challenge is minted under (the ring's active
// one at the time) need not be the key the decision is signed with —
// each approver signs with their own — so the nonce is checked
// against the ring, not against the signer.
func (r *Keyring) issued(nonce string, req Request) bool {
	ok := false
	for _, key := range r.keys {
		if nonceIssued(key, nonce, req) {
			ok = true // no early exit: every key costs the same
		}
	}
	return ok
}

// SignedDecision is a decision that crossed a process boundary (ADR
// 0021 §3): what the client decided over the challenge it was handed,
// vouched by the MAC the challenge's key computed. Build one with
// Keyring.Sign or SignDecision on the signing side; hand it to
// Session.DecideSigned on the session side. The fields are the
// challenge's claims — the verifier checks them against the pending
// request, so a stale or retargeted signature fails loudly.
type SignedDecision struct {
	Session string
	// RequestID and RunID name the occurrence the challenge was minted
	// for: the request entry's id and the run that parked the call. A
	// signature answers that occurrence only — the same call id parked
	// again by a later run is a different request.
	RequestID  string
	RunID      string
	CallID     string
	Tool       string
	ArgsSHA256 string
	// Expiry is the request's expiry as the challenge stated it. It
	// must equal the request's own: the session enforces the request's
	// expiry, never a time the signer chose.
	Expiry  time.Time
	Kind    Outcome
	Reason  string
	Content string
	// Who is a label for the audit trail. The decision's identity —
	// what Quorum counts — is KeyID.
	Who string
	// Always approves and grants the same thing for the future, the
	// signed ApproveAlways (Decision.Always carries it through
	// SignDecision).
	Always bool
	Nonce  string
	KeyID  string
	MAC    []byte
}

// SignDecision signs d over r's challenge under key — the client-side
// half of the exchange for a signer that holds a bare secret
// (Keyring.Sign is the same thing with the key looked up, Key.Sign
// the same as a key of the signer's own). r is the Request the
// session minted (its Nonce names the challenge) and key must be the
// secret of the key r.KeyID names; d is what the human decided; the
// MAC covers the challenge fields and the decision together, so
// neither can be altered in flight without failing verification. An
// empty d.Who is signed as the key's id.
func SignDecision(key []byte, r Request, d Decision) SignedDecision {
	sd := SignedDecision{
		Session:    r.Session,
		RequestID:  r.ID,
		RunID:      r.RunID,
		CallID:     r.CallID,
		Tool:       r.Tool,
		ArgsSHA256: r.ArgsSHA256,
		Expiry:     r.Expiry,
		Kind:       d.Kind,
		Reason:     d.Reason,
		Content:    d.Content,
		Who:        d.Who,
		Always:     d.Always,
		Nonce:      r.Nonce,
		KeyID:      r.KeyID,
	}
	if sd.Who == "" {
		sd.Who = r.KeyID
	}
	sd.MAC = challengeMAC(key, sd)
	return sd
}

// Request returns the pending request for callID with a fresh
// challenge minted under the keyring's active key: a nonce, the key's
// id, and everything a signer needs to vouch for a decision over
// exactly this request (ADR 0021 §3). The challenge is bound to the
// request entry — this occurrence of the call, not its call id — and
// is single-use: DecideSigned records the nonce, and a replay of the
// same signature fails with ErrReplay, across restarts, because
// recorded nonces are entries. Request writes nothing; it may be
// called any number of times, each call minting another valid
// challenge for the same request.
//
// It fails with ErrNotPending when callID is not pending, with
// ErrDelegated when it is a call delegating to a pool child (which
// takes no decision), with ErrExpired when the request is past its
// expiry (no decision could be accepted for it), and when the session
// has no keyring with an active key.
func (s *Session) Request(callID string) (Request, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ring := s.cfg.keyring
	if ring == nil || ring.active == "" {
		return Request{}, fmt.Errorf("thread: Request needs a keyring with an active key")
	}
	if child := s.approvalWalkLocked().wrappers[callID]; child != "" {
		// A delegating call takes no decision, so it gets no challenge.
		return Request{}, fmt.Errorf("%w: call %q completes with child session %s", ErrDelegated, callID, child)
	}
	now := s.approvalNow()
	for _, r := range s.pendingLocked() {
		if r.CallID != callID {
			continue
		}
		if !r.Expiry.IsZero() && now.After(r.Expiry) {
			return Request{}, fmt.Errorf("%w: call %q: %s", ErrExpired, callID, expiryDetail(r.Expiry))
		}
		nonce, err := mintNonce(ring.keys[ring.active], r)
		if err != nil {
			return Request{}, err
		}
		r.Nonce, r.KeyID = nonce, ring.active
		return r, nil
	}
	return Request{}, fmt.Errorf("%w: call %q", ErrNotPending, callID)
}

// maxSignedRefusals bounds the StepSigned audit entries recorded per
// request: a refused signature costs its sender nothing, and the
// session's log must not grow at a stranger's will.
const maxSignedRefusals = 16

// DecideSigned verifies sd fail-closed, then records it as the
// request's decision — with the nonce and key id, so a replay of the
// same signature is detectable from the file — and resumes when the
// boundary completes (AutoResume, like Decide). The decision is
// recorded with Via "signed"; its identity under Quorum is the key.
//
// No decision is recorded until every check passes. The checks, in
// order, and the error each fails with (ADR 0021 §3):
//
//   - the MAC, compared in constant time over the canonical encoding
//     of every claimed field, under the key sd.KeyID names; an
//     outcome that is none of the four; a session other than this
//     one; an empty nonce — ErrBadSignature. A key the ring does not
//     hold fails the same way, with the same words: DecideSigned
//     never says which key ids exist.
//   - a nonce a recorded decision already answered — ErrReplay,
//     whatever became of the call since.
//   - the request past its own expiry — ErrExpired. The session
//     enforces the request's expiry, not the one the signature
//     states: the lapsed request is denied on the spot with the
//     stated reason, like on every other path.
//   - no pending call with that id, or a pending one that is another
//     occurrence (its request entry or run differ from the ones
//     signed — the call id was parked again by a later run) —
//     ErrNotPending.
//   - a nonce no key of the ring minted for this request, an expiry
//     or a tool that differ from the request's — ErrBadSignature.
//   - an arguments hash that differs from the request's —
//     ErrArgsChanged.
//
// A signature that passes every check and names a call delegating to
// a pool child fails with ErrDelegated, unrecorded and unaudited: the
// call completes with its child's answer (ADR 0022 §7).
//
// A refusal is audited: a StepSigned entry names the reason, the call
// and, when the ring holds it, the key — and carries no decision. Two
// bounds keep the log from growing at a stranger's will: a signature
// that fails its MAC and names no pending call leaves nothing, and at
// most 16 refusals are recorded per request.
func (s *Session) DecideSigned(ctx context.Context, sd SignedDecision) (*Turn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.keyring == nil {
		return nil, fmt.Errorf("thread: DecideSigned needs a keyring")
	}
	v := s.verifySignedLocked(ctx, sd)
	if v.err == nil {
		// A verified signature over a delegating call is still no
		// decision (ADR 0022 §7): the call completes with its child.
		// Checked after the signature, so a stranger learns nothing.
		if child := s.approvalWalkLocked().wrappers[sd.CallID]; child != "" && (v.request == nil || v.request.Child == "") {
			v.err = fmt.Errorf("%w: call %q completes with child session %s", ErrDelegated, sd.CallID, child)
		}
	}
	if v.err == nil {
		d := Decision{
			CallID: sd.CallID, Kind: sd.Kind, Reason: sd.Reason, Content: sd.Content,
			Who: sd.Who, Via: viaSigned, Always: sd.Always,
		}
		v.err = s.recordDecisionsLocked(ctx, []recordedDecision{{Decision: d, nonce: sd.Nonce, keyID: sd.KeyID}}, true)
		if v.err == nil {
			return s.armSettledLocked(ctx)
		}
		v.refusal = "" // a storage failure, not a refused signature
	}
	if v.refusal != "" {
		s.auditSignedRefusalLocked(ctx, sd, v)
	}
	if v.swept {
		// The expiry denials are durable whatever became of the
		// signature; when they completed the boundary, AutoResume's
		// contract does not wait for a caller who got an error.
		if _, aerr := s.armSettledLocked(context.WithoutCancel(ctx)); aerr != nil {
			s.agent.Logger().Error("thread: auto-resume arm failed",
				"session", s.header.ID, "err", aerr)
		}
	}
	return nil, v.err
}

// signedVerdict is what verifying a signed decision came to: the
// error (nil when every check passed), the audit's word for a refusal,
// whether the MAC verified — only then are the signature's own claims
// worth recording — the request it named when one is known, and
// whether the expiry sweep denied anything on the way.
type signedVerdict struct {
	err           error
	refusal       string
	authenticated bool
	request       *Request
	swept         bool
}

// verifySignedLocked runs DecideSigned's checks in their documented
// order and records nothing but the expiry sweep's own denials.
// Callers hold s.mu.
func (s *Session) verifySignedLocked(ctx context.Context, sd SignedDecision) signedVerdict {
	var v signedVerdict
	refuse := func(sentinel error, refusal, format string, args ...any) signedVerdict {
		v.err = fmt.Errorf("%w: "+format, append([]any{sentinel}, args...)...)
		v.refusal = refusal
		return v
	}
	// The request the signature names, when the session knows one: the
	// audit's handle, looked up before any check so every refusal can
	// carry it.
	pending := s.pendingLocked()
	for i := range pending {
		if pending[i].CallID == sd.CallID {
			v.request = &pending[i]
			break
		}
	}
	key, known := s.cfg.keyring.keys[sd.KeyID]
	if !known {
		// The MAC is computed all the same, under a key nobody holds:
		// an unknown key id and a wrong MAC cost the same time and
		// read the same.
		key = []byte(challengeDomain)
	}
	if want := challengeMAC(key, sd); !hmac.Equal(want, sd.MAC) || !known {
		return refuse(ErrBadSignature, "bad signature", "call %q", sd.CallID)
	}
	v.authenticated = true
	if !validOutcome(sd.Kind) {
		return refuse(ErrBadSignature, "bad signature", "outcome %q", sd.Kind)
	}
	if sd.Session != s.header.ID {
		return refuse(ErrBadSignature, "bad signature", "challenge names session %q, this is %q", sd.Session, s.header.ID)
	}
	if sd.Nonce == "" {
		// No nonce, no replay guard: a signature without one could be
		// presented again and again.
		return refuse(ErrBadSignature, "bad signature", "call %q: the challenge carries no nonce", sd.CallID)
	}
	// The replay guard runs before the pending check: a replayed
	// signature is a replay whatever became of the call it answered —
	// the boundary may have resolved and moved on, and the answer to a
	// replay is the replay error, not "not pending". Nonces are
	// entries, so the guard survives restarts.
	for _, e := range s.order {
		if d, ok := e.(ApprovalDecisionEntry); ok && d.Nonce != "" && d.Nonce == sd.Nonce {
			return refuse(ErrReplay, "replayed", "nonce already decided call %q", d.CallID)
		}
	}
	// Expiry is the request's own, enforced against the session's
	// clock — never the time the signature states.
	expired, err := s.resolveExpiredLocked(ctx)
	if err != nil {
		v.err = err
		return v
	}
	v.swept = len(expired) > 0
	for i := range expired {
		if r := expired[i]; r.CallID == sd.CallID && r.ID == sd.RequestID && r.RunID == sd.RunID {
			v.request = &expired[i]
			return refuse(ErrExpired, "expired", "call %q: %s", sd.CallID, expiryDetail(r.Expiry))
		}
	}
	// The pending request the signature answers: the occurrence it was
	// minted for, not merely a call with the same id.
	v.request = nil
	pending = s.pendingLocked()
	for i := range pending {
		if pending[i].CallID == sd.CallID {
			v.request = &pending[i]
			break
		}
	}
	req := v.request
	if req == nil {
		return refuse(ErrNotPending, "not pending", "call %q", sd.CallID)
	}
	if req.ID != sd.RequestID || req.RunID != sd.RunID {
		return refuse(ErrNotPending, "not pending",
			"call %q: the signature answers request %q of run %q, the pending one is %q of run %q",
			sd.CallID, sd.RequestID, sd.RunID, req.ID, req.RunID)
	}
	if !s.cfg.keyring.issued(sd.Nonce, *req) {
		return refuse(ErrBadSignature, "bad signature", "call %q: the nonce was not minted for this request", sd.CallID)
	}
	if !sd.Expiry.Equal(req.Expiry) {
		return refuse(ErrBadSignature, "bad signature", "call %q: the signed expiry is not the request's", sd.CallID)
	}
	if req.ArgsSHA256 != sd.ArgsSHA256 {
		return refuse(ErrArgsChanged, "args changed", "call %q", sd.CallID)
	}
	if req.Tool != sd.Tool {
		return refuse(ErrBadSignature, "bad signature", "call %q names tool %q, the request holds %q", sd.CallID, sd.Tool, req.Tool)
	}
	return v
}

// auditSignedRefusalLocked records a refused signed decision as a
// StepSigned audit entry — the reason, the call, the key when the
// ring holds it; no decision. What it writes is bounded: the call and
// run come from the session's own request when it knows one, from the
// signature only when its MAC verified, and a signature that neither
// verifies nor names a pending call leaves nothing; at most
// maxSignedRefusals entries are kept per request. The refusal stands
// whether or not the entry lands: a write failure is logged. Callers
// hold s.mu.
func (s *Session) auditSignedRefusalLocked(ctx context.Context, sd SignedDecision, v signedVerdict) {
	callID, runID := "", ""
	switch {
	case v.request != nil:
		callID, runID = v.request.CallID, v.request.RunID
	case v.authenticated && len(sd.CallID) <= 256 && len(sd.RunID) <= 256:
		callID, runID = sd.CallID, sd.RunID
	default:
		return
	}
	recorded := 0
	for _, e := range s.order {
		if a, ok := e.(ApprovalAuditEntry); ok && a.Step == StepSigned && a.CallID == callID && a.RunID == runID {
			recorded++
		}
	}
	if recorded >= maxSignedRefusals {
		return
	}
	keyID := ""
	if _, ok := s.cfg.keyring.keys[sd.KeyID]; ok {
		keyID = sd.KeyID
	}
	err := s.appendLocked(context.WithoutCancel(ctx), func(id, parent string, created time.Time) Entry {
		return ApprovalAuditEntry{
			ID: id, ParentID: parent, Created: created,
			CallID: callID, Step: StepSigned, Outcome: "refused",
			Detail: v.refusal, RunID: runID, KeyID: keyID,
		}
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		s.agent.Logger().Warn("thread: refused signed decision not audited",
			"session", s.header.ID, "call", callID, "err", err)
	}
}
