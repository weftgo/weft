package thread_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// scriptedAgent is an agent whose scripted model reports usage u per
// step, so the trigger's lastInput lands where the test wants.
func scriptedAgent(u weft.Usage, turns int) (*weft.Agent, weft.Model) {
	turnsList := make([]wefttest.Turn, turns)
	for i := range turnsList {
		turnsList[i] = wefttest.Say("reply").WithUsage(u)
	}
	m := wefttest.Script(turnsList...)
	return weft.New(m), m
}

func bigUsage() weft.Usage {
	return weft.Usage{InputTokens: 90_000, OutputTokens: 5}
}

// waitFor polls until cond or the deadline, failing the test on a
// timeout.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func hasCompaction(s *thread.Session) int {
	n := 0
	for _, e := range s.Entries() {
		if _, ok := e.(thread.CompactionEntry); ok {
			n++
		}
	}
	return n
}

func TestContextWindowFiresTrigger(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent, _ := scriptedAgent(bigUsage(), 6)
		s, _ := thread.Create(ctx, st, agent, thread.ContextWindow(100_000))
		// A big recorded history, then turns whose reported input
		// crosses the line: 90k of a 100k window minus 16,384 reserve.
		msgs(t, ctx, st, s,
			strings.Repeat("a", 30_000),
			strings.Repeat("b", 30_000),
			strings.Repeat("c", 30_000))
		s = reopenWith(t, ctx, st, s, agent, thread.ContextWindow(100_000))
		for i := 0; i < 2; i++ {
			turn, err := s.Send(ctx, weft.User("go"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := turn.Wait(); err != nil {
				t.Fatal(err)
			}
			_ = s.WaitIdle(ctx) // the post-turn trigger runs once the turn is decided
		}
		waitFor(t, "an automatic compaction", func() bool { return hasCompaction(s) > 0 })
		for _, e := range s.Entries() {
			if c, ok := e.(thread.CompactionEntry); ok && c.Reason != thread.ReasonThreshold {
				t.Errorf("compaction reason = %q, want threshold", c.Reason)
			}
		}
	})
}

func TestModelWindowsOverride(t *testing.T) {
	ctx := context.Background()
	agent, _ := scriptedAgent(bigUsage(), 2)
	info := weft.InfoOf(agent.Model())
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, agent,
		thread.ModelWindows(map[weft.ModelInfo]int64{info: 100_000}))
	// The override is in force: the same shape as the window test,
	// checked through the trigger firing.
	msgs(t, ctx, st, s,
		strings.Repeat("a", 30_000),
		strings.Repeat("b", 30_000),
		strings.Repeat("c", 30_000))
	s = reopenWith(t, ctx, st, s, agent, thread.ModelWindows(map[weft.ModelInfo]int64{info: 100_000}))
	turn, err := s.Send(ctx, weft.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	_ = s.WaitIdle(ctx) // the post-turn trigger runs once the turn is decided
	waitFor(t, "the per-model window to fire", func() bool { return hasCompaction(s) > 0 })

	// A window for a different model leaves the session window-less.
	other := weft.ModelInfo{Provider: "other", Name: "nope"}
	s2, _ := thread.Create(ctx, st, agent,
		thread.ModelWindows(map[weft.ModelInfo]int64{other: 100_000}))
	if hasCompaction(s2) != 0 {
		t.Error("another model's window fired")
	}
}

