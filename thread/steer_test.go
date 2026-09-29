package thread_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"strings"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/wefttest"
)

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
