package studio

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
)

// The step-aligned diff (plan E3, E3.1):
//
//	GET /api/diff?a=<run id>&b=<run id>
//
// Two runs, aligned by step ordinal (decision 8: the ordinal is the
// step's id everywhere, here the alignment key), each step assembled
// exactly as GET /api/runs/{id}/steps/{n} assembles it (assembleStep),
// then compared field by field: the system prompt (by hash when both
// sides have one), the tool calls as name(args) with the arguments
// canonicalised (sorted keys), the tool results (content and is_error,
// in call order), the assistant text (exact), the usage (the numbers).
// A step one run has and the other lacks is changed with "missing".
//
// A compaction view and a subagent call are marks on their side, never
// alignment changes (decision 12): they flip nothing by themselves.
// What a side lacks is a hole from obsdb.HoleNote's table in its holes,
// never an empty field without a reason. Scoped as the step route is:
// both runs must be inside a panel token's public id (403, an unknown
// run 404), and a read-scoped token reads every column but the system
// prompt (system null, the hidden hole).
//
// The route is two-way; an N-way view calls it N−1 times against a
// base run. Both clients (the panel's two columns, Studio's N) render
// this one response.

// maxDiffSteps bounds the steps one diff compares: a run row's step
// count is the producer's number, and one hostile value must not size
// the response. A longer run is compared this far, said by the
// truncated hole (cause response_cap). A var so a test can lower it.
var maxDiffSteps = 500

// The changes a row can list, in this order.
const (
	diffSystem      = "system"
	diffToolCalls   = "tool_calls"
	diffToolResults = "tool_results"
	diffText        = "text"
	diffUsage       = "usage"
	diffMissing     = "missing"
)

// diffDoc is GET /api/diff.
type diffDoc struct {
	A       diffRun     `json:"a"`
	B       diffRun     `json:"b"`
	Steps   []diffRow   `json:"steps"`
	Summary diffSummary `json:"summary"`
	// Holes is the diff's own: truncated (cause response_cap) when a
	// run has more steps than the diff compares.
	Holes []stepHole `json:"holes"`
}

// diffRun names one side's run: its id, the steps it recorded, its
// derived status.
type diffRun struct {
	RunID  string `json:"run_id"`
	Steps  int    `json:"steps"`
	Status string `json:"status"`
}

type diffSummary struct {
	ChangedSteps []int `json:"changed_steps"`
	FirstChanged *int  `json:"first_changed"`
}

// diffRow is one step ordinal: each side's step (null when that run
// has no such step) and what differs.
type diffRow struct {
	Step    int       `json:"step"`
	Changed bool      `json:"changed"`
	A       *diffSide `json:"a"`
	B       *diffSide `json:"b"`
	Changes []string  `json:"changes"`
	// Unknown lists the columns that could not be compared because a
	// side did not record them (its holes say why) — neither changed
	// nor the same; [] when every column compared.
	Unknown []string `json:"unknown"`
}

// diffSide is one run's step, reduced to the compared columns. System
// is null when the step's request block does not carry it (hidden,
// not_recorded, gap, a missing prompt record — the hole says which);
// Text is null when no messages record of the step holds its
// assistant message.
type diffSide struct {
	Status      string           `json:"status"`
	SystemHash  string           `json:"system_hash"`
	System      *string          `json:"system"`
	ToolCalls   []diffToolCall   `json:"tool_calls"`
	ToolResults []diffToolResult `json:"tool_results"`
	Text        *string          `json:"text"`
	Usage       core.Usage       `json:"usage"`
	Marks       []string         `json:"marks"`
	Holes       []stepHole       `json:"holes"`

	// What the side could not record, for compareSides (never served:
	// each is a hole in Holes).
	eventsLost, contentLost, namesLost, textLost bool
}

