package thread

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/weftgo/weft"
)

// Outcome is a decision's kind (ADR 0021 §1): approve (run the call
// through the ordinary chain), deny with a reason, or resolve with
// content computed outside the process, resolve_error marking it an
// error. The wire values are the decision entry's "outcome" field;
// they never change without a format version.
type Outcome string

const (
	OutcomeApprove      Outcome = "approve"
	OutcomeDeny         Outcome = "deny"
	OutcomeResolve      Outcome = "resolve"
	OutcomeResolveError Outcome = "resolve_error"
)

// The audit entry's steps (ADR 0021 §2): every step the decision chain
// takes leaves an approval_audit entry naming its step. Grants join in
// step 2.2.
const (
	StepGrant    = "grant"    // step 2.2: a matching live grant
	StepApprover = "approver" // the live chain step, consulted and bounded
	StepPark     = "park"     // the request persisted, the turn ended pending
	StepExpiry   = "expiry"   // an expired request denied on resume
	StepResume   = "resume"   // the parked boundary resumed under its decisions
)

// Request is a parked call awaiting a decision (ADR 0021 §1): what the
// model asked for, hashed and named so a decision can state exactly
// what it decided. Session.Pending returns the undecided ones; they
// survive restarts because they are entries.
type Request struct {
	// Session is the session the request lives in.
	Session string
	// CallID is the pending call's id — the key decisions address and
	// the core's Approve/Deny/Resolve key on.
	CallID string
	// Tool is the call's tool name.
	Tool string
	// Args is the call's arguments, verbatim as the model wrote them.
	Args json.RawMessage
	// ArgsSHA256 is the hex SHA-256 of Args — a decision names what it
	// decided by this hash, and step 2.2's signed decisions carry it in
	// the challenge.
	ArgsSHA256 string
	// RunID is the run that parked the call.
	RunID string
	// Reason is why the call parked: "tool requires approval" for a
	// RequireApproval tool, "middleware required approval" when the
	// tool chain parked it.
	Reason string
	// Expiry is when the request lapses — zero means never. A request
	// is expired strictly after this time, never at it; the next resume
	// denies it with the stated reason (ADR 0021 §5).
	Expiry time.Time
	// Created is when the request was persisted.
	Created time.Time
	// Nonce and KeyID are the challenge a signed decision answers (ADR
	// 0021 §3): set by Session.Request — the signing side's handle on
	// exactly what it vouched for — and empty on the Pending view,
	// which mints no challenge.
	Nonce string
	KeyID string
}

// Decision is one call's outcome, the value Decide records (ADR 0021
// §1). Build one with Approve, Deny, Resolve or ResolveError; Who and
// Via are the caller's to fill — Who names the deciding identity for
// the audit trail, Via the channel ("cli", "webhook", …); Via is
// forced to "approver" and "expiry" on the paths that own it.
type Decision struct {
	// CallID is the pending call the decision addresses.
	CallID string
	// Kind is the outcome: approve, deny, resolve or resolve_error.
	Kind Outcome
	// Reason is the deny reason — the text the model sees after
	// "DENIED: ".
	Reason string
	// Content is the resolve payload, verbatim; resolve_error marks it
	// an error.
	Content string
	// Who decided, for the audit trail — and the quorum's identity:
	// distinct Who values are distinct approvers (ADR 0021 §5).
	Who string
	// Via which channel the decision arrived.
	Via string
	// Always approves and grants the same thing for the future: the
	// tool plus the call's exact arguments become a session grant
	// ("approve and always allow this", ADR 0021 §4), recorded with
	// the decision in one append. Build it with ApproveAlways.
	Always bool
}

// Approve returns a Decision that resumes the call: the run executes
// it through the ordinary tool chain with Call.Approved set (ADR
// 0007).
func Approve(callID string) Decision {
	return Decision{CallID: callID, Kind: OutcomeApprove}
}

// ApproveAlways returns a Decision that approves the call and grants
// the same thing for the future (ADR 0021 §4's "approve and always
// allow this command"): the tool plus the call's exact arguments
// become a session grant, recorded with the decision in one append —
// so the next such call never parks. A richer grant (a command glob,
// a path prefix) is s.Grant's to make.
func ApproveAlways(callID string) Decision {
	return Decision{CallID: callID, Kind: OutcomeApprove, Always: true}
}

// Deny returns a Decision that resolves the call without running it:
// the model sees "DENIED: <reason>" and the loop continues.
func Deny(callID, reason string) Decision {
	return Decision{CallID: callID, Kind: OutcomeDeny, Reason: reason}
}

