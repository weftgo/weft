package thread_test

import (
	"context"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/thread/threadtest"
	"github.com/weftgo/weft/wefttest"
)

// mixedAgent builds the agent of the mixed-batch rows: one step calls a
// plain tool and an approval-gated one, so the step executes the first
// and parks the second — the tool message the parking run records is
// partial, and the resume completes it.
func mixedAgent(extra ...wefttest.Turn) (*weft.Agent, *wefttest.Model, *atomic.Int32) {
	steps := append([]wefttest.Turn{
		wefttest.ToolCalls(
			wefttest.Call{Name: "safe", ID: "call_s"},
			wefttest.Call{Name: "dangerous", ID: "call_d"},
		),
		wefttest.Say("both done"),
	}, extra...)
	m := wefttest.Script(steps...)
	ran := new(atomic.Int32)
	agent := weft.New(m,
		weft.Tool("safe", "runs at once", func(context.Context, struct{}) (string, error) {
			return "safe result", nil
		}),
		weft.Tool("dangerous", "needs a human", func(context.Context, struct{}) (string, error) {
			ran.Add(1)
			return "dangerous result", nil
		}, weft.RequireApproval()),
	)
	return agent, m, ran
}

// toolMessages lists the tool-role messages of the session's context.
func toolMessages(s *thread.Session) []weft.Message {
	var out []weft.Message
	for _, m := range s.Context() {
		if m.Role == weft.RoleTool {
			out = append(out, m)
		}
	}
	return out
}

// resultIDs lists a tool message's result call ids, in order.
func resultIDs(m weft.Message) []string {
	var out []string
	for _, p := range m.Content {
		if r, ok := p.(weft.ToolResultPart); ok {
			out = append(out, r.CallID)
		}
	}
	return out
}

// assertMixedClosed is the mixed batch's end state: the boundary
// closed, nothing pending, one tool message carrying both results in
// call order, and the raw path equal to the repaired one — the tree
// holds exactly what the core saw.
func assertMixedClosed(t *testing.T, s *thread.Session) {
	t.Helper()
	if p := s.Pending(); len(p) != 0 {
		t.Errorf("Pending = %+v, want none: the resume resolved the boundary", p)
	}
	tools := toolMessages(s)
	if len(tools) != 1 {
		t.Fatalf("%d tool messages in the context, want 1:\n%s", len(tools), renderContext(s))
	}
	if got := resultIDs(tools[0]); !reflect.DeepEqual(got, []string{"call_s", "call_d"}) {
		t.Errorf("tool message results = %v, want [call_s call_d]", got)
	}
	// The active path holds one tool message — the complete one — and
	// not the partial one beside it.
	path, err := s.Path(s.Leaf())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range path {
		if me, ok := e.(thread.MessageEntry); ok && me.Message.Role == weft.RoleTool {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the active path holds %d tool message entries, want 1 (the complete one replaces the partial)", n)
	}
}

// A step with one executed and one parked call: the resume's completed
// tool message replaces the partial one on the active path, so the
// boundary closes after exactly one resume, the next Send runs, and the
// context holds one tool message with both results (ADR 0011 §7).
func TestMixedBatchAutoResume(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent, model, ran := mixedAgent(wefttest.Say("next reply"))
		s, err := thread.Create(ctx, st, agent)
		if err != nil {
			t.Fatal(err)
		}
		t1, err := s.Send(ctx, weft.User("do both"))
		if err != nil {
			t.Fatal(err)
		}
		res1, err := t1.Wait()
		if err != nil {
			t.Fatal(err)
		}
		if len(res1.Pending) != 1 || res1.Pending[0].ID != "call_d" {
			t.Fatalf("Pending = %+v, want call_d alone", res1.Pending)
		}
		rt, err := s.Decide(ctx, thread.Approve("call_d"))
		if err != nil {
			t.Fatal(err)
		}
		if rt == nil {
			t.Fatal("Decide did not resume the boundary")
		}
		res, err := rt.Wait()
		if err != nil {
			t.Fatalf("resume: %v", err)
		}
		if res.Text() != "both done" {
			t.Errorf("resume reply = %q", res.Text())
		}
		if n := ran.Load(); n != 1 {
			t.Errorf("the approved call ran %d times, want 1", n)
		}
		assertMixedClosed(t, s)
		// What the tree holds is what the core saw, byte for byte.
		if got := s.Context(); !reflect.DeepEqual(got, res.Messages) {
			t.Errorf("Context differs from the resume's transcript:\n got %+v\nwant %+v", got, res.Messages)
		}
		// Exactly one resume: the settled boundary is not picked up again.
		if rt.Next() != nil {
			t.Error("the resume armed another resume")
		}
		if n := len(model.Requests()); n != 2 {
			t.Errorf("%d model calls after the resume, want 2 (the parking step and the resume's)", n)
		}
		// The next Send runs — it is not queued behind a boundary that
		// never closes.
		t3, err := s.Send(ctx, weft.User("and then"))
		if err != nil {
			t.Fatal(err)
		}
		res3, err := t3.Wait()
		if err != nil {
			t.Fatal(err)
		}
		if res3.Text() != "next reply" {
			t.Errorf("follow-up reply = %q", res3.Text())
		}
		if n := len(model.Requests()); n != 3 {
			t.Errorf("%d model calls, want 3", n)
		}
		assertMixedClosed(t, reopen(t, ctx, st, s))
		if got, want := reopen(t, ctx, st, s).Context(), s.Context(); !reflect.DeepEqual(got, want) {
			t.Errorf("the reopened context differs:\n got %+v\nwant %+v", got, want)
		}
	})
}