// diffToolCall is a call as compared: its name and its arguments,
// canonicalised (object keys sorted, no insignificant space).
type diffToolCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// diffToolResult is a call's result as the model saw it; a parked or
// running call has none and is not listed.
type diffToolResult struct {
	CallID  string `json:"call_id"`
	Name    string `json:"name"`
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

// serveDiff answers GET /api/diff?a=&b=.
func (s *Server) serveDiff(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	a, b := q.Get("a"), q.Get("b")
	if a == "" || b == "" {
		badRequest(w, r, "a and b must both name a run: /api/diff?a=<run id>&b=<run id>")
		return
	}
	if !s.scopeRunID(w, r, a) || !s.scopeRunID(w, r, b) {
		return
	}
	prompts := readsPrompts(r)
	ra, err := s.diffSteps(r, a, prompts)
	if err != nil {
		writeStepError(w, r, a, 0, err)
		return
	}
	rb := ra
	if b != a {
		if rb, err = s.diffSteps(r, b, prompts); err != nil {
			writeStepError(w, r, b, 0, err)
			return
		}
	}

	doc := diffDoc{
		A: ra.ref, B: rb.ref, Steps: []diffRow{},
		Summary: diffSummary{ChangedSteps: []int{}}, Holes: []stepHole{},
	}
	if ra.truncated || rb.truncated {
		reason, fix := obsdb.HoleNoteFor(obsdb.HoleTruncated, obsdb.CauseResponseCap)
		doc.Holes = []stepHole{{Hole: string(obsdb.HoleTruncated), Reason: reason, Fix: fix}}
	}
	for n := range max(len(ra.sides), len(rb.sides)) {
		row := diffRow{Step: n, Changes: []string{}, Unknown: []string{}}
		if n < len(ra.sides) {
			row.A = ra.sides[n]
		}
		if n < len(rb.sides) {
			row.B = rb.sides[n]
		}
		if row.A == nil || row.B == nil {
			row.Changes = append(row.Changes, diffMissing)
		} else {
			row.Changes, row.Unknown = compareSides(row.A, row.B)
		}
		row.Changed = len(row.Changes) > 0
		if row.Changed {
			doc.Summary.ChangedSteps = append(doc.Summary.ChangedSteps, n)
			if doc.Summary.FirstChanged == nil {
				first := n
				doc.Summary.FirstChanged = &first
			}
		}
		doc.Steps = append(doc.Steps, row)
	}
	writeJSON(w, r, http.StatusOK, doc)
}

// diffRunSteps is one run's steps, reduced.
type diffRunSteps struct {
	ref       diffRun
	sides     []*diffSide
	truncated bool
}

// diffSteps assembles every step of run id (up to maxDiffSteps) and
// reduces each to its compared columns. The steps are the ordinals
// assembleStep answers, from 0 until the first it does not.
func (s *Server) diffSteps(r *http.Request, id string, prompts bool) (diffRunSteps, error) {
	sr, err := s.loadStepRun(r.Context(), id)
	if err != nil {
		return diffRunSteps{}, err
	}
	out := diffRunSteps{ref: diffRun{RunID: id, Status: string(sr.det.Status)}}
	var texts map[int]*string
	readable := true
	for n := 0; ; n++ {
		if n == maxDiffSteps {
			// One more step recorded is a truncated diff.
			if _, err := sr.assembleStep(n, prompts); err == nil {
				out.truncated = true
			} else if !isNoStep(err) {
				return diffRunSteps{}, err
			}
			break
		}
		doc, err := sr.assembleStep(n, prompts)
		if isNoStep(err) {
			break
		}
		if err != nil {
			return diffRunSteps{}, err
		}
		if texts == nil {
			if texts, readable, err = stepTexts(sr); err != nil {
				return diffRunSteps{}, err
			}
		}
		out.sides = append(out.sides, diffSideOf(doc, texts[n], readable))
	}
	// The run row's count when it says more (a truncated diff, a step
	// the walk could not reach); a running run's row may lag its
	// events, so never less than the steps assembled.
	out.ref.Steps = max(len(out.sides), sr.det.Steps)
	return out, nil
}

func isNoStep(err error) bool { return errors.Is(err, errNoStep) }

// stepTexts is each step's assistant text from the run's transcript
// (the transcript route's records, read once): the text parts of the
// step's assistant messages, joined by a newline. A step with no
// assistant message stored is absent. readable is false when a
// messages body does not parse: then no step's text is known.
func stepTexts(sr *stepRun) (texts map[int]*string, readable bool, err error) {
	batches, err := sr.transcript()
	if err != nil {
		return nil, false, err
	}
	out := map[int]*string{}
	msgs, err := runSteps(batches)
	if err != nil {
		return out, false, nil
	}
	for _, m := range msgs {
		if m.msg.Role != core.RoleAssistant {
			continue
		}
		t := m.msg.Text()
		if cur, ok := out[m.step]; ok {
			if t != "" {
				joined := *cur
				if joined != "" {
					joined += "\n"
				}
				joined += t
				out[m.step] = &joined
			}
			continue
		}
		out[m.step] = &t
	}
	return out, true, nil
}

// diffSideOf reduces one assembled step. readable is stepTexts'.
func diffSideOf(doc stepDoc, text *string, readable bool) *diffSide {
	side := &diffSide{
		Status: doc.Status, SystemHash: doc.sysHash, System: doc.sysText,
		ToolCalls: []diffToolCall{}, ToolResults: []diffToolResult{},
		Text: text, Usage: doc.Usage, Marks: []string{},
		eventsLost: doc.eventsLost,
	}
	holes := holeSet{}
	for _, h := range doc.Holes {
		holes[obsdb.Hole(h.Hole)] = h
	}
	for _, c := range doc.ToolCalls {
		side.ToolCalls = append(side.ToolCalls, diffToolCall{Name: c.Name, Args: canonicalJSON(c.Args)})
		if c.Badge == string(obsdb.HoleStripped) {
			side.contentLost = true
			// A content-off max_tokens step counts its calls, nothing
			// more: their names are unknown too.
			side.namesLost = side.namesLost || c.Name == ""
		}
		if c.Result != nil {
			side.ToolResults = append(side.ToolResults, diffToolResult{
				CallID: c.CallID, Name: c.Name, Content: c.Result.Content, IsError: c.Result.IsError,
			})
		}
	}
	// The marks: what the side's step was, beside what it said.
	if doc.Compaction != nil {
		side.Marks = append(side.Marks, string(obsdb.HoleCompacted))
	}
	if len(doc.Children) > 0 {
		side.Marks = append(side.Marks, "subagent")
	}
	if doc.Reason == string(core.StopMaxTokens) {
		side.Marks = append(side.Marks, string(obsdb.HoleMaxTokens))
	}
	switch _, interrupted := holes[obsdb.HoleInterrupted]; {
	case doc.Status == stepParked:
		side.Marks = append(side.Marks, stepParked)
	case doc.Status == stepRunning:
		side.Marks = append(side.Marks, stepRunning)
	case interrupted:
		side.Marks = append(side.Marks, string(obsdb.HoleInterrupted))
	case doc.Status == stepError:
		side.Marks = append(side.Marks, stepError)
	}
	// The text: a messages body that does not parse hides every step's;
	// a finished step's assistant message is recorded with it, so none
	// stored is a hole, unless one already says why (content-off, a run
	// before the record, a gap — a hidden block does not hide text).
	switch {
	case !readable:
		side.Text, side.textLost = nil, true
		holes.also(obsdb.HoleGap, "a messages record of this run does not parse: the step's assistant text is unknown", holeFix(obsdb.HoleGap))
	case text == nil && (doc.Status == stepOK || doc.Status == stepParked):
		side.textLost = true
		explained := false
		for _, h := range []obsdb.Hole{obsdb.HoleStripped, obsdb.HoleNotRecorded, obsdb.HoleGap} {
			_, ok := holes[h]
			explained = explained || ok
		}
		if !explained {
			holes.add(obsdb.HoleGap, "no messages record of this step was stored: its assistant text is unknown", holeFix(obsdb.HoleGap))
		}
	}
	side.Holes = holes.list()
	return side
}

// compareSides lists what differs between two sides of one step
// (changes) and what cannot be compared (unknown): a column either side
// could not record — its hole says why — is never compared with the
// other side's value, so "unknown" is never read as "same".
//
//   - system: unknown when a side has neither a hash nor the text (a
//     run before the request record, a dropped request record); by
//     hash when both have one (hidden and content-off keep the hash);
//   - tool_calls, tool_results, usage: unknown when a side's events
//     are not all stored (eventsLost); tool_calls by name alone and
//     tool_results unknown when a side's calls were content-off;
//   - text: unknown when a side's text is null for a hole.
//
// Marks, holes, status and timing are never compared: a compaction or
// a subagent call marks its side and changes nothing by itself.
func compareSides(a, b *diffSide) (changes, unknown []string) {
	changes, unknown = []string{}, []string{}
	note := func(col string, known, same bool) {
		switch {
		case !known:
			unknown = append(unknown, col)
		case !same:
			changes = append(changes, col)
		}
	}
	sysKnown := (a.SystemHash != "" || a.System != nil) && (b.SystemHash != "" || b.System != nil)
	sysSame := a.SystemHash == b.SystemHash
	if a.SystemHash == "" || b.SystemHash == "" {
		sysSame = sysSame && sameText(a.System, b.System)
	}
	note(diffSystem, sysKnown, sysSame)

	eventsKnown := !a.eventsLost && !b.eventsLost
	contentKnown := !a.contentLost && !b.contentLost
	namesSame := slices.EqualFunc(a.ToolCalls, b.ToolCalls, func(x, y diffToolCall) bool { return x.Name == y.Name })
	callsSame := slices.EqualFunc(a.ToolCalls, b.ToolCalls, func(x, y diffToolCall) bool {
		return x.Name == y.Name && bytes.Equal(x.Args, y.Args)
	})
	switch {
	case !eventsKnown || a.namesLost || b.namesLost:
		note(diffToolCalls, false, false)
	case !namesSame:
		note(diffToolCalls, true, false) // the names alone already differ
	default:
		note(diffToolCalls, contentKnown, callsSame)
	}
	note(diffToolResults, eventsKnown && contentKnown, slices.EqualFunc(a.ToolResults, b.ToolResults, func(x, y diffToolResult) bool {
		return x.Content == y.Content && x.IsError == y.IsError
	}))
	note(diffText, !a.textLost && !b.textLost, sameText(a.Text, b.Text))
	note(diffUsage, eventsKnown, a.Usage == b.Usage)
	return changes, unknown
}

// sameText says two nullable texts are both null or equal.
func sameText(a, b *string) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}

// canonicalJSON re-encodes a JSON value with its object keys sorted
// and numbers kept as written; a value that does not parse is kept
// verbatim, an empty one is null.
func canonicalJSON(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 {
		return json.RawMessage("null")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return raw
	}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return raw
	}
	return json.RawMessage(strings.TrimSuffix(buf.String(), "\n"))
}
