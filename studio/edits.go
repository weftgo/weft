package studio

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
	linkruntime "github.com/weftgo/weft/studio/runtime"
)

// Transcript-edit validation (WEFT-PLAYGROUND §1's Transcript knob,
// D2/D3): Studio rejects an edit that would leave a call without a
// result or a kept prefix that does not end at a step boundary (400,
// §10.4's table) — Repair would otherwise synthesize or drop results
// silently. The runtime's copy re-validates before its accepted ack
// (its transcript is the one the run feeds on); this file keeps the
// HTTP side honest against Studio's own database.
//
// The counting is the part that must not drift:
//
//   - the run's INPUT record — the conversation it was fed plus the
//     turn's own prompt (D1) — is context, never a step: an assistant
//     message of an earlier turn is not a step of this run;
//   - every other record is what the run's steps added, and it says
//     which: the step it joined, as the core stamped it on the record
//     (weft.step.index, ADR 0028 §8). Step N is the run's (N+1)th model
//     call. A resumed run's rebuilt tool message is step 0's; a steered
//     message the step that just finished;
//   - from_step N keeps the input and steps 0..N−1 and runs step N
//     fresh; an edit names one of the kept steps, and a tool-result
//     patch is scoped to the step it names (call ids are a step's own
//     — a deterministic model reuses them).
//
// The split is the input flag (stored, or inferred by a backend
// without the attribute). Only a transcript with a record that carries
// no step (a ClickHouse row from before 0004, a producer that never
// stamped one) is numbered by order — each assistant message opens the
// next step, what precedes the first is step 0's — the same rule as
// weft/runtime's orderSteps, so the two never disagree on a command.

// stepMessage is one of the run's own messages and the step it joined.
type stepMessage struct {
	step int
	msg  core.Message
}

// runSteps returns the run's own messages (every record but the input
// one), each with its step: the stored step when every record carries
// one, else the order walk.
func runSteps(batches []obsdb.TranscriptBatch) ([]stepMessage, error) {
	stored := true
	var out []stepMessage
	for _, b := range batches {
		if b.Input || len(b.Messages) == 0 || string(b.Messages) == "null" {
			continue
		}
		var batch []core.Message
		if err := json.Unmarshal(b.Messages, &batch); err != nil {
			return nil, fmt.Errorf("messages body: %w", err)
		}
		if b.Step < 0 {
			stored = false
		}
		for _, m := range batch {
			out = append(out, stepMessage{step: b.Step, msg: m})
		}
	}
	if !stored {
		at := -1
		for i := range out {
			if out[i].msg.Role == core.RoleAssistant {
				at++
			}
			out[i].step = max(at, 0)
		}
	}
	return out, nil
}

// stepCount is how many steps the run recorded: one past the last
// step holding an assistant message (each step's model call writes
// one).
func stepCount(steps []stepMessage) int {
	n := 0
	for _, m := range steps {
		if m.msg.Role == core.RoleAssistant && m.step+1 > n {
			n = m.step + 1
		}
	}
	return n
}

// compactedRange is the run-scope view from_step's request carried
// (ADR 0028 §8), placed over the run's own messages: the transcript
// seqs [from, to) it replaced (a seq counts the input's messages too —
// inputLen of them precede the run's own) by entries messages. The
// replay prefix is that view (ADR 0029), so an edit inside the range
// edits a message the model never saw at from_step: refused.
type compactedRange struct {
	inputLen, from, to, entries int
}

// compactedRangeOf places sm's view, nil when the step's request
// carried the plain transcript.
func compactedRangeOf(batches []obsdb.TranscriptBatch, sm obsdb.StepMessages) *compactedRange {
	if sm.View == nil {
		return nil
	}
	n := 0
	for _, b := range batches {
		if !b.Input || len(b.Messages) == 0 || string(b.Messages) == "null" {
			continue
		}
		var batch []json.RawMessage
		if json.Unmarshal(b.Messages, &batch) == nil {
			n += len(batch)
		}
	}
	return &compactedRange{inputLen: n, from: int(sm.View.FromSeq), to: int(sm.View.ToSeq), entries: sm.View.Entries}
}

// compactedEditError is the refusal of an edit to a message the
// compacted prefix no longer holds — weft/runtime's copy words it
// identically (edits.go's compactedEditError; both tests pin the text).
func compactedEditError(what string, step, fromStep int, c *compactedRange) error {
	return fmt.Errorf("%s of step %d was compacted away before step %d's request (messages [%d, %d) replaced by %d): the model never saw it there; edit from an earlier from_step",
		what, step, fromStep, c.from, c.to, c.entries)
}

