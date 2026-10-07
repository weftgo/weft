package thread_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
)

func thresholdCompactions(s *thread.Session) int {
	n := 0
	for _, e := range s.Entries() {
		if c, ok := e.(thread.CompactionEntry); ok && c.Reason == thread.ReasonThreshold {
			n++
		}
	}
	return n
}

// usageAgent is a scripted agent whose every turn reports input
// tokens of in, with a separate recorder to summarize — so the script
// is never consumed by a summary.
func usageAgent(in int64, turns int) (*core.Agent, *summaryRecorder) {
	agent, _ := scriptedAgent(core.Usage{InputTokens: in, OutputTokens: 5}, turns)
	return agent, &summaryRecorder{reply: "the summary"}
}

// Reserve moves the trigger line: 60k of a 100k window does not cross
// the default 16,384 reserve, and does cross a 50,000 one. It also
// sizes the summarizer's default output cap (0.8 × Reserve).
func TestReserveMovesTheTriggerLine(t *testing.T) {
	agent, rec := usageAgent(60_000, 2)
	s := compactable(t, thread.Memory(), agent, thread.ContextWindow(100_000), thread.SummaryModel(rec))
	sendAndWait(t, s, "go")
	if n := thresholdCompactions(s); n != 0 {
		t.Fatalf("default reserve: threshold compactions = %d, want 0 (60k is under 100k − 16,384)", n)
	}

	agent, rec = usageAgent(60_000, 2)
	s = compactable(t, thread.Memory(), agent, thread.ContextWindow(100_000), thread.Reserve(50_000), thread.SummaryModel(rec))
	sendAndWait(t, s, "go")
	if n := thresholdCompactions(s); n != 1 {
		t.Fatalf("Reserve(50_000): threshold compactions = %d, want 1 (60k crosses 100k − 50k)", n)
	}
	reqs := rec.saw()
	if len(reqs) != 1 || reqs[0].Params.MaxTokens == nil || *reqs[0].Params.MaxTokens != 40_000 {
		t.Errorf("summarizer output cap = %v, want 0.8 × Reserve = 40000", reqs[0].Params.MaxTokens)
	}
}

// ModelReserves overrides Reserve for the session agent's model, and
// only for it.
func TestModelReservesOverride(t *testing.T) {
	agent, rec := usageAgent(60_000, 2)
	info := core.InfoOf(agent.Model())
	s := compactable(t, thread.Memory(), agent, thread.ContextWindow(100_000),
		thread.ModelReserves(map[core.ModelInfo]int64{info: 50_000}), thread.SummaryModel(rec))
	sendAndWait(t, s, "go")
	if n := thresholdCompactions(s); n != 1 {
		t.Errorf("this model's reserve: threshold compactions = %d, want 1", n)
	}

	agent, rec = usageAgent(60_000, 2)
	other := core.ModelInfo{Provider: "other", Name: "nope"}
	s = compactable(t, thread.Memory(), agent, thread.ContextWindow(100_000),
		thread.ModelReserves(map[core.ModelInfo]int64{other: 50_000}), thread.SummaryModel(rec))
	sendAndWait(t, s, "go")
	if n := thresholdCompactions(s); n != 0 {
		t.Errorf("another model's reserve: threshold compactions = %d, want 0", n)
	}
}

