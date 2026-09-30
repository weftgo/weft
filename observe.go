package weft

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
)

// The loop's own reporting: one OTel span and at most one slog line at
// each of the phases every run passes through — run, model call, tool
// call (ADR 0016). It is not a seam: it takes no user function, sees no
// request or result content (error text alone reaches the log lines and
// the spans' recorded exceptions), changes nothing, and runs on the
// calling goroutine of each phase. Spans and lines are two outputs of the same
// calls, so their ids, names, durations and outcomes cannot disagree.
//
// Why not a Tap: a tap cannot put a span on the tool handler's context
// (that context is built later, on the tool goroutine), never sees the
// end of a cancelled run (nothing is delivered after cancellation), and
// would need per-run and per-call start times kept in maps that leak for
// exactly those cancelled runs. The loop knows all three; it reports.
// Tap remains the *user's* observer, unchanged.

// instrumentationName names weft's tracer; version rides on it as the
// instrumentation version (ADR 0005's release process owns the value).
const (
	instrumentationName = "github.com/weftgo/weft"
	version             = "v0.3.6"
)

// The weft.* span attributes and the slog line keys, pinned by tests and
// recorded with their semantics in ADR 0016's tables. Changing one is an
// ADR amendment, like model-visible bytes; the gen_ai.* keys beside them
// are the semconv/v1.41.0 constants — the newest semconv package that
// carries the GenAI group, itself recorded in ADR 0016.
const (
	attrRunID           = attribute.Key("weft.run.id")
	attrRunSteps        = attribute.Key("weft.run.steps")
	attrRunPending      = attribute.Key("weft.run.pending")
	attrRunStopReason   = attribute.Key("weft.run.stop_reason")
	attrStepIndex       = attribute.Key("weft.step.index")
	attrStopRaw         = attribute.Key("weft.stop.raw")
	attrModelToolCalls  = attribute.Key("weft.model.tool_calls")
	attrToolSeq         = attribute.Key("weft.tool.seq")
	attrToolApproved    = attribute.Key("weft.tool.approved")
	attrToolPending     = attribute.Key("weft.tool.pending")
	attrToolResultBytes = attribute.Key("weft.tool.result_bytes")
)

// The record attributes and event names (ADR 0024's record contract),
// pinned by tests the same way. weft.record mirrors the EventName for
// backends that drop it; the two positions are the two counters [D3].
const (
	attrRecord          = attribute.Key("weft.record")
	attrEventType       = attribute.Key("weft.event.type")
	attrEventPos        = attribute.Key("weft.event.pos")
	attrDeltaPos        = attribute.Key("weft.delta.pos")
	attrMessagesIndex   = attribute.Key("weft.messages.index")
	attrMessagesCount   = attribute.Key("weft.messages.count")
	attrMessagesInput   = attribute.Key("weft.messages.input")
	attrContent         = attribute.Key("weft.content")
	attrParentRunID     = attribute.Key("weft.parent.run.id")
	attrParentCallID    = attribute.Key("weft.parent.call.id")
	attrManifestHash    = attribute.Key("weft.manifest.hash")
	attrVersion         = attribute.Key("weft.version")
	attrMetadataDropped = attribute.Key("weft.metadata.dropped")

	eventNameEvent    = "weft.event"
	eventNameDelta    = "weft.delta"
	eventNameMessages = "weft.messages"

	contentFull = "full"
	contentNone = "none"
)

// Log-line keys (ADR 0016's log table). A log is the caller's, unlike a
// span: the error lines carry the error's text.
const (
	logRun          = "run"
	logSteps        = "steps"
	logStep         = "step"
	logCall         = "call"
	logTool         = "tool"
	logAgent        = "agent"
	logProvider     = "provider"
	logModel        = "model"
	logReason       = "reason"
	logRaw          = "raw"
	logInputTokens  = "input_tokens"
	logOutputTokens = "output_tokens"
	logToolCalls    = "tool_calls"
	logStop         = "stop"
	logPending      = "pending"
	logDur          = "dur"
	logErr          = "err"
	logResultBytes  = "result_bytes"
)

