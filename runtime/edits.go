package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
)

// Transcript edits (WEFT-PLAYGROUND §1's Transcript knob, D2/D3): a
// command that continues from step N may rewrite one of the kept
// steps' assistant replies (a plain-text content edit) or patch one of
// its tool results (the "what if the API returned 429?" counterfactual).
//
// The rule the whole file enforces (§1): an edit may not leave a tool
// call without a result, and the kept prefix must end at a step
// boundary — Repair would otherwise synthesize or drop results
// silently, and the experiment would run on a transcript nobody
// wrote. Both surfaces reject with the named reasons; the runtime's
// copy is authoritative (§10.4).
//
// Steps are the source run's own: each message belongs to the step its
// messages record stored (weft.step.index, ADR 0028 §8 — a resumed
// run's rebuilt tool message is step 0's, a steered message the step
// that just finished), numbered by order only for a source without
// stored steps (orderSteps). An edit names a step of the run, never a
// turn of the context.

// keptPrefix is §5.1's from_step semantics over a resolved source: the
// run's whole input, then its steps through step from_step − 1 (their
// tool results included). from_step 0 keeps the input alone — plus
// whatever the run recorded before its first model call (a resumed
// run's completed tool message), which is not a step's to re-run. It
// refuses a prefix that leaves one of the kept steps' calls without a
// result: Repair would synthesize one, and the experiment would run on
// a transcript nobody wrote.
//
// When step from_step's request carried a compaction view (ADR 0028
// §8), the prefix is what that request carried — the view's range
// replaced (ADR 0029): the replay feeds the model its exact input.
// from_step 0 re-runs the turn from what it was fed; no view applies.
func keptPrefix(src *sourceRun, fromStep int) ([]core.Message, error) {
	cut := src.cut(fromStep)
	if err := prefixComplete(src.steps[:cut]); err != nil {
		return nil, err
	}
	out := make([]core.Message, 0, len(src.input)+cut)
	out = append(out, src.input...)
	return src.seenAt(fromStep, append(out, src.steps[:cut]...))
}

// seenAt applies the source's view to prefix when it is from_step's
// (and from_step > 0).
func (s *sourceRun) seenAt(fromStep int, prefix []core.Message) ([]core.Message, error) {
	if s.view == nil || fromStep <= 0 || s.view.step != fromStep {
		return prefix, nil
	}
	return s.seen(prefix)
}

// compactedEditError is the refusal of an edit to a message the
// compacted prefix no longer holds — Studio's copy words it identically
// (studio/edits.go's compactedEditError; both tests pin the text).
func compactedEditError(what string, step, fromStep int, v *stepView) error {
	return fmt.Errorf("%s of step %d was compacted away before step %d's request (messages [%d, %d) replaced by %d): the model never saw it there; edit from an earlier from_step",
		what, step, fromStep, v.c.FromSeq, v.c.ToSeq, v.c.Entries)
}

// applyTranscriptEdits returns the kept prefix (the input, then the
// steps through step from_step − 1) with the edits applied, no schema
// known (applyEdits with none).
func applyTranscriptEdits(src *sourceRun, fromStep int, edits []transcriptEdit) ([]core.Message, error) {
	return applyEdits(src, fromStep, edits, nil)
}

// Edit kinds (ADR 0029 §8). An edit without one is read as before F2:
// tool_result + call_id is a result patch, content a reply rewrite.
const (
	editToolResult = "tool_result"
	editReply      = "reply"
	editUser       = "user"
	editToolArgs   = "tool_args"
	editInsert     = "insert"
)

