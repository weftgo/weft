package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ── explain_failure ────────────────────────────────────────────────
//
// explain_failure is MCP-side composition over the read routes (no new
// server logic, I4.1's rule). I5 (UX-H-23, UX-H-24, UX-H-25) made it
// read the whole run, not one step:
//
//   - every step is walked (the step route, up to explainStepCap), and
//     the run's subagent children with them: the digest says per step
//     what each call returned (errors and parks in full, ok calls by
//     name), failed model attempts, and per child its outcome; the
//     first error found — in step order, a failed call's child run
//     before the call's own text — is first_error, naming the run and
//     the step it happened in (a child's own when it was a child's);
//   - last_assistant_text is read from this run's own output batches
//     only (transcript batches with input false): a thread turn's
//     input carries the earlier turns' answers, which are not this
//     turn's text — none is "no text this run";
//   - a parked run a resume decided reads decided: each call's outcome
//     from the run document's decided block (U13.2) — "denied by
//     alice: <reason> → resumed as <run>" — and only a park no resume
//     names reads "awaiting a decision"; a resume names the calls it
//     decided (the resumes block).
//
// Bounded: at most explainFetchCap GETs in all (the run row and the
// transcript included), 25 steps a run, 12 children listed, each
// digest line at most 1 KB, the digest at most explainDigestCap lines;
// whatever a cap left out is said at the digest's end — the steps a
// run (or a child) did not walk, the children not listed, by id, a
// child the budget left unread, by id — with the read that goes
// further (step, events, runs {parent}, explain_failure on the child).

const (
	explainStepCap   = 25 // steps walked per run
	explainChildCap  = 12 // children listed per run
	explainFetchCap  = 60 // GETs one answer may make, the run row and the transcript included
	explainDigestCap = 40 // digest lines
	explainTextRunes = 160
	explainLineBytes = 1024 // a digest line, at most
	explainChildDeep = 2    // child levels walked for a first error
)

// explanation is explain_failure's answer: the run's fields and the
// chosen step's, each passed through as the routes serve them, then
// the run-wide reading (first_error, decisions, digest).
type explanation struct {
	RunID      string          `json:"run_id"`
	URL        string          `json:"url"`
	Agent      string          `json:"agent"`
	Status     string          `json:"status"`
	Error      string          `json:"error"`
	StopReason string          `json:"stop_reason"`
	Pending    int             `json:"pending"`
	Steps      int             `json:"steps"`
	RunHoles   json.RawMessage `json:"run_holes"`
	// Step is the step explained: the one in flight when the run
	// ended, else its last; null when the run has no step.
	Step *int `json:"step"`
	// StepRole says which: "in_flight" (the step the run was in when
	// it ended — a model or tool failure inside it) or "last" (the
	// run's last recorded step: it ended between steps — a budget at
	// the continuation point, a success).
	StepRole string `json:"step_role,omitempty"`
	// StepNote says why the in-flight step is not the one shown: it
	// answered 403 (hidden) or 5xx (unavailable).
	StepNote   string          `json:"step_note,omitempty"`
	StepStatus string          `json:"step_status,omitempty"`
	StepReason string          `json:"step_reason,omitempty"`
	Model      json.RawMessage `json:"model,omitempty"`
	StepHoles  json.RawMessage `json:"step_holes,omitempty"`
	// FirstError is the first error the walk found — in step order, in
	// this run or a child run under it — with where it happened; null
	// when no step, call, attempt or child failed.
	FirstError *firstError `json:"first_error,omitempty"`
	// ResumedBy is, on a parked run, the run that resumed it; Decisions
	// each parked call's decision as that resume applied it ("deploy
	// denied by alice: too risky"), or on a resume the calls it
	// decided. Absent when the run is neither.
	ResumedBy   string   `json:"resumed_by,omitempty"`
	ResumedFrom string   `json:"resumed_from,omitempty"`
	Decisions   []string `json:"decisions,omitempty"`
	// LastAssistantText is this run's own last assistant text (its
	// output batches; a thread turn's input — earlier turns — is never
	// read); null when it produced none, LastAssistantNote then saying
	// so; TranscriptError when the transcript was not readable.
	LastAssistantText *string `json:"last_assistant_text"`
	LastAssistantNote string  `json:"last_assistant_note,omitempty"`
	TranscriptError   string  `json:"transcript_error,omitempty"`
	// Next names the reads that go further.
	Next []string `json:"next"`
	// Digest is the run read whole: one line per step (its calls and
	// what they returned, failed attempts; at most explainLineBytes a
	// line), then one per child run, then the caps the walk hit. After
	// next and the text, so a cut at max_bytes takes it before them.
	Digest []string `json:"digest,omitempty"`
	// The step's bulk last — attempts, tool calls with their results,
	// children — so a cut at max_bytes takes them and keeps the
	// structured fields above (field order is the JSON's order).
	Attempts  json.RawMessage `json:"attempts,omitempty"`
	ToolCalls json.RawMessage `json:"tool_calls,omitempty"`
	Children  json.RawMessage `json:"children,omitempty"`
}

