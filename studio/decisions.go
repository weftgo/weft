package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The decisions a resume applied (U13.2; UX-B-03, UX-H-17, UX-S3-04):
// GET /api/runs/{id} serves, on a parked run a resume names
// (resumed_by), `decided` — each parked call's outcome as that resume
// applied it — and, on the resume itself (resumed_from), `resumes`: the
// same calls, the card the run page and the panel draw before step 0.
// Read time, never stored, from records already on the wire:
//
//   - the calls and their decision kinds: the resume's weft.resume.calls
//     (the invoke_agent span, U13.1) — else the resume's pre-step tool
//     results (the step-0 output batches before the model's first
//     message), outcome derived from the result text (source
//     "transcript": a resolve then reads as approved unless thread's
//     record names it) — else the resume's step-0 execute_tool spans
//     that ran before its first model call or were denied (source
//     "spans": a resolve has none) — else no list at all: hole
//     not_recorded, never an empty list without one;
//   - the outcome, via / key / grant / when: thread's approval.decision
//     records filed under the parked run or the resume (the body's
//     outcome, via, key_id, grant_id, at; who under content), folded as
//     thread folds them under the quorum its approval.request names —
//     every approver of a met quorum named, a call short of it unnamed
//     (core's no_decision takes no attribution), a split verdict via
//     quorum naming its denier as the decider and the approvals as
//     approvers, its reason the model-visible DENIED text — else the
//     resumed call's execute_tool span (weft.tool.approved_by,
//     weft.grant.id; a denied call's zero-length span's start);
//   - a resolved call's result: the resume's pre-step result (core runs
//     no handler for a resolve — no tool event, no span), under the
//     content policy (result_badge hidden / not_recorded);
//   - the reason of a denial: thread's content.reason, else the resume's
//     DENIED result text after "DENIED: " (both under the content
//     policy: absent content-off, reason_badge not_recorded);
//   - the resume's weft.approval.actor: metadata — an opaque id the app
//     chose — so every identity reads it;
//   - expired: a deny thread decided via expiry, or a DENIED result
//     reading "expired: …".
//
// A read-scoped panel token gets no who, no reason and no resolved
// result (reason_badge / result_badge hidden; who_hidden unless one
// actor names the decider — several comma-joined, or a split verdict's
// two sides, name no one call's: readsThreadContent's rule). A read that fails is said (error, or a note when the
// block still stands), never an empty list.

// approvalActorKey is the run metadata naming who decided a resume's
// calls (weft.approval.actor): an opaque id, metadata every identity
// reads — the decided block's actor and a decision mark's.
const approvalActorKey = "weft.approval.actor"

// decisionsCap bounds the calls one decisions block lists (core's
// weft.resume.calls cap; past it More counts the rest).
const decisionsCap = 32

// decisionDoc is the decisions one resume applied to one parked run.
// Run is the other side: on the parked run's `decided` the resume, on
// the resume's `resumes` the parked run. At is the resume's start (the
// moment it applied the decisions); Actor its weft.approval.actor
// (metadata, never content). Source says where the calls come from:
// resume_calls (weft.resume.calls), transcript (the resume's pre-step
// results), spans (its pre-step execute_tool spans) or none — then Hole
// is not_recorded. Notes are the block's honesty lines: a read that
// failed beside it, decision records past the read's cap.
type decisionDoc struct {
	Run    string        `json:"run"`
	At     time.Time     `json:"at"`
	Actor  string        `json:"actor,omitempty"`
	Source string        `json:"source"`
	Calls  []decidedCall `json:"calls"`
	More   int           `json:"more,omitempty"`
	Hole   string        `json:"hole,omitempty"`
	Notes  []string      `json:"notes,omitempty"`
	Error  string        `json:"error,omitempty"`
}