// Resolve returns a Decision that resumes the call with a result
// computed outside the process — the human-as-tool-executor shape. The
// handler never runs; the content becomes the call's result verbatim.
func Resolve(callID, content string) Decision {
	return Decision{CallID: callID, Kind: OutcomeResolve, Content: content}
}

// ResolveError is Resolve with the result marked as an error.
func ResolveError(callID, content string) Decision {
	return Decision{CallID: callID, Kind: OutcomeResolveError, Content: content}
}

// Approver is the decision chain's live step (ADR 0021 §2): the "ask
// now" path for a terminal or an already-connected UI. It returns the
// Decision and true when it decided, or false to decline (a pipe, no
// TTY). It is never given unbounded time: the session consults it
// under ApproverTimeout, and a timeout reads as a decline. It must not
// call back into the same Session — the chain consults it outside the
// session lock, but the turn is still running.
type Approver func(ctx context.Context, r Request) (Decision, bool)

// Request builder options below (SessionOptions).

type approverOption struct{ a Approver }

func (o approverOption) applySession(c *sessionConfig) {
	if o.a != nil {
		c.approver = o.a
	}
}

// WithApprover sets the decision chain's live step (ADR 0021 §2). The
// Approver is consulted for a call about to park, after grants and
// before parking; it is armed only when ApproverTimeout is positive —
// the default of no timeout means no waiting, so a headless session
// never blocks on it (the ADR's "default 0 = no waiting when not
// interactive"). A nil approver is ignored.
func WithApprover(a Approver) SessionOption { return approverOption{a} }

type approverTimeoutOption time.Duration

func (o approverTimeoutOption) applySession(c *sessionConfig) { c.approverTimeout = time.Duration(o) }

// ApproverTimeout bounds the chain's live consultation: the Approver
// runs under a context with this deadline, and a timeout or panic
// reads as a decline, audited, before the call parks. Values <= 0
// leave the Approver unconsulted — set one to arm WithApprover.
func ApproverTimeout(d time.Duration) SessionOption { return approverTimeoutOption(d) }

type autoResumeOption bool

func (o autoResumeOption) applySession(c *sessionConfig) { c.autoResume = bool(o) }

// AutoResume sets whether the session resumes on its own once every
// pending call of the parked boundary has a decision (ADR 0021 §1).
// The default is on; AutoResume(false) parks the decided boundary
// until the caller calls Resume. Either way a plain Send never runs
// while a boundary is open — only the resume run resolves it.
func AutoResume(on bool) SessionOption { return autoResumeOption(on) }

type onRequestOption func(Request)

func (o onRequestOption) applySession(c *sessionConfig) {
	if o != nil {
		c.onRequest = o
	}
}

// OnRequest sets the notification fired when a request parks (ADR 0021
// §5): called after the request entry is durable, once per parked
// call, so an application can push it anywhere. The session does no
// I/O of its own for this; a panic in fn is contained and logged, never
// failing the turn. A nil fn is ignored.
func OnRequest(fn func(Request)) SessionOption { return onRequestOption(fn) }

type requestExpiryOption time.Duration

func (o requestExpiryOption) applySession(c *sessionConfig) { c.requestExpiry = time.Duration(o) }

type quorumOption int

func (o quorumOption) applySession(c *sessionConfig) { c.quorum = int(o) }

// Quorum sets how many decisions from distinct approver identities a
// call needs before it resolves (ADR 0021 §5): n approvals with
// different Who values. Values below 2 read as the default — one
// decision resolves. Conflicting decisions resolve to deny, with the
// pinned reason.
func Quorum(n int) SessionOption { return quorumOption(n) }

// RequestExpiry gives every request the session parks a lifetime: a
// request strictly past Created plus d is denied with the stated
// reason on the next resume (ADR 0021 §5). Values <= 0 (the default)
// mean requests never expire.
func RequestExpiry(d time.Duration) SessionOption { return requestExpiryOption(d) }

// Pending returns the session's undecided requests, in call order:
// the calls the leaf's path leaves dangling that have an approval
// request and no decision yet. It works after a restart — requests and
// decisions are entries — and it never shows a call whose boundary a
// resume already resolved.
func (s *Session) Pending() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pendingLocked()
}

