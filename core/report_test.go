package core_test

import (
	"context"
	"iter"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"

	"github.com/weftgo/weft/core"
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
			r.Attempt(core.AttemptInfo{Model: "gpt-x", Provider: "openai", Index: 1, Start: start,
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
}

type reportingFunc func(ctx context.Context, req core.ModelRequest, yield func(core.ModelEvent, error) bool)

func (f reportingFunc) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return func(yield func(core.ModelEvent, error) bool) { f(ctx, req, yield) }
}
