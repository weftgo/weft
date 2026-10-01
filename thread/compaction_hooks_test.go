package thread_test

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// compactable creates a session holding three ~7.5k-token user
// messages — enough for one compaction under the default KeepRecent —
// opened on agent with opts.
func compactable(t *testing.T, st thread.Storage, agent *weft.Agent, opts ...thread.SessionOption) *thread.Session {
	t.Helper()
	ctx := context.Background()
	s, err := thread.Create(ctx, st, agent, opts...)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	msgs(t, ctx, st, s,
		strings.Repeat("a", 30_000),
		strings.Repeat("b", 30_000),
		strings.Repeat("c", 30_000),
	)
	return reopenWith(t, ctx, st, s, agent, opts...)
}

// within fails the test when fn has not returned in five seconds — the
// shape a session deadlocked on its own mutex takes.
func within(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not return: the session is deadlocked on its own lock", what)
	}
}

// An AfterCompact hook may read the session it is told about: the
// entry is appended under the lock, the hook runs after it is
// released. The bug: ApplyCompaction called the hook with s.mu held,
// and a hook calling Context, Usage or Leaf blocked forever.
func TestAfterCompactMayCallTheSession(t *testing.T) {
	ctx := context.Background()
	rec := &summaryRecorder{reply: "the summary"}
	var s *thread.Session
	var got thread.CompactionEntry
	var sawSummary bool
	var sawLeaf string
	after := thread.AfterCompact(func(ctx context.Context, e thread.CompactionEntry) {
		got = e
		sawSummary = strings.Contains(s.Context()[0].Text(), "the summary")
		_ = s.Usage()
		sawLeaf = s.Leaf()
	})
	s = compactable(t, thread.Memory(), weft.New(rec), after)
	within(t, "Compact with an AfterCompact hook that reads the session", func() {
		if err := s.Compact(ctx); err != nil {
			t.Errorf("Compact: %v", err)
		}
	})
	if got.ID == "" || got.Reason != thread.ReasonManual {
		t.Fatalf("AfterCompact entry = %+v, want the landed manual compaction", got)
	}
	if !sawSummary {
		t.Error("the hook's Context did not show the compaction it was told about")
	}
	if sawLeaf != got.ID {
		t.Errorf("the hook's Leaf = %q, want the compaction entry %q", sawLeaf, got.ID)
	}
}

// leafSummarizer and leafCompactor call back into the session — the
// thing every caller hook is allowed to do.
type leafSummarizer struct{ s **thread.Session }

func (l leafSummarizer) Summarize(ctx context.Context, in thread.SummaryInput) (thread.Summary, error) {
	_ = (*l.s).Leaf()
	return thread.Summary{Text: "custom summary"}, nil
}

type leafCompactor struct{ s **thread.Session }

func (l leafCompactor) Compact(ctx context.Context, p thread.Preparation) (*thread.Compaction, error) {
	_ = (*l.s).Leaf()
	return &thread.Compaction{Summary: "compactor summary", FirstKept: p.FirstKept}, nil
}

// sessionEstimator weighs every message at 10,000 tokens and reads the
// session while it does.
type sessionEstimator struct{ s **thread.Session }

func (e sessionEstimator) Estimate(msgs []weft.Message) int64 {
	_ = (*e.s).Leaf()
	return int64(len(msgs)) * 10_000
}