// Decide records decisions over the session's pending calls, durably,
// and resumes the boundary when they complete it (ADR 0021 §1).
//
// Every decision must address a currently pending call: one that does
// not fails with ErrNotPending and nothing is recorded — the error is
// raised before any entry lands and before any run starts, the core's
// rule made strict. When, after recording, every pending call of the
// boundary has a decision and AutoResume is on (the default), the
// resume run starts and Decide returns its Turn; otherwise the return
// is (nil, nil) and the caller drives Resume. The undecided calls a
// Resume-driven run carries are denied with the core's "no decision"
// text; expired ones were denied on the spot with the expiry reason.
func (s *Session) Decide(ctx context.Context, ds ...Decision) (*Turn, error) {
	if len(ds) == 0 {
		return nil, fmt.Errorf("thread: Decide with no decisions")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.requireSigned {
		return nil, fmt.Errorf("%w: session %s", ErrSignatureRequired, s.header.ID)
	}
	pending := s.pendingLocked()
	pendingIDs := make(map[string]string, len(pending))     // call id → parked run id
	pendingByCall := make(map[string]Request, len(pending)) // call id → the request
	for _, r := range pending {
		pendingIDs[r.CallID] = r.RunID
		pendingByCall[r.CallID] = r
	}
	for _, d := range ds {
		if !validOutcome(d.Kind) {
			return nil, fmt.Errorf("thread: decision for call %q has no outcome", d.CallID)
		}
		if _, ok := pendingIDs[d.CallID]; !ok {
			return nil, fmt.Errorf("%w: call %q", ErrNotPending, d.CallID)
		}
	}
	// One atomic Append carries every decision — and every grant an
	// Always decision makes: a partial recording of a Decide call
	// cannot happen (the storage's all-or-nothing rule).
	var entries []Entry
	parent := s.leaf
	for _, d := range ds {
		e := ApprovalDecisionEntry{
			CallID:  d.CallID,
			Outcome: d.Kind,
			Reason:  d.Reason,
			Content: d.Content,
			Who:     d.Who,
			Via:     d.Via,
			RunID:   pendingIDs[d.CallID],
		}
		if e.Via == "" {
			e.Via = "user"
		}
		e.ID, e.ParentID, e.Created = s.mintIDLocked(), parent, time.Now().UTC()
		entries = append(entries, e)
		parent = e.ID
		if d.Always && d.Kind == OutcomeApprove {
			// Only an approval grants: a Deny or Resolve built with
			// Always set must not mint a standing approval — the same
			// gate the Approver path applies (ADR 0021 §4's "approve
			// and always allow" names an approve).
			req := pendingByCall[d.CallID]
			g := GrantEntry{
				ID: s.mintIDLocked(), ParentID: parent, Created: time.Now().UTC(),
				Grant: Grant{
					Tool: req.Tool,
					Args: []Arg{ArgEquals("", slices.Clone(req.Args))},
				},
			}
			entries = append(entries, g)
			parent = g.ID
		}
	}
	if err := s.st.Append(ctx, s.header.ID, entries...); err != nil {
		return nil, err
	}
	for _, e := range entries {
		s.adoptLocked(e)
	}
	if err := s.flushLocked(ctx); err != nil {
		return nil, fmt.Errorf("thread: decision flush: %w", err)
	}
	if s.cfg.autoResume && s.boundaryLocked() && len(s.pendingLocked()) == 0 {
		// The live context, by design: a decider whose context dies
		// after the call reads "canceled before it started" and retries
		// with a live one — the pinned TestResumeRetryAfterCanceledArm
		// contract. The entries are already durable; nothing is lost.
		return s.armResumeLocked(ctx)
	}
	return nil, nil
}

// Resume starts the boundary's resume run now (ADR 0021 §1): the
// decided calls resolve under their decisions — approved calls run
// with Call.Approved set, denied ones show their reason, resolved ones
// their content — and every undecided call is denied with the core's
// "no decision" text. Requests strictly past their expiry were denied
// on the spot, with the stated reason, before the run starts. The
// parked boundary must still be open: Resume with nothing to resume
// fails with ErrNotPending. The returned Turn is the resume run's
// receipt and handle, like any Send's.
func (s *Session) Resume(ctx context.Context) (*Turn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.boundaryLocked() {
		return nil, fmt.Errorf("%w: no parked approvals to resume", ErrNotPending)
	}
	if expired := s.expiredPendingLocked(time.Now().UTC()); len(expired) > 0 {
		var entries []Entry
		parent := s.leaf
		for _, r := range expired {
			audit := ApprovalAuditEntry{
				CallID: r.CallID, Step: StepExpiry, Outcome: "denied",
				Detail: expiryDetail(r.Expiry), RunID: r.RunID,
			}
			audit.ID, audit.ParentID, audit.Created = s.mintIDLocked(), parent, time.Now().UTC()
			entries = append(entries, audit)
			parent = audit.ID
			d := ApprovalDecisionEntry{
				CallID: r.CallID, Outcome: OutcomeDeny,
				Reason: expiryReason(r.Expiry), Via: "expiry", RunID: r.RunID,
			}
			d.ID, d.ParentID, d.Created = s.mintIDLocked(), parent, time.Now().UTC()
			entries = append(entries, d)
			parent = d.ID
		}
		if err := s.st.Append(ctx, s.header.ID, entries...); err != nil {
			return nil, err
		}
		for _, e := range entries {
			s.adoptLocked(e)
		}
		if err := s.flushLocked(ctx); err != nil {
			return nil, fmt.Errorf("thread: expiry flush: %w", err)
		}
	}
	return s.armResumeLocked(ctx)
}

// armResumeLocked hands the resume run to the runner: the Turn is
// minted here so Decide and Resume can return it, and the runner —
// started if no runner is alive — picks the work up. Arming is
// idempotent while the armed resume has not landed: a boundary resumes
// once, so a second Resume before the first lands returns the turn
// already armed — overwriting it would orphan the first caller's Turn
// on a wait that never ends. Callers hold s.mu; the boundary is
// decided (or Resume is forcing it).
func (s *Session) armResumeLocked(ctx context.Context) (*Turn, error) {
	if s.await.resumed != nil {
		return s.await.resumed, nil // one boundary resumes once
	}
	t := s.mintResumeLocked()
	if s.running {
		s.resumeWork = &pendingResume{ctx: ctx, turn: t}
		return t, nil
	}
	s.running = true
	s.inFlight = t
	go s.execute(workItem{ps: pendingSend{ctx: ctx, turn: t}, resume: true})
	return t, nil
}

// mintResumeLocked mints a resume turn for the open boundary and
// registers it as the boundary's one armed resume — the idempotency
// key Decide, Resume and the runner's own arming paths (the
// chain-decided hand-off, the settled-boundary pickup) all share, so
// two paths can never arm two resumes for one boundary. A resume
// already armed is returned as-is, never displaced. Callers hold s.mu.
func (s *Session) mintResumeLocked() *Turn {
	if s.await.resumed != nil {
		return s.await.resumed
	}
	t := s.newTurnLocked()
	t.resume = true
	s.await.resumed = t
	if s.await.turn != nil { // nil after a restart: the parked Turn object is gone
		s.await.turn.setNext(t)
	}
	return t
}

// settleResumeLocked retires a resume turn's arming and drops the
// boundary's captured settings when the resume resolved it. Called
// under s.mu on every path that decides a resume turn — the run
// completed, the context died first, the persistence failed, the turn
// panicked — always before the turn is decided, so a caller whose Wait
// returns faces a settled boundary: a retry after a failure arms
// fresh, a resolution reads ErrNotPending. Never a stale dead Turn on
// a wait that cannot end.
func (s *Session) settleResumeLocked(t *Turn) {
	if s.await.resumed == t {
		s.await.resumed = nil
	}
	if !s.boundaryLocked() {
		s.await = awaitState{}
	}
}

// validOutcome reports whether o is one of the four decision outcomes.
func validOutcome(o Outcome) bool {
	switch o {
	case OutcomeApprove, OutcomeDeny, OutcomeResolve, OutcomeResolveError:
		return true
	}
	return false
}

// expiryReason is the model-visible denial text for an expired request
// (ADR 0021 §5) — pinned bytes, asserted inline by the approval tests:
// the reason names the deadline so the model can tell a lapsed request
// from a refused one.
func expiryReason(expiry time.Time) string {
	return "expired: no decision before " + expiry.UTC().Format(time.RFC3339)
}

// expiryDetail is the audit entry's Detail for the same event.
func expiryDetail(expiry time.Time) string {
	return "request expired at " + expiry.UTC().Format(time.RFC3339)
}

// hashArgs returns the hex SHA-256 of a call's argument bytes — nil
// args hash as the empty string's hash, the bytes being the bytes.
func hashArgs(args []byte) string {
	sum := sha256.Sum256(args)
	return hex.EncodeToString(sum[:])
}

// chainResult is what the decision chain produced for one parked turn:
// the entries to append with it (callers fill their ids at append
// time), the calls that parked (for OnRequest), and the turn settings
// the resume run inherits.
type chainResult struct {
	entries     []Entry
	parkedCalls []weft.ToolCallPart
	opts        []weft.RunOption // the parked send's captured run options
	awaitCtx    context.Context  // the persistence window the resume inherits
	runID       string           // the run that parked
}

// runChain is the decision chain (ADR 0021 §2): for each call a turn
// left pending, grants (ADR 0021 §4), then a bounded Approver, then
// the park. It runs at the turn's end, before the
// request is persisted — "before a request parks" is before the
// durable parked state exists, because the core's run boundary has
// already ended the run — and every step leaves an audit entry,
// including automatic approvals. It holds no lock: the Approver is a
// caller's function and may take its own time, bounded by
// ApproverTimeout.
func (s *Session) runChain(ctx context.Context, t *Turn, opts []weft.RunOption, calls []weft.ToolCallPart) *chainResult {
	cr := &chainResult{opts: opts, awaitCtx: ctx, runID: t.runID}
	var expiry time.Time
	if s.cfg.requestExpiry > 0 {
		expiry = time.Now().UTC().Add(s.cfg.requestExpiry)
	}
	for _, c := range calls {
		if d, ref, ok := s.matchGrant(ctx, c); ok {
			// The chain's first step decides at once, audited — the
			// audit's Detail names the grant (namespaced "shared grant"
			// for a store's, so the session's use counting cannot
			// cross-count an id collision), and the session counts its
			// own grants' uses from exactly these entries (ADR 0021
			// §2, §4).
			d.CallID = c.ID
			outcome := "approved"
			if d.Kind == OutcomeDeny {
				outcome = "denied"
			}
			detail := "grant " + ref.id
			if ref.shared {
				detail = "shared grant " + ref.id
			}
			cr.entries = append(cr.entries,
				ApprovalAuditEntry{
					CallID: c.ID, Step: StepGrant, Outcome: outcome,
					Detail: detail, RunID: t.runID,
				},
				decisionFrom(c.ID, d, t.runID),
			)
			continue
		}
		if s.cfg.approver != nil && s.cfg.approverTimeout > 0 {
			req := Request{
				Session: s.header.ID, CallID: c.ID, Tool: c.Name,
				Args: slices.Clone(c.Args), ArgsSHA256: hashArgs(c.Args),
				RunID: t.runID, Reason: parkingReason(s, c.Name), Expiry: expiry,
			}
			d, decided, outcome := s.consultApprover(ctx, req)
			cr.entries = append(cr.entries, ApprovalAuditEntry{
				CallID: c.ID, Step: StepApprover, Outcome: outcome, RunID: t.runID,
			})
			if decided {
				if d.Via == "" {
					d.Via = "approver"
				}
				cr.entries = append(cr.entries, decisionFrom(c.ID, d, t.runID))
				if d.Always && d.Kind == OutcomeApprove {
					// "Approve and always allow" from the live step
					// grants like the same decision through Decide or
					// DecideSigned (ADR 0021 §4): the tool plus the
					// call's exact arguments, recorded with the
					// decision in the same append.
					cr.entries = append(cr.entries, GrantEntry{
						Grant: Grant{
							Tool: c.Name,
							Args: []Arg{ArgEquals("", slices.Clone(c.Args))},
						},
					})
				}
				continue
			}
		}
		args := slices.Clone(c.Args)
		cr.entries = append(cr.entries,
			ApprovalRequestEntry{
				CallID: c.ID, Tool: c.Name, Args: args, ArgsSHA256: hashArgs(args),
				RunID: t.runID, Reason: parkingReason(s, c.Name), Expiry: expiry,
			},
			ApprovalAuditEntry{CallID: c.ID, Step: StepPark, Outcome: "parked", RunID: t.runID},
		)
		cr.parkedCalls = append(cr.parkedCalls, c)
	}
	return cr
}

// decisionFrom builds the decision entry a chain step records.
func decisionFrom(callID string, d Decision, runID string) ApprovalDecisionEntry {
	return ApprovalDecisionEntry{
		CallID: callID, Outcome: d.Kind, Reason: d.Reason, Content: d.Content,
		Who: d.Who, Via: d.Via, RunID: runID,
	}
}

// consultApprover runs the chain's live step under its timeout: the
// answer, whether it decided, and the audit outcome ("approved",
// "denied", "resolved", "declined", "timeout", or "panic"). The
// consultation never blocks the turn beyond ApproverTimeout — an
// approver that ignores its context is abandoned where it stands, the
// buffered channel catching its late answer, and the call parks.
func (s *Session) consultApprover(ctx context.Context, r Request) (Decision, bool, string) {
	actx, cancel := context.WithTimeout(ctx, s.cfg.approverTimeout)
	defer cancel()
	type answer struct {
		d        Decision
		ok       bool
		panicked bool
	}
	ch := make(chan answer, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				s.agent.Logger().Error("thread: approver panicked",
					"session", s.header.ID, "call", r.CallID, "panic", p)
				ch <- answer{panicked: true}
			}
		}()
		d, ok := s.cfg.approver(actx, r)
		d.CallID = r.CallID // the request names the call, whatever the approver copied
		ch <- answer{d: d, ok: ok}
	}()
	select {
	case a := <-ch:
		switch {
		case a.panicked:
			return Decision{}, false, "panic"
		case !a.ok:
			return Decision{}, false, "declined"
		}
		switch a.d.Kind {
		case OutcomeApprove:
			return a.d, true, "approved"
		case OutcomeDeny:
			return a.d, true, "denied"
		case OutcomeResolve, OutcomeResolveError:
			return a.d, true, "resolved"
		}
		return Decision{}, false, "declined" // an outcome-less decision is no decision
	case <-actx.Done():
		return Decision{}, false, "timeout"
	}
}