// The same boundary under AutoResume(false): Resume resolves it once,
// and a Send that queued behind it runs.
func TestMixedBatchManualResume(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent, model, _ := mixedAgent(wefttest.Say("next reply"))
		s, err := thread.Create(ctx, st, agent, thread.AutoResume(false))
		if err != nil {
			t.Fatal(err)
		}
		t1, _ := s.Send(ctx, weft.User("do both"))
		if _, err := t1.Wait(); err != nil {
			t.Fatal(err)
		}
		queued, err := s.Send(ctx, weft.User("and then"))
		if err != nil {
			t.Fatal(err)
		}
		if rt, err := s.Decide(ctx, thread.Approve("call_d")); err != nil || rt != nil {
			t.Fatalf("Decide = %v, %v; want no resume under AutoResume(false)", rt, err)
		}
		rt, err := s.Resume(ctx)
		if err != nil {
			t.Fatal(err)
		}
		res, err := rt.Wait()
		if err != nil {
			t.Fatalf("resume: %v", err)
		}
		res3, err := queued.Wait()
		if err != nil {
			t.Fatalf("the queued send: %v", err)
		}
		if res3.Text() != "next reply" {
			t.Errorf("queued reply = %q", res3.Text())
		}
		assertMixedClosed(t, s)
		if got := s.Context()[:len(res.Messages)]; !reflect.DeepEqual(got, res.Messages) {
			t.Errorf("Context differs from the resume's transcript:\n got %+v\nwant %+v", got, res.Messages)
		}
		if n := len(model.Requests()); n != 3 {
			t.Errorf("%d model calls, want 3", n)
		}
		if _, err := s.Resume(ctx); err == nil {
			t.Error("a second Resume found a boundary to resume")
		}
	})
}

// A reopen between the parking turn and the decision: the boundary and
// its partial tool message are entries, so the reopened session resumes
// it the same way.
func TestMixedBatchAcrossReopen(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent, model, ran := mixedAgent(wefttest.Say("next reply"))
		s, err := thread.Create(ctx, st, agent)
		if err != nil {
			t.Fatal(err)
		}
		t1, _ := s.Send(ctx, weft.User("do both"))
		if _, err := t1.Wait(); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(ctx); err != nil {
			t.Fatal(err)
		}
		s2 := reopenWith(t, ctx, st, s, agent)
		if p := s2.Pending(); len(p) != 1 || p[0].CallID != "call_d" {
			t.Fatalf("reopened Pending = %+v, want call_d", p)
		}
		rt, err := s2.Decide(ctx, thread.Approve("call_d"))
		if err != nil || rt == nil {
			t.Fatalf("Decide = %v, %v", rt, err)
		}
		res, err := rt.Wait()
		if err != nil {
			t.Fatal(err)
		}
		if ran.Load() != 1 {
			t.Errorf("the approved call ran %d times, want 1", ran.Load())
		}
		assertMixedClosed(t, s2)
		if got := s2.Context(); !reflect.DeepEqual(got, res.Messages) {
			t.Errorf("Context differs from the resume's transcript:\n got %+v\nwant %+v", got, res.Messages)
		}
		t3, _ := s2.Send(ctx, weft.User("and then"))
		if res3, err := t3.Wait(); err != nil || res3.Text() != "next reply" {
			t.Fatalf("follow-up = %v, %v", res3, err)
		}
		if n := len(model.Requests()); n != 3 {
			t.Errorf("%d model calls, want 3", n)
		}
		// And once more from the file alone.
		if err := s2.Close(ctx); err != nil {
			t.Fatal(err)
		}
		assertMixedClosed(t, reopenWith(t, ctx, st, s, agent))
	})
}