// firstError is where the first error happened: the run (this one, or
// a child under it), its agent, the step, the tool call when one
// failed, and the error text. Via is the path from the explained run
// ("step 0 shipping_quote") when the run is a child.
type firstError struct {
	Run   string `json:"run"`
	Agent string `json:"agent,omitempty"`
	Step  *int   `json:"step,omitempty"`
	Tool  string `json:"tool,omitempty"`
	Error string `json:"error"`
	Via   string `json:"via,omitempty"`
}

func (f *firstError) words(root string) string {
	var b strings.Builder
	if f.Via != "" {
		b.WriteString(f.Via + " → ")
	}
	if f.Run != root {
		fmt.Fprintf(&b, "child %s (%s) ", f.Run, orDash(f.Agent))
	}
	if f.Step != nil {
		fmt.Fprintf(&b, "step %d ", *f.Step)
	}
	if f.Tool != "" {
		b.WriteString(f.Tool + " ")
	}
	return strings.TrimSpace(b.String()) + ": " + f.Error
}

// runBrief is what explain_failure reads of a run document.
type runBrief struct {
	runHead
	ToolErrors int64        `json:"tool_errors"`
	Children   []childBrief `json:"children"`
	Decided    *decidedDoc  `json:"decided"`
	Resumes    *decidedDoc  `json:"resumes"`
}

// childBrief is a run document's child row as the walk reads it.
type childBrief struct {
	ID             string `json:"id"`
	ParentCallID   string `json:"parent_call_id"`
	Agent          string `json:"agent"`
	Status         string `json:"status"`
	Err            string `json:"err"`
	ErrorType      string `json:"error_type"`
	Steps          int    `json:"steps"`
	Pending        int    `json:"pending"`
	ResumedBy      string `json:"resumed_by"`
	ToolErrors     int64  `json:"tool_errors"`
	ChildrenFailed int    `json:"children_failed"`
}

// decidedDoc is studio's decisions block (U13.2: decided / resumes).
type decidedDoc struct {
	Run    string `json:"run"`
	Actor  string `json:"actor"`
	Source string `json:"source"`
	Hole   string `json:"hole"`
	Error  string `json:"error"`
	Calls  []struct {
		CallID     string `json:"call_id"`
		Name       string `json:"name"`
		Outcome    string `json:"outcome"`
		Via        string `json:"via"`
		Who        string `json:"who"`
		WhoHidden  bool   `json:"who_hidden"`
		ApprovedBy string `json:"approved_by"`
		Reason     string `json:"reason"`
	} `json:"calls"`
}

