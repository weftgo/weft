package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
)

// The step route (plan A7, with A10's children and A4's attempts):
//
//	GET /api/runs/{id}/steps/{n}
//
// One step, assembled here from obsdb so a client folds nothing: the
// request it sent (attempt 1, prompt and catalog inline), every attempt
// with its model and outcome, the messages it carried (a count — the
// bytes stay on the transcript route), its events as the events route
// serves them, its tool calls with their spans and results, the child
// runs those calls started, its usage, timing, the compaction view it
// saw, and every hole that applies to it as a badge from ADR 0028 §11's
// closed table (obsdb.Hole) with a reason and a fix. n is the step
// ordinal (decision 8). Scoped like the events route; only the request
// block follows the requests route's rule — a read-scoped panel token
// gets {badge: "hidden", reason, fix} in its place, never a 403 on the
// whole step.

// The step statuses: ok (finished), error (the run failed or stopped
// reporting inside it), parked (finished with a call awaiting
// approval), running (started, not finished, the run alive).
// not_started is reserved for a step a run row counts but nothing
// records starting; the reader currently answers such a step with the
// run's outcome and a gap hole instead.
const (
	stepOK      = "ok"
	stepError   = "error"
	stepParked  = "parked"
	stepRunning = "running"
)

// stepEventsPage is the events page size the step walk reads with.
const stepEventsPage = 1000

// stepDoc is GET /api/runs/{id}/steps/{n}. Blocks a step may lack are
// omitted; when one is missing for a reason, that reason is a badge —
// on the block (request, attempts_badge, messages_in, compaction) and
// in holes, deduplicated, in ADR 0028 §11's order.
type stepDoc struct {
	RunID    string     `json:"run_id"`
	Step     int        `json:"step"`
	Status   string     `json:"status"`
	Reason   string     `json:"reason,omitempty"` // step_finish's stop reason
	Started  *time.Time `json:"started,omitempty"`
	Finished *time.Time `json:"finished,omitempty"`
	// LatencyMS and TTFTMS are step_finish's (ADR 0016's A4 note):
	// absent when not measured, never 0.
	LatencyMS int64     `json:"latency_ms,omitempty"`
	TTFTMS    int64     `json:"ttft_ms,omitempty"`
	Model     stepModel `json:"model"`
	// Request is the requests route's row for the step's attempt 1,
	// prompt and catalog inline (a requestRow), or the badge that says
	// why it is not here (badgeFields: hidden, not_recorded, gap).
	// Absent only for a step that made no model call.
	Request       any            `json:"request,omitempty"`
	Attempts      []stepAttempt  `json:"attempts"`
	AttemptsBadge *badgeFields   `json:"attempts_badge,omitempty"`
	MessagesIn    stepMessagesIn `json:"messages_in"`
	// Events are the step's durable events, pos ordered, from its
	// step_start up to the next step's (a steer delivered after the
	// step included); run_start and run_finish are the run's own.
	Events     []posEvent      `json:"events"`
	ToolCalls  []stepToolCall  `json:"tool_calls"`
	Children   []stepChild     `json:"children"`
	Usage      core.Usage      `json:"usage"`
	Compaction *stepCompaction `json:"compaction,omitempty"`
	Holes      []stepHole      `json:"holes"`
}

// stepModel is the model the step asked for and the one that answered
// (gen_ai.response.model, A4: the last successful attempt's model);
// answered is absent when the step did not finish or the run predates
// A4.
type stepModel struct {
	Provider  string `json:"provider,omitempty"`
	Requested string `json:"requested"`
	Answered  string `json:"answered,omitempty"`
}

