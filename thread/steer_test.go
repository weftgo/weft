package thread_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"strings"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/wefttest"
)

// waitUntil polls cond until it holds or the deadline passes, failing
// the test with what instead — the steer tests' asynchronous settles
// (follow-ups the runner picks up) all need it.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(what)
}

// steerInContext reports whether the steered text is on the session's
// active context.
func steerInContext(s *thread.Session, text string) bool {
	for _, m := range s.Context() {
		if m.Role == weft.RoleUser && m.Text() == text {
			return true
		}
	}
	return false
}

// receipts collects a session's receipt entries, oldest first.
func receipts(s *thread.Session) []thread.ReceiptEntry {
	var out []thread.ReceiptEntry
	for _, e := range s.Entries() {
		if r, ok := e.(thread.ReceiptEntry); ok {
			out = append(out, r)
		}
	}
	return out
}

// receiptStatus folds the receipts into receipt id → final status.
func receiptStatus(rs []thread.ReceiptEntry) map[string]string {
	out := map[string]string{}
	for _, r := range rs {
		if r.Receipt == "" {
			out[r.ID] = thread.ReceiptQueued
		} else {
			out[r.Receipt] = r.Status
		}
	}
	return out
}

// A steer sent while a parallel tool batch runs is delivered after the
// batch: every call of the batch keeps its result beside it, the
// delivered message lands after the tool message, and the receipts
// tell the story (plan §6; ADR 0019 §2.2).
func TestSteerDuringParallelBatch(t *testing.T) {
	ctx := context.Background()
	echo := weft.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo"}, wefttest.Call{Name: "echo"}),
		wefttest.Say("done"),
	)
	var steerRef *thread.Session
	var steerOnce sync.Once
	agent := weft.New(model, echo, weft.Tap(func(_ context.Context, ev weft.Event) {
		if _, ok := ev.(weft.ToolStart); ok {
			steerOnce.Do(func() {
				if _, err := steerRef.Send(ctx, weft.User("switch to metric units")); err != nil {
					t.Errorf("steer Send: %v", err)
				}
			})
		}
	}))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.BusyPolicy(thread.Steer))
	if err != nil {
		t.Fatal(err)
	}
	steerRef = s
	t1, err := s.Send(ctx, weft.User("convert this"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := t1.Wait()
	if err != nil {
		t.Fatal(err)
	}
	// The model saw the steer after the tool message, both calls paired.
	reqs := model.Requests()
	if len(reqs) != 2 {
		t.Fatalf("model called %d times, want the drained steer to spend another step", len(reqs))
	}
	second := reqs[1].Messages
	if n := second[len(second)-1]; n.Role != weft.RoleUser || n.Text() != "switch to metric units" {
		t.Errorf("second request ends with %+v, want the steer", n)
	}
	if n := second[len(second)-2]; n.Role != weft.RoleTool || len(n.Content) != 2 {
		t.Errorf("message before the steer = %+v, want the batch's tool message with both results", n)
	}
	if res.Text() != "done" {
		t.Errorf("reply = %q", res.Text())
	}
	// The receipts: one queued, one delivered naming the run.
	status := receiptStatus(receipts(s))
	if len(status) != 1 {
		t.Fatalf("receipts = %+v, want one steer's story", status)
	}
	for id, st := range status {
		if st != thread.ReceiptDelivered {
			t.Errorf("receipt %s status = %q, want delivered", id, st)
		}
	}
	for _, r := range receipts(s) {
		if r.Status == thread.ReceiptDelivered && r.RunID != t1.RunID() {
			t.Errorf("delivered receipt names run %q, want %q", r.RunID, t1.RunID())
		}
	}
	// The receipt never reaches the model on its own: the context holds
	// the message exactly once, as the delivered transcript message.
	count := 0
	for _, m := range s.Context() {
		if m.Role == weft.RoleUser && m.Text() == "switch to metric units" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("the steered message appears %d times in the context, want exactly once", count)
	}
}

// A steer arriving during what would have been the final step
// redirects the run into one more step — the drain at Final (ADR 0019
// §2.3) — and the receipt records the delivery.
func TestSteerAtFinalStepRedirects(t *testing.T) {
	ctx := context.Background()
	model := wefttest.Script(wefttest.Say("done"), wefttest.Say("done, metric"))
	var steerRef *thread.Session
	var steerOnce sync.Once
	agent := weft.New(model, weft.Tap(func(_ context.Context, ev weft.Event) {
		if _, ok := ev.(weft.TextDelta); ok {
			steerOnce.Do(func() {
				if _, err := steerRef.Send(ctx, weft.User("actually, metric units")); err != nil {
					t.Errorf("steer Send: %v", err)
				}
			})
		}
	}))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.BusyPolicy(thread.Steer))
	steerRef = s
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("convert this"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := t1.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if res.NumSteps() != 2 || res.Text() != "done, metric" {
		t.Errorf("result = %d steps, %q; want the redirect to spend one more step", res.NumSteps(), res.Text())
	}
	status := receiptStatus(receipts(s))
	if len(status) != 1 {
		t.Fatalf("receipts = %+v, want the steer's delivered story", status)
	}
	for id, st := range status {
		if st != thread.ReceiptDelivered {
			t.Errorf("receipt %s = %q, want delivered", id, st)
		}
	}
}

// A steer never resolves a parked call: a run that ends at the
// approval boundary leaves the steer undelivered, and it defers to a
// follow-up that runs once the boundary resolves (plan §6). A steer
// sent while only the boundary is open defers at once.
func TestSteerVsApprovals(t *testing.T) {
	ctx := context.Background()
	gate := weft.Tool("gate", "", func(_ context.Context, _ struct{}) (string, error) {
		return "g", nil
	}, weft.RequireApproval())
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "gate"}),
		wefttest.Say("resumed"),
		wefttest.Say("followed up"),
	)
	agent := weft.New(model, gate)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.BusyPolicy(thread.Steer))
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("run the gate"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	p := s.Pending()
	if len(p) != 1 {
		t.Fatalf("pending = %d, want the parked call", len(p))
	}
	// The boundary holds the session with nothing running: a steer now
	// defers at once — queued then deferred, a follow-up enqueued.
	st, err := s.Send(ctx, weft.User("and then check the totals"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Wait(); err != nil {
		t.Fatal(err)
	}
	status := receiptStatus(receipts(s))
	if got := status[st.ID()]; got != thread.ReceiptDeferred {
		t.Fatalf("steer over a boundary: receipt status = %q, want deferred", got)
	}
	// The follow-up runs after the boundary resolves.
	next := st.Next()
	if next == nil {
		t.Fatal("a deferred steer links its follow-up through Next")
	}
	if _, err := s.Decide(ctx, thread.Approve(p[0].CallID)); err != nil {
		t.Fatal(err)
	}
	if _, err := next.Wait(); err != nil {
		t.Fatalf("follow-up Wait: %v", err)
	}
	// The follow-up's prompt is the steered message, and it ran.
	if got := status[st.ID()]; got != thread.ReceiptDeferred {
		t.Errorf("receipt status = %q, want still deferred", got)
	}
	found := false
	for _, m := range s.Context() {
		if m.Role == weft.RoleUser && m.Text() == "and then check the totals" {
			found = true
		}
	}
	if !found {
		t.Error("the deferred steer's message never reached the context")
	}
}

// A steer that meets a StopWhen end defers: an intended end stays an
// end, and the message runs as the follow-up turn (plan §6).
func TestSteerDeferredAtStopWhen(t *testing.T) {
	ctx := context.Background()
	submit := weft.Tool("submit", "", func(_ context.Context, _ struct{}) (string, error) {
		return "submitted", nil
	})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "submit"}),
		wefttest.Say("followed"),
	)
	var steerRef *thread.Session
	var steerOnce sync.Once
	agent := weft.New(model, submit, weft.StopWhen(weft.HasToolCall("submit")),
		weft.Tap(func(_ context.Context, ev weft.Event) {
			if _, ok := ev.(weft.ToolStart); ok {
				steerOnce.Do(func() {
					if _, err := steerRef.Send(ctx, weft.User("one more thing")); err != nil {
						t.Errorf("steer Send: %v", err)
					}
				})
			}
		}))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.BusyPolicy(thread.Steer))
	steerRef = s
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("finish"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	if res, _ := t1.Wait(); res != nil && res.NumSteps() != 1 {
		t.Fatalf("steps = %d, want the intended end to end", res.NumSteps())
	}
	// The steer deferred to a follow-up whose receipt the deferred
	// entry names — the follow-up's prompt entry — and the follow-up
	// runs the message.
	var followID string
	for _, r := range receipts(s) {
		if r.Status == thread.ReceiptDeferred {
			followID = r.Turn
		}
	}
	if followID == "" {
		t.Fatal("no deferred receipt: the steer should have deferred at the StopWhen end")
	}
	deadline := time.Now().Add(5 * time.Second)
	found := false
	for time.Now().Before(deadline) && !found {
		for _, e := range s.Entries() {
			if me, ok := e.(thread.MessageEntry); ok && me.ID == followID {
				found = true // the follow-up's prompt entry: its turn started
			}
		}
		if !found {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !found {
		t.Fatal("the deferred steer's follow-up never started (its prompt entry is missing)")
	}
	for _, m := range s.Context() {
		if m.Role == weft.RoleUser && m.Text() == "one more thing" {
			return // delivered by the follow-up's turn
		}
	}
	t.Fatal("the deferred steer's message never reached the context")
}

// As overrides the session's policy per Send: a Queue session steers
// one message; a Steer session holds one message for the next turn.
func TestAsOverridesPolicy(t *testing.T) {
	ctx := context.Background()

	// Queue session, one steered send.
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
		wefttest.Say("done"),
		wefttest.Say("held"),
	)
	echo := weft.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) { return "ok", nil })
	var steerRef *thread.Session
	var steerOnce sync.Once
	agent := weft.New(model, echo, weft.Tap(func(_ context.Context, ev weft.Event) {
		if _, ok := ev.(weft.ToolStart); ok {
			steerOnce.Do(func() {
				if _, err := steerRef.Send(ctx, weft.User("steer this one"), thread.As(thread.Steer)); err != nil {
					t.Errorf("As(Steer) send: %v", err)
				}
			})
		}
	}))
	s, _ := thread.Create(ctx, thread.Memory(), agent) // Queue, the default
	steerRef = s
	t1, _ := s.Send(ctx, weft.User("go"))
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := receiptStatus(receipts(s))[t1.ID()]; got != "" {
		t.Errorf("an ordinary Send carries no receipt; statuses = %+v", receiptStatus(receipts(s)))
	}
	var one string
	for id, st := range receiptStatus(receipts(s)) {
		_ = id
		one = st
	}
	if one != thread.ReceiptDelivered {
		t.Errorf("As(Steer) receipt = %q, want delivered", one)
	}

	// Steer session, one held send.
	model2 := wefttest.Script(wefttest.Say("first"), wefttest.Say("held reply"))
	var heldRef *thread.Session
	var heldOnce sync.Once
	agent2 := weft.New(model2, weft.Tap(func(_ context.Context, ev weft.Event) {
		if _, ok := ev.(weft.TextDelta); ok {
			heldOnce.Do(func() {
				if _, err := heldRef.Send(ctx, weft.User("hold this one"), thread.As(thread.Queue)); err != nil {
					t.Errorf("As(Queue) send: %v", err)
				}
			})
		}
	}))
	s2, _ := thread.Create(ctx, thread.Memory(), agent2, thread.BusyPolicy(thread.Steer))
	heldRef = s2
	u1, _ := s2.Send(ctx, weft.User("go"))
	if _, err := u1.Wait(); err != nil {
		t.Fatal(err)
	}
	// The held send ran as its own turn; a Queue send writes no
	// receipts. The follow-up may still be landing its turn entry, so
	// poll the context briefly.
	deadline := time.Now().Add(5 * time.Second)
	found := false
	for time.Now().Before(deadline) && !found {
		for _, m := range s2.Context() {
			if m.Text() == "hold this one" {
				found = true
			}
		}
		if !found {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !found {
		t.Fatal("As(Queue) send never ran as its own turn")
	}
	if rs := receipts(s2); len(rs) != 0 {
		t.Errorf("a Queue-policy send writes no receipts; got %+v", rs)
	}
}

// ClearQueue drops queued steers before delivery: dropped receipts,
// nothing in the context, and the run that was draining nothing ends
// normally. Queue lists what is live before the clear.
func TestClearQueue(t *testing.T) {
	ctx := context.Background()
	release := make(chan struct{})
	block := weft.Tool("block", "", func(ctx context.Context, _ struct{}) (string, error) {
		select {
		case <-release:
			return "ok", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "block"}),
		wefttest.Say("done"),
	)
	var queued sync.WaitGroup
	queued.Add(1)
	var steerRef *thread.Session
	var steerTurn *thread.Turn
	agent := weft.New(model, block, weft.Tap(func(_ context.Context, ev weft.Event) {
		if _, ok := ev.(weft.ToolStart); ok {
			tt, err := steerRef.Send(ctx, weft.User("never mind"))
			if err != nil {
				t.Errorf("steer Send: %v", err)
				return
			}
			steerTurn = tt
			queued.Done()
		}
	}))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.BusyPolicy(thread.Steer))
	if err != nil {
		t.Fatal(err)
	}
	steerRef = s
	t1, _ := s.Send(ctx, weft.User("go"))
	queued.Wait()
	if q := s.Queue(); len(q) != 1 || q[0].Msg.Text() != "never mind" {
		t.Fatalf("Queue() = %+v, want the live steer", q)
	}
	n, err := s.ClearQueue(ctx)
	if err != nil || n != 1 {
		t.Fatalf("ClearQueue = %d, %v; want 1, nil", n, err)
	}
	if q := s.Queue(); len(q) != 0 {
		t.Errorf("Queue() after clear = %+v, want empty", q)
	}
	close(release)
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	status := receiptStatus(receipts(s))
	if got := status[steerTurn.ID()]; got != thread.ReceiptDropped {
		t.Fatalf("cleared steer status = %q, want dropped", got)
	}
	for _, m := range s.Context() {
		if m.Text() == "never mind" {
			t.Error("a dropped steer reached the model's context")
		}
	}
	if res, err := steerTurn.Wait(); res != nil || err != nil {
		t.Errorf("a dropped steer's Turn finished with %v, %v; want nil, nil", res, err)
	}
}

