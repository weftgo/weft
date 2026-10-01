package thread

import (
	"context"
	"fmt"
	"slices"
	"time"
)

// Plumbing for thread/pool (ADR 0022).
//
// Everything in this file exists for one caller: thread/pool, which
// lives in another package and therefore needs exported names to
// write its ledger and bridge nested approvals. None of it is a
// second way to do what the rest of the Session API does — an
// application delegates with pool.Wrap and pool.Submit, decides with
// Decide, DecideSigned and pool.Decide, and never calls these. They
// are safe for concurrent use like every Session method, and they
// keep the session's own invariants (ids vetted, appends atomic,
// expiry resolved first); what they do not keep is the policy the
// user-facing doors enforce — ReplayDecisions and ResolveDelegation
// record without a signature on a RequireSigned session, because the
// decision they carry was verified where it was taken. Go has no
// package-private export, so the boundary is this paragraph.

// AppendPoolReceipt appends one pool receipt entry (ADR 0022 §4) and
// returns it as stored, its minted ID the receipt handle a later entry
// links back to with Receipt. The pool calls this for every state its
// delegations pass through — acceptance, each start, each park, the
// settlement — under the rule the entry kind's contract states: pool
// receipts are ledger, never model context, and a child's answer
// reaches the model only through its delegating call's result or the
// application. Session.Usage sums the Usage of settlement entries
// into its Delegated bucket, one per entry: writing exactly one
// settlement per receipt is the caller's contract.
func (s *Session) AppendPoolReceipt(ctx context.Context, e PoolReceiptEntry) (PoolReceiptEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out PoolReceiptEntry
	err := s.appendLocked(ctx, func(id, parent string, created time.Time) Entry {
		e.ID, e.ParentID, e.Created = id, parent, created
		out = e
		return e
	})
	if err != nil {
		return PoolReceiptEntry{}, err
	}
	return out, nil
}

// AppendApprovalRequests appends mirrored approval requests in one
// atomic batch (ADR 0022 §7): the pool writes a child session's parked
// calls onto the parent's tree — Child naming the session they park
// in, Wrapper the delegating call they park under — so the parent's
// Pending surfaces them and a decision records like any other. Every
// request must name its Child: an entry without one would be an
// ordinary request with no parked call behind it. The entries' tree
// fields are minted here, each id vetted like every other the session
// mints (valid, and new to the tree and to the batch); the stored
// entries return. Mirrors are ledger until decided: they never join
// the model's context, and their resolution is the pool's to route. A
// mirror stops being pending when it is decided — by an approver, by
// its expiry, or by DenyMirrored when its delegation ends — or when a
// later mirror reuses its call id.
func (s *Session) AppendApprovalRequests(ctx context.Context, reqs ...ApprovalRequestEntry) ([]ApprovalRequestEntry, error) {
	if len(reqs) == 0 {
		return nil, fmt.Errorf("thread: AppendApprovalRequests with no requests")
	}
	for _, r := range reqs {
		if r.Child == "" {
			return nil, fmt.Errorf("thread: AppendApprovalRequests: request for call %q names no child session", r.CallID)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(reqs))
	parent := s.leaf
	now := s.now()
	out := make([]ApprovalRequestEntry, 0, len(reqs))
	for _, r := range reqs {
		id, err := s.mintCheckedLocked(entries)
		if err != nil {
			return nil, err
		}
		r.ID, r.ParentID, r.Created = id, parent, now
		r.Args = slices.Clone(r.Args)
		entries = append(entries, r)
		out = append(out, r)
		parent = id
	}
	if err := s.appendEntriesLocked(ctx, entries...); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Args = slices.Clone(out[i].Args) // the caller's copy, not the tree's
	}
	return out, nil
}

// MirroredRequest is one mirrored child request as the parent session
// holds it (ADR 0022 §7): the request, the delegating call it parks
// under, and what this session has decided about it.
type MirroredRequest struct {
	// Request is the mirror: CallID in the namespaced form decisions
	// address, Child the session the call is parked in, RunID the
	// child run that parked it — the occurrence's name.
	Request Request
	// Wrapper is the parent-side delegating call; empty for an async
	// delegation.
	Wrapper string
	// Decisions are the decisions in force for this occurrence, in
	// the order they were recorded.
	Decisions []ApprovalDecisionEntry
	// Decided reports whether those decisions reach an effective
	// verdict under this session's Quorum — the moment the pool
	// replays them into the child.
	Decided bool
}