// editKind resolves an edit's kind and checks that it carries the
// fields its kind takes, no other — Studio's copy (studio/edits.go's
// editKindOf) words every refusal identically.
func editKind(e transcriptEdit) (string, error) {
	kind := e.Kind
	if kind == "" {
		switch {
		case e.ToolResult != "" && e.Content != "":
			return "", fmt.Errorf("an edit is one thing: tool_result (patch a result) or content (rewrite the reply), not both")
		case e.ToolResult != "":
			kind = editToolResult
		case e.Content != "":
			kind = editReply
		case len(e.Args) > 0:
			return "", fmt.Errorf("an edit with args needs kind %q", editToolArgs)
		default:
			return "", fmt.Errorf("an empty edit (neither tool_result nor content)")
		}
	}
	var takes []string
	switch kind {
	case editToolResult:
		takes = []string{"call_id", "tool_result"}
	case editReply, editInsert:
		takes = []string{"content"}
	case editUser:
		takes = []string{"content", "index"}
	case editToolArgs:
		takes = []string{"call_id", "args"}
	default:
		return "", fmt.Errorf("unknown edit kind %q (tool_result, reply, user, tool_args or insert)", kind)
	}
	carried := map[string]bool{
		"tool_result": e.ToolResult != "", "call_id": e.CallID != "", "content": e.Content != "",
		"args": len(e.Args) > 0, "index": e.Index != 0,
	}
	for _, f := range []string{"tool_result", "call_id", "content", "args", "index"} {
		if carried[f] && !slices.Contains(takes, f) {
			return "", fmt.Errorf("%s %s edit does not take %s", article(kind), kind, f)
		}
	}
	for _, f := range takes {
		if f != "index" && !carried[f] {
			return "", fmt.Errorf("%s %s edit needs %s", article(kind), kind, f)
		}
	}
	if e.Index < 0 {
		return "", fmt.Errorf("edit index %d is negative", e.Index)
	}
	return kind, nil
}

// article is the indefinite article an edit kind takes in a refusal.
func article(kind string) string {
	if kind == editInsert {
		return "an"
	}
	return "a"
}

// compactedBoundaryError is the refusal of an insert at a boundary the
// compacted prefix no longer has — Studio's copy words it identically.
func compactedBoundaryError(step, fromStep int, from, to int64, entries int) error {
	return fmt.Errorf("the boundary before step %d was compacted away before step %d's request (messages [%d, %d) replaced by %d): the model never saw it there; insert outside the range",
		step, fromStep, from, to, entries)
}

// insertAt is one insert edit placed: before transcript seq seq (the
// input's messages counted), the message to add.
type insertAt struct {
	seq int
	msg core.Message
}