// A steered message must be a user message; anything else fails the
// Send before anything is written.
func TestSteerRejectsNonUserRole(t *testing.T) {
	ctx := context.Background()
	model := wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "echo"}), wefttest.Say("done"))
	echo := weft.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) { return "ok", nil })
	var steerRef *thread.Session
	var once sync.Once
	agent := weft.New(model, echo, weft.Tap(func(_ context.Context, ev weft.Event) {
		if _, ok := ev.(weft.ToolStart); ok {
			once.Do(func() {
				if _, err := steerRef.Send(ctx, weft.Message{Role: weft.RoleAssistant, Content: []weft.Part{weft.TextPart{Text: "I speak for the model"}}}); err == nil {
					t.Error("an assistant-role steer was accepted")
				}
			})
		}
	}))
	s, _ := thread.Create(ctx, thread.Memory(), agent, thread.BusyPolicy(thread.Steer))
	steerRef = s
	t1, _ := s.Send(ctx, weft.User("go"))
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	if rs := receipts(s); len(rs) != 0 {
		t.Errorf("a rejected steer wrote receipts: %+v", rs)
	}
}

// weft.Steering in RunOptions is refused: the session owns the steer
// queue (ADR 0019 through thread).
func TestSteeringRejectedInRunOptions(t *testing.T) {
	ctx := context.Background()
	s, _ := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script(wefttest.Say("x"))))
	_, err := s.Send(ctx, weft.User("go"), thread.RunOptions(weft.Steering(func(context.Context, weft.SteerPoint) []weft.Message { return nil })))
	if err == nil || !strings.Contains(err.Error(), "weft.Steering") {
		t.Fatalf("err = %v, want the refusal naming weft.Steering", err)
	}
	if rs := receipts(s); len(rs) != 0 {
		t.Errorf("a refused Send wrote receipts: %+v", rs)
	}
}

