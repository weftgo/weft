package pool_test

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/thread/pool"
	"github.com/weftgo/weft/wefttest"
)

// blocking is a Model whose single turn waits for its release channel
// to close, then says text — the FIFO and cancellation tests' child.
// onStart fires when the turn starts, which is the moment the pool
// admitted it.
type blocking struct {
	release chan struct{}
	text    string
	onStart func()
}

func (b blocking) Stream(ctx context.Context, _ weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	return func(yield func(weft.ModelEvent, error) bool) {
		if b.onStart != nil {
			b.onStart()
		}
		select {
		case <-b.release:
			yield(weft.ModelTextDelta{Text: b.text}, nil)
			yield(weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}}, nil)
		case <-ctx.Done():
			// The house convention for a stream that dies mid-flight
			// (wefttest's own shape): a terminal error yield.
			yield(nil, ctx.Err())
		}
	}
}

// waitState polls a parent session's receipts until one reaches state,
// failing the test on timeout — an async child settles on a pool
// goroutine, and a bounded poll is the honest wait.
func waitState(t *testing.T, parent *thread.Session, state string) pool.Receipt {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, r := range pool.Receipts(parent) {
			if r.State == state {
				return r
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no receipt reached %q; have %+v", state, pool.Receipts(parent))
	return pool.Receipt{}
}

func TestNewMaxPanics(t *testing.T) {
	for _, max := range []int{0, -1} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("New(%d) did not panic", max)
				}
			}()
			pool.New(max)
		}()
	}
}

// A sync delegation returns the child's answer, records the receipt
// machine, links the child session by lineage, and bills the child's
// usage to the parent's Delegated bucket (ADR 0022 §2–§5).
func TestWrapSync(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	child := weft.New(wefttest.Script(wefttest.Say("the bug is in compaction")))
	p := pool.New(2)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research",
			Args: wefttest.Args(struct{ Prompt string }{"find the bug"})}),
		wefttest.Say("done"),
	), p.Wrap("research", "delegates research", child))
	s, err := thread.Create(ctx, st, parent)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	turn, err := s.Send(ctx, weft.User("where is the bug?"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	res, err := turn.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := res.Text(); got != "done" {
		t.Errorf("parent reply = %q", got)
	}
	// The child's answer was the tool result the parent model saw.
	found := false
	for _, m := range res.Messages {
		if m.Role != weft.RoleTool {
			continue
		}
		for _, part := range m.Content {
			if tp, ok := part.(weft.ToolResultPart); ok && tp.Content == "the bug is in compaction" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("child answer not in the parent transcript: %+v", res.Messages)
	}
	rs := pool.Receipts(s)
	if len(rs) != 1 {
		t.Fatalf("receipts = %+v, want one", rs)
	}
	if rs[0].State != thread.PoolDone || rs[0].Stop != "the bug is in compaction" {
		t.Errorf("receipt = %+v", rs[0])
	}
	if rs[0].Child == "" || rs[0].Child == s.ID() {
		t.Errorf("child session id = %q", rs[0].Child)
	}
	// The child session: linked by lineage, holding the prompt and
	// answer of its own turn.
	open, err := thread.Open(ctx, st, rs[0].Child, child)
	if err != nil {
		t.Fatalf("Open child: %v", err)
	}
	lin := open.Lineage()
	if lin.Session != s.ID() {
		t.Errorf("lineage = %+v", lin)
	}
	// The entries carry the whole machine: acceptance, start,
	// settlement.
	var states []string
	for _, e := range s.Entries() {
		if pr, ok := e.(thread.PoolReceiptEntry); ok {
			states = append(states, pr.Status)
		}
	}
	if fmt.Sprint(states) != "[accepted running done]" {
		t.Errorf("receipt states = %v", states)
	}
	// The ledger: the parent's own turns in Turns, the child's cost in
	// Delegated (ADR 0022 D3).
	u := s.Usage()
	if u.Delegated.InputTokens != 10 || u.Delegated.OutputTokens != 5 {
		t.Errorf("Delegated = %+v", u.Delegated)
	}
	if u.Turns.OutputTokens == 0 {
		t.Errorf("Turns = %+v; the parent's own cost must be here", u.Turns)
	}
}

// A failing child is a tool error the parent model sees — failure is
// data (ADR 0014) — and the receipt settles failed.
func TestWrapSyncFails(t *testing.T) {
	ctx := context.Background()
	cause := errors.New("connection reset")
	child := weft.New(wefttest.Script(wefttest.SayThenFail("partial", cause)))
	p := pool.New(1)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research",
			Args: wefttest.Args(struct{ Prompt string }{"go"})}),
		wefttest.Say("noted"),
	), p.Wrap("research", "", child))
	s, _ := thread.Create(ctx, thread.Memory(), parent)
	turn, err := s.Send(ctx, weft.User("go"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatalf("a failed child must not fail the parent turn: %v", err)
	}
	rs := pool.Receipts(s)
	if len(rs) != 1 || rs[0].State != thread.PoolFailed {
		t.Fatalf("receipts = %+v", rs)
	}
	if rs[0].Stop == "" {
		t.Errorf("failed receipt carries no cause: %+v", rs[0])
	}
}