// The lock rule, hook by hook: every caller-supplied function runs
// without the session lock and may call the session.
func TestEveryCompactionHookMayCallTheSession(t *testing.T) {
	ctx := context.Background()
	t.Run("manual path", func(t *testing.T) {
		for name, mk := range map[string]func(s **thread.Session) []thread.SessionOption{
			"BeforeCompact": func(s **thread.Session) []thread.SessionOption {
				return []thread.SessionOption{thread.BeforeCompact(func(ctx context.Context, p *thread.Preparation) (thread.Verdict, error) {
					_ = (*s).Context()
					return thread.Proceed(), nil
				})}
			},
			"CheckSummary": func(s **thread.Session) []thread.SessionOption {
				return []thread.SessionOption{thread.CheckSummary(func(sum thread.Summary) error {
					_ = (*s).Usage()
					return nil
				})}
			},
			"AfterCompact": func(s **thread.Session) []thread.SessionOption {
				return []thread.SessionOption{thread.AfterCompact(func(ctx context.Context, e thread.CompactionEntry) {
					_ = (*s).Entries()
				})}
			},
			"WithSummarizer": func(s **thread.Session) []thread.SessionOption {
				return []thread.SessionOption{thread.WithSummarizer(leafSummarizer{s})}
			},
			"WithCompactor": func(s **thread.Session) []thread.SessionOption {
				return []thread.SessionOption{thread.WithCompactor(leafCompactor{s})}
			},
			"WithEstimator": func(s **thread.Session) []thread.SessionOption {
				return []thread.SessionOption{thread.WithEstimator(sessionEstimator{s})}
			},
		} {
			t.Run(name, func(t *testing.T) {
				var s *thread.Session
				s = compactable(t, thread.Memory(), weft.New(&summaryRecorder{reply: "the summary"}), mk(&s)...)
				within(t, "Compact under "+name, func() {
					if err := s.Compact(ctx); err != nil {
						t.Errorf("Compact: %v", err)
					}
				})
				if hasCompaction(s) != 1 {
					t.Errorf("compactions = %d, want 1", hasCompaction(s))
				}
			})
		}
	})

	t.Run("CompactFailed", func(t *testing.T) {
		var s *thread.Session
		called := false
		s = compactable(t, thread.Memory(), weft.New(&failingModel{}),
			thread.CompactFailed(func(ctx context.Context, r thread.Reason, err error) {
				called = true
				_ = s.Context()
			}))
		within(t, "Compact under a CompactFailed hook", func() {
			if err := s.Compact(ctx); err == nil {
				t.Error("Compact with a failing summarizer: no error")
			}
		})
		if !called {
			t.Error("CompactFailed did not run")
		}
	})

	t.Run("automatic path", func(t *testing.T) {
		var s *thread.Session
		var triggered, trimmed atomic.Bool
		agent, _ := scriptedAgent(bigUsage(), 4)
		opts := []thread.SessionOption{
			thread.ContextWindow(100_000),
			thread.TriggerFunc(func(in thread.TriggerInput) bool {
				triggered.Store(true)
				_ = s.Leaf()
				return true
			}),
			thread.WithTrimmer(trimmerFunc(func(ctx context.Context, m []weft.Message) ([]weft.Message, error) {
				trimmed.Store(true)
				_ = s.Context()
				return m, nil
			})),
		}
		s = compactable(t, thread.Memory(), agent, opts...)
		within(t, "a turn whose trigger and trimmer read the session", func() {
			turn, err := s.Send(ctx, weft.User("go"))
			if err != nil {
				t.Errorf("Send: %v", err)
				return
			}
			if _, err := turn.Wait(); err != nil {
				t.Errorf("Wait: %v", err)
			}
		})
		if !triggered.Load() || !trimmed.Load() {
			t.Errorf("TriggerFunc ran = %v, Trimmer ran = %v; want both", triggered.Load(), trimmed.Load())
		}
	})
}

// trimmerFunc adapts a function to thread.Trimmer.
type trimmerFunc func(ctx context.Context, msgs []weft.Message) ([]weft.Message, error)

func (f trimmerFunc) Trim(ctx context.Context, msgs []weft.Message) ([]weft.Message, error) {
	return f(ctx, msgs)
}

// failingAppends is a Storage whose Append fails on demand — the
// disk-full moment between a computed compaction and its write.
type failingAppends struct {
	thread.Storage
	fail atomic.Bool
}

func (f *failingAppends) Append(ctx context.Context, session string, entries ...thread.Entry) error {
	if f.fail.Load() {
		return errors.New("disk on fire")
	}
	return f.Storage.Append(ctx, session, entries...)
}