// parkingReason is why a call parked, for the request entry: the
// tool's own RequireApproval mark when it has one, the tool chain's
// deferral otherwise, and the unknown-tool text a tool source that
// changed shape leaves.
func parkingReason(s *Session, tool string) string {
	for _, def := range s.agent.Tools() {
		if def.Name == tool {
			if def.RequiresApproval() {
				return "tool requires approval"
			}
			return "middleware required approval"
		}
	}
	return "unknown tool"
}

// fireOnRequest delivers the chain's parked requests to the OnRequest
// callback, after the append that made them durable (ADR 0021 §5).
// Panics are contained — a push notification must not fail a turn
// that already landed.
func (s *Session) fireOnRequest(cr *chainResult) {
	if s.cfg.onRequest == nil || len(cr.parkedCalls) == 0 {
		return
	}
	want := make(map[string]bool, len(cr.parkedCalls))
	for _, c := range cr.parkedCalls {
		want[c.ID] = true
	}
	s.mu.Lock()
	var reqs []Request
	for i := len(s.order) - 1; i >= 0 && len(reqs) < len(cr.parkedCalls); i-- {
		if re, ok := s.order[i].(ApprovalRequestEntry); ok && want[re.CallID] {
			reqs = append(reqs, requestFromEntry(s.header.ID, re))
		}
	}
	fn := s.cfg.onRequest
	s.mu.Unlock()
	slices.Reverse(reqs) // call order, like everything the chain reports
	for _, r := range reqs {
		func() {
			defer func() {
				if p := recover(); p != nil {
					s.agent.Logger().Error("thread: OnRequest panicked",
						"session", s.header.ID, "call", r.CallID, "panic", p)
				}
			}()
			fn(r)
		}()
	}
}

