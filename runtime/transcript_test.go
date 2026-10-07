package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
)

// TestParseThreadRunID pins the source-run id grammar: thread mints
// "<session>-t<n>", and only that shape resolves through storage.
func TestParseThreadRunID(t *testing.T) {
	for _, tc := range []struct {
		in      string
		session string
		turn    int
		ok      bool
	}{
		{"s_01M3-t3", "s_01M3", 3, true},
		{"s_01M3-t1", "s_01M3", 1, true},
		{"s_01M3-t12", "s_01M3", 12, true},
		{"s_01M3", "", 0, false},
		{"t3", "", 0, false},
		{"s-t0", "", 0, false},
		{"s-tx", "", 0, false},
	} {
		s, n, err := parseThreadRunID(tc.in)
		if tc.ok {
			if err != nil || s != tc.session || n != tc.turn {
				t.Errorf("parseThreadRunID(%q) = %q %d %v", tc.in, s, n, err)
			}
		} else if err == nil {
			t.Errorf("parseThreadRunID(%q) accepted a non-thread id", tc.in)
		}
	}
}

// TestThreadTurnMessages pins path 1 (thread storage): turn n's steps
// are its entries from its first assistant message on, and its input
// is everything on its path before that — the earlier turns' messages
// and its own prompt; a steered user message inside a turn does not
// open a false one.
func TestThreadTurnMessages(t *testing.T) {
	now := time.Now().Round(0)
	path := []thread.Entry{
		thread.MessageEntry{ID: "e1", Created: now, Message: core.User("where is my order?")},
		thread.MessageEntry{ID: "e2", Created: now, Message: core.Assistant("checking")},
		thread.MessageEntry{ID: "e3", Created: now, Message: core.User("and the tracking link?")}, // steered, turn 1
		thread.MessageEntry{ID: "e4", Created: now, Message: core.Assistant("it shipped")},
		thread.TurnEntry{ID: "e5", Created: now, RunID: "s_01M3-t1"},
		thread.MessageEntry{ID: "e6", Created: now, Message: core.User("thanks")},
		thread.MessageEntry{ID: "e7", Created: now, Message: core.Assistant("anytime")},
		thread.TurnEntry{ID: "e8", Created: now, RunID: "s_01M3-t2"},
	}
	src, err := threadTurnMessages(path, "s_01M3-t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(src.input) != 1 || src.input[0].Text() != "where is my order?" {
		t.Errorf("turn 1 input = %+v, want its prompt alone", src.input)
	}
	if len(src.steps) != 3 || src.stepCount() != 2 {
		t.Errorf("turn 1 steps = %d messages / %d steps, want 3 / 2 (assistant, steered user, assistant)",
			len(src.steps), src.stepCount())
	}
	src, err = threadTurnMessages(path, "s_01M3-t2")
	if err != nil {
		t.Fatal(err)
	}
	// Turn 2 ran on the whole conversation: turn 1's four messages and
	// its own prompt are what the model was fed; its one step is its own.
	if len(src.input) != 5 || src.input[4].Text() != "thanks" {
		t.Errorf("turn 2 input = %d messages, want turn 1's four and the prompt", len(src.input))
	}
	if len(src.steps) != 1 || src.steps[0].Text() != "anytime" {
		t.Errorf("turn 2 steps = %+v, want its one reply", src.steps)
	}
	if _, err := threadTurnMessages(path, "s_01M3-t3"); err == nil {
		t.Error("turn 3 accepted, want not found")
	}
}

// TestDecodeBodies pins the obsdb and Studio paths' shared decoder:
// one JSON array of core.Message per body, nulls skipped; the first
// record is the run's input, the rest its steps.
func TestDecodeBodies(t *testing.T) {
	b1, _ := json.Marshal([]core.Message{core.User("a"), core.Assistant("b"), core.User("c")})
	b2, _ := json.Marshal([]core.Message{core.Assistant("d")})
	b3, _ := json.Marshal([]core.Message{{Role: core.RoleTool,
		Content: []core.Part{core.ToolResultPart{CallID: "c1", Content: "ok"}}}})
	src, err := decodeBodies([]json.RawMessage{json.RawMessage("null"), b1, b2, json.RawMessage("null"), b3})
	if err != nil {
		t.Fatal(err)
	}
	if len(src.input) != 3 || len(src.steps) != 2 || src.steps[1].Role != core.RoleTool {
		t.Errorf("decoded input %d / steps %d, want 3 / 2 with a tool message last", len(src.input), len(src.steps))
	}
	// The input's assistant message is an earlier turn's answer: the
	// run recorded one step, not two.
	if n := src.stepCount(); n != 1 {
		t.Errorf("steps = %d, want 1 (the input's assistant message is context)", n)
	}
	if _, err := decodeBodies([]json.RawMessage{json.RawMessage("not json")}); err == nil {
		t.Error("a malformed body was accepted")
	}
	if _, err := decodeBodies([]json.RawMessage{json.RawMessage("null")}); err == nil {
		t.Error("a run with no messages resolved (content off: nothing to re-run from)")
	}
}

// TestTranscriptFromThreadMemory runs path 1 end to end over
// thread.Memory(): a session with two turns resolves by run id, and
// the second turn's input carries the first.
func TestTranscriptFromThreadMemory(t *testing.T) {
	st := thread.Memory()
	agent := core.New(wefttest.Script(wefttest.Say("answer one"), wefttest.Say("answer two")), core.Name("a"))
	sess, err := thread.Create(context.Background(), st, agent)
	if err != nil {
		t.Fatal(err)
	}
	for _, prompt := range []string{"first", "second"} {
		turn, err := sess.Send(context.Background(), core.User(prompt))
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		if _, err := turn.Wait(); err != nil { // the TurnEntry lands when the turn ends
			t.Fatalf("turn: %v", err)
		}
	}
	src, err := transcriptFromThread(context.Background(), st, agent, sess.ID()+"-t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(src.input) != 1 || src.input[0].Text() != "first" || len(src.steps) != 1 {
		t.Errorf("turn 1 = input %+v steps %+v, want the prompt and one reply", src.input, src.steps)
	}
	src, err = transcriptFromThread(context.Background(), st, agent, sess.ID()+"-t2")
	if err != nil {
		t.Fatal(err)
	}
	if len(src.input) != 3 || src.input[2].Text() != "second" || len(src.steps) != 1 || src.steps[0].Text() != "answer two" {
		t.Errorf("turn 2 = input %+v steps %+v, want turn 1, the prompt, and one reply", src.input, src.steps)
	}
	if _, err := transcriptFromThread(context.Background(), st, agent, "not-a-thread-id"); err == nil {
		t.Error("a non-thread id resolved")
	}
	for _, id := range []string{"", "a b", "s/../x", "s?x=1", "s#frag", "s%2e", "a//b", strings.Repeat("x", 600)} {
		if validRunID(id) {
			t.Errorf("validRunID(%q) = true, want refused", id)
		}
	}
	for _, id := range []string{"s_01M3-t3", "pg_01M3", "s_01M3-t3/2/call_1"} {
		if !validRunID(id) {
			t.Errorf("validRunID(%q) = false, want accepted", id)
		}
	}
}