// validateTranscriptEdits applies the edits to the source run's kept
// prefix in memory and reports the first rule they break, in the
// runtime's own words (editedPrefix).
func validateTranscriptEdits(input []core.Message, steps []stepMessage, fromStep int, edits []linkruntime.TranscriptEdit, view *compactedRange, schemas schemaOf) error {
	_, _, err := editedPrefix(input, steps, fromStep, edits, view, schemas)
	return err
}

// schemaOf resolves the input schema a tool_args edit of a call made at
// step is checked against: nil when none is at hand (the arguments need
// only be an object).
type schemaOf func(step int, name string) json.RawMessage

func (f schemaOf) of(step int, name string) json.RawMessage {
	if f == nil {
		return nil
	}
	return f(step, name)
}

// Edit kinds (ADR 0029 §8) — weft/runtime's names. An edit without one
// is read as before F2: tool_result + call_id is a result patch,
// content a reply rewrite.
const (
	editToolResult = "tool_result"
	editReply      = "reply"
	editUser       = "user"
	editToolArgs   = "tool_args"
	editInsert     = "insert"
)

// editKindOf resolves an edit's kind and checks that it carries the
// fields its kind takes, no other — weft/runtime's editKind, word for
// word.
func editKindOf(e linkruntime.TranscriptEdit) (string, error) {
	kind := e.Kind
	if kind == "" {
		switch {
		case e.ToolResult != "" && e.Content != "":
			return "", fmt.Errorf("an edit is one thing: tool_result (patch a result) or content (rewrite the reply), not both")
		case e.ToolResult != "":
			kind = editToolResult
		case e.Content != "":
			kind = editReply
		case hasArgs(e.Args):
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
		"args": hasArgs(e.Args), "index": e.Index != 0,
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

// hasArgs reports whether an edit carries arguments: empty or JSON
// null is absent — a client that serialises "args": null on every edit
// sends none, and a tool_args edit needs an object.
func hasArgs(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && string(t) != "null"
}

// article is the indefinite article an edit kind takes in a refusal.
func article(kind string) string {
	if kind == editInsert {
		return "an"
	}
	return "a"
}

// insertAt is one insert edit placed: before transcript seq seq (the
// input's messages counted), the message to add.
type insertAt struct {
	seq int
	msg core.Message
}

// editedPrefix is weft/runtime's applyEdits over Studio's records: the
// kept prefix — the run's input, then its own messages through step
// from_step − 1 — with every edit but the inserts applied, and the
// inserts placed (seq in the prefix's own numbering), before any
// compaction view is spliced in. input is the run's input record
// (runInput), steps its own messages (runSteps); view is the
// compaction from_step's request carried (nil: none) — an edit inside
// its range is refused; schemas resolves a tool's input schema for the
// step whose call an args edit rewrites (Server.editSchemas; nil, or a
// tool it has none for: the arguments need only be an object). A nil error
// means the patched prefix is complete: every kept call answered, the
// cut at a step boundary. Every refusal is the runtime's sentence.
func editedPrefix(input []core.Message, steps []stepMessage, fromStep int, edits []linkruntime.TranscriptEdit, view *compactedRange, schemas schemaOf) ([]core.Message, []insertAt, error) {
	if len(edits) == 0 {
		return nil, nil, nil
	}
	if fromStep <= 0 {
		return nil, nil, fmt.Errorf("transcript_edits need from_step > 0 (0 re-runs the whole turn, nothing is kept)")
	}
	// from_step at the step count is fresh only when the last kept step
	// ended in answered tool calls (weft/runtime's endsInAnsweredCalls).
	if n := stepCount(steps); fromStep > n || fromStep == n && !endsInAnsweredCalls(steps) {
		return nil, nil, fmt.Errorf("from_step %d is beyond the source run's last step (it recorded %d; a run past the end has nothing fresh to answer)",
			fromStep, n)
	}
	cut := cutTranscriptAtStep(steps, fromStep)
	// Copied deep enough to patch: the parts slices are the decoded
	// transcript's.
	in := make([]core.Message, len(input))
	for i, m := range input {
		in[i] = core.Message{Role: m.Role, Content: append([]core.Part(nil), m.Content...)}
	}
	kept := make([]stepMessage, cut)
	for i, m := range steps[:cut] {
		kept[i] = stepMessage{step: m.step, msg: core.Message{Role: m.msg.Role, Content: append([]core.Part(nil), m.msg.Content...)}}
	}
	var inserts []insertAt
	for _, e := range edits {
		kind, err := editKindOf(e)
		if err != nil {
			return nil, nil, err
		}
		if e.Step < 0 {
			return nil, nil, fmt.Errorf("edit step %d is negative", e.Step)
		}
		if kind == editInsert {
			if e.Step > fromStep {
				return nil, nil, fmt.Errorf("insert step %d is past from_step %d: an insert lands at a step boundary 0..%d", e.Step, fromStep, fromStep)
			}
			seq := len(in) + cutTranscriptAtStep(kept, e.Step)
			if view != nil && seq > view.from && seq < view.to {
				return nil, nil, fmt.Errorf("the boundary before step %d was compacted away before step %d's request (messages [%d, %d) replaced by %d): the model never saw it there; insert outside the range",
					e.Step, fromStep, view.from, view.to, view.entries)
			}
			inserts = append(inserts, insertAt{seq: seq, msg: core.User(e.Content)})
			continue
		}
		if e.Step >= fromStep {
			return nil, nil, fmt.Errorf("edit step %d is not in the kept prefix (from_step %d keeps steps 0..%d)",
				e.Step, fromStep, fromStep-1)
		}
		switch kind {
		case editToolResult:
			at := patchTranscriptResult(kept, e.Step, e.CallID, e.ToolResult)
			if len(at) == 0 {
				return nil, nil, fmt.Errorf("no tool call %q in the kept prefix's step %d", e.CallID, e.Step)
			}
			for _, i := range at {
				if view.holds(i) {
					return nil, nil, compactedEditError(fmt.Sprintf("call %q", e.CallID), e.Step, fromStep, view)
				}
			}
		case editReply:
			at := checkRewrite(kept, e.Step)
			if at < 0 {
				return nil, nil, fmt.Errorf("step %d has no assistant reply in the kept prefix (or it carried tool calls: patch their results instead)", e.Step)
			}
			if view.holds(at) {
				return nil, nil, compactedEditError("the reply", e.Step, fromStep, view)
			}
			kept[at].msg.Content = []core.Part{core.TextPart{Text: e.Content}}
		case editUser:
			seqs := userSeqs(in, kept, e.Step)
			if e.Index >= len(seqs) {
				if len(seqs) == 0 {
					return nil, nil, fmt.Errorf("step %d has no user message in the kept prefix", e.Step)
				}
				return nil, nil, fmt.Errorf("step %d has %d user message(s) in the kept prefix: index %d is out of range", e.Step, len(seqs), e.Index)
			}
			seq := seqs[e.Index]
			if view.holds(seq - len(in)) {
				return nil, nil, compactedEditError("the user message", e.Step, fromStep, view)
			}
			if seq < len(in) {
				in[seq].Content = rewriteText(in[seq].Content, e.Content)
			} else {
				kept[seq-len(in)].msg.Content = rewriteText(kept[seq-len(in)].msg.Content, e.Content)
			}
		case editToolArgs:
			mi, pi, name := findCall(kept, e.Step, e.CallID)
			if mi < 0 {
				return nil, nil, fmt.Errorf("no tool call %q in the kept prefix's step %d", e.CallID, e.Step)
			}
			if err := obsdb.CheckToolArgs(name, schemas.of(e.Step, name), e.Args); err != nil {
				return nil, nil, err
			}
			if view.holds(mi) {
				return nil, nil, compactedEditError(fmt.Sprintf("call %q", e.CallID), e.Step, fromStep, view)
			}
			var buf bytes.Buffer
			_ = json.Compact(&buf, e.Args) // CheckToolArgs read it as JSON
			c := kept[mi].msg.Content[pi].(core.ToolCallPart)
			c.Args = json.RawMessage(buf.Bytes())
			kept[mi].msg.Content[pi] = c
		}
	}
	// §1's boundary rule over the kept steps: every call answered.
	answered := map[string]bool{}
	for _, m := range kept {
		if m.msg.Role != core.RoleTool {
			continue
		}
		for _, p := range m.msg.Content {
			if tr, ok := p.(core.ToolResultPart); ok {
				answered[tr.CallID] = true
			}
		}
	}
	for _, m := range kept {
		if m.msg.Role != core.RoleAssistant {
			continue
		}
		for _, p := range m.msg.Content {
			if c, ok := p.(core.ToolCallPart); ok && !answered[c.ID] {
				return nil, nil, fmt.Errorf("the kept prefix leaves call %q (%s) without a result: from_step must end at a step boundary", c.ID, c.Name)
			}
		}
	}
	out := make([]core.Message, 0, len(in)+len(kept))
	out = append(out, in...)
	for _, m := range kept {
		out = append(out, m.msg)
	}
	return out, inserts, nil
}

// runInput returns the run's input record's messages — what the run
// was fed: the conversation before the turn and the turn's prompt.
func runInput(batches []obsdb.TranscriptBatch) ([]core.Message, error) {
	var out []core.Message
	for _, b := range batches {
		if !b.Input || len(b.Messages) == 0 || string(b.Messages) == "null" {
			continue
		}
		var batch []core.Message
		if err := json.Unmarshal(b.Messages, &batch); err != nil {
			return nil, fmt.Errorf("messages body: %w", err)
		}
		out = append(out, batch...)
	}
	return out, nil
}

// userSeqs is weft/runtime's userMessages: the prefix seqs of step's
// user messages — for step 0 the turn's prompt first (the input's last
// message when it is a user message), then the user messages the
// step's records hold.
func userSeqs(input []core.Message, steps []stepMessage, step int) []int {
	var out []int
	if n := len(input); step == 0 && n > 0 && input[n-1].Role == core.RoleUser {
		out = append(out, n-1)
	}
	for i, m := range steps {
		if m.msg.Role == core.RoleUser && m.step == step {
			out = append(out, len(input)+i)
		}
	}
	return out
}

// rewriteText is weft/runtime's: the first text part takes text, the
// other text parts go, every other part stays.
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

// findCall locates call callID in step's assistant message: the
// message's index, the part's, and the tool's name (-1 for none).
func findCall(steps []stepMessage, step int, callID string) (mi, pi int, name string) {
	for i, m := range steps {
		if m.msg.Role != core.RoleAssistant || m.step != step {
			continue
		}
		for j, p := range m.msg.Content {
			if c, ok := p.(core.ToolCallPart); ok && c.ID == callID {
				return i, j, c.Name
			}
		}
	}
	return -1, -1, ""
}

// placeInserts is weft/runtime's: the inserts added to seen (the
// prefix as the model sees it) before their boundaries, moved past a
// spliced view's range (v nil: none).
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

// endsInAnsweredCalls reports whether the run's own messages end in a
// step whose assistant message made tool calls that all have results
// after it — weft/runtime's copy of the same name.
func endsInAnsweredCalls(steps []stepMessage) bool {
	last := -1
	for i, m := range steps {
		if m.msg.Role == core.RoleAssistant {
			last = i
		}
	}
	if last < 0 {
		return false
	}
	answered := map[string]bool{}
	for _, m := range steps[last+1:] {
		for _, p := range m.msg.Content {
			if tr, ok := p.(core.ToolResultPart); ok && m.msg.Role == core.RoleTool {
				answered[tr.CallID] = true
			}
		}
	}
	calls := 0
	for _, p := range steps[last].msg.Content {
		if c, ok := p.(core.ToolCallPart); ok {
			calls++
			if !answered[c.ID] {
				return false
			}
		}
	}
	return calls > 0
}

// cutTranscriptAtStep is §5.1's from_step cut over a run's own steps:
// the index of step fromStep's assistant message (its model call), so
// what lies before it is steps 0..fromStep−1 complete — weft/runtime's
// cutAt.
func cutTranscriptAtStep(steps []stepMessage, fromStep int) int {
	if fromStep < 0 {
		fromStep = 0
	}
	for i, m := range steps {
		if m.msg.Role == core.RoleAssistant && m.step >= fromStep {
			return i
		}
	}
	return len(steps)
}

// holds reports whether the run's own message i lies inside the
// replaced range (false for no view).
func (c *compactedRange) holds(i int) bool {
	if c == nil {
		return false
	}
	seq := c.inputLen + i
	return seq >= c.from && seq < c.to
}

// patchTranscriptResult patches the result of call callID inside step
// — scoped to the step, like the runtime's patchResult — and returns
// the indices of the messages it patched (none: no such call).
func patchTranscriptResult(steps []stepMessage, step int, callID, content string) []int {
	var patched []int
	for mi := range steps {
		if steps[mi].msg.Role != core.RoleTool || steps[mi].step != step {
			continue
		}
		parts := steps[mi].msg.Content
		for pi := range parts {
			tr, ok := parts[pi].(core.ToolResultPart)
			if !ok || tr.CallID != callID {
				continue
			}
			tr.Content = content
			tr.IsError = false
			parts[pi] = tr
			patched = append(patched, mi)
		}
	}
	return patched
}

// checkRewrite returns the index of step's assistant message when it
// exists and carries no tool calls (dropping them would orphan their
// results), -1 otherwise.
func checkRewrite(steps []stepMessage, step int) int {
	for i, m := range steps {
		if m.msg.Role != core.RoleAssistant || m.step != step {
			continue
		}
		for _, p := range m.msg.Content {
			if _, ok := p.(core.ToolCallPart); ok {
				return -1
			}
		}
		return i
	}
	return -1
}