// Audit returns the session's approval trail (ADR 0021 §5): every
// request, chain step, decision, grant and revocation entry, in
// append order, each carrying its entry id and time — the whole file,
// not only the leaf's path, because the trail is what happened, not
// what the leaf remembers. An expiry denial reads as its audit entry
// plus the decision with Via "expiry"; a grant match as the audit
// entry naming the grant.
func (s *Session) Audit() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Entry
	for _, e := range s.order {
		switch e.(type) {
		case ApprovalRequestEntry, ApprovalDecisionEntry, ApprovalAuditEntry,
			GrantEntry, GrantRevokedEntry:
			out = append(out, cloneEntry(e))
		}
	}
	return out
}

// requestFromEntry lifts a stored request entry to its Request value.
func requestFromEntry(session string, e ApprovalRequestEntry) Request {
	return Request{
		Session: session, CallID: e.CallID, Tool: e.Tool,
		Args: slices.Clone(e.Args), ArgsSHA256: e.ArgsSHA256,
		RunID: e.RunID, Reason: e.Reason, Expiry: e.Expiry, Created: e.Created,
	}
}

// fillApprovalEntry stamps a chain entry's tree fields — id, parent,
// created — at append time, the same stamping recordTurnEnd gives the
// messages and the turn entry.
func fillApprovalEntry(e Entry, id, parent string, created time.Time) Entry {
	switch e := e.(type) {
	case ApprovalRequestEntry:
		e.ID, e.ParentID, e.Created = id, parent, created
		return e
	case ApprovalDecisionEntry:
		e.ID, e.ParentID, e.Created = id, parent, created
		return e
	case ApprovalAuditEntry:
		e.ID, e.ParentID, e.Created = id, parent, created
		return e
	case GrantEntry:
		// The approver's "approve and always allow" lands its grant
		// through the chain's batch, like Decide lands its.
		e.ID, e.ParentID, e.Created = id, parent, created
		return e
	}
	return e
}