func TestDisabledAndNoWindow(t *testing.T) {
	ctx := context.Background()
	agent, _ := scriptedAgent(bigUsage(), 4)
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, agent, thread.ContextWindow(100_000), thread.NoAutoCompact())
	msgs(t, ctx, st, s,
		strings.Repeat("a", 30_000),
		strings.Repeat("b", 30_000),
		strings.Repeat("c", 30_000))
	s = reopenWith(t, ctx, st, s, agent, thread.ContextWindow(100_000), thread.NoAutoCompact())
	for i := 0; i < 2; i++ {
		turn, err := s.Send(ctx, weft.User("go"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := turn.Wait(); err != nil {
			t.Fatal(err)
		}
		_ = s.WaitIdle(ctx) // the post-turn trigger runs once the turn is decided
	}
	if hasCompaction(s) != 0 {
		t.Error("Disabled session auto-compacted")
	}
	// Manual compaction still works.
	s = reopenWith(t, ctx, st, s, agent)
	if err := s.Compact(ctx); err != nil {
		t.Errorf("manual Compact under Disabled: %v", err)
	}
}

func TestTriggerFuncAndRateLimits(t *testing.T) {
	ctx := context.Background()
	agent, _ := scriptedAgent(bigUsage(), 8)
	st := thread.Memory()
	var mu sync.Mutex
	var seen []thread.TriggerInput
	s, _ := thread.Create(ctx, st, agent,
		thread.ContextWindow(100_000),
		thread.TriggerFunc(func(in thread.TriggerInput) bool {
			mu.Lock()
			defer mu.Unlock()
			seen = append(seen, in)
			return in.LastInput > 0 && in.Window == 100_000
		}),
		thread.MinTurnsBetween(5),
		thread.MaxPerSession(1),
	)
	msgs(t, ctx, st, s,
		strings.Repeat("a", 30_000),
		strings.Repeat("b", 30_000),
		strings.Repeat("c", 30_000))
	s = reopenWith(t, ctx, st, s, agent,
		thread.ContextWindow(100_000),
		thread.TriggerFunc(func(in thread.TriggerInput) bool {
			seen = append(seen, in)
			return in.LastInput > 0 && in.Window == 100_000
		}),
		thread.MinTurnsBetween(5),
		thread.MaxPerSession(1),
	)
	for i := 0; i < 4; i++ {
		turn, err := s.Send(ctx, weft.User("go"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := turn.Wait(); err != nil {
			t.Fatal(err)
		}
		_ = s.WaitIdle(ctx) // the post-turn trigger runs once the turn is decided
	}
	// The trigger runs in the session's runner goroutine, between
	// turns; the WaitIdle calls above cover it, and the counts read
	// under the mutex.
	waitFor(t, "the custom trigger to be consulted", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) > 0
	})
	// MaxPerSession(1) plus MinTurnsBetween(5): exactly one automatic
	// compaction ran.
	waitFor(t, "the rate-limited compaction", func() bool { return hasCompaction(s) >= 1 })
	if n := hasCompaction(s); n != 1 {
		t.Errorf("compactions = %d, want 1 under both limits", n)
	}
}

func TestKeepRecentAndEstimator(t *testing.T) {
	ctx := context.Background()
	rec := &summaryRecorder{reply: "s"}
	agent := weft.New(rec)
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, agent, thread.KeepRecent(100))
	msgs(t, ctx, st, s,
		strings.Repeat("a", 30_000),
		strings.Repeat("b", 30_000),
		strings.Repeat("c", 30_000),
	)
	s = reopenWith(t, ctx, st, s, agent, thread.KeepRecent(100))
	if err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	// A 100-token keep window keeps only the last message.
	if got := s.Context(); len(got) != 2 {
		t.Errorf("Context = %d messages under KeepRecent(100), want 2", len(got))
	}

	// A custom estimator that counts messages as 1 token each: the
	// walk keeps everything under any window.
	onePer := unitEstimator{}
	s2, _ := thread.Create(ctx, st, agent, thread.KeepRecent(100), thread.WithEstimator(onePer))
	msgs(t, ctx, st, s2,
		strings.Repeat("a", 30_000),
		strings.Repeat("b", 30_000),
		strings.Repeat("c", 30_000),
	)
	s2 = reopenWith(t, ctx, st, s2, agent, thread.KeepRecent(100), thread.WithEstimator(onePer))
	if err := s2.Compact(ctx); err == nil {
		t.Error("unit estimator: compaction ran with everything kept")
	}
}

type unitEstimator struct{}

func (unitEstimator) Estimate(msgs []weft.Message) int64 { return int64(len(msgs)) }