// applyEdits returns the kept prefix (the input, then the steps
// through step from_step − 1) with the edits applied — what the replay
// is fed (ADR 0029 §8). schemas holds each tool's input schema the
// tool_args edits are checked against (nil, or a tool absent: the
// arguments need only be a JSON object). It rejects:
//
//   - an edit to a step the fresh run re-executes (step >= from_step;
//     an insert's boundary may be from_step itself, the prefix's end);
//   - a tool-result patch or an args rewrite whose call_id is not in
//     the named kept step;
//   - a reply rewrite of a step whose assistant message carried tool
//     calls (dropping them would orphan their results silently — patch
//     the results instead);
//   - a user rewrite of a step with no user message at that index (step
//     0's are the turn's prompt, then what the step's records hold);
//   - arguments that are not an object or do not fit the tool's schema
//     (obsdb.CheckToolArgs: INVALID_INPUT, the field named);
//   - an edit whose kind is unknown or that carries a field its kind
//     does not take (one edit, one meaning);
//   - a kept prefix that leaves a call without a result (from_step
//     must land on a step boundary);
//   - an edit to a message step from_step's compaction view replaced,
//     or an insert at a boundary inside it: the replay prefix is what
//     the model saw (ADR 0029), and the model never saw that there.
func applyEdits(src *sourceRun, fromStep int, edits []transcriptEdit, schemas map[string]json.RawMessage) ([]core.Message, error) {
	if len(edits) == 0 {
		return keptPrefix(src, fromStep)
	}
	if fromStep <= 0 {
		return nil, fmt.Errorf("transcript_edits need from_step > 0 (0 re-runs the whole turn, nothing is kept)")
	}
	cut := src.cut(fromStep)
	stepOf := src.stepIndex()[:cut]
	// The input and the kept steps are copied deep enough to patch: the
	// source is shared by every reader of this command.
	input := make([]core.Message, len(src.input))
	for i, m := range src.input {
		input[i] = core.Message{Role: m.Role, Content: append([]core.Part(nil), m.Content...)}
	}
	steps := make([]core.Message, cut)
	for i, m := range src.steps[:cut] {
		steps[i] = core.Message{Role: m.Role, Content: append([]core.Part(nil), m.Content...)}
	}

	var view *stepView
	if src.view != nil && src.view.step == fromStep {
		view = src.view
	}
	inView := func(i int) bool { return view.holds(len(input) + i) }
	var inserts []insertAt
	for _, e := range edits {
		kind, err := editKind(e)
		if err != nil {
			return nil, err
		}
		if e.Step < 0 {
			return nil, fmt.Errorf("edit step %d is negative", e.Step)
		}
		if kind == editInsert {
			if e.Step > fromStep {
				return nil, fmt.Errorf("insert step %d is past from_step %d: an insert lands at a step boundary 0..%d", e.Step, fromStep, fromStep)
			}
			seq := len(input) + cutAt(steps, stepOf, e.Step)
			if view != nil && int64(seq) > view.c.FromSeq && int64(seq) < view.c.ToSeq {
				return nil, compactedBoundaryError(e.Step, fromStep, view.c.FromSeq, view.c.ToSeq, view.c.Entries)
			}
			inserts = append(inserts, insertAt{seq: seq, msg: core.User(e.Content)})
			continue
		}
		if e.Step >= fromStep {
			return nil, fmt.Errorf("edit step %d is not in the kept prefix (from_step %d keeps steps 0..%d)",
				e.Step, fromStep, fromStep-1)
		}
		switch kind {
		case editToolResult:
			at := patchResult(steps, stepOf, e.Step, e.CallID, e.ToolResult)
			if len(at) == 0 {
				return nil, fmt.Errorf("no tool call %q in the kept prefix's step %d", e.CallID, e.Step)
			}
			for _, i := range at {
				if inView(i) {
					return nil, compactedEditError(fmt.Sprintf("call %q", e.CallID), e.Step, fromStep, view)
				}
			}
		case editReply:
			at := rewriteReply(steps, stepOf, e.Step, e.Content)
			if at < 0 {
				return nil, fmt.Errorf("step %d has no assistant reply in the kept prefix (or it carried tool calls: patch their results instead)", e.Step)
			}
			if inView(at) {
				return nil, compactedEditError("the reply", e.Step, fromStep, view)
			}
		case editUser:
			seqs := userMessages(input, steps, stepOf, e.Step)
			if e.Index >= len(seqs) {
				return nil, userIndexError(e.Step, e.Index, len(seqs))
			}
			seq := seqs[e.Index]
			if view.holds(seq) {
				return nil, compactedEditError("the user message", e.Step, fromStep, view)
			}
			if seq < len(input) {
				input[seq].Content = rewriteText(input[seq].Content, e.Content)
			} else {
				steps[seq-len(input)].Content = rewriteText(steps[seq-len(input)].Content, e.Content)
			}
		case editToolArgs:
			name := callName(steps, stepOf, e.Step, e.CallID)
			if name == "" {
				return nil, fmt.Errorf("no tool call %q in the kept prefix's step %d", e.CallID, e.Step)
			}
			if err := obsdb.CheckToolArgs(name, schemas[name], e.Args); err != nil {
				return nil, err
			}
			at := rewriteArgs(steps, stepOf, e.Step, e.CallID, e.Args)
			if inView(at) {
				return nil, compactedEditError(fmt.Sprintf("call %q", e.CallID), e.Step, fromStep, view)
			}
		}
	}
	if err := prefixComplete(steps); err != nil {
		return nil, err
	}
	out := make([]core.Message, 0, len(input)+cut+len(inserts))
	out = append(out, input...)
	seen, err := src.seenAt(fromStep, append(out, steps...))
	if err != nil {
		return nil, err
	}
	if view == nil {
		return placeInserts(seen, inserts, nil), nil
	}
	return placeInserts(seen, inserts, &view.c), nil
}

