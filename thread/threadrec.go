package thread

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"

	"github.com/weftgo/weft/core"
)

// The thread record family (record contract §4, kind thread; plan
// U12.1, audit O14). The session's state log — approval requests and
// decisions, refused signatures, grants minted, used, revoked and
// lapsed, the busy policies' receipts, interrupts and rewinds, branch
// navigation, labels, pins, forks and pool delegations — is durable in
// the session's own storage and, beside it, reported through the
// agent's LoggerProvider as one OTel log record per fact: weft.record
// thread, event name weft.thread.<kind>, filed under the run the fact
// concerns (the compaction marker's rule: the run named by the entry,
// else the turn in flight, else the last run of this session that
// produced the leaf's context; with none — or when it is the turn in
// flight's and that run has not reported yet — held for the run that
// reports next; what is still held at Close goes out then, marked held,
// under the run it waited for or, with none, under no run, keyed by the
// session). The provider is called by the Session's emitter alone
// (threadout.go), never under s.mu.
//
// The body is one JSON object: kind, session, entry, at (the entry's
// time on the session clock) and the kind's ids, enums and counts —
// always — plus, under "content", the fields the content policy
// governs: who decided, a deny reason, resolved content, a grant's
// reason, a queued or steered message's text, a branch summary, a
// delegated task and its answer. Content rides only when capture is on
// (the agent's Content option, else the destination's Enabled answer
// for weft.messages — the core's rule), the record then marked
// weft.content = full, else none; weft/otel strips "content" for a
// content-off destination (weft.content = stripped) and redacts it
// for a content-on one. Titles, labels and other app-chosen names are
// metadata (ADR 0024): a label's name is a body field, never content.
//
// The exported copy is an index, not evidence (ADR 0021, amendment
// 2026-10-19): the session file is the record, and a reader that needs
// the trail reads Audit. Observation only: a logger that panics or
// refuses changes nothing the session does.
const (
	threadRecordKind = "thread"
	threadEventPfx   = "weft.thread."
	// maxHeldThread bounds the records held for a run that has not
	// reported yet; past it the oldest go, counted in the Close line.
	maxHeldThread = 256
)

// The record kinds (the weft.thread.<kind> suffix and the body's kind).
const (
	recApprovalRequest  = "approval.request"
	recApprovalDecision = "approval.decision"
	recApprovalRefused  = "approval.refused"
	recGrantMint        = "grant.mint"
	recGrantUse         = "grant.use"
	recGrantRevoke      = "grant.revoke"
	recGrantExpire      = "grant.expire"
	recReceipt          = "receipt"
	recInterrupt        = "interrupt"
	recRewind           = "rewind"
	recBranch           = "branch"
	recLabel            = "label"
	recFork             = "fork"
	recPoolDelegate     = "pool.delegate"
	recPoolRetire       = "pool.retire"
	recUncompact        = "uncompact"
	recPin              = "pin"
)

// threadEventNames are the record family's event names, spelled out
// (the record contract names each one).
var threadEventNames = map[string]string{
	recApprovalRequest:  "weft.thread.approval.request",
	recApprovalDecision: "weft.thread.approval.decision",
	recApprovalRefused:  "weft.thread.approval.refused",
	recGrantMint:        "weft.thread.grant.mint",
	recGrantUse:         "weft.thread.grant.use",
	recGrantRevoke:      "weft.thread.grant.revoke",
	recGrantExpire:      "weft.thread.grant.expire",
	recReceipt:          "weft.thread.receipt",
	recInterrupt:        "weft.thread.interrupt",
	recRewind:           "weft.thread.rewind",
	recBranch:           "weft.thread.branch",
	recLabel:            "weft.thread.label",
	recFork:             "weft.thread.fork",
	recPoolDelegate:     "weft.thread.pool.delegate",
	recPoolRetire:       "weft.thread.pool.retire",
	recUncompact:        "weft.thread.uncompact",
	recPin:              "weft.thread.pin",
}