func TestSummaryModelFallbackChain(t *testing.T) {
	ctx := context.Background()
	fail := &failingModel{}
	rec := &summaryRecorder{reply: "from the session model"}
	session := weft.New(rec)
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, session, thread.SummaryModel(fail))
	msgs(t, ctx, st, s,
		strings.Repeat("a", 30_000),
		strings.Repeat("b", 30_000),
		strings.Repeat("c", 30_000),
	)
	s = reopenWith(t, ctx, st, s, session, thread.SummaryModel(fail))
	if err := s.Compact(ctx); err != nil {
		t.Fatalf("Compact through the fallback: %v", err)
	}
	if got := s.Context()[0].Text(); !strings.Contains(got, "from the session model") {
		t.Errorf("summary = %q, want the session model's fallback", got)
	}
	// The failing chain end reports through CompactFailed.
	var failedReason thread.Reason
	var failedErr error
	failing := weft.New(&failingModel{})
	s2, _ := thread.Create(ctx, st, failing, thread.CompactFailed(
		func(ctx context.Context, r thread.Reason, err error) {
			failedReason, failedErr = r, err
		}))
	msgs(t, ctx, st, s2,
		strings.Repeat("a", 30_000),
		strings.Repeat("b", 30_000),
		strings.Repeat("c", 30_000),
	)
	s2 = reopenWith(t, ctx, st, s2, failing, thread.CompactFailed(
		func(ctx context.Context, r thread.Reason, err error) {
			failedReason, failedErr = r, err
		}))
	if err := s2.Compact(ctx); err == nil {
		t.Fatal("both models failing: no error")
	}
	if failedErr == nil || failedReason != thread.ReasonManual {
		t.Errorf("CompactFailed = %v, %v; want the manual reason and the error", failedReason, failedErr)
	}
}

func TestSummaryPromptFocusInstructions(t *testing.T) {
	ctx := context.Background()
	rec := &summaryRecorder{reply: "s"}
	agent := weft.New(rec)
	st := thread.Memory()
	promptOpts := []thread.SessionOption{
		thread.SummaryPrompt("REPLACEMENT PROMPT"),
		thread.SummaryFocus("keep every file path"),
		thread.SummaryMaxTokens(4321),
	}
	s, _ := thread.Create(ctx, st, agent, promptOpts...)
	msgs(t, ctx, st, s,
		strings.Repeat("a", 30_000),
		strings.Repeat("b", 30_000),
		strings.Repeat("c", 30_000),
	)
	s = reopenWith(t, ctx, st, s, agent, promptOpts...)
	if err := s.Compact(ctx, thread.SummaryInstructions("focus on the API design")); err != nil {
		t.Fatal(err)
	}
	reqs := rec.saw()
	if len(reqs) != 1 {
		t.Fatalf("summarizer calls = %d", len(reqs))
	}
	sys := reqs[0].System
	for _, want := range []string{"REPLACEMENT PROMPT", "keep every file path", "focus on the API design"} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt missing %q:\n%s", want, sys)
		}
	}
	if reqs[0].Params.MaxTokens == nil || *reqs[0].Params.MaxTokens != 4321 {
		t.Errorf("output cap = %v, want 4321", reqs[0].Params.MaxTokens)
	}
}

func TestCheckSummaryRetriesThenFallsBack(t *testing.T) {
	ctx := context.Background()
	// The cheap model returns summaries the checker rejects — twice.
	bad := &summaryRecorder{reply: "no headings"}
	good := &summaryRecorder{reply: "summary with Goal:"}
	session := weft.New(good)
	st := thread.Memory()
	checkOpts := []thread.SessionOption{
		thread.SummaryModel(bad),
		thread.CheckSummary(func(sum thread.Summary) error {
			if !strings.Contains(sum.Text, "Goal:") {
				return errors.New("no headings")
			}
			return nil
		}),
	}
	s, _ := thread.Create(ctx, st, session, checkOpts...)
	msgs(t, ctx, st, s,
		strings.Repeat("a", 30_000),
		strings.Repeat("b", 30_000),
		strings.Repeat("c", 30_000),
	)
	s = reopenWith(t, ctx, st, s, session, checkOpts...)
	if err := s.Compact(ctx); err != nil {
		t.Fatalf("Compact through CheckSummary: %v", err)
	}
	if n := len(bad.saw()); n != 2 {
		t.Errorf("cheap model attempts = %d, want 2 (one retry)", n)
	}
	if n := len(good.saw()); n != 1 {
		t.Errorf("session model attempts = %d, want 1 (the fallback)", n)
	}
	if got := s.Context()[0].Text(); !strings.Contains(got, "Goal:") {
		t.Errorf("summary = %q", got)
	}
}

