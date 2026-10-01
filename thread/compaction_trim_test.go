package thread_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// toolHistory appends a turn with two bulky tool results (c1, c2 —
// ~10k estimated tokens each) under fixed ids: e_t1 the prompt, e_t2
// the calls, e_t3 the results, e_t4 the answer.
func toolHistory(t *testing.T, st thread.Storage, s *thread.Session) {
	t.Helper()
	now := timeUTC()
	if err := st.Append(context.Background(), s.ID(),
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
}

// resultContents lists the tool-result contents the context shows, in
// order, long ones abbreviated to "raw".
func resultContents(s *thread.Session) []string {
	var out []string
	for _, m := range s.Context() {
		for _, p := range m.Content {
			if r, ok := p.(weft.ToolResultPart); ok {
				if len(r.Content) > 200 {
					out = append(out, "raw")
				} else {
					out = append(out, r.Content)
				}
			}
		}
	}
	return out
}

func trims(s *thread.Session) []thread.CompactionEntry {
	var out []thread.CompactionEntry
	for _, e := range s.Entries() {
		if c, ok := e.(thread.CompactionEntry); ok && c.Reason == thread.ReasonTrim {
			out = append(out, c)
		}
	}
	return out
}

func sendAndWait(t *testing.T, s *thread.Session, text string) {
	t.Helper()
	turn, err := s.Send(context.Background(), weft.User(text))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
}

// A custom Trimmer changes what the model sees, durably: its output is
// diffed against its input into the entry's trim record, and every
// later context build replays that record — with the Trimmer, without
// it, or under a different one. The bug: the trimmer's output was only
// used to estimate; the walk re-derived stubs for the built-in trimmer
// alone, so a custom trim wrote a record that changed nothing and the
// trigger kept firing.
func TestCustomTrimmerChangesTheContext(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent, _ := scriptedAgent(bigUsage(), 4)
		gone := trimmerFunc(func(ctx context.Context, msgs []weft.Message) ([]weft.Message, error) {
			for i, m := range msgs {
				for j, p := range m.Content {
					if r, ok := p.(weft.ToolResultPart); ok && r.CallID == "c1" {
						r.Content = "[gone: " + r.Name + "]"
						msgs[i].Content[j] = r // in place, on the trimmer's own copy
					}
				}
			}
			return msgs, nil
		})
		var after []thread.CompactionEntry
		var mu sync.Mutex
		opts := []thread.SessionOption{
			thread.ContextWindow(100_000),
			thread.WithTrimmer(gone),
			thread.AfterCompact(func(ctx context.Context, e thread.CompactionEntry) {
				mu.Lock()
				defer mu.Unlock()
				after = append(after, e)
			}),
		}
		s, err := thread.Create(ctx, st, agent, opts...)
		if err != nil {
			t.Fatal(err)
		}
		toolHistory(t, st, s)
		s = reopenWith(t, ctx, st, s, agent, opts...)
		sendAndWait(t, s, "again")

		got := trims(s)
		if len(got) != 1 {
			t.Fatalf("trim entries = %d, want 1", len(got))
		}
		want := []thread.TrimStub{{Entry: "e_t3", CallID: "c1", Content: "[gone: read]"}}
		if got[0].Trim == nil || fmt.Sprint(got[0].Trim.Stubs) != fmt.Sprint(want) {
			t.Fatalf("trim record = %+v, want %+v", got[0].Trim, want)
		}
		if got[0].Summary != "" || got[0].FirstKept != "e_t1" {
			t.Errorf("trim entry = %+v, want no summary and the root as the kept boundary", got[0])
		}
		// AfterCompact fires for the trim, the Reason telling it apart.
		mu.Lock()
		if len(after) != 1 || after[0].Reason != thread.ReasonTrim || after[0].ID != got[0].ID {
			t.Errorf("AfterCompact calls = %+v, want the one trim entry", after)
		}
		mu.Unlock()
		// The stored result is untouched: the trim is a view.
		for _, e := range s.Entries() {
			if m, ok := e.(thread.MessageEntry); ok && m.ID == "e_t3" {
				if r := m.Message.Content[0].(weft.ToolResultPart); len(r.Content) != 40_000 {
					t.Error("the trimmer's in-place edit rewrote the stored result")
				}
			}
		}

		// The model's context carries the custom stub — live, and after
		// a reopen under any trimmer configuration.
		wantView := []string{"[gone: read]", "raw"}
		for name, o := range map[string][]thread.SessionOption{
			"live":                  nil,
			"reopened, same":        opts,
			"reopened, no trimmer":  {},
			"reopened, built-in(0)": {thread.ClearOldToolResults(0)},
		} {
			view := s
			if o != nil {
				view = reopenWith(t, ctx, st, s, agent, o...)
			}
			if got := resultContents(view); fmt.Sprint(got) != fmt.Sprint(wantView) {
				t.Errorf("%s: tool results in the context = %q, want %q", name, got, wantView)
			}
		}
	})
}

