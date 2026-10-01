package thread_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// longSession creates a session whose history is three long user
// messages (ids e_0, e_1, e_2) — more than the default KeepRecent
// keeps raw — opened on agent with opts.
func longSession(agent *weft.Agent, opts ...thread.SessionOption) *thread.Session {
	ctx := context.Background()
	st := thread.Memory()
	s, err := thread.Create(ctx, st, agent, opts...)
	if err != nil {
		panic(err)
	}
	parent := ""
	var batch []thread.Entry
	for i, topic := range []string{"order ", "invoice ", "refund "} {
		id := fmt.Sprintf("e_%d", i)
		batch = append(batch, thread.MessageEntry{ID: id, ParentID: parent, Created: time.Now().UTC(),
			Message: weft.User(strings.Repeat(topic, 30_000/len(topic)))})
		parent = id
	}
	if err := st.Append(ctx, s.ID(), batch...); err != nil {
		panic(err)
	}
	s, err = thread.Open(ctx, st, s.ID(), agent, opts...)
	if err != nil {
		panic(err)
	}
	return s
}

// PreviewCompaction computes the next compaction without writing it —
// the cut and the summary, to inspect or approve — and ApplyCompaction
// writes the plan as it stands.
func ExampleSession_PreviewCompaction() {
	ctx := context.Background()
	s := longSession(weft.New(&recordingModel{reply: "Goal: ship the order service."}))

	plan, err := s.PreviewCompaction(ctx, thread.SummaryInstructions("focus on the refund"))
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("summary:", plan.Summary)
	fmt.Println("first kept:", plan.FirstKept)
	fmt.Println("written by the preview:", len(s.Entries()) != 3)

	if err := s.ApplyCompaction(ctx, plan); err != nil {
		fmt.Println(err)
		return
	}
	got := s.Context()
	fmt.Println("context leads with the summary:", strings.HasPrefix(got[0].Text(), "<weft-summary>"))
	fmt.Println("then the kept entries:", len(got)-1)
	// Output:
	// summary: Goal: ship the order service.
	// first kept: e_1
	// written by the preview: false
	// context leads with the summary: true
	// then the kept entries: 2
}

// ApplyCompaction validates a plan against the leaf's path: the same
// boundary twice is refused — errors.Is tells the refusals apart.
func ExampleSession_ApplyCompaction() {
	ctx := context.Background()
	s := longSession(weft.New(wefttest.Script()))

	// A hand-made plan: your own summary, keeping from e_2.
	plan := &thread.Compaction{Summary: "Orders and invoices were reviewed.", FirstKept: "e_2"}
	if err := s.ApplyCompaction(ctx, plan); err != nil {
		fmt.Println(err)
		return
	}
	for _, m := range s.Context() {
		fmt.Println(m.Role, len(m.Text()) < 100)
	}
	err := s.ApplyCompaction(ctx, plan)
	fmt.Println("again:", errors.Is(err, thread.ErrNothingToCompact))
	err = s.ApplyCompaction(ctx, &thread.Compaction{Summary: "x", FirstKept: "e_missing"})
	fmt.Println("unknown entry:", errors.Is(err, thread.ErrNoEntry))
	// Output:
	// user true
	// user false
	// again: true
	// unknown entry: true
}

// SummarizeLeft leaves a branch behind as a summary: the new line's
// context carries it in the abandoned branch's place.
func ExampleSummarizeLeft() {
	ctx := context.Background()
	agent := weft.New(&recordingModel{reply: "Tried the carrier API; it rate-limits."})
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, agent)
	now := time.Now().UTC()
	if err := st.Append(ctx, s.ID(),
		thread.MessageEntry{ID: "e_ask", Created: now, Message: weft.User("Where is order 1234?")},
		thread.MessageEntry{ID: "e_try", ParentID: "e_ask", Created: now, Message: weft.Assistant("Trying the carrier API…")},
		thread.MessageEntry{ID: "e_fail", ParentID: "e_try", Created: now, Message: weft.Assistant("The carrier API is rate-limited.")},
	); err != nil {
		fmt.Println(err)
		return
	}
	s, _ = thread.Open(ctx, st, s.ID(), agent)

	// Back to the question, summarizing the detour on the way out.
	if err := s.Branch(ctx, "e_ask", thread.SummarizeLeft()); err != nil {
		fmt.Println(err)
		return
	}
	for _, m := range s.Context() {
		fmt.Printf("%s: %q\n", m.Role, m.Text())
	}
	// Output:
	// user: "Where is order 1234?"
	// user: "<weft-summary>\nTried the carrier API; it rate-limits.\n</weft-summary>"
}