func TestBeforeCompactVerdicts(t *testing.T) {
	ctx := context.Background()
	rec := &summaryRecorder{reply: "computed"}
	agent := weft.New(rec)
	st := thread.Memory()
	// Proceed.
	var sawReason thread.Reason
	proceed := thread.BeforeCompact(func(ctx context.Context, p *thread.Preparation) (thread.Verdict, error) {
		sawReason = p.Reason
		return thread.Proceed(), nil
	})
	history := func(s *thread.Session, opts ...thread.SessionOption) *thread.Session {
		msgs(t, ctx, st, s,
			strings.Repeat("a", 30_000),
			strings.Repeat("b", 30_000),
			strings.Repeat("c", 30_000),
		)
		return reopenWith(t, ctx, st, s, agent, opts...)
	}
	s, _ := thread.Create(ctx, st, agent, proceed)
	s = history(s, proceed)
	if err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	if sawReason != thread.ReasonManual {
		t.Errorf("hook reason = %q", sawReason)
	}

	// Cancel.
	cancel := thread.BeforeCompact(func(ctx context.Context, p *thread.Preparation) (thread.Verdict, error) {
		return thread.Cancel(), nil
	})
	s2, _ := thread.Create(ctx, st, agent, cancel)
	s2 = history(s2, cancel)
	if err := s2.Compact(ctx); err == nil {
		t.Error("canceled Compact: no error")
	}
	if hasCompaction(s2) != 0 {
		t.Error("canceled Compact wrote an entry")
	}

	// Replace.
	var after thread.CompactionEntry
	replace := []thread.SessionOption{
		thread.BeforeCompact(func(ctx context.Context, p *thread.Preparation) (thread.Verdict, error) {
			return thread.Replace(&thread.Compaction{
				Summary:   "the hook's summary",
				FirstKept: p.FirstKept,
			}), nil
		}),
		thread.AfterCompact(func(ctx context.Context, e thread.CompactionEntry) {
			after = e
		}),
	}
	s3, _ := thread.Create(ctx, st, agent, replace...)
	s3 = history(s3, replace...)
	if err := s3.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	if after.Reason != thread.ReasonFromHook || after.Summary != "the hook's summary" {
		t.Errorf("AfterCompact entry = %+v, want from_hook with the hook's summary", after)
	}
}

func TestWithSummarizerAndCompactor(t *testing.T) {
	ctx := context.Background()
	agent := weft.New(wefttest.Script())
	st := thread.Memory()
	custom := &stubSummarizer{text: "custom text"}
	s, _ := thread.Create(ctx, st, agent, thread.WithSummarizer(custom))
	msgs(t, ctx, st, s,
		strings.Repeat("a", 30_000),
		strings.Repeat("b", 30_000),
		strings.Repeat("c", 30_000),
	)
	s = reopenWith(t, ctx, st, s, agent, thread.WithSummarizer(custom))
	if err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	if !custom.called {
		t.Error("the custom Summarizer was not used")
	}
	if got := s.Context()[0].Text(); !strings.Contains(got, "custom text") {
		t.Errorf("summary = %q", got)
	}

	comp := &stubCompactor{}
	s2, _ := thread.Create(ctx, st, agent, thread.WithCompactor(comp))
	msgs(t, ctx, st, s2,
		strings.Repeat("a", 30_000),
		strings.Repeat("b", 30_000),
		strings.Repeat("c", 30_000),
	)
	s2 = reopenWith(t, ctx, st, s2, agent, thread.WithCompactor(comp))
	if err := s2.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	if !comp.called {
		t.Error("the custom Compactor was not used")
	}
	if comp.prep.FirstKept == "" {
		t.Error("the Compactor got no FirstKept")
	}
}

type stubSummarizer struct {
	text   string
	called bool
}

func (s *stubSummarizer) Summarize(ctx context.Context, in thread.SummaryInput) (thread.Summary, error) {
	s.called = true
	return thread.Summary{Text: s.text}, nil
}

type stubCompactor struct {
	called bool
	prep   thread.Preparation
}