// stepAttempt is one model-call attempt of the step: the request record
// of that attempt number joined to its attempt span (ADR 0028 §7) —
// or, when nothing reported attempts, attempt 1 joined to the step's
// chat span. Outcome is ok or error from the span's status; absent when
// no span says (attempts_badge then says why).
type stepAttempt struct {
	Attempt      int64      `json:"attempt"`
	Model        string     `json:"model"`
	Provider     string     `json:"provider,omitempty"`
	Outcome      string     `json:"outcome,omitempty"`
	ErrorType    string     `json:"error_type,omitempty"`
	RetryAfterMS int64      `json:"retry_after_ms,omitempty"`
	Started      *time.Time `json:"started,omitempty"`
	Finished     *time.Time `json:"finished,omitempty"`
	SpanID       string     `json:"span_id,omitempty"`
	RequestIndex *int64     `json:"request_index,omitempty"`
}

// stepMessagesIn is the request's messages_ref: the messages record
// index the request's view ends at and the number of messages it
// carried. A badge says when the index is not usable: stripped (a
// content-off chain removed it), compacted (it names the step's
// compaction view), not_recorded (no request record).
type stepMessagesIn struct {
	Index *int64 `json:"index,omitempty"`
	Count int    `json:"count"`
	badgeFields
}

// stepToolCall is one call the step's model made: its arguments, its
// result (absent while running or parked), its execute_tool span, and
// the child run it started (a Subagent). Pending is a parked call;
// Badge is "stripped" when content-off dropped the args and result.
type stepToolCall struct {
	CallID     string          `json:"call_id"`
	Name       string          `json:"name"`
	Seq        int64           `json:"seq,omitempty"`
	Args       json.RawMessage `json:"args"`
	Result     *stepToolResult `json:"result,omitempty"`
	Span       *stepSpan       `json:"span,omitempty"`
	ChildRunID string          `json:"child_run_id,omitempty"`
	Pending    bool            `json:"pending,omitempty"`
	Badge      string          `json:"badge,omitempty"`
}

// stepToolResult is a call's result as the model saw it. Bytes is the
// content's length (the span's weft.tool.result_bytes when the content
// was stripped); Truncated says the result cap cut it (the marker the
// model sees, core's capResult).
type stepToolResult struct {
	Content   string `json:"content"`
	IsError   bool   `json:"is_error"`
	Bytes     int64  `json:"bytes"`
	Truncated bool   `json:"truncated,omitempty"`
}

