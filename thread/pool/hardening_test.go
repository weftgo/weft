package pool_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/pool"
	"github.com/weftgo/weft/wefttest"
)

// failAppend fails every Append to one session id.
type failAppend struct {
	thread.Storage
	id string
}

func (f failAppend) Append(ctx context.Context, session string, es ...thread.Entry) error {
	if session == f.id {
		return errors.New("disk full")
	}
	return f.Storage.Append(ctx, session, es...)
}

// TestWrapSyncNonRunErrorFails: a sync child failing outside a run (its
// prompt append refused) is SUBAGENT_FAILED — before, the wrap read a
// nil *RunError and panicked (post-0.7 review).
func TestWrapSyncNonRunErrorFails(t *testing.T) {
	ctx := context.Background()
	st := failAppend{Storage: thread.Memory(), id: "child1"}
	child := weft.New(wefttest.Script(wefttest.Say("x")))
	p := pool.New(1, pool.IDs(func() string { return "child1" }))
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"go"}`}),
		wefttest.Say("noted"),
	), p.Wrap("research", "", child))
	s, err := thread.Create(ctx, st, parent)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, weft.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := turn.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	got := transcriptText(res.Messages)
	t.Logf("tool result the parent model saw: %q", got)
	if !strings.Contains(got, "SUBAGENT_FAILED") {
		t.Errorf("want SUBAGENT_FAILED, got %q", got)
	}
}

// hookAppend runs onAccept after the acceptance receipt lands.
type hookAppend struct {
	thread.Storage
	onAccept func()
}

func (h hookAppend) Append(ctx context.Context, session string, es ...thread.Entry) error {
	err := h.Storage.Append(ctx, session, es...)
	for _, e := range es {
		if pr, ok := e.(thread.PoolReceiptEntry); ok && pr.Status == thread.PoolAccepted && h.onAccept != nil {
			h.onAccept()
		}
	}
	return err
}

// TestCloseRacesAsyncSubmit: Close landing right after an async
// Submit's acceptance must see the child in its drain — the WaitGroup
// Add sat after the unlock and raced Close's Wait (post-0.7 review).
func TestCloseRacesAsyncSubmit(t *testing.T) {
	ctx := context.Background()
	hits, lost := 0, 0
	for i := 0; i < 5000; i++ {
		p := pool.New(1)
		closed := make(chan error, 1)
		st := hookAppend{Storage: thread.Memory(), onAccept: func() {
			go func() { closed <- p.Close(ctx) }()
		}}
		s, _ := thread.Create(ctx, st, weft.New(wefttest.Script()))
		child := weft.New(blocking{release: make(chan struct{})})
		rec, serr := p.Submit(ctx, s, child, "go")
		cerr := <-closed
		snap := pool.Receipts(s)
		if cerr != nil || serr != nil || rec == nil {
			continue
		}
		hits++
		for _, r := range snap {
			if r.ID == rec.ID && (r.State == thread.PoolAccepted || r.State == thread.PoolRunning) {
				lost++
				if lost == 1 {
					t.Errorf("iter %d: Close returned nil with receipt %s still %q", i, r.ID, r.State)
				}
			}
		}
	}
	t.Logf("submits accepted before close: %d, unsettled at Close return: %d", hits, lost)
}

// panicRunning panics once on the running receipt's append.
type panicRunning struct {
	thread.Storage
	once *sync.Once
}

func (p panicRunning) Append(ctx context.Context, session string, es ...thread.Entry) error {
	for _, e := range es {
		if pr, ok := e.(thread.PoolReceiptEntry); ok && pr.Status == thread.PoolRunning {
			p.once.Do(func() { panic("storage bug") })
		}
	}
	return p.Storage.Append(ctx, session, es...)
}

// TestSyncPanicIsAnError: a contained panic in runChild settles the
// receipt failed and reaches the sync caller as an error — before, the
// parent model read a successful empty tool result.
func TestSyncPanicIsAnError(t *testing.T) {
	ctx := context.Background()
	child := weft.New(wefttest.Script(wefttest.Say("x")))
	p := pool.New(1)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"go"}`}),
		wefttest.Say("noted"),
	), p.Wrap("research", "", child))
	s, _ := thread.Create(ctx, panicRunning{thread.Memory(), &sync.Once{}}, parent)
	turn, err := s.Send(ctx, weft.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := turn.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	for _, r := range pool.Receipts(s) {
		t.Logf("receipt %s state=%s stop=%q", r.ID, r.State, r.Stop)
	}
	for _, m := range res.Messages {
		for _, part := range m.Content {
			if tp, ok := part.(weft.ToolResultPart); ok {
				t.Logf("tool result: IsError=%v content=%q", tp.IsError, tp.Content)
				if !tp.IsError {
					t.Errorf("a delegation whose receipt settled failed returned a successful tool result %q", tp.Content)
				}
			}
		}
	}
}

// failMirror fails the parent's mirror batch.
type failMirror struct{ thread.Storage }

func (f failMirror) Append(ctx context.Context, session string, es ...thread.Entry) error {
	for _, e := range es {
		if re, ok := e.(thread.ApprovalRequestEntry); ok && re.Child != "" {
			return errors.New("disk full")
		}
	}
	return f.Storage.Append(ctx, session, es...)
}

// TestMirrorFailureFailsDelegation: a sync child parks but its mirrors
// fail — the delegation fails, settled; before, the parent parked on
// the wrapper call itself and the receipt read running forever.
func TestMirrorFailureFailsDelegation(t *testing.T) {
	ctx := context.Background()
	p := pool.New(1)
	child, _ := gatedChild(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}),
		wefttest.Say("child done"),
	)
	s, _ := thread.Create(ctx, failMirror{thread.Memory()}, nestedParent(p, child))
	turn, err := s.Send(ctx, weft.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := turn.Wait()
	n := -1
	if res != nil {
		n = len(res.Pending)
	}
	t.Logf("Wait err=%v res.Pending=%d", err, n)
	for _, r := range s.Pending() {
		t.Logf("parent Pending: tool=%s call=%s child=%q", r.Tool, r.CallID, r.Child)
	}
	for _, r := range pool.Receipts(s) {
		t.Logf("receipt %s state=%s", r.ID, r.State)
	}
	for _, r := range s.Pending() {
		if r.Tool == "research" {
			t.Errorf("mirror failure surfaced the wrapper call %s as a directly decidable request", r.CallID)
		}
	}
	for _, r := range pool.Receipts(s) {
		if r.State != thread.PoolFailed {
			t.Errorf("receipt %s state=%s, want failed", r.ID, r.State)
		}
	}
}