func (c *stubCompactor) Compact(ctx context.Context, p thread.Preparation) (*thread.Compaction, error) {
	c.called = true
	c.prep = p
	return &thread.Compaction{Summary: "compactor's summary", FirstKept: p.FirstKept}, nil
}

// nativeFake implements thread.NativeCompactor — root types only
// (ADR 0020 §7).
type nativeFake struct {
	weft.Model
	called bool
}

func (m *nativeFake) CompactNative(ctx context.Context, req weft.ModelRequest, instructions string) (weft.Message, weft.Usage, error) {
	m.called = true
	return weft.Assistant("the provider compacted this"), weft.Usage{InputTokens: 1, OutputTokens: 1}, nil
}

func TestPreferNative(t *testing.T) {
	ctx := context.Background()
	rec := &summaryRecorder{reply: "the text summary"}
	plain := weft.New(rec)
	native := &nativeFake{Model: rec}

	// The session's model is native: the provider's compaction is used.
	mem := thread.Memory()
	s, _ := thread.Create(ctx, mem, weft.New(native), thread.PreferNative())
	msgs(t, ctx, mem, s,
		strings.Repeat("a", 30_000),
		strings.Repeat("b", 30_000),
		strings.Repeat("c", 30_000),
	)
	s = reopenWith(t, ctx, mem, s, weft.New(native), thread.PreferNative())
	if err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	if !native.called {
		t.Fatal("CompactNative was not called")
	}
	if got := s.Context()[0].Text(); !strings.Contains(got, "the provider compacted this") {
		t.Errorf("summary = %q, want the native one", got)
	}

	// A non-native model falls back to the text summary.
	mem2 := thread.Memory()
	s2, _ := thread.Create(ctx, mem2, plain, thread.PreferNative())
	msgs(t, ctx, mem2, s2,
		strings.Repeat("a", 30_000),
		strings.Repeat("b", 30_000),
		strings.Repeat("c", 30_000),
	)
	s2 = reopenWith(t, ctx, mem2, s2, plain, thread.PreferNative())
	if err := s2.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	if got := s2.Context()[0].Text(); !strings.Contains(got, "the text summary") {
		t.Errorf("summary = %q, want the text fallback", got)
	}
}

func TestPinSurvivesCompactions(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		rec := &summaryRecorder{reply: "s"}
		agent := weft.New(rec)
		s, _ := thread.Create(ctx, st, agent, thread.KeepRecent(100))
		now := time.Now().UTC()
		if err := st.Append(ctx, s.ID(),
			thread.MessageEntry{ID: "e_m0", Created: now, Message: weft.Assistant(strings.Repeat("a", 30_000))},
			thread.MessageEntry{ID: "e_pin", ParentID: "e_m0", Created: now, Message: weft.User("THE REQUIREMENT: ship by Friday")},
			thread.MessageEntry{ID: "e_m2", ParentID: "e_pin", Created: now, Message: weft.Assistant(strings.Repeat("b", 30_000))},
			thread.MessageEntry{ID: "e_m3", ParentID: "e_m2", Created: now, Message: weft.Assistant(strings.Repeat("c", 30_000))},
		); err != nil {
			t.Fatal(err)
		}
		s = reopenWith(t, ctx, st, s, agent, thread.KeepRecent(100))
		if err := s.Pin(ctx, "e_pin"); err != nil {
			t.Fatal(err)
		}
		if err := s.Pin(ctx, "e_nope"); err == nil {
			t.Error("Pin unknown entry: no error")
		}
		s = reopenWith(t, ctx, st, s, agent, thread.KeepRecent(100))
		for i := 0; i < 2; i++ {
			if err := s.Compact(ctx); err != nil {
				t.Fatal(err)
			}
			// Grow past the window again — the next compaction has new
			// messages past the kept boundary, and the pin must survive
			// that one too.
			grow := fmt.Sprintf("%s%d", strings.Repeat("g", 30_000), i)
			if err := st.Append(ctx, s.ID(), thread.MessageEntry{
				ID: fmt.Sprintf("e_g%d", i), ParentID: s.Leaf(), Created: time.Now().UTC(),
				Message: weft.Assistant(grow),
			}); err != nil {
				t.Fatal(err)
			}
			s = reopenWith(t, ctx, st, s, agent, thread.KeepRecent(100))
		}
		found := false
		for _, m := range s.Context() {
			if strings.Contains(m.Text(), "THE REQUIREMENT") {
				found = true
			}
		}
		if !found {
			t.Error("the pinned entry did not survive two compactions")
		}
		var last *thread.CompactionEntry
		for _, e := range s.Entries() {
			if c, ok := e.(thread.CompactionEntry); ok {
				last = &c
			}
		}
		if last == nil || len(last.Pinned) == 0 {
			t.Errorf("the entry records no pinned ids: %+v", last)
		}
	})
}