// providerNames maps ModelInfo.Provider onto the semconv well-known
// gen_ai.provider.name values; every other value passes through
// verbatim — "wefttest", a third-party adapter's own name. The map is
// the whole mechanism: adapters do not learn about OTel (ADR 0016).
var providerNames = map[string]string{
	"openai":    "openai",
	"anthropic": "anthropic",
	"google":    "gcp.gemini",
}

func providerName(p string) string {
	if mapped, ok := providerNames[p]; ok {
		return mapped
	}
	return p
}

// observer is built once at New: a tracer (the global provider's, which
// is a no-op until an SDK registers, or the TracerProvider option's) and
// a logger (nil: slog.Default, resolved at log time so a program that
// sets its default after building agents is honoured). Nothing else:
// no goroutine, no lock, no state between calls.
type observer struct {
	tracer trace.Tracer
	// elog is the Logs API logger the run's records go through: the
	// LoggerProvider option's, or the global one, which delegates (an
	// SDK registered after New is still picked up) and answers Enabled
	// false until one does — so a program with no SDK pays nothing.
	elog log.Logger
	log  *slog.Logger
}

// logger resolves the destination once per line: the option's logger
// when one was given, slog.Default otherwise — at log time, not at New,
// because the common program order builds agents before configuring
// logging.
func (o *observer) logger() *slog.Logger {
	if o.log != nil {
		return o.log
	}
	return slog.Default()
}

// spanName renders "<operation> <name>" — the GenAI conventions' shape
// ("invoke_agent planner", "chat gpt-5", "execute_tool lookup") — from
// the semconv operation constant, falling back to the bare operation
// when there is nothing to name.
func spanName(op attribute.KeyValue, name string) string {
	if name == "" {
		return op.Value.AsString()
	}
	return op.Value.AsString() + " " + name
}

// usageSplits renders Usage's three reporting subsets in their
// semconv/v1.41.0 names, each only when non-zero (ADR 0016's
// 2026-09-22 amendment). Span-only: the slog lines keep carrying the
// two totals, per the line-stability rule.
func usageSplits(u Usage) []attribute.KeyValue {
	var attrs []attribute.KeyValue
	if u.CachedInputTokens > 0 {
		attrs = append(attrs, semconv.GenAIUsageCacheReadInputTokens(int(u.CachedInputTokens)))
	}
	if u.CacheWriteTokens > 0 {
		attrs = append(attrs, semconv.GenAIUsageCacheCreationInputTokens(int(u.CacheWriteTokens)))
	}
	if u.ReasoningTokens > 0 {
		attrs = append(attrs, semconv.GenAIUsageReasoningOutputTokens(int(u.ReasoningTokens)))
	}
	return attrs
}