// Receipts survive a restart, and the crash window closes: a queued
// receipt with no fate defers on reopen and its follow-up runs —
// accepted input is durable input (ADR 0011 §4).
func TestReceiptsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
		wefttest.Say("done"),
		wefttest.Say("resurrected"),
	)
	echo := weft.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) { return "ok", nil })
	var steerRef *thread.Session
	var steerOnce sync.Once
	agent := weft.New(model, echo, weft.Tap(func(_ context.Context, ev weft.Event) {
		if _, ok := ev.(weft.ToolStart); ok {
			steerOnce.Do(func() {
				if _, err := steerRef.Send(ctx, weft.User("switch to metric units")); err != nil {
					t.Errorf("steer Send: %v", err)
				}
			})
		}
	}))
	s, err := thread.Create(ctx, st, agent, thread.BusyPolicy(thread.Steer))
	if err != nil {
		t.Fatal(err)
	}
	steerRef = s
	t1, _ := s.Send(ctx, weft.User("convert this"))
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	statuses := receiptStatus(receipts(s))
	if len(statuses) != 1 {
		t.Fatalf("receipts = %+v, want the one delivered steer", statuses)
	}
	for id, st := range statuses {
		if st != thread.ReceiptDelivered {
			t.Fatalf("receipt %s = %q, want delivered", id, st)
		}
	}

	// The crash window: hand-write a queued receipt no fate followed —
	// exactly what a killed writer leaves behind.
	clone := weft.User("lost in the crash")
	if err := st.Append(ctx, s.ID(), thread.ReceiptEntry{
		ID: "e_crashwindow", ParentID: "", Created: time.Now().UTC(),
		Status: thread.ReceiptQueued, Msg: &clone,
	}); err != nil {
		t.Fatal(err)
	}

	// Reopen: the delivered story reads back, and the orphan defers.
	s2, err := thread.Open(ctx, st, s.ID(), agent, thread.BusyPolicy(thread.Steer))
	if err != nil {
		t.Fatal(err)
	}
	status := receiptStatus(receipts(s2))
	if got := status["e_crashwindow"]; got != thread.ReceiptDeferred {
		t.Fatalf("crash-window receipt after reopen = %q, want deferred", got)
	}
	for id, st := range status {
		if id != "e_crashwindow" && st != thread.ReceiptDelivered {
			t.Errorf("receipt %s after reopen = %q, want delivered", id, st)
		}
	}
	if got := status["e_crashwindow"]; got != thread.ReceiptDeferred {
		t.Fatalf("crash-window receipt after reopen = %q, want deferred", got)
	}
	// The resurrected follow-up ran: the lost message reached the model.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		found := false
		for _, m := range s2.Context() {
			if m.Text() == "lost in the crash" {
				found = true
			}
		}
		if found {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the resurrected steer never ran")
}