// words is each call's decision in one phrase, by call id: "deploy
// denied by alice: too risky". The decider is thread's who, else the
// resume's actor (weft.approval.actor), else the channel (via).
func (d *decidedDoc) words() (list []string, byCall map[string]string) {
	byCall = map[string]string{}
	if d == nil {
		return nil, byCall
	}
	for _, c := range d.Calls {
		w := c.Outcome
		switch who := c.Who; {
		case who != "":
			w += " by " + who
		case d.Actor != "" && c.Outcome != "no_decision":
			w += " by " + d.Actor
		case c.WhoHidden:
			w += " by (hidden)"
		case c.Via != "":
			w += " via " + c.Via
		}
		if c.ApprovedBy != "" {
			w += " (approved by " + c.ApprovedBy + ")"
		}
		if c.Reason != "" {
			w += ": " + cutRunes(c.Reason, explainTextRunes)
		}
		byCall[c.CallID] = w
		list = append(list, strings.TrimSpace(orDash(c.Name)+" "+w))
	}
	if len(d.Calls) == 0 {
		switch {
		case d.Error != "":
			list = append(list, "decisions not readable: "+d.Error)
		case d.Hole != "":
			list = append(list, "decisions "+d.Hole)
		}
	}
	return list, byCall
}

// stepBrief is what the walk reads of a step document.
type stepBrief struct {
	Status   string `json:"status"`
	Reason   string `json:"reason"`
	Attempts []struct {
		Attempt    int64  `json:"attempt"`
		Outcome    string `json:"outcome"`
		ErrorType  string `json:"error_type"`
		HTTPStatus int64  `json:"http_status"`
		Error      string `json:"error"`
	} `json:"attempts"`
	ToolCalls []struct {
		CallID     string `json:"call_id"`
		Name       string `json:"name"`
		ChildRunID string `json:"child_run_id"`
		Pending    bool   `json:"pending"`
		Badge      string `json:"badge"`
		Result     *struct {
			Content string `json:"content"`
			IsError bool   `json:"is_error"`
		} `json:"result"`
	} `json:"tool_calls"`
	Holes []struct {
		Hole string `json:"hole"`
	} `json:"holes"`
}

// walker reads runs and steps for one answer, each once, within the
// read budget (explainFetchCap, the transcript's read included).
type walker struct {
	m      *mcpReader
	left   int
	runs   map[string]*runBrief
	steps  map[string]stepRead
	capped bool // the budget ran out: the digest says so
	// callOf is each child run's call, as the walk matched it: the
	// tool that started it and its place in the run's calls (step
	// order, then call order) — call ids repeat across steps (call_1
	// every step), so a child is never matched by call id alone.
	callOf map[string]callRef
	// notes are the walk's caps (steps not walked, a run's or a
	// child's), said at the digest's end.
	notes []string
}

type callRef struct {
	tool string
	ord  int
}

type stepRead struct {
	body   []byte
	status int
	err    error
}

func newWalker(m *mcpReader, root string, h *runBrief) *walker {
	return &walker{m: m, left: explainFetchCap - 2, // the run row and the transcript are reads too
		runs: map[string]*runBrief{root: h}, steps: map[string]stepRead{}, callOf: map[string]callRef{}}
}

func (w *walker) step(ctx context.Context, id string, n int) stepRead {
	k := id + "\x00" + fmt.Sprint(n)
	if r, ok := w.steps[k]; ok {
		return r
	}
	if w.left <= 0 {
		w.capped = true
		return stepRead{err: errBudget}
	}
	w.left--
	status, b, err := w.m.fetch(ctx, stepPath(id, n), nil)
	r := stepRead{body: b, status: status, err: err}
	w.steps[k] = r
	return r
}

func (w *walker) run(ctx context.Context, id string) (*runBrief, error) {
	if r, ok := w.runs[id]; ok {
		return r, nil
	}
	if w.left <= 0 {
		w.capped = true
		return nil, errBudget
	}
	w.left--
	b, err := w.m.get(ctx, runPath(id), nil)
	if err != nil {
		return nil, err
	}
	var r runBrief
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	w.runs[id] = &r
	return &r, nil
}