// run brackets one execute: the span starts before RunStart is emitted
// and the returned end function is called with the outcome execute
// decided — the result on success, the *RunError on failure — including
// cancellation, which no event reports but the loop knows at fail. The
// returned context carries the span, so every chat and execute_tool
// below, and every span a tool handler or child run starts, parents
// under it. A child run's tree hangs under its delegating tool span
// exactly as Nested events hang inside ToolStart..ToolFinish.
//
// extra carries the run-level attributes only the caller knows: the
// subagent linkage (weft.parent.run.id / weft.parent.call.id), the
// manifest hash, weft.version, weft.metadata.dropped when the limits
// dropped pairs. The metadata in force on ctx lands on every span of
// the run — this one and the model and tool spans below — verbatim,
// with the semconv mirrors (metadataAttrs).
func (o *observer) run(ctx context.Context, runID, agent string, info ModelInfo, extra []attribute.KeyValue) (context.Context, func(res *RunResult, err error)) {
	ctx, span := o.tracer.Start(ctx, spanName(semconv.GenAIOperationNameInvokeAgent, agent),
		trace.WithSpanKind(trace.SpanKindInternal))
	start := time.Now()
	if span.IsRecording() {
		attrs := []attribute.KeyValue{semconv.GenAIOperationNameInvokeAgent, attrRunID.String(runID)}
		if agent != "" {
			attrs = append(attrs, semconv.GenAIAgentName(agent))
		}
		if info.Provider != "" {
			attrs = append(attrs, semconv.GenAIProviderNameKey.String(providerName(info.Provider)))
		}
		if info.Name != "" {
			attrs = append(attrs, semconv.GenAIRequestModel(info.Name))
		}
		attrs = append(attrs, extra...)
		attrs = append(attrs, metadataAttrs(ctx)...)
		span.SetAttributes(attrs...)
	}
	if l := o.logger(); l.Enabled(ctx, slog.LevelDebug) {
		attrs := []slog.Attr{slog.String(logRun, runID)}
		if agent != "" {
			attrs = append(attrs, slog.String(logAgent, agent))
		}
		if info.Provider != "" {
			attrs = append(attrs, slog.String(logProvider, info.Provider))
		}
		if info.Name != "" {
			attrs = append(attrs, slog.String(logModel, info.Name))
		}
		l.LogAttrs(ctx, slog.LevelDebug, "run start", attrs...)
	}
	return ctx, func(res *RunResult, err error) {
		dur := time.Since(start)
		if span.IsRecording() {
			attrs := []attribute.KeyValue{
				semconv.GenAIUsageInputTokens(int(res.Usage.InputTokens)),
				semconv.GenAIUsageOutputTokens(int(res.Usage.OutputTokens)),
				attrRunSteps.Int(len(res.Steps)),
			}
			attrs = append(attrs, usageSplits(res.Usage)...)
			if n := len(res.Pending); n > 0 {
				attrs = append(attrs, attrRunPending.Int(n))
			}
			if res.StopReason != "" {
				attrs = append(attrs, attrRunStopReason.String(string(res.StopReason)))
			}
			span.SetAttributes(attrs...)
		}
		var l *slog.Logger
		debug := false
		if l = o.logger(); l.Enabled(ctx, slog.LevelDebug) {
			debug = true
		}
		if err != nil {
			// err is the *RunError fail built; its cause decides the
			// type, the span records the failure itself.
			if span.IsRecording() {
				span.SetStatus(codes.Error, err.Error())
				span.RecordError(err)
				span.SetAttributes(semconv.ErrorTypeKey.String(runErrorType(err)))
			}
			if debug {
				// The line names the cause — the ctx error for a
				// cancelled run — with the step; the full RunError text
				// is the caller's return value, not the log's job.
				step := -1
				errText := err.Error()
				var re *RunError
				if errors.As(err, &re) {
					step = re.Step
					if re.Err != nil {
						errText = re.Err.Error()
					}
				}
				l.LogAttrs(ctx, slog.LevelDebug, "run error",
					slog.String(logRun, runID),
					slog.Int(logStep, step),
					slog.String(logErr, errText),
					slog.Duration(logDur, dur))
			}
		} else {
			if span.IsRecording() {
				span.SetStatus(codes.Ok, "")
			}
			if debug {
				attrs := []slog.Attr{
					slog.String(logRun, runID),
					slog.Int(logSteps, len(res.Steps)),
					slog.Int64(logInputTokens, res.Usage.InputTokens),
					slog.Int64(logOutputTokens, res.Usage.OutputTokens),
					slog.Duration(logDur, dur),
				}
				if res.StopReason != "" {
					attrs = append(attrs, slog.String(logStop, string(res.StopReason)))
				}
				if n := len(res.Pending); n > 0 {
					attrs = append(attrs, slog.Int(logPending, n))
				}
				l.LogAttrs(ctx, slog.LevelDebug, "run finish", attrs...)
			}
		}
		span.End()
	}
}

