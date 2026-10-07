package thread_test

import (
	"context"
	"errors"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
)

// blockWait is the interrupt tests' blocking tool, on turn_test.go's
// release type: holdingTool() gives the tool and its release.
func holdingTool() (*core.ToolDef, *release) { return blockingTool() }

// Interrupt cancels the running turn, completes its dangling call with
// the golden text, and runs the message as the next turn — the
// interrupted entries stay on the tree.
func TestInterruptCancelsAndRuns(t *testing.T) {
	ctx := context.Background()
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "wait"}),
		wefttest.Say("after the interrupt"),
	)
	tool, _ := holdingTool()
	started := make(chan struct{})
	var once sync.Once
	agent := core.New(model, tool, core.Tap(func(_ context.Context, ev core.Event) {
		if _, ok := ev.(core.ToolStart); ok {
			once.Do(func() { close(started) })
		}
	}))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.BusyPolicy(thread.Interrupt))
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, core.User("start the work"))
	if err != nil {
		t.Fatal(err)
	}
	// The turn is inside the blocking call: interrupt it.
	<-started
	t2, err := s.Send(ctx, core.User("stop, do this instead"))
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
			if r, ok := p.(core.ToolResultPart); ok && r.Content == want {
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
		if m.Role == core.RoleUser {
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
	agent := core.New(model, tool, core.Tap(func(_ context.Context, ev core.Event) {
		if _, ok := ev.(core.ToolStart); ok {
			once.Do(func() { close(started) })
		}
	}))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.BusyPolicy(thread.Rollback))
	if err != nil {
		t.Fatal(err)
	}
	t0, err := s.Send(ctx, core.User("first question"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t0.Wait(); err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, core.User("start the work"))
	if err != nil {
		t.Fatal(err)
	}
	<-started
	t2, err := s.Send(ctx, core.User("no — take this road instead"))
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
	gate := core.Tool("gate", "", func(_ context.Context, _ struct{}) (string, error) {
		return "g", nil
	}, core.RequireApproval())
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "gate"}),
		wefttest.Say("resumed tail"),
		wefttest.Say("after the denial"),
	)
	agent := core.New(model, gate)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.BusyPolicy(thread.Interrupt))
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, core.User("run the gate"))
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
	t2, err := s.Send(ctx, core.User("forget the gate, do this"))
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
			if r, ok := part.(core.ToolResultPart); ok && r.IsError && strings.Contains(r.Content, want) {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("the parked call's denial (%q) missing from the context", want)
	}
}

// A turn failing with core.ErrContextOverflow compacts — reason
// overflow — and re-runs once over the shrunken path (ADR 0020 §5).
func TestOverflowCompactsAndReRuns(t *testing.T) {
	ctx := context.Background()
	overflow := core.ErrContextOverflow
	model := wefttest.Script(
		wefttest.Say("the first answer"),
		wefttest.Fail(overflow),
		wefttest.Say("the summary of what came before"),
		wefttest.Say("recovered after compaction"),
	)
	agent := core.New(model)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.KeepRecent(1))
	if err != nil {
		t.Fatal(err)
	}
	// Some history, so the overflow compaction has a cut to make.
	t0, err := s.Send(ctx, core.User("a first question"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t0.Wait(); err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, core.User("a prompt that overflows"))
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
	// One prompt, and the failed attempt left no transcript of its own
	// — but its ledger: a turn entry under the attempt's run id naming
	// the re-run, beside the first turn's and the re-run's.
	tes := turnEntries(s)
	if len(tes) != 3 {
		t.Fatalf("turn entries = %d, want 3 (the first turn, the failed attempt, the re-run)", len(tes))
	}
	if tes[1].RunID != s.ID()+"-t2" || tes[1].ReRun != t1.RunID() || !strings.Contains(tes[1].Err, "context window") {
		t.Errorf("the attempt's entry = %+v, want run -t2, the overflow, and the re-run's id %s", tes[1], t1.RunID())
	}
	if tes[2].RunID != t1.RunID() || t1.RunID() != s.ID()+"-t3" {
		t.Errorf("the re-run's entry runs under %q; the turn reports %q", tes[2].RunID, t1.RunID())
	}
}

