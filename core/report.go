package core

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
)

// AttemptInfo is one provider attempt inside a model call, as the code
// that made it saw it: a retry middleware's try, a fallback's model, an
// adapter's request. Fields the reporter does not know stay zero.
type AttemptInfo struct {
	// Model is the model id the attempt requested (ModelInfo.Name of the
	// model it called).
	Model string
	// Provider is the provider that served it (ModelInfo.Provider).
	Provider string
	// Index is the attempt's 1-based number within the model call, as
	// counted by the reporter.
	Index int
	// Start and End bracket the attempt.
	Start, End time.Time
	// Err is the attempt's failure, nil when it succeeded.
	Err error
	// RetryAfter is the wait the provider asked for on a failed attempt
	// (a retry-after header), zero when it asked for none.
	RetryAfter time.Duration
}

// RawPair is one attempt's wire bodies: the request as sent and the
// response as received, as the reporter holds them. The bytes are
// content (the request carries the prompt), governed by the content
// policy wherever they are recorded.
type RawPair struct {
	Request   []byte
	Response  []byte
	MediaType string // e.g. "application/json"
}

// Reporter is the reporting path from the model chain into the loop's
// own record (ADR 0016): middleware and adapters inside the chain tell
// the run's observer about attempts and wire bodies, which the loop
// cannot see from outside the chain. It is reporting, not a seam (ADR
// 0006): no report alters a step, a retry, a tool call or a model
// choice, a report never returns an error and never blocks, and one
// the observer cannot write is dropped (to the agent's logger at Debug
// when it is enabled). The zero Reporter, and the one
// ReportFromContext returns outside a run's model call, discards every
// report; a Reporter is safe for concurrent use.
type Reporter struct {
	s *stepReport
}

// ReportFromContext returns the reporter of the model call whose
// context ctx is (or derives from): the loop puts one on the context it
// hands to the model chain, so a ModelMiddleware or a Model adapter
// reaches it from the ctx of its Stream. Outside a run's model call —
// a tool handler, a bare Model.Stream, any context the loop did not
// hand the chain — it is a no-op Reporter, never nil, so callers do not
// check. Using it is optional for adapters (ADR 0013).
func ReportFromContext(ctx context.Context) Reporter {
	if ctx == nil {
		return Reporter{}
	}
	s, _ := ctx.Value(reportKey{}).(*stepReport)
	return Reporter{s: s}
}

// Attempt reports one provider attempt of the current model call. With
// a tracer recording, it becomes an "attempt" child span of the step's
// chat span (provider, model, index, error.type or Ok, the retry-after
// ask); with the logger at Debug, a "model attempt" line.
func (r Reporter) Attempt(a AttemptInfo) {
	if r.s == nil {
		return
	}
	r.s.attempt(a)
}

// Raw reports one attempt's wire bodies. The observer receives them;
// what is recorded, and under which content policy, is not decided yet
// — today the bodies are dropped and nothing is written anywhere.
func (r Reporter) Raw(p RawPair) {
	if r.s == nil {
		return
	}
	r.s.raw(p)
}

type reportKey struct{}

// stepReport is the model call's reporter and its context in one value:
// the context the chain receives is this struct, so installing the
// reporter costs one allocation per step and no extra context layer.
type stepReport struct {
	context.Context
	obs   *observer
	runID string
	step  int
}

func (s *stepReport) Value(key any) any {
	if _, ok := key.(reportKey); ok {
		return s
	}
	return s.Context.Value(key)
}

// withReport wraps the model call's context (the chat span's) with its
// reporter.
func (o *observer) withReport(ctx context.Context, runID string, step int) context.Context {
	return &stepReport{Context: ctx, obs: o, runID: runID, step: step}
}

// The attempt span's weft.* attributes (ADR 0016's span table).
const (
	attrAttemptIndex        = attribute.Key("weft.attempt.index")
	attrAttemptRetryAfterMS = attribute.Key("weft.attempt.retry_after_ms")
	logAttempt              = "attempt"
	logRetryAfter           = "retry_after"
)

func (s *stepReport) attempt(a AttemptInfo) {
	ctx := s.Context
	if trace.SpanFromContext(ctx).IsRecording() {
		start := []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindClient)}
		if !a.Start.IsZero() {
			start = append(start, trace.WithTimestamp(a.Start))
		}
		_, span := s.obs.tracer.Start(ctx, "attempt", start...)
		attrs := []attribute.KeyValue{
			attrRunID.String(s.runID),
			attrStepIndex.Int(s.step),
			attrAttemptIndex.Int(a.Index),
		}
		if a.Provider != "" {
			attrs = append(attrs, semconv.GenAIProviderNameKey.String(providerName(a.Provider)))
		}
		if a.Model != "" {
			attrs = append(attrs, semconv.GenAIRequestModel(a.Model))
		}
		if a.RetryAfter > 0 {
			attrs = append(attrs, attrAttemptRetryAfterMS.Int64(a.RetryAfter.Milliseconds()))
		}
		attrs = append(attrs, metadataAttrs(ctx)...)
		span.SetAttributes(attrs...)
		if a.Err != nil {
			span.SetStatus(codes.Error, a.Err.Error())
			span.SetAttributes(semconv.ErrorTypeKey.String(runErrorType(a.Err)))
		} else {
			span.SetStatus(codes.Ok, "")
		}
		var end []trace.SpanEndOption
		if !a.End.IsZero() {
			end = append(end, trace.WithTimestamp(a.End))
		}
		span.End(end...)
	}
	if l := s.obs.logger(); l.Enabled(ctx, slog.LevelDebug) {
		attrs := []slog.Attr{
			slog.String(logRun, s.runID),
			slog.Int(logStep, s.step),
			slog.Int(logAttempt, a.Index),
		}
		if a.Provider != "" {
			attrs = append(attrs, slog.String(logProvider, a.Provider))
		}
		if a.Model != "" {
			attrs = append(attrs, slog.String(logModel, a.Model))
		}
		if !a.Start.IsZero() && !a.End.IsZero() {
			attrs = append(attrs, slog.Duration(logDur, a.End.Sub(a.Start)))
		}
		if a.RetryAfter > 0 {
			attrs = append(attrs, slog.Duration(logRetryAfter, a.RetryAfter))
		}
		if a.Err != nil {
			attrs = append(attrs, slog.String(logErr, a.Err.Error()))
		}
		l.LogAttrs(ctx, slog.LevelDebug, "model attempt", attrs...)
	}
}

// raw receives an attempt's wire bodies. Storage is not decided (the
// request record's ADR will); until then the report is dropped here, on
// purpose, and nothing is written.
func (s *stepReport) raw(RawPair) {}