// model brackets one model call: the span starts after PrepareStep has
// built the request — a PrepareStep function rewrites the request, it is
// not the model call — and before the model chain runs, and ends when
// the stream is consumed. The middleware chain (mw.Log, mw.Retry,
// mw.Fallback) runs inside it: one span per step measures the chain's
// outcome, exactly as mw.Log placed outermost reports one line per step.
// The returned context carries the span, so an adapter's own HTTP spans
// parent under chat. ModelInfo is the agent's — the model that was
// asked; the model that answered after a fallback is not observable from
// the loop and is not invented (ADR 0016).
func (o *observer) model(ctx context.Context, runID string, step int, info ModelInfo) (context.Context, func(finish ModelFinish, finished bool, calls int, err error)) {
	ctx, span := o.tracer.Start(ctx, spanName(semconv.GenAIOperationNameChat, info.Name),
		trace.WithSpanKind(trace.SpanKindClient))
	start := time.Now()
	if span.IsRecording() {
		attrs := []attribute.KeyValue{
			semconv.GenAIOperationNameChat,
			attrRunID.String(runID),
			attrStepIndex.Int(step),
		}
		if info.Provider != "" {
			attrs = append(attrs, semconv.GenAIProviderNameKey.String(providerName(info.Provider)))
		}
		if info.Name != "" {
			attrs = append(attrs, semconv.GenAIRequestModel(info.Name))
		}
		attrs = append(attrs, metadataAttrs(ctx)...)
		span.SetAttributes(attrs...)
	}
	return ctx, func(finish ModelFinish, finished bool, calls int, err error) {
		dur := time.Since(start)
		// finished is the loop's own flag: a ModelFinish arrived. It is
		// not inferred from the reason — the loop accepts an empty one —
		// so a stream that failed before finishing carries no usage and
		// no finish reason; the error status is the whole story.
		if span.IsRecording() {
			if finished {
				attrs := []attribute.KeyValue{
					semconv.GenAIUsageInputTokens(int(finish.Usage.InputTokens)),
					semconv.GenAIUsageOutputTokens(int(finish.Usage.OutputTokens)),
					semconv.GenAIResponseFinishReasons(string(finish.Reason)),
					attrModelToolCalls.Int(calls),
				}
				attrs = append(attrs, usageSplits(finish.Usage)...)
				if finish.Raw != "" {
					attrs = append(attrs, attrStopRaw.String(finish.Raw))
				}
				span.SetAttributes(attrs...)
			}
			if err != nil {
				span.SetStatus(codes.Error, err.Error())
				span.RecordError(err)
				span.SetAttributes(semconv.ErrorTypeKey.String(runErrorType(err)))
			} else {
				span.SetStatus(codes.Ok, "")
			}
		}
		if l := o.logger(); l.Enabled(ctx, slog.LevelDebug) {
			attrs := []slog.Attr{
				slog.String(logRun, runID),
				slog.Int(logStep, step),
				slog.Duration(logDur, dur),
			}
			if info.Provider != "" {
				attrs = append(attrs, slog.String(logProvider, info.Provider))
			}
			if info.Name != "" {
				attrs = append(attrs, slog.String(logModel, info.Name))
			}
			// Mirrors the span: the finish keys when a ModelFinish
			// arrived, err when the chain failed — both when a contract
			// violation followed the finish.
			if finished {
				attrs = append(attrs,
					slog.String(logReason, string(finish.Reason)),
					slog.Int64(logInputTokens, finish.Usage.InputTokens),
					slog.Int64(logOutputTokens, finish.Usage.OutputTokens),
					slog.Int(logToolCalls, calls))
				if finish.Raw != "" {
					attrs = append(attrs, slog.String(logRaw, finish.Raw))
				}
			}
			if err != nil {
				attrs = append(attrs, slog.String(logErr, err.Error()))
			}
			l.LogAttrs(ctx, slog.LevelDebug, "model call", attrs...)
		}
		span.End()
	}
}