// decidedCall is one parked call's decision. Outcome is approved,
// denied, resolved, expired or no_decision; Derived says it was read
// from the result text or a span, not from a decision record (a resolve
// is then not told apart). Via is thread's via (user, signed, grant,
// approver, quorum, expiry, …), else the span's approved_by. Who is
// thread's content.who — every approver of a quorum, " + "-joined, as
// KeyID their keys; a split verdict's Who its denier, ApprovedBy the
// approvers it overruled; WhoHidden says a who in content the reader
// may not read and no single actor names. Reason is a denial's reason,
// ReasonBadge why a denial has none (hidden, not_recorded); Result a
// resolved call's result (the resume's pre-step result: core runs no
// handler, so no tool event or span carries it), ResultBadge why it has
// none. At is the decision's own time when a record names one.
type decidedCall struct {
	CallID      string     `json:"call_id"`
	Name        string     `json:"name,omitempty"`
	Outcome     string     `json:"outcome"`
	Derived     bool       `json:"derived,omitempty"`
	Via         string     `json:"via,omitempty"`
	KeyID       string     `json:"key_id,omitempty"`
	GrantID     string     `json:"grant_id,omitempty"`
	Who         string     `json:"who,omitempty"`
	WhoHidden   bool       `json:"who_hidden,omitempty"`
	ApprovedBy  string     `json:"approved_by,omitempty"`
	Reason      string     `json:"reason,omitempty"`
	ReasonBadge string     `json:"reason_badge,omitempty"`
	Result      string     `json:"result,omitempty"`
	ResultBadge string     `json:"result_badge,omitempty"`
	At          *time.Time `json:"at,omitempty"`
}

// The outcomes a decided call reads.
const (
	outcomeApproved   = "approved"
	outcomeDenied     = "denied"
	outcomeResolved   = "resolved"
	outcomeExpired    = "expired"
	outcomeNoDecision = "no_decision"
)

// deniedPrefix is the result text core gives a denied call (rule 12).
const deniedPrefix = "DENIED: "

// runDecisions fills the run page's decided / resumes blocks.
func (s *Server) runDecisions(r *http.Request, rec obsdb.RunRow, page *runPage) {
	ctx := r.Context()
	pub := panelScope(r)
	if rec.ResumedBy != "" && rec.Pending > 0 {
		if det, err := s.db.Run(ctx, rec.ResumedBy); err != nil {
			page.Decided = &decisionDoc{Run: rec.ResumedBy, Calls: []decidedCall{}, Error: "the resume: " + err.Error()}
		} else if inScope(pub, det.RunRow) {
			d := s.decisionsOf(ctx, r, rec, det.RunRow)
			d.Run = det.ID
			page.Decided = &d
		}
	}
	if rec.ResumedFrom != "" {
		parked := obsdb.RunRow{ID: rec.ResumedFrom}
		var note string
		if det, err := s.db.Run(ctx, rec.ResumedFrom); err == nil {
			if !inScope(pub, det.RunRow) {
				return
			}
			parked = det.RunRow
		} else {
			// The calls are the resume's own records; the parked run
			// only scopes thread's decision records — the resume's
			// session stands in for it, said.
			note = "the parked run could not be read: " + err.Error()
		}
		d := s.decisionsOf(ctx, r, parked, rec)
		d.Run = rec.ResumedFrom
		if note != "" {
			d.Notes = append([]string{note}, d.Notes...)
		}
		page.Resumes = &d
	}
}