// TriggerFunc replaces the condition both ways — it can hold a
// compaction the default would run and run one the default would not —
// is told the numbers in force, and a panic in it reads as "do not
// fire".
func TestTriggerFuncReplacesTheCondition(t *testing.T) {
	t.Run("holds what the default would fire", func(t *testing.T) {
		agent, rec := usageAgent(95_000, 2)
		var mu sync.Mutex
		var seen []thread.TriggerInput
		s := compactable(t, thread.Memory(), agent, thread.ContextWindow(100_000), thread.Reserve(10_000), thread.SummaryModel(rec),
			thread.TriggerFunc(func(in thread.TriggerInput) bool {
				mu.Lock()
				defer mu.Unlock()
				seen = append(seen, in)
				return false
			}))
		sendAndWait(t, s, "go")
		if n := thresholdCompactions(s); n != 0 {
			t.Errorf("threshold compactions = %d under a trigger that says no, want 0", n)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(seen) != 1 {
			t.Fatalf("TriggerFunc consulted %d times over one measured turn, want 1 (the pre-turn site has no report yet)", len(seen))
		}
		if in := seen[0]; in.Window != 100_000 || in.Reserve != 10_000 || in.LastInput < 95_000 || in.Estimated != 0 {
			t.Errorf("TriggerInput = %+v, want the window, the reserve, the reported 95k and no delta", in)
		}
	})
	t.Run("fires what the default would hold", func(t *testing.T) {
		agent, rec := usageAgent(1_000, 2)
		s := compactable(t, thread.Memory(), agent, thread.ContextWindow(100_000), thread.SummaryModel(rec),
			thread.TriggerFunc(func(in thread.TriggerInput) bool { return true }))
		sendAndWait(t, s, "go")
		if n := thresholdCompactions(s); n != 1 {
			t.Errorf("threshold compactions = %d under a trigger that says yes, want 1", n)
		}
	})
	t.Run("a panic does not fire", func(t *testing.T) {
		agent, rec := usageAgent(95_000, 2)
		s := compactable(t, thread.Memory(), agent, thread.ContextWindow(100_000), thread.SummaryModel(rec),
			thread.TriggerFunc(func(in thread.TriggerInput) bool { panic("trigger blew up") }))
		sendAndWait(t, s, "go")
		if n := thresholdCompactions(s); n != 0 {
			t.Errorf("threshold compactions = %d after a panicking trigger, want 0", n)
		}
	})
}

// The rate limits count along the leaf's path: a compaction on an
// abandoned branch neither uses up this line's MaxPerSession nor
// starts its MinTurnsBetween clock. The bug: both counted every
// compaction and turn in the file, in file order.
func TestRateLimitsCountAlongThePath(t *testing.T) {
	ctx := context.Background()
	for name, limit := range map[string]thread.SessionOption{
		"MaxPerSession":   thread.MaxPerSession(1),
		"MinTurnsBetween": thread.MinTurnsBetween(3),
	} {
		t.Run(name, func(t *testing.T) {
			agent, rec := usageAgent(95_000, 8)
			s := compactable(t, thread.Memory(), agent,
				thread.ContextWindow(100_000), thread.KeepRecent(100), thread.SummaryModel(rec), limit)
			first := ""
			for _, e := range s.Entries() {
				if m, ok := e.(thread.MessageEntry); ok {
					first = m.ID
					break
				}
			}
			big := strings.Repeat("p", 8_000) // each prompt alone outweighs KeepRecent
			sendAndWait(t, s, big)
			if n := thresholdCompactions(s); n != 1 {
				t.Fatalf("line A: threshold compactions = %d, want 1", n)
			}
			// Leave line A — its compaction and its turn stay in the
			// file, off the path.
			if err := s.Branch(ctx, first); err != nil {
				t.Fatal(err)
			}
			sendAndWait(t, s, big)
			if n := thresholdCompactions(s); n != 2 {
				t.Fatalf("line B: threshold compactions = %d, want 2 — line A's compaction rate-limited a path it is not on", n)
			}
			// And on line B itself the limit holds.
			sendAndWait(t, s, big)
			sendAndWait(t, s, big)
			if n := thresholdCompactions(s); n != 2 {
				t.Errorf("line B within its own limit: threshold compactions = %d, want 2", n)
			}
		})
	}
	t.Run("MinTurnsBetween re-arms after n turns on the path", func(t *testing.T) {
		agent, rec := usageAgent(95_000, 8)
		s := compactable(t, thread.Memory(), agent,
			thread.ContextWindow(100_000), thread.KeepRecent(100), thread.SummaryModel(rec), thread.MinTurnsBetween(2))
		big := strings.Repeat("p", 8_000)
		sendAndWait(t, s, big) // compacts after the turn
		sendAndWait(t, s, big) // 1 turn since: held
		if n := thresholdCompactions(s); n != 1 {
			t.Fatalf("threshold compactions = %d one turn after the last, want 1", n)
		}
		sendAndWait(t, s, big) // 2 turns since: allowed
		if n := thresholdCompactions(s); n != 2 {
			t.Errorf("threshold compactions = %d two turns after the last, want 2", n)
		}
	})
}

// failingNative offers the provider-native seam and fails it.
type failingNative struct {
	core.Model
	calls int
}

func (m *failingNative) CompactNative(ctx context.Context, req core.ModelRequest, instructions string) (core.Message, core.Usage, error) {
	m.calls++
	return core.Message{}, core.Usage{}, errors.New("compact endpoint down")
}

// wrapping is a middleware-shaped model: the native lookup follows its
// Unwrap.
type wrapping struct{ inner core.Model }

func (w wrapping) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return w.inner.Stream(ctx, req)
}
func (w wrapping) Unwrap() core.Model { return w.inner }