// tool brackets one executed tool call: the span starts on the call's
// context before the chain runs — so it is the parent of every span the
// handler starts, an HTTP client's, a database driver's, a child run's
// invoke_agent — and ends when callTool returns, before ToolFinish is
// emitted. c is the Call the loop placed on the context; seq is the
// ToolStart's Seq. A parked (approval) call ends with weft.tool.pending
// set and no error status: the call did not fail, it was not made. An
// error result sets Error status with error.type only — never the result
// text, which is model-visible content (ADR 0016). A timed-out call ends
// when the call does: the span reports the call, which is over, not the
// abandoned handler goroutine.
func (o *observer) tool(ctx context.Context, c Call, seq int64) (context.Context, func(res ToolResultPart, pending bool, err error)) {
	ctx, span := o.tracer.Start(ctx, spanName(semconv.GenAIOperationNameExecuteTool, c.Name),
		trace.WithSpanKind(trace.SpanKindInternal))
	start := time.Now()
	if span.IsRecording() {
		attrs := []attribute.KeyValue{
			semconv.GenAIOperationNameExecuteTool,
			semconv.GenAIToolName(c.Name),
			semconv.GenAIToolCallID(c.CallID),
			attrRunID.String(c.RunID),
			attrStepIndex.Int(c.Step),
			attrToolSeq.Int64(seq),
		}
		if c.Approved {
			attrs = append(attrs, attrToolApproved.Bool(true))
		}
		attrs = append(attrs, metadataAttrs(ctx)...)
		span.SetAttributes(attrs...)
	}
	return ctx, func(res ToolResultPart, pending bool, err error) {
		dur := time.Since(start)
		if span.IsRecording() {
			switch {
			case pending:
				span.SetAttributes(attrToolPending.Bool(true))
			case err != nil:
				span.SetAttributes(semconv.ErrorTypeKey.String(toolErrorType(err)))
				span.SetStatus(codes.Error, "")
			default:
				span.SetAttributes(attrToolResultBytes.Int(len(res.Content)))
				span.SetStatus(codes.Ok, "")
			}
		}
		if l := o.logger(); l.Enabled(ctx, slog.LevelDebug) {
			attrs := []slog.Attr{
				slog.String(logRun, c.RunID),
				slog.Int(logStep, c.Step),
				slog.String(logCall, c.CallID),
				slog.String(logTool, c.Name),
				slog.Duration(logDur, dur),
			}
			switch {
			case pending:
				attrs = append(attrs, slog.Bool(logPending, true))
			case res.IsError:
				attrs = append(attrs, slog.String(logErr, res.Content))
			default:
				attrs = append(attrs, slog.Int(logResultBytes, len(res.Content)))
			}
			l.LogAttrs(ctx, slog.LevelDebug, "tool call", attrs...)
		}
		span.End()
	}
}

// errRunPanicked types the run span's outcome when a panic nothing
// contains — a PrepareStep function is arbitrary user code — unwinds
// execute: the guard there ends the span with it and re-panics, so the
// crash still reaches the caller and the span still ends (ADR 0016).
var errRunPanicked = errors.New("weft: run panicked")

// errorTypes maps weft's run-level sentinels onto their error.type
// tokens (ADR 0016): low-cardinality identifiers backends group on,
// never the sentinel's text. A sentinel missing here reports as
// "model_error"; TestSpansErrorTypes pins the table.
var errorTypes = []struct {
	err  error
	name string
}{
	{ErrMaxSteps, "max_steps"},
	{ErrUsageLimit, "usage_limit"},
	{ErrModelContract, "model_contract"},
	{ErrLoopDetected, "loop_detected"},
	{ErrModelRetriesExceeded, "model_retries_exceeded"},
	{ErrDuplicateTool, "duplicate_tool"},
	{ErrNilTool, "nil_tool"},
	{ErrNoOutput, "no_output"},
	{ErrStreamIdle, "stream_idle"},
	{ErrUnsupported, "unsupported"},
	{ErrModelRequestsDenied, "model_requests_denied"},
	{errRunPanicked, "run_panicked"}, // the loop's own, not a public sentinel
}

// runErrorType classifies a run or model error for error.type: the
// context error's name for cancellation, the errorTypes token for
// weft's named failures, and "model_error" for a provider error, whose
// text rides the recorded exception instead. Never the wrapping
// error's text (ADR 0016).
func runErrorType(err error) string {
	if name, ok := ctxErrorType(err); ok {
		return name
	}
	for _, e := range errorTypes {
		if errors.Is(err, e.err) {
			return e.name
		}
	}
	return "model_error"
}

// ctxErrorType names a cancellation or deadline error, the one
// classification the run and tool spans share.
func ctxErrorType(err error) (string, bool) {
	switch {
	case errors.Is(err, context.Canceled):
		return "context.Canceled", true
	case errors.Is(err, context.DeadlineExceeded):
		return "context.DeadlineExceeded", true
	}
	return "", false
}

// toolErrorType classifies a tool error result for error.type: the
// ToolError's code when the chain produced one, "timeout" for a
// timed-out call (the typed error), the context error's name when the
// run's cancellation reached the handler — the same name the run span
// reports — and "tool_error" otherwise. The result text never becomes
// a span attribute (ADR 0016).
func toolErrorType(err error) string {
	var te *ToolError
	if errors.As(err, &te) && te.Code != "" {
		return te.Code
	}
	var timeout *toolTimeoutError
	if errors.As(err, &timeout) {
		return "timeout"
	}
	if name, ok := ctxErrorType(err); ok {
		return name
	}
	return "tool_error"
}