var errBudget = errors.New("not read: this answer's read budget is spent")

// readable is what a failed step read is, in words.
func readable(r stepRead) string {
	switch r.status {
	case 0:
		if errors.Is(r.err, errBudget) {
			return "not read (the answer's read budget is spent)"
		}
		return "unreachable"
	case http.StatusForbidden:
		return "hidden (403)"
	}
	return fmt.Sprintf("unavailable (%d)", r.status)
}

// childFor is the child run a step's call started: the step's own
// child_run_id, else core's id rule (<run>/<step>/<call>), else the
// one child naming that call id when only one does.
func childFor(id string, n int, callID, childRunID string, byID map[string]childBrief, byCall map[string][]childBrief) (childBrief, bool) {
	if childRunID != "" {
		if c, ok := byID[childRunID]; ok {
			return c, true
		}
		return childBrief{ID: childRunID}, true
	}
	if c, ok := byID[fmt.Sprintf("%s/%d/%s", id, n, callID)]; ok {
		return c, true
	}
	if cs := byCall[callID]; len(cs) == 1 {
		return cs[0], true
	}
	return childBrief{}, false
}

// digestLine joins a step's head and parts, at most explainLineBytes:
// the parts past it are counted ("+n calls"), never cut mid-part.
func digestLine(head string, parts []string) string {
	if len(parts) == 0 {
		return head
	}
	s := head + ": "
	for i, p := range parts {
		sep := ""
		if i > 0 {
			sep = "; "
		}
		if len(s)+len(sep)+len(p) > explainLineBytes && i > 0 {
			return s + fmt.Sprintf("; +%d more (step {n:…} has them all)", len(parts)-i)
		}
		s += sep + p
	}
	return s
}