// Several steers accepted between two drain points deliver in one
// drain, in acceptance order (the review's ordering focus).
func TestSteersDeliverInAcceptanceOrder(t *testing.T) {
	ctx := context.Background()
	release := make(chan struct{})
	block := weft.Tool("block", "", func(ctx context.Context, _ struct{}) (string, error) {
		select {
		case <-release:
			return "ok", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "block"}),
		wefttest.Say("done"),
	)
	var ref *thread.Session
	agent := weft.New(model, block, weft.Tap(func(_ context.Context, ev weft.Event) {
		if _, ok := ev.(weft.ToolStart); ok {
			// All three steers accepted between two drain points — one
			// tool call, one batch, one drain.
			for n := 1; n <= 3; n++ {
				if _, err := ref.Send(ctx, weft.User(fmt.Sprintf("steer number %d", n))); err != nil {
					t.Errorf("steer %d: %v", n, err)
				}
			}
		}
	}))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.BusyPolicy(thread.Steer))
	if err != nil {
		t.Fatal(err)
	}
	ref = s
	t1, _ := s.Send(ctx, weft.User("go"))
	close(release)
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	// One drain delivered all three, in acceptance order, after the
	// tool message.
	second := model.Requests()[1].Messages
	var got []string
	for _, m := range second {
		if m.Role == weft.RoleUser && len(m.Text()) > len("steer number ") && m.Text()[:6] == "steer " {
			got = append(got, m.Text())
		}
	}
	want := []string{"steer number 1", "steer number 2", "steer number 3"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("delivered steers = %v, want %v in acceptance order", got, want)
	}
	// All three receipts reached delivered.
	statuses := receiptStatus(receipts(s))
	if len(statuses) != 3 {
		t.Fatalf("receipts = %+v, want three", statuses)
	}
	for id, st := range statuses {
		if st != thread.ReceiptDelivered {
			t.Errorf("receipt %s = %q, want delivered", id, st)
		}
	}
}

