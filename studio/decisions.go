package studio

import (
	"context"
	"encoding/json"
	"net/http"
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
//     "transcript": a resolve then reads as approved);
//   - the reason of a denial: thread's approval.decision content.reason,
//     else the resume's DENIED result text after "DENIED: " (both under
//     the content policy: absent content-off, reason_badge not_recorded);
//   - who / via / key / grant / when: thread's approval.decision record
//     filed under the parked run (via, key_id, grant_id, the body's at;
//     who under content), else the resumed call's execute_tool span
//     (weft.tool.approved_by, weft.grant.id; a denied call's zero-length
//     span's start); the resume's weft.approval.actor (metadata) beside;
//   - expired: a deny thread decided via expiry, or a DENIED result
//     reading "expired: …".
//
// A read-scoped panel token gets no who and no reason (reason_badge
// hidden: readsThreadContent's rule). A read that fails is said
// (error), never an empty list.

// decisionsCap bounds the calls one decisions block lists (core's
// weft.resume.calls cap; past it More counts the rest).
const decisionsCap = 32

// decisionDoc is the decisions one resume applied to one parked run.
// Run is the other side: on the parked run's `decided` the resume, on
// the resume's `resumes` the parked run. At is the resume's start (the
// moment it applied the decisions); Actor its weft.approval.actor
// (metadata, never content). Source says where the calls come from:
// resume_calls (weft.resume.calls) or transcript (derived from the
// resume's pre-step results — older records).
type decisionDoc struct {
	Run    string        `json:"run"`
	At     time.Time     `json:"at"`
	Actor  string        `json:"actor,omitempty"`
	Source string        `json:"source"`
	Calls  []decidedCall `json:"calls"`
	More   int           `json:"more,omitempty"`
	Error  string        `json:"error,omitempty"`
}

// decidedCall is one parked call's decision. Outcome is approved,
// denied, resolved, expired or no_decision. Via is thread's via (user,
// signed, grant, approver, quorum, expiry, …), else the span's
// approved_by. Who is thread's content.who; Reason a denial's reason;
// ReasonBadge why a denial has none (hidden, not_recorded). At is the
// decision's own time when a record names one.
type decidedCall struct {
	CallID      string     `json:"call_id"`
	Name        string     `json:"name,omitempty"`
	Outcome     string     `json:"outcome"`
	Via         string     `json:"via,omitempty"`
	KeyID       string     `json:"key_id,omitempty"`
	GrantID     string     `json:"grant_id,omitempty"`
	Who         string     `json:"who,omitempty"`
	WhoHidden   bool       `json:"who_hidden,omitempty"`
	Reason      string     `json:"reason,omitempty"`
	ReasonBadge string     `json:"reason_badge,omitempty"`
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
	pub := panelPublicID(r)
	if rec.ResumedBy != "" && rec.Pending > 0 {
		if det, err := s.db.Run(ctx, rec.ResumedBy); err != nil {
			page.Decided = &decisionDoc{Run: rec.ResumedBy, Calls: []decidedCall{}, Error: err.Error()}
		} else if inScope(pub, det.RunRow) {
			d := s.decisionsOf(ctx, r, rec, det.RunRow)
			d.Run = det.ID
			page.Decided = &d
		}
	}
	if rec.ResumedFrom != "" {
		parked := obsdb.RunRow{ID: rec.ResumedFrom}
		if det, err := s.db.Run(ctx, rec.ResumedFrom); err == nil {
			if !inScope(pub, det.RunRow) {
				return
			}
			parked = det.RunRow
		}
		d := s.decisionsOf(ctx, r, parked, rec)
		d.Run = rec.ResumedFrom
		page.Resumes = &d
	}
}

// decisionsOf composes what resume applied to parked's calls.
func (s *Server) decisionsOf(ctx context.Context, r *http.Request, parked, resume obsdb.RunRow) decisionDoc {
	out := decisionDoc{At: resume.Started, Actor: resume.Meta["weft.approval.actor"], Calls: []decidedCall{}}
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
	for _, sp := range spans {
		if attrString(sp.Attrs, "weft.run.id") != "" && attrString(sp.Attrs, "weft.run.id") != resume.ID {
			continue
		}
		switch {
		case strings.HasPrefix(sp.Name, "invoke_agent"):
			if v := attrString(sp.Attrs, "weft.resume.calls"); v != "" {
				calls = v
			}
		case strings.HasPrefix(sp.Name, "execute_tool"):
			if id := attrString(sp.Attrs, "gen_ai.tool.call.id"); id != "" {
				if _, seen := byCall[id]; !seen && spanStep(sp) == 0 {
					byCall[id] = sp
				}
				if names[id] == "" {
					names[id] = attrString(sp.Attrs, "gen_ai.tool.name")
				}
			}
		}
	}
	if calls != "" {
		out.Source = "resume_calls"
		for _, kv := range strings.Split(calls, ",") {
			if rest, ok := strings.CutPrefix(kv, "…+"); ok {
				out.More, _ = strconv.Atoi(rest)
				continue
			}
			id, kind, _ := strings.Cut(kv, "=")
			out.Calls = append(out.Calls, decidedCall{CallID: id, Outcome: kindOutcome(kind)})
		}
	} else {
		out.Source = "transcript"
		for _, res := range results.order {
			out.Calls = append(out.Calls, decidedCall{CallID: res, Outcome: derivedOutcome(results.by[res])})
		}
	}
	if len(out.Calls) > decisionsCap {
		out.More += len(out.Calls) - decisionsCap
		out.Calls = out.Calls[:decisionsCap]
	}
	recs := s.threadDecisions(ctx, parked)
	for i := range out.Calls {
		c := &out.Calls[i]
		c.Name = names[c.CallID]
		res, hasRes := results.by[c.CallID]
		if sp, ok := byCall[c.CallID]; ok {
			c.Via = attrString(sp.Attrs, "weft.tool.approved_by")
			c.GrantID = attrString(sp.Attrs, "weft.grant.id")
			if v, _ := sp.Attrs["weft.tool.denied"].(bool); v && !sp.Start.IsZero() {
				at := sp.Start.UTC()
				c.At = &at
			}
		}
		if hasRes && c.Outcome == outcomeDenied && strings.HasPrefix(res.Content, deniedPrefix+"expired") {
			c.Outcome = outcomeExpired
		}
		var reason string
		if rec, ok := recs[c.CallID]; ok {
			if rec.via != "" {
				c.Via = rec.via
			}
			if rec.keyID != "" {
				c.KeyID = rec.keyID
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
			if rec.who != "" {
				if reads {
					c.Who = rec.who
				} else {
					c.WhoHidden = true
				}
			}
			reason = rec.reason
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

// threadDecision is one approval.decision thread filed under a parked run.
type threadDecision struct {
	via, keyID, grantID, who, reason string
	at                               *time.Time
}

// threadDecisionPages bounds the session-event pages one decisions read
// scans for a parked turn's approval.decision records.
const threadDecisionPages = 5

// threadDecisions reads the approval.decision records thread filed under
// the parked run (record contract §4), by call id — the last one of a
// call wins (a quorum's final verdict). None for a run of no session, or
// a database without the session index.
func (s *Server) threadDecisions(ctx context.Context, parked obsdb.RunRow) map[string]threadDecision {
	out := map[string]threadDecision{}
	er, ok := s.db.(obsdb.SessionEventReader)
	if !ok || parked.SessionID == "" {
		return out
	}
	q := obsdb.SessionEventQuery{SessionID: parked.SessionID, Limit: obsdb.MaxSessionEventLimit}
	for range threadDecisionPages {
		page, err := er.SessionEvents(ctx, q)
		if err != nil {
			return out
		}
		for _, e := range page.Events {
			if e.RunID != parked.ID || obsdb.SessionEventKind(e) != "approval.decision" {
				continue
			}
			var body struct {
				CallID  string `json:"call_id"`
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
			d := threadDecision{via: body.Via, keyID: body.KeyID, grantID: body.GrantID,
				who: body.Content.Who, reason: body.Content.Reason}
			if t, err := time.Parse(time.RFC3339Nano, body.At); err == nil {
				t = t.UTC()
				d.at = &t
			}
			out[body.CallID] = d
		}
		q.Offset += len(page.Events)
		if len(page.Events) == 0 || q.Offset >= page.Total {
			break
		}
	}
	return out
}

// attrString reads a string attribute, "" when absent or not a string.
func attrString(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
}