// The capped settlement: a child that dies on a budget (ADR 0022 §4).
func TestWrapSyncCapped(t *testing.T) {
	ctx := context.Background()
	child := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "loop", Args: `{}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "loop", Args: `{}`}),
	), weft.MaxSteps(1), weft.Tool("loop", "", func(_ context.Context, _ struct{}) (string, error) {
		return "again", nil
	}))
	p := pool.New(1)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"go"}`}),
		wefttest.Say("noted"),
	), p.Wrap("research", "", child))
	s, _ := thread.Create(ctx, thread.Memory(), parent)
	turn, _ := s.Send(ctx, weft.User("go"))
	if _, err := turn.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	rs := pool.Receipts(s)
	if len(rs) != 1 || rs[0].State != thread.PoolCapped {
		t.Fatalf("receipts = %+v, want capped", rs)
	}
}

// The async delegation's tool result is the receipt line, byte-pinned
// (model-visible bytes change only with an ADR and a golden —
// ADR 0022 §2), and the answer settles on the receipt for a later
// turn to read.
func TestWrapAsyncGolden(t *testing.T) {
	ctx := context.Background()
	child := weft.New(wefttest.Script(wefttest.Say("the answer")))
	p := pool.New(2)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"go"}`}),
		wefttest.Say("ok"),
	), p.Wrap("research", "", child, pool.Async()))
	s, _ := thread.Create(ctx, thread.Memory(), parent)
	turn, err := s.Send(ctx, weft.User("go"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	res, err := turn.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	var line string
	for _, m := range res.Messages {
		if m.Role != weft.RoleTool {
			continue
		}
		for _, part := range m.Content {
			if tp, ok := part.(weft.ToolResultPart); ok {
				line = tp.Content
			}
		}
	}
	rs := pool.Receipts(s)
	if len(rs) != 1 {
		t.Fatalf("receipts = %+v", rs)
	}
	want := fmt.Sprintf("background task accepted; receipt %s", rs[0].ID)
	if line != want {
		t.Errorf("async tool result:\n got %q\nwant %q", line, want)
	}
	// The child settles on its own — a fast, scripted run — and only
	// then does the pool close: Close is a drain that cancels what is
	// still flying, and must not race the happy ending.
	final := waitState(t, s, thread.PoolDone)
	if final.Stop != "the answer" {
		t.Errorf("settled receipt = %+v", final)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if s.Usage().Delegated.OutputTokens != 5 {
		t.Errorf("Delegated = %+v", s.Usage().Delegated)
	}
}

// Admission is serialized by the bound (ADR 0022 D2): with one slot,
// a queued child never starts while another holds it, and each
// settling child admits exactly one waiter. Which waiter is first is
// the channel wait queue's order among goroutines that reached it —
// the FIFO guarantee covers blocked senders, not goroutine start
// order — so the test is permutation-agnostic: it pins one-at-a-time
// admission, not the permutation.
func TestFIFO(t *testing.T) {
	ctx := context.Background()
	s, _ := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
	p := pool.New(1)
	var mu sync.Mutex
	started := []int{}
	releases := make([]chan struct{}, 4)
	for i := range releases {
		releases[i] = make(chan struct{})
	}
	submit := func(i int) *pool.Receipt {
		mdl := blocking{release: releases[i], text: fmt.Sprintf("child %d", i),
			onStart: func() {
				mu.Lock()
				started = append(started, i)
				mu.Unlock()
			}}
		r, err := p.Submit(ctx, s, weft.New(mdl), "go")
		if err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
		return r
	}
	warm := submit(0) // holds the only slot
	waitCount(t, s, 1)
	// The warm-up must be inside its model before it holds the slot
	// in the sense the queue sees: wait for its start.
	waitStarted(t, &mu, &started, 1)
	// Queued children: accepted, none started.
	submit(1)
	submit(2)
	submit(3)
	waitCount(t, s, 4)
	mu.Lock()
	if len(started) != 1 { // the warm-up itself
		mu.Unlock()
		t.Fatalf("queued children started: %v", started)
	}
	mu.Unlock()
	// Each release settles one child and admits exactly one waiter,
	// three times over.
	close(releases[0])
	waitState(t, s, thread.PoolDone)
	for round := 1; round <= 3; round++ {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			n := len(started)
			mu.Unlock()
			if n == round+1 {
				break
			}
			time.Sleep(time.Millisecond)
		}
		mu.Lock()
		got := append([]int(nil), started...)
		mu.Unlock()
		if len(got) != round+1 {
			t.Fatalf("round %d: started = %v", round, got)
		}
		next := got[len(got)-1]
		if next < 1 || next > 3 {
			t.Fatalf("round %d admitted the warm-up again: %v", round, got)
		}
		close(releases[next])
		waitState(t, s, thread.PoolDone)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_ = warm
}

// waitStarted polls until n children have started.
func waitStarted(t *testing.T, mu *sync.Mutex, started *[]int, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		c := len(*started)
		mu.Unlock()
		if c == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Fatalf("started = %v, want %d", *started, n)
}

// waitCount polls until the parent holds n receipts.
func waitCount(t *testing.T, s *thread.Session, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(pool.Receipts(s)) >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("receipts = %d, want %d", len(pool.Receipts(s)), n)
}

// Cancel: an explicit cancel settles the receipt canceled (D4).
func TestSubmitCancel(t *testing.T) {
	ctx := context.Background()
	s, _ := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
	p := pool.New(1)
	release := make(chan struct{})
	r, err := p.Submit(ctx, s, weft.New(blocking{release: release, text: "late"}), "go")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if r.State != thread.PoolAccepted {
		t.Errorf("fresh receipt = %+v", r)
	}
	if err := p.Cancel(r.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	final := waitState(t, s, thread.PoolCanceled)
	if final.ID != r.ID {
		t.Errorf("settled receipt = %+v, want %s", final, r.ID)
	}
	close(release) // the model's goroutine, if it slipped in, may end
	// A settled receipt refuses to cancel again.
	if err := p.Cancel(r.ID); !errors.Is(err, pool.ErrNotRunning) {
		t.Errorf("second Cancel err = %v", err)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// Close drains: no new work, every child canceled and settled.
func TestCloseDrains(t *testing.T) {
	ctx := context.Background()
	s, _ := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
	p := pool.New(2)
	release := make(chan struct{})
	for i := 0; i < 2; i++ {
		if _, err := p.Submit(ctx, s, weft.New(blocking{release: release, text: "x"}), "go"); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	if err := p.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	close(release)
	for _, r := range pool.Receipts(s) {
		if r.State != thread.PoolCanceled {
			t.Errorf("receipt %s settled %q after Close", r.ID, r.State)
		}
	}
	if _, err := p.Submit(ctx, s, weft.New(wefttest.Script()), "go"); !errors.Is(err, pool.ErrClosed) {
		t.Errorf("Submit after Close err = %v", err)
	}
	if err := p.Close(ctx); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// D4's other half: an async child survives the submitting caller's
// cancellation — it runs on the pool's context, not the caller's.
func TestSubmitDetachedFromCallerCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s, _ := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
	p := pool.New(1)
	r, err := p.Submit(ctx, s, weft.New(wefttest.Script(wefttest.Say("done anyway"))), "go")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	cancel()
	final := waitState(t, s, thread.PoolDone)
	if final.ID != r.ID {
		t.Errorf("settled receipt = %+v", final)
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// A sync delegation chain deeper than the pool's max is refused with
// SUBAGENT_CYCLE before any child starts (the deadlock guard).
func TestDepthRefusal(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	p := pool.New(1)
	inner := weft.New(wefttest.Script(wefttest.Say("inner answer")))
	middleAgent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "inner", Args: `{"prompt":"go"}`}),
		wefttest.Say("middle done"),
	), p.Wrap("inner", "", inner))
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "middle", Args: `{"prompt":"go"}`}),
		wefttest.Say("parent done"),
	), p.Wrap("middle", "", middleAgent))
	s, _ := thread.Create(ctx, st, parent)
	turn, _ := s.Send(ctx, weft.User("go"))
	res, err := turn.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	var saw string
	for _, m := range res.Messages {
		if m.Role != weft.RoleTool {
			continue
		}
		for _, part := range m.Content {
			if tp, ok := part.(weft.ToolResultPart); ok {
				saw = tp.Content
			}
		}
	}
	if res.Text() != "parent done" {
		t.Errorf("parent reply = %q", res.Text())
	}
	// The refusal fired inside the middle child — its own transcript
	// shows it, and the middle model recovered (failure is data) and
	// answered, which is what the parent's call returned.
	rs := pool.Receipts(s)
	if len(rs) != 1 {
		t.Fatalf("receipts = %+v", rs)
	}
	middleSess, err := thread.Open(ctx, st, rs[0].Child, middleAgent)
	if err != nil {
		t.Fatalf("Open middle: %v", err)
	}
	if !strings.Contains(saw, "middle done") {
		t.Errorf("parent tool result = %q; the middle's recovered answer", saw)
	}
	refused := false
	for _, e := range middleSess.Entries() {
		if m, ok := e.(thread.MessageEntry); ok && m.Message.Role == weft.RoleTool {
			for _, part := range m.Message.Content {
				if tp, ok := part.(weft.ToolResultPart); ok && strings.Contains(tp.Content, "SUBAGENT_CYCLE") {
					refused = true
				}
			}
		}
	}
	if !refused {
		t.Errorf("no SUBAGENT_CYCLE in the middle child's transcript")
	}
}

// Outside a session run the wrap falls back to the ordinary subagent
// path under the slot (ADR 0022 §2): no receipts, the child's answer
// is the result.
func TestWrapOutsideSession(t *testing.T) {
	ctx := context.Background()
	child := weft.New(wefttest.Script(wefttest.Say("bare answer")))
	p := pool.New(1)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "ask", Args: `{"prompt":"go"}`}),
		wefttest.Say("done"),
	), p.Wrap("ask", "", child))
	res, err := parent.Generate(ctx, weft.Prompt("go"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if res.Text() != "done" {
		t.Errorf("reply = %q", res.Text())
	}
}

func settledState(s string) bool {
	switch s {
	case thread.PoolDone, thread.PoolFailed, thread.PoolCanceled, thread.PoolCapped:
		return true
	}
	return false
}

func mustList(ctx context.Context, t *testing.T, st thread.Storage) []thread.Header {
	t.Helper()
	page, err := thread.List(ctx, st, thread.Query{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return page.Sessions
}

func mustOpen(ctx context.Context, t *testing.T, st thread.Storage, id string) *thread.Session {
	t.Helper()
	s, err := thread.Open(ctx, st, id, weft.New(wefttest.Script()))
	if err != nil {
		t.Fatalf("Open %s: %v", id, err)
	}
	return s
}

// Receipts survive a restart: they are entries, and the ledger with
// them (ADR 0022 §4–§5).
func TestReceiptsAfterRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatalf("jsonl.Open: %v", err)
	}
	child := weft.New(wefttest.Script(wefttest.Say("persisted answer")))
	p := pool.New(1)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"go"}`}),
		wefttest.Say("done"),
	), p.Wrap("research", "", child))
	s, _ := thread.Create(ctx, st, parent)
	turn, _ := s.Send(ctx, weft.User("go"))
	if _, err := turn.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	open, err := thread.Open(ctx, st, s.ID(), parent)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	rs := pool.Receipts(open)
	if len(rs) != 1 || rs[0].State != thread.PoolDone || rs[0].Stop != "persisted answer" {
		t.Fatalf("receipts after restart = %+v", rs)
	}
	if open.Usage().Delegated.OutputTokens != 5 {
		t.Errorf("Delegated after restart = %+v", open.Usage().Delegated)
	}
}

