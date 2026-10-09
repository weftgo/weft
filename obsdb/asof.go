package obsdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/weftgo/weft/core"
)

// StepMessages is the messages one step's model call carried — what
// the model saw at that step (ADR 0028 §8, ADR 0029): the replay
// prefix for from_step Step, the "compacted messages" a fixture keys
// on. Step is the step asked for. View is the run-scope compaction view
// the step's request named (its messages_ref.index), nil when the
// request carried the plain transcript; Messages is then the growth
// records up to the view's index with its range replaced, else the
// growth records up to the request's own index. Derived is true when no
// request record placed the messages — a run written before ADR 0028,
// or a request whose messages_ref a content-off chain stripped — and
// the from_step cut rule did instead: the transcript before step Step's
// assistant message (no view can apply there: nothing names one).
type StepMessages struct {
	Step     int
	Messages []core.Message
	View     *Compaction
	Derived  bool
}

// ErrStepMessages is wrapped by MessagesAsOf and AssembleStep when the
// stored records do not rebuild the messages a request names: a growth
// record missing below its index, a view that does not fit the
// transcript, a body that is not messages. A reader badges it derived
// (or gap) and never guesses.
var ErrStepMessages = errors.New("obsdb: the records do not rebuild the step's messages")

// MessagesAsOf answers the messages step step of run runID's model call
// carried (StepMessages), read through TranscriptBatches, Requests and
// Compactions — one assembly (AssembleStep) for every backend and every
// reader: Studio's transcript route with ?step=, weft/runtime's replay
// prefix, the wefttest fixtures. ErrNotFound for an unknown run and for
// a step the run never reached (no request record and no assistant
// message of that step); an error wrapping ErrStepMessages when the
// records do not rebuild what the request names.
//
// A session-scope compaction marker is never applied: the run's input
// record already holds the compacted context literally. A run-scope
// view applies only to the request that names it, never to a later
// step's.
func MessagesAsOf(ctx context.Context, db DB, runID string, step int) (StepMessages, error) {
	if step < 0 {
		return StepMessages{}, fmt.Errorf("obsdb: step %d: %w", step, ErrNotFound)
	}
	batches, err := db.TranscriptBatches(ctx, runID)
	if err != nil {
		return StepMessages{}, err
	}
	reqs, err := db.Requests(ctx, runID, RequestQuery{Step: &step, Limit: 1000})
	if err != nil && !errors.Is(err, ErrNotFound) {
		return StepMessages{}, err
	}
	var cs []Compaction
	if hasViewRef(reqs) {
		if cs, err = db.Compactions(ctx, runID); err != nil {
			return StepMessages{}, err
		}
	}
	return AssembleStep(batches, reqs, cs, step)
}

func hasViewRef(reqs []RequestRecord) bool {
	for _, r := range reqs {
		if r.Body.MessagesRef.Index != nil {
			return true
		}
	}
	return false
}