// walkRun reads run r's steps (and, for a failed call, its child) and
// answers the step lines and the run's first error: the first one
// nothing recovered from — a failed call, a step's last failed attempt,
// the run's own failure — else the first attempt a retry recovered
// from, marked so. via is the path from the explained run, depth the
// child level (0 = the run itself).
func (w *walker) walkRun(ctx context.Context, id string, r *runBrief, via string, depth int, decided map[string]string) (lines []string, first *firstError) {
	byID, byCall := map[string]childBrief{}, map[string][]childBrief{}
	for _, c := range r.Children {
		byID[c.ID] = c
		byCall[c.ParentCallID] = append(byCall[c.ParentCallID], c)
	}
	var recovered *firstError
	ord := 0
	last := min(r.Steps, explainStepCap-1)
	for n := 0; n <= last; n++ {
		sr := w.step(ctx, id, n)
		if sr.status == http.StatusNotFound {
			break // the in-flight step that never started
		}
		if sr.err != nil {
			lines = append(lines, fmt.Sprintf("step %d: %s", n, readable(sr)))
			continue
		}
		var st stepBrief
		_ = json.Unmarshal(sr.body, &st)
		head := fmt.Sprintf("step %d: %s", n, orDash(st.Status))
		if st.Reason != "" {
			head += " (" + st.Reason + ")"
		}
		var parts []string
		for ai, a := range st.Attempts {
			if a.Outcome != "error" {
				continue
			}
			what := "attempt " + fmt.Sprint(a.Attempt) + " error"
			if a.HTTPStatus != 0 {
				what += fmt.Sprintf(" (%d)", a.HTTPStatus)
			}
			e := orDash(firstNonEmpty(a.Error, a.ErrorType))
			okAfter := int64(0)
			for _, b := range st.Attempts[ai+1:] {
				if b.Outcome == "ok" {
					okAfter = b.Attempt
					break
				}
			}
			nn := n
			fe := &firstError{Run: id, Agent: r.Agent, Step: &nn, Error: "model attempt " + fmt.Sprint(a.Attempt) + ": " + e, Via: via}
			if okAfter > 0 {
				what += fmt.Sprintf(" (retried, attempt %d ok)", okAfter)
				fe.Error += fmt.Sprintf(" (retried, attempt %d ok)", okAfter)
				if recovered == nil {
					recovered = fe
				}
			} else if first == nil {
				first = fe
			}
			parts = append(parts, what+" "+quoteCut(e))
		}
		for _, c := range st.ToolCalls {
			child, hasChild := childFor(id, n, c.CallID, c.ChildRunID, byID, byCall)
			if hasChild {
				w.callOf[child.ID] = callRef{tool: c.Name, ord: ord}
			}
			ord++
			switch {
			case c.Pending:
				if d, ok := decided[c.CallID]; ok {
					parts = append(parts, c.Name+" "+d)
				} else {
					parts = append(parts, c.Name+" pending (awaiting a decision)")
				}
			case c.Result != nil && c.Result.IsError:
				parts = append(parts, c.Name+" error "+quoteCut(c.Result.Content))
				if first == nil {
					nn := n
					if hasChild && depth < explainChildDeep {
						if f, _ := w.childFirst(ctx, child, joinVia(via, fmt.Sprintf("step %d %s", n, c.Name)), depth+1); f != nil {
							first = f
							break
						}
					}
					first = &firstError{Run: id, Agent: r.Agent, Step: &nn, Tool: c.Name, Error: c.Result.Content, Via: via}
				}
			case c.Result != nil:
				parts = append(parts, c.Name+" ok")
			case c.Badge != "":
				parts = append(parts, c.Name+" "+c.Badge)
			default:
				parts = append(parts, c.Name+" no result")
			}
		}
		for _, h := range st.Holes {
			parts = append(parts, "hole "+h.Hole)
		}
		lines = append(lines, digestLine(head, parts))
	}
	if r.Steps+1 > explainStepCap {
		w.notes = append(w.notes, fmt.Sprintf("run %s: steps %d–%d not walked (at most %d per run): step {id:%q, n:…} or events {id:%q}",
			id, explainStepCap, r.Steps, explainStepCap, id, id))
	}
	if first == nil && (r.Status == "failed" || r.Status == "interrupted") && r.Err != "" {
		n := r.Steps
		first = &firstError{Run: id, Agent: r.Agent, Step: &n, Error: r.Err, Via: via}
	}
	if first == nil {
		first = recovered
	}
	return lines, first
}

// childFirst is a child run's first error (nil when it has none) and
// whether the child was walked: false when the read budget was spent
// before its run row.
func (w *walker) childFirst(ctx context.Context, c childBrief, via string, depth int) (*firstError, bool) {
	r, err := w.run(ctx, c.ID)
	if err != nil {
		if c.Err != "" {
			return &firstError{Run: c.ID, Agent: c.Agent, Error: c.Err, Via: via}, !errors.Is(err, errBudget)
		}
		return nil, !errors.Is(err, errBudget)
	}
	_, decided := r.Decided.words()
	_, f := w.walkRun(ctx, c.ID, r, via, depth, decided)
	return f, true
}

// childLine is one child run in a phrase, with its first error when it
// has one; a child the budget left unread says so, naming the read.
func (w *walker) childLine(ctx context.Context, c childBrief, tool string, depth int) string {
	s := fmt.Sprintf("child %s (%s)", c.ID, orDash(c.Agent))
	if tool != "" {
		s += " ← " + tool
	}
	s += ": " + orDash(c.Status)
	if c.ErrorType != "" && c.Status == "failed" {
		s += " (" + c.ErrorType + ")"
	}
	if c.Err != "" {
		s += " " + quoteCut(c.Err)
	}
	switch {
	case c.Pending > 0 && c.ResumedBy != "":
		s += fmt.Sprintf("; parked (%d calls), decided → resumed as %s", c.Pending, c.ResumedBy)
	case c.Pending > 0:
		s += fmt.Sprintf("; parked: %d calls awaiting a decision", c.Pending)
	}
	if c.Status == "succeeded" && (c.ToolErrors > 0 || c.ChildrenFailed > 0) && depth < explainChildDeep {
		f, walked := w.childFirst(ctx, c, "", depth+1)
		switch {
		case !walked:
			s += fmt.Sprintf("; not walked (the read budget is spent): explain_failure {id:%q}", c.ID)
		case f != nil:
			s += "; its first error: " + f.words(c.ID)
		}
	}
	return s
}