// The many-sessions race: several sessions delegating through one
// pool, concurrently, everything settling exactly once (the -race
// table the step asks for).
func TestManySessionsRace(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	p := pool.New(3)
	const sessions = 8
	const delegates = 4
	var wg sync.WaitGroup
	errs := make(chan error, sessions*delegates)
	for i := 0; i < sessions; i++ {
		// One script with a turn per delegation the session makes —
		// the wrapped call during its turn plus its direct submits.
		turns := make([]wefttest.Turn, delegates+1)
		for j := range turns {
			turns[j] = wefttest.Say("child answer")
		}
		child := weft.New(wefttest.Script(turns...))
		parent := weft.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"go"}`}),
			wefttest.Say("done"),
		), p.Wrap("research", "", child, pool.Async()))
		s, err := thread.Create(ctx, st, parent)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := s.Send(ctx, weft.User("go")); err != nil {
			t.Fatalf("Send: %v", err)
		}
		for d := 0; d < delegates; d++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := p.Submit(ctx, s, child, "go"); err != nil {
					errs <- err
				}
			}()
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("Submit: %v", err)
	}
	// Every child is fast and scripted: settle before closing, so the
	// drain never races the assertions.
	deadline := time.Now().Add(10 * time.Second)
settling:
	for time.Now().Before(deadline) {
		for _, h := range mustList(ctx, t, st) {
			if h.Lineage != nil {
				continue
			}
			open := mustOpen(ctx, t, st, h.ID)
			for _, r := range pool.Receipts(open) {
				if !settledState(r.State) {
					time.Sleep(time.Millisecond)
					continue settling
				}
			}
		}
		break
	}
	if err := p.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Every session: one wrapped receipt per scripted call plus its
	// direct submits, all settled exactly once, ledger matching.
	// The children live in the same storage, linked by lineage; the
	// parents are the headers nobody is a child of.
	var parents []thread.Header
	for _, h := range mustList(ctx, t, st) {
		if h.Lineage == nil {
			parents = append(parents, h)
		}
	}
	if len(parents) != sessions {
		t.Fatalf("parent sessions = %d, want %d", len(parents), sessions)
	}
	for _, h := range parents {
		open := mustOpen(ctx, t, st, h.ID)
		rs := pool.Receipts(open)
		if len(rs) != delegates+1 {
			t.Errorf("session %s: %d receipts, want %d", h.ID, len(rs), delegates+1)
		}
		var settlements int
		for _, r := range rs {
			switch r.State {
			case thread.PoolDone:
				settlements++
			default:
				t.Errorf("session %s receipt %s state %q", h.ID, r.ID, r.State)
			}
		}
		if settlements != len(rs) {
			t.Errorf("session %s: %d of %d receipts settled", h.ID, settlements, len(rs))
		}
	}
}