// recorder emits the run's OTel log records (ADR 0024): every durable
// event and every delta through deliver — the one place each passes
// exactly once — and the transcript's batches at their growth points
// (recordMessages). Two counters [D3]: weft.event.pos numbers the seven
// durable types, weft.delta.pos the three deltas, so dropping deltas
// never opens a hole in the durable sequence; Nested takes neither (a
// child run numbers its own). Records go out on the run's context, so
// the SDK derives their trace/span ids from the invoke_agent span, and
// nothing is emitted after that context ends (deliver checks first).
//
// Cost rule: Enabled is asked before anything is marshalled, so a
// program with no SDK pays no JSON encoding; the no-SDK logger answers
// false.
type recorder struct {
	elog log.Logger
	// capture is the Content option's override; nil = as the logger in
	// force says, resolved at each emission (agents are usually built
	// before the pipeline installs; the global provider delegates).
	capture *bool
	runID   string
	agent   string
	// manifestHash is sha256(Manifest(a)) computed at New for a named
	// agent; "" when unnamed. Reported on the run_start record and the
	// invoke_agent span only.
	manifestHash string

	eventPos    atomic.Int64
	deltaPos    atomic.Int64
	messagesIdx atomic.Int64
}

// captureOn resolves the content question for one emission: the agent's
// Content option when set; else whether any destination wants message
// content, through the one standard Enabled question the pipeline's
// processors answer per destination (S1.1 [D2]).
func (r *recorder) captureOn(ctx context.Context) bool {
	if r.capture != nil {
		return *r.capture
	}
	return r.elog.Enabled(ctx, log.EnabledParameters{EventName: eventNameMessages})
}

// recordEvent reports one event from deliver, after the taps and before
// the sink. Nested is not reported: the child run emitted (or dropped)
// its own records, and recording the parent's wrapper copy would store
// every child event twice (ADR 0024 D3).
func (r *recorder) recordEvent(ctx context.Context, ev Event) {
	var kind, eventName string
	switch ev.(type) {
	case RunStart, StepStart, ToolStart, ToolFinish, StepFinish, Steered, RunFinish:
		kind, eventName = "event", eventNameEvent
	case TextDelta, ReasoningDelta, ToolArgsDelta:
		kind, eventName = "delta", eventNameDelta
	default:
		return
	}
	if !r.elog.Enabled(ctx, log.EnabledParameters{EventName: eventName}) {
		return
	}

	var rec log.Record
	rec.SetTimestamp(time.Now())
	rec.SetEventName(eventName)
	rec.SetSeverity(log.SeverityInfo)
	if tf, ok := ev.(ToolFinish); ok && tf.IsError {
		rec.SetSeverity(log.SeverityWarn)
	}
	body := ev
	content := contentFull
	if !r.captureOn(ctx) {
		body, content = StripContent(ev), contentNone
	}
	b, err := json.Marshal(body)
	if err != nil {
		return // an event that cannot encode is dropped, never fatal
	}
	rec.SetBody(attribute.StringValue(string(b)))

	attrs := []attribute.KeyValue{
		attrRecord.String(kind),
		attrRunID.String(r.runID),
		attrEventType.String(eventDiscriminator(ev)),
		attrContent.String(content),
	}
	if r.agent != "" {
		attrs = append(attrs, semconv.GenAIAgentName(r.agent))
	}
	if kind == "delta" {
		attrs = append(attrs, attrDeltaPos.Int64(r.deltaPos.Add(1)-1))
	} else {
		attrs = append(attrs, attrEventPos.Int64(r.eventPos.Add(1)-1))
	}
	switch e := ev.(type) {
	case StepStart:
		attrs = append(attrs, attrStepIndex.Int(e.Index))
	case StepFinish:
		attrs = append(attrs, attrStepIndex.Int(e.Index))
	case Steered:
		attrs = append(attrs, attrStepIndex.Int(e.Step))
	case ToolStart:
		attrs = append(attrs, attrToolSeq.Int64(e.Seq), semconv.GenAIToolCallID(e.CallID), semconv.GenAIToolName(e.Name))
	case ToolFinish:
		attrs = append(attrs, attrToolSeq.Int64(e.Seq), semconv.GenAIToolCallID(e.CallID), semconv.GenAIToolName(e.Name))
	case RunStart:
		// The linkage a child run reports: the parent's ids from the call
		// its context carries (CallFromContext is the parent's tool call
		// the child executes under). Absent on a top-level run.
		if c, ok := CallFromContext(ctx); ok {
			attrs = append(attrs, attrParentRunID.String(c.RunID), attrParentCallID.String(c.CallID))
		}
		if r.manifestHash != "" {
			attrs = append(attrs, attrManifestHash.String(r.manifestHash))
		}
		attrs = append(attrs, attrVersion.String(version))
	}
	attrs = append(attrs, metadataAttrs(ctx)...)
	rec.AddAttributes(attrs...)
	r.elog.Emit(ctx, rec)
}