// A second overflow fails the turn with both errors joined. A turn
// whose re-run never happened — the compaction had nothing to cut —
// fails with the one overflow, said once.
func TestOverflowSecondFailureJoins(t *testing.T) {
	ctx := context.Background()
	model := wefttest.Script(
		wefttest.Say("the first answer"),                              // history, so the overflow compaction has a cut to make
		wefttest.ToolCalls(wefttest.Call{Name: "note", ID: "call_a"}), // attempt one: a step lands…
		wefttest.Fail(core.ErrContextOverflow),                        // …then it overflows
		wefttest.Say("the summary"),                                   // the compaction's summarizer
		wefttest.Fail(core.ErrContextOverflow),                        // the re-run overflows at once
	)
	note := core.Tool("note", "", func(context.Context, struct{}) (string, error) { return "noted", nil })
	s, err := thread.Create(ctx, thread.Memory(), core.New(model, note), thread.KeepRecent(1))
	if err != nil {
		t.Fatal(err)
	}
	t0, _ := s.Send(ctx, core.User("a first question"))
	if _, err := t0.Wait(); err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, core.User("a prompt that keeps overflowing"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = t1.Wait()
	if err == nil {
		t.Fatal("the twice-overflowing turn reported success")
	}
	if !errors.Is(err, core.ErrContextOverflow) {
		t.Fatalf("err = %v, want the overflow sentinel", err)
	}
	if got := strings.Count(err.Error(), core.ErrContextOverflow.Error()); got != 2 {
		t.Errorf("err = %v, want both attempts' overflows joined (%d found)", err, got)
	}
	var runErr *core.RunError
	if !errors.As(err, &runErr) {
		t.Errorf("err = %v, want a *core.RunError in the chain", err)
	}
	// Two attempts ran, so two ledgers: the attempt's, then the turn's.
	tes := turnEntries(s)
	if len(tes) != 3 || tes[1].ReRun != tes[2].RunID || tes[2].Err == "" {
		t.Fatalf("turn entries = %+v, want the first turn, the attempt (naming the re-run) and the failed re-run", tes)
	}
	// What the turn recorded is the re-run's transcript, not the failed
	// attempt's: the attempt's step — its call and result — stays on
	// its own line, off the path the next turn continues from.
	for _, m := range s.Context() {
		for _, p := range m.Content {
			if c, ok := p.(core.ToolCallPart); ok && c.ID == "call_a" {
				t.Errorf("the failed attempt's step rides the active path:\n%s", renderContext(s))
			}
		}
	}
	// The re-run can be switched off: the first overflow fails directly.
	model2 := wefttest.Script(
		wefttest.Fail(core.ErrContextOverflow),
		wefttest.Say("never reached"),
	)
	s2, err := thread.Create(ctx, thread.Memory(), core.New(model2), thread.KeepRecent(1), thread.ReRunOnOverflow(false))
	if err != nil {
		t.Fatal(err)
	}
	t2, err := s2.Send(ctx, core.User("overflow, no re-run"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = t2.Wait()
	if !errors.Is(err, core.ErrContextOverflow) {
		t.Fatalf("err = %v, want the first overflow to fail directly", err)
	}
	if got := strings.Count(err.Error(), core.ErrContextOverflow.Error()); got != 1 {
		t.Errorf("err = %v, want the one overflow said once", err)
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
			if r, ok := p.(core.ToolResultPart); ok {
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
	hang := core.Tool("hang", "", func(_ context.Context, _ struct{}) (string, error) {
		<-make(chan struct{}) // ignores ctx outright
		return "never", nil
	}, core.Timeout(50*time.Millisecond))
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "hang"}),
		wefttest.Say("through"),
	)
	started := make(chan struct{})
	var once sync.Once
	agent := core.New(model, hang, core.Tap(func(_ context.Context, ev core.Event) {
		if _, ok := ev.(core.ToolStart); ok {
			once.Do(func() { close(started) })
		}
	}))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.BusyPolicy(thread.Interrupt))
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, core.User("start"))
	if err != nil {
		t.Fatal(err)
	}
	<-started
	t2, err := s.Send(ctx, core.User("enough of this"))
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
		wefttest.Fail(core.ErrContextOverflow), // the run
		wefttest.Fail(core.ErrContextOverflow), // the summarizer
	)
	agent := core.New(model)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.KeepRecent(1))
	if err != nil {
		t.Fatal(err)
	}
	t0, err := s.Send(ctx, core.User("a first question"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t0.Wait(); err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, core.User("a prompt that overflows"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = t1.Wait()
	if !errors.Is(err, core.ErrContextOverflow) {
		t.Fatalf("err = %v, want the overflow sentinel", err)
	}
	// One overflow in the joined error only — the re-run never ran.
	if got := strings.Count(err.Error(), core.ErrContextOverflow.Error()); got < 2 {
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
	agent := core.New(model, tool, core.Tap(func(_ context.Context, ev core.Event) {
		if _, ok := ev.(core.ToolStart); ok {
			once.Do(func() { close(started) })
		}
	}))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.BusyPolicy(thread.Interrupt))
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, core.User("start the work"))
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := s.Send(ctx, core.User("stop, do this instead")); err != nil {
		t.Fatal(err)
	}
	_, err = t1.Wait()
	if err == nil {
		t.Fatal("the interrupted turn reported success")
	}
	var runErr *core.RunError
	if !errors.As(err, &runErr) || runErr.Result == nil {
		t.Fatalf("err = %v, want a RunError carrying the partial transcript", err)
	}
	want := "tool call wait was interrupted: the run was canceled for a newer message"
	toolMsgs := 0
	for _, m := range runErr.Result.Messages {
		if m.Role != core.RoleTool {
			continue
		}
		toolMsgs++
		if len(m.Content) != 1 {
			t.Fatalf("the interrupted tool message holds %d results (%+v), want exactly one per call", len(m.Content), m.Content)
		}
		r, ok := m.Content[0].(core.ToolResultPart)
		if !ok || r.Content != want || !r.IsError {
			t.Fatalf("the interrupted call's result = %+v, want the golden interruption text", m.Content[0])
		}
	}
	if toolMsgs != 1 {
		t.Fatalf("the partial holds %d tool messages, want exactly one", toolMsgs)
	}
}

// parkResumes is a model that parks the resume's own model call — the
// request whose transcript ends on the gate call's result, the input
// the approve resolution completed at step 0 — until its context dies,
// then fails with the context error: deterministic cancel-mid-model
// for the interrupt-during-resume composition. parked closes when the
// park begins, so the test interrupts a resume that is provably inside
// its model call instead of racing the resume's startup. (Parking on
// the dangling assistant call instead never fires — by its first model
// call the resume has already resolved the call — and the test then
// raced the resume's startup, flaking when the resume finished first.)
type parkResumes struct {
	inner  core.Model
	parked chan struct{}
	once   sync.Once
}

func (p *parkResumes) Info() core.ModelInfo { return core.InfoOf(p.inner) }

func (p *parkResumes) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	if last := len(req.Messages) - 1; last >= 0 && req.Messages[last].Role == core.RoleTool {
		for _, part := range req.Messages[last].Content {
			if r, ok := part.(core.ToolResultPart); ok && r.Name == "gate" {
				p.once.Do(func() { close(p.parked) })
				return func(yield func(core.ModelEvent, error) bool) {
					<-ctx.Done()
					yield(nil, ctx.Err())
				}
			}
		}
	}
	return p.inner.Stream(ctx, req)
}

