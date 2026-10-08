package core

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
)

// AttemptInfo is one provider request inside a model call, as the code
// that made it saw it: a retry middleware's try, a fallback's model, an
// adapter's request. Fields the reporter does not know stay zero. The
// attempt's number is not the caller's to give: the reporter numbers
// the model call's attempts 1, 2, … in the order they are reported, so
// numbers stay unique however many layers report.
type AttemptInfo struct {
	// Model is the model id the attempt requested (ModelInfo.Name of the
	// model it called).
	Model string
	// Provider is the provider that served it (ModelInfo.Provider).
	Provider string
	// Start and End bracket the attempt. Both or neither: with Start
	// zero, or End before Start, the times are ignored and the record
	// carries none.
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
// choice. A report never returns an error, never blocks and never
// panics into the caller — a panicking tracer or log handler is
// contained and counted in Agent.TapPanics, the report dropped. A
// report made after its model call ended is dropped, best-effort: a
// goroutine the chain left behind that reports while the call is
// ending may still land one attempt under the ended chat span. The
// zero Reporter, and the one ReportFromContext returns outside a run's
// model call, discards every report; a Reporter is safe for concurrent
// use.
//
// Layers that report must not double-report one provider request. A
// model or middleware that reports its own attempts says so with an
// optional method, ReportsAttempts() bool, returning true; a reporting
// layer above it (mw.Retry, mw.Fallback) walks the Unwrap chain, finds
// the marker and stays silent. A layer that unwraps to a self-reporting
// model but may not stream through it (a router) returns false, which
// ends the walk. The marker is a convention, not a core type (ADR 0013).
type Reporter struct {
	s *stepReport
}

// ReportFromContext returns the reporter of the model call whose
// context ctx is (or derives from): the loop puts one on the context it
// hands to the model chain, so a ModelMiddleware or a Model adapter
// reaches it from the ctx of its Stream. Outside a run's model call —
// a tool handler, a bare Model.Stream, a run started on the chain's
// context (it is masked at run start), any context the loop did not
// hand the chain — it is a no-op Reporter, never nil, so callers do not
// check. Using it is optional for adapters (ADR 0013).
func ReportFromContext(ctx context.Context) Reporter {
	if ctx == nil {
		return Reporter{}
	}
	s, _ := ctx.Value(reportKey{}).(*stepReport)
	return Reporter{s: s}
}

// Attempt reports one provider request of the current model call. With
// a tracer recording, it becomes an "attempt" child span of the step's
// chat span (provider, model, the reporter's attempt number, error.type
// or Ok, the retry-after ask); with the logger at Debug, a "model
// attempt" line.
func (r Reporter) Attempt(a AttemptInfo) {
	if r.s == nil {
		return
	}
	r.s.attempt(a)
}

// Raw reports one attempt's wire bodies. They are accepted and
// discarded: what is recorded, and under which content policy, is a
// later ADR's decision (the request record). Until then nothing is
// written anywhere; with the logger at Debug a "model raw dropped" line
// names the sizes, never the bytes.
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
	obs    *observer
	runID  string
	step   int
	panics *atomic.Int64 // the agent's TapPanics counter; nil in observer-only tests
	n      atomic.Int64  // attempts reported so far: the next number is n+1
	ended  atomic.Bool   // the model call returned: later reports are dropped
	// request is the call's prepared request record (ADR 0028 §7): every
	// attempt reported after the first re-emits it with that attempt's
	// number. Nil when no destination wants request records. Set before
	// the chain runs, never after.
	request *requestRecord
	// answered is the model of the last attempt reported as a success
	// (AttemptInfo.Model with a nil Err): the model that answered after
	// a retry or a fallback, read by the loop once the call has ended
	// (answeredModel). Guarded by mu — reports may race each other —
	// and touched only on the reporting path, so a call nothing reports
	// on never locks.
	mu       sync.Mutex
	answered string

	// start and ttft are the call's clock (ADR 0016's A4 note), kept
	// here because this value is already allocated per step: the loop
	// sets start before the chain's Stream and ttft at the first
	// TextDelta or ToolArgsDelta (firstDelta). Loop goroutine only.
	start time.Time
	ttft  time.Duration
}

// firstDelta records the time to the first token once: the first
// TextDelta or ToolArgsDelta of the call. Later calls are no-ops.
func (s *stepReport) firstDelta() {
	if s.ttft == 0 {
		s.ttft = sinceAtLeast(s.start)
	}
}

// answeredModel returns the model the last successful reported attempt
// requested, "" when no attempt reported a success.
func (s *stepReport) answeredModel() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.answered
}

func (s *stepReport) Value(key any) any {
	if _, ok := key.(reportKey); ok {
		return s
	}
	return s.Context.Value(key)
}

// end closes the reporter: the model call is over, and a report that
// arrives later (a goroutine the chain left behind) is dropped rather
// than recorded against a step that has moved on.
//
// The flag is stored under mu so that it orders with answered and the
// request record: an attempt that names the answering model, or emits a
// further attempt's record, checks ended inside the same lock, so once
// end returns no late report can change answeredModel or add a record.
func (s *stepReport) end() {
	s.mu.Lock()
	s.ended.Store(true)
	s.mu.Unlock()
}