// userIndexError is the refusal of a user edit naming a user message
// the step does not hold — Studio's copy words it identically.
func userIndexError(step, index, n int) error {
	if n == 0 {
		return fmt.Errorf("step %d has no user message in the kept prefix", step)
	}
	return fmt.Errorf("step %d has %d user message(s) in the kept prefix: index %d is out of range", step, n, index)
}

// userMessages returns the transcript seqs (the input's messages
// counted) of step's user messages, in order: for step 0 the turn's
// prompt first — the input's last message when it is a user message —
// then the user messages the step's records hold (a steer delivered
// after its tool batch; a resumed turn's prompt in its step-0 tail).
func userMessages(input, steps []core.Message, stepOf []int, step int) []int {
	var out []int
	if n := len(input); step == 0 && n > 0 && input[n-1].Role == core.RoleUser {
		out = append(out, n-1)
	}
	for i, m := range steps {
		if m.Role == core.RoleUser && stepOf[i] == step {
			out = append(out, len(input)+i)
		}
	}
	return out
}

// rewriteText replaces a message's text with text: the first text part
// takes it, the other text parts go, every other part (an image, a
// file) stays where it was. A message with no text part gains one
// first.
func rewriteText(parts []core.Part, text string) []core.Part {
	out := make([]core.Part, 0, len(parts)+1)
	placed := false
	for _, p := range parts {
		if _, ok := p.(core.TextPart); ok {
			if !placed {
				out = append(out, core.TextPart{Text: text})
				placed = true
			}
			continue
		}
		out = append(out, p)
	}
	if !placed {
		out = append([]core.Part{core.TextPart{Text: text}}, out...)
	}
	return out
}

// callName is the tool name of call callID in step's assistant
// message, "" for no such call.
func callName(steps []core.Message, stepOf []int, step int, callID string) string {
	for mi, m := range steps {
		if m.Role != core.RoleAssistant || stepOf[mi] != step {
			continue
		}
		for _, p := range m.Content {
			if c, ok := p.(core.ToolCallPart); ok && c.ID == callID {
				return c.Name
			}
		}
	}
	return ""
}

// capMark joins tokens with commas inside limit bytes: when they do not
// all fit, the ones that do are followed by "+<n> more".
func capMark(tokens []string, limit int) string {
	if all := strings.Join(tokens, ","); len(all) <= limit {
		return all
	}
	out := ""
	for i, t := range tokens {
		next := t
		if out != "" {
			next = out + "," + t
		}
		if len(next)+len(fmt.Sprintf(",+%d more", len(tokens)-i-1)) > limit {
			if out == "" {
				return fmt.Sprintf("+%d more", len(tokens)-i)
			}
			return out + fmt.Sprintf(",+%d more", len(tokens)-i)
		}
		out = next
	}
	return out
}

// rewriteArgs replaces call callID's arguments in step's assistant
// message (compacted JSON) and returns the message's index, -1 for no
// such call. The call keeps its id, name and signature; its result is
// untouched (patch it too to change it).
func rewriteArgs(steps []core.Message, stepOf []int, step int, callID string, args json.RawMessage) int {
	var buf bytes.Buffer
	if err := json.Compact(&buf, args); err != nil {
		return -1 // CheckToolArgs refused it first
	}
	for mi := range steps {
		if steps[mi].Role != core.RoleAssistant || stepOf[mi] != step {
			continue
		}
		for pi, p := range steps[mi].Content {
			if c, ok := p.(core.ToolCallPart); ok && c.ID == callID {
				c.Args = json.RawMessage(buf.Bytes())
				steps[mi].Content[pi] = c
				return mi
			}
		}
	}
	return -1
}