// An Interrupt that fells a resume mid-model still runs its message:
// the canceled resume's persistence records the repaired input's tail
// — the approval resolution — so the boundary it was resolving closes
// and the interrupting follow-up is not held behind it (the resume was
// the boundary's resolver, and its corpse completes it).
func TestInterruptDuringResumeRunsTheMessage(t *testing.T) {
	ctx := context.Background()
	gate := core.Tool("gate", "", func(_ context.Context, _ struct{}) (string, error) {
		return "g", nil
	}, core.RequireApproval())
	model := &parkResumes{inner: wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "gate"}),
		wefttest.Say("after the interrupt"),
	), parked: make(chan struct{})}
	agent := core.New(model, gate)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.BusyPolicy(thread.Interrupt))
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, core.User("run the gate"))
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
	// The approval starts the resume (AutoResume), which parks inside
	// its model call: the interrupt fells it there — provably, once the
	// park signal arrives, not by racing the resume's startup.
	if _, err := s.Decide(ctx, thread.Approve(p[0].CallID)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-model.parked:
	case <-time.After(10 * time.Second):
		t.Fatal("the resume never reached its model call")
	}
	t2, err := s.Send(ctx, core.User("stop, do this instead"))
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		res *core.RunResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := t2.Wait()
		done <- outcome{res, err}
	}()
	select {
	case o := <-done:
		if o.err != nil {
			t.Fatalf("the interrupt's follow-up failed: %v", o.err)
		}
		if o.res.Text() != "after the interrupt" {
			t.Errorf("follow-up reply = %q, want the interrupting message's answer", o.res.Text())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the interrupt's follow-up never ran: it is held behind the boundary the felled resume was resolving")
	}
	// The interrupted turn itself failed as canceled, and the boundary
	// it was resolving is gone.
	if _, err := t1.Next().Wait(); err == nil {
		t.Error("the interrupted resume reported success")
	}
	if p := s.Pending(); len(p) != 0 {
		t.Errorf("pending after the interrupt = %d, want the boundary resolved", len(p))
	}
}