// BeforeCompact sees every compaction before the summarizer runs and
// may edit the plan: a redaction hook rewrites p.Messages, and the
// summarizer is fed the redacted range.
func ExampleBeforeCompact() {
	ctx := context.Background()
	summarizer := &summaryRecorder{reply: "Goal: resolve the customer's ticket."}
	redact := thread.BeforeCompact(func(ctx context.Context, p *thread.Preparation) (thread.Verdict, error) {
		clean := make([]weft.Message, len(p.Messages))
		for i, m := range p.Messages {
			clean[i] = weft.Message{Role: m.Role, Content: []weft.Part{
				weft.TextPart{Text: strings.ReplaceAll(m.Text(), "ada@example.com", "[email]")},
			}}
		}
		p.Messages = clean
		return thread.Proceed(), nil
	})
	agent := weft.New(summarizer)
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, agent, redact)
	now := time.Now().UTC()
	if err := st.Append(ctx, s.ID(),
		thread.MessageEntry{ID: "e_0", Created: now, Message: weft.User("I am ada@example.com. " + strings.Repeat("My order is late. ", 5_000))},
		thread.MessageEntry{ID: "e_1", ParentID: "e_0", Created: now, Message: weft.User(strings.Repeat("Any news? ", 4_000))},
	); err != nil {
		fmt.Println(err)
		return
	}
	s, _ = thread.Open(ctx, st, s.ID(), agent, redact)
	if err := s.Compact(ctx); err != nil {
		fmt.Println(err)
		return
	}
	fed := summarizer.saw()[0].Messages[0].Text()
	fmt.Println("summarizer saw the address:", strings.Contains(fed, "ada@example.com"))
	fmt.Println("summarizer saw:", fed[:len("I am [email].")])
	// Output:
	// summarizer saw the address: false
	// summarizer saw: I am [email].
}

// CheckSummary validates each summary: a rejected one is retried once
// on the same model, then the chain falls back to the session's own.
func ExampleCheckSummary() {
	ctx := context.Background()
	cheap := &summaryRecorder{reply: "it went fine"}
	session := &summaryRecorder{reply: "Goal: ship the order service."}
	s := longSession(weft.New(session),
		thread.SummaryModel(cheap),
		thread.CheckSummary(func(sum thread.Summary) error {
			if !strings.Contains(sum.Text, "Goal:") {
				return errors.New("summary has no Goal heading")
			}
			return nil
		}),
	)
	if err := s.Compact(ctx); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("cheap model attempts:", len(cheap.saw()))
	fmt.Println("session model attempts:", len(session.saw()))
	fmt.Println(s.Context()[0].Text())
	// Output:
	// cheap model attempts: 2
	// session model attempts: 1
	// <weft-summary>
	// Goal: ship the order service.
	// </weft-summary>
}

// SummaryModel summarizes with a different, cheaper model; its cost
// lands in the ledger's own bucket, never mixed with the turns'.
func ExampleSummaryModel() {
	ctx := context.Background()
	cheap := &summaryRecorder{reply: "Goal: ship the order service."}
	s := longSession(weft.New(wefttest.Script()), thread.SummaryModel(cheap))
	if err := s.Compact(ctx); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("summaries made by the cheap model:", len(cheap.saw()))
	u := s.Usage()
	fmt.Println("summary tokens:", u.Summaries.InputTokens, "in,", u.Summaries.OutputTokens, "out")
	fmt.Println("turn tokens:", u.Turns.InputTokens, "in")
	// Output:
	// summaries made by the cheap model: 1
	// summary tokens: 7 in, 3 out
	// turn tokens: 0 in
}