// placeInserts adds the insert edits to seen — the prefix as the model
// sees it — each before its boundary's seq, moved past a compaction
// view's range when one was spliced in (v: [FromSeq, ToSeq) became
// Entries messages; a boundary inside it was refused). Inserts at one
// boundary keep the edits' order.
func placeInserts(seen []core.Message, inserts []insertAt, v *obsdb.Compaction) []core.Message {
	if len(inserts) == 0 {
		return seen
	}
	pos := func(seq int) int {
		if v != nil && int64(seq) >= v.ToSeq {
			return seq - int(v.ToSeq-v.FromSeq) + v.Entries
		}
		return seq
	}
	sorted := slices.Clone(inserts)
	slices.SortStableFunc(sorted, func(a, b insertAt) int { return pos(a.seq) - pos(b.seq) })
	out := make([]core.Message, 0, len(seen)+len(inserts))
	next := 0
	for i, m := range seen {
		for next < len(sorted) && pos(sorted[next].seq) <= i {
			out = append(out, sorted[next].msg)
			next++
		}
		out = append(out, m)
	}
	for ; next < len(sorted); next++ {
		out = append(out, sorted[next].msg)
	}
	return out
}

// editsMark is the replayed run's record of its edits — the
// weft.edits metadata value (ADR 0029 §8): one token per edit, in the
// command's order, comma-joined: <step>:<call_id>:result,
// <step>:<call_id>:args (the "args edited" mark on the pair),
// <step>:reply, <step>:user (<step>:user:<index> past the first) and
// <step>:insert. Metadata values are capped at 1024 bytes (core's
// Metadata): past it the list ends in "+<n> more" — visible, never a
// silent cut.
func editsMark(edits []transcriptEdit) string {
	tokens := make([]string, 0, len(edits))
	for _, e := range edits {
		kind, err := editKind(e)
		if err != nil {
			continue // validated before the run; never reached
		}
		switch kind {
		case editToolResult:
			tokens = append(tokens, fmt.Sprintf("%d:%s:result", e.Step, e.CallID))
		case editToolArgs:
			tokens = append(tokens, fmt.Sprintf("%d:%s:args", e.Step, e.CallID))
		case editUser:
			if e.Index > 0 {
				tokens = append(tokens, fmt.Sprintf("%d:user:%d", e.Step, e.Index))
			} else {
				tokens = append(tokens, fmt.Sprintf("%d:user", e.Step))
			}
		default:
			tokens = append(tokens, fmt.Sprintf("%d:%s", e.Step, kind))
		}
	}
	return capMark(tokens, 1024)
}

// patchResult replaces one call's result content in place — the result
// of call callID inside step (stepOf[i] is the step steps[i] joined) —
// and returns the indices of the messages it patched (none: no such
// call). Scoped to the step: call ids are only unique within a run's
// step, and a deterministic model reuses them.
func patchResult(steps []core.Message, stepOf []int, step int, callID, content string) []int {
	var patched []int
	for mi := range steps {
		if steps[mi].Role != core.RoleTool || stepOf[mi] != step {
			continue
		}
		for pi := range steps[mi].Content {
			tr, ok := steps[mi].Content[pi].(core.ToolResultPart)
			if !ok || tr.CallID != callID {
				continue
			}
			tr.Content = content
			tr.IsError = false
			steps[mi].Content[pi] = tr
			patched = append(patched, mi)
		}
	}
	return patched
}

// rewriteReply replaces one step's assistant message with a plain-text
// reply and returns its index, -1 for none. It refuses (-1) a message
// that carried tool calls: their results would become orphans Repair
// drops silently.
func rewriteReply(steps []core.Message, stepOf []int, step int, content string) int {
	for mi := range steps {
		if steps[mi].Role != core.RoleAssistant || stepOf[mi] != step {
			continue
		}
		for _, p := range steps[mi].Content {
			if _, ok := p.(core.ToolCallPart); ok {
				return -1
			}
		}
		steps[mi].Content = []core.Part{core.TextPart{Text: content}}
		return mi
	}
	return -1
}