// decisionsOf composes what resume applied to parked's calls.
func (s *Server) decisionsOf(ctx context.Context, r *http.Request, parked, resume obsdb.RunRow) decisionDoc {
	out := decisionDoc{At: resume.Started, Actor: resume.Meta[approvalActorKey], Calls: []decidedCall{}}
	reads := readsThreadContent(r)
	spans, err := s.db.RunSpans(ctx, resume.ID)
	if err != nil {
		out.Error = "the resume's spans: " + err.Error()
		return out
	}
	batches, err := s.db.TranscriptBatches(ctx, resume.ID)
	if err != nil {
		out.Error = "the resume's transcript: " + err.Error()
		return out
	}
	results, names := preStepResults(batches)
	byCall := map[string]obsdb.Span{}
	var calls string
	var firstChat time.Time
	var step0 []obsdb.Span
	for _, sp := range spans {
		if attrString(sp.Attrs, "weft.run.id") != "" && attrString(sp.Attrs, "weft.run.id") != resume.ID {
			continue
		}
		switch {
		case strings.HasPrefix(sp.Name, "invoke_agent"):
			if v := attrString(sp.Attrs, "weft.resume.calls"); v != "" {
				calls = v
			}
		case strings.HasPrefix(sp.Name, "chat"):
			if spanStep(sp) == 0 && (firstChat.IsZero() || sp.Start.Before(firstChat)) {
				firstChat = sp.Start
			}
		case strings.HasPrefix(sp.Name, "execute_tool"):
			if id := attrString(sp.Attrs, "gen_ai.tool.call.id"); id != "" {
				if _, seen := byCall[id]; !seen && spanStep(sp) == 0 {
					byCall[id] = sp
					step0 = append(step0, sp)
				}
				if names[id] == "" {
					names[id] = attrString(sp.Attrs, "gen_ai.tool.name")
				}
			}
		}
	}
	switch {
	case calls != "":
		out.Source = "resume_calls"
		for _, kv := range strings.Split(calls, ",") {
			if rest, ok := strings.CutPrefix(kv, "…+"); ok {
				out.More, _ = strconv.Atoi(rest)
				continue
			}
			id, kind, _ := strings.Cut(kv, "=")
			out.Calls = append(out.Calls, decidedCall{CallID: id, Outcome: kindOutcome(kind)})
		}
	case len(results.order) > 0:
		out.Source = "transcript"
		for _, res := range results.order {
			out.Calls = append(out.Calls, decidedCall{CallID: res, Outcome: derivedOutcome(results.by[res]), Derived: true})
		}
	default:
		// No weft.resume.calls and no pre-step results (an older core
		// with content off): the step-0 execute_tool spans that ran
		// before the first model call, or that a resume denied.
		out.Source = "spans"
		slices.SortStableFunc(step0, func(a, b obsdb.Span) int { return a.Start.Compare(b.Start) })
		for _, sp := range step0 {
			denied := obsdb.AttrTrue(sp.Attrs, "weft.tool.denied")
			if !denied && !firstChat.IsZero() && !sp.Start.Before(firstChat) {
				continue // the model's own call; with no model call, every step-0 call is a decided one
			}
			c := decidedCall{CallID: attrString(sp.Attrs, "gen_ai.tool.call.id"), Outcome: outcomeApproved, Derived: true}
			if denied {
				c.Outcome = outcomeDenied
				if attrString(sp.Attrs, "weft.tool.denied_kind") == outcomeNoDecision {
					c.Outcome = outcomeNoDecision
				}
			}
			out.Calls = append(out.Calls, c)
		}
		if len(out.Calls) == 0 {
			out.Source = "none"
			out.Hole = string(obsdb.HoleNotRecorded)
		}
	}
	if len(out.Calls) > decisionsCap {
		out.More += len(out.Calls) - decisionsCap
		out.Calls = out.Calls[:decisionsCap]
	}
	recs, note := s.threadDecisions(ctx, parked, resume)
	if note != "" {
		out.Notes = append(out.Notes, note)
	}
	for i := range out.Calls {
		c := &out.Calls[i]
		c.Name = names[c.CallID]
		res, hasRes := results.by[c.CallID]
		if sp, ok := byCall[c.CallID]; ok {
			c.Via = attrString(sp.Attrs, "weft.tool.approved_by")
			c.GrantID = attrString(sp.Attrs, "weft.grant.id")
			if obsdb.AttrTrue(sp.Attrs, "weft.tool.denied") && !sp.Start.IsZero() {
				at := sp.Start.UTC()
				c.At = &at
			}
		}
		var reason string
		// The record's attribution only with its outcome adopted: a call
		// core left undecided (short of its quorum) names no approver.
		if rec, ok := recs[c.CallID]; ok && rec.outcome != "" && c.Outcome != outcomeNoDecision {
			// The record names the decision (a resolve reads resolved).
			c.Outcome, c.Derived = rec.outcome, false
			if rec.via != "" {
				c.Via = rec.via
			}
			if k := strings.Join(rec.keys, " + "); k != "" {
				c.KeyID = k
			}
			if rec.grantID != "" {
				c.GrantID = rec.grantID
			}
			if rec.at != nil {
				c.At = rec.at
			}
			if rec.via == "expiry" && c.Outcome == outcomeDenied {
				c.Outcome = outcomeExpired
			}
			if len(rec.who)+len(rec.approvers) > 0 {
				switch {
				case reads:
					c.Who = strings.Join(rec.who, " + ")
					c.ApprovedBy = strings.Join(rec.approvers, " + ")
				case out.Actor == "" || strings.Contains(out.Actor, ",") || len(rec.approvers) > 0:
					// The actor is metadata and names the decider when it
					// is one; several (thread comma-joins them) or a split
					// verdict's sides are no one call's — a who in content
					// alone is then hidden from this token.
					c.WhoHidden = true
				}
			}
			reason = rec.reason // a split verdict's is the transcript's (below)
		}
		if c.Outcome == outcomeResolved {
			switch {
			case !hasRes || res.Content == "":
				c.ResultBadge = string(obsdb.HoleNotRecorded)
			case !reads:
				c.ResultBadge = string(obsdb.HoleHidden)
			default:
				c.Result = res.Content
			}
		}
		if hasRes && c.Outcome == outcomeDenied && strings.HasPrefix(res.Content, deniedPrefix+"expired") {
			c.Outcome = outcomeExpired
		}
		if c.Outcome != outcomeDenied && c.Outcome != outcomeExpired {
			continue // no_decision is its own reason (rule 12)
		}
		if reason == "" && hasRes {
			reason = strings.TrimPrefix(res.Content, deniedPrefix)
		}
		switch {
		case reason == "":
			c.ReasonBadge = string(obsdb.HoleNotRecorded)
		case !reads:
			c.ReasonBadge = string(obsdb.HoleHidden)
		default:
			c.Reason = reason
		}
	}
	return out
}