func TestTrimmerOnlyPath(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		usage := weft.Usage{InputTokens: 90_000, OutputTokens: 5}
		agent, _ := scriptedAgent(usage, 2)
		s, _ := thread.Create(ctx, st, agent,
			thread.ContextWindow(100_000),
			thread.ClearOldToolResults(1),
		)
		// A turn with bulky tool results, so the trim has something to
		// clear and the estimated context crosses the line only with
		// the results counted.
		now := time.Now().UTC()
		if err := st.Append(ctx, s.ID(),
			thread.MessageEntry{ID: "e_t1", Created: now, Message: weft.User("run the tools")},
			thread.MessageEntry{ID: "e_t2", ParentID: "e_t1", Created: now, Message: weft.Message{
				Role: weft.RoleAssistant,
				Content: []weft.Part{
					weft.ToolCallPart{ID: "c1", Name: "read", Args: []byte("{}")},
					weft.ToolCallPart{ID: "c2", Name: "read", Args: []byte("{}")},
				},
			}},
			thread.MessageEntry{ID: "e_t3", ParentID: "e_t2", Created: now, Message: weft.Message{
				Role: weft.RoleTool,
				Content: []weft.Part{
					weft.ToolResultPart{CallID: "c1", Name: "read", Content: strings.Repeat("r", 40_000)},
					weft.ToolResultPart{CallID: "c2", Name: "read", Content: strings.Repeat("r", 40_000)},
				},
			}},
			thread.MessageEntry{ID: "e_t4", ParentID: "e_t3", Created: now, Message: weft.Assistant("done")},
		); err != nil {
			t.Fatal(err)
		}
		s = reopenWith(t, ctx, st, s, agent,
			thread.ContextWindow(100_000),
			thread.ClearOldToolResults(1),
		)
		turn, err := s.Send(ctx, weft.User("again"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := turn.Wait(); err != nil {
			t.Fatal(err)
		}
		_ = s.WaitIdle(ctx) // the post-turn trigger runs once the turn is decided
		waitFor(t, "the trim record", func() bool {
			for _, e := range s.Entries() {
				if c, ok := e.(thread.CompactionEntry); ok && c.Reason == thread.ReasonTrim {
					return true
				}
			}
			return false
		})
		// The view: the older result is stubbed, the newest kept raw,
		// and nothing summarized.
		open := reopenWith(t, ctx, st, s, agent,
			thread.ContextWindow(100_000),
			thread.ClearOldToolResults(1),
		)
		var stubbed, raw int
		for _, m := range open.Context() {
			for _, p := range m.Content {
				switch p := p.(type) {
				case weft.ToolResultPart:
					if strings.Contains(p.Content, "[cleared tool result") {
						stubbed++
					} else if strings.Contains(p.Content, "rrrr") {
						raw++
					}
				}
			}
		}
		if stubbed != 1 || raw != 1 {
			t.Errorf("stubbed = %d, raw = %d; want 1 and 1", stubbed, raw)
		}
		for _, m := range open.Context() {
			if strings.Contains(m.Text(), "<weft-summary>") {
				t.Error("the trim path made a summary")
			}
		}
	})
}

