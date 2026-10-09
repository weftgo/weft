package obsdb

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/weftgo/weft/core"
)

// TestAssembleStep pins ADR 0029's reader over ADR 0028 §8's records:
// a request naming a growth record carries the growth up to it; one
// naming a view carries the growth below the view with its range
// replaced, and only that request (a later step's plain ref ignores
// it); a session marker is never applied; a request whose count the
// records do not rebuild is ErrStepMessages; a run without request
// records falls back to the cut rule (Derived); a step the run never
// reached is ErrNotFound.
func TestAssembleStep(t *testing.T) {
	body := func(msgs ...core.Message) json.RawMessage {
		b, err := json.Marshal(msgs)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	call := core.Message{Role: core.RoleAssistant, Content: []core.Part{core.ToolCallPart{ID: "c1", Name: "lookup", Args: []byte(`{}`)}}}
	result := core.Message{Role: core.RoleTool, Content: []core.Part{core.ToolResultPart{CallID: "c1", Name: "lookup", Content: "t1"}}}
	batches := []TranscriptBatch{
		{Index: 0, Step: 0, Input: true, Messages: body(core.User("u1"))},
		{Index: 1, Step: 0, Messages: body(call)},
		{Index: 2, Step: 0, Messages: body(result)},
		{Index: 4, Step: 1, Messages: body(core.Assistant("a2"))},
		{Index: 6, Step: 2, Messages: body(core.Assistant("a3"))},
	}
	ref := func(i int64) *int64 { return &i }
	req := func(index int64, step int, at int64, count int) RequestRecord {
		return RequestRecord{Index: index, Step: step, Body: RequestBody{MessagesRef: RequestMessagesRef{Index: ref(at), Count: count}}}
	}
	view := Compaction{Scope: CompactionRun, Index: 3, Step: 1, FromSeq: 1, ToSeq: 3, Messages: body(core.User("s")), Replaced: 2, Entries: 1}
	marker := Compaction{Scope: CompactionSession, Index: -1, Step: -1, Replaced: 9, Entries: 1}
	reqs := []RequestRecord{req(0, 0, 0, 1), req(1, 1, 3, 2), req(2, 2, 4, 4)}
	cs := []Compaction{view, marker}
	texts := func(sm StepMessages) []string {
		var out []string
		for _, m := range sm.Messages {
			out = append(out, string(m.Role)+":"+m.Text())
		}
		return out
	}
	for _, c := range []struct {
		step   int
		want   []string
		viewed bool
	}{
		{0, []string{"user:u1"}, false},
		{1, []string{"user:u1", "user:s"}, true},
		{2, []string{"user:u1", "assistant:", "tool:", "assistant:a2"}, false},
	} {
		sm, err := AssembleStep(batches, reqs, cs, c.step)
		if err != nil {
			t.Fatalf("step %d: %v", c.step, err)
		}
		if got := texts(sm); len(got) != len(c.want) || (sm.View != nil) != c.viewed || sm.Derived {
			t.Errorf("step %d = %v (view %v, derived %v), want %v (view %v)", c.step, got, sm.View != nil, sm.Derived, c.want, c.viewed)
		} else {
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("step %d message %d = %s, want %s", c.step, i, got[i], c.want[i])
				}
			}
		}
	}
	// A retried step answers from its last attempt.
	if sm, err := AssembleStep(batches, append([]RequestRecord{req(5, 1, 3, 2)}, req(9, 1, 2, 3)), cs, 1); err != nil || sm.View != nil || len(sm.Messages) != 3 {
		t.Errorf("last attempt = %+v %v, want the plain ref of 3", sm, err)
	}
	// A count the records do not rebuild: a growth record is missing.
	if _, err := AssembleStep(batches[:2], reqs, cs, 2); !isHole(err, HoleGap) {
		t.Errorf("missing growth = %v, want ErrStepMessages", err)
	}
	// No request records (a run before ADR 0028): the cut rule.
	sm, err := AssembleStep(batches, nil, nil, 2)
	if err != nil || !sm.Derived || len(sm.Messages) != 4 {
		t.Errorf("cut rule = %+v %v, want 4 derived messages", sm, err)
	}
	if _, err := AssembleStep(batches, nil, nil, 3); !errors.Is(err, ErrNotFound) {
		t.Errorf("step past the last = %v, want ErrNotFound", err)
	}
	if _, err := AssembleStep(batches, reqs, cs, 7); !errors.Is(err, ErrNotFound) {
		t.Errorf("unreached step = %v, want ErrNotFound", err)
	}
	// A view that does not fit is ErrStepMessages, never a guess.
	bad := view
	bad.ToSeq = 9
	if _, err := AssembleStep(batches, reqs, []Compaction{bad}, 1); !isHole(err, HoleGap) {
		t.Errorf("unfit view = %v, want ErrStepMessages (gap)", err)
	}
	// The growth record a plain ref names must be stored: a lost view
	// (here: step 1's request names index 3, and the view is gone with
	// the count still matching a 1:1 replacement) never passes as the
	// original transcript.
	lost := []RequestRecord{req(1, 1, 3, 3)}
	if _, err := AssembleStep(batches, lost, nil, 1); !isHole(err, HoleGap) {
		t.Errorf("a ref to an unstored record = %v, want ErrStepMessages (gap)", err)
	}
	// A rewritten request whose view the core could not build: no index,
	// unstripped, messages captured — the growth is not what it saw.
	rewrote := []RequestRecord{{Index: 1, Step: 1, Body: RequestBody{MessagesRef: RequestMessagesRef{Count: 2}}}}
	if _, err := AssembleStep(batches, rewrote, nil, 1); !isHole(err, HoleGap) {
		t.Errorf("rewritten without a view = %v, want ErrStepMessages (gap)", err)
	}
	// ...but a step-0 request of a run fed nothing carried nothing.
	empty := []RequestRecord{{Index: 0, Step: 0, Body: RequestBody{MessagesRef: RequestMessagesRef{Count: 0}}}}
	if sm, err := AssembleStep(batches[1:], empty, nil, 0); err != nil || len(sm.Messages) != 0 {
		t.Errorf("empty input = %+v %v, want no messages", sm, err)
	}
	// Content off: a stripped request and no messages records.
	stripped := []RequestRecord{{Index: 0, Step: 0, Content: HoleStripped, Body: RequestBody{MessagesRef: RequestMessagesRef{Count: 1}}}}
	if _, err := AssembleStep(nil, stripped, nil, 0); !isHole(err, HoleStripped) {
		t.Errorf("content off = %v, want ErrStepMessages (stripped)", err)
	}
}

func isHole(err error, h Hole) bool {
	var e *StepMessagesError
	return errors.Is(err, ErrStepMessages) && errors.As(err, &e) && e.Hole == h
}