// danglingCallsLocked returns the calls the leaf's path leaves
// unresolved — the tool calls of the last assistant message with
// calls, minus the results the message after it serves (ADR 0007's
// unresolved set, read from the tree). Through this package a
// dangling tail exists only where a successful turn parked: failed
// turns persist their partial repaired. Callers hold s.mu.
func (s *Session) danglingCallsLocked() []weft.ToolCallPart {
	path, err := s.pathLocked(s.leaf)
	if err != nil {
		return nil
	}
	var msgs []weft.Message
	for _, e := range path {
		if me, ok := e.(MessageEntry); ok {
			msgs = append(msgs, me.Message)
		}
	}
	last := -1
	for j := len(msgs) - 1; j >= 0; j-- {
		if msgs[j].Role != weft.RoleAssistant {
			continue
		}
		for _, p := range msgs[j].Content {
			if _, ok := p.(weft.ToolCallPart); ok {
				last = j
				break
			}
		}
		if last >= 0 {
			break
		}
	}
	if last < 0 {
		return nil
	}
	served := map[string]bool{}
	if last+1 < len(msgs) && msgs[last+1].Role == weft.RoleTool {
		for _, p := range msgs[last+1].Content {
			if r, ok := p.(weft.ToolResultPart); ok {
				served[r.CallID] = true
			}
		}
	}
	var out []weft.ToolCallPart
	for _, p := range msgs[last].Content {
		if c, ok := p.(weft.ToolCallPart); ok && !served[c.ID] {
			out = append(out, c)
		}
	}
	return out
}