// The trim record governs the replay, not the session's current
// options: a trim recorded under ClearOldToolResults(1) reads the same
// when the session is reopened with keepLast 0, 5, or no trimmer at
// all. The bug: the walk re-derived the stubs from the runtime
// keepLast, so the model-visible transcript of a recorded trim changed
// with the options.
func TestTrimReplayIgnoresTheCurrentOptions(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent, _ := scriptedAgent(bigUsage(), 2)
		opts := []thread.SessionOption{thread.ContextWindow(100_000), thread.ClearOldToolResults(1)}
		s, _ := thread.Create(ctx, st, agent, opts...)
		toolHistory(t, st, s)
		s = reopenWith(t, ctx, st, s, agent, opts...)
		sendAndWait(t, s, "again")
		got := trims(s)
		if len(got) != 1 || got[0].Trim == nil {
			t.Fatalf("trim entries = %+v, want one with a record", got)
		}
		want := []thread.TrimStub{{Entry: "e_t3", CallID: "c1", Content: "[cleared tool result read c1]"}}
		if fmt.Sprint(got[0].Trim.Stubs) != fmt.Sprint(want) {
			t.Errorf("trim record = %+v, want %+v", got[0].Trim.Stubs, want)
		}
		wantView := []string{"[cleared tool result read c1]", "raw"}
		for name, o := range map[string][]thread.SessionOption{
			"keepLast 0": {thread.ClearOldToolResults(0)},
			"keepLast 5": {thread.ClearOldToolResults(5)},
			"no trimmer": {},
		} {
			view := reopenWith(t, ctx, st, s, agent, o...)
			if got := resultContents(view); fmt.Sprint(got) != fmt.Sprint(wantView) {
				t.Errorf("%s: tool results in the context = %q, want %q", name, got, wantView)
			}
		}
	})
}

// A trim entry written before trim records existed (no "trim" field)
// keeps reading the way it did: under ClearOldToolResults the walk
// re-derives the built-in stubs with the configured keepLast, and
// under any other configuration it stubs nothing.
func TestLegacyTrimEntryReadsAsBefore(t *testing.T) {
	ctx := context.Background()
	agent := weft.New(wefttest.Script())
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, agent)
	toolHistory(t, st, s)
	if err := st.Append(ctx, s.ID(), thread.CompactionEntry{
		ID: "e_legacy", ParentID: "e_t4", Created: timeUTC(),
		FirstKept: "e_t1", TokensBefore: 1, Reason: thread.ReasonTrim,
	}); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		opts []thread.SessionOption
		want []string
	}{
		"keepLast 1": {[]thread.SessionOption{thread.ClearOldToolResults(1)}, []string{"[cleared tool result read c1]", "raw"}},
		"keepLast 0": {[]thread.SessionOption{thread.ClearOldToolResults(0)}, []string{"[cleared tool result read c1]", "[cleared tool result read c2]"}},
		"no trimmer": {nil, []string{"raw", "raw"}},
	} {
		view := reopenWith(t, ctx, st, s, agent, tc.opts...)
		if got := resultContents(view); fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("%s: tool results in the context = %q, want %q", name, got, tc.want)
		}
	}
}