// AssembleStep is MessagesAsOf over records already read: the run's
// growth batches (TranscriptBatches), its request records (any steps;
// the answering — last — attempt of step is used) and its compactions.
func AssembleStep(batches []TranscriptBatch, requests []RequestRecord, compactions []Compaction, step int) (StepMessages, error) {
	type placed struct {
		index int64
		step  int
		input bool
		msg   core.Message
	}
	var all []placed
	stored := true
	for _, b := range batches {
		if len(b.Messages) == 0 || string(b.Messages) == "null" {
			continue
		}
		var batch []core.Message
		if err := json.Unmarshal(b.Messages, &batch); err != nil {
			return StepMessages{}, fmt.Errorf("messages record %d: %v: %w", b.Index, err, ErrStepMessages)
		}
		if !b.Input && b.Step < 0 {
			stored = false
		}
		for _, m := range batch {
			all = append(all, placed{index: b.Index, step: b.Step, input: b.Input, msg: m})
		}
	}
	upTo := func(keep func(index int64) bool) []core.Message {
		out := make([]core.Message, 0, len(all))
		for _, p := range all {
			if keep(p.index) {
				out = append(out, p.msg)
			}
		}
		return out
	}

	var rec *RequestRecord
	for i := range requests {
		r := &requests[i]
		if r.Step == step && (rec == nil || r.Index > rec.Index) {
			rec = r
		}
	}
	if rec != nil && rec.Body.MessagesRef.Index != nil {
		ref := *rec.Body.MessagesRef.Index
		out := StepMessages{Step: step}
		if v, ok := ViewOf(*rec, compactions); ok {
			seen, err := ApplyView(upTo(func(i int64) bool { return i < ref }), v)
			if err != nil {
				return StepMessages{}, fmt.Errorf("step %d: %v: %w", step, err, ErrStepMessages)
			}
			out.Messages, out.View = seen, &v
		} else {
			out.Messages = upTo(func(i int64) bool { return i <= ref })
		}
		if n := rec.Body.MessagesRef.Count; n != len(out.Messages) {
			return StepMessages{}, fmt.Errorf("step %d's request names %d messages, the stored records rebuild %d (a messages record is missing): %w",
				step, n, len(out.Messages), ErrStepMessages)
		}
		return out, nil
	}

	// No request record places the messages: the cut rule — the input,
	// then the run's own messages before step's assistant message, each
	// at its stored step (numbered by order when a record stored none).
	steps := make([]int, len(all))
	at := -1
	for i, p := range all {
		steps[i] = p.step
		if !stored && !p.input {
			if p.msg.Role == core.RoleAssistant {
				at++
			}
			steps[i] = max(at, 0)
		}
	}
	cut, found := len(all), rec != nil
	for i, p := range all {
		if p.input || p.msg.Role != core.RoleAssistant || steps[i] < step {
			continue
		}
		if i < cut {
			cut = i
		}
		if steps[i] == step {
			found = true
		}
	}
	if !found {
		return StepMessages{}, fmt.Errorf("obsdb: no step %d: %w", step, ErrNotFound)
	}
	if len(all) == 0 {
		if rec != nil && rec.Content == "" && rec.Body.MessagesRef.Count == 0 {
			// A step-0 request of a run fed nothing: it carried no
			// messages, and no record was written for an empty input.
			return StepMessages{Step: step, Messages: []core.Message{}}, nil
		}
		return StepMessages{}, fmt.Errorf("step %d: the run stored no messages (content capture off?): %w", step, ErrStepMessages)
	}
	out := StepMessages{Step: step, Derived: true, Messages: make([]core.Message, 0, cut)}
	for _, p := range all[:cut] {
		out.Messages = append(out.Messages, p.msg)
	}
	return out, nil
}

// ViewOf returns the run-scope compaction view a request record names:
// the one whose index is the request's messages_ref.index. ok is false
// for a request that carried the plain transcript (its ref names a
// growth record) or names nothing.
func ViewOf(rec RequestRecord, compactions []Compaction) (Compaction, bool) {
	ref := rec.Body.MessagesRef.Index
	if ref == nil {
		return Compaction{}, false
	}
	for _, c := range compactions {
		if c.Scope == CompactionRun && c.Index == *ref {
			return c, true
		}
	}
	return Compaction{}, false
}

// ApplyView rebuilds the messages a compacted request carried (ADR
// 0028 §8): transcript — the growth records below the view's index,
// concatenated — with the range [FromSeq, ToSeq) replaced by the view's
// messages. A range that does not fit the transcript, or a body that is
// not messages, is an error.
func ApplyView(transcript []core.Message, v Compaction) ([]core.Message, error) {
	if v.FromSeq < 0 || v.FromSeq > v.ToSeq || v.ToSeq > int64(len(transcript)) {
		return nil, fmt.Errorf("compaction view %d replaces [%d, %d) of a %d-message transcript", v.Index, v.FromSeq, v.ToSeq, len(transcript))
	}
	var entries []core.Message
	if err := json.Unmarshal(v.Messages, &entries); err != nil {
		return nil, fmt.Errorf("compaction view %d is not readable as messages: %w", v.Index, err)
	}
	out := make([]core.Message, 0, len(transcript)-int(v.ToSeq-v.FromSeq)+len(entries))
	out = append(out, transcript[:v.FromSeq]...)
	out = append(out, entries...)
	return append(out, transcript[v.ToSeq:]...), nil
}