// CompactFailed's contract: it runs when a compaction that was to be
// written is not — an ApplyCompaction that cannot store its entry
// included — and never for a PreviewCompaction, which is a dry run,
// nor for the refusals that attempt nothing.
func TestCompactFailedFiresForWritesNotDryRuns(t *testing.T) {
	ctx := context.Background()
	type call struct {
		reason thread.Reason
		err    error
	}
	var mu sync.Mutex
	var calls []call
	failed := thread.CompactFailed(func(ctx context.Context, r thread.Reason, err error) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, call{r, err})
	})
	take := func() []call {
		mu.Lock()
		defer mu.Unlock()
		out := calls
		calls = nil
		return out
	}

	t.Run("ApplyCompaction storage failure fires", func(t *testing.T) {
		st := &failingAppends{Storage: thread.Memory()}
		s := compactable(t, st, weft.New(&summaryRecorder{reply: "s"}), failed)
		plan, err := s.PreviewCompaction(ctx)
		if err != nil {
			t.Fatal(err)
		}
		st.fail.Store(true)
		err = s.ApplyCompaction(ctx, plan)
		if err == nil || !strings.Contains(err.Error(), "disk on fire") {
			t.Fatalf("ApplyCompaction over a failing storage: err = %v", err)
		}
		got := take()
		if len(got) != 1 || got[0].reason != thread.ReasonManual || !strings.Contains(got[0].err.Error(), "disk on fire") {
			t.Errorf("CompactFailed calls = %+v, want one with the manual reason and the storage error", got)
		}
		if hasCompaction(s) != 0 {
			t.Error("a compaction entry was adopted over a failed append")
		}
	})

	t.Run("PreviewCompaction failure does not fire, Compact does", func(t *testing.T) {
		s := compactable(t, thread.Memory(), weft.New(&failingModel{}), failed)
		if _, err := s.PreviewCompaction(ctx); err == nil {
			t.Fatal("PreviewCompaction with a failing summarizer: no error")
		}
		if got := take(); len(got) != 0 {
			t.Errorf("CompactFailed ran for a dry run: %+v", got)
		}
		if err := s.Compact(ctx); err == nil {
			t.Fatal("Compact with a failing summarizer: no error")
		}
		if got := take(); len(got) != 1 || got[0].reason != thread.ReasonManual {
			t.Errorf("CompactFailed calls after a failed Compact = %+v, want one", got)
		}
	})

	t.Run("refusals do not fire", func(t *testing.T) {
		s, err := thread.Create(ctx, thread.Memory(), weft.New(&summaryRecorder{reply: "s"}), failed,
			thread.BeforeCompact(func(ctx context.Context, p *thread.Preparation) (thread.Verdict, error) {
				return thread.Cancel(), nil
			}))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Compact(ctx); !errors.Is(err, thread.ErrNothingToCompact) {
			t.Errorf("Compact on an empty session: err = %v, want ErrNothingToCompact", err)
		}
		s2 := compactable(t, thread.Memory(), weft.New(&summaryRecorder{reply: "s"}), failed,
			thread.BeforeCompact(func(ctx context.Context, p *thread.Preparation) (thread.Verdict, error) {
				return thread.Cancel(), nil
			}))
		if err := s2.Compact(ctx); !errors.Is(err, thread.ErrCompactCanceled) {
			t.Errorf("canceled Compact: err = %v, want ErrCompactCanceled", err)
		}
		if got := take(); len(got) != 0 {
			t.Errorf("CompactFailed ran for a refusal: %+v", got)
		}
	})

	t.Run("an invalid plan fires", func(t *testing.T) {
		s := compactable(t, thread.Memory(), weft.New(&summaryRecorder{reply: "s"}), failed)
		err := s.ApplyCompaction(ctx, &thread.Compaction{Summary: "x", FirstKept: "e_missing"})
		if !errors.Is(err, thread.ErrNoEntry) {
			t.Fatalf("ApplyCompaction with an unheld FirstKept: err = %v, want ErrNoEntry", err)
		}
		if got := take(); len(got) != 1 || !errors.Is(got[0].err, thread.ErrNoEntry) {
			t.Errorf("CompactFailed calls = %+v, want one wrapping ErrNoEntry", got)
		}
	})
}

