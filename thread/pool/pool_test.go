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
func waitState(t *testing.T, parent *thread.Session, state pool.State) pool.Receipt {
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

// Submit's argument contract: a nil parent or agent is an error, not
// a panic mid-delegation.
func TestSubmitValidates(t *testing.T) {
	ctx := context.Background()
	p := pool.New(1)
	s, _ := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
	if _, err := p.Submit(ctx, nil, weft.New(wefttest.Script()), "go"); err == nil {
		t.Errorf("nil parent accepted")
	}
	if _, err := p.Submit(ctx, s, nil, "go"); err == nil {
		t.Errorf("nil agent accepted")
	}
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
	), p.MustWrap("research", "delegates research", child))
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

// A pool child's run carries its lineage identity (ADR 0024 S5): the
// child session's own weft.session.id and turn, plus
// weft.session.parent and — for a wrapped delegation, where a call
// delegated — weft.session.parent_call.
func TestWrapChildRunIdentity(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	var mu sync.Mutex
	var md map[string]string
	child := weft.New(wefttest.Script(wefttest.Say("the bug is in compaction")),
		weft.Tap(func(ctx context.Context, ev weft.Event) {
			if _, ok := ev.(weft.RunStart); ok {
				mu.Lock()
				md = weft.MetadataFromContext(ctx)
				mu.Unlock()
			}
		}))
	p := pool.New(2)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research",
			Args: wefttest.Args(struct{ Prompt string }{"find the bug"})}),
		wefttest.Say("done"),
	), p.MustWrap("research", "delegates research", child))
	s, err := thread.Create(ctx, st, parent)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	turn, err := s.Send(ctx, weft.User("where is the bug?"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	rs := pool.Receipts(s)
	if len(rs) != 1 {
		t.Fatalf("receipts = %+v, want one", rs)
	}
	mu.Lock()
	got := md
	mu.Unlock()
	if got == nil {
		t.Fatal("the child run never started")
	}
	if got["weft.session.parent"] != s.ID() {
		t.Errorf("weft.session.parent = %q, want the parent session %q", got["weft.session.parent"], s.ID())
	}
	if got["weft.session.parent_call"] == "" {
		t.Errorf("weft.session.parent_call missing: %v — a wrapped delegation names its call", got)
	}
	if got["weft.session.id"] != rs[0].Child {
		t.Errorf("weft.session.id = %q, want the child session %q", got["weft.session.id"], rs[0].Child)
	}
	if got["weft.turn"] != "1" {
		t.Errorf("weft.turn = %q, want 1 — the child's own first turn", got["weft.turn"])
	}
	if _, ok := got["weft.session.forked_from"]; ok {
		t.Errorf("a pool child carries forked_from: %v — lineage is a reference, not a copied path", got)
	}
}

// A failing child is a tool error the parent model sees — failure is
// data (ADR 0014) — and the receipt settles failed. The model-visible
// failure text is ADR 0014's pinned shape, byte-for-byte: the pool
// replaces the mechanism, not the contract.
func TestWrapSyncFails(t *testing.T) {
	ctx := context.Background()
	cause := errors.New("connection reset")
	child := weft.New(wefttest.Script(wefttest.SayThenFail("partial", cause)))
	p := pool.New(1)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research",
			Args: wefttest.Args(struct{ Prompt string }{"go"})}),
		wefttest.Say("noted"),
	), p.MustWrap("research", "", child))
	s, _ := thread.Create(ctx, thread.Memory(), parent)
	turn, err := s.Send(ctx, weft.User("go"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	res, err := turn.Wait()
	if err != nil {
		t.Fatalf("a failed child must not fail the parent turn: %v", err)
	}
	want := `SUBAGENT_FAILED: agent "research" failed at step 0: model stream: connection reset`
	if !strings.Contains(transcriptText(res.Messages), want) {
		t.Errorf("parent transcript lacks the pinned failure text %q:\n%s", want, transcriptText(res.Messages))
	}
	rs := pool.Receipts(s)
	if len(rs) != 1 || rs[0].State != thread.PoolFailed {
		t.Fatalf("receipts = %+v", rs)
	}
	if rs[0].Stop == "" {
		t.Errorf("failed receipt carries no cause: %+v", rs[0])
	}
}

// transcriptText renders a transcript's tool results, for substring
// assertions over what the model saw.
func transcriptText(msgs []weft.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		for _, part := range m.Content {
			if tp, ok := part.(weft.ToolResultPart); ok {
				b.WriteString(tp.Content)
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
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
	), p.MustWrap("research", "", child))
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
	), p.MustWrap("research", "", child, pool.Async()))
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
	if err := p.Cancel(ctx, s, r.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	final := waitState(t, s, thread.PoolCanceled)
	if final.ID != r.ID {
		t.Errorf("settled receipt = %+v, want %s", final, r.ID)
	}
	close(release) // the model's goroutine, if it slipped in, may end
	// A settled receipt refuses to cancel again, and says what it is;
	// an id the ledger does not hold is a different error.
	err = p.Cancel(ctx, s, r.ID)
	var se *pool.StateError
	if !errors.Is(err, pool.ErrNotRunning) || !errors.As(err, &se) || se.State != pool.Canceled || se.Orphan {
		t.Errorf("second Cancel err = %v (%+v), want a StateError at canceled", err, se)
	}
	if err := p.Cancel(ctx, s, "e_nope"); !errors.Is(err, pool.ErrUnknownReceipt) || errors.Is(err, pool.ErrNotRunning) {
		t.Errorf("Cancel of an unknown receipt err = %v, want ErrUnknownReceipt", err)
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
	), p.MustWrap("ask", "", child))
	res, err := parent.Generate(ctx, weft.Prompt("go"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if res.Text() != "done" {
		t.Errorf("reply = %q", res.Text())
	}
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
	), p.MustWrap("research", "", child))
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
	// The link survives the restart from both ends: the parent's
	// receipt names the child, and the child's header names the
	// parent (ADR 0022 §3).
	childOpen, err := thread.Open(ctx, st, rs[0].Child, child)
	if err != nil {
		t.Fatalf("Open child after restart: %v", err)
	}
	if lin := childOpen.Lineage(); lin.Session != open.ID() {
		t.Errorf("child lineage after restart = %+v, want session %s", lin, open.ID())
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
		// One wrap name names one agent: each session's own child gets
		// its own.
		name := fmt.Sprintf("research%d", i)
		parent := weft.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: name, Args: `{"prompt":"go"}`}),
			wefttest.Say("done"),
		), p.MustWrap(name, "", child, pool.Async()))
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
				if !r.Settled() {
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