// A turn that dies without draining — the caller's context dies before
// the run starts — still settles its queued steers: they defer and the
// follow-up runs (the review's every-receipt-final-state focus).
func TestSteerSettlesWhenTurnDiesEarly(t *testing.T) {
	ctx := context.Background()
	model := wefttest.Script(
		wefttest.Say("first"),
		wefttest.Say("followed"),
	)
	var ref *thread.Session
	var once sync.Once
	t1ctx, cancelTurn := context.WithCancel(ctx)
	agent := weft.New(model, weft.Tap(func(_ context.Context, ev weft.Event) {
		if _, ok := ev.(weft.TextDelta); ok {
			once.Do(func() {
				if _, err := ref.Send(ctx, weft.User("steer the dying turn")); err != nil {
					t.Errorf("steer Send: %v", err)
					return
				}
				// The TURN dies before any drain point can take the
				// steer: cancellation wins over delivery.
				cancelTurn()
			})
		}
	}))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.BusyPolicy(thread.Steer))
	if err != nil {
		t.Fatal(err)
	}
	ref = s
	t1, _ := s.Send(t1ctx, weft.User("go"))
	if _, err := t1.Wait(); err == nil {
		t.Fatal("the canceled turn reported success")
	}
	// The steer was queued for the dying run but never drained: it
	// defers and its follow-up runs the message.
	deadline := time.Now().Add(5 * time.Second)
	found := false
	for time.Now().Before(deadline) && !found {
		for _, r := range receipts(s) {
			if r.Status == thread.ReceiptDeferred {
				found = true
			}
		}
		if !found {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !found {
		t.Fatal("the steer never reached a final receipt state")
	}
	inContext := false
	for time.Now().Before(deadline) && !inContext {
		for _, m := range s.Context() {
			if m.Text() == "steer the dying turn" {
				inContext = true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !inContext {
		t.Fatal("the deferred steer's follow-up never ran its message")
	}
}

// A steer delivered during the attempt that then overflows is not lost
// with that attempt's unpersisted transcript (ADR 0020 §5: the failed
// attempt records none): the re-run re-queues it and delivers it
// again, so the message lands on the tree exactly once and its
// receipt names the re-run.
func TestOverflowReRunRedeliversHandedSteers(t *testing.T) {
	ctx := context.Background()
	echo := weft.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})
	model := wefttest.Script(
		wefttest.Say("a first answer"),                  // a prior turn, so the compaction has a cut
		wefttest.ToolCalls(wefttest.Call{Name: "echo"}), // attempt 1: a batch to drain the steer after
		wefttest.Fail(weft.ErrContextOverflow),          // attempt 1: the overflow
		wefttest.Say("the summary"),                     // the compaction's summarizer call
		wefttest.ToolCalls(wefttest.Call{Name: "echo"}), // attempt 2: a batch to drain the re-queued steer after
		wefttest.Say("done"),
	)
	var steerRef *thread.Session
	var steerOnce sync.Once
	agent := weft.New(model, echo, weft.Tap(func(_ context.Context, ev weft.Event) {
		if _, ok := ev.(weft.ToolStart); ok {
			steerOnce.Do(func() {
				if _, err := steerRef.Send(ctx, weft.User("switch to metric units")); err != nil {
					t.Errorf("steer Send: %v", err)
				}
			})
		}
	}))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.BusyPolicy(thread.Steer), thread.KeepRecent(1))
	if err != nil {
		t.Fatal(err)
	}
	steerRef = s
	t0, err := s.Send(ctx, weft.User("a first question"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t0.Wait(); err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("convert this"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := t1.Wait()
	if err != nil {
		t.Fatalf("the overflow re-run failed: %v", err)
	}
	if res.Text() != "done" {
		t.Errorf("reply = %q, want the re-run's answer", res.Text())
	}
	// The steered message reached the tree exactly once — through the
	// re-run's transcript, not only the failed attempt's memory.
	if n := countUser(s, "switch to metric units"); n != 1 {
		t.Fatalf("the steered message appears %d times in the context, want exactly once", n)
	}
	status := receiptStatus(receipts(s))
	if len(status) != 1 {
		t.Fatalf("receipts = %+v, want one steer's story", status)
	}
	for id, st := range status {
		if st != thread.ReceiptDelivered {
			t.Errorf("receipt %s = %q, want delivered", id, st)
		}
	}
	for _, r := range receipts(s) {
		if r.Status == thread.ReceiptDelivered && r.RunID != t1.RunID() {
			t.Errorf("delivered receipt names run %q, want the re-run's %q", r.RunID, t1.RunID())
		}
	}
}

// countUser counts the user messages on the context carrying text.
func countUser(s *thread.Session, text string) int {
	n := 0
	for _, m := range s.Context() {
		if m.Role == weft.RoleUser && m.Text() == text {
			n++
		}
	}
	return n
}

// panicOnceStorage panics, once, on the Append that carries a turn
// entry — the turn's end batch — and behaves otherwise.
type panicOnceStorage struct {
	thread.Storage
	armed bool
}

func (p *panicOnceStorage) Append(ctx context.Context, session string, entries ...thread.Entry) error {
	if p.armed {
		for _, e := range entries {
			if _, ok := e.(thread.TurnEntry); ok {
				p.armed = false
				panic("storage exploded under the turn's end batch")
			}
		}
	}
	return p.Storage.Append(ctx, session, entries...)
}

// A steer handed to a run whose turn then dies on the session layer's
// panic path (the turn's end batch never lands) must still reach a
// final state and its message must still run: it defers to a
// follow-up, like every undelivered steer — delivered-to-a-lost-
// transcript is not a receipt state at all (the review's
// every-receipt-final-state focus).
func TestSteerSettlesWhenTurnEndPanics(t *testing.T) {
	ctx := context.Background()
	st := &panicOnceStorage{Storage: thread.Memory(), armed: true}
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
		wefttest.Say("done"),
		wefttest.Say("followed"),
	)
	echo := weft.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})
	var steerRef *thread.Session
	var steerOnce sync.Once
	agent := weft.New(model, echo, weft.Tap(func(_ context.Context, ev weft.Event) {
		if _, ok := ev.(weft.ToolStart); ok {
			steerOnce.Do(func() {
				if _, err := steerRef.Send(ctx, weft.User("steer the doomed turn")); err != nil {
					t.Errorf("steer Send: %v", err)
				}
			})
		}
	}))
	s, err := thread.Create(ctx, st, agent, thread.BusyPolicy(thread.Steer))
	if err != nil {
		t.Fatal(err)
	}
	steerRef = s
	t1, err := s.Send(ctx, weft.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t1.Wait(); err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("the panicked turn's Wait = %v, want the containment error", err)
	}
	// The steer defers (a queued receipt alone is not a final state)
	// and its follow-up runs the message.
	waitUntil(t, "the steer of a panicked turn never reached a final receipt state", func() bool {
		for _, r := range receipts(s) {
			if r.Status == thread.ReceiptDeferred {
				return true
			}
		}
		return false
	})
	waitUntil(t, "the steer of a panicked turn never ran its message", func() bool {
		return steerInContext(s, "steer the doomed turn")
	})
}

// failTurnEndStorage fails, once, the Append that carries a turn
// entry, and behaves otherwise.
type failTurnEndStorage struct {
	thread.Storage
	fail bool
}

func (f *failTurnEndStorage) Append(ctx context.Context, session string, entries ...thread.Entry) error {
	if f.fail {
		for _, e := range entries {
			if _, ok := e.(thread.TurnEntry); ok {
				f.fail = false
				return errors.New("disk on fire")
			}
		}
	}
	return f.Storage.Append(ctx, session, entries...)
}

// A steer handed to a run whose turn-end batch the storage refuses
// must not finish as delivered while its message is in no tree — not
// in storage (the batch failed) and not in memory (nothing was
// adopted). It defers and the follow-up re-runs the message: accepted
// input acknowledged then lost is the exact scar ADR 0011 §1 carries
// from Codex #40805.
func TestSteerRedeliveredWhenTurnEndNotPersisted(t *testing.T) {
	ctx := context.Background()
	st := &failTurnEndStorage{Storage: thread.Memory(), fail: true}
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
		wefttest.Say("done"),
		wefttest.Say("followed"),
	)
	echo := weft.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})
	var steerRef *thread.Session
	var steerOnce sync.Once
	agent := weft.New(model, echo, weft.Tap(func(_ context.Context, ev weft.Event) {
		if _, ok := ev.(weft.ToolStart); ok {
			steerOnce.Do(func() {
				if _, err := steerRef.Send(ctx, weft.User("steer the unpersisted turn")); err != nil {
					t.Errorf("steer Send: %v", err)
				}
			})
		}
	}))
	s, err := thread.Create(ctx, st, agent, thread.BusyPolicy(thread.Steer))
	if err != nil {
		t.Fatal(err)
	}
	steerRef = s
	t1, err := s.Send(ctx, weft.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t1.Wait(); err != nil {
		t.Fatalf("the run itself succeeded; only its persistence failed: %v", err)
	}
	// The message re-runs: it is in no tree until a follow-up carries it.
	waitUntil(t, "the steer of an unpersisted turn never reached a final receipt state", func() bool {
		for _, r := range receipts(s) {
			if r.Status == thread.ReceiptDeferred {
				return true
			}
		}
		return false
	})
	waitUntil(t, "the steer of an unpersisted turn never re-ran its message", func() bool {
		return steerInContext(s, "steer the unpersisted turn")
	})
}