// heldModel blocks its first Stream until released — a turn held in
// flight for as long as the test needs.
type heldModel struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (m *heldModel) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	return func(yield func(weft.ModelEvent, error) bool) {
		m.once.Do(func() { close(m.started) })
		select {
		case <-m.release:
		case <-ctx.Done():
			yield(nil, ctx.Err())
			return
		}
		if !yield(weft.ModelTextDelta{Text: "done"}, nil) {
			return
		}
		yield(weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 10, OutputTokens: 1}}, nil)
	}
}

// Compaction is a between-turns operation, like Branch: while a turn
// is in flight Compact, ApplyCompaction and Uncompact fail with
// ErrBusy and write nothing — a manual compaction entry must not land
// between a running turn's per-step entries.
func TestCompactWhileATurnRunsIsBusy(t *testing.T) {
	ctx := context.Background()
	gate := &heldModel{started: make(chan struct{}), release: make(chan struct{})}
	rec := &summaryRecorder{reply: "the summary"}
	s := compactable(t, thread.Memory(), weft.New(gate), thread.SummaryModel(rec))
	firstKept := s.Leaf()

	turn, err := s.Send(ctx, weft.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	<-gate.started
	if err := s.Compact(ctx); !errors.Is(err, thread.ErrBusy) {
		t.Errorf("Compact mid-turn: err = %v, want ErrBusy", err)
	}
	if n := len(rec.saw()); n != 0 {
		t.Errorf("the busy Compact paid for %d summaries before refusing", n)
	}
	if err := s.ApplyCompaction(ctx, &thread.Compaction{Summary: "x", FirstKept: firstKept}); !errors.Is(err, thread.ErrBusy) {
		t.Errorf("ApplyCompaction mid-turn: err = %v, want ErrBusy", err)
	}
	if err := s.Uncompact(ctx); !errors.Is(err, thread.ErrBusy) {
		t.Errorf("Uncompact mid-turn: err = %v, want ErrBusy", err)
	}
	if hasCompaction(s) != 0 {
		t.Error("a compaction entry landed while the turn was in flight")
	}
	close(gate.release)
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	// Between turns it works again.
	if err := s.Compact(ctx); err != nil {
		t.Errorf("Compact after the turn: %v", err)
	}
}

// A summary cut off at the output cap is never stored: a max_tokens
// finish is a failed summary on the CheckSummary road — one retry,
// then the fallback — and with no fallback left the compaction fails
// wrapping ErrSummaryTruncated. The bug: the finish reason was never
// read, and half a summary silently became the context.
func TestTruncatedSummaryIsNeverStored(t *testing.T) {
	ctx := context.Background()

	t.Run("no fallback left", func(t *testing.T) {
		cut := wefttest.Script(wefttest.MaxTokens("Goal: ship the"), wefttest.MaxTokens("Goal: ship the"))
		var failedErr error
		s := compactable(t, thread.Memory(), weft.New(cut),
			thread.CompactFailed(func(ctx context.Context, r thread.Reason, err error) { failedErr = err }))
		before := fmt.Sprint(s.Context())
		err := s.Compact(ctx)
		if !errors.Is(err, thread.ErrSummaryTruncated) {
			t.Fatalf("Compact with a truncated summary: err = %v, want ErrSummaryTruncated", err)
		}
		if n := len(cut.Requests()); n != 2 {
			t.Errorf("summarizer attempts = %d, want 2 (one retry)", n)
		}
		if hasCompaction(s) != 0 || fmt.Sprint(s.Context()) != before {
			t.Error("a truncated summary changed the session")
		}
		if !errors.Is(failedErr, thread.ErrSummaryTruncated) {
			t.Errorf("CompactFailed err = %v, want ErrSummaryTruncated", failedErr)
		}
	})

	t.Run("falls back to the session model", func(t *testing.T) {
		cut := wefttest.Script(wefttest.MaxTokens("half"), wefttest.MaxTokens("half"))
		whole := &summaryRecorder{reply: "the whole summary"}
		s := compactable(t, thread.Memory(), weft.New(whole), thread.SummaryModel(cut))
		if err := s.Compact(ctx); err != nil {
			t.Fatalf("Compact through the truncation fallback: %v", err)
		}
		if got := s.Context()[0].Text(); !strings.Contains(got, "the whole summary") || strings.Contains(got, "half") {
			t.Errorf("summary = %q, want the session model's whole one", got)
		}
		if n := len(cut.Requests()); n != 2 {
			t.Errorf("cheap model attempts = %d, want 2 (one retry)", n)
		}
	})

	t.Run("a custom Summarizer reports it the same way", func(t *testing.T) {
		calls := 0
		s := compactable(t, thread.Memory(), weft.New(wefttest.Script()),
			thread.WithSummarizer(summarizerFunc(func(ctx context.Context, in thread.SummaryInput) (thread.Summary, error) {
				calls++
				return thread.Summary{}, fmt.Errorf("%w: my model stopped early", thread.ErrSummaryTruncated)
			})))
		if err := s.Compact(ctx); !errors.Is(err, thread.ErrSummaryTruncated) {
			t.Fatalf("err = %v, want ErrSummaryTruncated", err)
		}
		if calls != 2 {
			t.Errorf("custom Summarizer attempts = %d, want 2 (one retry)", calls)
		}
	})

	t.Run("an empty custom summary is a failure", func(t *testing.T) {
		s := compactable(t, thread.Memory(), weft.New(wefttest.Script()),
			thread.WithSummarizer(summarizerFunc(func(ctx context.Context, in thread.SummaryInput) (thread.Summary, error) {
				return thread.Summary{Text: "  "}, nil
			})))
		if err := s.Compact(ctx); err == nil || hasCompaction(s) != 0 {
			t.Errorf("an empty summary was accepted: err = %v, compactions = %d", err, hasCompaction(s))
		}
	})
}

// summarizerFunc adapts a function to thread.Summarizer.
type summarizerFunc func(ctx context.Context, in thread.SummaryInput) (thread.Summary, error)

func (f summarizerFunc) Summarize(ctx context.Context, in thread.SummaryInput) (thread.Summary, error) {
	return f(ctx, in)
}

// BeforeCompact's edits are the plan: Messages, Instructions, Pinned
// and FirstKept are read back from the Preparation after the hook. The
// bug: only FirstKept was honoured, so a redaction hook that rewrote
// Messages redacted nothing — the summarizer was still fed the
// originals.
func TestBeforeCompactEditsAreHonoured(t *testing.T) {
	ctx := context.Background()

	t.Run("replaced Messages and Instructions reach the summarizer", func(t *testing.T) {
		rec := &summaryRecorder{reply: "the summary"}
		redact := thread.BeforeCompact(func(ctx context.Context, p *thread.Preparation) (thread.Verdict, error) {
			out := make([]weft.Message, len(p.Messages))
			for i := range p.Messages {
				out[i] = weft.User("[redacted]")
			}
			p.Messages = out
			p.Instructions = "mention nothing personal"
			return thread.Proceed(), nil
		})
		s := compactable(t, thread.Memory(), weft.New(rec), redact)
		if err := s.Compact(ctx); err != nil {
			t.Fatal(err)
		}
		reqs := rec.saw()
		if len(reqs) != 1 {
			t.Fatalf("summarizer calls = %d, want 1", len(reqs))
		}
		for _, m := range reqs[0].Messages {
			if m.Text() != "[redacted]" {
				t.Errorf("the summarizer saw %q, want only the redacted range", m.Text()[:min(40, len(m.Text()))])
			}
		}
		if !strings.Contains(reqs[0].System, "mention nothing personal") {
			t.Error("the hook's Instructions did not reach the summary prompt")
		}
	})

	t.Run("an in-place edit never reaches the tree", func(t *testing.T) {
		rec := &summaryRecorder{reply: "the summary"}
		scrub := thread.BeforeCompact(func(ctx context.Context, p *thread.Preparation) (thread.Verdict, error) {
			for i := range p.Messages {
				p.Messages[i].Content[0] = weft.TextPart{Text: "[scrubbed]"}
			}
			return thread.Proceed(), nil
		})
		s := compactable(t, thread.Memory(), weft.New(rec), scrub)
		if err := s.Compact(ctx); err != nil {
			t.Fatal(err)
		}
		if got := rec.saw()[0].Messages[0].Text(); got != "[scrubbed]" {
			t.Errorf("the summarizer saw %q, want the in-place edit", got[:min(40, len(got))])
		}
		for _, e := range s.Entries() {
			if m, ok := e.(thread.MessageEntry); ok && m.Message.Text() == "[scrubbed]" {
				t.Fatal("the hook's in-place edit rewrote a stored entry")
			}
		}
	})

	t.Run("a moved FirstKept rebuilds the range", func(t *testing.T) {
		rec := &summaryRecorder{reply: "the summary"}
		var s *thread.Session
		var third string
		move := thread.BeforeCompact(func(ctx context.Context, p *thread.Preparation) (thread.Verdict, error) {
			p.FirstKept = third // keep only the last message
			return thread.Proceed(), nil
		})
		s = compactable(t, thread.Memory(), weft.New(rec), move)
		third = s.Leaf()
		if err := s.Compact(ctx); err != nil {
			t.Fatal(err)
		}
		if got := rec.saw()[0].Messages; len(got) != 2 {
			t.Errorf("summarized range = %d messages, want the two below the moved boundary", len(got))
		}
		if got := s.Context(); len(got) != 2 || !strings.HasPrefix(got[1].Text(), "c") {
			t.Errorf("Context = %d messages, want the summary and the one kept message", len(got))
		}
	})

	t.Run("invalid edits are loud", func(t *testing.T) {
		for name, tc := range map[string]struct {
			edit func(p *thread.Preparation)
			want error
		}{
			"FirstKept the session does not hold": {func(p *thread.Preparation) { p.FirstKept = "e_nowhere" }, thread.ErrNoEntry},
			"Pinned id off the path":              {func(p *thread.Preparation) { p.Pinned = []string{"e_nowhere"} }, thread.ErrNoEntry},
		} {
			rec := &summaryRecorder{reply: "the summary"}
			s := compactable(t, thread.Memory(), weft.New(rec),
				thread.BeforeCompact(func(ctx context.Context, p *thread.Preparation) (thread.Verdict, error) {
					tc.edit(p)
					return thread.Proceed(), nil
				}))
			if err := s.Compact(ctx); !errors.Is(err, tc.want) {
				t.Errorf("%s: err = %v, want %v", name, err, tc.want)
			}
			if hasCompaction(s) != 0 || len(rec.saw()) != 0 {
				t.Errorf("%s: the invalid edit still summarized or wrote", name)
			}
		}
	})

	t.Run("a FirstKept moved before the previous boundary is refused", func(t *testing.T) {
		rec := &summaryRecorder{reply: "the summary"}
		st := thread.Memory()
		var first string
		armed := false
		hook := thread.BeforeCompact(func(ctx context.Context, p *thread.Preparation) (thread.Verdict, error) {
			if armed {
				p.FirstKept = first
			}
			return thread.Proceed(), nil
		})
		s := compactable(t, st, weft.New(rec), hook)
		for _, e := range s.Entries() {
			if m, ok := e.(thread.MessageEntry); ok {
				first = m.ID
				break
			}
		}
		if err := s.Compact(ctx); err != nil {
			t.Fatal(err)
		}
		msgs(t, ctx, st, s, strings.Repeat("d", 30_000), strings.Repeat("e", 30_000), strings.Repeat("f", 30_000))
		s = reopenWith(t, ctx, st, s, weft.New(rec), hook)
		armed = true
		if err := s.Compact(ctx); !errors.Is(err, thread.ErrInvalidCompaction) {
			t.Errorf("err = %v, want ErrInvalidCompaction", err)
		}
		if hasCompaction(s) != 1 {
			t.Errorf("compactions = %d, want only the first", hasCompaction(s))
		}
	})
}