// threadEventName is a kind's event name: weft.thread.<kind>.
func threadEventName(kind string) string {
	if n, ok := threadEventNames[kind]; ok {
		return n
	}
	return threadEventPfx + kind
}

// threadRec is one record before it is filed: its kind, the fields
// every destination receives, and the content-policy fields.
type threadRec struct {
	kind    string
	at      time.Time
	fields  map[string]any
	content map[string]string
}

// threadLogger is the logger the thread records go through: the
// agent's LoggerProvider under the thread instrumentation scope (the
// compaction marker's).
func (s *Session) threadLogger() log.Logger { return s.markerLogger() }

// reportLocked files one record under runID — or, when runID is "",
// under the run the session would name now (concernedRunLocked). The
// record is queued for the session's emitter (threadout.go), never
// emitted here: no provider call runs under s.mu. With no run to file
// it under — or when it is the turn in flight's and that run has not
// reported yet, so its span is unknown — it is held for the run that
// reports next (seeRun); a Close emits what is still held. Callers hold
// s.mu.
func (s *Session) reportLocked(runID string, r threadRec) {
	if r.at.IsZero() {
		r.at = s.now()
	}
	if runID == "" {
		runID = s.concernedRunLocked()
	}
	if runID == "" || s.awaitsSightLocked(runID) {
		if s.closing == stateSealed {
			// No run reports under a sealed Session: out at once, as a
			// held record (Close's rule).
			s.queueRecLocked(s.closeItemLocked(heldRec{rec: r, run: runID}))
			return
		}
		if len(s.heldRecs) >= maxHeldThread {
			s.heldRecs = slices.Delete(s.heldRecs, 0, 1)
			s.heldDropped++
		}
		s.heldRecs = append(s.heldRecs, heldRec{rec: r, run: runID})
		return
	}
	s.queueRecLocked(s.itemForRunLocked(runID, r))
}

// itemForRunLocked is one record filed under runID: under the run's
// invoke_agent span with its merged metadata when the Session saw it,
// else spanless with the session's metadata for that turn. Callers
// hold s.mu.
func (s *Session) itemForRunLocked(runID string, r threadRec) outItem {
	if sight, ok := s.sightLocked(runID); ok {
		return outItem{ctx: trace.ContextWithSpanContext(context.Background(), sight.sc), run: runID, md: sight.md, rec: r}
	}
	n, _ := runSeq(s.header.ID, runID)
	return outItem{ctx: context.Background(), run: runID, md: s.unseenRunMetadataLocked(n), rec: r}
}

// awaitsSightLocked reports whether runID is the turn in flight's and
// has not reported yet: a record or summarizer span filed under it now
// would have no parent span, so it waits for the run's first report.
// Callers hold s.mu.
func (s *Session) awaitsSightLocked(runID string) bool {
	if s.inFlight == nil || s.inFlight.RunID() != runID {
		return false
	}
	_, seen := s.sightLocked(runID)
	return !seen
}