// A Trimmer whose change the trim record cannot represent fails
// loudly — CompactFailed hears ErrInvalidCompaction — and the summary
// compaction runs instead; a Trimmer that returns nothing or an error
// is logged and likewise falls through. Nothing is silently lost.
func TestUnrepresentableTrimIsLoud(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		trim       thread.Trimmer
		wantFailed bool
		wantLog    string
	}{
		"drops a message": {trimmerFunc(func(ctx context.Context, msgs []weft.Message) ([]weft.Message, error) {
			return msgs[1:], nil
		}), true, "the trim cannot be recorded"},
		"rewrites a text part": {trimmerFunc(func(ctx context.Context, msgs []weft.Message) ([]weft.Message, error) {
			msgs[0] = weft.User("rewritten")
			return msgs, nil
		}), true, "the trim cannot be recorded"},
		"renames a result's call": {trimmerFunc(func(ctx context.Context, msgs []weft.Message) ([]weft.Message, error) {
			for i, m := range msgs {
				for j, p := range m.Content {
					if r, ok := p.(weft.ToolResultPart); ok {
						r.CallID, r.Content = "other", "x"
						msgs[i].Content[j] = r
					}
				}
			}
			return msgs, nil
		}), true, "the trim cannot be recorded"},
		"returns nil": {trimmerFunc(func(ctx context.Context, msgs []weft.Message) ([]weft.Message, error) {
			return nil, nil
		}), false, "the trimmer failed"},
		"returns an error": {trimmerFunc(func(ctx context.Context, msgs []weft.Message) ([]weft.Message, error) {
			return nil, errors.New("trimmer down")
		}), false, "the trimmer failed"},
		"panics": {trimmerFunc(func(ctx context.Context, msgs []weft.Message) ([]weft.Message, error) {
			panic("trimmer blew up")
		}), false, "the trimmer failed"},
	} {
		t.Run(name, func(t *testing.T) {
			var buf syncBuffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
			agent := weft.New(wefttest.Script(
				wefttest.Say("reply").WithUsage(bigUsage()),
				wefttest.Say("the summary"),
			), weft.Logger(logger))
			var mu sync.Mutex
			var failedReason thread.Reason
			var failedErr error
			opts := []thread.SessionOption{
				thread.ContextWindow(100_000),
				thread.KeepRecent(1_000),
				thread.WithTrimmer(tc.trim),
				thread.CompactFailed(func(ctx context.Context, r thread.Reason, err error) {
					mu.Lock()
					defer mu.Unlock()
					failedReason, failedErr = r, err
				}),
			}
			st := thread.Memory()
			s, _ := thread.Create(ctx, st, agent, opts...)
			toolHistory(t, st, s)
			s = reopenWith(t, ctx, st, s, agent, opts...)
			sendAndWait(t, s, "again")
			if n := len(trims(s)); n != 0 {
				t.Errorf("trim entries = %d, want none", n)
			}
			mu.Lock()
			if tc.wantFailed {
				if failedReason != thread.ReasonTrim || !errors.Is(failedErr, thread.ErrInvalidCompaction) {
					t.Errorf("CompactFailed = %q, %v; want the trim reason and ErrInvalidCompaction", failedReason, failedErr)
				}
			} else if failedErr != nil {
				t.Errorf("CompactFailed ran: %v", failedErr)
			}
			mu.Unlock()
			if !strings.Contains(buf.String(), tc.wantLog) {
				t.Errorf("log = %q, want %q", buf.String(), tc.wantLog)
			}
			// The summary compaction ran in the trim's place.
			summarized := false
			for _, e := range s.Entries() {
				if c, ok := e.(thread.CompactionEntry); ok && c.Reason == thread.ReasonThreshold && c.Summary == "the summary" {
					summarized = true
				}
			}
			if !summarized {
				t.Error("the summary compaction did not run after the failed trim")
			}
		})
	}
}