// flushFailStorage adds the Flusher capability to any storage, failing
// the first failNext Flush calls.
type flushFailStorage struct {
	thread.Storage
	failNext atomic.Int32
}

func (f *flushFailStorage) Flush(ctx context.Context, session string) error {
	if f.failNext.Add(-1) >= 0 {
		return errors.New("fsync failed")
	}
	return nil
}

// A steer whose receipt flush fails is written but unsynced: Send
// reports the failure, and the live session must match what a reopen
// would do with the same on-disk state (resurrectSteers defers the
// orphan) — the steer defers and runs, rather than sitting in a
// queued receipt no running turn will ever drain.
func TestSteerFlushFailureStillSettles(t *testing.T) {
	ctx := context.Background()
	st := &flushFailStorage{Storage: thread.Memory()}
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
		wefttest.Say("done"),
		wefttest.Say("followed"),
	)
	echo := weft.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})
	var steerRef *thread.Session
	var steerOnce sync.Once
	agent := weft.New(model, echo, weft.Tap(func(_ context.Context, ev weft.Event) {
		if _, ok := ev.(weft.ToolStart); ok {
			steerOnce.Do(func() {
				st.failNext.Store(1) // the steer receipt's flush fails
				if _, err := steerRef.Send(ctx, weft.User("steer past the flush failure")); err == nil {
					t.Error("a steer whose receipt flush failed was reported accepted")
				}
			})
		}
	}))
	s, err := thread.Create(ctx, st, agent, thread.BusyPolicy(thread.Steer))
	if err != nil {
		t.Fatal(err)
	}
	steerRef = s
	t1, err := s.Send(ctx, weft.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	// The receipt reaches a final state and the message runs — exactly
	// what a reopen would resurrect from the written entry.
	waitUntil(t, "the flush-failed steer never reached a final receipt state", func() bool {
		for _, r := range receipts(s) {
			if r.Status == thread.ReceiptDeferred {
				return true
			}
		}
		return false
	})
	waitUntil(t, "the flush-failed steer never ran its message", func() bool {
		return steerInContext(s, "steer past the flush failure")
	})
}