// emitThread writes one record. ctx carries the span the record is
// filed under (or none); md is the run's metadata; held marks a record
// Close emitted because no run reported to take it (runID "" when it
// named none).
func (s *Session) emitThread(ctx context.Context, l log.Logger, runID string, md map[string]string, r threadRec, held bool) {
	body := make(map[string]any, len(r.fields)+5)
	for k, v := range r.fields {
		switch x := v.(type) {
		case string:
			if x == "" {
				continue
			}
		case nil:
			continue
		}
		body[k] = v
	}
	body["kind"] = r.kind
	body["session"] = s.header.ID
	body["at"] = r.at.UTC().Format(time.RFC3339Nano)
	if held {
		body["held"] = true
	}
	// weft.content says what the body holds: full when it carries a
	// content object, none when capture is off or the fact had no
	// content field set.
	content := "none"
	if c := nonEmpty(r.content); len(c) > 0 && s.captureContent(ctx, l) {
		content = "full"
		body["content"] = c
	}
	b, err := json.Marshal(body)
	if err != nil {
		return
	}
	var rec log.Record
	rec.SetTimestamp(time.Now())
	rec.SetEventName(threadEventName(r.kind))
	rec.SetSeverity(log.SeverityInfo)
	if r.kind == recApprovalRefused {
		rec.SetSeverity(log.SeverityWarn)
	}
	rec.SetBody(attribute.StringValue(string(b)))
	attrs := []attribute.KeyValue{
		attribute.String("weft.record", threadRecordKind),
		attribute.String("weft.content", content),
	}
	if runID != "" {
		attrs = append(attrs, attribute.String("weft.run.id", runID))
	}
	if name := s.agent.Name(); name != "" {
		attrs = append(attrs, attribute.String("gen_ai.agent.name", name))
	}
	for _, k := range slices.Sorted(maps.Keys(md)) {
		switch k {
		case "weft.record", "weft.run.id", "weft.content", "gen_ai.agent.name":
			continue // the record's own attributes win
		}
		if md[k] == "" {
			continue
		}
		attrs = append(attrs, attribute.String(k, md[k]))
	}
	rec.AddAttributes(attrs...)
	l.Emit(ctx, rec)
}

// captureContent is the content question for one thread record: the
// agent's Content option when set, else whether any destination wants
// message content — the one Enabled question the core asks.
func (s *Session) captureContent(ctx context.Context, l log.Logger) bool {
	if c, set := s.agent.ContentCapture(); set {
		return c
	}
	return l.Enabled(ctx, log.EnabledParameters{EventName: "weft.messages"})
}