// prefixComplete is §1's boundary rule over the kept steps: every tool
// call they made has a result among them. A from_step that lands
// mid-step (or a source that never finished one — a parked or failed
// run) fails here instead of letting Repair synthesize results.
func prefixComplete(steps []core.Message) error {
	answered := map[string]bool{}
	for _, m := range steps {
		if m.Role != core.RoleTool {
			continue
		}
		for _, p := range m.Content {
			if tr, ok := p.(core.ToolResultPart); ok {
				answered[tr.CallID] = true
			}
		}
	}
	for _, m := range steps {
		if m.Role != core.RoleAssistant {
			continue
		}
		for _, p := range m.Content {
			c, ok := p.(core.ToolCallPart)
			if !ok || answered[c.ID] {
				continue
			}
			return fmt.Errorf("the kept prefix leaves call %q (%s) without a result: from_step must end at a step boundary", c.ID, c.Name)
		}
	}
	return nil
}

// recorded is one recorded tool result: what the source run's call
// answered, and whether it answered with an error.
type recorded struct {
	content string
	isError bool
}

// substitutes is the substitute mode's lookup (§6 rule 3): the source
// run's recorded results by (tool, canonical args), each key a queue
// in the order the source made the calls — a tool called twice with
// the same arguments answered twice, and the re-run gets the answers
// in that order, never the last one for both.
type substitutes struct {
	byKey map[string][]recorded
}

// take answers one call from the record: the next recorded result of
// that (tool, args), ok false on a miss. The last result of a key
// stays — a call the re-run repeats beyond the record's count is
// answered like the last recorded one (the handler still never runs).
func (s *substitutes) take(name string, args json.RawMessage) (recorded, bool) {
	key := callKey(name, args)
	q := s.byKey[key]
	if len(q) == 0 {
		return recorded{}, false
	}
	if len(q) > 1 {
		s.byKey[key] = q[1:]
	}
	return q[0], true
}

// recordedCalls indexes the source's calls for the substitute mode.
// fresh are the steps the run re-executes (from from_step on) — their
// results queue first, in order, because those are the calls the re-run
// is making again; kept are the steps before it, whose results answer
// only a key the fresh steps never recorded (the model re-issuing a
// call the kept prefix already made). Only calls with a result count.
func recordedCalls(fresh, kept []core.Message) *substitutes {
	s := &substitutes{byKey: map[string][]recorded{}}
	index := func(msgs []core.Message, skip map[string]bool) map[string]bool {
		seen := map[string]bool{}
		// A result pairs with the call of the assistant message before
		// it, never by id across the whole run: call ids are a step's
		// own, and a deterministic model reuses them step after step.
		var calls []core.ToolCallPart
		for _, m := range msgs {
			switch m.Role {
			case core.RoleAssistant:
				calls = calls[:0]
				for _, p := range m.Content {
					if c, ok := p.(core.ToolCallPart); ok {
						calls = append(calls, c)
					}
				}
			case core.RoleTool:
				for _, c := range calls {
					for _, p := range m.Content {
						tr, ok := p.(core.ToolResultPart)
						if !ok || tr.CallID != c.ID {
							continue
						}
						key := callKey(c.Name, c.Args)
						if !skip[key] {
							s.byKey[key] = append(s.byKey[key], recorded{content: tr.Content, isError: tr.IsError})
							seen[key] = true
						}
						break
					}
				}
			}
		}
		return seen
	}
	index(kept, index(fresh, nil))
	return s
}

// callKey is the substitute match: the tool's name and its arguments
// in canonical JSON — object keys sorted, insignificant whitespace
// gone, numbers as written — so a call that differs only in key order
// or spacing still matches its record (a miss never re-fires, it only
// parks; this keeps a re-serialising model from parking every call).
// Arguments that are not JSON match byte for byte.
func callKey(name string, args json.RawMessage) string {
	return name + "\x00" + canonicalArgs(args)
}

func canonicalArgs(args json.RawMessage) string {
	if len(bytes.TrimSpace(args)) == 0 {
		return "{}"
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return string(args)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return string(args)
	}
	return string(out)
}