// kindOutcome maps a weft.resume.calls kind to its outcome word.
func kindOutcome(kind string) string {
	switch kind {
	case "approved":
		return outcomeApproved
	case "denied":
		return outcomeDenied
	case "resolved":
		return outcomeResolved
	case "no_decision":
		return outcomeNoDecision
	}
	return kind
}

// derivedOutcome reads a pre-step result's outcome from its text: core's
// "DENIED: no decision" (rule 12), "DENIED: expired: …" (thread's lapse),
// any other DENIED a deny, else approved (a resolve is not told apart).
func derivedOutcome(res preStep) string {
	switch {
	case res.IsError && res.Content == deniedPrefix+"no decision":
		return outcomeNoDecision
	case res.IsError && strings.HasPrefix(res.Content, deniedPrefix+"expired"):
		return outcomeExpired
	case res.IsError && strings.HasPrefix(res.Content, deniedPrefix):
		return outcomeDenied
	}
	return outcomeApproved
}

// preStep is one pre-step tool result of a resume.
type preStep struct {
	Content string
	IsError bool
}

// preStepSet is a resume's pre-step results in call order.
type preStepSet struct {
	order []string
	by    map[string]preStep
}

// preStepResults reads a resume's pre-step tool results — the step-0
// output batches holding tool messages before the model's first
// assistant message (the decided calls, ADR 0007) — and every call's
// tool name the transcript names.
func preStepResults(batches []obsdb.TranscriptBatch) (preStepSet, map[string]string) {
	out := preStepSet{by: map[string]preStep{}}
	names := map[string]string{}
	type part struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		CallID  string `json:"call_id"`
		Name    string `json:"name"`
		Content string `json:"content"`
		IsError bool   `json:"is_error"`
	}
	type message struct {
		Role    string `json:"role"`
		Content []part `json:"content"`
	}
	done := false
	for _, b := range batches {
		var msgs []message
		if json.Unmarshal(b.Messages, &msgs) != nil {
			continue
		}
		for _, m := range msgs {
			for _, p := range m.Content {
				if p.Type == "tool_call" && p.ID != "" && p.Name != "" {
					names[p.ID] = p.Name
				}
			}
		}
		if b.Input || b.Step != 0 || done {
			continue
		}
		for _, m := range msgs {
			if m.Role != "tool" {
				done = true
				break
			}
			for _, p := range m.Content {
				if p.Type != "tool_result" || p.CallID == "" {
					continue
				}
				if _, seen := out.by[p.CallID]; !seen {
					out.order = append(out.order, p.CallID)
				}
				out.by[p.CallID] = preStep{Content: p.Content, IsError: p.IsError}
				if p.Name != "" {
					names[p.CallID] = p.Name
				}
			}
		}
	}
	return out, names
}

