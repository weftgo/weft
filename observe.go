package weft

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
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
	version             = "v0.3.0"
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
	log    *slog.Logger
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
func (o *observer) run(ctx context.Context, runID, agent string, info ModelInfo) (context.Context, func(res *RunResult, err error)) {
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