// The resume's own appends may fail too — its join (the completed tool
// message) or its closing step: the join is held and written first, in
// order, and the context still equals the resume's transcript.
func TestMixedBatchResumeSurvivesAFailedAppend(t *testing.T) {
	// The resume's appends: 1 the decision, 2 the "started" audit entry,
	// 3 the join, 4 the closing assistant message, 5 the turn's end.
	for nth := 3; nth <= 4; nth++ {
		t.Run(fmt.Sprintf("append_%d", nth), func(t *testing.T) {
			ctx := context.Background()
			st := &failNthAppend{Storage: thread.Memory()}
			agent, _, ran := mixedAgent()
			s, err := thread.Create(ctx, st, agent)
			if err != nil {
				t.Fatal(err)
			}
			t1, _ := s.Send(ctx, weft.User("do both"))
			if _, err := t1.Wait(); err != nil {
				t.Fatal(err)
			}
			st.arm(nth)
			rt, err := s.Decide(ctx, thread.Approve("call_d"))
			if err != nil || rt == nil {
				t.Fatalf("Decide = %v, %v", rt, err)
			}
			res, err := rt.Wait()
			if err != nil {
				t.Fatal(err)
			}
			if ran.Load() != 1 {
				t.Errorf("the approved call ran %d times", ran.Load())
			}
			assertMixedClosed(t, s)
			if got := s.Context(); !reflect.DeepEqual(got, res.Messages) {
				t.Errorf("Context differs from the resume's transcript:\n got %+v\nwant %+v", got, res.Messages)
			}
			tes := turnEntries(s)
			if last := tes[len(tes)-1]; last.LateSteps != 1 {
				t.Errorf("the resume's LateSteps = %d, want 1", last.LateSteps)
			}
		})
	}
}

// A message the application wrote while the boundary was open
// (CustomMessage) sits after the parked step; the core completes the
// tool message before it, and so does the tree: the join attaches to
// the assistant entry and the application's message follows it.
func TestResumeJoinKeepsMessagesWrittenWhileParked(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		name := "all_parked"
		if mixed {
			name = "mixed"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			calls := []wefttest.Call{{Name: "dangerous", ID: "call_d"}}
			if mixed {
				calls = []wefttest.Call{{Name: "safe", ID: "call_s"}, {Name: "dangerous", ID: "call_d"}}
			}
			agent := weft.New(wefttest.Script(wefttest.ToolCalls(calls...), wefttest.Say("done")),
				weft.Tool("safe", "", func(context.Context, struct{}) (string, error) { return "safe result", nil }),
				weft.Tool("dangerous", "", func(context.Context, struct{}) (string, error) { return "dangerous result", nil },
					weft.RequireApproval()),
			)
			s, _ := thread.Create(ctx, thread.Memory(), agent)
			t1, _ := s.Send(ctx, weft.User("go"))
			if _, err := t1.Wait(); err != nil {
				t.Fatal(err)
			}
			if err := s.CustomMessage(ctx, "note", weft.User("a note written while parked")); err != nil {
				t.Fatal(err)
			}
			rt, err := s.Decide(ctx, thread.Approve("call_d"))
			if err != nil || rt == nil {
				t.Fatalf("Decide = %v, %v", rt, err)
			}
			res, err := rt.Wait()
			if err != nil {
				t.Fatal(err)
			}
			if got := s.Context(); !reflect.DeepEqual(got, res.Messages) {
				t.Errorf("Context differs from the resume's transcript:\n got %+v\nwant %+v", got, res.Messages)
			}
			if p := s.Pending(); len(p) != 0 {
				t.Errorf("Pending = %+v after the resume", p)
			}
			for _, m := range s.Context() {
				for _, p := range m.Content {
					if r, ok := p.(weft.ToolResultPart); ok && r.CallID == "call_d" && r.Content != "dangerous result" {
						t.Errorf("call_d reads %q in the context, want the approved call's result", r.Content)
					}
				}
			}
		})
	}
}