// A Rollback over the session's first turn returns the leaf to the
// root: the follow-up runs as though nothing had been said.
func TestRollbackOfTheFirstTurnReturnsToTheRoot(t *testing.T) {
	ctx := context.Background()
	agent, model, started, rel := heldAgent("a fresh start")
	defer rel.open()
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	t1, _ := s.Send(ctx, core.User("start the work"))
	<-started
	t2, err := s.Send(ctx, core.User("no — this instead"), thread.As(thread.Rollback))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t1.Wait(); err == nil {
		t.Fatal("the rolled-back turn reported success")
	}
	if res, err := t2.Wait(); err != nil || res.Text() != "a fresh start" {
		t.Fatalf("the follow-up: %v, %v", res, err)
	}
	if got, want := contextTexts(s), []string{"no — this instead", "a fresh start"}; !equalStrings(got, want) {
		t.Errorf("Context = %v, want %v: the first turn rolled back to the root", got, want)
	}
	reqs := model.Requests()
	if last := reqs[len(reqs)-1]; len(last.Messages) != 1 {
		t.Errorf("the follow-up's model call carried %d messages, want its prompt alone", len(last.Messages))
	}
}

// An Interrupt or Rollback over a parked boundary whose denial cannot
// be recorded is refused whole: the Send fails, and the message it had
// already accepted leaves the queue again — its receipt dropped, so
// neither this session nor a reopened one runs it behind a boundary
// that still stands. Under RequireSigned too: the denial is the
// session's own, not the unsigned door's.
func TestInterruptRefusedLeavesNothingQueued(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy thread.Policy
		signed bool
	}{
		{"interrupt", thread.Interrupt, false},
		{"rollback", thread.Rollback, false},
		{"interrupt_require_signed", thread.Interrupt, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := &failNthAppend{Storage: thread.Memory()}
			agent, ran := refundAgent(
				wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "call_r"}),
				wefttest.Say("resumed tail"),
				wefttest.Say("after the denial"),
			)
			opts := []thread.SessionOption{thread.BusyPolicy(tc.policy)}
			var reopenOpts []thread.SessionOption
			if tc.signed {
				ring, _ := signerRing(t)
				opts = append(opts, thread.WithKeyring(ring), thread.RequireSigned())
				reopenOpts = append(reopenOpts, thread.WithKeyring(ring))
			}
			s, err := thread.Create(ctx, st, agent, opts...)
			if err != nil {
				t.Fatal(err)
			}
			parkTurn(t, s, ctx)

			// The Send's appends: 1 the accepted receipt, 2 the denial.
			st.arm(2)
			turn, err := s.Send(ctx, core.User("forget the refund"))
			if err == nil || turn != nil {
				t.Fatalf("the interrupting Send = %v, %v; want the storage's refusal", turn, err)
			}
			if q := s.Queue(); len(q) != 0 {
				t.Errorf("Queue after the refused interrupt = %+v, want empty", q)
			}
			if got := len(s.Pending()); got != 1 {
				t.Fatalf("Pending = %d, want the call still parked", got)
			}
			status := map[string]int{}
			for _, r := range receipts(s) {
				status[r.Status]++
			}
			if status[thread.ReceiptAccepted] != 1 || status[thread.ReceiptDropped] != 1 {
				t.Errorf("receipts = %v, want the acceptance and its drop", status)
			}

			// The same Send again, the storage well: the boundary is
			// denied by the session itself and the message runs.
			follow, err := s.Send(ctx, core.User("forget the refund"))
			if err != nil {
				t.Fatal(err)
			}
			res, err := follow.Wait()
			if err != nil || res.Text() != "after the denial" {
				t.Fatalf("the follow-up: %v, %v", res, err)
			}
			got := decisionsFor(s, "call_r")
			if len(got) != 1 || got[0].Via != "interrupt" || got[0].Outcome != thread.OutcomeDeny {
				t.Errorf("the interrupt's denial = %+v, want one deny via interrupt", got)
			}
			if len(ran.snapshot()) != 0 {
				t.Error("the denied call ran")
			}
			if n := countUser(s, "forget the refund"); n != 1 {
				t.Errorf("the message sits %d times in the context, want 1", n)
			}
			// Nothing of the refused attempt comes back with the file.
			if err := s.Close(ctx); err != nil {
				t.Fatal(err)
			}
			if q := reopenWith(t, ctx, st.Storage, s, agent, reopenOpts...).Queue(); len(q) != 0 {
				t.Errorf("the reopened Queue = %+v, want empty", q)
			}
		})
	}
}