// MirroredRequests returns the mirrored child requests in force on
// this session, decided or not, in append order (ADR 0022 §7) — the
// pool's read of what to replay. Expiry is resolved first, as on
// every path that looks at the boundary: a mirror strictly past its
// expiry is denied on the spot (Via "expiry") and returns Decided,
// which is how a lapsed nested request reaches its child. A mirror
// superseded by a later one for the same call id is not in force and
// not returned.
func (s *Session) MirroredRequests(ctx context.Context) ([]MirroredRequest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	expired, err := s.resolveExpiredLocked(ctx)
	if err != nil {
		return nil, err
	}
	if len(expired) > 0 {
		// The denials are durable; when they completed this session's
		// own boundary, AutoResume's contract holds here as in Decide.
		if _, aerr := s.armSettledLocked(context.WithoutCancel(ctx)); aerr != nil {
			s.agent.Logger().Error("thread: auto-resume arm failed",
				"session", s.header.ID, "err", aerr)
		}
	}
	walk := s.approvalWalkLocked()
	var out []MirroredRequest
	for _, re := range s.liveMirrorsLocked() {
		ds := scopedDecisions(walk.decisions[re.CallID], re.RunID)
		_, decided := effectiveDecision(ds, s.cfg.quorum)
		out = append(out, MirroredRequest{
			Request:   requestFromEntry(s.header.ID, re),
			Wrapper:   re.Wrapper,
			Decisions: slices.Clone(ds),
			Decided:   decided,
		})
	}
	return out, nil
}

// ResolveDelegation records the outcome of a pool delegation as the
// resolution of the parent-side call that delegated it (ADR 0022 §7):
// content becomes the call's result, marked as an error when isError,
// recorded with Who "thread/pool" and Via "child", and the boundary
// resumes when that completes it, like Decide. It is the one way a
// delegating call is resolved — Decide refuses such a call with
// ErrDelegated. callID must be the delegating call of a mirrored
// request of child: the current occurrence of the call, named as
// Wrapper by a request entry whose Child is child. Anything else — a
// call that delegates to nobody, or to another child, as a call id
// reused by a later turn does — fails with ErrNotPending. A
// delegation's answer is not an approval, so the call records under
// RequireSigned too (the approvals that let the child run were signed
// where they were decided), and it is not subject to a request
// expiry: the child ran, and its answer is the result.
func (s *Session) ResolveDelegation(ctx context.Context, callID, child, content string, isError bool) (*Turn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if got := s.approvalWalkLocked().wrappers[callID]; got == "" || got != child {
		return nil, fmt.Errorf("%w: call %q does not delegate to child session %s", ErrNotPending, callID, child)
	}
	d := Resolve(callID, content)
	if isError {
		d = ResolveError(callID, content)
	}
	d.Who, d.Via = "thread/pool", viaChild
	if err := s.recordDecisionsLocked(ctx, []recordedDecision{{Decision: d}}, true); err != nil {
		return nil, err
	}
	return s.armSettledLocked(ctx)
}

// DenyMirrored denies every still-pending mirrored request of child
// with reason (ADR 0022 §7): what the pool records when a delegation
// ends with requests nobody decided — a canceled child, a child that
// failed or is gone. Nothing can resume the child through this
// session any more, so its requests must stop being offered, and the
// record says why: a deny with Who "thread/pool" and Via "child". The
// denials are never replayed anywhere. When they complete this
// session's own boundary its resume is armed, as after Decide. A
// child with nothing pending records nothing.
func (s *Session) DenyMirrored(ctx context.Context, child, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var batch []recordedDecision
	for _, r := range s.pendingLocked() {
		if r.Child == "" || r.Child != child {
			continue
		}
		d := Deny(r.CallID, reason)
		d.Who, d.Via = "thread/pool", viaChild
		batch = append(batch, recordedDecision{Decision: d})
	}
	if len(batch) == 0 {
		return nil
	}
	if err := s.recordDecisionsLocked(ctx, batch, true); err != nil {
		return err
	}
	_, err := s.armSettledLocked(ctx)
	return err
}

