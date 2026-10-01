package runtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
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

// TestThreadTurnMessages pins path 1 (thread storage): the n-th
// turn's messages are the MessageEntries and CustomMessageEntries
// between the (n−1)-th and n-th TurnEntry; a steered user message
// inside a turn does not open a false one.
func TestThreadTurnMessages(t *testing.T) {
	now := time.Now().Round(0)
	entries := []thread.Entry{
		thread.MessageEntry{ID: "e1", Created: now, Message: weft.User("where is my order?")},
		thread.MessageEntry{ID: "e2", Created: now, Message: weft.Assistant("checking")},
		thread.MessageEntry{ID: "e3", Created: now, Message: weft.User("and the tracking link?")}, // steered, turn 1
		thread.MessageEntry{ID: "e4", Created: now, Message: weft.Assistant("it shipped")},
		thread.TurnEntry{ID: "e5", Created: now, RunID: "s_01M3-t1"},
		thread.MessageEntry{ID: "e6", Created: now, Message: weft.User("thanks")},
		thread.MessageEntry{ID: "e7", Created: now, Message: weft.Assistant("anytime")},
		thread.TurnEntry{ID: "e8", Created: now, RunID: "s_01M3-t2"},
	}
	msgs, err := threadTurnMessages(entries, "s_01M3-t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 4 || msgs[0].Content[0].(weft.TextPart).Text != "where is my order?" {
		t.Errorf("turn 1 = %d messages, want 4 (prompt, assistant, steered user, assistant)", len(msgs))
	}
	msgs, err = threadTurnMessages(entries, "s_01M3-t2")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Errorf("turn 2 = %d messages, want 2", len(msgs))
	}
	if _, err := threadTurnMessages(entries, "s_01M3-t3"); err == nil {
		t.Error("turn 3 accepted, want not found")
	}
}

// TestDecodeBodies pins the obsdb and Studio paths' shared decoder:
// one JSON array of weft.Message per body, nulls skipped.
func TestDecodeBodies(t *testing.T) {
	b1, _ := json.Marshal([]weft.Message{weft.User("a"), weft.Assistant("b")})
	b2, _ := json.Marshal([]weft.Message{{Role: weft.RoleTool,
		Content: []weft.Part{weft.ToolResultPart{CallID: "c1", Content: "ok"}}}})
	msgs, err := decodeBodies([]json.RawMessage{b1, json.RawMessage("null"), b2})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 || msgs[2].Role != weft.RoleTool {
		t.Errorf("decoded %d messages, want 3 with a tool message last", len(msgs))
	}
	if _, err := decodeBodies([]json.RawMessage{json.RawMessage("not json")}); err == nil {
		t.Error("a malformed body was accepted")
	}
}

// TestTranscriptFromThreadMemory runs path 1 end to end over
// thread.Memory(): a session with two turns resolves by run id.
func TestTranscriptFromThreadMemory(t *testing.T) {
	st := thread.Memory()
	agent := weft.New(wefttest.Script(wefttest.Say("answer")), weft.Name("a"))
	sess, err := thread.Create(context.Background(), st, agent)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := sess.Send(context.Background(), weft.User("first"))
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if _, err := turn.Wait(); err != nil { // the TurnEntry lands when the turn ends
		t.Fatalf("turn: %v", err)
	}
	// The run id thread minted for turn 1.
	runID := sess.ID() + "-t1"
	msgs, err := transcriptFromThread(context.Background(), st, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) == 0 {
		t.Error("turn 1 resolved to no messages")
	}
	if _, err := transcriptFromThread(context.Background(), st, "not-a-thread-id"); err == nil {
		t.Error("a non-thread id resolved")
	}
}