// PreferNative: the seam is found through middleware, only the text is
// kept, and a native failure is logged — never swallowed — before the
// text summary takes over.
func TestPreferNativeFallbackIsLogged(t *testing.T) {
	ctx := context.Background()
	rec := &summaryRecorder{reply: "the text summary"}

	t.Run("found through Unwrap, text only", func(t *testing.T) {
		native := &nativeFake{Model: rec}
		s := compactable(t, thread.Memory(), core.New(wrapping{native}), thread.PreferNative())
		if err := s.Compact(ctx); err != nil {
			t.Fatal(err)
		}
		if !native.called {
			t.Fatal("CompactNative behind a middleware was not found")
		}
		if got := s.Context()[0].Text(); got != "<weft-summary>\nthe provider compacted this\n</weft-summary>" {
			t.Errorf("summary message = %q, want the native text behind the ordinary marker", got)
		}
	})

	t.Run("an error falls back, out loud", func(t *testing.T) {
		var buf syncBuffer
		native := &failingNative{Model: rec}
		agent := core.New(native, core.Logger(slog.New(slog.NewTextHandler(&buf, nil))))
		s := compactable(t, thread.Memory(), agent, thread.PreferNative())
		if err := s.Compact(ctx); err != nil {
			t.Fatalf("Compact through the native fallback: %v", err)
		}
		if native.calls != 1 {
			t.Errorf("CompactNative calls = %d, want 1", native.calls)
		}
		if got := s.Context()[0].Text(); !strings.Contains(got, "the text summary") {
			t.Errorf("summary = %q, want the text fallback", got)
		}
		if log := buf.String(); !strings.Contains(log, "provider-native compaction failed") || !strings.Contains(log, "compact endpoint down") {
			t.Errorf("the native error was swallowed; log = %q", log)
		}
	})
}

// TokensBefore and Preparation.Context describe what the model was
// shown — the compacted view — not the raw path. The bug: after a
// first compaction both still counted everything the summary had
// replaced.
func TestTokensBeforeCountsTheCompactedView(t *testing.T) {
	ctx := context.Background()
	rec := &summaryRecorder{reply: "the summary"}
	agent := core.New(rec)
	st := thread.Memory()
	var prep thread.Preparation
	hook := thread.BeforeCompact(func(ctx context.Context, p *thread.Preparation) (thread.Verdict, error) {
		prep = *p
		return thread.Proceed(), nil
	})
	s := compactable(t, st, agent, hook)
	if err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	msgs(t, ctx, st, s, strings.Repeat("d", 30_000), strings.Repeat("e", 30_000), strings.Repeat("f", 30_000))
	s = reopenWith(t, ctx, st, s, agent, hook)

	shown := s.Context()
	var want int64
	for _, m := range shown {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		want += (int64(len(b)) + 3) / 4
	}
	var raw int64
	for _, e := range s.Entries() {
		if m, ok := e.(thread.MessageEntry); ok {
			b, _ := json.Marshal(m.Message)
			raw += (int64(len(b)) + 3) / 4
		}
	}
	if err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	var last thread.CompactionEntry
	for _, e := range s.Entries() {
		if c, ok := e.(thread.CompactionEntry); ok {
			last = c
		}
	}
	if last.TokensBefore != want {
		t.Errorf("TokensBefore = %d, want %d (the context the model was shown; the raw path weighs %d)", last.TokensBefore, want, raw)
	}
	if prep.TokensBefore != want {
		t.Errorf("Preparation.TokensBefore = %d, want %d", prep.TokensBefore, want)
	}
	if fmt.Sprint(prep.Context) != fmt.Sprint(shown) {
		t.Errorf("Preparation.Context = %d messages, want the %d the model was shown", len(prep.Context), len(shown))
	}
	if !strings.HasPrefix(prep.Context[0].Text(), "<weft-summary>") {
		t.Error("Preparation.Context does not lead with the previous summary")
	}
}