// scopedDecisions keeps the decisions that belong to one request: the
// ones its own run recorded (ADR 0007 lets call ids repeat across
// turns, so a call id alone names nothing — the parked run does). A
// request with no run id keeps every decision for its call id, the
// defensive dangling path where no request entry exists.
func scopedDecisions(ds []ApprovalDecisionEntry, runID string) []ApprovalDecisionEntry {
	if runID == "" {
		return ds
	}
	var out []ApprovalDecisionEntry
	for _, d := range ds {
		if d.RunID == runID {
			out = append(out, d)
		}
	}
	return out
}

// approvalWalkLocked collects the approval entries on the leaf's
// path: the requests by call id, and every decision by call id in
// append order — the quorum's raw material, folded by
// effectiveDecision over the run-scoped slice. A call id the model
// re-issues (ADR 0007 lets call ids repeat across turns) starts a
// fresh occurrence: the walk resets the call's request and decisions
// at the message that carries it, so a later occurrence never
// inherits an earlier one's request or verdicts. Callers hold s.mu.
func (s *Session) approvalWalkLocked() (map[string]ApprovalRequestEntry, map[string][]ApprovalDecisionEntry) {
	path, err := s.pathLocked(s.leaf)
	if err != nil {
		return nil, nil
	}
	requests := map[string]ApprovalRequestEntry{}
	decisions := map[string][]ApprovalDecisionEntry{}
	for _, e := range path {
		switch e := e.(type) {
		case MessageEntry:
			for _, p := range e.Message.Content {
				if c, ok := p.(weft.ToolCallPart); ok {
					delete(requests, c.ID)
					delete(decisions, c.ID)
				}
			}
		case ApprovalRequestEntry:
			requests[e.CallID] = e
			decisions[e.CallID] = nil
		case ApprovalDecisionEntry:
			decisions[e.CallID] = append(decisions[e.CallID], e)
		}
	}
	return requests, decisions
}

// conflictReason is the model-visible denial text for conflicting
// decisions under quorum (ADR 0021 §5: pinned bytes).
const conflictReason = "conflicting decisions"

// effectiveDecision folds a call's recorded decisions into the one
// that resolves it, under the session's quorum (ADR 0021 §5): a deny
// alone resolves; a deny beside an approve is a conflict, and a
// conflict resolves to deny with the pinned reason, so nothing
// outranks a refusal and a split verdict names itself; one resolve
// resolves, a resolve beside an approve or a second resolve conflicts;
// and approvals resolve once n distinct approver identities (distinct
// Who, the empty string one identity) have approved. ok is false
// while the call is still pending.
func effectiveDecision(decisions []ApprovalDecisionEntry, quorum int) (ApprovalDecisionEntry, bool) {
	if quorum < 1 {
		quorum = 1
	}
	conflict := func(d ApprovalDecisionEntry) (ApprovalDecisionEntry, bool) {
		return ApprovalDecisionEntry{
			CallID: d.CallID, Outcome: OutcomeDeny,
			Reason: conflictReason, Via: "quorum", RunID: d.RunID,
		}, true
	}
	var resolve *ApprovalDecisionEntry
	sawApprove := false
	approvers := map[string]bool{}
	for i := range decisions {
		d := decisions[i]
		switch d.Outcome {
		case OutcomeDeny:
			if sawApprove {
				return conflict(d)
			}
			return d, true
		case OutcomeResolve, OutcomeResolveError:
			if resolve != nil || sawApprove {
				return ApprovalDecisionEntry{
					CallID: d.CallID, Outcome: OutcomeDeny,
					Reason: conflictReason, Via: "quorum", RunID: d.RunID,
				}, true
			}
			resolve = &decisions[i]
		case OutcomeApprove:
			if resolve != nil {
				// An approve beside a resolve is the same split verdict
				// the other order is — the fold's words, both ways.
				return conflict(d)
			}
			sawApprove = true
			approvers[d.Who] = true
		}
	}
	if resolve != nil {
		return *resolve, true
	}
	if sawApprove && len(approvers) >= quorum {
		// Any recorded approval serves as the effective one; they agree.
		return decisions[len(decisions)-1], true
	}
	return ApprovalDecisionEntry{}, false
}