// ClearOldToolResults is the cheap pre-pass: when the trigger fires,
// old tool results are stubbed, and if that is enough no summary is
// made — a trim record lands instead, and the file keeps the results.
func ExampleClearOldToolResults() {
	ctx := context.Background()
	agent := weft.New(wefttest.Script(
		wefttest.Say("Both files read.").WithUsage(weft.Usage{InputTokens: 90_000, OutputTokens: 5}),
	))
	opts := []thread.SessionOption{
		thread.ContextWindow(100_000), // arms the trigger at 100,000 − Reserve
		thread.ClearOldToolResults(1), // keep the newest result raw
	}
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, agent, opts...)
	now := time.Now().UTC()
	if err := st.Append(ctx, s.ID(),
		thread.MessageEntry{ID: "e_ask", Created: now, Message: weft.User("read both files")},
		thread.MessageEntry{ID: "e_calls", ParentID: "e_ask", Created: now, Message: weft.Message{Role: weft.RoleAssistant,
			Content: []weft.Part{
				weft.ToolCallPart{ID: "call_1", Name: "read", Args: []byte(`{"path":"a.go"}`)},
				weft.ToolCallPart{ID: "call_2", Name: "read", Args: []byte(`{"path":"b.go"}`)},
			}}},
		thread.MessageEntry{ID: "e_results", ParentID: "e_calls", Created: now, Message: weft.Message{Role: weft.RoleTool,
			Content: []weft.Part{
				weft.ToolResultPart{CallID: "call_1", Name: "read", Content: strings.Repeat("a", 40_000)},
				weft.ToolResultPart{CallID: "call_2", Name: "read", Content: strings.Repeat("b", 40_000)},
			}}},
	); err != nil {
		fmt.Println(err)
		return
	}
	s, _ = thread.Open(ctx, st, s.ID(), agent, opts...)

	// The turn reports 90,000 input tokens: over the line, so the
	// trimmer runs after it.
	turn, err := s.Send(ctx, weft.User("thanks"))
	if err != nil {
		fmt.Println(err)
		return
	}
	if _, err := turn.Wait(); err != nil {
		fmt.Println(err)
		return
	}
	for _, e := range s.Entries() {
		if c, ok := e.(thread.CompactionEntry); ok {
			fmt.Println("compaction entry:", c.Reason, "— stubbed", len(c.Trim.Stubs), "result")
		}
	}
	for _, m := range s.Context() {
		for _, p := range m.Content {
			if r, ok := p.(weft.ToolResultPart); ok {
				fmt.Println(r.CallID, "shows", len(r.Content), "bytes:", r.Content[:min(40, len(r.Content))])
			}
		}
	}
	// Output:
	// compaction entry: trim — stubbed 1 result
	// call_1 shows 33 bytes: [cleared tool result read call_1]
	// call_2 shows 40000 bytes: bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
}

// TriggerFunc replaces the trigger's condition: here, compact as soon
// as the reported input passes half the window.
func ExampleTriggerFunc() {
	ctx := context.Background()
	agent := weft.New(wefttest.Script(
		wefttest.Say("Noted.").WithUsage(weft.Usage{InputTokens: 60_000, OutputTokens: 5}),
	))
	s := longSession(agent,
		thread.ContextWindow(100_000),
		thread.SummaryModel(&recordingModel{reply: "Goal: ship the order service."}),
		thread.TriggerFunc(func(in thread.TriggerInput) bool {
			return in.LastInput+in.Estimated > in.Window/2
		}),
	)
	turn, err := s.Send(ctx, weft.User("one more thing"))
	if err != nil {
		fmt.Println(err)
		return
	}
	if _, err := turn.Wait(); err != nil {
		fmt.Println(err)
		return
	}
	for _, e := range s.Entries() {
		if c, ok := e.(thread.CompactionEntry); ok {
			fmt.Println("compacted:", c.Reason)
		}
	}
	// Output:
	// compacted: threshold
}

// bulletSummarizer is a Summarizer that runs no model: it keeps the
// first words of every message in the range.
type bulletSummarizer struct{}

func (bulletSummarizer) Summarize(ctx context.Context, in thread.SummaryInput) (thread.Summary, error) {
	var b strings.Builder
	if in.PrevSummary != "" {
		b.WriteString(in.PrevSummary + "\n")
	}
	for _, m := range in.Messages {
		fmt.Fprintf(&b, "- %s: %s…\n", m.Role, strings.TrimSpace(m.Text()[:min(12, len(m.Text()))]))
	}
	return thread.Summary{Text: strings.TrimSpace(b.String())}, nil
}

// WithSummarizer swaps text production — just the text: the cut, the
// serialization and the entry stay the session's.
func ExampleWithSummarizer() {
	ctx := context.Background()
	s := longSession(weft.New(wefttest.Script()), thread.WithSummarizer(bulletSummarizer{}))
	if err := s.Compact(ctx); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(s.Context()[0].Text())
	// Output:
	// <weft-summary>
	// - user: order order…
	// </weft-summary>
}
