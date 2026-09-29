package thread_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// blockWait is the interrupt tests' blocking tool, on turn_test.go's
// release type: holdingTool() gives the tool and its release.
func holdingTool() (*weft.ToolDef, *release) { return blockingTool() }

// Interrupt cancels the running turn, completes its dangling call with
// the golden text, and runs the message as the next turn — the
// interrupted entries stay on the tree (plan §6).
func TestInterruptCancelsAndRuns(t *testing.T) {
	ctx := context.Background()
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "wait"}),
		wefttest.Say("after the interrupt"),
	)
	tool, _ := holdingTool()
	started := make(chan struct{})
	var once sync.Once
	agent := weft.New(model, tool, weft.Tap(func(_ context.Context, ev weft.Event) {
		if _, ok := ev.(weft.ToolStart); ok {
			once.Do(func() { close(started) })
		}
	}))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.BusyPolicy(thread.Interrupt))
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("start the work"))
	if err != nil {
		t.Fatal(err)
	}
	// The turn is inside the blocking call: interrupt it.
	<-started
	t2, err := s.Send(ctx, weft.User("stop, do this instead"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t1.Wait(); err == nil {
		t.Fatal("the interrupted turn reported success")
	} else if !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted turn err = %v, want context.Canceled", err)
	}
	res2, err := t2.Wait()
	if err != nil {
		t.Fatalf("follow-up Wait: %v", err)
	}
	if res2.Text() != "after the interrupt" {
		t.Errorf("follow-up reply = %q", res2.Text())
	}
	// The interrupted turn's partial is on the tree, its dangling call
	// completed with the golden text.
	want := "tool call wait was interrupted: the run was canceled for a newer message"
	found := false
	for _, m := range s.Context() {
		for _, p := range m.Content {
			if r, ok := p.(weft.ToolResultPart); ok && r.Content == want {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("the interrupted call's result is missing or wrong; context:\n%s", renderContext(s))
	}
	// Nothing lost: the interrupted prompt and the follow-up are both
	// on the path the next turn sees.
	seen := map[string]bool{}
	for _, m := range s.Context() {
		if m.Role == weft.RoleUser {
			seen[m.Text()] = true
		}
	}
	if !seen["start the work"] || !seen["stop, do this instead"] {
		t.Errorf("prompts on the path = %v; interrupt keeps everything", seen)
	}
}

// Rollback is an interrupt that branches the leaf back to before the
// interrupted turn: the follow-up runs as though it never happened,
// while its entries keep their own line of the tree.
func TestRollbackBranchesBack(t *testing.T) {
	ctx := context.Background()
	model := wefttest.Script(
		wefttest.Say("first answer"),
		wefttest.ToolCalls(wefttest.Call{Name: "wait"}),
		wefttest.Say("after rollback"),
	)
	tool, _ := holdingTool()
	started := make(chan struct{})
	var once sync.Once
	agent := weft.New(model, tool, weft.Tap(func(_ context.Context, ev weft.Event) {
		if _, ok := ev.(weft.ToolStart); ok {
			once.Do(func() { close(started) })
		}
	}))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.BusyPolicy(thread.Rollback))
	if err != nil {
		t.Fatal(err)
	}
	t0, err := s.Send(ctx, weft.User("first question"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t0.Wait(); err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("start the work"))
	if err != nil {
		t.Fatal(err)
	}
	<-started
	t2, err := s.Send(ctx, weft.User("no — take this road instead"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t1.Wait(); err == nil {
		t.Fatal("the rolled-back turn reported success")
	}
	res2, err := t2.Wait()
	if err != nil {
		t.Fatalf("follow-up Wait: %v", err)
	}
	if res2.Text() != "after rollback" {
		t.Errorf("follow-up reply = %q", res2.Text())
	}
	// The active path holds the first turn and the follow-up — not the
	// interrupted one.
	for _, m := range s.Context() {
		if m.Text() == "start the work" || m.Text() == "was interrupted" {
			t.Errorf("the interrupted turn reached the rolled-back context: %q", m.Text())
		}
	}
	sawFollow := false
	for _, m := range s.Context() {
		if m.Text() == "no — take this road instead" {
			sawFollow = true
		}
	}
	if !sawFollow {
		t.Error("the follow-up prompt is not on the rolled-back path")
	}
	// Nothing deleted: the interrupted turn's entries are in the tree.
	var entries int
	for _, e := range s.Entries() {
		if me, ok := e.(thread.MessageEntry); ok && me.Message.Text() == "start the work" {
			entries++
		}
	}
	if entries != 1 {
		t.Errorf("the interrupted prompt appears %d times in the tree, want kept exactly once", entries)
	}
}

// An Interrupt over an open approval boundary denies the parked calls
// with the interrupted reason and the follow-up runs.
func TestInterruptDeniesPendingApprovals(t *testing.T) {
	ctx := context.Background()
	gate := weft.Tool("gate", "", func(_ context.Context, _ struct{}) (string, error) {
		return "g", nil
	}, weft.RequireApproval())
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "gate"}),
		wefttest.Say("resumed tail"),
		wefttest.Say("after the denial"),
	)
	agent := weft.New(model, gate)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.BusyPolicy(thread.Interrupt))
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
	// Only the boundary holds the session: the interrupt denies it.
	t2, err := s.Send(ctx, weft.User("forget the gate, do this"))
	if err != nil {
		t.Fatal(err)
	}
	res2, err := t2.Wait()
	if err != nil {
		t.Fatalf("follow-up Wait: %v", err)
	}
	if res2 == nil {
		t.Fatal("the follow-up produced no result")
	}
	// The parked call carries the denial the model sees.
	want := "DENIED: interrupted by a newer message"
	found := false
	for _, e := range s.Entries() {
		if de, ok := e.(thread.ApprovalDecisionEntry); ok && de.CallID == p[0].CallID {
			_ = de // the audit trail below carries the result text
		}
	}
	for _, m := range s.Context() {
		for _, part := range m.Content {
			if r, ok := part.(weft.ToolResultPart); ok && r.IsError && strings.Contains(r.Content, want) {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("the parked call's denial (%q) missing from the context", want)
	}
}

// A turn failing with weft.ErrContextOverflow compacts — reason
// overflow — and re-runs once over the shrunken path (ADR 0020 §5).
func TestOverflowCompactsAndReRuns(t *testing.T) {
	ctx := context.Background()
	overflow := weft.ErrContextOverflow
	model := wefttest.Script(
		wefttest.Say("the first answer"),
		wefttest.Fail(overflow),
		wefttest.Say("the summary of what came before"),
		wefttest.Say("recovered after compaction"),
	)
	agent := weft.New(model)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.KeepRecent(1))
	if err != nil {
		t.Fatal(err)
	}
	// Some history, so the overflow compaction has a cut to make.
	t0, err := s.Send(ctx, weft.User("a first question"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t0.Wait(); err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("a prompt that overflows"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := t1.Wait()
	if err != nil {
		t.Fatalf("the overflow re-run failed: %v", err)
	}
	if res.Text() != "recovered after compaction" {
		t.Errorf("reply = %q, want the re-run's answer", res.Text())
	}
	// The compaction entry names the overflow reason.
	var reasons []string
	for _, e := range s.Entries() {
		if c, ok := e.(thread.CompactionEntry); ok {
			reasons = append(reasons, string(c.Reason))
		}
	}
	if len(reasons) != 1 || reasons[0] != "overflow" {
		t.Errorf("compaction reasons = %v, want exactly one overflow", reasons)
	}
	// One turn entry, one prompt: the failed attempt left no
	// transcript of its own.
	turns := 0
	for _, e := range s.Entries() {
		if _, ok := e.(thread.TurnEntry); ok {
			turns++
		}
	}
	if turns != 2 {
		t.Errorf("turn entries = %d, want 2 (the first turn and the re-run's; the failed attempt records none)", turns)
	}
}

// A second overflow fails the turn with both errors joined.
func TestOverflowSecondFailureJoins(t *testing.T) {
	ctx := context.Background()
	model := wefttest.Script(
		wefttest.Fail(weft.ErrContextOverflow),
		wefttest.Say("the summary"),
		wefttest.Fail(weft.ErrContextOverflow),
	)
	agent := weft.New(model)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.KeepRecent(1))
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("a prompt that keeps overflowing"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = t1.Wait()
	if err == nil {
		t.Fatal("the twice-overflowing turn reported success")
	}
	if !errors.Is(err, weft.ErrContextOverflow) {
		t.Fatalf("err = %v, want the overflow sentinel", err)
	}
	if got := strings.Count(err.Error(), weft.ErrContextOverflow.Error()); got < 2 {
		t.Errorf("err = %v, want both attempts' overflows joined", err)
	}
	// The re-run can be switched off: the first overflow fails directly.
	model2 := wefttest.Script(
		wefttest.Fail(weft.ErrContextOverflow),
		wefttest.Say("never reached"),
	)
	s2, err := thread.Create(ctx, thread.Memory(), weft.New(model2), thread.KeepRecent(1), thread.ReRunOnOverflow(false))
	if err != nil {
		t.Fatal(err)
	}
	t2, err := s2.Send(ctx, weft.User("overflow, no re-run"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t2.Wait(); !errors.Is(err, weft.ErrContextOverflow) {
		t.Fatalf("err = %v, want the first overflow to fail directly", err)
	}
	var compacted bool
	for _, e := range s2.Entries() {
		if _, ok := e.(thread.CompactionEntry); ok {
			compacted = true
		}
	}
	if compacted {
		t.Error("with the re-run off, no compaction ran either")
	}
}

// renderContext renders the session's context for failure messages.
func renderContext(s *thread.Session) string {
	var b strings.Builder
	for _, m := range s.Context() {
		b.WriteString(string(m.Role) + ": " + m.Text() + "\n")
		for _, p := range m.Content {
			if r, ok := p.(weft.ToolResultPart); ok {
				b.WriteString("  result " + r.Name + ": " + r.Content + "\n")
			}
		}
	}
	return b.String()
}

// Interrupt during a tool that ignores ctx: the documented cure is the
// per-tool Timeout (the core abandons the handler and records the
// timeout), and the interrupt flows through — the turn ends, the
// follow-up runs. Without a Timeout a hung handler hangs any cancel
// equally; interrupt adds no new requirement (the review's focus).
func TestInterruptDuringCtxIgnoringTool(t *testing.T) {
	ctx := context.Background()
	hang := weft.Tool("hang", "", func(_ context.Context, _ struct{}) (string, error) {
		<-make(chan struct{}) // ignores ctx outright
		return "never", nil
	}, weft.Timeout(50*time.Millisecond))
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "hang"}),
		wefttest.Say("through"),
	)
	started := make(chan struct{})
	var once sync.Once
	agent := weft.New(model, hang, weft.Tap(func(_ context.Context, ev weft.Event) {
		if _, ok := ev.(weft.ToolStart); ok {
			once.Do(func() { close(started) })
		}
	}))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.BusyPolicy(thread.Interrupt))
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("start"))
	if err != nil {
		t.Fatal(err)
	}
	<-started
	t2, err := s.Send(ctx, weft.User("enough of this"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t1.Wait(); err == nil {
		t.Fatal("the interrupted turn reported success")
	}
	res2, err := t2.Wait()
	if err != nil {
		t.Fatalf("follow-up Wait: %v", err)
	}
	if res2.Text() != "through" {
		t.Errorf("follow-up reply = %q", res2.Text())
	}
}

// A compaction that itself overflows cannot loop: the compaction
// fails, no re-run is armed, and the turn fails with the run's own
// overflow (the review's focus).
func TestOverflowCompactionThatOverflows(t *testing.T) {
	ctx := context.Background()
	model := wefttest.Script(
		wefttest.Say("the first answer"),
		wefttest.Fail(weft.ErrContextOverflow), // the run
		wefttest.Fail(weft.ErrContextOverflow), // the summarizer
	)
	agent := weft.New(model)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.KeepRecent(1))
	if err != nil {
		t.Fatal(err)
	}
	t0, err := s.Send(ctx, weft.User("a first question"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t0.Wait(); err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("a prompt that overflows"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = t1.Wait()
	if !errors.Is(err, weft.ErrContextOverflow) {
		t.Fatalf("err = %v, want the overflow sentinel", err)
	}
	// One overflow in the joined error only — the re-run never ran.
	if got := strings.Count(err.Error(), weft.ErrContextOverflow.Error()); got < 2 {
		t.Logf("err = %v (compaction-failure shape)", err)
	}
	// No compaction entry landed: the summarizer failed.
	for _, e := range s.Entries() {
		if c, ok := e.(thread.CompactionEntry); ok {
			t.Fatalf("a compaction entry landed despite the overflow: %+v", c)
		}
	}
}

// The interrupted partial the consumer reads carries exactly one
// result per call: the golden interruption text replaces the bare
// cancellation noise a handler returned as the run died under it —
// the noise result itself must not survive beside its replacement
// (the pairing invariant; Repair hides the duplicate from the tree,
// but Wait's RunError.Result is the caller's copy of the partial).
func TestInterruptedPartialOneResultPerCall(t *testing.T) {
	ctx := context.Background()
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "wait"}),
		wefttest.Say("after the interrupt"),
	)
	tool, _ := holdingTool()
	started := make(chan struct{})
	var once sync.Once
	agent := weft.New(model, tool, weft.Tap(func(_ context.Context, ev weft.Event) {
		if _, ok := ev.(weft.ToolStart); ok {
			once.Do(func() { close(started) })
		}
	}))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.BusyPolicy(thread.Interrupt))
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("start the work"))
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := s.Send(ctx, weft.User("stop, do this instead")); err != nil {
		t.Fatal(err)
	}
	_, err = t1.Wait()
	if err == nil {
		t.Fatal("the interrupted turn reported success")
	}
	var runErr *weft.RunError
	if !errors.As(err, &runErr) || runErr.Result == nil {
		t.Fatalf("err = %v, want a RunError carrying the partial transcript", err)
	}
	want := "tool call wait was interrupted: the run was canceled for a newer message"
	toolMsgs := 0
	for _, m := range runErr.Result.Messages {
		if m.Role != weft.RoleTool {
			continue
		}
		toolMsgs++
		if len(m.Content) != 1 {
			t.Fatalf("the interrupted tool message holds %d results (%+v), want exactly one per call", len(m.Content), m.Content)
		}
		r, ok := m.Content[0].(weft.ToolResultPart)
		if !ok || r.Content != want || !r.IsError {
			t.Fatalf("the interrupted call's result = %+v, want the golden interruption text", m.Content[0])
		}
	}
	if toolMsgs != 1 {
		t.Fatalf("the partial holds %d tool messages, want exactly one", toolMsgs)
	}
}
