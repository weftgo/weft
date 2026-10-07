package core_test

import (
	"context"
	"iter"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/mw"
	"github.com/weftgo/weft/core/wefttest"
)

// An Attempt reported from inside the model chain becomes an "attempt"
// span, child of the step's chat span, carrying provider, model, index,
// the retry-after ask and the outcome (Ok, or Error + error.type).
func TestReportAttemptSpanUnderChat(t *testing.T) {
	tp := newRecProvider()
	start := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	report := func(next core.Model) core.Model {
		return reportingFunc(func(ctx context.Context, req core.ModelRequest, yield func(core.ModelEvent, error) bool) {
			r := core.ReportFromContext(ctx)
			if r == (core.Reporter{}) {
				t.Error("no reporter on the model chain's context")
			}
			r.Attempt(core.AttemptInfo{Model: "gpt-x", Provider: "openai", Start: start,
				End: start.Add(time.Second), Err: core.ErrStreamIdle, RetryAfter: 1500 * time.Millisecond})
			for ev, err := range next.Stream(ctx, req) {
				if !yield(ev, err) {
					return
				}
			}
		})
	}
	agt := core.New(wefttest.Script(wefttest.Say("done")), core.Name("demo"),
		core.TracerProvider(tp), core.WrapModel(report))
	if _, err := agt.Generate(context.Background(), core.RunID("r"), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	chat, att := tp.find(t, "chat"), tp.find(t, "attempt")
	if att.parent.SpanID() != chat.sc.SpanID() {
		t.Errorf("attempt span parent = %v, want the chat span %v", att.parent.SpanID(), chat.sc.SpanID())
	}
	want := map[string]string{
		"weft.run.id":                 "r",
		"weft.step.index":             "0",
		"weft.attempt.index":          "1",
		"gen_ai.provider.name":        "openai",
		"gen_ai.request.model":        "gpt-x",
		"weft.attempt.retry_after_ms": "1500",
		"error.type":                  "stream_idle",
	}
	got := att.attrsMap()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("attempt attr %s = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("attempt attrs = %v, want exactly %v", got, want)
	}
	if ended, status, _, _ := att.state(); !ended || status != codes.Error {
		t.Errorf("attempt span ended=%v status=%v, want ended/Error", ended, status)
	}
	if !att.startTS.Equal(start) || !att.endTS.Equal(start.Add(time.Second)) {
		t.Errorf("attempt span times = %v..%v, want the reported %v..%v", att.startTS, att.endTS, start, start.Add(time.Second))
	}
}

type reportingFunc func(ctx context.Context, req core.ModelRequest, yield func(core.ModelEvent, error) bool)

func (f reportingFunc) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return func(yield func(core.ModelEvent, error) bool) { f(ctx, req, yield) }
}

// reportLines captures the observer's Debug lines by message.
type reportLines struct {
	mu    sync.Mutex
	lines []reportLine
}

type reportLine struct {
	msg   string
	attrs map[string]string
}

func (h *reportLines) Enabled(context.Context, slog.Level) bool { return true }
func (h *reportLines) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *reportLines) WithGroup(string) slog.Handler            { return h }
func (h *reportLines) Handle(_ context.Context, r slog.Record) error {
	m := map[string]string{}
	r.Attrs(func(a slog.Attr) bool { m[a.Key] = a.Value.String(); return true })
	h.mu.Lock()
	h.lines = append(h.lines, reportLine{r.Message, m})
	h.mu.Unlock()
	return nil
}

func (h *reportLines) named(msg string) []map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []map[string]string
	for _, l := range h.lines {
		if l.msg == msg {
			out = append(out, l.attrs)
		}
	}
	return out
}

// attemptSpans returns the recorded "attempt" spans in start order.
func attemptSpans(tp *recProvider) []*recSpan {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	var out []*recSpan
	for _, s := range tp.spans {
		if s.name == "attempt" {
			out = append(out, s)
		}
	}
	return out
}

// reportVia is a middleware that runs fn with the chain's context
// before streaming the inner model.
func reportVia(fn func(ctx context.Context)) core.ModelMiddleware {
	return func(next core.Model) core.Model {
		return reportingFunc(func(ctx context.Context, req core.ModelRequest, yield func(core.ModelEvent, error) bool) {
			fn(ctx)
			for ev, err := range next.Stream(ctx, req) {
				if !yield(ev, err) {
					return
				}
			}
		})
	}
}

// Times are both-or-neither: a zero Start (or an End before Start)
// drops both, so no span is negative and the line carries no dur. Raw
// is accepted and discarded, with only its sizes on a Debug line.
func TestReportUntimedAttemptAndRawLine(t *testing.T) {
	tp, h := newRecProvider(), &reportLines{}
	end := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	agt := core.New(wefttest.Script(wefttest.Say("done")), core.TracerProvider(tp), core.Logger(slog.New(h)),
		core.WrapModel(reportVia(func(ctx context.Context) {
			r := core.ReportFromContext(ctx)
			r.Attempt(core.AttemptInfo{Model: "m", End: end})
			r.Attempt(core.AttemptInfo{Model: "m", Start: end, End: end.Add(-time.Second)})
			r.Raw(core.RawPair{Request: []byte("ab"), Response: []byte("cde"), MediaType: "application/json"})
		})))
	if _, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	for _, s := range attemptSpans(tp) {
		if !s.startTS.IsZero() || !s.endTS.IsZero() {
			t.Errorf("untimed attempt span carries times %v..%v", s.startTS, s.endTS)
		}
	}
	for _, l := range h.named("model attempt") {
		if _, ok := l["dur"]; ok {
			t.Errorf("untimed attempt line carries dur: %v", l)
		}
	}
	raw := h.named("model raw dropped")
	if len(raw) != 1 || raw[0]["request_bytes"] != "2" || raw[0]["response_bytes"] != "3" || raw[0]["media_type"] != "application/json" {
		t.Errorf("raw lines = %v, want one with the sizes", raw)
	}
}

