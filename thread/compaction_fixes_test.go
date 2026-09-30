package thread_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
)

// The post-0.7 review's compaction fixes, one regression test each.

func callMsg(id string) weft.Message {
	return weft.Message{Role: weft.RoleAssistant, Content: []weft.Part{
		weft.ToolCallPart{ID: id, Name: "lookup", Args: json.RawMessage(`{}`)},
	}}
}

func resultMsg(id, content string) weft.Message {
	return weft.Message{Role: weft.RoleTool, Content: []weft.Part{
		weft.ToolResultPart{CallID: id, Name: "lookup", Content: content},
	}}
}

func countReasons(s *thread.Session) map[thread.Reason]int {
	out := map[thread.Reason]int{}
	for _, e := range s.Entries() {
		if c, ok := e.(thread.CompactionEntry); ok {
			out[c.Reason]++
		}
	}
	return out
}

func estimateTokens(msgs []weft.Message) int64 {
	var n int64
	for _, m := range msgs {
		b, _ := json.Marshal(m)
		n += (int64(len(b)) + 3) / 4
	}
	return n
}

// A trim record on the path is never the iterative chain's link: the
// next summary compaction is fed the previous summary and summarizes
// only from the previous kept boundary, as it would with no trim.
func TestCompactionAfterTrimKeepsSummaryChain(t *testing.T) {
	ctx := context.Background()
	rec := &summaryRecorder{reply: "SUMMARY-ONE"}
	agent, _ := scriptedAgent(bigUsage(), 4)
	st := thread.Memory()
	opts := []thread.SessionOption{thread.ContextWindow(100_000), thread.ClearOldToolResults(0), thread.SummaryModel(rec), thread.KeepRecent(100)}
	s, _ := thread.Create(ctx, st, agent, opts...)
	msgs(t, ctx, st, s, strings.Repeat("a", 30_000), strings.Repeat("b", 30_000), strings.Repeat("c", 30_000))
	s = reopenWith(t, ctx, st, s, agent, opts...)
	if err := s.Compact(ctx); err != nil { // C1
		t.Fatal(err)
	}
	// Bulky tool results after C1: clearing them saves ~20k estimated
	// tokens, bringing the reported 90k back under 100k − 16,384.
	now := time.Now().UTC()
	if err := st.Append(ctx, s.ID(),
		thread.MessageEntry{ID: "e_tc", ParentID: s.Leaf(), Created: now, Message: callMsg("c1")},
		thread.MessageEntry{ID: "e_tr", ParentID: "e_tc", Created: now, Message: resultMsg("c1", strings.Repeat("r", 80_000))},
	); err != nil {
		t.Fatal(err)
	}
	s = reopenWith(t, ctx, st, s, agent, opts...)
	turn, err := s.Send(ctx, weft.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the trim record", func() bool { return countReasons(s)[thread.ReasonTrim] > 0 })

	s, _ = msgs(t, ctx, st, s, strings.Repeat("d", 30_000), strings.Repeat("e", 30_000))
	s = reopenWith(t, ctx, st, s, agent, thread.SummaryModel(rec), thread.KeepRecent(100))
	rec.mu.Lock()
	rec.reply = "SUMMARY-TWO"
	rec.mu.Unlock()
	if err := s.Compact(ctx); err != nil { // C2
		t.Fatal(err)
	}
	reqs := rec.saw()
	fed := reqs[len(reqs)-1].Messages
	var sawPrev, sawRoot bool
	for _, m := range fed {
		sawPrev = sawPrev || strings.Contains(m.Text(), "SUMMARY-ONE")
		sawRoot = sawRoot || strings.HasPrefix(m.Text(), "aaaa")
	}
	if !sawPrev {
		t.Error("the summarizer after a trim was not fed the previous summary: the chain broke at the trim record")
	}
	if sawRoot {
		t.Error("the summarizer after a trim re-summarized history C1 had already replaced")
	}
	if got := s.Context()[0].Text(); !strings.Contains(got, "SUMMARY-TWO") {
		t.Errorf("context leads with %q, want C2's summary", got)
	}
}

// The trim pre-pass is judged by the reported input less the trim's
// saving (ADR 0020 §2) — never by an estimate of the whole context —
// and a trim that clears nothing is never recorded: with no tool
// results to clear, a reported input over the line summarizes.
func TestTrimThatClearsNothingIsNotRecorded(t *testing.T) {
	ctx := context.Background()
	agent, _ := scriptedAgent(bigUsage(), 8)
	st := thread.Memory()
	opts := []thread.SessionOption{thread.ContextWindow(100_000), thread.ClearOldToolResults(0)}
	s, _ := thread.Create(ctx, st, agent, opts...)
	msgs(t, ctx, st, s, strings.Repeat("a", 30_000), strings.Repeat("b", 30_000), strings.Repeat("c", 30_000))
	s = reopenWith(t, ctx, st, s, agent, opts...)
	turn, err := s.Send(ctx, weft.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	got := countReasons(s)
	if got[thread.ReasonTrim] != 0 {
		t.Errorf("%d trim records for a trim that cleared nothing", got[thread.ReasonTrim])
	}
	if got[thread.ReasonThreshold] == 0 {
		t.Errorf("reported input over the line, nothing to trim: no summary compaction (%v)", got)
	}
}

// A trim whose saving is too small to bring the reported input back
// under the line does not stand in for the summary.
func TestTrimTooSmallStillSummarizes(t *testing.T) {
	ctx := context.Background()
	agent, _ := scriptedAgent(bigUsage(), 8)
	st := thread.Memory()
	opts := []thread.SessionOption{thread.ContextWindow(100_000), thread.ClearOldToolResults(0)}
	s, _ := thread.Create(ctx, st, agent, opts...)
	now := time.Now().UTC()
	if err := st.Append(ctx, s.ID(),
		thread.MessageEntry{ID: "e_u", Created: now, Message: weft.User(strings.Repeat("a", 30_000))},
		thread.MessageEntry{ID: "e_tc", ParentID: "e_u", Created: now, Message: callMsg("c1")},
		thread.MessageEntry{ID: "e_tr", ParentID: "e_tc", Created: now, Message: resultMsg("c1", strings.Repeat("r", 400))},
		thread.MessageEntry{ID: "e_a", ParentID: "e_tr", Created: now, Message: weft.Assistant(strings.Repeat("b", 30_000))},
		thread.MessageEntry{ID: "e_u2", ParentID: "e_a", Created: now, Message: weft.User(strings.Repeat("c", 30_000))},
	); err != nil {
		t.Fatal(err)
	}
	s = reopenWith(t, ctx, st, s, agent, opts...)
	turn, err := s.Send(ctx, weft.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	got := countReasons(s)
	if got[thread.ReasonTrim] != 0 || got[thread.ReasonThreshold] == 0 {
		t.Errorf("a ~100-token trim against a 90k reported input: compactions %v, want a summary and no trim", got)
	}
}

// pinPairSession writes a user turn, a tool step (call c1, result
// "THE REAL RESULT"), and two big messages, pins pinID and compacts
// with a 100-token keep window.
func pinPairSession(t *testing.T, pinID string) (*thread.Session, *summaryRecorder) {
	t.Helper()
	ctx := context.Background()
	rec := &summaryRecorder{reply: "s"}
	agent := weft.New(rec)
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, agent, thread.KeepRecent(100))
	now := time.Now().UTC()
	if err := st.Append(ctx, s.ID(),
		thread.MessageEntry{ID: "e_u0", Created: now, Message: weft.User(strings.Repeat("a", 30_000))},
		thread.MessageEntry{ID: "e_call", ParentID: "e_u0", Created: now, Message: callMsg("c1")},
		thread.MessageEntry{ID: "e_res", ParentID: "e_call", Created: now, Message: resultMsg("c1", "THE REAL RESULT")},
		thread.MessageEntry{ID: "e_a1", ParentID: "e_res", Created: now, Message: weft.Assistant(strings.Repeat("b", 30_000))},
		thread.MessageEntry{ID: "e_u2", ParentID: "e_a1", Created: now, Message: weft.User(strings.Repeat("c", 30_000))},
	); err != nil {
		t.Fatal(err)
	}
	s = reopenWith(t, ctx, st, s, agent, thread.KeepRecent(100))
	if err := s.Pin(ctx, pinID); err != nil {
		t.Fatal(err)
	}
	if err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	return s, rec
}

// Pinning either half of a tool call/result pair keeps the pair: the
// context shows the call with its real result, never a fabricated
// "interrupted" one or an orphan result Repair drops.
func TestPinKeepsToolCallAndResultTogether(t *testing.T) {
	for _, pin := range []string{"e_call", "e_res"} {
		t.Run(pin, func(t *testing.T) {
			s, rec := pinPairSession(t, pin)
			var calls, real, other int
			for _, m := range s.Context() {
				for _, p := range m.Content {
					switch p := p.(type) {
					case weft.ToolCallPart:
						if p.ID == "c1" {
							calls++
						}
					case weft.ToolResultPart:
						if p.CallID == "c1" && p.Content == "THE REAL RESULT" {
							real++
						} else if p.CallID == "c1" {
							other++
						}
					}
				}
			}
			if calls != 1 || real != 1 || other != 0 {
				t.Errorf("pinned %s: context shows call×%d, real result×%d, other result×%d; want the pair once", pin, calls, real, other)
			}
			var last thread.CompactionEntry
			for _, e := range s.Entries() {
				if c, ok := e.(thread.CompactionEntry); ok {
					last = c
				}
			}
			if fmt.Sprint(last.Pinned) != "[e_call e_res]" {
				t.Errorf("entry records Pinned %v, want [e_call e_res]", last.Pinned)
			}
			// Pin's doc: a pinned entry is summarized with its range,
			// then re-enters raw — it does not hold the cut back.
			reqs := rec.saw()
			found := false
			for _, m := range reqs[len(reqs)-1].Messages {
				for _, p := range m.Content {
					if r, ok := p.(weft.ToolResultPart); ok && r.Content == "THE REAL RESULT" {
						found = true
					}
				}
			}
			if !found {
				t.Error("the pinned pair was not in the summarized range")
			}
		})
	}
}

// The summarizer's input is a repaired transcript: a dangling call a
// dead turn left in the tree reaches it with a result, never as an
// orphan tool call a provider rejects.
func TestCompactionSummarizerInputIsRepaired(t *testing.T) {
	ctx := context.Background()
	rec := &summaryRecorder{reply: "s"}
	agent := weft.New(rec)
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, agent, thread.KeepRecent(100))
	now := time.Now().UTC()
	if err := st.Append(ctx, s.ID(),
		thread.MessageEntry{ID: "e_u0", Created: now, Message: weft.User(strings.Repeat("a", 30_000))},
		thread.MessageEntry{ID: "e_call", ParentID: "e_u0", Created: now, Message: callMsg("c1")},
		// no tool message: the turn died mid-step
		thread.MessageEntry{ID: "e_u1", ParentID: "e_call", Created: now, Message: weft.User(strings.Repeat("b", 30_000))},
		// a later completed step, so the approval boundary is closed
		thread.MessageEntry{ID: "e_a1", ParentID: "e_u1", Created: now, Message: callMsg("c2")},
		thread.MessageEntry{ID: "e_r2", ParentID: "e_a1", Created: now, Message: resultMsg("c2", "ok")},
		thread.MessageEntry{ID: "e_a2", ParentID: "e_r2", Created: now, Message: weft.Assistant(strings.Repeat("c", 30_000))},
		thread.MessageEntry{ID: "e_u2", ParentID: "e_a2", Created: now, Message: weft.User(strings.Repeat("d", 30_000))},
	); err != nil {
		t.Fatal(err)
	}
	s = reopenWith(t, ctx, st, s, agent, thread.KeepRecent(100))
	if err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	reqs := rec.saw()
	sent := reqs[len(reqs)-1].Messages
	calls, results := 0, 0
	for _, m := range sent {
		for _, p := range m.Content {
			switch p.(type) {
			case weft.ToolCallPart:
				calls++
			case weft.ToolResultPart:
				results++
			}
		}
	}
	if calls != results {
		t.Errorf("summarizer request carries %d tool calls and %d results: an orphan call reached the provider", calls, results)
	}
}

// TokensBefore measures the context the model sees — after a first
// compaction, the summary and its kept range — not the raw history.
func TestTokensBeforeMeasuresTheCompactedContext(t *testing.T) {
	ctx := context.Background()
	rec := &summaryRecorder{reply: "s"}
	agent := weft.New(rec)
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, agent, thread.KeepRecent(100))
	s, _ = msgs(t, ctx, st, s, strings.Repeat("a", 30_000), strings.Repeat("b", 30_000), strings.Repeat("c", 30_000))
	s = reopenWith(t, ctx, st, s, agent, thread.KeepRecent(100))
	if err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	s, _ = msgs(t, ctx, st, s, strings.Repeat("d", 30_000))
	s = reopenWith(t, ctx, st, s, agent, thread.KeepRecent(100))
	want := estimateTokens(s.Context())
	var prepCtx int64
	s = reopenWith(t, ctx, st, s, agent, thread.KeepRecent(100),
		thread.BeforeCompact(func(_ context.Context, p *thread.Preparation) (thread.Verdict, error) {
			prepCtx = estimateTokens(p.Context)
			return thread.Proceed, nil
		}))
	c, err := s.PreviewCompaction(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c.TokensBefore != want {
		t.Errorf("TokensBefore = %d, want the context's %d", c.TokensBefore, want)
	}
	if prepCtx != want {
		t.Errorf("Preparation.Context estimates %d, want the context's %d", prepCtx, want)
	}
}

// MaxPerSession caps automatic compactions only: a manual Compact
// does not spend the automatic budget.
func TestMaxPerSessionIgnoresManualCompactions(t *testing.T) {
	ctx := context.Background()
	agent, _ := scriptedAgent(bigUsage(), 10)
	st := thread.Memory()
	opts := []thread.SessionOption{thread.ContextWindow(100_000), thread.MaxPerSession(1), thread.KeepRecent(100)}
	s, _ := thread.Create(ctx, st, agent, opts...)
	msgs(t, ctx, st, s, strings.Repeat("a", 30_000), strings.Repeat("b", 30_000), strings.Repeat("c", 30_000))
	s = reopenWith(t, ctx, st, s, agent, opts...)
	if err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		turn, err := s.Send(ctx, weft.User(strings.Repeat("g", 30_000)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := turn.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	if n := countReasons(s)[thread.ReasonThreshold]; n != 1 {
		t.Errorf("automatic compactions = %d after one manual Compact under MaxPerSession(1), want 1", n)
	}
}

// CorruptError is ErrCorrupt by class and keeps its cause reachable.
func TestCorruptErrorReachesItsCause(t *testing.T) {
	var syn *json.SyntaxError
	cause := json.Unmarshal([]byte("{"), &struct{}{})
	if !errors.As(cause, &syn) {
		t.Fatalf("setup: %T", cause)
	}
	err := fmt.Errorf("load: %w", &thread.CorruptError{Session: "s_x", Line: 3, Err: cause})
	if !errors.Is(err, thread.ErrCorrupt) {
		t.Error("errors.Is(err, ErrCorrupt) = false")
	}
	if !errors.As(err, &syn) {
		t.Error("errors.As cannot reach the cause")
	}
	sentinel := errors.New("cause")
	if !errors.Is(&thread.CorruptError{Err: sentinel}, sentinel) {
		t.Error("errors.Is cannot reach the cause")
	}
	if !errors.Is(&thread.CorruptError{}, thread.ErrCorrupt) {
		t.Error("a CorruptError without a cause is not ErrCorrupt")
	}
	if errors.Is(&thread.CorruptError{Err: sentinel}, thread.ErrNotFound) {
		t.Error("CorruptError matched an unrelated sentinel")
	}
}