// rawDecision is one approval.decision record of a call, as thread
// filed it (record contract §4).
type rawDecision struct {
	outcome, via, keyID, grantID, who, reason string
	at                                        *time.Time
}

// threadDecision is a call's decision records folded as thread folds
// them (effectiveDecision, ADR 0021 §5): outcome the verdict's
// (approved, denied, resolved; "" while none resolves the call — a
// quorum not yet met included), who / keys the deciders the verdict
// stands on, in record order — each approver of a met quorum — and a
// split verdict via quorum with no reason of its own (the model saw
// thread's; the transcript's text names it), who its denier and
// approvers the approvals it overruled.
type threadDecision struct {
	outcome, via, grantID, reason string
	who, keys, approvers          []string
	at                            *time.Time
}

// foldDecisions folds one call's records in order under the session's
// quorum (the call's approval.request names it; < 2 is one): a deny
// resolves (beside an approve a split verdict, unless the session's own
// expiry or interrupt), one resolve resolves (beside an approve or
// another resolve a split verdict), approvals resolve together once
// quorum distinct approvers (thread's approverIdentity: the key, else
// the grant, else who) stand.
func foldDecisions(ds []rawDecision, quorum int) threadDecision {
	verdict := func(outcome string, by []rawDecision) threadDecision {
		last := by[len(by)-1]
		v := threadDecision{outcome: outcome, via: last.via, reason: last.reason, at: last.at}
		for _, d := range by {
			if d.who != "" && !slices.Contains(v.who, d.who) {
				v.who = append(v.who, d.who)
			}
			if d.keyID != "" && !slices.Contains(v.keys, d.keyID) {
				v.keys = append(v.keys, d.keyID)
			}
			if d.grantID != "" {
				v.grantID = d.grantID
			}
		}
		return v
	}
	// split is the verdict a conflict resolves to: a deny via quorum, its
	// decider the conflicting deny (none for a resolve's conflict), the
	// approvals beside it named as approvers — never as deniers.
	split := func(conflict rawDecision, approves []rawDecision) threadDecision {
		v := threadDecision{outcome: outcomeDenied, via: "quorum", at: conflict.at}
		if conflict.outcome == "deny" {
			v = verdict(outcomeDenied, []rawDecision{conflict})
			v.via, v.reason = "quorum", ""
		}
		for _, d := range approves {
			if d.who != "" && !slices.Contains(v.approvers, d.who) {
				v.approvers = append(v.approvers, d.who)
			}
		}
		return v
	}
	var approves []rawDecision
	approvers := map[string]bool{}
	resolved := -1
	for i, d := range ds {
		switch d.outcome {
		case "deny":
			if len(approves) > 0 && d.via != "expiry" && d.via != "interrupt" {
				return split(d, approves)
			}
			return verdict(outcomeDenied, []rawDecision{d})
		case "resolve", "resolve_error":
			if resolved >= 0 || len(approves) > 0 {
				return split(d, approves)
			}
			resolved = i
		case "approve":
			if resolved >= 0 {
				return split(d, append(approves, d))
			}
			approves = append(approves, d)
			switch {
			case d.keyID != "":
				approvers["key:"+d.keyID] = true
			case d.via == "grant":
				approvers["grant"] = true
			default:
				approvers["who:"+d.who] = true
			}
		}
	}
	switch {
	case resolved >= 0:
		return verdict(outcomeResolved, ds[resolved:resolved+1])
	case len(approves) > 0 && len(approvers) >= max(quorum, 1):
		v := verdict(outcomeApproved, approves)
		if quorum > 1 {
			v.via = "quorum" // the span's approved_by under a quorum
		}
		return v
	}
	return threadDecision{} // undecided, or short of its quorum: no names
}