// recordMessages reports one batch of messages joining the run's
// transcript — the five growth points (S1.3 [D1]): the repaired input at
// run start, the tool message a resume creates or rebuilds, each
// assistant message, each tool message, each steered batch. Emitted only
// when capture is on: pure content, unlike the event records, which keep
// their stripped shape. step is the step the batch belongs to (0 for the
// input); input marks index 0. The body is a JSON array of Message,
// never capped — a capped transcript is not replay-grade (ADR 0024 D1).
func (r *recorder) recordMessages(ctx context.Context, step int, msgs []Message, input bool) {
	if len(msgs) == 0 || !r.captureOn(ctx) {
		return
	}
	if !r.elog.Enabled(ctx, log.EnabledParameters{EventName: eventNameMessages}) {
		return
	}
	b, err := json.Marshal(msgs)
	if err != nil {
		return
	}

	var rec log.Record
	rec.SetTimestamp(time.Now())
	rec.SetEventName(eventNameMessages)
	rec.SetSeverity(log.SeverityInfo)
	rec.SetBody(attribute.StringValue(string(b)))
	attrs := []attribute.KeyValue{
		attrRecord.String("messages"),
		attrRunID.String(r.runID),
		attrContent.String(contentFull),
		attrStepIndex.Int(step),
		attrMessagesIndex.Int64(r.messagesIdx.Add(1) - 1),
		attrMessagesCount.Int(len(msgs)),
	}
	if input {
		attrs = append(attrs, attrMessagesInput.Bool(true))
	}
	if r.agent != "" {
		attrs = append(attrs, semconv.GenAIAgentName(r.agent))
	}
	attrs = append(attrs, metadataAttrs(ctx)...)
	rec.AddAttributes(attrs...)
	r.elog.Emit(ctx, rec)
}

// metadataAttrs renders the metadata in force on ctx as span/record
// attributes: every key verbatim, sorted, plus the semconv mirrors
// backends group on — weft.session.id as gen_ai.conversation.id and
// session.id (Logfire reads the first, Langfuse and Phoenix the second),
// enduser.id as user.id (ADR 0024's identity chain).
func metadataAttrs(ctx context.Context) []attribute.KeyValue {
	md := metadataFromCtx(ctx)
	if len(md) == 0 {
		return nil
	}
	attrs := make([]attribute.KeyValue, 0, len(md)+3)
	for _, k := range slices.Sorted(maps.Keys(md)) {
		attrs = append(attrs, attribute.String(k, md[k]))
	}
	if v := md["weft.session.id"]; v != "" {
		attrs = append(attrs,
			semconv.GenAIConversationIDKey.String(v),
			semconv.SessionIDKey.String(v))
	}
	if v := md["enduser.id"]; v != "" {
		attrs = append(attrs, semconv.UserIDKey.String(v))
	}
	return attrs
}

// eventDiscriminator returns ev's wire "type" (ADR 0004) — the value
// weft.event.type reports.
func eventDiscriminator(ev Event) string {
	switch ev.(type) {
	case RunStart:
		return eventRunStart
	case StepStart:
		return eventStepStart
	case TextDelta:
		return eventTextDelta
	case ReasoningDelta:
		return eventReasoningDelta
	case ToolArgsDelta:
		return eventToolArgsDelta
	case ToolStart:
		return eventToolStart
	case ToolFinish:
		return eventToolFinish
	case StepFinish:
		return eventStepFinish
	case Steered:
		return eventSteered
	case RunFinish:
		return eventRunFinish
	case Nested:
		return eventNested
	default:
		return ""
	}
}