type stepSpan struct {
	ID       string    `json:"id"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	Status   string    `json:"status"`
}

// stepChild is a child run one of the step's calls started (ADR 0014's
// weft.parent.run.id / weft.parent.call.id). Cost arrives with A5.
type stepChild struct {
	ID     string     `json:"id"`
	CallID string     `json:"call_id"`
	Agent  string     `json:"agent"`
	Status string     `json:"status"`
	Usage  core.Usage `json:"usage"`
}

// stepCompaction is the run-scope compaction view the step's request
// carried (ADR 0028 §8, obsdb.Compactions): the transcript range
// [from_seq, to_seq) replaced by entries messages.
type stepCompaction struct {
	Scope    string `json:"scope"`
	Index    int64  `json:"index"`
	FromSeq  int64  `json:"from_seq"`
	ToSeq    int64  `json:"to_seq"`
	Hash     string `json:"hash"`
	Replaced int    `json:"replaced"`
	Entries  int    `json:"entries"`
	badgeFields
}

// stepHole is one hole of the step: the badge, why, and the fix.
type stepHole struct {
	Hole   string `json:"hole"`
	Reason string `json:"reason"`
	Fix    string `json:"fix,omitempty"`
}

// truncatedMarker is the marker core's capResult ends a cut tool
// result with.
var truncatedMarker = regexp.MustCompile(`\n…\[truncated \d+ bytes\]$`)

// holeSet collects a step's holes, first reason per badge, and lists
// them in ADR 0028 §11's order.
type holeSet map[obsdb.Hole]stepHole

func (hs holeSet) add(h obsdb.Hole, reason, fix string) {
	if _, ok := hs[h]; !ok {
		hs[h] = stepHole{Hole: string(h), Reason: reason, Fix: fix}
	}
}

func (hs holeSet) note(h obsdb.Hole) {
	n := holeNotes[h]
	hs.add(h, n.reason, n.fix)
}

func (hs holeSet) list() []stepHole {
	out := []stepHole{}
	for _, h := range obsdb.Holes() {
		if v, ok := hs[h]; ok {
			out = append(out, v)
		}
	}
	return out
}

// stepEvents is what the walk over the run's events found for step n.
type stepEvents struct {
	found        bool
	events       []obsdb.PosEvent
	start        *obsdb.PosEvent
	finish       *obsdb.PosEvent
	finishBody   core.StepFinish
	pendingCalls map[string]bool // run_finish's pending call ids, when the walk reached it
	gaps         []int64
}

// eventHead is the fields of a stored event body the step reads.
type eventHead struct {
	Type    string              `json:"type"`
	Index   int                 `json:"index"`
	Pending []core.ToolCallPart `json:"pending"`
	CallID  string              `json:"call_id"`
	Name    string              `json:"name"`
	Seq     int64               `json:"seq"`
	Args    json.RawMessage     `json:"args"`
	Content string              `json:"content"`
	IsError bool                `json:"is_error"`
}

// readStepEvents walks the run's events from the start, a page at a
// time, and keeps step n's: from its step_start to the next step's
// step_start (or run_finish). Step 0 also keeps what precedes its
// step_start after run_start — a resume's approved calls, which the
// transcript files under step 0 too. obsdb pages events by position
// only, so the walk reads every earlier step's events too — but stops
// at step n's end, never reading the rest of the run.
func (s *Server) readStepEvents(ctx context.Context, id string, n int) (stepEvents, error) {
	var out stepEvents
	after := int64(-1)
	var startPos, endPos int64 = -1, -1
	var allGaps []int64
	var before []obsdb.PosEvent // step 0's events ahead of its step_start
walk:
	for {
		page, err := s.db.Events(ctx, id, after, stepEventsPage)
		if err != nil {
			return out, err
		}
		allGaps = page.Gaps
		for i := range page.Events {
			pe := page.Events[i]
			var h eventHead
			_ = json.Unmarshal(pe.Event, &h)
			switch h.Type {
			case "step_start":
				if h.Index == n && !out.found {
					out.found, startPos = true, pe.Pos
					out.start = &pe
					out.events = append(out.events, before...)
					before = nil
				} else if out.found || h.Index > n {
					endPos = pe.Pos
					break walk
				}
			case "run_finish":
				out.pendingCalls = map[string]bool{}
				for _, c := range h.Pending {
					out.pendingCalls[c.ID] = true
				}
				if out.found {
					endPos = pe.Pos
				}
				break walk
			case "run_start":
				continue
			}
			if !out.found {
				if n == 0 {
					before = append(before, pe)
				}
				continue
			}
			out.events = append(out.events, pe)
			if h.Type == "step_finish" && h.Index == n {
				out.finish = &pe
				_ = json.Unmarshal(pe.Event, &out.finishBody)
			}
		}
		if page.NextAfter == nil {
			break
		}
		after = *page.NextAfter
	}
	if out.found {
		if len(out.events) > 0 {
			startPos = out.events[0].Pos
		}
		for _, g := range allGaps {
			if g > startPos && (endPos < 0 || g < endPos) {
				out.gaps = append(out.gaps, g)
			}
		}
	}
	return out, nil
}

// serveRunStep answers api/runs/{id}/steps/{n}.
func (s *Server) serveRunStep(w http.ResponseWriter, r *http.Request, id, item string) {
	if !s.scopeRunID(w, r, id) {
		return
	}
	n, err := strconv.Atoi(item)
	if err != nil || n < 0 || strconv.Itoa(n) != item {
		badRequest(w, r, "the step must be a non-negative integer ordinal")
		return
	}
	ctx := r.Context()
	det, err := s.db.Run(ctx, id)
	if err != nil {
		dbError(w, r, "run", id, err)
		return
	}
	evs, err := s.readStepEvents(ctx, id, n)
	if err != nil {
		dbError(w, r, "events of run", id, err)
		return
	}
	reqHole := det.RequestsHole()
	var reqs []obsdb.RequestRecord
	if reqHole == "" {
		if reqs, err = s.db.Requests(ctx, id, obsdb.RequestQuery{Step: &n, Limit: maxRequestsLimit}); err != nil {
			dbError(w, r, "requests of run", id, err)
			return
		}
	}
	// The step exists when something records it: its step_start, a
	// request record, or the run row's step count (a run whose events
	// were lost). A step past all three is 404, a running run's next
	// step included.
	if !evs.found && len(reqs) == 0 && n >= det.Steps {
		notFound(w, r, "no step "+item+" of run "+id)
		return
	}
	allSpans, err := s.db.RunSpans(ctx, id)
	if err != nil {
		dbError(w, r, "spans of run", id, err)
		return
	}
	comps, err := s.db.Compactions(ctx, id)
	if err != nil {
		dbError(w, r, "compactions of run", id, err)
		return
	}

	holes := holeSet{}
	doc := stepDoc{
		RunID: id, Step: n,
		Attempts: []stepAttempt{}, Events: []posEvent{}, ToolCalls: []stepToolCall{},
		Children: []stepChild{},
	}

	// Events, status, timing, usage.
	for _, pe := range evs.events {
		doc.Events = append(doc.Events, posEvent{Pos: pe.Pos, Time: pe.Time, Event: pe.Event})
	}
	if evs.start != nil {
		t := evs.start.Time
		doc.Started = &t
	}
	if evs.finish != nil {
		t := evs.finish.Time
		doc.Finished = &t
		doc.Reason = string(evs.finishBody.Reason)
		doc.LatencyMS, doc.TTFTMS = evs.finishBody.LatencyMS, evs.finishBody.TTFTMS
		doc.Usage = evs.finishBody.Usage
	}
	if len(evs.gaps) > 0 {
		holes.add(obsdb.HoleGap, "positions are missing from this step's event stream: a destination dropped a batch", holeNotes[obsdb.HoleGap].fix)
	}
	if doc.Reason == string(core.StopMaxTokens) {
		holes.add(obsdb.HoleMaxTokens, "the step finished on the output token limit: its tool calls were not executed", "raise max_tokens")
	}

	// The step's spans: its chat span, that span's attempt children,
	// and its execute_tool spans by call id. obsdb reads a run's spans
	// whole; the step's are picked out here.
	var chat *obsdb.Span
	var attemptSpans []obsdb.Span
	toolSpans := map[string]obsdb.Span{}
	for i := range allSpans {
		sp := allSpans[i]
		if spanStep(sp) != n {
			continue
		}
		switch sp.Attrs["gen_ai.operation.name"] {
		case "chat":
			if chat == nil {
				chat = &sp
			}
		case "execute_tool":
			if cid, _ := sp.Attrs["gen_ai.tool.call.id"].(string); cid != "" {
				toolSpans[cid] = sp
			}
		}
	}
	if chat != nil {
		for _, sp := range allSpans {
			if sp.ParentSpanID == chat.SpanID && sp.Attrs["weft.attempt.index"] != nil {
				attemptSpans = append(attemptSpans, sp)
			}
		}
	}
	if doc.Started == nil && chat != nil {
		t := chat.Start
		doc.Started = &t
	}

	// Tool calls, in the order the step's events name them.
	calls := map[string]*stepToolCall{}
	var order []string
	callOf := func(id, name string) *stepToolCall {
		if c, ok := calls[id]; ok {
			return c
		}
		c := &stepToolCall{CallID: id, Name: name, Args: json.RawMessage("null")}
		calls[id] = c
		order = append(order, id)
		return c
	}
	stripped := false
	for _, rec := range reqs {
		stripped = stripped || rec.Content == obsdb.HoleStripped
	}
	if stripped {
		holes.add(obsdb.HoleStripped, "this run's records were content-off: the step's system prompt, catalog, messages, tool arguments and results were dropped before they were stored", holeNotes[obsdb.HoleStripped].fix)
	}
	for _, pe := range evs.events {
		var h eventHead
		_ = json.Unmarshal(pe.Event, &h)
		switch h.Type {
		case "tool_start":
			c := callOf(h.CallID, h.Name)
			c.Seq = h.Seq
			if len(h.Args) > 0 {
				c.Args = h.Args
			}
		case "tool_finish":
			c := callOf(h.CallID, h.Name)
			c.Result = &stepToolResult{
				Content: h.Content, IsError: h.IsError, Bytes: int64(len(h.Content)),
				Truncated: truncatedMarker.MatchString(h.Content),
			}
		}
	}
	for _, cid := range order {
		c := calls[cid]
		if sp, ok := toolSpans[cid]; ok {
			c.Span = &stepSpan{ID: sp.SpanID, Started: sp.Start, Finished: sp.End, Status: spanStatus(sp.StatusCode)}
			if p, _ := sp.Attrs["weft.tool.pending"].(bool); p {
				c.Pending = true
			}
			if c.Result != nil && stripped {
				if b, ok := attrInt64(sp.Attrs["weft.tool.result_bytes"]); ok {
					c.Result.Bytes = b
				}
			}
		}
		if c.Result == nil && evs.pendingCalls[cid] {
			c.Pending = true
		}
		if stripped {
			c.Badge = string(obsdb.HoleStripped)
		}
		if c.Result != nil && c.Result.Truncated {
			holes.add(obsdb.HoleTruncated, "a tool result was cut by its result cap: the model saw a prefix and the marker", "raise the tool's weft.MaxResultBytes")
		}
	}

	// Children: the run's child runs that this step's calls started.
	prefix := id + "/" + item + "/"
	for _, kid := range det.Children {
		c, ok := calls[kid.ParentCallID]
		if !ok && !strings.HasPrefix(kid.ID, prefix) {
			continue
		}
		if ok && c.ChildRunID == "" {
			c.ChildRunID = kid.ID
		}
		doc.Children = append(doc.Children, stepChild{
			ID: kid.ID, CallID: kid.ParentCallID, Agent: kid.Agent, Status: string(kid.Status), Usage: kid.Usage,
		})
	}
	for _, cid := range order {
		doc.ToolCalls = append(doc.ToolCalls, *calls[cid])
	}

	// Status.
	switch {
	case evs.finish != nil:
		doc.Status = stepOK
		for _, c := range doc.ToolCalls {
			if c.Pending {
				doc.Status = stepParked
			}
		}
	case !evs.found:
		// Counted by the run row or named by a request, but no event
		// records the step: the run's outcome, and the gap said.
		holes.add(obsdb.HoleGap, "the run counts this step, but no event of it was stored: a destination dropped its records", holeNotes[obsdb.HoleGap].fix)
		doc.Status = stepOK
		if det.Status == obsdb.StatusFailed && n == det.Steps-1 {
			doc.Status = stepError
		}
	case det.Status == obsdb.StatusRunning:
		doc.Status = stepRunning
	case det.Status == obsdb.StatusInterrupted:
		doc.Status = stepError
		holes.add(obsdb.HoleInterrupted, "the run stopped reporting inside this step (last seen more than 30 s ago)", "")
	default:
		doc.Status = stepError
	}

	// The model: asked for (the request's, else the chat span's, else
	// the run's — derived), and the one that answered (A4).
	modelCalled := len(reqs) > 0 || chat != nil || evs.finish != nil
	var first *obsdb.RequestRecord
	for i := range reqs {
		if first == nil || reqs[i].Attempt == 1 {
			first = &reqs[i]
			if reqs[i].Attempt == 1 {
				break
			}
		}
	}
	switch {
	case first != nil && first.Body.Model.Name != "":
		doc.Model.Provider, doc.Model.Requested = first.Body.Model.Provider, first.Body.Model.Name
	case chat != nil:
		doc.Model.Provider, _ = chat.Attrs["gen_ai.provider.name"].(string)
		doc.Model.Requested, _ = chat.Attrs["gen_ai.request.model"].(string)
	default:
		doc.Model.Provider, doc.Model.Requested = det.Provider, det.Model
		if modelCalled || !evs.found {
			holes.add(obsdb.HoleDerived, "the step's requested model is the run's: no request record or chat span of the step names it", "")
		}
	}
	if chat != nil {
		doc.Model.Answered, _ = chat.Attrs["gen_ai.response.model"].(string)
	}

	// The request block: attempt 1, as the requests route serves it.
	switch {
	case !readsPrompts(r):
		doc.Request = badgeOf(obsdb.HoleHidden)
		holes.note(obsdb.HoleHidden)
	case reqHole != "":
		doc.Request = badgeOf(reqHole)
		holes.note(reqHole)
	case first != nil:
		res := resolver{ctx: ctx, db: s.db, run: id, prompts: map[string]any{}, catalogs: map[string]any{}}
		row := requestRow{
			Index: first.Index, Step: first.Step, Attempt: first.Attempt, Time: first.Time,
			SystemHash: first.SystemHash, CatalogHash: first.CatalogHash,
			Content: string(first.Content), TruncatedBytes: first.TruncatedBytes, Body: first.Body,
		}
		if row.Body.Tools.Names == nil {
			row.Body.Tools.Names = []string{}
		}
		if row.Prompt, err = res.prompt(first.SystemHash, stripped); err == nil {
			row.Tools, err = res.catalog(first.CatalogHash, stripped)
		}
		if err != nil {
			dbError(w, r, "requests of run", id, err)
			return
		}
		doc.Request = row
		if first.Content != "" {
			holes.note(first.Content)
		}
		for _, v := range []any{row.Prompt, row.Tools} {
			switch d := v.(type) {
			case holeRef:
				if d.Badge != string(obsdb.HoleStripped) {
					holes.note(obsdb.Hole(d.Badge))
				}
			case promptDoc:
				if d.Content == string(obsdb.HoleTruncated) {
					holes.add(obsdb.HoleTruncated, "the step's system prompt was cut by a destination's cap", "raise the destination's MaxBytes")
				}
			case catalogDoc:
				if d.Content == string(obsdb.HoleTruncated) {
					holes.add(obsdb.HoleTruncated, "the step's tool catalog was cut by a destination's cap", "raise the destination's MaxBytes")
				}
			}
		}
	case modelCalled:
		gap := badgeFields{Badge: string(obsdb.HoleGap), Reason: "the step called the model, but no request record of it was stored", Fix: holeNotes[obsdb.HoleGap].fix}
		doc.Request = gap
		holes.add(obsdb.HoleGap, gap.Reason, gap.Fix)
	}

	// Attempts: request records joined to attempt spans on the attempt
	// number (ADR 0028 §7); without attempt spans, attempt 1 joins the
	// chat span.
	byAttempt := map[int64]*stepAttempt{}
	att := func(n int64) *stepAttempt {
		if a, ok := byAttempt[n]; ok {
			return a
		}
		a := &stepAttempt{Attempt: n}
		byAttempt[n] = a
		return a
	}
	for _, rec := range reqs {
		if rec.Attempt <= 0 {
			continue
		}
		a := att(rec.Attempt)
		idx := rec.Index
		a.RequestIndex = &idx
		a.Model, a.Provider = rec.Body.Model.Name, rec.Body.Model.Provider
	}
	fromSpan := func(a *stepAttempt, sp obsdb.Span) {
		if m, _ := sp.Attrs["gen_ai.request.model"].(string); m != "" {
			a.Model = m
		}
		if p, _ := sp.Attrs["gen_ai.provider.name"].(string); p != "" {
			a.Provider = p
		}
		switch sp.StatusCode {
		case 1:
			a.Outcome = "ok"
		case 2:
			a.Outcome = "error"
		}
		a.ErrorType, _ = sp.Attrs["error.type"].(string)
		if ms, ok := attrInt64(sp.Attrs["weft.attempt.retry_after_ms"]); ok {
			a.RetryAfterMS = ms
		}
		st, en := sp.Start, sp.End
		if !st.IsZero() {
			a.Started = &st
		}
		if !en.IsZero() {
			a.Finished = &en
		}
		a.SpanID = sp.SpanID
	}
	for _, sp := range attemptSpans {
		if k, ok := attrInt64(sp.Attrs["weft.attempt.index"]); ok && k > 0 {
			fromSpan(att(k), sp)
		}
	}
	if len(attemptSpans) == 0 && chat != nil {
		fromSpan(att(1), *chat)
	}
	keys := make([]int64, 0, len(byAttempt))
	for k := range byAttempt {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		a := byAttempt[k]
		if a.Model == "" {
			a.Model = doc.Model.Requested
		}
		doc.Attempts = append(doc.Attempts, *a)
	}
	var attBadge obsdb.Hole
	var attReason string
	switch {
	case chat == nil && len(allSpans) == 0 && (modelCalled || !evs.found):
		attBadge, attReason = obsdb.HoleNotRecorded, "the run has no spans: it was recorded without a tracer, or by a weft without attempt reporting (A4), so no attempt's outcome or timing exists"
	case chat == nil && modelCalled:
		attBadge, attReason = obsdb.HoleGap, "the step called the model, but its chat span was not stored: a destination dropped it"
	case chat != nil && len(attemptSpans) == 0 && chat.Attrs["weft.stream"] == nil:
		attBadge, attReason = obsdb.HoleNotRecorded, "the step's chat span has no attempt spans and no A4 timing: it was recorded by a weft before attempt reporting"
	}
	if attBadge != "" {
		fix := holeNotes[attBadge].fix
		doc.AttemptsBadge = &badgeFields{Badge: string(attBadge), Reason: attReason, Fix: fix}
		holes.add(attBadge, attReason, fix)
	}

	// The compaction view the step's request carried.
	for _, c := range comps {
		if c.Scope != obsdb.CompactionRun || c.Step != n {
			continue
		}
		note := badgeFields{Badge: string(obsdb.HoleCompacted),
			Reason: "the model saw a compacted view: a PrepareStep replaced part of the transcript for this step's request",
			Fix:    "see the compaction block: the transcript range it replaced and the messages that stood in"}
		doc.Compaction = &stepCompaction{
			Scope: c.Scope, Index: c.Index, FromSeq: c.FromSeq, ToSeq: c.ToSeq, Hash: c.Hash,
			Replaced: c.Replaced, Entries: c.Entries, badgeFields: note,
		}
		holes.add(obsdb.HoleCompacted, note.Reason, note.Fix)
		break
	}

	// Messages in: the request's messages_ref, resolved to a count.
	switch {
	case reqHole != "":
		doc.MessagesIn.badgeFields = badgeOf(reqHole)
	case first == nil:
		if modelCalled {
			doc.MessagesIn.badgeFields = badgeFields{Badge: string(obsdb.HoleGap), Reason: "no request record of the step was stored", Fix: holeNotes[obsdb.HoleGap].fix}
		}
	default:
		ref := first.Body.MessagesRef
		doc.MessagesIn.Count, doc.MessagesIn.Index = ref.Count, ref.Index
		switch {
		case first.Content == obsdb.HoleStripped:
			doc.MessagesIn.badgeFields = badgeFields{Badge: string(obsdb.HoleStripped),
				Reason: "a content-off chain removes messages_ref.index: the count is kept, the messages are not stored",
				Fix:    holeNotes[obsdb.HoleStripped].fix}
		case doc.Compaction != nil && ref.Index != nil && *ref.Index == doc.Compaction.Index:
			doc.MessagesIn.badgeFields = doc.Compaction.badgeFields
		}
	}
	doc.Holes = holes.list()
	writeJSON(w, r, http.StatusOK, doc)
}

// spanStep is a span's weft.step.index, -1 when it has none.
func spanStep(sp obsdb.Span) int {
	if n, ok := attrInt64(sp.Attrs["weft.step.index"]); ok {
		return int(n)
	}
	return -1
}

// attrInt64 reads a numeric span attribute however the backend stored
// it (int64, float64, or a numeric string).
func attrInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case int:
		return int64(x), true
	case float64:
		return int64(x), true
	case string:
		n, err := strconv.ParseInt(x, 10, 64)
		return n, err == nil
	}
	return 0, false
}
