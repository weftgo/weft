package core

// The observer's no-SDK cost, pinned: this file is internal because it
// builds the observer directly. It imports no wefttest — an internal
// test file cannot (wefttest imports weft) — and needs no model: the
// observer's three start/end pairs are exercised as the loop calls
// them.

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// S7: with no SDK registered, a span of each kind — start and end —
// costs fewer than ten allocations; the no-op path is the default path
// for every user. The logger is a real handler at Info level, so the
// Debug gate is closed exactly as in a default program.
func TestObserverNoopAllocations(t *testing.T) {
	o := newNoopObserver()
	ctx := context.Background()
	res := &RunResult{}
	finish := ModelFinish{Reason: StopEndTurn}
	out := ToolResultPart{Content: "x"}
	if n := testing.AllocsPerRun(100, func() {
		_, end := o.run(ctx, "r", "", ModelInfo{}, nil)
		end(res, nil)
	}); n >= 10 {
		t.Errorf("run span allocs = %.0f, want < 10", n)
	}
	if n := testing.AllocsPerRun(100, func() {
		_, end := o.model(ctx, "r", 0, ModelInfo{})
		end(finish, true, 0, nil)
	}); n >= 10 {
		t.Errorf("model span allocs = %.0f, want < 10", n)
	}
	if n := testing.AllocsPerRun(100, func() {
		_, end := o.tool(ctx, Call{RunID: "r"}, 1)
		end(out, false, nil)
	}); n >= 10 {
		t.Errorf("tool span allocs = %.0f, want < 10", n)
	}
}

// BenchmarkObserverNoop reports the no-SDK, no-Debug cost of one span of
// each kind — the numbers ADR 0016 records.
func BenchmarkObserverNoop(b *testing.B) {
	o := newNoopObserver()
	ctx := context.Background()
	res := &RunResult{}
	finish := ModelFinish{Reason: StopEndTurn}
	out := ToolResultPart{Content: "x"}
	bench := func(name string, call func()) {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				call()
			}
		})
	}
	bench("run", func() {
		_, end := o.run(ctx, "r", "", ModelInfo{}, nil)
		end(res, nil)
	})
	bench("model", func() {
		_, end := o.model(ctx, "r", 0, ModelInfo{})
		end(finish, true, 0, nil)
	})
	bench("tool", func() {
		_, end := o.tool(ctx, Call{RunID: "r"}, 1)
		end(out, false, nil)
	})
}

// newNoopObserver is the observer as a default program gets it: the
// global tracer (a no-op until an SDK registers) and the Debug gate
// closed.
func newNoopObserver() observer {
	return observer{
		tracer: otel.GetTracerProvider().Tracer(instrumentationName, trace.WithInstrumentationVersion(version)),
		log:    slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
}

// L1: with the Debug gate closed, Enabled is the whole cost — no record
// is built and the handler's Handle is never reached. The failing
// handler proves it structurally; TestObserverNoopAllocations bounds it.
func TestLoggerNoAllocWhenDisabled(t *testing.T) {
	h := &gateHandler{t: t}
	o := observer{
		tracer: otel.GetTracerProvider().Tracer(instrumentationName),
		log:    slog.New(h),
	}
	ctx := context.Background()
	_, endRun := o.run(ctx, "r", "a", ModelInfo{Provider: "p", Name: "m"}, nil)
	_, endModel := o.model(ctx, "r", 0, ModelInfo{Provider: "p", Name: "m"})
	_, endTool := o.tool(ctx, Call{RunID: "r", Name: "t"}, 1)
	endTool(ToolResultPart{Content: "x"}, false, nil)
	endModel(ModelFinish{Reason: StopEndTurn}, true, 0, nil)
	endRun(&RunResult{}, nil)
	if h.enabled == 0 {
		t.Fatal("Enabled was never consulted")
	}
}

// gateHandler answers Enabled with false and fails the test on Handle.
type gateHandler struct {
	t       *testing.T
	enabled int
}

func (h *gateHandler) Enabled(context.Context, slog.Level) bool { h.enabled++; return false }
func (h *gateHandler) Handle(context.Context, slog.Record) error {
	h.t.Fatal("Handle reached with the Debug gate closed")
	return nil
}
func (h *gateHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *gateHandler) WithGroup(string) slog.Handler      { return h }

// The reporting hook's cost under a stub observer (no SDK, Debug gate
// closed) — the default program's configuration: an Attempt report on
// the model call's context allocates nothing, and neither does one
// outside a run.
func TestReportAttemptNoopAllocations(t *testing.T) {
	o := newNoopObserver()
	mctx, end := o.model(context.Background(), "r", 0, ModelInfo{})
	defer end(ModelFinish{}, true, 0, nil)
	ctx := o.withReport(mctx, "r", 0, nil)
	a := AttemptInfo{Model: "m", Provider: "p", Start: time.Now(), End: time.Now()}
	if n := testing.AllocsPerRun(100, func() { ReportFromContext(ctx).Attempt(a) }); n != 0 {
		t.Errorf("Attempt under a stub observer allocs = %.0f, want 0", n)
	}
	if n := testing.AllocsPerRun(100, func() { ReportFromContext(context.Background()).Attempt(a) }); n != 0 {
		t.Errorf("Attempt outside a run allocs = %.0f, want 0", n)
	}
}

// BenchmarkReportAttempt reports what the hook costs a middleware per
// attempt: outside a run (the no-op reporter), and inside a model call
// under a stub observer (no SDK registered, logger at Info).
func BenchmarkReportAttempt(b *testing.B) {
	o := newNoopObserver()
	mctx, end := o.model(context.Background(), "r", 0, ModelInfo{})
	defer end(ModelFinish{}, true, 0, nil)
	inRun := o.withReport(mctx, "r", 0, nil)
	a := AttemptInfo{Model: "m", Provider: "p", Start: time.Now(), End: time.Now()}
	for _, c := range []struct {
		name string
		ctx  context.Context
	}{{"outside_run", context.Background()}, {"stub_observer", inRun}} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				ReportFromContext(c.ctx).Attempt(a)
			}
		})
	}
}