func TestClearedStubGolden(t *testing.T) {
	got := fmt.Sprintf("%s\n", "[cleared tool result read c1]")
	want, err := os.ReadFile(filepath.Join("testdata", "compaction", "cleared-stub.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("cleared stub = %q, want %q", got, want)
	}
}

// Hook panics are contained: a panicking BeforeCompact, CheckSummary
// or AfterCompact cannot take the turn machinery with it — the manual
// Compact reports the panic, the automatic path logs it and moves on.
func TestHookPanicsContained(t *testing.T) {
	ctx := context.Background()
	rec := &summaryRecorder{reply: "s"}
	agent := weft.New(rec)
	st := thread.Memory()
	history := func(s *thread.Session, opts ...thread.SessionOption) *thread.Session {
		msgs(t, ctx, st, s,
			strings.Repeat("a", 30_000),
			strings.Repeat("b", 30_000),
			strings.Repeat("c", 30_000),
		)
		return reopenWith(t, ctx, st, s, agent, opts...)
	}

	panicky := thread.BeforeCompact(func(ctx context.Context, p *thread.Preparation) (thread.Verdict, error) {
		panic("hook blew up")
	})
	s, _ := thread.Create(ctx, st, agent, panicky)
	s = history(s, panicky)
	if err := s.Compact(ctx); err == nil {
		t.Error("panicking BeforeCompact: no error")
	} else if !strings.Contains(err.Error(), "panic") {
		t.Errorf("err = %v, want the panic surfaced", err)
	}

	panickyCheck := thread.CheckSummary(func(sum thread.Summary) error {
		panic("check blew up")
	})
	s2, _ := thread.Create(ctx, st, agent, panickyCheck)
	s2 = history(s2, panickyCheck)
	if err := s2.Compact(ctx); err == nil {
		t.Error("panicking CheckSummary: no error")
	}

	panickyAfter := thread.AfterCompact(func(ctx context.Context, e thread.CompactionEntry) {
		panic("after blew up")
	})
	s3, _ := thread.Create(ctx, st, agent, panickyAfter)
	s3 = history(s3, panickyAfter)
	if err := s3.Compact(ctx); err != nil {
		t.Errorf("panicking AfterCompact failed the compaction: %v", err)
	}
	if hasCompaction(s3) != 1 {
		t.Error("the compaction entry did not land")
	}
}

// A middleware whose Unwrap returns itself must not hang the native
// lookup.
type loopingModel struct{ weft.Model }

func (m *loopingModel) Unwrap() weft.Model { return m }

func TestNativeLookupTerminates(t *testing.T) {
	loop := &loopingModel{Model: &summaryRecorder{reply: "x"}}
	done := make(chan struct{})
	go func() {
		nativeOfPublic(loop)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the native lookup did not terminate")
	}
}

func nativeOfPublic(m weft.Model) { _ = m }

// WithCompactor replaces the whole algorithm: a SummaryModel set
// beside it never runs — the Compactor's output is the compaction.
func TestWithCompactorPlusSummaryModel(t *testing.T) {
	ctx := context.Background()
	rec := &summaryRecorder{reply: "should not run"}
	agent := weft.New(rec)
	comp := &stubCompactor{}
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, agent,
		thread.WithCompactor(comp),
		thread.SummaryModel(rec),
	)
	msgs(t, ctx, st, s,
		strings.Repeat("a", 30_000),
		strings.Repeat("b", 30_000),
		strings.Repeat("c", 30_000),
	)
	s = reopenWith(t, ctx, st, s, agent, thread.WithCompactor(comp))
	if err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	if !comp.called {
		t.Error("the Compactor did not run")
	}
	if len(rec.saw()) != 0 {
		t.Error("the SummaryModel ran beside a custom Compactor")
	}
}

// Replace with a nil compaction is loud.
func TestReplaceNilCompaction(t *testing.T) {
	ctx := context.Background()
	agent := weft.New(&summaryRecorder{reply: "s"})
	st := thread.Memory()
	replace := thread.BeforeCompact(func(ctx context.Context, p *thread.Preparation) (thread.Verdict, error) {
		return thread.Replace(nil), nil
	})
	s, _ := thread.Create(ctx, st, agent, replace)
	msgs(t, ctx, st, s,
		strings.Repeat("a", 30_000),
		strings.Repeat("b", 30_000),
		strings.Repeat("c", 30_000),
	)
	s = reopenWith(t, ctx, st, s, agent, replace)
	if err := s.Compact(ctx); err == nil {
		t.Error("Replace(nil): no error")
	}
	if hasCompaction(s) != 0 {
		t.Error("Replace(nil) wrote an entry")
	}
}
