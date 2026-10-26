package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
// Two runs, aligned by the SOURCE's step ordinal (decision 8: the
// ordinal is the step's id everywhere, here the alignment key; A8,
// UX-C-04). A replay numbers its own steps from 0 — its step 0 is the
// fresh step from_step of its source — so when one side's run row
// carries weft.forked_from "<other side>#<n>" (weft/runtime stamps it,
// ADR 0029) that side's step k aligns with the other's step n+k, and
// the source's steps before n are rows marked kept: not re-run, the
// replay's prefix (the replay side null, nothing compared, never a
// change). The response says so in aligned (which side is the replay
// and its from_step), and each row names both sides' own ordinals
// (a_step, b_step) beside the aligned one (step) — a link to a side's
// step is by its own ordinal. Runs with no such lineage align by
// their own ordinals, as before. Two replays of one source from
// different steps (aligned replay "both") have rows between their
// from_steps where one side kept the source's step and the other re-ran
// it: kept_by names the keeping side, whose column is the source's step,
// compared with the re-run — a change there is a change (first_changed
// too); a source Studio cannot read leaves those rows not compared
// (every column unknown). Each step assembled
// exactly as GET /api/runs/{id}/steps/{n} assembles it (assembleStep),
// then compared field by field: the system prompt (by hash when both
// sides have one), the tool calls as name(args) with the arguments
// canonicalised (sorted keys), the tool results (content and is_error,
// in call order), the assistant text (exact), the usage (the numbers).
// A step one run has and the other lacks is changed with "missing".
// A usage difference where a side reported no tokens at all (the
// scripted engine's 0→0, UX-D-15) is not a change: the column is listed
// in the row's differs, the numbers kept on both sides.
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

// diffAlign is the response's alignment when the two runs share a
// lineage (weft.forked_from "<source>#<from_step>", followed up the
// chain — a replay of a replay included): Source is the common run
// whose ordinals the rows are, AFrom and BFrom the source ordinal each
// side's step 0 is (0 for the source itself), Replay the side that
// replays the other ("a" or "b"), or "both" for two replays of a third
// run (siblings, maybe from different steps). null when the two align
// by their own ordinals.
type diffAlign struct {
	Replay string `json:"replay"`
	Source string `json:"source"`
	AFrom  int    `json:"a_from"`
	BFrom  int    `json:"b_from"`
}

// diffDoc is GET /api/diff.
type diffDoc struct {
	A diffRun `json:"a"`
	B diffRun `json:"b"`
	// Aligned is the lineage alignment (diffAlign), null without one.
	Aligned *diffAlign  `json:"aligned"`
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
	// Kept counts the kept rows: the source's steps before the replay's
	// from_step (0 without a lineage alignment).
	Kept int `json:"kept"`
}