// mw.Retry's attempts are spans under the step's chat span even across
// the backoff sleep between them, numbered 1..n, each with its outcome.
func TestRetryAttemptSpansUnderChatAcrossBackoff(t *testing.T) {
	tp := newRecProvider()
	model := wefttest.Script(wefttest.Fail(core.ErrStreamIdle), wefttest.Say("ok"))
	agt := core.New(model, core.TracerProvider(tp),
		core.WrapModel(mw.Retry(mw.BaseDelay(10*time.Millisecond), mw.MaxWait(time.Second))))
	if _, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	chat := tp.find(t, "chat script")
	atts := attemptSpans(tp)
	if len(atts) != 2 {
		t.Fatalf("attempt spans = %d, want 2", len(atts))
	}
	for i, a := range atts {
		got := a.attrsMap()
		if a.parent.SpanID() != chat.sc.SpanID() {
			t.Errorf("attempt %d parent = %v, want chat", i+1, a.parent.SpanID())
		}
		if got["weft.attempt.index"] != []string{"1", "2"}[i] {
			t.Errorf("attempt %d index = %s", i+1, got["weft.attempt.index"])
		}
	}
	if _, status, _, _ := atts[0].state(); status != codes.Error || atts[0].attrsMap()["error.type"] != "stream_idle" {
		t.Errorf("first attempt status=%v attrs=%v, want Error/stream_idle", status, atts[0].attrsMap())
	}
	if _, status, _, _ := atts[1].state(); status != codes.Ok {
		t.Errorf("second attempt status = %v, want Ok", status)
	}
	if gap := atts[1].startTS.Sub(atts[0].endTS); gap < 5*time.Millisecond {
		t.Errorf("gap between attempts = %v, want the backoff sleep", gap)
	}
}

// Reports from parallel goroutines are safe (run under -race) and each
// gets its own number: the reporter numbers attempts, not the callers.
func TestReportAttemptConcurrent(t *testing.T) {
	const n = 16
	tp := newRecProvider()
	agt := core.New(wefttest.Script(wefttest.Say("done")), core.TracerProvider(tp),
		core.Logger(slog.New(&reportLines{})),
		core.WrapModel(reportVia(func(ctx context.Context) {
			r := core.ReportFromContext(ctx)
			var wg sync.WaitGroup
			for range n {
				wg.Go(func() { r.Attempt(core.AttemptInfo{Model: "m"}) })
			}
			wg.Wait()
		})))
	if _, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	var got []int
	for _, s := range attemptSpans(tp) {
		i, err := strconv.Atoi(s.attrsMap()["weft.attempt.index"])
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, i)
	}
	sort.Ints(got)
	if len(got) != n {
		t.Fatalf("attempt spans = %d, want %d", len(got), n)
	}
	for i, idx := range got {
		if idx != i+1 {
			t.Fatalf("indexes = %v, want 1..%d each once", got, n)
		}
	}
}

// A report made after its model call ended is dropped: no span, no
// line, nothing against a step that has moved on.
func TestReportAfterModelCallIsDropped(t *testing.T) {
	tp, h := newRecProvider(), &reportLines{}
	var kept core.Reporter
	agt := core.New(wefttest.Script(wefttest.Say("done")), core.TracerProvider(tp), core.Logger(slog.New(h)),
		core.WrapModel(reportVia(func(ctx context.Context) { kept = core.ReportFromContext(ctx) })))
	if _, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	if kept == (core.Reporter{}) {
		t.Fatal("no reporter inside the chain")
	}
	kept.Attempt(core.AttemptInfo{Model: "late"})
	kept.Raw(core.RawPair{Request: []byte("x")})
	if n := len(attemptSpans(tp)); n != 0 {
		t.Errorf("late report made %d attempt spans, want 0", n)
	}
	if n := len(h.named("model attempt")) + len(h.named("model raw dropped")); n != 0 {
		t.Errorf("late report wrote %d lines, want 0", n)
	}
}

// A run started on a model chain's context — a middleware running a
// child agent — does not inherit the parent's reporter: the child's
// tool handlers see none, and its own model chain sees its own.
func TestReportMaskedInNestedRun(t *testing.T) {
	var parent, childChain, childTool core.Reporter
	probe := core.Tool("probe", "Probe.", func(ctx context.Context, _ struct{}) (string, error) {
		childTool = core.ReportFromContext(ctx)
		return "ok", nil
	})
	child := core.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "probe", Args: `{}`}), wefttest.Say("child done")),
		probe, core.WrapModel(reportVia(func(ctx context.Context) { childChain = core.ReportFromContext(ctx) })))
	agt := core.New(wefttest.Script(wefttest.Say("done")),
		core.WrapModel(reportVia(func(ctx context.Context) {
			parent = core.ReportFromContext(ctx)
			if _, err := child.Generate(ctx, core.Prompt("y")); err != nil {
				t.Error(err)
			}
		})))
	if _, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	if parent == (core.Reporter{}) || childChain == (core.Reporter{}) {
		t.Fatal("a model chain saw no reporter")
	}
	if childChain == parent {
		t.Error("the child's model chain reports into the parent's step")
	}
	if childTool != (core.Reporter{}) {
		t.Error("the child's tool handler sees a reporter")
	}
}