// Two summary compactions cannot name the same boundary: the second
// would summarize nothing the first did not, and is refused wrapping
// ErrNothingToCompact. The bug: only a boundary BEFORE the previous
// one was rejected, so a replayed plan landed twice.
func TestApplyCompactionRejectsTheSameBoundary(t *testing.T) {
	ctx := context.Background()
	s := compactable(t, thread.Memory(), core.New(&summaryRecorder{reply: "the summary"}))
	plan, err := s.PreviewCompaction(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyCompaction(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyCompaction(ctx, plan); !errors.Is(err, thread.ErrNothingToCompact) {
		t.Errorf("the same plan applied twice: err = %v, want ErrNothingToCompact", err)
	}
	if err := s.ApplyCompaction(ctx, &thread.Compaction{Summary: "another", FirstKept: plan.FirstKept}); !errors.Is(err, thread.ErrNothingToCompact) {
		t.Errorf("another summary at the same boundary: err = %v, want ErrNothingToCompact", err)
	}
	if n := hasCompaction(s); n != 1 {
		t.Errorf("compactions = %d, want 1", n)
	}
}

// The compaction errors are sentinels a caller can branch on.
func TestCompactionErrorsAreSentinels(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	agent := core.New(&summaryRecorder{reply: "the summary"})
	s, _ := thread.Create(ctx, st, agent)
	now := timeUTC()
	if err := st.Append(ctx, s.ID(),
		thread.MessageEntry{ID: "e_1", Created: now, Message: core.User("one")},
		thread.MessageEntry{ID: "e_2", ParentID: "e_1", Created: now, Message: core.Message{Role: core.RoleTool,
			Content: []core.Part{core.ToolResultPart{CallID: "c1", Name: "read", Content: "result"}}}},
		thread.MessageEntry{ID: "e_3", ParentID: "e_2", Created: now, Message: core.User("three")},
		thread.MessageEntry{ID: "e_side", ParentID: "e_1", Created: now, Message: core.User("another branch")},
		thread.LeafEntry{ID: "e_nav", ParentID: "e_side", Created: now, Entry: "e_3"},
	); err != nil {
		t.Fatal(err)
	}
	s = reopenWith(t, ctx, st, s, agent)
	stub := func(entry, call string) *thread.TrimRecord {
		return &thread.TrimRecord{Stubs: []thread.TrimStub{{Entry: entry, CallID: call, Content: "x"}}}
	}
	for name, tc := range map[string]struct {
		c    *thread.Compaction
		want error
	}{
		"no Compaction":               {nil, thread.ErrInvalidCompaction},
		"FirstKept not held":          {&thread.Compaction{Summary: "s", FirstKept: "e_missing"}, thread.ErrNoEntry},
		"FirstKept on another branch": {&thread.Compaction{Summary: "s", FirstKept: "e_side"}, thread.ErrInvalidCompaction},
		"no summary, not a trim":      {&thread.Compaction{FirstKept: "e_3"}, thread.ErrInvalidCompaction},
		"a trim without its record":   {&thread.Compaction{FirstKept: "e_1", Reason: thread.ReasonTrim}, thread.ErrInvalidCompaction},
		"a trim with an empty record": {&thread.Compaction{FirstKept: "e_1", Reason: thread.ReasonTrim, Trim: &thread.TrimRecord{}}, thread.ErrInvalidCompaction},
		"a trim naming no result":     {&thread.Compaction{FirstKept: "e_1", Reason: thread.ReasonTrim, Trim: stub("e_2", "c9")}, thread.ErrInvalidCompaction},
		"a trim naming no entry":      {&thread.Compaction{FirstKept: "e_1", Reason: thread.ReasonTrim, Trim: stub("e_side", "c1")}, thread.ErrInvalidCompaction},
		"a trim with a summary":       {&thread.Compaction{Summary: "s", FirstKept: "e_1", Reason: thread.ReasonTrim, Trim: stub("e_2", "c1")}, thread.ErrInvalidCompaction},
		"a summary with a record":     {&thread.Compaction{Summary: "s", FirstKept: "e_3", Trim: stub("e_2", "c1")}, thread.ErrInvalidCompaction},
	} {
		if err := s.ApplyCompaction(ctx, tc.c); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
	if n := hasCompaction(s); n != 0 {
		t.Fatalf("an invalid compaction landed: %d entries", n)
	}
	if err := s.Uncompact(ctx); !errors.Is(err, thread.ErrNoEntry) {
		t.Errorf("Uncompact with nothing to undo: err = %v, want ErrNoEntry", err)
	}
	if err := s.Compact(ctx); !errors.Is(err, thread.ErrNothingToCompact) {
		t.Errorf("Compact on a small session: err = %v, want ErrNothingToCompact", err)
	}
	// A valid trim lands, and an empty Reason is recorded as manual.
	if err := s.ApplyCompaction(ctx, &thread.Compaction{FirstKept: "e_1", Reason: thread.ReasonTrim, Trim: stub("e_2", "c1")}); err != nil {
		t.Errorf("a valid trim: %v", err)
	}
	if err := s.ApplyCompaction(ctx, &thread.Compaction{Summary: "s", FirstKept: "e_3"}); err != nil {
		t.Fatalf("a valid summary compaction: %v", err)
	}
	entries := s.Entries()
	if c := entries[len(entries)-1].(thread.CompactionEntry); c.Reason != thread.ReasonManual {
		t.Errorf("an empty Reason was recorded as %q, want manual", c.Reason)
	}
}

// Pin takes only entries that put a message in the context: a
// bookkeeping entry is refused with ErrNotPinnable (the walk would
// have dropped it silently), an unknown id with ErrNoEntry.
func TestPinRejectsBookkeepingEntries(t *testing.T) {
	ctx := context.Background()
	agent := core.New(wefttest.Script(wefttest.Say("reply")))
	s, _ := thread.Create(ctx, thread.Memory(), agent)
	sendAndWait(t, s, "hello")
	if err := s.Custom(ctx, "app/state", json.RawMessage(`{"k":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.CustomMessage(ctx, "app/note", core.User("a note")); err != nil {
		t.Fatal(err)
	}
	before := len(s.Entries())
	pinned := 0
	for _, e := range s.Entries() {
		switch e := e.(type) {
		case thread.TurnEntry:
			if err := s.Pin(ctx, e.ID); !errors.Is(err, thread.ErrNotPinnable) {
				t.Errorf("Pin(turn entry): err = %v, want ErrNotPinnable", err)
			}
		case thread.CustomEntry:
			if err := s.Pin(ctx, e.ID); !errors.Is(err, thread.ErrNotPinnable) {
				t.Errorf("Pin(custom entry): err = %v, want ErrNotPinnable", err)
			}
		case thread.MessageEntry:
			if err := s.Pin(ctx, e.ID); err != nil {
				t.Errorf("Pin(message entry): %v", err)
			}
			pinned++
		case thread.CustomMessageEntry:
			if err := s.Pin(ctx, e.ID); err != nil {
				t.Errorf("Pin(custom_message entry): %v", err)
			}
			pinned++
		}
	}
	if err := s.Pin(ctx, "e_missing"); !errors.Is(err, thread.ErrNoEntry) {
		t.Errorf("Pin(unknown): err = %v, want ErrNoEntry", err)
	}
	if got := len(s.Entries()); got != before+pinned {
		t.Errorf("Entries = %d, want %d: a refused pin wrote a record", got, before+pinned)
	}
}

// A pin made on an entry already below the boundary is read when the
// next compaction is computed: it takes effect then, not at once — the
// documented rule.
func TestPinBelowTheBoundaryTakesEffectAtTheNextCompaction(t *testing.T) {
	ctx := context.Background()
	rec := &summaryRecorder{reply: "the summary"}
	agent := core.New(rec)
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, agent)
	now := timeUTC()
	if err := st.Append(ctx, s.ID(),
		thread.MessageEntry{ID: "e_req", Created: now, Message: core.User("THE REQUIREMENT " + strings.Repeat("a", 30_000))},
		thread.MessageEntry{ID: "e_b", ParentID: "e_req", Created: now, Message: core.User(strings.Repeat("b", 30_000))},
		thread.MessageEntry{ID: "e_c", ParentID: "e_b", Created: now, Message: core.User(strings.Repeat("c", 30_000))},
	); err != nil {
		t.Fatal(err)
	}
	s = reopenWith(t, ctx, st, s, agent)
	if err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Pin(ctx, "e_req"); err != nil {
		t.Fatal(err)
	}
	shows := func() bool {
		for _, m := range s.Context() {
			if strings.Contains(m.Text(), "THE REQUIREMENT") {
				return true
			}
		}
		return false
	}
	if shows() {
		t.Error("a pin below the boundary changed the context before any compaction ran")
	}
	msgs(t, ctx, st, s, strings.Repeat("d", 30_000), strings.Repeat("e", 30_000), strings.Repeat("f", 30_000))
	s = reopenWith(t, ctx, st, s, agent)
	if err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	if !shows() {
		t.Error("the pinned entry did not return to the context at the next compaction")
	}
	if got := s.Context()[1].Text(); !strings.HasPrefix(got, "THE REQUIREMENT") {
		t.Errorf("the pinned entry is not placed right after the summary: second message = %q", got[:min(40, len(got))])
	}
}

// A window too small for its reserve, or a KeepRecent that alone
// crosses the trigger line, could never compact under the line: Create
// and Open refuse the configuration with ErrCompactConfig instead of
// running a session that silently never compacts.
func TestCompactConfigIsValidated(t *testing.T) {
	ctx := context.Background()
	agent := core.New(wefttest.Script())
	info := core.InfoOf(agent.Model())
	other := core.ModelInfo{Provider: "other", Name: "nope"}
	for name, tc := range map[string]struct {
		opts []thread.SessionOption
		ok   bool
	}{
		"window under the default reserve": {[]thread.SessionOption{thread.ContextWindow(8_000)}, false},
		"reserve at the window":            {[]thread.SessionOption{thread.ContextWindow(100_000), thread.Reserve(100_000)}, false},
		"keep-recent at the trigger line":  {[]thread.SessionOption{thread.ContextWindow(100_000), thread.KeepRecent(83_616)}, false},
		"per-model window too small":       {[]thread.SessionOption{thread.ContextWindow(100_000), thread.ModelWindows(map[core.ModelInfo]int64{info: 16_000})}, false},
		"per-model reserve too large":      {[]thread.SessionOption{thread.ContextWindow(100_000), thread.ModelReserves(map[core.ModelInfo]int64{info: 90_000})}, false},
		"a small window, sized knobs":      {[]thread.SessionOption{thread.ContextWindow(8_000), thread.Reserve(1_000), thread.KeepRecent(2_000)}, true},
		"another model's small window":     {[]thread.SessionOption{thread.ModelWindows(map[core.ModelInfo]int64{other: 10})}, true},
		"no window: nothing to validate":   {[]thread.SessionOption{thread.Reserve(1 << 40), thread.KeepRecent(1 << 40)}, true},
	} {
		t.Run(name, func(t *testing.T) {
			st := thread.Memory()
			s, err := thread.Create(ctx, st, agent, tc.opts...)
			if tc.ok {
				if err != nil {
					t.Fatalf("Create: %v", err)
				}
				if _, err := thread.Open(ctx, st, s.ID(), agent, tc.opts...); err != nil {
					t.Errorf("Open: %v", err)
				}
				return
			}
			if !errors.Is(err, thread.ErrCompactConfig) {
				t.Fatalf("Create: err = %v, want ErrCompactConfig", err)
			}
			if page, _ := thread.List(ctx, st, thread.Query{}); page.Total != 0 {
				t.Errorf("the refused Create left %d session(s) in the storage", page.Total)
			}
			good, err := thread.Create(ctx, st, agent)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := thread.Open(ctx, st, good.ID(), agent, tc.opts...); !errors.Is(err, thread.ErrCompactConfig) {
				t.Errorf("Open: err = %v, want ErrCompactConfig", err)
			}
		})
	}
}

// A trigger that fires over a context compaction cannot shrink — the
// tail fits inside KeepRecent — says so once, not never and not every
// turn.
func TestTriggerFiringOverNothingWarnsOnce(t *testing.T) {
	ctx := context.Background()
	var buf syncBuffer
	turns := make([]wefttest.Turn, 3)
	for i := range turns {
		turns[i] = wefttest.Say("reply").WithUsage(core.Usage{InputTokens: 95_000, OutputTokens: 5})
	}
	agent := core.New(wefttest.Script(turns...), core.Logger(slog.New(slog.NewTextHandler(&buf, nil))))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.ContextWindow(100_000))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		sendAndWait(t, s, "small")
	}
	if n := hasCompaction(s); n != 0 {
		t.Fatalf("compactions = %d over a tail that fits KeepRecent, want 0", n)
	}
	if n := strings.Count(buf.String(), "there is nothing to compact"); n != 1 {
		t.Errorf("nothing-to-compact warnings = %d over three firing turns, want 1:\n%s", n, buf.String())
	}
}