// failDeferredStorage fails, once, the Append of a deferred receipt
// entry, and behaves otherwise.
type failDeferredStorage struct {
	thread.Storage
	fail bool
}

func (f *failDeferredStorage) Append(ctx context.Context, session string, entries ...thread.Entry) error {
	if f.fail {
		for _, e := range entries {
			if r, ok := e.(thread.ReceiptEntry); ok && r.Status == thread.ReceiptDeferred {
				f.fail = false
				return errors.New("disk on fire")
			}
		}
	}
	return f.Storage.Append(ctx, session, entries...)
}

// A crashed steer resurrected on reopen whose deferral cannot be
// persisted still runs: the follow-up is queued in memory, the same
// recovery settleSteersLocked uses — the message is in hand, and only
// the receipt entry stays queued on disk.
func TestResurrectSteerSurvivesFailedDeferral(t *testing.T) {
	ctx := context.Background()
	inner := thread.Memory()
	st := &failDeferredStorage{Storage: inner, fail: true}
	agent := weft.New(wefttest.Script(wefttest.Say("resurrected"), wefttest.Say("resurrected")))
	s0, err := thread.Create(ctx, inner, agent)
	if err != nil {
		t.Fatal(err)
	}
	// The crash window: a queued receipt no fate followed.
	clone := weft.User("lost in the crash")
	if err := st.Append(ctx, s0.ID(), thread.ReceiptEntry{
		ID: "e_orphan", ParentID: "", Created: time.Now().UTC(),
		Status: thread.ReceiptQueued, Msg: &clone,
	}); err != nil {
		t.Fatal(err)
	}
	s, err := thread.Open(ctx, st, s0.ID(), agent)
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the resurrected steer never ran despite the failed deferral write", func() bool {
		return steerInContext(s, "lost in the crash")
	})
}