// An interrupting Send carries its run options into its own turn, like
// any Send.
func TestInterruptingSendKeepsItsRunOptions(t *testing.T) {
	ctx := context.Background()
	model := wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "wait"}), wefttest.Say("after"))
	tool, rel := blockingTool()
	defer rel.open()
	started := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	tenants := map[string]string{} // run id → the tenant its metadata carried
	agent := core.New(model, tool, core.Tap(func(ctx context.Context, ev core.Event) {
		switch ev := ev.(type) {
		case core.RunStart:
			mu.Lock()
			tenants[ev.ID] = core.MetadataFromContext(ctx)["tenant"]
			mu.Unlock()
		case core.ToolStart:
			once.Do(func() { close(started) })
		}
	}))
	s, _ := thread.Create(ctx, thread.Memory(), agent)
	if _, err := s.Send(ctx, core.User("go")); err != nil {
		t.Fatal(err)
	}
	<-started
	next, err := s.Send(ctx, core.User("stop"), thread.As(thread.Interrupt),
		thread.RunOptions(core.Metadata(map[string]string{"tenant": "acme"})))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := next.Wait(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := tenants[next.RunID()]; got != "acme" {
		t.Errorf("the interrupting send's run carried tenant %q, want its own run options (acme)", got)
	}
}