// diffRow is one aligned step ordinal (the source's, under a lineage
// alignment): each side's step (null when that run has no such step)
// and what differs. AStep and BStep are each side's own ordinal (null
// when the side has no step here): equal to Step without an
// alignment, the replay's own k = Step − from_step with one. Kept
// marks a source step before the replay's from_step: the replay did
// not re-run it (its side null), so nothing was compared and the row
// is no change.
type diffRow struct {
	Step  int  `json:"step"`
	AStep *int `json:"a_step"`
	BStep *int `json:"b_step"`
	Kept  bool `json:"kept"`
	// KeptBy names the side ("a" or "b") that kept the common source's
	// step at this row while the other side re-ran it: two replays of
	// one source from different steps (aligned replay "both"), the rows
	// between their from_steps. That side's column holds the SOURCE's
	// step (its kept copy; its own ordinal null) and the re-run side is
	// compared with it — a change counts, first_changed included. When
	// the source cannot be read (scope, a database error), the kept
	// side stays null and every column is unknown: not compared, never
	// kept, never the same.
	KeptBy  string    `json:"kept_by,omitempty"`
	Changed bool      `json:"changed"`
	A       *diffSide `json:"a"`
	B       *diffSide `json:"b"`
	Changes []string  `json:"changes"`
	// Unknown lists the columns that could not be compared because a
	// side did not record them (its holes say why) — neither changed
	// nor the same; [] when every column compared.
	Unknown []string `json:"unknown"`
	// Differs lists the columns that differ but do not count as a
	// change: usage when a side reported no tokens (the scripted
	// engine's zero usage, UX-D-15). [] otherwise.
	Differs []string `json:"differs"`
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
	// SystemHidden is true when the step's request block is withheld
	// from this token (a read-scoped panel token: System null under the
	// hidden hole) — the explicit fact lib/stepdiff.ts's systemHidden
	// reads, never inferred from the hidden hole's count (a hidden hole
	// with a count is child runs left out, and may stand beside it).
	SystemHidden bool `json:"system_hidden,omitempty"`

	// What the side could not record, for compareSides (never served:
	// each is a hole in Holes). derived: the side's run was built from
	// stock spans (derivedSide) — no column of it was recorded.
	eventsLost, contentLost, namesLost, textLost, derived bool
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
		doc.Holes = []stepHole{{Hole: string(obsdb.HoleTruncated), Cause: string(obsdb.CauseResponseCap), Reason: reason, Fix: fix}}
	}
	// The lineage offsets: side a's step k is the common source's step
	// offA+k, b's offB+k (forkChain, lineageOffsets). Rows are the
	// source's ordinals from the smaller offset on: the steps before it
	// neither side ran.
	offA, offB := 0, 0
	if b != a {
		depth := maxForkDepth
		if idFrom(r).panel != nil {
			depth = 1 // a panel token reads no ancestor row beyond the sides' own forked_from
		}
		ca := s.forkChain(r.Context(), a, ra.forkedFrom, depth)
		cb := s.forkChain(r.Context(), b, rb.forkedFrom, depth)
		if src, oa, ob, ok := lineageOffsets(ca, cb); ok {
			// A source side that is one of the two must hold the steps the
			// replay names: a from_step past its last is no alignment (a
			// malformed or stale forked_from), said by a hole.
			past := ""
			switch {
			case src == a && !ra.truncated && ob > len(ra.sides):
				past = fmt.Sprintf("%s's forked_from names step %d of %s, which recorded %d", b, ob, a, len(ra.sides))
			case src == b && !rb.truncated && oa > len(rb.sides):
				past = fmt.Sprintf("%s's forked_from names step %d of %s, which recorded %d", a, oa, b, len(rb.sides))
			}
			if past != "" {
				doc.Holes = append(doc.Holes, stepHole{Hole: string(obsdb.HoleGap),
					Reason: past + ": the two are aligned by their own ordinals",
					Fix:    "compare the replay with the run it was taken from"})
			} else {
				offA, offB = oa, ob
				replay := "both"
				switch src {
				case a:
					replay = "b"
				case b:
					replay = "a"
				}
				doc.Aligned = &diffAlign{Replay: replay, Source: src, AFrom: oa, BFrom: ob}
			}
		}
	}
	// Two replays of one source from different steps (siblings): the
	// rows between their from_steps are the source's steps one side
	// kept and the other re-ran, compared against the source read here
	// — its sides, nil when it cannot be read (srcWhy).
	var src []*diffSide
	srcRead := false
	srcSide := func(n int) *diffSide {
		if !srcRead {
			srcRead = true
			src = s.siblingSource(r, doc.Aligned.Source, prompts)
		}
		if n < 0 || n >= len(src) {
			return nil
		}
		return src[n]
	}
	at := func(sides []*diffSide, off, n int) (*diffSide, *int) {
		k := n - off
		if k < 0 || k >= len(sides) {
			return nil, nil
		}
		return sides[k], &k
	}
	for n := min(offA, offB); n < max(len(ra.sides)+offA, len(rb.sides)+offB); n++ {
		row := diffRow{Step: n, Changes: []string{}, Unknown: []string{}, Differs: []string{}}
		row.A, row.AStep = at(ra.sides, offA, n)
		row.B, row.BStep = at(rb.sides, offB, n)
		switch {
		case row.A == nil && row.B == nil:
			continue // neither side has a step here: no row
		case doc.Aligned != nil && doc.Aligned.Replay == "both" && (row.A == nil && n < offA || row.B == nil && n < offB):
			// Siblings: one side kept the source's step n, the other
			// re-ran it — compared with the source's own step.
			row.KeptBy = "a"
			if row.B == nil {
				row.KeptBy = "b"
			}
			if kept := srcSide(n); kept == nil {
				row.Unknown = []string{diffSystem, diffToolCalls, diffToolResults, diffText, diffUsage}
			} else if row.KeptBy == "a" {
				row.A = kept
				row.Changes, row.Unknown, row.Differs = compareSides(row.A, row.B)
			} else {
				row.B = kept
				row.Changes, row.Unknown, row.Differs = compareSides(row.A, row.B)
			}
		case row.A == nil && n < offA || row.B == nil && n < offB:
			// A source step before that side's from_step, the other side
			// (the source itself) holding it: kept by the replay, not
			// re-run — nothing to compare, never a change.
			row.Kept = true
			doc.Summary.Kept++
		case row.A == nil || row.B == nil:
			row.Changes = append(row.Changes, diffMissing)
		default:
			row.Changes, row.Unknown, row.Differs = compareSides(row.A, row.B)
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

// siblingSource reads the common source of two sibling replays for the
// rows one kept and the other re-ran: nil when the token may not read
// it (a panel token's other public id) or it cannot be read — the rows
// then read not compared.
func (s *Server) siblingSource(r *http.Request, id string, prompts bool) []*diffSide {
	if p := idFrom(r).panel; p != nil {
		det, err := s.db.Run(r.Context(), id)
		if err != nil || det.PublicID != p.PublicID {
			return nil
		}
	}
	src, err := s.diffSteps(r, id, prompts)
	if err != nil {
		return nil
	}
	return src.sides
}

// diffRunSteps is one run's steps, reduced.
type diffRunSteps struct {
	ref        diffRun
	sides      []*diffSide
	truncated  bool
	forkedFrom string // the run row's weft.forked_from
}

// maxForkDepth bounds a forked_from chain's walk: a replay of a replay
// of … is followed this many links up, no further.
const maxForkDepth = 8

// forkLink is one run of a forked_from chain and the offset at which
// the chain's first run's step 0 sits in that run's ordinals.
type forkLink struct {
	id  string
	off int
}

// forkChain is run id's lineage, itself first (offset 0): each
// weft.forked_from "<parent>#<n>" adds n to the offset, read up to
// depth links (each a run row read, the first from ff), stopping at a
// cycle, an unknown run, a malformed value or an offset past the diff's
// step cap.
func (s *Server) forkChain(ctx context.Context, id, ff string, depth int) []forkLink {
	chain := []forkLink{{id: id}}
	seen := map[string]bool{id: true}
	off := 0
	for range depth {
		parent, n, ok := parseForkedFrom(ff)
		if !ok || seen[parent] || off+n > maxDiffSteps {
			break
		}
		off += n
		seen[parent] = true
		chain = append(chain, forkLink{id: parent, off: off})
		det, err := s.db.Run(ctx, parent)
		if err != nil {
			break
		}
		ff = det.ForkedFrom
	}
	return chain
}

// lineageOffsets finds the nearest run both chains hold and each side's
// offset into it.
func lineageOffsets(a, b []forkLink) (source string, offA, offB int, ok bool) {
	inB := map[string]int{}
	for _, l := range b {
		inB[l.id] = l.off
	}
	for _, l := range a {
		if ob, found := inB[l.id]; found {
			return l.id, l.off, ob, true
		}
	}
	return "", 0, 0, false
}

// diffSteps assembles every step of run id (up to maxDiffSteps) and
// reduces each to its compared columns. The steps are the ordinals
// assembleStep answers, from 0 until the first it does not.
func (s *Server) diffSteps(r *http.Request, id string, prompts bool) (diffRunSteps, error) {
	sr, err := s.loadStepRun(r.Context(), id)
	if err != nil {
		return diffRunSteps{}, err
	}
	out := diffRunSteps{ref: diffRun{RunID: id, Status: string(sr.det.Status)}, forkedFrom: sr.det.ForkedFrom}
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
		side := diffSideOf(doc, texts[n], readable)
		if sr.det.Derived {
			derivedSide(side)
		}
		out.sides = append(out.sides, side)
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
	// The request block hidden (a read token) withholds the system
	// prompt: said by SystemHidden, so a hidden hole with a count (the
	// child runs' alone, hiddenChildrenHole) keeps its count beside it.
	if b, ok := doc.Request.(badgeFields); ok && b.Badge == string(obsdb.HoleHidden) {
		side.SystemHidden = true
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

// derivedSide is a step of a derived run (obsdb.RunRow.Derived,
// docs/record-contract.md §9) as the diff states it: no column was
// recorded — no events, no transcript, no request record — so every
// one is unknown against any other side (compareSides), and its one
// hole is derivedBadge's, whatever the step assembly guessed from the
// spans (a gap, an old weft) — those words would send its author to
// the wrong fix. The status and the marks stay: the chat span says them.
func derivedSide(side *diffSide) {
	side.derived = true
	b := derivedBadge()
	side.Holes = []stepHole{{Hole: b.Badge, Reason: b.Reason, Fix: b.Fix}}
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
//   - text: unknown when a side's text is null for a hole;
//   - every column: unknown when a side is a derived run's (derivedSide).
//
// Marks, holes, status and timing are never compared: a compaction or
// a subagent call marks its side and changes nothing by itself. A
// usage that differs where a side reported no tokens (zero usage: the
// scripted engine replays recorded answers and bills nothing) is in
// differs, not changes (UX-D-15): the numbers stay on both sides.
func compareSides(a, b *diffSide) (changes, unknown, differs []string) {
	changes, unknown, differs = []string{}, []string{}, []string{}
	if a.derived || b.derived {
		return changes, []string{diffSystem, diffToolCalls, diffToolResults, diffText, diffUsage}, differs
	}
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
	zero := core.Usage{}
	switch {
	case eventsKnown && a.Usage != b.Usage && (a.Usage == zero || b.Usage == zero):
		differs = append(differs, diffUsage)
	default:
		note(diffUsage, eventsKnown, a.Usage == b.Usage)
	}
	return changes, unknown, differs
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