// A file written before the join rule holds the partial tool message
// and the complete one side by side on the path. The boundary reads
// closed — the second message serves the call — instead of holding the
// session forever.
func TestBoundaryReadsLegacyDoubleToolMessageAsClosed(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	agent := weft.New(wefttest.Script(wefttest.Say("still alive")))
	s, _ := thread.Create(ctx, st, agent)
	at := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	call := func(id, name string) weft.ToolCallPart {
		return weft.ToolCallPart{ID: id, Name: name, Args: []byte(`{}`)}
	}
	result := func(id, name string) weft.ToolResultPart {
		return weft.ToolResultPart{CallID: id, Name: name, Content: name + " result"}
	}
	entries := []thread.Entry{
		thread.MessageEntry{ID: "e_1", Created: at, Message: weft.User("do both")},
		thread.MessageEntry{ID: "e_2", ParentID: "e_1", Created: at, Message: weft.Message{Role: weft.RoleAssistant,
			Content: []weft.Part{call("call_s", "safe"), call("call_d", "dangerous")}}},
		thread.MessageEntry{ID: "e_3", ParentID: "e_2", Created: at, Message: weft.Message{Role: weft.RoleTool,
			Content: []weft.Part{result("call_s", "safe")}}},
		thread.MessageEntry{ID: "e_4", ParentID: "e_3", Created: at, Message: weft.Message{Role: weft.RoleTool,
			Content: []weft.Part{result("call_s", "safe"), result("call_d", "dangerous")}}},
	}
	if err := s.Close(ctx); err != nil { // the old writer is gone
		t.Fatal(err)
	}
	if err := st.Append(ctx, s.ID(), entries...); err != nil {
		t.Fatal(err)
	}
	s2 := reopenWith(t, ctx, st, s, agent)
	if p := s2.Pending(); len(p) != 0 {
		t.Fatalf("Pending = %+v, want none: the second tool message serves call_d", p)
	}
	turn, err := s2.Send(ctx, weft.User("hello?"))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _, _ = turn.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the Send queued behind a boundary that is not one")
	}
	if res, err := turn.Wait(); err != nil || res.Text() != "still alive" {
		t.Fatalf("Wait = %v, %v", res, err)
	}
}

// A turn that died mid-step (a crash between the assistant message and
// its tool message) leaves a call without a result and without a turn
// entry: nothing parked it, so it is no approval boundary. The next
// Send runs — the loop's input repair answers the call — and the tree
// holds the run's transcript exactly, although the repaired input is
// one message longer than the raw path.
func TestCrashDanglingCallIsNoBoundary(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := weft.New(wefttest.Script(wefttest.Say("recovered"), wefttest.Say("and again")))
		s, _ := thread.Create(ctx, st, agent)
		at := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
		crashed := []thread.Entry{
			thread.MessageEntry{ID: "e_1", Created: at, Message: weft.User("start")},
			thread.MessageEntry{ID: "e_2", ParentID: "e_1", Created: at, Message: weft.Message{Role: weft.RoleAssistant,
				Content: []weft.Part{weft.ToolCallPart{ID: "call_x", Name: "work", Args: []byte(`{}`)}}}},
		}
		// The crashed writer is gone; what it left is in the file.
		if err := s.Close(ctx); err != nil {
			t.Fatal(err)
		}
		if err := st.Append(ctx, s.ID(), crashed...); err != nil {
			t.Fatal(err)
		}
		s2 := reopenWith(t, ctx, st, s, agent)
		if p := s2.Pending(); len(p) != 0 {
			t.Errorf("Pending = %+v, want none: a crashed call is not an approval request", p)
		}
		turn, err := s2.Send(ctx, weft.User("continue"))
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-waitDone(turn):
		case <-time.After(5 * time.Second):
			t.Fatal("the Send queued behind a crashed call nobody can decide")
		}
		res, err := turn.Wait()
		if err != nil {
			t.Fatal(err)
		}
		if got := s2.Context(); !reflect.DeepEqual(got, res.Messages) {
			t.Errorf("Context differs from the run's transcript:\n got %+v\nwant %+v", got, res.Messages)
		}
		if n := countAssistantText(s2, "recovered"); n != 1 {
			t.Errorf("the reply sits %d times on the path, want 1", n)
		}
		// And the turn after it, whose input the repair also lengthens.
		t3, _ := s2.Send(ctx, weft.User("again"))
		res3, err := t3.Wait()
		if err != nil {
			t.Fatal(err)
		}
		if got := s2.Context(); !reflect.DeepEqual(got, res3.Messages) {
			t.Errorf("Context differs from the second run's transcript:\n got %+v\nwant %+v", got, res3.Messages)
		}
	})
}