// ReplayDecisions records, in a pool child, the decisions its parent
// session took over the child's parked calls, and resumes the child
// when they complete its boundary (ADR 0022 §7). It is the child's
// only door for them: not Decide — the batch rules of the user's door
// do not apply, several decisions for one call (a parent's Quorum)
// record in the order given — and not DecideSigned, though it records
// on a RequireSigned child, because each decision was verified in the
// parent, where it was taken.
//
// Each entry is a decision the parent recorded, its CallID rewritten
// to the child's own call id. The child's record keeps the decider's
// identity — Who, and KeyID, so a signed approver counts by key under
// the child's Quorum as it did under the parent's — and names the
// channel honestly: Via "parent", never "signed", for the signature
// itself lives in the parent's entry, not here.
//
// Replay is idempotent by construction. The child's own expiry is
// resolved first; then every decision for a call that is no longer
// pending — resolved by an earlier replay, by the child's expiry, by
// a resume already run — is dropped rather than refused, so a replay
// repeated after a crash, or one carrying the decisions of an earlier
// park, records nothing twice. With nothing left to record the call
// still arms a resume that a completed boundary is owed.
//
// The returned Turn is the resume run, started under ctx; nil when
// the boundary is not complete. A session with no pool lineage fails:
// replay is not a way around Decide.
func (s *Session) ReplayDecisions(ctx context.Context, ds ...ApprovalDecisionEntry) (*Turn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.header.Lineage == nil {
		return nil, fmt.Errorf("thread: ReplayDecisions on session %s, which is not a pool child", s.header.ID)
	}
	if _, err := s.resolveExpiredLocked(ctx); err != nil {
		return nil, err
	}
	pending := map[string]bool{}
	for _, r := range s.pendingLocked() {
		pending[r.CallID] = true
	}
	var batch []recordedDecision
	for _, e := range ds {
		if !pending[e.CallID] {
			continue
		}
		batch = append(batch, recordedDecision{
			Decision: Decision{
				CallID: e.CallID, Kind: e.Outcome, Reason: e.Reason, Content: e.Content,
				Who: e.Who, Via: viaParent, Always: e.Always,
			},
			keyID: e.KeyID,
		})
	}
	if len(batch) > 0 {
		if err := s.recordDecisionsLocked(ctx, batch, false); err != nil {
			return nil, err
		}
	}
	return s.armSettledLocked(ctx)
}

// CancelDelegated denies every pending request of a pool child with
// reason and resumes nothing (ADR 0022 §6): the pool's Cancel of a
// child parked at an approval. The denials are recorded with Who
// "thread/pool" and Via "parent", so the parked calls can never run
// on a later approval; the child's run is not started — a canceled
// delegation does no more work — and whoever opens the session later
// finds a decided boundary whose resume shows the model the denial.
// A session with no pool lineage fails; one with nothing pending
// records nothing.
func (s *Session) CancelDelegated(ctx context.Context, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.header.Lineage == nil {
		return fmt.Errorf("thread: CancelDelegated on session %s, which is not a pool child", s.header.ID)
	}
	if _, err := s.resolveExpiredLocked(ctx); err != nil {
		return err
	}
	pending := s.pendingLocked()
	if len(pending) == 0 {
		return nil
	}
	batch := make([]recordedDecision, 0, len(pending))
	for _, r := range pending {
		d := Deny(r.CallID, reason)
		d.Who, d.Via = "thread/pool", viaParent
		batch = append(batch, recordedDecision{Decision: d})
	}
	return s.recordDecisionsLocked(ctx, batch, true)
}

// inheritApprovalsOption carries a parent session's approval policy
// into a pool child's configuration.
type inheritApprovalsOption struct{ parent *Session }

func (o inheritApprovalsOption) applySession(c *sessionConfig) {
	pc := &o.parent.cfg // fixed at construction: read without the parent's lock
	c.requestExpiry = pc.requestExpiry
	c.quorum = pc.quorum
	c.requireSigned = c.requireSigned || pc.requireSigned
	if c.keyring == nil {
		c.keyring = pc.keyring
	}
	if c.clock == nil {
		c.clock = pc.clock
	}
}

// InheritApprovals returns the SessionOption that gives a pool child
// the parts of its parent's approval policy that nesting needs (ADR
// 0022 §7): the parent's RequestExpiry, so a nested request lapses
// like one of the parent's own; its Quorum, so the replayed approvals
// count in the child as they did in the parent; its keyring and its
// RequireSigned rule, so nobody holding the child session can decide
// its parked calls through the unsigned door the parent closed; and
// its Clock, the time those expiries are read against. The pool
// passes it when it creates a child and again when it reopens one —
// only RequireSigned is stored in the header; the rest is
// configuration, as on any session. Options listed after it override
// what it set; a nil parent is ignored.
func InheritApprovals(parent *Session) SessionOption {
	if parent == nil {
		return nil
	}
	return inheritApprovalsOption{parent}
}