func nonEmpty(m map[string]string) map[string]string {
	var out map[string]string
	for k, v := range m {
		if v == "" {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[k] = v
	}
	return out
}

// concernedRunLocked is the run a session-level fact concerns when its
// entry names none: the turn in flight — the busy cause of a receipt,
// the turn whose chain minted a grant — else the last run of this
// session that produced the leaf's context (the compaction marker's
// rule); "" when neither exists. Callers hold s.mu.
func (s *Session) concernedRunLocked() string {
	if s.inFlight != nil {
		if id := s.inFlight.RunID(); id != "" {
			if _, ours := runSeq(s.header.ID, id); ours {
				return id
			}
		}
	}
	return s.lastRunIDLocked()
}

// takeHeldLocked takes the records held for the run reporting as
// runID — the untargeted ones and its own — and those whose run is no
// longer the turn in flight (it ended without reporting: they go out
// spanless). The rest stay held. Callers hold s.mu.
func (s *Session) takeHeldLocked(runID string) (mine, stale []heldRec) {
	keep := s.heldRecs[:0]
	for _, h := range s.heldRecs {
		switch {
		case h.run == "" || h.run == runID:
			mine = append(mine, h)
		case s.inFlight == nil || s.inFlight.RunID() != h.run:
			stale = append(stale, h)
		default:
			keep = append(keep, h)
		}
	}
	s.heldRecs = slices.Clip(keep)
	if len(s.heldRecs) == 0 {
		s.heldRecs = nil
	}
	return mine, stale
}

// reportEntryLocked is the adoption hook (adoptLocked): every entry the
// session accepts that is a state-log fact becomes its record. The
// navigation kinds (a leaf entry's meaning depends on who wrote it)
// and the facts that are no entry — an interrupt, a fork, a grant's
// lapse — are reported by their own paths. Callers hold s.mu.
func (s *Session) reportEntryLocked(e Entry) {
	switch e := e.(type) {
	case ApprovalRequestEntry:
		f := map[string]any{
			"entry": e.ID, "call_id": e.CallID, "tool": e.Tool, "args_sha256": e.ArgsSHA256,
			"child_session": e.Child, "wrapper_call": e.Wrapper,
		}
		if !e.Expiry.IsZero() {
			f["expiry"] = e.Expiry.UTC().Format(time.RFC3339Nano)
		}
		if q := s.cfg.quorum; q > 1 {
			f["quorum"] = q
		}
		s.reportLocked(e.RunID, threadRec{kind: recApprovalRequest, at: e.Created, fields: f})
	case ApprovalDecisionEntry:
		f := map[string]any{
			"entry": e.ID, "call_id": e.CallID, "outcome": string(e.Outcome), "via": e.Via,
			"key_id": e.KeyID, "request_entry": e.RequestID,
		}
		if e.Always {
			f["always"] = true
		}
		if a, ok := s.precedingLocked(e.ID).(ApprovalAuditEntry); ok && a.Step == StepGrant && a.CallID == e.CallID {
			// The chain's grant step: the grant that decided, and — a
			// deny-grant's decision records no channel of its own — via.
			f["grant_id"] = a.GrantID
			if e.Via == "" {
				f["via"] = viaGrant
			}
			if a.GrantShared {
				f["grant_scope"] = "shared"
			}
		}
		s.reportLocked(e.RunID, threadRec{kind: recApprovalDecision, at: e.Created, fields: f,
			content: map[string]string{"who": e.Who, "reason": e.Reason, "content": e.Content}})
	case ApprovalAuditEntry:
		switch e.Step {
		case StepSigned:
			s.reportLocked(e.RunID, threadRec{kind: recApprovalRefused, at: e.Created, fields: map[string]any{
				"entry": e.ID, "call_id": e.CallID, "reason": strings.ReplaceAll(e.Detail, " ", "_"), "key_id": e.KeyID,
			}})
		case StepGrant:
			f := map[string]any{
				"entry": e.ID, "call_id": e.CallID, "grant_id": e.GrantID, "outcome": e.Outcome, "scope": "session",
			}
			var g *GrantEntry
			if e.GrantShared {
				f["scope"] = "shared"
			} else if i, ok := s.byID[e.GrantID]; ok {
				if ge, ok := s.order[i].(GrantEntry); ok {
					g = &ge
					uses := s.grantUsesLocked(e.GrantID)
					f["uses"] = uses
					if ge.MaxUses > 0 {
						f["max_uses"] = ge.MaxUses
					}
				}
			}
			s.reportLocked(e.RunID, threadRec{kind: recGrantUse, at: e.Created, fields: f})
			if g != nil && g.MaxUses > 0 && s.grantUsesLocked(g.ID) == g.MaxUses {
				s.reportGrantExpiryLocked(e.RunID, *g, "max_uses", e.Created)
			}
		}
	case GrantEntry:
		f := grantFields(e)
		run := ""
		if d, ok := s.precedingLocked(e.ID).(ApprovalDecisionEntry); ok {
			f["call_id"], f["via"] = d.CallID, "always"
			run = d.RunID
		}
		s.reportLocked(run, threadRec{kind: recGrantMint, at: e.Created, fields: f,
			content: map[string]string{"reason": e.Reason}})
	case GrantRevokedEntry:
		f := map[string]any{"entry": e.ID, "grant_id": e.GrantID}
		if i, ok := s.byID[e.GrantID]; ok {
			if ge, ok := s.order[i].(GrantEntry); ok {
				f["tool"] = ge.Tool
				f["uses"] = s.grantUsesLocked(ge.ID)
			}
		}
		s.reportLocked("", threadRec{kind: recGrantRevoke, at: e.Created, fields: f})
	case ReceiptEntry:
		s.reportReceiptLocked(e)
	case PoolReceiptEntry:
		switch e.Status {
		case PoolAccepted:
			s.reportLocked("", threadRec{kind: recPoolDelegate, at: e.Created, fields: map[string]any{
				"entry": e.ID, "receipt_id": e.ID, "child_session": e.Child, "call_id": e.Call, "state": e.Status,
			}, content: map[string]string{"prompt": e.Prompt}})
		case PoolDone, PoolFailed, PoolCanceled, PoolCapped:
			f := map[string]any{
				"entry": e.ID, "receipt_id": e.Receipt, "child_session": e.Child, "call_id": e.Call, "state": e.Status,
			}
			if e.Usage != (core.Usage{}) {
				f["usage"] = e.Usage
			}
			s.reportLocked("", threadRec{kind: recPoolRetire, at: e.Created, fields: f,
				content: map[string]string{"stop": e.Stop}})
		}
	case LabelEntry:
		// The label's name is an app-chosen name: metadata (ADR 0024),
		// never content — it survives a content-off destination.
		s.reportLocked("", threadRec{kind: recLabel, at: e.Created, fields: map[string]any{
			"entry": e.ID, "target": e.Entry, "name": e.Name,
		}})
	case CustomEntry:
		if e.Kind == pinKind {
			var target string
			_ = json.Unmarshal(e.Data, &target)
			s.reportLocked("", threadRec{kind: recPin, at: e.Created, fields: map[string]any{
				"entry": e.ID, "target": target,
			}})
		}
	}
}

// reportReceiptLocked files a busy-policy receipt (ADR 0019): its
// state, the kind of input it carries (a steer or a queued send), the
// turn a queued send was minted as, and the message's text under the
// content policy. Delivered receipts are filed under the run that
// drained them; the rest under the turn in flight (the busy cause) —
// never under a queued send's own minted run, which may never run.
func (s *Session) reportReceiptLocked(e ReceiptEntry) {
	id := e.Receipt
	if id == "" {
		id = e.ID
	}
	kind := "send"
	switch e.Status {
	case ReceiptQueued:
		kind = "steer"
	case ReceiptAccepted:
	default:
		if i, ok := s.byID[e.Receipt]; ok {
			if acc, ok := s.order[i].(ReceiptEntry); ok && acc.Status == ReceiptQueued {
				kind = "steer"
			}
		}
	}
	f := map[string]any{"entry": e.ID, "receipt_id": id, "state": e.Status, "input": kind}
	minted := ""
	switch {
	case e.Status == ReceiptAccepted:
		minted = e.RunID
	case e.Status == ReceiptQueued && s.steerMint != nil && s.steerMint.id == e.ID:
		// A steer takes a turn number as it is accepted (newTurnLocked):
		// delivered, it runs inside the busy turn and that number never
		// names a run — the receipt says which number it took.
		minted = s.steerMint.RunID()
	}
	if minted != "" {
		f["minted_run"] = minted
		if n, ok := runSeq(s.header.ID, minted); ok {
			f["turn"] = n
		}
	}
	if e.Status == ReceiptDeferred {
		f["follow_up_entry"] = e.Turn
	}
	if e.Unanswered {
		f["unanswered"] = true
	}
	var text string
	if e.Msg != nil {
		text = e.Msg.Text()
	}
	run := ""
	if e.Status == ReceiptDelivered {
		run = e.RunID
	}
	s.reportLocked(run, threadRec{kind: recReceipt, at: e.Created, fields: f,
		content: map[string]string{"text": text}})
}

// grantFields are a grant's ids, enums and counts: its id, tool,
// whether it denies, its predicate count, MaxUses and expiry — the
// predicates' values are argument content and never leave.
func grantFields(e GrantEntry) map[string]any {
	f := map[string]any{"entry": e.ID, "grant_id": e.ID, "tool": e.Tool, "scope": "session", "args": len(e.Args)}
	if e.Deny {
		f["deny"] = true
	}
	if e.MaxUses > 0 {
		f["max_uses"] = e.MaxUses
	}
	if !e.Expiry.IsZero() {
		f["expiry"] = e.Expiry.UTC().Format(time.RFC3339Nano)
	}
	return f
}

// reportGrantExpiryLocked files a grant's lapse — reason expiry (its
// Expiry passed) or max_uses (its last use) — once per grant per
// Session. Callers hold s.mu.
func (s *Session) reportGrantExpiryLocked(runID string, g GrantEntry, reason string, at time.Time) {
	if s.grantsLapsed[g.ID] {
		return
	}
	if s.grantsLapsed == nil {
		s.grantsLapsed = map[string]bool{}
	}
	s.grantsLapsed[g.ID] = true
	f := map[string]any{"grant_id": g.ID, "tool": g.Tool, "reason": reason, "uses": s.grantUsesLocked(g.ID)}
	if g.MaxUses > 0 {
		f["max_uses"] = g.MaxUses
	}
	if !g.Expiry.IsZero() {
		f["expiry"] = g.Expiry.UTC().Format(time.RFC3339Nano)
	}
	s.reportLocked(runID, threadRec{kind: recGrantExpire, at: at, fields: f})
}

// grantUsesLocked counts a session grant's uses: its StepGrant audit
// entries (the chain's own count, liveGrantsLocked's).
func (s *Session) grantUsesLocked(grantID string) int {
	n := 0
	for _, e := range s.order {
		if a, ok := e.(ApprovalAuditEntry); ok && a.Step == StepGrant && a.GrantID == grantID && !a.GrantShared {
			n++
		}
	}
	return n
}

// precedingLocked is the entry adopted just before id, nil when none.
func (s *Session) precedingLocked(id string) Entry {
	i, ok := s.byID[id]
	if !ok || i == 0 {
		return nil
	}
	return s.order[i-1]
}

// reportNavLocked files a navigation the session just appended — the
// leaf entry newest in the tree: branch, uncompact or rewind, filed
// under run (the run on the line being left; "" for the session's
// choice). from is the leaf before the navigation. Callers hold s.mu.
func (s *Session) reportNavLocked(kind, run, from string, extra map[string]any, content map[string]string) {
	var nav LeafEntry
	for i := len(s.order) - 1; i >= 0; i-- {
		if le, ok := s.order[i].(LeafEntry); ok {
			nav = le
			break
		}
	}
	if nav.ID == "" {
		return
	}
	f := map[string]any{"entry": nav.ID, "from_entry": from, "to_entry": nav.Entry}
	maps.Copy(f, extra)
	s.reportLocked(run, threadRec{kind: kind, at: nav.Created, fields: f, content: content})
}

// lastRunOnPathLocked is lastRunIDLocked over a given path: the run of
// its newest turn entry this session minted. Callers hold s.mu.
func (s *Session) lastRunOnPathLocked(path []Entry) string {
	for i := len(path) - 1; i >= 0; i-- {
		e, ok := path[i].(TurnEntry)
		if !ok || e.RunID == "" {
			continue
		}
		if _, ours := runSeq(s.header.ID, e.RunID); !ours {
			return ""
		}
		return e.RunID
	}
	return ""
}

// The summarizer's own calls (audit O15): every model call a
// compaction or a branch summary makes is a chat span — the agent's
// TracerProvider, the thread scope — carrying the call's model and
// usage and weft.chat.role = summarizer, filed under the run the
// session names (the turn in flight, else the last run; with none, or
// when the turn in flight has not reported yet — the pre-run trigger's
// compaction — held and opened, with its recorded times, under that run
// when it reports). Usage is what the provider reported; it is never
// folded into a run's own usage — a reader adds it as the summarizer's
// (obsdb's SessionRow.SummarizerUsage).

const (
	summarizerRole = "summarizer"
	attrChatRole   = "weft.chat.role"
)

// summarizerCall is one recorded summarizer model call. run is the
// run a held call waits for ("" for the next run that reports).
type summarizerCall struct {
	start, end time.Time
	model      core.ModelInfo
	usage      core.Usage
	err        error
	run        string
}

// recordSummarizer files a summarizer call's span: under the run the
// session names now, a child of its invoke_agent span. A call with no
// run to name, or one filed under the turn in flight before that run
// reported (the pre-run trigger's compaction), is held and opened, with
// its recorded times, under the run when it reports (seeRun) — never as
// a trace of its own; at most maxHeldThread wait, the oldest dropped
// past it with a Debug line. A call filed under a run this Session
// never saw report (Open, then Compact before any turn: the last run is
// a previous writer's) has no span to open under: it is dropped with a
// Debug line, as a stale held call is — one opened on a bare context
// would be a root in a trace of its own, read as a run (record contract
// §3). Its usage is not lost: the compaction marker (summarizer_usage,
// summarizer_model) or the branch record that call served is a session
// record filed under that run all the same, and Studio's timeline
// prices the marks; only obsdb's span sum (SessionRow.SummarizerUsage)
// leaves it out. Callers must not hold s.mu.
func (s *Session) recordSummarizer(c summarizerCall) {
	var (
		run     string
		sight   runSight
		seen    bool
		held    bool
		evicted bool
	)
	s.locked(func() {
		run = s.concernedRunLocked()
		if run == "" || s.awaitsSightLocked(run) {
			if s.closing != stateSealed {
				c.run = run
				if len(s.heldSummaries) >= maxHeldThread {
					s.heldSummaries = slices.Delete(s.heldSummaries, 0, 1)
					evicted = true
				}
				s.heldSummaries = append(s.heldSummaries, c)
				held = true
			}
			return
		}
		sight, seen = s.sightLocked(run)
	})
	if evicted {
		s.agent.Logger().Debug("thread: summarizer span dropped: too many held for a run that has not reported",
			"session", s.header.ID, "held", maxHeldThread)
	}
	if held {
		return
	}
	if run == "" {
		s.agent.Logger().Debug("thread: summarizer span dropped: no run to file it under", "session", s.header.ID)
		return
	}
	if !seen {
		s.agent.Logger().Debug("thread: summarizer span dropped: its run is not one this Session saw report",
			"session", s.header.ID, "run", run)
		return
	}
	s.emitSummarizer(trace.ContextWithSpanContext(context.Background(), sight.sc), run, sight.md, c)
}

// takeHeldSummariesLocked splits the held summarizer calls as
// takeHeldLocked splits the records. Callers hold s.mu.
func (s *Session) takeHeldSummariesLocked(runID string) (mine, stale []summarizerCall) {
	var keep []summarizerCall
	for _, c := range s.heldSummaries {
		switch {
		case c.run == "" || c.run == runID:
			mine = append(mine, c)
		case s.inFlight == nil || s.inFlight.RunID() != c.run:
			stale = append(stale, c)
		default:
			keep = append(keep, c)
		}
	}
	s.heldSummaries = keep
	return mine, stale
}

// emitSummarizer opens and ends one summarizer span with its recorded
// times. Observation only: a panicking tracer changes nothing.
func (s *Session) emitSummarizer(ctx context.Context, runID string, md map[string]string, c summarizerCall) {
	defer func() {
		if p := recover(); p != nil {
			s.agent.Logger().Debug("thread: summarizer span dropped", "session", s.header.ID, "panic", fmt.Sprint(p))
		}
	}()
	tp := s.agent.TracerProvider()
	if tp == nil {
		return
	}
	tr := tp.Tracer(markerScopeName)
	name := "chat"
	if c.model.Name != "" {
		name += " " + c.model.Name
	}
	attrs := []attribute.KeyValue{
		attribute.String("gen_ai.operation.name", "chat"),
		attribute.String("weft.run.id", runID),
		attribute.String(attrChatRole, summarizerRole),
		attribute.Int64("gen_ai.usage.input_tokens", c.usage.InputTokens),
		attribute.Int64("gen_ai.usage.output_tokens", c.usage.OutputTokens),
	}
	if c.model.Provider != "" {
		attrs = append(attrs, attribute.String("gen_ai.provider.name", c.model.Provider))
	}
	if c.model.Name != "" {
		attrs = append(attrs, attribute.String("gen_ai.request.model", c.model.Name))
	}
	if c.usage.CachedInputTokens > 0 {
		attrs = append(attrs, attribute.Int64("gen_ai.usage.cache_read.input_tokens", c.usage.CachedInputTokens))
	}
	if c.usage.CacheWriteTokens > 0 {
		attrs = append(attrs, attribute.Int64("gen_ai.usage.cache_creation.input_tokens", c.usage.CacheWriteTokens))
	}
	if c.usage.ReasoningTokens > 0 {
		attrs = append(attrs, attribute.Int64("gen_ai.usage.reasoning.output_tokens", c.usage.ReasoningTokens))
	}
	if name := s.agent.Name(); name != "" {
		attrs = append(attrs, attribute.String("gen_ai.agent.name", name))
	}
	for _, k := range slices.Sorted(maps.Keys(md)) {
		switch k {
		case "weft.run.id", "gen_ai.agent.name", "gen_ai.operation.name", attrChatRole:
			continue
		}
		if md[k] == "" {
			continue
		}
		attrs = append(attrs, attribute.String(k, md[k]))
	}
	_, span := tr.Start(ctx, name, trace.WithSpanKind(trace.SpanKindClient),
		trace.WithTimestamp(c.start), trace.WithAttributes(attrs...))
	if c.err != nil {
		span.SetStatus(codes.Error, c.err.Error())
		span.SetAttributes(attribute.String("error.type", "summarizer"))
	}
	span.End(trace.WithTimestamp(c.end))
}

// emitHeldSummaries opens held summarizer spans under the run
// reporting on rctx. The stale ones — their run ended without
// reporting — are never opened: with no span to open under they would
// be root spans of traces of their own (seeRun and Close drop them with
// a Debug line).
func (s *Session) emitHeldSummaries(rctx context.Context, runID string, mine []summarizerCall) {
	ctx := trace.ContextWithSpanContext(context.Background(), trace.SpanContextFromContext(rctx))
	md := core.MetadataFromContext(rctx)
	for _, c := range mine {
		s.emitSummarizer(ctx, runID, md, c)
	}
}

// identityMetadataLocked is the session's identity pairs for the run
// minted as turn n (the turnSeq its id carries), without the caller's
// — runMetadata's base and the fallback for a run this Session did not
// see. Callers hold s.mu.
func (s *Session) identityMetadataLocked(n int) map[string]string {
	md := map[string]string{
		"weft.session.id": s.header.ID,
		"weft.turn":       strconv.Itoa(n), // the turnSeq the run id was minted from
	}
	if v := s.metaLocked()[publicIDKey]; v != "" {
		md[publicIDKey] = v
	}
	if v := s.titleLocked(); v != "" {
		md[sessionTitleKey] = v
	}
	if p := s.header.Parent; p != nil { // a fork
		md["weft.session.forked_from"] = p.Session + "#" + p.Entry
	}
	if l := s.header.Lineage; l != nil { // a pool child
		md["weft.session.parent"] = l.Session
		if l.Call != "" {
			md["weft.session.parent_call"] = l.Call
		}
	}
	return md
}

// unseenRunMetadataLocked is the metadata a record filed under a run
// this Session did not see carries (it ran before a reopen, or is out
// of the sights): the session's run metadata (WithRunMetadata) under
// its identity — the caller's per-Send pairs are gone. Callers hold
// s.mu.
func (s *Session) unseenRunMetadataLocked(n int) map[string]string {
	md := maps.Clone(s.runMetaLocked())
	if md == nil {
		md = map[string]string{}
	}
	for _, k := range sessionOwnedKeys {
		delete(md, k) // the session's own, or none
	}
	maps.Copy(md, s.identityMetadataLocked(n))
	return md
}
