package core

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/embedded"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Provider containment (ADR 0016: observability never changes
// behaviour). Every call core makes into an OpenTelemetry provider on
// its own instrumentation scope — TracerProvider.Tracer and
// LoggerProvider.Logger at New; the tracer's Start and every span call
// (IsRecording, SetAttributes, SetStatus, RecordError, End); the
// logger's Enabled and Emit; the raw marker walk — runs under a recover.
// A panic there is dropped, counted with the tap panics (TapPanics, the
// run's weft.tap.panics) and named in one Warn line on the agent's
// Logger, once per run per provider kind, so a provider that panics on
// every call does not flood the log. The run continues exactly as it
// would with no provider: what failed was reporting, which decides
// nothing. User code — tool handlers, middleware, PrepareStep, taps and
// OnRunEnd observers — is not covered here; each keeps its own rule.

// The provider kinds a run warns about, as bits of recorder.warned.
const (
	providerTraces uint32 = 1 << iota
	providerLogs
)

func providerKindName(kind uint32) string {
	if kind == providerTraces {
		return "tracer_provider"
	}
	return "logger_provider"
}

// noopSpan is the span a contained Start hands back: non-recording, so
// every later call on it is a no-op.
var noopSpan = trace.SpanFromContext(context.Background())

// containTraces recovers a panic out of the tracer or one of its spans
// and reports it (providerPanicked). Deferred directly, so its recover
// sees the panic. Nil-safe: with no recorder (observer-only tests) the
// panic is dropped silently.
func (r *recorder) containTraces(ctx context.Context) {
	if p := recover(); p != nil {
		r.providerPanicked(ctx, providerTraces, p)
	}
}

// providerPanicked counts one contained provider panic and, the first
// time this run sees one of its kind, leaves the Warn line.
func (r *recorder) providerPanicked(ctx context.Context, kind uint32, p any) {
	if r == nil {
		return
	}
	r.countPanic()
	if r.warned.Or(kind)&kind != 0 {
		return
	}
	r.obs.warnProvider(ctx, r.runID, kind, p)
}

// warnProvider writes the one Warn line: the run, the provider kind and
// the panic's type — never its value, which may be built from the very
// attributes or body the call carried (content). A panicking log
// handler is not the run's problem either.
func (o *observer) warnProvider(ctx context.Context, runID string, kind uint32, p any) {
	if o == nil {
		return
	}
	defer func() { _ = recover() }()
	attrs := []slog.Attr{
		slog.String("provider", providerKindName(kind)),
		slog.String("panic", fmt.Sprintf("%T", p)),
	}
	if runID != "" {
		attrs = append([]slog.Attr{slog.String(logRun, runID)}, attrs...)
	}
	o.logger().LogAttrs(ctx, slog.LevelWarn,
		"observability provider panicked; contained, reporting dropped (later panics this run are counted, not logged)",
		attrs...)
}

// startSpan is o.tracer.Start, contained: a panic hands back the
// incoming context and a non-recording span, so the run goes on as if
// no tracer were set.
func (o *observer) startSpan(ctx context.Context, rec *recorder, name string, opts ...trace.SpanStartOption) (sctx context.Context, span trace.Span) {
	defer func() {
		if p := recover(); p != nil {
			rec.providerPanicked(ctx, providerTraces, p)
			sctx, span = ctx, noopSpan
		}
	}()
	return o.tracer.Start(ctx, name, opts...)
}

// endSpan is span.End, contained.
func (r *recorder) endSpan(ctx context.Context, span trace.Span, opts ...trace.SpanEndOption) {
	defer r.containTraces(ctx)
	span.End(opts...)
}

// containTraces and containLogs are the step reporter's: through the
// run's recorder when it has one, else counted on the agent's counter
// alone (observer-only tests).
func (s *stepReport) containTraces() {
	if p := recover(); p != nil {
		s.providerPanicked(providerTraces, p)
	}
}

func (s *stepReport) containLogs() {
	if p := recover(); p != nil {
		s.providerPanicked(providerLogs, p)
	}
}

func (s *stepReport) providerPanicked(kind uint32, p any) {
	if s.rec != nil {
		s.rec.providerPanicked(s.Context, kind, p)
	} else if s.panics != nil {
		s.panics.Add(1)
	}
}

// resolveTracer asks the provider for core's tracer at New, contained:
// a panic leaves the agent a no-op tracer, counted and named in one
// Warn line (there is no run yet to warn once per).
func (o *observer) resolveTracer(tp trace.TracerProvider, panics *atomic.Int64) (t trace.Tracer) {
	defer func() {
		if p := recover(); p != nil {
			panics.Add(1)
			o.warnProvider(context.Background(), "", providerTraces, p)
			t = noop.NewTracerProvider().Tracer(instrumentationName)
		}
	}()
	return tp.Tracer(instrumentationName, trace.WithInstrumentationVersion(version))
}

// resolveLogger is resolveTracer's twin for the record logger.
func (o *observer) resolveLogger(lp log.LoggerProvider, panics *atomic.Int64) (l log.Logger) {
	defer func() {
		if p := recover(); p != nil {
			panics.Add(1)
			o.warnProvider(context.Background(), "", providerLogs, p)
			l = noopLogger{}
		}
	}()
	return lp.Logger(instrumentationName, log.WithInstrumentationVersion(version))
}

// noopLogger records nothing and enables nothing: the logger an agent
// keeps when its LoggerProvider panicked handing one out.
type noopLogger struct{ embedded.Logger }

func (noopLogger) Emit(context.Context, log.Record)                    {}
func (noopLogger) Enabled(context.Context, log.EnabledParameters) bool { return false }