func joinVia(a, b string) string {
	if a == "" {
		return b
	}
	return a + " → " + b
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// quoteCut is s cut and quoted for a digest line.
func quoteCut(s string) string {
	return fmt.Sprintf("%q", cutRunes(s, explainTextRunes))
}

func (m *mcpReader) explainFailure(ctx context.Context, _ *sdk.CallToolRequest, in mcpRunIn) (*sdk.CallToolResult, any, error) {
	if err := needID("id", in.ID); err != nil {
		return nil, nil, err
	}
	rb, err := m.get(ctx, runPath(in.ID), nil)
	if err != nil {
		return nil, nil, err
	}
	var h runBrief
	if err := json.Unmarshal(rb, &h); err != nil {
		return nil, nil, fmt.Errorf("GET %s: %w", runPath(in.ID), err)
	}
	w := newWalker(m, in.ID, &h)
	ex := explanation{
		RunID: in.ID, URL: m.s.runLink(in.ID), Agent: h.Agent, Status: h.Status, Error: h.Err,
		StopReason: h.StopReason, Pending: h.Pending, Steps: h.Steps, RunHoles: h.Holes,
		ResumedBy: h.ResumedBy, ResumedFrom: h.ResumedFrom,
	}
	// The step: the one in flight when the run ended (ordinal = the
	// count of finished steps) when it started, else the last finished
	// one. A succeeded run's in-flight step does not exist.
	var sb []byte
	tryStep := func(n int, role string) bool {
		if n < 0 {
			return false
		}
		r := w.step(ctx, in.ID, n)
		if r.status == http.StatusNotFound {
			return false
		}
		if r.err != nil {
			what := readable(r)
			note := fmt.Sprintf("step %d is %s", n, what)
			if role == "in_flight" {
				note = fmt.Sprintf("the in-flight step %d is %s; showing the last recorded step", n, what)
			}
			// A failing fallback adds to the in-flight note, never replaces it.
			if ex.StepNote != "" {
				note = ex.StepNote + "; " + note
			}
			ex.StepNote = note
			ex.Next = append(ex.Next, fmt.Sprintf("step %d not readable: %v", n, r.err))
			return false
		}
		sb, ex.Step, ex.StepRole = r.body, &n, role
		return true
	}
	if !tryStep(h.Steps, "in_flight") {
		tryStep(h.Steps-1, "last")
	}
	var sh stepHead
	requestHidden := false
	if sb != nil {
		var sd struct {
			Status    string          `json:"status"`
			Reason    string          `json:"reason"`
			Model     json.RawMessage `json:"model"`
			Attempts  json.RawMessage `json:"attempts"`
			ToolCalls json.RawMessage `json:"tool_calls"`
			Children  json.RawMessage `json:"children"`
			Holes     json.RawMessage `json:"holes"`
			Request   struct {
				Badge string `json:"badge"`
			} `json:"request"`
		}
		_ = json.Unmarshal(sb, &sd)
		_ = json.Unmarshal(sb, &sh)
		ex.StepStatus, ex.StepReason, ex.Model, ex.Attempts = sd.Status, sd.Reason, sd.Model, sd.Attempts
		ex.ToolCalls, ex.Children, ex.StepHoles = sd.ToolCalls, sd.Children, sd.Holes
		requestHidden = sd.Request.Badge == "hidden"
	}

	// The decisions: a parked run's (decided), a resume's (resumes).
	decidedList, decided := h.Decided.words()
	ex.Decisions = append(ex.Decisions, decidedList...)
	if h.Resumes != nil {
		rl, _ := h.Resumes.words()
		for _, d := range rl {
			ex.Decisions = append(ex.Decisions, "resumed "+h.ResumedFrom+": "+d)
		}
	}

	// The run read whole: every step, then the children.
	lines, first := w.walkRun(ctx, in.ID, &h, "", 0, decided)
	ex.FirstError = first
	// The children in call order (the walk's matches: step, then call),
	// each named by the tool that started it.
	slices.SortStableFunc(h.Children, func(a, b childBrief) int {
		ia, oka := w.callOf[a.ID]
		ib, okb := w.callOf[b.ID]
		switch {
		case oka && okb:
			return ia.ord - ib.ord
		case oka:
			return -1
		case okb:
			return 1
		}
		return 0
	})
	for i, c := range h.Children {
		if i == explainChildCap {
			var ids []string
			for _, x := range h.Children[i:] {
				ids = append(ids, x.ID)
			}
			w.notes = append(w.notes, fmt.Sprintf("%d more children not listed (at most %d): %s — runs {parent:%q}",
				len(ids), explainChildCap, cutRunes(strings.Join(ids, ", "), 600), in.ID))
			break
		}
		lines = append(lines, w.childLine(ctx, c, w.callOf[c.ID].tool, 0))
	}
	if w.capped {
		w.notes = append(w.notes, fmt.Sprintf("the walk stopped at this answer's %d reads: step / run / explain_failure on what is left", explainFetchCap))
	}
	// The caps last, never cut by the line cap.
	if room := explainDigestCap - len(w.notes); len(lines) > room {
		lines = append(lines[:room-1], fmt.Sprintf("%d more lines: events {id:%q}, runs {parent:%q}", len(lines)-room+1, in.ID, in.ID))
	}
	lines = append(lines, w.notes...)
	ex.Digest = lines

	if tb, err := m.get(ctx, runPath(in.ID)+"/transcript", nil); err != nil {
		ex.TranscriptError = err.Error()
	} else {
		ex.LastAssistantText = lastAssistantText(tb)
		if ex.LastAssistantText == nil {
			ex.LastAssistantNote = "no text this run: it produced no assistant text (its input — an earlier turn's answer — is not read as this run's)"
		}
	}
	if ex.Step != nil {
		ex.Next = append(ex.Next, fmt.Sprintf("step {id:%q, n:%d} for the step whole", in.ID, *ex.Step))
		if !requestHidden { // a read token's request block is the hidden badge: the request tool would 403
			ex.Next = append(ex.Next, fmt.Sprintf("request {id:%q, step:%d, transcript:true} for the exact model input", in.ID, *ex.Step))
		}
	}
	ex.Next = append(ex.Next, fmt.Sprintf("events {id:%q} for the event stream", in.ID))
	if len(h.Children) > 0 {
		ex.Next = append(ex.Next, fmt.Sprintf("explain_failure {id:<child id>} or spans {id:%q, tree:true} for a child run", in.ID))
	}
	b, err := json.Marshal(ex)
	if err != nil {
		return nil, nil, err
	}
	return out(in.MaxBytes, explainLine(ex, sh, decided), b)
}

// explainLine is the answer in one sentence an agent can quote.
func explainLine(ex explanation, sh stepHead, decided map[string]string) string {
	s := fmt.Sprintf("run %s (%s): %s", ex.RunID, orDash(ex.Agent), ex.Status)
	if ex.Step != nil {
		switch {
		case ex.StepRole == "in_flight" || ex.StepStatus == "error":
			s += fmt.Sprintf(" at step %d", *ex.Step)
		case ex.Status == "failed" || ex.Status == "interrupted":
			s += fmt.Sprintf(" before step %d started (step %d, its last recorded step, finished)", *ex.Step+1, *ex.Step)
		default:
			s += fmt.Sprintf(", last step %d", *ex.Step)
		}
	} else {
		s += ", no step recorded"
	}
	if ex.Error != "" {
		s += ": " + ex.Error
	}
	if ex.StopReason != "" {
		s += "; stop_reason " + ex.StopReason
	}
	if ex.StepNote != "" {
		s += "; " + ex.StepNote
	}
	switch {
	case ex.Pending > 0 && ex.ResumedBy != "":
		s += fmt.Sprintf("; parked, decided: %s → resumed as %s", orDash(strings.Join(ex.Decisions, "; ")), ex.ResumedBy)
	case ex.Pending > 0:
		var names []string
		for _, c := range sh.ToolCalls {
			if c.Pending {
				names = append(names, c.Name)
			}
		}
		s += fmt.Sprintf("; parked: %d calls awaiting a decision (%s)", ex.Pending, strings.Join(names, ", "))
	case ex.ResumedFrom != "" && len(ex.Decisions) > 0:
		s += "; " + strings.Join(ex.Decisions, "; ")
	case ex.ResumedFrom != "":
		s += "; resumes " + ex.ResumedFrom
	}
	if ex.Step != nil {
		if d := sh.lineDecided(ex.RunID, *ex.Step, decided); strings.Contains(d, "; ") {
			s += "; " + d[strings.Index(d, "; ")+2:]
		}
	}
	// The first error, unless it is the run's own failure in the step
	// it failed in (the line already says that).
	if f := ex.FirstError; f != nil && (f.Run != ex.RunID || ex.Error == "" || f.Step == nil || *f.Step != ex.Steps) {
		s += "; first error: " + cutRunes(f.words(ex.RunID), 300)
	}
	if n := problemLines(ex.Digest); n > 0 {
		s += fmt.Sprintf("; digest: %d of %d lines report an error, a park or a failed child", n, len(ex.Digest))
	}
	if ex.LastAssistantText != nil {
		s += fmt.Sprintf("; last assistant text %q", cutRunes(*ex.LastAssistantText, 200))
	} else if ex.LastAssistantNote != "" {
		s += "; no assistant text this run"
	}
	return s + " — " + ex.URL
}

// problemLines counts the digest lines that report trouble.
func problemLines(lines []string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, " error ") || strings.Contains(l, "pending") || strings.Contains(l, "denied") ||
			strings.Contains(l, ": failed") || strings.Contains(l, ": interrupted") || strings.Contains(l, "parked") {
			n++
		}
	}
	return n
}

// lastAssistantText reads GET /api/runs/{id}/transcript's batches and
// returns the last assistant message's text among the run's own output
// batches (input false); nil when none has text. The input batches are
// what the run was given — on a thread turn the earlier turns, their
// answers included — and never this run's text (UX-H-23).
func lastAssistantText(b []byte) *string {
	var tr struct {
		Batches []struct {
			Input    bool            `json:"input"`
			Messages json.RawMessage `json:"messages"`
		} `json:"batches"`
	}
	if json.Unmarshal(b, &tr) != nil {
		return nil
	}
	var last *string
	for _, batch := range tr.Batches {
		if batch.Input {
			continue
		}
		var msgs []struct {
			Role  string `json:"role"`
			Parts []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(batch.Messages, &msgs) != nil {
			continue
		}
		for _, msg := range msgs {
			if msg.Role != "assistant" {
				continue
			}
			var t []string
			for _, p := range msg.Parts {
				if p.Type == "text" && p.Text != "" {
					t = append(t, p.Text)
				}
			}
			if len(t) > 0 {
				s := strings.Join(t, "")
				last = &s
			}
		}
	}
	return last
}