// contain recovers a panic out of the tracer or the logger and counts
// it, as safeTap does for a tap: a broken observer must not break the
// run, nor be invisible.
func (s *stepReport) contain() {
	if recover() != nil && s.panics != nil {
		s.panics.Add(1)
	}
}

// withReport wraps the model call's context (the chat span's) with its
// reporter. panics is the agent's TapPanics counter.
func (o *observer) withReport(ctx context.Context, runID string, step int, panics *atomic.Int64) *stepReport {
	return &stepReport{Context: ctx, obs: o, runID: runID, step: step, panics: panics}
}

// reportMask hides an enclosing model call's reporter from a run started
// on the chain's context (a middleware or handler running a child
// agent): the child's tool handlers must not report into the parent's
// step. Its own model calls install their own reporter beneath it.
type reportMask struct{ context.Context }

func (m reportMask) Value(key any) any {
	if _, ok := key.(reportKey); ok {
		return nil
	}
	return m.Context.Value(key)
}

// maskReport applies reportMask only when ctx carries a reporter — the
// nested case — so an ordinary run pays nothing.
func maskReport(ctx context.Context) context.Context {
	if _, ok := ctx.Value(reportKey{}).(*stepReport); ok {
		return reportMask{ctx}
	}
	return ctx
}

// The attempt span's weft.* attributes (ADR 0016's span table).
const (
	attrAttemptIndex        = attribute.Key("weft.attempt.index")
	attrAttemptRetryAfterMS = attribute.Key("weft.attempt.retry_after_ms")
	logAttempt              = "attempt"
	logRetryAfter           = "retry_after"
	logMediaType            = "media_type"
	logRequestBytes         = "request_bytes"
	logResponseBytes        = "response_bytes"
)

func (s *stepReport) attempt(a AttemptInfo) {
	if s.ended.Load() {
		return
	}
	defer s.contain()
	index := s.n.Add(1)
	if a.Err == nil && a.Model != "" {
		s.mu.Lock()
		if !s.ended.Load() { // re-checked under the lock end takes
			s.answered = a.Model
		}
		s.mu.Unlock()
	}
	timed := !a.Start.IsZero() && !a.End.Before(a.Start)
	ctx := s.Context
	if trace.SpanFromContext(ctx).IsRecording() {
		start := []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindClient)}
		if timed {
			start = append(start, trace.WithTimestamp(a.Start))
		}
		_, span := s.obs.tracer.Start(ctx, "attempt", start...)
		attrs := []attribute.KeyValue{
			attrRunID.String(s.runID),
			attrStepIndex.Int(s.step),
			attrAttemptIndex.Int64(index),
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
			// The attempt answered: the model it requested is the one
			// that produced the response (the attempt knows no other
			// name), the same value the chat span and the step_finish
			// record carry as gen_ai.response.model.
			span.SetStatus(codes.Ok, "")
			if a.Model != "" {
				span.SetAttributes(semconv.GenAIResponseModel(a.Model))
			}
		}
		if timed {
			span.End(trace.WithTimestamp(a.End))
		} else {
			span.End()
		}
	}
	if l := s.obs.logger(); l.Enabled(ctx, slog.LevelDebug) {
		attrs := []slog.Attr{
			slog.String(logRun, s.runID),
			slog.Int(logStep, s.step),
			slog.Int64(logAttempt, index),
		}
		if a.Provider != "" {
			attrs = append(attrs, slog.String(logProvider, a.Provider))
		}
		if a.Model != "" {
			attrs = append(attrs, slog.String(logModel, a.Model))
		}
		if timed {
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
	// Attempt 1's request record was emitted by the loop before the
	// chain ran; each further reported attempt adds its own.
	if index > 1 && s.request != nil {
		s.requestAttempt(ctx, index, a)
	}
}

// requestAttempt emits a further attempt's request record under mu,
// re-checking ended there: end takes the same lock, so a report racing
// the call's end either lands its record before end returns or adds
// none (ADR 0028 §7).
func (s *stepReport) requestAttempt(ctx context.Context, index int64, a AttemptInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ended.Load() {
		s.request.attempt(ctx, index, a)
	}
}

// raw receives an attempt's wire bodies. Storage is not decided (the
// request record's ADR will); until then the report is dropped here, on
// purpose, and only its sizes reach a Debug line.
func (s *stepReport) raw(p RawPair) {
	if s.ended.Load() {
		return
	}
	defer s.contain()
	if l := s.obs.logger(); l.Enabled(s.Context, slog.LevelDebug) {
		l.LogAttrs(s.Context, slog.LevelDebug, "model raw dropped",
			slog.String(logRun, s.runID),
			slog.Int(logStep, s.step),
			slog.String(logMediaType, p.MediaType),
			slog.Int(logRequestBytes, len(p.Request)),
			slog.Int(logResponseBytes, len(p.Response)))
	}
}

// sinceAtLeast is time.Since(t), never below one nanosecond: a measured
// interval must not read as "not measured" (0) on a clock too coarse to
// see it pass.
func sinceAtLeast(t time.Time) time.Duration {
	return max(time.Since(t), time.Nanosecond)
}
