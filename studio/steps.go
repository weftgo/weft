package studio

import (
	"context"
	"encoding/json"
	"errors"
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

// maxStepCountedCalls bounds the anonymous calls a content-off
// max_tokens step lists from its chat span's weft.model.tool_calls (a
// count, not a list); a larger count is listed this far, badged derived.
const maxStepCountedCalls = maxRequestsLimit

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

	// For the diff route, never served here: attempt 1's system hash
	// (whoever reads) and its system text when the request block
	// carries it ("" for a request with no system text; nil when the
	// block is a badge or the prompt record is missing).
	sysHash string
	sysText *string
	// eventsLost: the step's events are not all stored (its step_start
	// never arrived, or positions inside it are missing once the run
	// stopped), so its calls, results and usage may be incomplete.
	eventsLost bool
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
	// Badge is "derived" on a request record with no attempt number:
	// the step's only record joins the first attempt the spans time;
	// beside others it is listed by its request index (Attempt 0).

	Badge string `json:"badge,omitempty"`
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
// the child run it started (a Subagent). Pending is a parked call.
// Badge is "max_tokens" on a call of a step cut at the output token
// limit (never executed: read from the step's transcript, its result
// the "not executed" text, no span), "stripped" when content-off
// dropped the args and result — on such a max_tokens step the calls
// are unknown beyond their count (the chat span's
// weft.model.tool_calls), listed with call_id "". With neither content
// nor a tracer, the calls are not listed; the max_tokens hole says the
// step made some.
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
// weft.parent.run.id / weft.parent.call.id). The plan's cost field is
// omitted until A5 adds costs: it is absent, not zero.
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

// also adds a second cause to a badge already named: its reason and
// fix are appended ("; ") after the first one's, never in its place.
func (hs holeSet) also(h obsdb.Hole, reason, fix string) {
	cur, ok := hs[h]
	if !ok {
		hs.add(h, reason, fix)
		return
	}
	if !strings.Contains(cur.Reason, reason) {
		cur.Reason += "; " + reason
	}
	if fix != "" && !strings.Contains(cur.Fix, fix) {
		if cur.Fix != "" {
			cur.Fix += "; "
		}
		cur.Fix += fix
	}
	hs[h] = cur
}

func (hs holeSet) note(h obsdb.Hole) {
	reason, fix := obsdb.HoleNote(h)
	hs.add(h, reason, fix)
}

// list is the holes in ADR 0028 §11's order; a hole never goes out
// without a reason.
func (hs holeSet) list() []stepHole {
	out := []stepHole{}
	for _, h := range obsdb.Holes() {
		if v, ok := hs[h]; ok {
			if v.Reason == "" {
				reason, fix := obsdb.HoleNote(h)
				v.Reason = reason
				if v.Fix == "" {
					v.Fix = fix
				}
			}
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
	Step    int                 `json:"step"`
	Pending []core.ToolCallPart `json:"pending"`
	CallID  string              `json:"call_id"`
	Name    string              `json:"name"`
	Seq     int64               `json:"seq"`
	Args    json.RawMessage     `json:"args"`
	Content string              `json:"content"`
	IsError bool                `json:"is_error"`
}

// stepRun is what assembling any step of one run reads once: the run
// row, its spans and compactions, and — as a walk needs them — its
// event pages and transcript batches. The step route assembles one
// step from it; the diff route assembles every step of two runs.
type stepRun struct {
	ctx     context.Context
	db      obsdb.DB
	id      string
	det     obsdb.RunDetail
	spans   []obsdb.Span
	comps   []obsdb.Compaction
	pages   []obsdb.EventPage
	batches []obsdb.TranscriptBatch
	batched bool
	// resume is where the last walk stopped: the first event that was
	// not its step's. A walk for the next step starts there, never at
	// page 0 — every event before it belongs to a step it has passed.
	resume *walkPoint
}

// walkPoint is a position in the cached event pages: event i of page
// pi, where a walk for step n begins.
type walkPoint struct{ n, pi, i int }

// stepReadError is a failed database read of the step assembly: noun
// names it for dbError ("run", "events of run", …).
type stepReadError struct {
	noun string
	err  error
}

func (e *stepReadError) Error() string { return "read " + e.noun + ": " + e.err.Error() }
func (e *stepReadError) Unwrap() error { return e.err }

// errNoStep is assembleStep's answer for a step nothing records.
var errNoStep = errors.New("no such step")

// loadStepRun reads the run's row, spans and compactions.
func (s *Server) loadStepRun(ctx context.Context, id string) (*stepRun, error) {
	sr := &stepRun{ctx: ctx, db: s.db, id: id}
	var err error
	if sr.det, err = s.db.Run(ctx, id); err != nil {
		return nil, &stepReadError{"run", err}
	}
	if sr.spans, err = s.db.RunSpans(ctx, id); err != nil {
		return nil, &stepReadError{"spans of run", err}
	}
	if sr.comps, err = s.db.Compactions(ctx, id); err != nil {
		return nil, &stepReadError{"compactions of run", err}
	}
	return sr, nil
}

// eventPage is the run's i-th events page of stepEventsPage events,
// read once.
func (sr *stepRun) eventPage(i int) (obsdb.EventPage, error) {
	for len(sr.pages) <= i {
		after := int64(-1)
		if k := len(sr.pages); k > 0 {
			if sr.pages[k-1].NextAfter == nil {
				return sr.pages[k-1], nil
			}
			after = *sr.pages[k-1].NextAfter
		}
		page, err := sr.db.Events(sr.ctx, sr.id, after, stepEventsPage)
		if err != nil {
			return obsdb.EventPage{}, &stepReadError{"events of run", err}
		}
		sr.pages = append(sr.pages, page)
	}
	return sr.pages[i], nil
}

// transcript is the run's messages batches, read once.
func (sr *stepRun) transcript() ([]obsdb.TranscriptBatch, error) {
	if !sr.batched {
		b, err := sr.db.TranscriptBatches(sr.ctx, sr.id)
		if err != nil {
			return nil, &stepReadError{"transcript of run", err}
		}
		sr.batches, sr.batched = b, true
	}
	return sr.batches, nil
}

// writeStepError answers a failed assembly: 404 for a step nothing records,
// dbError's mapping for a failed read.
func writeStepError(w http.ResponseWriter, r *http.Request, id string, n int, err error) {
	var re *stepReadError
	switch {
	case errors.Is(err, errNoStep):
		notFound(w, r, "no step "+strconv.Itoa(n)+" of run "+id)
	case errors.As(err, &re):
		dbError(w, r, re.noun, id, re.err)
	default:
		dbError(w, r, "run", id, err)
	}
}

// readStepEvents walks the run's events from the start, a page at a
// time, and keeps step n's: from its step_start to the first event that
// is not step n's — one naming another step (step_start, step_finish
// and steered carry the step, as does their record's weft.step.index),
// anything but a steer of step n once step n's step_finish passed, or
// run_finish. So a lost step_start never folds the next step's events
// into this one; the lost position is the gap hole. Step 0 also keeps
// what precedes its step_start after run_start — a resume's approved
// calls, which the transcript files under step 0 too. obsdb pages
// events by position only, so the walk reads every earlier step's
// events too — but stops at step n's end, never reading the rest of
// the run. The pages read are kept on sr, so a later step's walk (the
// diff route's, step after step) re-reads none of them.
func (sr *stepRun) readStepEvents(n int) (stepEvents, error) {
	var out stepEvents
	var startPos, endPos int64 = -1, -1
	var allGaps []int64
	var before []obsdb.PosEvent // step 0's events ahead of its step_start
	startPi, startI := 0, 0
	if r := sr.resume; r != nil && r.n == n && n > 0 {
		startPi, startI = r.pi, r.i
	}
	stopPi, stopI := 0, 0
	defer func() { sr.resume = &walkPoint{n: n + 1, pi: stopPi, i: stopI} }()
walk:
	for pi := startPi; ; pi++ {
		page, err := sr.eventPage(pi)
		if err != nil {
			return out, err
		}
		allGaps = page.Gaps
		first := 0
		if pi == startPi {
			first = startI
		}
		stopPi, stopI = pi, len(page.Events)
		for i := first; i < len(page.Events); i++ {
			stopI = i
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
				if h.Type == "step_finish" && h.Index > n {
					break walk // past step n without its step_start
				}
				if n == 0 {
					before = append(before, pe)
				}
				continue
			}
			otherStep := (h.Type == "step_finish" && h.Index != n) || (h.Type == "steered" && h.Step != n)
			steerOfN := h.Type == "steered" && h.Step == n
			if otherStep || (out.finish != nil && !steerOfN) {
				endPos = pe.Pos
				break walk
			}
			out.events = append(out.events, pe)
			if h.Type == "step_finish" && h.Index == n {
				out.finish = &pe
				_ = json.Unmarshal(pe.Event, &out.finishBody)
			}
		}
		stopI = len(page.Events)
		if page.NextAfter == nil {
			break
		}
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
	sr, err := s.loadStepRun(r.Context(), id)
	if err != nil {
		writeStepError(w, r, id, n, err)
		return
	}
	doc, err := sr.assembleStep(n, readsPrompts(r))
	if err != nil {
		writeStepError(w, r, id, n, err)
		return
	}
	writeJSON(w, r, http.StatusOK, doc)
}

// assembleStep is step n of sr's run as the step route serves it;
// prompts is readsPrompts' answer for the identity (false: the request
// block is the hidden badge). errNoStep for a step nothing records; a
// *stepReadError for a failed read.
func (sr *stepRun) assembleStep(n int, prompts bool) (stepDoc, error) {
	ctx, id, det, allSpans, comps := sr.ctx, sr.id, sr.det, sr.spans, sr.comps
	item := strconv.Itoa(n)
	evs, err := sr.readStepEvents(n)
	if err != nil {
		return stepDoc{}, err
	}
	reqHole := det.RequestsHole()
	var reqs []obsdb.RequestRecord
	if reqHole == "" {
		if reqs, err = sr.db.Requests(ctx, id, obsdb.RequestQuery{Step: &n, Limit: maxRequestsLimit}); err != nil {
			return stepDoc{}, &stepReadError{"requests of run", err}
		}
	}
	// The step exists when something records it: its step_start, a
	// request record, or the run row's step count (a run whose events
	// were lost). A step past all three is 404, a running run's next
	// step included.
	if !evs.found && len(reqs) == 0 && n >= det.Steps {
		return stepDoc{}, errNoStep
	}

	holes := holeSet{}
	doc := stepDoc{
		RunID: id, Step: n,
		Attempts: []stepAttempt{}, Events: []posEvent{}, ToolCalls: []stepToolCall{},
		Children: []stepChild{},
	}

	doc.eventsLost = !evs.found || (len(evs.gaps) > 0 && det.Status != obsdb.StatusRunning)
	// Events, status, timing, usage.
	for _, pe := range evs.events {
		doc.Events = append(doc.Events, posEventOf(pe))
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
	// A running run's missing positions may still be in flight (runHoles'
	// rule): a gap only once it stopped.
	if len(evs.gaps) > 0 && det.Status != obsdb.StatusRunning {
		holes.add(obsdb.HoleGap, "positions are missing from this step's event stream: a destination dropped a batch", holeFix(obsdb.HoleGap))
	}
	if doc.Reason == string(core.StopMaxTokens) {
		holes.note(obsdb.HoleMaxTokens)
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
		holes.add(obsdb.HoleStripped, "this run's records were content-off: the step's system prompt, catalog, messages, tool arguments and results were dropped before they were stored", holeFix(obsdb.HoleStripped))
	}
	// A call's own events say whether its content was dropped
	// (weft.content stripped or none) — a run written before the request
	// record has no request records to say it for the step.
	callStripped := map[string]bool{}
	for _, pe := range evs.events {
		var h eventHead
		_ = json.Unmarshal(pe.Event, &h)
		if (h.Type == "tool_start" || h.Type == "tool_finish") && (pe.Content == "stripped" || pe.Content == "none") {
			callStripped[h.CallID] = true
		}
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
	// A max_tokens step's calls never ran (rule 11): no tool events, no
	// spans — the step's transcript batches hold the calls and their
	// "not executed" results.
	maxTokens := doc.Reason == string(core.StopMaxTokens)
	// The run's messages batches, read once when a block needs them (a
	// max_tokens step's calls, a messages_ref to check).
	var batches []obsdb.TranscriptBatch
	if maxTokens {
		if batches, err = sr.transcript(); err != nil {
			return stepDoc{}, err
		}
		for _, b := range batches {
			if b.Step != n || b.Input {
				continue
			}
			var msgs []struct {
				Role    string `json:"role"`
				Content []struct {
					Type    string          `json:"type"`
					ID      string          `json:"id"`
					CallID  string          `json:"call_id"`
					Name    string          `json:"name"`
					Args    json.RawMessage `json:"args"`
					Content string          `json:"content"`
					IsError bool            `json:"is_error"`
				} `json:"content"`
			}
			_ = json.Unmarshal(b.Messages, &msgs)
			for _, m := range msgs {
				for _, part := range m.Content {
					switch {
					case m.Role == "assistant" && part.Type == "tool_call":
						c := callOf(part.ID, part.Name)
						if len(part.Args) > 0 {
							c.Args = part.Args
						}
					case m.Role == "tool" && part.Type == "tool_result":
						if c, ok := calls[part.CallID]; ok && c.Result == nil {
							c.Result = &stepToolResult{Content: part.Content, IsError: part.IsError, Bytes: int64(len(part.Content))}
						}
					}
				}
			}
		}
	}
	if maxTokens && len(order) == 0 && chat != nil {
		// Content-off: no messages were stored, so the calls' ids,
		// names and arguments are unknown; the chat span counts them.
		// One entry per call, stripped, so the step never reads as one
		// that made none.
		// The count is a span attribute, not a list: bounded, so a
		// hostile value cannot size the response.
		k, _ := attrInt64(chat.Attrs["weft.model.tool_calls"])
		if k > maxStepCountedCalls {
			holes.also(obsdb.HoleDerived, "the chat span counts "+strconv.FormatInt(k, 10)+" tool calls: the step lists the first "+
				strconv.Itoa(maxStepCountedCalls), "")
			k = maxStepCountedCalls
		}
		for i := range k {
			key := "#" + strconv.FormatInt(i, 10)
			c := callOf(key, "")
			c.CallID = ""
		}
	}
	for _, cid := range order {
		c := calls[cid]
		if sp, ok := toolSpans[cid]; ok {
			c.Span = &stepSpan{ID: sp.SpanID, Started: sp.Start, Finished: sp.End, Status: spanStatus(sp.StatusCode)}
			if p, _ := sp.Attrs["weft.tool.pending"].(bool); p {
				c.Pending = true
			}
			if c.Result != nil && (stripped || callStripped[cid]) {
				if b, ok := attrInt64(sp.Attrs["weft.tool.result_bytes"]); ok {
					c.Result.Bytes = b
				}
			}
		}
		if c.Result == nil && evs.pendingCalls[cid] {
			c.Pending = true
		}
		switch {
		case stripped || callStripped[cid]:
			c.Badge = string(obsdb.HoleStripped)
			holes.note(obsdb.HoleStripped)
		case maxTokens && c.Span == nil:
			c.Badge = string(obsdb.HoleMaxTokens)
		}
		if c.Result != nil && c.Result.Truncated {
			reason, fix := obsdb.HoleNoteFor(obsdb.HoleTruncated, obsdb.CauseResultCap)
			holes.add(obsdb.HoleTruncated, reason, fix)
		}
	}

	// Children: the run's child runs that this step's calls started. A
	// child named <run>/<step>/<call> (the core's childRunID) is this
	// step's by its id alone — a call id may repeat across steps; only a
	// child named otherwise joins on its parent call id.
	prefix := id + "/" + item + "/"
	for _, kid := range det.Children {
		var c *stepToolCall
		ok := false
		switch {
		case strings.HasPrefix(kid.ID, prefix):
			c, ok = calls[kid.ParentCallID]
		case strings.HasPrefix(kid.ID, id+"/"):
			continue // another step's
		default:
			if c, ok = calls[kid.ParentCallID]; !ok {
				continue
			}
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
	case !evs.found && det.Status == obsdb.StatusRunning:
		// Named by a request record of a running run whose step_start
		// is not stored yet: in flight, not lost.
		doc.Status = stepRunning
	case !evs.found:
		// Counted by the run row or named by a request, but no event
		// records the step: the run's outcome, and the gap said.
		holes.add(obsdb.HoleGap, "the run counts this step, but no event of it was stored: a destination dropped its records", holeFix(obsdb.HoleGap))
		doc.Status = stepOK
		if det.Status == obsdb.StatusFailed && n == det.Steps-1 {
			doc.Status = stepError
		}

	case det.Status == obsdb.StatusRunning:
		doc.Status = stepRunning
	case det.Status == obsdb.StatusInterrupted:
		doc.Status = stepError
		holes.note(obsdb.HoleInterrupted)
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
			holes.add(obsdb.HoleDerived, "the step's requested model is the run's: no request record or chat span of the step names it", holeFix(obsdb.HoleDerived))
		}
	}
	if chat != nil {
		doc.Model.Answered, _ = chat.Attrs["gen_ai.response.model"].(string)
	}

	if first != nil {
		doc.sysHash = first.SystemHash
	}
	// The request block: attempt 1, as the requests route serves it.
	switch {
	case !prompts:
		doc.Request = badgeOf(obsdb.HoleHidden)
		holes.note(obsdb.HoleHidden)
	case reqHole != "":
		doc.Request = badgeOf(reqHole)
		holes.note(reqHole)
	case first != nil:
		res := resolver{ctx: ctx, db: sr.db, run: id, prompts: map[string]any{}, catalogs: map[string]any{}}
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
			return stepDoc{}, &stepReadError{"requests of run", err}
		}
		doc.Request = row
		if d, ok := row.Prompt.(promptDoc); ok {
			doc.sysText = &d.Text
		} else if first.SystemHash == "" {
			doc.sysText = new(string)
		}
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
					holes.add(obsdb.HoleTruncated, "the step's system prompt was cut by a destination's cap", holeFix(obsdb.HoleTruncated))
				}
			case catalogDoc:
				if d.Content == string(obsdb.HoleTruncated) {
					holes.add(obsdb.HoleTruncated, "the step's tool catalog was cut by a destination's cap", holeFix(obsdb.HoleTruncated))
				}
			}
		}
	case modelCalled:
		gap := badgeFields{Badge: string(obsdb.HoleGap), Reason: "the step called the model, but no request record of it was stored", Fix: holeFix(obsdb.HoleGap)}
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
	// A request record without an attempt number (a body that did not
	// parse: content derived) is still an attempt — listed by its
	// request index, badged derived — so the count agrees with the
	// requests route. The step's only record, unnumbered, is the call
	// the spans time: it joins the first attempt there (attempt 1, or
	// the lowest attempt span), never a second attempt beside it.
	var unnumbered []stepAttempt
	numbered := false
	for _, rec := range reqs {
		numbered = numbered || rec.Attempt > 0
	}
	for _, rec := range reqs {
		idx := rec.Index
		if rec.Attempt <= 0 && (numbered || len(reqs) > 1) {
			unnumbered = append(unnumbered, stepAttempt{
				Model: rec.Body.Model.Name, Provider: rec.Body.Model.Provider,
				RequestIndex: &idx, Badge: string(obsdb.HoleDerived),
			})
			holes.add(obsdb.HoleDerived, "a request record of this step carried no attempt number (its body did not parse): it is listed by its request index", "")
			continue
		}
		num := rec.Attempt
		if num <= 0 {
			num = 0
			for _, sp := range attemptSpans {
				if k, ok := attrInt64(sp.Attrs["weft.attempt.index"]); ok && k > 0 && (num == 0 || k < num) {
					num = k
				}
			}
			num = max(num, 1)

			holes.add(obsdb.HoleDerived, "the step's request record carried no attempt number (its body did not parse): it is joined to attempt "+
				strconv.FormatInt(num, 10)+", the first the spans time", "")
		}
		a := att(num)
		a.RequestIndex = &idx
		a.Model, a.Provider = rec.Body.Model.Name, rec.Body.Model.Provider
		if rec.Attempt <= 0 {
			a.Badge = string(obsdb.HoleDerived)
		}
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
	spanWithoutRequest := false
	for _, sp := range attemptSpans {
		if k, ok := attrInt64(sp.Attrs["weft.attempt.index"]); ok && k > 0 {
			if _, known := byAttempt[k]; !known && reqHole == "" {
				spanWithoutRequest = true
			}
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
	doc.Attempts = append(doc.Attempts, unnumbered...)
	// The attempts' outcomes and the answering model come from spans
	// (the chat span's gen_ai.response.model, the attempt spans): obsdb
	// serves no event record's attributes, so the step_finish record's
	// own gen_ai.response.model is not read. A run with no spans says
	// why: it ran without a tracer. A running step's chat span is not
	// exported until its call ends — nothing is missing yet.
	var attBadge obsdb.Hole
	var attReason, attFix string
	switch {
	case doc.Status == stepRunning:
	case chat == nil && len(allSpans) == 0 && (evs.finish != nil || modelCalled || !evs.found):
		attBadge = obsdb.HoleNotRecorded
		attReason, attFix = obsdb.HoleNoteFor(obsdb.HoleNotRecorded, obsdb.CauseNoSpans)
	case chat == nil && modelCalled:
		attBadge, attReason = obsdb.HoleGap, "the step called the model, but its chat span was not stored: a destination dropped it"
	case chat != nil && len(attemptSpans) == 0 && chat.Attrs["weft.stream"] == nil:
		attBadge, attReason = obsdb.HoleNotRecorded, "the step's chat span has no attempt spans and no A4 timing: it was recorded by a weft before attempt reporting"
	case spanWithoutRequest:
		attBadge, attReason = obsdb.HoleGap, "an attempt span of this step has no request record: a destination dropped it"
	}
	if attBadge != "" {
		if attFix == "" {
			attFix = holeFix(attBadge)
		}
		doc.AttemptsBadge = &badgeFields{Badge: string(attBadge), Reason: attReason, Fix: attFix}
		holes.add(attBadge, attReason, attFix)
	}

	// The compaction view the step's request carried.
	for _, c := range comps {
		if c.Scope != obsdb.CompactionRun || c.Step != n {
			continue
		}
		note := badgeFields{Badge: string(obsdb.HoleCompacted),
			Reason: "the model saw a compacted view: a PrepareStep replaced part of the transcript for this step's request",
			Fix:    holeFix(obsdb.HoleCompacted)}
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
			doc.MessagesIn.badgeFields = badgeFields{Badge: string(obsdb.HoleGap), Reason: "no request record of the step was stored", Fix: holeFix(obsdb.HoleGap)}
		}
	default:
		ref := first.Body.MessagesRef
		doc.MessagesIn.Count, doc.MessagesIn.Index = ref.Count, ref.Index
		switch {
		case first.Content == obsdb.HoleStripped:
			doc.MessagesIn.badgeFields = badgeFields{Badge: string(obsdb.HoleStripped),
				Reason: "a content-off chain removes messages_ref.index: the count is kept, the messages are not stored",
				Fix:    holeFix(obsdb.HoleStripped)}
		case doc.Compaction != nil && ref.Index != nil && *ref.Index == doc.Compaction.Index:
			doc.MessagesIn.badgeFields = doc.Compaction.badgeFields
		case ref.Index != nil && det.Status != obsdb.StatusRunning:
			// The index names a messages record: a growth batch or a
			// stored compaction view. Neither stored, once the run
			// stopped (a running run's may be in flight), is a dropped
			// record.

			known := false
			for _, c := range comps {
				known = known || (c.Scope == obsdb.CompactionRun && c.Index == *ref.Index)
			}
			if !known {
				if batches, err = sr.transcript(); err != nil {
					return stepDoc{}, err
				}
				for _, b := range batches {
					known = known || b.Index == *ref.Index
				}
			}
			if !known {
				doc.MessagesIn.badgeFields = badgeFields{Badge: string(obsdb.HoleGap),
					Reason: "the request's messages_ref names messages record " + strconv.FormatInt(*ref.Index, 10) +
						", which was not stored: a destination dropped it",
					Fix: holeFix(obsdb.HoleGap)}
			}
		}

	}
	// Every block's badge lands in holes — messages_in's too, whichever
	// identity reads the step (the request block may be hidden).
	if b := doc.MessagesIn.badgeFields; b.Badge != "" {
		holes.add(obsdb.Hole(b.Badge), b.Reason, b.Fix)
	}
	if first != nil && first.Content == obsdb.HoleDerived {
		holes.add(obsdb.HoleDerived, "the step's request record did not parse: its body is empty and its hashes come from the record's attributes", "")
	}
	// Last, what the recorder did to the events' content (their
	// weft.content.* attributes) — after every specific reason, so it
	// only fills what nothing else named: a content-off chain's or the
	// core's capture-off mark is stripped; a destination's cap on the
	// events is truncated, its bytes summed, beside any other cut of
	// the step (the core's result cap, a capped prompt or catalog).
	var cut int64
	for _, pe := range evs.events {
		if pe.Content == "stripped" || pe.Content == "none" {
			holes.note(obsdb.HoleStripped)
		}
		if pe.TruncatedBytes > 0 {
			cut += pe.TruncatedBytes
		}
	}
	if cut > 0 {
		holes.also(obsdb.HoleTruncated, "a destination's cap cut "+strconv.FormatInt(cut, 10)+" bytes from this step's events before they were stored", holeFix(obsdb.HoleTruncated))
	}
	doc.Holes = holes.list()
	return doc, nil
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