// syncBuffer is a bytes.Buffer safe for the runner goroutine's logger.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// The iterative chain survives a trim: Compact → trim → Compact feeds
// the first summary to the second summarizer call and keeps the range
// starting at the first compaction's boundary. The bug: the walk back
// from the leaf took the first CompactionEntry it met as "previous" —
// the trim, whose summary is empty — so the earlier summary dropped
// out of the chain and of the context.
func TestCompactTrimCompactKeepsTheChain(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		rec := &summaryRecorder{reply: "FIRST-SUMMARY"}
		agent := weft.New(rec)
		s, _ := thread.Create(ctx, st, agent)
		now := timeUTC()
		if err := st.Append(ctx, s.ID(),
			thread.MessageEntry{ID: "e_a", Created: now, Message: weft.User(strings.Repeat("a", 60_000))},
			thread.MessageEntry{ID: "e_b", ParentID: "e_a", Created: now, Message: weft.User(strings.Repeat("b", 30_000))},
			thread.MessageEntry{ID: "e_call", ParentID: "e_b", Created: now, Message: weft.Message{Role: weft.RoleAssistant,
				Content: []weft.Part{weft.ToolCallPart{ID: "c1", Name: "read", Args: []byte("{}")}}}},
			thread.MessageEntry{ID: "e_res", ParentID: "e_call", Created: now, Message: weft.Message{Role: weft.RoleTool,
				Content: []weft.Part{weft.ToolResultPart{CallID: "c1", Name: "read", Content: strings.Repeat("r", 2_000)}}}},
			thread.MessageEntry{ID: "e_c", ParentID: "e_res", Created: now, Message: weft.User(strings.Repeat("c", 30_000))},
		); err != nil {
			t.Fatal(err)
		}
		s = reopenWith(t, ctx, st, s, agent)
		if err := s.Compact(ctx); err != nil {
			t.Fatalf("first Compact: %v", err)
		}
		var first thread.CompactionEntry
		for _, e := range s.Entries() {
			if c, ok := e.(thread.CompactionEntry); ok {
				first = c
			}
		}
		if first.FirstKept != "e_b" {
			t.Fatalf("first compaction keeps from %q; the scenario expects e_b", first.FirstKept)
		}
		// A trim lands on top, keeping the boundary.
		if err := s.ApplyCompaction(ctx, &thread.Compaction{
			FirstKept: first.FirstKept, Reason: thread.ReasonTrim, TokensBefore: 1,
			Trim: &thread.TrimRecord{Stubs: []thread.TrimStub{{Entry: "e_res", CallID: "c1", Content: "[cleared tool result read c1]"}}},
		}); err != nil {
			t.Fatalf("trim: %v", err)
		}
		if got := s.Context()[0].Text(); !strings.Contains(got, "FIRST-SUMMARY") {
			t.Fatalf("the summary left the context when the trim landed: %q", got)
		}
		// The session grows; the second compaction must carry the first
		// summary forward.
		msgs(t, ctx, st, s, strings.Repeat("d", 30_000), strings.Repeat("e", 30_000), strings.Repeat("f", 30_000))
		rec.mu.Lock()
		rec.reply = "SECOND-SUMMARY"
		rec.mu.Unlock()
		s = reopenWith(t, ctx, st, s, agent)
		var prev string
		s = reopenWith(t, ctx, st, s, agent, thread.BeforeCompact(func(ctx context.Context, p *thread.Preparation) (thread.Verdict, error) {
			prev = p.PrevSummary
			return thread.Proceed(), nil
		}))
		if err := s.Compact(ctx); err != nil {
			t.Fatalf("second Compact: %v", err)
		}
		if prev != "FIRST-SUMMARY" {
			t.Errorf("Preparation.PrevSummary = %q, want the first summary (the trim skipped)", prev)
		}
		reqs := rec.saw()
		second := reqs[len(reqs)-1].Messages
		if len(second) == 0 || second[0].Text() != "<weft-summary>\nFIRST-SUMMARY\n</weft-summary>" {
			t.Fatalf("second summarizer input starts with %q, want the first summary behind the marker", second[0].Text()[:min(60, len(second[0].Text()))])
		}
		for _, m := range second[1:] {
			if strings.Contains(m.Text(), strings.Repeat("a", 100)) {
				t.Error("the range the first summary replaced was re-serialized into the second call")
			}
		}
		sawB := false
		for _, m := range second {
			if strings.Contains(m.Text(), strings.Repeat("b", 100)) {
				sawB = true
			}
		}
		if !sawB {
			t.Error("the second range does not start at the first compaction's kept boundary")
		}
		if got := s.Context()[0].Text(); !strings.Contains(got, "SECOND-SUMMARY") {
			t.Errorf("context leads with %q, want the second summary", got)
		}
		// A summary compaction supersedes the trims below it.
		for _, c := range resultContents(s) {
			if strings.HasPrefix(c, "[cleared") {
				t.Error("a trim below the governing compaction still stubs the kept tail")
			}
		}
	})
}