// waitDone closes its channel when the turn ends.
func waitDone(turn *thread.Turn) <-chan struct{} {
	done := make(chan struct{})
	go func() { _, _ = turn.Wait(); close(done) }()
	return done
}

// countAssistantText counts the assistant message entries on the
// active path whose text is text.
func countAssistantText(s *thread.Session, text string) int {
	path, _ := s.Path(s.Leaf())
	n := 0
	for _, e := range path {
		if me, ok := e.(thread.MessageEntry); ok && me.Message.Role == weft.RoleAssistant && me.Message.Text() == text {
			n++
		}
	}
	return n
}

// The session-level conformance rows every backend runs (threadtest):
// the mixed batch's resume and a restored queued send, from the file
// alone.
func TestTurnsConformance(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		threadtest.RunTurns(t, func(*testing.T) thread.Storage { return thread.Memory() })
	})
	t.Run("jsonl", func(t *testing.T) {
		threadtest.RunTurns(t, func(t *testing.T) thread.Storage {
			st, err := jsonl.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			return st
		})
	})
}

// A mixed batch whose parked call delegates to a pool child: the
// child's request is mirrored onto this session and decided here, the
// delegation's answer resolves the parked call, and the resume's join
// moves the active path off the line that holds the mirror and its
// decision. The mirror is ledger, not transcript — it stays decided
// wherever the leaf goes.
func TestMixedBatchKeepsMirroredRequestsDecided(t *testing.T) {
	ctx := context.Background()
	agent, _, ran := mixedAgent()
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	t1, _ := s.Send(ctx, weft.User("do both"))
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	// The parked call stands for a child session's parked call.
	if _, err := s.AppendApprovalRequests(ctx, thread.ApprovalRequestEntry{
		CallID: "child_call", Tool: "spend", Args: []byte(`{}`),
		RunID: "s_child-t1", Child: "s_child", Wrapper: "call_d",
	}); err != nil {
		t.Fatal(err)
	}
	if p := s.Pending(); len(p) != 1 || p[0].CallID != "child_call" || p[0].Child != "s_child" {
		t.Fatalf("Pending = %+v, want the mirrored child request", p)
	}
	if rt, err := s.Decide(ctx, thread.Approve("child_call")); err != nil || rt != nil {
		t.Fatalf("Decide on the mirror = %v, %v; want it recorded, no resume yet", rt, err)
	}
	rt, err := s.ResolveDelegation(ctx, "call_d", "s_child", "the child's answer", false)
	if err != nil || rt == nil {
		t.Fatalf("ResolveDelegation = %v, %v", rt, err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	if ran.Load() != 0 {
		t.Error("the delegating call's handler ran: its result is the child's answer")
	}
	if p := s.Pending(); len(p) != 0 {
		t.Errorf("Pending after the resume = %+v, want none: the mirror was decided", p)
	}
	tools := toolMessages(s)
	if len(tools) != 1 || len(tools[0].Content) != 2 {
		t.Fatalf("tool messages = %+v, want one with both results", tools)
	}
	// And after the leaf moves again.
	if err := s.Branch(ctx, t1.ID()); err != nil {
		t.Fatal(err)
	}
	for _, r := range s.Pending() {
		if r.CallID == "child_call" {
			t.Errorf("the mirrored request reads pending again after a Branch: %+v", r)
		}
	}
}
