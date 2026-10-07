package threadtest

import (
	"context"
	"iter"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/thread"
)

// RunTurns executes the session-level rows a backend must carry — the
// turn machinery's durable shapes, which are entries like any others
// but whose correctness depends on what the backend gives back on a
// reload: parent links that leave the append order (a resume's join
// attaches to an earlier entry), and receipts that are restored from
// the file alone. open returns fresh storage for each row. Backends
// call it beside Run.
func RunTurns(t *testing.T, open func(t *testing.T) thread.Storage) {
	t.Run("MixedBatchResume", mixedBatchResume(open))
	t.Run("QueuedSendRestored", queuedSendRestored(open))
}

// scripted plays fixed model turns: each a list of events. It records
// how many calls it served.
type scripted struct {
	turns [][]core.ModelEvent
	calls atomic.Int32
}

func (m *scripted) Info() core.ModelInfo {
	return core.ModelInfo{Provider: "threadtest", Name: "scripted"}
}

func (m *scripted) Stream(_ context.Context, _ core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	n := int(m.calls.Add(1)) - 1
	return func(yield func(core.ModelEvent, error) bool) {
		if n >= len(m.turns) {
			yield(core.ModelTextDelta{Text: "(script exhausted)"}, nil)
			yield(core.ModelFinish{Reason: core.StopEndTurn}, nil)
			return
		}
		for _, ev := range m.turns[n] {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

func say(text string) []core.ModelEvent {
	return []core.ModelEvent{
		core.ModelTextDelta{Text: text},
		core.ModelFinish{Reason: core.StopEndTurn, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
	}
}

// mixedBatchResume: one step calls a plain tool and an approval-gated
// one, so the parking turn records a partial tool message; the resume
// completes it. The completed message replaces the partial one on the
// active path (ADR 0011 §7), the boundary closes after one resume, and
// a session reloaded from the backend reads the same context — one
// tool message carrying both results.
func mixedBatchResume(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		ctx := context.Background()
		st := open(t)
		model := &scripted{turns: [][]core.ModelEvent{
			{
				core.ModelToolCall{ID: "call_s", Name: "safe", Args: []byte(`{}`)},
				core.ModelToolCall{ID: "call_d", Name: "dangerous", Args: []byte(`{}`)},
				core.ModelFinish{Reason: core.StopToolCalls, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
			},
			say("both done"),
			say("next reply"),
		}}
		agent := core.New(model,
			core.Tool("safe", "runs at once", func(context.Context, struct{}) (string, error) { return "safe result", nil }),
			core.Tool("dangerous", "needs a human", func(context.Context, struct{}) (string, error) { return "dangerous result", nil },
				core.RequireApproval()),
		)
		s, err := thread.Create(ctx, st, agent)
		if err != nil {
			t.Fatal(err)
		}
		t1, err := s.Send(ctx, core.User("do both"))
		if err != nil {
			t.Fatal(err)
		}
		if res, err := t1.Wait(); err != nil || len(res.Pending) != 1 {
			t.Fatalf("the parking turn: %v, %v", res, err)
		}
		// The decision is made by a session loaded from the backend.
		if err := s.Close(ctx); err != nil {
			t.Fatal(err)
		}
		s2, err := thread.Open(ctx, st, s.ID(), agent)
		if err != nil {
			t.Fatal(err)
		}
		rt, err := s2.Decide(ctx, thread.Approve("call_d"))
		if err != nil || rt == nil {
			t.Fatalf("Decide = %v, %v", rt, err)
		}
		res, err := rt.Wait()
		if err != nil {
			t.Fatal(err)
		}
		next, err := s2.Send(ctx, core.User("and then"))
		if err != nil {
			t.Fatal(err)
		}
		res3, err := next.Wait()
		if err != nil {
			t.Fatalf("the send after the resume: %v", err)
		}
		if res3.Text() != "next reply" || model.calls.Load() != 3 {
			t.Errorf("reply %q after %d model calls; want the third call's reply — one resume, then the send", res3.Text(), model.calls.Load())
		}
		if err := s2.Close(ctx); err != nil {
			t.Fatal(err)
		}
		s3, err := thread.Open(ctx, st, s.ID(), agent)
		if err != nil {
			t.Fatal(err)
		}
		if p := s3.Pending(); len(p) != 0 {
			t.Errorf("Pending after the resume, reloaded = %+v", p)
		}
		got := s3.Context()
		if len(got) < len(res.Messages) || !reflect.DeepEqual(got[:len(res.Messages)], res.Messages) {
			t.Errorf("the reloaded context does not start with the resume's transcript:\n got %+v\nwant %+v", got, res.Messages)
		}
		tools := 0
		for _, m := range got {
			if m.Role == core.RoleTool {
				tools++
				if len(m.Content) != 2 {
					t.Errorf("the tool message carries %d results, want both", len(m.Content))
				}
			}
		}
		if tools != 1 {
			t.Errorf("%d tool messages in the reloaded context, want 1", tools)
		}
	}
}

// queuedSendRestored: an accepted receipt whose prompt entry never
// landed is a send its writer never got to (ADR 0011 §4). A session
// loaded from the backend lists it, runs nothing on its own, and
// Continue runs it under the id the receipt promised.
func queuedSendRestored(open func(t *testing.T) thread.Storage) func(*testing.T) {
	return func(t *testing.T) {
		ctx := context.Background()
		st := open(t)
		model := &scripted{turns: [][]core.ModelEvent{say("picked up")}}
		agent := core.New(model)
		s, err := thread.Create(ctx, st, agent)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Close(ctx); err != nil {
			t.Fatal(err)
		}
		msg := core.User("the send a stopped writer left queued")
		prompt := thread.NewEntryID()
		if err := st.Append(ctx, s.ID(), thread.ReceiptEntry{
			ID: thread.NewEntryID(), Created: time.Now().UTC(),
			Status: thread.ReceiptAccepted, Msg: &msg, Turn: prompt, RunID: s.ID() + "-t1",
		}); err != nil {
			t.Fatal(err)
		}
		s2, err := thread.Open(ctx, st, s.ID(), agent)
		if err != nil {
			t.Fatal(err)
		}
		q := s2.Queue()
		if len(q) != 1 || q[0].Receipt != prompt || q[0].Policy != thread.Queue || q[0].Msg.Text() != msg.Text() {
			t.Fatalf("Queue after the reload = %+v, want the accepted send", q)
		}
		if n := model.calls.Load(); n != 0 {
			t.Fatalf("Open ran %d model calls", n)
		}
		turn, err := s2.Continue(ctx)
		if err != nil || turn == nil {
			t.Fatalf("Continue = %v, %v", turn, err)
		}
		if res, err := turn.Wait(); err != nil || res.Text() != "picked up" {
			t.Fatalf("the restored send: %v, %v", res, err)
		}
		if turn.ID() != prompt || turn.RunID() != s.ID()+"-t1" {
			t.Errorf("the restored turn is %q / %q, want the receipt's %q / -t1", turn.ID(), turn.RunID(), prompt)
		}
		if err := s2.Close(ctx); err != nil {
			t.Fatal(err)
		}
		s3, err := thread.Open(ctx, st, s.ID(), agent)
		if err != nil {
			t.Fatal(err)
		}
		if q := s3.Queue(); len(q) != 0 {
			t.Errorf("Queue after the send ran = %+v, want empty", q)
		}
		// The next run id continues past the restored one.
		model.turns = append(model.turns, say("and again"))
		after, err := s3.Send(ctx, core.User("again"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := after.Wait(); err != nil {
			t.Fatal(err)
		}
		if after.RunID() != s.ID()+"-t2" {
			t.Errorf("the next run id = %q, want -t2", after.RunID())
		}
	}
}