// threadDecisionPages bounds the session-event pages one decisions read
// takes of the parked run's and the resume's events.
const threadDecisionPages = 5

// threadDecisions reads the approval.decision records thread filed under
// the parked run or the resume (record contract §4; the session index
// narrowed to those two runs), folded per call id. None for a run of no
// session, or a database without the session index. note says the
// records past the read's cap, when there are any.
func (s *Server) threadDecisions(ctx context.Context, parked, resume obsdb.RunRow) (map[string]threadDecision, string) {
	out := map[string]threadDecision{}
	er, ok := s.db.(obsdb.SessionEventReader)
	sid := parked.SessionID
	if sid == "" {
		sid = resume.SessionID
	}
	if !ok || sid == "" {
		return out, ""
	}
	runs := []string{resume.ID}
	if parked.ID != "" && parked.ID != resume.ID {
		runs = append(runs, parked.ID)
	}
	q := obsdb.SessionEventQuery{SessionID: sid, RunIDs: runs, Limit: obsdb.MaxSessionEventLimit}
	byCall := map[string][]rawDecision{}
	quorum := map[string]int{}
	var order []string
	note := ""
	for range threadDecisionPages {
		page, err := er.SessionEvents(ctx, q)
		if err != nil {
			note = "thread's decision records could not be read: " + err.Error()
			break
		}
		for _, e := range page.Events {
			kind := obsdb.SessionEventKind(e)
			if kind == "approval.request" {
				// The quorum the call's approvals must meet (thread names it
				// on the request when it is > 1).
				var req struct {
					CallID string `json:"call_id"`
					Quorum int    `json:"quorum"`
				}
				if json.Unmarshal(e.Body, &req) == nil && req.CallID != "" {
					quorum[req.CallID] = req.Quorum
				}
				continue
			}
			if kind != "approval.decision" {
				continue
			}
			var body struct {
				CallID  string `json:"call_id"`
				Outcome string `json:"outcome"`
				Via     string `json:"via"`
				KeyID   string `json:"key_id"`
				GrantID string `json:"grant_id"`
				At      string `json:"at"`
				Content struct {
					Who    string `json:"who"`
					Reason string `json:"reason"`
				} `json:"content"`
			}
			if json.Unmarshal(e.Body, &body) != nil || body.CallID == "" {
				continue
			}
			d := rawDecision{outcome: body.Outcome, via: body.Via, keyID: body.KeyID, grantID: body.GrantID,
				who: body.Content.Who, reason: body.Content.Reason}
			if t, err := time.Parse(time.RFC3339Nano, body.At); err == nil {
				t = t.UTC()
				d.at = &t
			}
			if _, seen := byCall[body.CallID]; !seen {
				order = append(order, body.CallID)
			}
			byCall[body.CallID] = append(byCall[body.CallID], d)
		}
		q.Offset += len(page.Events)
		if len(page.Events) == 0 || q.Offset >= page.Total {
			break
		}
		if q.Offset < page.Total && q.Offset >= threadDecisionPages*obsdb.MaxSessionEventLimit {
			note = fmt.Sprintf("thread's records past the first %d of these runs' %d were not read: a decision there is not shown", q.Offset, page.Total)
		}
	}
	for _, id := range order {
		out[id] = foldDecisions(byCall[id], quorum[id])
	}
	return out, note
}

// attrString reads a string attribute, "" when absent or not a string.
func attrString(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
}