// pendingLocked returns the undecided requests of the open boundary,
// in call order: the dangling calls that carry a request and no
// decision. A dangling call without a request entry — impossible
// through this package, whose chain writes one with the turn — is
// still reported, defensively: Pending never hides a dangling call.
// Callers hold s.mu.
func (s *Session) pendingLocked() []Request {
	dangling := s.danglingCallsLocked()
	if len(dangling) == 0 {
		return nil
	}
	requests, decisions := s.approvalWalkLocked()
	var out []Request
	for _, c := range dangling {
		run := ""
		if re, ok := requests[c.ID]; ok {
			run = re.RunID
		}
		if _, ok := effectiveDecision(scopedDecisions(decisions[c.ID], run), s.cfg.quorum); ok {
			continue
		}
		if re, ok := requests[c.ID]; ok {
			out = append(out, requestFromEntry(s.header.ID, re))
			continue
		}
		out = append(out, Request{
			Session: s.header.ID, CallID: c.ID, Tool: c.Name,
			Args: slices.Clone(c.Args), ArgsSHA256: hashArgs(c.Args),
		})
	}
	return out
}

// boundaryLocked reports whether the leaf's path ends on an approval
// boundary: calls left dangling that only a resume run resolves. While
// it is open no plain Send runs — the queue holds — and no compaction
// touches the tail: the dangling calls must stay raw for the decisions
// to resolve. Callers hold s.mu.
func (s *Session) boundaryLocked() bool {
	return len(s.danglingCallsLocked()) > 0
}

// expiredPendingLocked filters the pending requests strictly past
// their expiry. Callers hold s.mu.
func (s *Session) expiredPendingLocked(now time.Time) []Request {
	var out []Request
	for _, r := range s.pendingLocked() {
		if !r.Expiry.IsZero() && now.After(r.Expiry) {
			out = append(out, r)
		}
	}
	return out
}

// danglingDecisionsLocked maps the boundary's calls to the core
// options the resume run carries (ADR 0007's mapping, exact):
// approved calls run with Call.Approved set, denied ones show their
// reason, resolved ones their content. Undecided calls get nothing
// when denyUndecided is false; with it true (the resume run's own
// mode) each becomes weft.Deny(id, "no decision") — the same text the
// core's resolvePending writes, synthesized because the core only
// writes it when some decision exists: a resume carrying none would
// otherwise repair the dangling calls as interrupted, losing the
// boundary instead of denying it. Callers hold s.mu.
func (s *Session) danglingDecisionsLocked(denyUndecided bool) []weft.RunOption {
	dangling := s.danglingCallsLocked()
	if len(dangling) == 0 {
		return nil
	}
	requests, decisions := s.approvalWalkLocked()
	var opts []weft.RunOption
	for _, c := range dangling {
		run := ""
		if re, ok := requests[c.ID]; ok {
			run = re.RunID
		}
		d, ok := effectiveDecision(scopedDecisions(decisions[c.ID], run), s.cfg.quorum)
		if !ok {
			if denyUndecided {
				opts = append(opts, weft.Deny(c.ID, "no decision"))
			}
			continue
		}
		switch d.Outcome {
		case OutcomeApprove:
			opts = append(opts, weft.Approve(c.ID))
		case OutcomeDeny:
			opts = append(opts, weft.Deny(c.ID, d.Reason))
		case OutcomeResolve:
			opts = append(opts, weft.Resolve(c.ID, d.Content))
		case OutcomeResolveError:
			opts = append(opts, weft.ResolveError(c.ID, d.Content))
		}
	}
	return opts
}