// After a compaction lands the trigger stands down until the next
// provider report: the measurement it holds describes the context
// before the compaction. The bug: the stale number re-fired the
// trigger on the next Send, compacting a context that had just shrunk.
func TestTriggerStandsDownAfterACompaction(t *testing.T) {
	ctx := context.Background()
	rec := &summaryRecorder{reply: "the summary"}
	agent := weft.New(wefttest.Script(
		wefttest.Say("first").WithUsage(weft.Usage{InputTokens: 95_000, OutputTokens: 5}),
		wefttest.Fail(errors.New("model down")),
		wefttest.Say("third").WithUsage(weft.Usage{InputTokens: 95_000, OutputTokens: 5}),
	))
	opts := []thread.SessionOption{thread.ContextWindow(100_000), thread.KeepRecent(100), thread.SummaryModel(rec)}
	s := compactable(t, thread.Memory(), agent, opts...)
	thresholds := func() int {
		n := 0
		for _, e := range s.Entries() {
			if c, ok := e.(thread.CompactionEntry); ok && c.Reason == thread.ReasonThreshold {
				n++
			}
		}
		return n
	}
	sendAndWait(t, s, "one")
	if thresholds() != 1 {
		t.Fatalf("threshold compactions after the first turn = %d, want 1 — the scenario is not measuring what it should", thresholds())
	}
	// The next turn's prompt alone outweighs KeepRecent, so a re-fire
	// would have something to cut — and the run fails before any step
	// reports: only the stale 95k could fire the pre-turn trigger.
	turn, err := s.Send(ctx, weft.User(strings.Repeat("p", 8_000)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err == nil {
		t.Fatal("the scripted failure did not fail")
	}
	if thresholds() != 1 {
		t.Errorf("threshold compactions = %d after a turn with no new report, want 1: the stale measurement re-fired", thresholds())
	}
	// A turn that reports again re-arms it.
	sendAndWait(t, s, strings.Repeat("q", 8_000))
	if thresholds() != 2 {
		t.Errorf("threshold compactions = %d after a fresh report, want 2: the trigger did not re-arm", thresholds())
	}
}

// A summary compaction whose FirstKept the path does not reach below
// it — a hand-made file; ApplyCompaction never writes one — is
// skipped with one warning: the next older usable compaction governs,
// or the whole path reads raw. The bug: the walk emitted the unusable
// summary AND the whole path.
func TestUnusableCompactionIsSkipped(t *testing.T) {
	ctx := context.Background()
	history := []thread.Entry{
		thread.MessageEntry{ID: "e_1", Created: timeUTC(), Message: weft.User("one")},
		thread.MessageEntry{ID: "e_2", ParentID: "e_1", Created: timeUTC(), Message: weft.Assistant("two")},
		thread.MessageEntry{ID: "e_3", ParentID: "e_2", Created: timeUTC(), Message: weft.User("three")},
	}
	open := func(t *testing.T, extra ...thread.Entry) (*thread.Session, *syncBuffer) {
		t.Helper()
		var buf syncBuffer
		agent := weft.New(wefttest.Script(), weft.Logger(slog.New(slog.NewTextHandler(&buf, nil))))
		st := thread.Memory()
		s, _ := thread.Create(ctx, st, agent)
		if err := st.Append(ctx, s.ID(), append(append([]thread.Entry(nil), history...), extra...)...); err != nil {
			t.Fatal(err)
		}
		return reopenWith(t, ctx, st, s, agent), &buf
	}

	t.Run("no older compaction: the whole path, raw", func(t *testing.T) {
		s, buf := open(t, thread.CompactionEntry{
			ID: "e_bad", ParentID: "e_3", Created: timeUTC(),
			Summary: "A SUMMARY OF NOTHING", FirstKept: "e_elsewhere", Reason: thread.ReasonManual,
		})
		if got, want := contextTexts(s), []string{"one", "two", "three"}; !equalStrings(got, want) {
			t.Errorf("Context = %q, want %q", got, want)
		}
		_ = s.Context()
		if n := strings.Count(buf.String(), "compaction entry is unusable"); n != 1 {
			t.Errorf("unusable-compaction warnings = %d over two context builds, want 1:\n%s", n, buf.String())
		}
	})

	t.Run("an older usable compaction governs", func(t *testing.T) {
		s, _ := open(t,
			thread.CompactionEntry{ID: "e_good", ParentID: "e_3", Created: timeUTC(),
				Summary: "GOOD", FirstKept: "e_3", Reason: thread.ReasonManual},
			thread.CompactionEntry{ID: "e_bad", ParentID: "e_good", Created: timeUTC(),
				Summary: "BAD", FirstKept: "e_elsewhere", Reason: thread.ReasonManual},
		)
		want := []string{"<weft-summary>\nGOOD\n</weft-summary>", "three"}
		if got := contextTexts(s); !equalStrings(got, want) {
			t.Errorf("Context = %q, want %q", got, want)
		}
	})
}

// The trim record's wire shape is pinned: a compaction entry carrying
// one is written with "v":5 — its minimum reader version (ADR 0011
// §6) — and reads back to the same bytes; a summary compaction stays a
// format-1 line with no "v"; a higher "v" is ErrNewerFormat.
func TestTrimRecordGolden(t *testing.T) {
	e := thread.CompactionEntry{
		ID: "e_01J8X9M2K7QW4R5N8T6V2B3C5A", ParentID: "e_01J8X9M2K7QW4R5N8T6V2B3C4Z", Created: at(30),
		FirstKept:    entryID0,
		TokensBefore: 91_204,
		Reason:       thread.ReasonTrim,
		Trim: &thread.TrimRecord{Stubs: []thread.TrimStub{
			{Entry: entryID3, CallID: "call_1", Content: "[cleared tool result read call_1]"},
			{Entry: entryID3, CallID: "call_2", Content: "[failed lookup elided]", IsError: true},
		}},
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"type":"compaction","v":5,`)) {
		t.Errorf("trim entry wire = %s, want \"v\":5 after the type", b)
	}
	path := filepath.Join("testdata", "format5", "compaction_trim.json")
	wefttest.Golden(t, path, append(b, '\n'))

	back, err := thread.UnmarshalEntry(b)
	if err != nil {
		t.Fatalf("UnmarshalEntry: %v", err)
	}
	again, err := json.Marshal(back)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, again) {
		t.Errorf("trim entry re-marshal differs\n got %s\nwant %s", again, b)
	}

	plain, err := json.Marshal(thread.CompactionEntry{ID: "e_x", FirstKept: "e_y", Summary: "s", Reason: thread.ReasonManual})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(plain, []byte(`"v"`)) {
		t.Errorf("a summary compaction carries a version: %s", plain)
	}
	newer := bytes.Replace(b, []byte(`"v":5`), []byte(`"v":6`), 1)
	if _, err := thread.UnmarshalEntry(newer); !errors.Is(err, thread.ErrNewerFormat) {
		t.Errorf("a compaction entry at v=6: err = %v, want ErrNewerFormat", err)
	}
}

// The compacted context's whole shape is model-visible and pinned (ADR
// 0020, amendment 2026-10-01): the summary behind the marker first,
// then the pinned entries in path order, then the kept tail with the
// trim record replayed — and a branch summary, behind the same
// marker, wherever it sits on the path.
func TestCompactedContextShapeGolden(t *testing.T) {
	ctx := context.Background()
	agent := weft.New(wefttest.Script())
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, agent)
	now := timeUTC()
	if err := st.Append(ctx, s.ID(),
		thread.MessageEntry{ID: "e_1", Created: now, Message: weft.User("THE REQUIREMENT: ship by Friday")},
		thread.MessageEntry{ID: "e_2", ParentID: "e_1", Created: now, Message: weft.Assistant("Understood.")},
		thread.MessageEntry{ID: "e_3", ParentID: "e_2", Created: now, Message: weft.User("read both files")},
		thread.MessageEntry{ID: "e_4", ParentID: "e_3", Created: now, Message: weft.Message{Role: weft.RoleAssistant,
			Content: []weft.Part{
				weft.ReasoningPart{Text: "which first?", Signature: "sig-1"},
				weft.ToolCallPart{ID: "c1", Name: "read", Args: []byte(`{"path":"a.go"}`)},
				weft.ToolCallPart{ID: "c2", Name: "read", Args: []byte(`{"path":"b.go"}`)},
			}}},
		thread.MessageEntry{ID: "e_5", ParentID: "e_4", Created: now, Message: weft.Message{Role: weft.RoleTool,
			Content: []weft.Part{
				weft.ToolResultPart{CallID: "c1", Name: "read", Content: "package a // a long file"},
				weft.ToolResultPart{CallID: "c2", Name: "read", Content: "package b"},
			}}},
		thread.CompactionEntry{ID: "e_6", ParentID: "e_5", Created: now,
			Summary: "Goal: ship by Friday. Progress: plan agreed.", FirstKept: "e_3",
			TokensBefore: 120, Reason: thread.ReasonManual, Pinned: []string{"e_1"}},
		thread.CompactionEntry{ID: "e_7", ParentID: "e_6", Created: now,
			FirstKept: "e_3", TokensBefore: 80, Reason: thread.ReasonTrim,
			Trim: &thread.TrimRecord{Stubs: []thread.TrimStub{
				{Entry: "e_5", CallID: "c1", Content: "[cleared tool result read c1]"},
			}}},
		thread.BranchSummaryEntry{ID: "e_8", ParentID: "e_7", Created: now,
			Summary: "The abandoned branch tried a rewrite; it was dropped.", FromEntry: "e_7"},
		thread.MessageEntry{ID: "e_9", ParentID: "e_8", Created: now, Message: weft.User("now fix a.go")},
	); err != nil {
		t.Fatal(err)
	}
	s = reopenWith(t, ctx, st, s, agent)
	got, err := json.MarshalIndent(s.Context(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	wefttest.Golden(t, filepath.Join("testdata", "compaction", "compacted-context-full.txt"), append(got, '\n'))
}
