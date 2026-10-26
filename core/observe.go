package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"sync"
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
// The value is the version the NEXT root tag will carry: version_test
// pins it against the newest v* tag reachable from HEAD or the
// CHANGELOG's unreleased heading, whichever speaks, so it cannot go
// stale again (it sat at v0.3.6 through the v0.5.0 release once).
const (
	instrumentationName = "github.com/weftgo/weft/core"
	version             = "v0.19.0"
)

// The weft.* span attributes and the slog line keys, pinned by tests and
// recorded with their semantics in ADR 0016's tables. Changing one is an
// ADR amendment, like model-visible bytes; the gen_ai.* keys beside them
// are the semconv/v1.41.0 constants — the newest semconv package that
// carries the GenAI group, itself recorded in ADR 0016.
const (
	attrRunID          = attribute.Key("weft.run.id")
	attrRunSteps       = attribute.Key("weft.run.steps")
	attrRunPending     = attribute.Key("weft.run.pending")
	attrRunStopReason  = attribute.Key("weft.run.stop_reason")
	attrRunStoppedBy   = attribute.Key("weft.run.stopped_by")
	attrRunEvents      = attribute.Key("weft.run.events")
	attrRunMessages    = attribute.Key("weft.run.messages")
	attrCancelCause    = attribute.Key("weft.cancel.cause")
	attrStepIndex      = attribute.Key("weft.step.index")
	attrStopRaw        = attribute.Key("weft.stop.raw")
	attrModelToolCalls = attribute.Key("weft.model.tool_calls")
	attrToolSeq        = attribute.Key("weft.tool.seq")
	attrToolApproved   = attribute.Key("weft.tool.approved")
	// An approved call's provenance (ApproveBy; UX-B-14): the decider's
	// class and the grant that decided.
	attrToolApprovedBy = attribute.Key("weft.tool.approved_by")
	attrGrantID        = attribute.Key("weft.grant.id")
	attrToolPending    = attribute.Key("weft.tool.pending")
	// The execution facts of a call (UX-S2-06, UX-H-18, UX-S3-15): the
	// deadline it ran under, how long it queued, why it parked, and a
	// Subagent child's parked calls.
	attrToolPendingSource = attribute.Key("weft.tool.pending.source")
	attrToolTimeoutMS     = attribute.Key("weft.tool.timeout_ms")
	attrToolQueueMS       = attribute.Key("weft.tool.queue_ms")
	attrSubagentPending   = attribute.Key("weft.subagent.pending")
	// An Output agent's run end (UX-S1-06).
	attrOutputSubmitted = attribute.Key("weft.output.submitted")
	attrToolResultBytes = attribute.Key("weft.tool.result_bytes")
	attrToolTruncated   = attribute.Key("weft.tool.truncated_bytes")
	attrToolInputError  = attribute.Key("weft.tool.input_error")
	attrToolRetry       = attribute.Key("weft.tool.retry")
	attrToolRetryBudget = attribute.Key("weft.tool.retry_budget")
	attrTapPanics       = attribute.Key("weft.tap.panics")
	attrPanicLayer      = attribute.Key("weft.panic.layer")
	attrRunDenied       = attribute.Key("weft.run.denied")
	// The approval link (UX-E-17, ADR 0016's 2026-10-13 amendment): the
	// resuming run's decisions per parked call and the run it resumes
	// (ResumeOf), on its invoke_agent span and run_start record; each
	// denied call's zero-length execute_tool span.
	attrResumeCalls    = attribute.Key("weft.resume.calls")
	attrResumedFrom    = attribute.Key("weft.resumed_from")
	attrToolDenied     = attribute.Key("weft.tool.denied")
	attrToolDeniedKind = attribute.Key("weft.tool.denied_kind")
	attrStream         = attribute.Key("weft.stream")
	attrTTFTMS         = attribute.Key("weft.ttft_ms")
	attrLatencyMS      = attribute.Key("weft.latency_ms")
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
	attrCallerRunID     = attribute.Key("weft.caller.run.id")
	attrCallerCallID    = attribute.Key("weft.caller.call.id")
	attrManifestHash    = attribute.Key("weft.manifest.hash")
	attrVersion         = attribute.Key("weft.version")
	attrMetadataDropped = attribute.Key("weft.metadata.dropped")

	eventNameEvent    = "weft.event"
	eventNameDelta    = "weft.delta"
	eventNameMessages = "weft.messages"

	contentFull = "full"
	contentNone = "none"
	// contentStripped marks a request record emitted with capture off
	// (ADR 0028 §6): the same mark weft/otel's content-off chains set.
	contentStripped = "stripped"
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
	// logErrBytes stands in for logErr on a tool call's line under the
	// agent's Content(false): an error result is model-visible content,
	// and a bridged line (otel.Slog) would store it on a content-on
	// destination regardless of the agent's choice.
	logErrBytes = "err_bytes"
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
	// lp is the provider elog came from, and globalLP says it is the
	// global one (no LoggerProvider option): the raw record's marker
	// question (rawMarked, raw.go) is asked of it.
	lp       log.LoggerProvider
	globalLP bool
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

// runLinkAttrs is the linkage of a run started on ctx (O11, record
// contract §2): a core Subagent's child — the call Subagent marked
// (subagentOf) — names its parent, weft.parent.run.id /
// weft.parent.call.id, the run whose usage includes its own (rule 13);
// any other run started on a tool call's context — a thread/pool child
// session's turn, another agent's Generate inside a handler — names the
// call it was started from, weft.caller.run.id / weft.caller.call.id: a
// link, never parenthood (its usage is its own). Nil outside a tool
// call, and nil when the call on ctx is the one the enclosing run was
// itself started on (ownCallOf): a run started from inside that run's
// model chain, PrepareStep, taps or OnRunEnd was started from no call
// that run made — a judge under a Subagent's child carries no link,
// exactly as one under a top-level run.
func runLinkAttrs(ctx context.Context) []attribute.KeyValue {
	c, ok := CallFromContext(ctx)
	if !ok || ownCallOf(ctx, c) {
		return nil
	}
	if subagentOf(ctx, c) {
		return []attribute.KeyValue{attrParentRunID.String(c.RunID), attrParentCallID.String(c.CallID)}
	}
	return []attribute.KeyValue{attrCallerRunID.String(c.RunID), attrCallerCallID.String(c.CallID)}
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
// subagent linkage (weft.parent.run.id / weft.parent.call.id) or the
// caller link (weft.caller.run.id / weft.caller.call.id), the
// manifest hash, weft.version, weft.metadata.dropped when the limits
// dropped pairs. The metadata in force on ctx lands on every span of
// the run — this one and the model and tool spans below — verbatim,
// with the semconv mirrors (metadataAttrs).
//
// rec is the run's recorder (nil in observer-only tests): at the end the
// span reports its two counters as weft.run.events and
// weft.run.messages — the durable event records and the growth messages
// records the run handed to its LoggerProvider. Both count only records
// actually emitted (a record is numbered after Enabled said yes), so a
// run with no logger, or one whose provider enabled nothing, reports 0:
// a reader tells "no logger" (0) from "emitted M, stored fewer" (a
// destination lost them) by them. Counts, never content (ADR 0016).
func (o *observer) run(ctx context.Context, runID, agent string, info ModelInfo, extra []attribute.KeyValue, rec *recorder) (context.Context, func(res *RunResult, err error, end runEnd)) {
	ctx, span := o.startSpan(ctx, rec, spanName(semconv.GenAIOperationNameInvokeAgent, agent),
		trace.WithSpanKind(trace.SpanKindInternal))
	start := time.Now()
	func() {
		defer rec.containTraces(ctx)
		if !span.IsRecording() {
			return
		}
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
	}()
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
	return ctx, func(res *RunResult, err error, end runEnd) {
		dur := time.Since(start)
		recording := rec.spanRecording(ctx, span)
		func() {
			defer rec.containTraces(ctx)
			if !recording {
				return
			}
			attrs := []attribute.KeyValue{
				semconv.GenAIUsageInputTokens(int(res.Usage.InputTokens)),
				semconv.GenAIUsageOutputTokens(int(res.Usage.OutputTokens)),
				attrRunSteps.Int(len(res.Steps)),
			}
			if rec != nil {
				attrs = append(attrs,
					attrRunEvents.Int64(rec.eventPos.Load()),
					attrRunMessages.Int64(rec.growthCount.Load()))
			}
			attrs = append(attrs, usageSplits(res.Usage)...)
			if n := len(res.Pending); n > 0 {
				attrs = append(attrs, attrRunPending.Int(n))
			}
			if rec != nil {
				if n := rec.denied.Load(); n > 0 {
					attrs = append(attrs, attrRunDenied.Int64(n))
				}
				if n := rec.runPanics.Load(); n > 0 {
					attrs = append(attrs, attrTapPanics.Int64(n))
				}
			}
			if res.StopReason != "" {
				attrs = append(attrs, attrRunStopReason.String(string(res.StopReason)))
			}
			if end.stoppedBy != "" {
				attrs = append(attrs, attrRunStoppedBy.String(end.stoppedBy))
			}
			if end.cancelCause != "" {
				attrs = append(attrs, attrCancelCause.String(end.cancelCause))
			}
			if end.output {
				attrs = append(attrs, attrOutputSubmitted.Bool(end.submitted))
			}
			span.SetAttributes(attrs...)
		}()
		var l *slog.Logger
		debug := false
		if l = o.logger(); l.Enabled(ctx, slog.LevelDebug) {
			debug = true
		}
		if err != nil {
			// err is the *RunError fail built; its cause decides the
			// type, the span records the failure itself. The error's
			// own methods run outside the provider containment: a panic
			// in them is the error's, not the provider's.
			if recording {
				msg, typ := err.Error(), runErrorType(err)
				func() {
					defer rec.containTraces(ctx)
					span.SetStatus(codes.Error, msg)
					span.RecordError(err)
					span.SetAttributes(semconv.ErrorTypeKey.String(typ))
				}()
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
			if recording {
				func() {
					defer rec.containTraces(ctx)
					span.SetStatus(codes.Ok, "")
				}()
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
		rec.endSpan(ctx, span)
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
// asked (gen_ai.request.model). The model that answered is the end
// function's: callTiming.answered, from the chain's reported attempts
// when one reported a success, else the asked model (ADR 0016's
// 2026-10-07 A4 note); it is set only on a call that finished. The
// span carries weft.stream=true — the loop always consumes a stream —
// and weft.ttft_ms when a TextDelta or ToolArgsDelta arrived.
func (o *observer) model(ctx context.Context, rec *recorder, runID string, step int, info ModelInfo) (context.Context, func(finish ModelFinish, finished bool, calls int, err error, t callTiming)) {
	ctx, span := o.startSpan(ctx, rec, spanName(semconv.GenAIOperationNameChat, info.Name),
		trace.WithSpanKind(trace.SpanKindClient))
	start := time.Now()
	func() {
		defer rec.containTraces(ctx)
		if !span.IsRecording() {
			return
		}
		attrs := []attribute.KeyValue{
			semconv.GenAIOperationNameChat,
			attrRunID.String(runID),
			attrStepIndex.Int(step),
			attrStream.Bool(true),
		}
		if info.Provider != "" {
			attrs = append(attrs, semconv.GenAIProviderNameKey.String(providerName(info.Provider)))
		}
		if info.Name != "" {
			attrs = append(attrs, semconv.GenAIRequestModel(info.Name))
		}
		attrs = append(attrs, metadataAttrs(ctx)...)
		span.SetAttributes(attrs...)
	}()
	return ctx, func(finish ModelFinish, finished bool, calls int, err error, t callTiming) {
		dur := time.Since(start)
		// finished is the loop's own flag: a ModelFinish arrived. It is
		// not inferred from the reason — the loop accepts an empty one —
		// so a stream that failed before finishing carries no usage and
		// no finish reason; the error status is the whole story. The
		// error's own methods run before the provider containment (a
		// panic in them is the error's, not the provider's).
		recording := rec.spanRecording(ctx, span)
		var errMsg, errType string
		if recording && err != nil {
			errMsg, errType = err.Error(), runErrorType(err)
		}
		func() {
			defer rec.containTraces(ctx)
			if !recording {
				return
			}
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
				if t.answered != "" {
					attrs = append(attrs, semconv.GenAIResponseModel(t.answered))
				}
				span.SetAttributes(attrs...)
			}
			if t.ttft > 0 {
				span.SetAttributes(attrTTFTMS.Int64(ceilMS(t.ttft)))
			}
			if err != nil {
				span.SetStatus(codes.Error, errMsg)
				span.RecordError(err)
				span.SetAttributes(semconv.ErrorTypeKey.String(errType))
			} else {
				span.SetStatus(codes.Ok, "")
			}
		}()
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
		rec.endSpan(ctx, span)
	}
}

// callTiming is what the loop measured of one model call as it
// consumed the stream, handed to the chat span's end and to the
// step_finish record: ttft is the time to the first TextDelta or
// ToolArgsDelta (0 when none arrived), latency the call's whole wall
// time, answered the model that answered (see observer.model). A value,
// so measuring costs no allocation.
type callTiming struct {
	ttft, latency time.Duration
	answered      string
}

// ceilMS converts a measured duration to whole milliseconds rounded up,
// so an interval that was measured is never reported as 0 — the value
// that means "not measured" on StepFinish.TTFTMS. A duration <= 0 is 0.
func ceilMS(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return int64((d + time.Millisecond - 1) / time.Millisecond)
}

// tool brackets one executed tool call: the span starts on the call's
// context before the chain runs — so it is the parent of every span the
// handler starts, an HTTP client's, a database driver's, a child run's
// invoke_agent — and ends when callTool returns, before ToolFinish is
// emitted; a counted RETRY's span alone ends after its batch, when its
// retry mark is known, stamped with the moment the call returned (end),
// so its duration is still the call's; its Debug "tool call" log line,
// written by the same func, is therefore logged after the batch too (its
// duration still the call's). A zero end is now. c is the Call the loop placed on the context; seq is the
// ToolStart's Seq. A parked (approval) call ends with weft.tool.pending
// set and no error status: the call did not fail, it was not made. An
// error result sets Error status with error.type only — never the result
// text, which is model-visible content (ADR 0016). A timed-out call ends
// when the call does: the span reports the call, which is over, not the
// abandoned handler goroutine. cut is the bytes the result cap removed
// (weft.tool.truncated_bytes, on a result or an error result alike); a
// panic names its layer (weft.panic.layer) and an INVALID_INPUT result
// the rule it broke (weft.tool.input_error).
// toolStartFacts is what the loop knows of a call as it starts: the
// deadline in force (0: none) and the time it queued for its turn (0:
// it started at once).
type toolStartFacts struct {
	timeout time.Duration
	queue   time.Duration
	// contentOff is the agent's Content(false): the Debug line names an
	// error result's size (err_bytes), never its text.
	contentOff bool
	// approvedBy and grantID are an approved call's provenance
	// (ApproveBy), "" when the decision named none.
	approvedBy, grantID string
}

func (o *observer) tool(ctx context.Context, rec *recorder, c Call, seq int64, facts toolStartFacts) (context.Context, func(res ToolResultPart, park string, fail toolFailure, cut int, retry retryMark, end time.Time)) {
	ctx, span := o.startSpan(ctx, rec, spanName(semconv.GenAIOperationNameExecuteTool, c.Name),
		trace.WithSpanKind(trace.SpanKindInternal))
	start := time.Now()
	func() {
		defer rec.containTraces(ctx)
		if !span.IsRecording() {
			return
		}
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
			if facts.approvedBy != "" {
				attrs = append(attrs, attrToolApprovedBy.String(facts.approvedBy))
			}
			if facts.grantID != "" {
				attrs = append(attrs, attrGrantID.String(facts.grantID))
			}
		}
		if facts.timeout > 0 {
			attrs = append(attrs, attrToolTimeoutMS.Int64(ceilMS(facts.timeout)))
		}
		if facts.queue > 0 {
			attrs = append(attrs, attrToolQueueMS.Int64(ceilMS(facts.queue)))
		}
		attrs = append(attrs, metadataAttrs(ctx)...)
		span.SetAttributes(attrs...)
	}()
	return ctx, func(res ToolResultPart, park string, fail toolFailure, cut int, retry retryMark, end time.Time) {
		pending := park != ""
		if end.IsZero() {
			end = time.Now()
		}
		dur := end.Sub(start)
		recording := rec.spanRecording(ctx, span)
		// The error's facts were read by the loop, contained with the
		// call (settleToolError): its methods (ErrorType, As, Is) are
		// the tool's, and a panic in them is the tool's failure — the
		// facts are the contained panic's — never the provider's.
		errType, panicLayer, inputKind := fail.errType, fail.panicLayer, fail.inputKind
		err := fail.err
		func() {
			defer rec.containTraces(ctx)
			if !recording {
				return
			}
			switch {
			case pending:
				span.SetAttributes(attrToolPending.Bool(true), attrToolPendingSource.String(park))
			case err != nil:
				span.SetAttributes(semconv.ErrorTypeKey.String(errType))
				if names := nestFromContext(ctx).pendingNames(); names != "" {
					span.SetAttributes(attrSubagentPending.String(names))
				}
				if panicLayer != "" {
					span.SetAttributes(attrPanicLayer.String(panicLayer))
				}
				if inputKind != "" {
					span.SetAttributes(attrToolInputError.String(inputKind))
				}
				if retry.n > 0 {
					span.SetAttributes(attrToolRetry.Int(retry.n), attrToolRetryBudget.Int(retry.budget))
				}
				span.SetStatus(codes.Error, "")
			default:
				span.SetAttributes(attrToolResultBytes.Int(len(res.Content)))
				span.SetStatus(codes.Ok, "")
			}
			if cut > 0 && !pending {
				span.SetAttributes(attrToolTruncated.Int(cut))
			}
		}()
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
			case res.IsError && facts.contentOff:
				attrs = append(attrs, slog.Int(logErrBytes, len(res.Content)))
			case res.IsError:
				attrs = append(attrs, slog.String(logErr, res.Content))
			default:
				attrs = append(attrs, slog.Int(logResultBytes, len(res.Content)))
			}
			l.LogAttrs(ctx, slog.LevelDebug, "tool call", attrs...)
		}
		rec.endSpan(ctx, span, trace.WithTimestamp(end))
	}
}

// deniedTool reports one parked call a resume denied — an explicit
// Deny (kind "denied") or a call left undecided ("no_decision", the
// "DENIED: no decision" result rule 12 writes): a zero-length
// execute_tool span under the run's invoke_agent span, carrying the
// call's ids, weft.tool.denied and weft.tool.denied_kind, status Error
// with error.type DENIED — the result the model sees is an error. No
// seq (no ToolStart was emitted: the call never ran) and never the
// reason, which is the transcript's (ids and enums only on spans, ADR
// 0016). The invoke_agent span's weft.run.denied still counts these
// calls; a reader counts them there, not here (record contract §6).
func (o *observer) deniedTool(ctx context.Context, rec *recorder, runID string, call ToolCallPart, kind string) {
	now := time.Now()
	_, span := o.startSpan(ctx, rec, spanName(semconv.GenAIOperationNameExecuteTool, call.Name),
		trace.WithSpanKind(trace.SpanKindInternal), trace.WithTimestamp(now))
	// End is deferred first and contained on its own, so a span whose
	// attribute calls panic is still ended (and exported).
	defer rec.endSpan(ctx, span, trace.WithTimestamp(now))
	if !rec.spanRecording(ctx, span) {
		return
	}
	func() {
		defer rec.containTraces(ctx)
		attrs := []attribute.KeyValue{
			semconv.GenAIOperationNameExecuteTool,
			semconv.GenAIToolName(call.Name),
			semconv.GenAIToolCallID(call.ID),
			attrRunID.String(runID),
			attrStepIndex.Int(0),
			attrToolDenied.Bool(true),
			attrToolDeniedKind.String(kind),
			semconv.ErrorTypeKey.String(CodeDenied),
		}
		attrs = append(attrs, metadataAttrs(ctx)...)
		span.SetAttributes(attrs...)
		span.SetStatus(codes.Error, "")
	}()
}

// The denied span's kinds (deniedTool); its error.type is CodeDenied,
// mw.Allow's refusal's.
const (
	deniedKindDenied     = "denied"
	deniedKindNoDecision = "no_decision"
)

// runEnd is what the loop knows about a run's end beyond its result
// and error: the control that stopped it and, for a canceled run, the
// cancellation's typed cause — the invoke_agent span's
// weft.run.stopped_by and weft.cancel.cause (ADR 0016, amendment
// 2026-10-12). Either may be empty: a panicked run, or a failure no
// control decided (a model error), has no stopped_by.
type runEnd struct {
	stoppedBy   string
	cancelCause string
	// output says the agent was built with Output; submitted that the
	// run ended with a valid submission (weft.output.submitted).
	output, submitted bool
}

// The loop's own weft.run.stopped_by values — the exits no StopWhen
// condition names. A condition that fired reports its manifest name
// (stopName), so the attribute, RunFinish.StoppedBy and the manifest's
// policy.stop_when share one vocabulary.
const (
	stoppedFinalAnswer  = "final_answer"
	stoppedParked       = "parked"
	stoppedMaxSteps     = "max_steps"
	stoppedUsageLimit   = "usage_limit"
	stoppedLoopDetected = "loop_detected"
	stoppedRetries      = "model_retries_exceeded"
	stoppedCanceled     = "canceled"
)

// failEnd classifies a failed run's end: "canceled" (with the typed
// cause, when context.Cause names one) when the run's own context
// ended and the failure is that ending, the budget's name for a budget
// breach at the continuation point (rule 13), and nothing for any other
// failure — error.type names those.
func failEnd(ctx context.Context, err error) runEnd {
	if ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return runEnd{stoppedBy: stoppedCanceled, cancelCause: cancelCause(ctx)}
	}
	switch {
	case errors.Is(err, ErrMaxSteps):
		return runEnd{stoppedBy: stoppedMaxSteps}
	case errors.Is(err, ErrUsageLimit):
		return runEnd{stoppedBy: stoppedUsageLimit}
	case errors.Is(err, ErrLoopDetected):
		return runEnd{stoppedBy: stoppedLoopDetected}
	case errors.Is(err, ErrModelRetriesExceeded):
		return runEnd{stoppedBy: stoppedRetries}
	}
	return runEnd{}
}

// cancelCause names a canceled context's cause for weft.cancel.cause:
// a cause with a Kind() string method (CancelCause, or a layer's own
// type) reports its kind, a deadline reports "deadline", and a plain
// cancel — no cause given — reports nothing.
func cancelCause(ctx context.Context) string {
	cause := context.Cause(ctx)
	var k interface{ Kind() string }
	if errors.As(cause, &k) {
		return k.Kind()
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		return "deadline"
	}
	return ""
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

// classifyToolError classifies a tool error result for error.type: the
// ToolError's code when the chain produced one, "timeout" for a
// timed-out call (the typed error), "panic" for a contained panic in
// the handler or its middleware (the typed error; weft.panic.layer says
// which), the context error's name when the run's cancellation reached
// the handler — the same name the run span reports — the error's own
// ErrorType() string when one in its chain declares it (a convention,
// like a cancel cause's Kind(): weft/mcp's mcp_transport and
// mcp_tool_error), and "tool_error" otherwise. The result text never
// becomes a span attribute (ADR 0016). te / isTE and isPE are err's
// *ToolError and *toolPanicError matches, already read by the caller
// (readToolError). Every target is errors.AsType's, on the stack.
func classifyToolError(err error, te *ToolError, isTE, isPE bool) string {
	if isTE && te.Code != "" {
		return te.Code
	}
	if _, ok := errors.AsType[*toolTimeoutError](err); ok {
		return "timeout"
	}
	if isPE {
		return "panic"
	}
	if name, ok := ctxErrorType(err); ok {
		return name
	}
	if typed, ok := errors.AsType[errorTyper](err); ok {
		if t := typed.ErrorType(); t != "" {
			return t
		}
	}
	return "tool_error"
}

// errorTyper is an error declaring its own error.type (classifyToolError).
type errorTyper interface {
	error
	ErrorType() string
}

// recorder emits the run's OTel log records (ADR 0024): every durable
// event and every delta through deliver — the one place each passes
// exactly once — and the transcript's batches at their growth points
// (recordMessages). Two counters [D3]: weft.event.pos numbers the seven
// durable types, weft.delta.pos the three deltas, so dropping deltas
// never opens a hole in the durable sequence; Nested takes neither (a
// child run numbers its own). Records go out on the run's context, so
// the SDK derives their trace/span ids from the invoke_agent span, and
// nothing is emitted after that context ends: deliver checks before an
// event record, recordMessages before a transcript batch.
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
	// resumedFrom and resumeCalls are a resuming run's link (ResumeOf)
	// and its decisions per parked call (weft.resume.calls): reported
	// on the run_start record and the invoke_agent span only; "" on a
	// run that resumes nothing.
	resumedFrom string
	resumeCalls string
	// link is the run's linkage (runLinkAttrs), read by execute from the
	// context the run was started on before it detached the call:
	// reported on the run_start record, as on the invoke_agent span.
	link []attribute.KeyValue

	eventPos    atomic.Int64
	deltaPos    atomic.Int64
	messagesIdx atomic.Int64
	// growthTop is one past the index of the latest growth messages
	// record (0 = none yet): what a request whose messages are the
	// transcript points at. A compaction view takes an index on the
	// same counter but never moves it (ADR 0028 §8).
	growthTop atomic.Int64
	// growthCount is how many growth messages records were emitted (views
	// excluded: they take an index on messagesIdx but are not transcript)
	// — the invoke_agent span's weft.run.messages, comparable with a
	// store's message_count.
	growthCount atomic.Int64

	// The request record's state (ADR 0028, request.go): one counter
	// per kind, the per-run dedupe sets of the prompt and catalog
	// hashes already recorded, and the last hashes computed, so an
	// unchanged prompt or tool set is not hashed again. The counters
	// are atomic (a reporter may emit a further attempt's record from
	// a chain goroutine); the rest is touched on the loop goroutine
	// only.
	requestIdx     atomic.Int64
	promptIdx      atomic.Int64
	toolsIdx       atomic.Int64
	rawIdx         atomic.Int64 // weft.raw.index (raw.go)
	seenPrompts    map[string]bool
	seenCatalogs   map[string]bool
	lastSystem     string
	lastSystemHash string
	systemHashed   bool
	lastTools      []*ToolDef
	lastCatalog    string
	catalogHashed  bool

	// obs is the run's observer, for the Debug line a dropped record
	// leaves; panics the agent's TapPanics counter, for a contained
	// panic. Either may be nil in observer-only tests.
	obs    *observer
	panics *atomic.Int64

	// truncated holds each call's result-cap cut (call id → bytes) from
	// its tool goroutine until its ToolFinish is recorded — the
	// tool_finish record's weft.tool.truncated_bytes. Written before the
	// ToolFinish emit and read inside it, on the same goroutine; the
	// mutex orders concurrent calls. Nil until a call is cut.
	truncMu   sync.Mutex
	truncated map[string]int
	// denied counts the calls a resume denied (Deny, or no decision):
	// they have no span and no event (ADR 0007), so the invoke_agent
	// span's weft.run.denied is their one carrier.
	denied atomic.Int64
	// runPanics counts this run's contained observer panics — taps,
	// OnMessages observers, a record or a report the pipeline failed to
	// build, and every call into the TracerProvider or LoggerProvider
	// that panicked (providerpanic.go) — beside the agent-wide
	// TapPanics: the invoke_agent span's weft.tap.panics. OnRunEnd
	// observers run after the span ended, so theirs reach TapPanics
	// alone.
	runPanics atomic.Int64
	// warned holds the provider kinds (providerTraces, providerLogs)
	// whose contained panic this run has already named in its one Warn
	// line (providerpanic.go).
	warned atomic.Uint32

	// stepModel is the model that answered the step whose StepFinish is
	// being emitted (callTiming.answered): set by the loop goroutine
	// right before that emit, read by recordEvent on the same goroutine
	// (deliver is synchronous) for the step_finish record's
	// gen_ai.response.model.
	stepModel string
}

// captureOn resolves the content question for one emission: the agent's
// Content option when set; else whether any destination wants message
// content, through the one standard Enabled question the pipeline's
// processors answer per destination (S1.1 [D2]).
func (r *recorder) captureOn(ctx context.Context) bool {
	if r.capture != nil {
		return *r.capture
	}
	return r.logEnabled(ctx, log.EnabledParameters{EventName: eventNameMessages})
}

// noteTruncated remembers a call's result-cap cut for its tool_finish
// record. Nil-safe: execTools may run without a recorder.
func (r *recorder) noteTruncated(callID string, cut int) {
	if r == nil {
		return
	}
	r.truncMu.Lock()
	if r.truncated == nil {
		r.truncated = map[string]int{}
	}
	r.truncated[callID] = cut
	r.truncMu.Unlock()
}

// takeTruncated returns and forgets a call's cut, 0 when none.
func (r *recorder) takeTruncated(callID string) int {
	r.truncMu.Lock()
	defer r.truncMu.Unlock()
	n, ok := r.truncated[callID]
	if ok {
		delete(r.truncated, callID)
	}
	return n
}

// countPanic counts one contained observer panic: the agent's
// TapPanics and this run's weft.tap.panics. Nil-safe.
func (r *recorder) countPanic() {
	if r == nil {
		return
	}
	if r.panics != nil {
		r.panics.Add(1)
	}
	r.runPanics.Add(1)
}

// retryMark is a RETRY result's place in its tool's budget: n is the
// tool's consecutive RETRY count with this result (1-based), budget
// the run's MaxModelRetries — the guard fails the run once n exceeds
// it. The zero value marks nothing (not a counted RETRY). execTools
// assigns the marks after its batch, in call order, so a mark is
// exactly what the guard counts; it rides the execute_tool span only
// (the tool_finish record is written as the call finishes, before its
// siblings' outcomes are known).
type retryMark struct{ n, budget int }

// noteDenied counts one call a resume denied. Nil-safe.
func (r *recorder) noteDenied() {
	if r != nil {
		r.denied.Add(1)
	}
}

// recordEvent reports one event from deliver, after the taps and before
// the sink. Nested is not reported: the child run emitted (or dropped)
// its own records, and recording the parent's wrapper copy would store
// every child event twice (ADR 0024 D3).
func (r *recorder) recordEvent(ctx context.Context, ev Event) {
	defer r.contain(ctx, -1, "event")
	var kind, eventName string
	switch ev.(type) {
	case RunStart, StepStart, ToolStart, ToolFinish, StepFinish, Steered, RunFinish:
		kind, eventName = "event", eventNameEvent
	case TextDelta, ReasoningDelta, ToolArgsDelta:
		kind, eventName = "delta", eventNameDelta
	default:
		return
	}
	// A call's noted cut is taken before the Enabled question: with no
	// destination enabling event records the entry must still go, or it
	// stays in the map until the run ends.
	var cut int
	if tf, ok := ev.(ToolFinish); ok {
		cut = r.takeTruncated(tf.CallID)
	}
	if !r.logEnabled(ctx, log.EnabledParameters{EventName: eventName}) {
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
		// The one way an event fails to encode: a tool call whose
		// arguments are not JSON (quoteInvalidArgs).
		if b, err = json.Marshal(quoteInvalidEventArgs(body)); err != nil {
			return // an event that still cannot encode is dropped, never fatal
		}
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
		// The step's timing (the event's own fields, as attributes for
		// backends that do not parse bodies) and the model that answered
		// (the loop's stepModel, set just before the emit; not on the
		// event).
		attrs = append(attrs, attrStepIndex.Int(e.Index), attrLatencyMS.Int64(e.LatencyMS))
		if e.TTFTMS > 0 {
			attrs = append(attrs, attrTTFTMS.Int64(e.TTFTMS))
		}
		if r.stepModel != "" {
			attrs = append(attrs, semconv.GenAIResponseModel(r.stepModel))
		}
	case Steered:
		attrs = append(attrs, attrStepIndex.Int(e.Step))
	case ToolStart:
		attrs = append(attrs, attrToolSeq.Int64(e.Seq), semconv.GenAIToolCallID(e.CallID), semconv.GenAIToolName(e.Name))
	case ToolFinish:
		attrs = append(attrs, attrToolSeq.Int64(e.Seq), semconv.GenAIToolCallID(e.CallID), semconv.GenAIToolName(e.Name))
		if cut > 0 {
			attrs = append(attrs, attrToolTruncated.Int(cut))
		}
	case RunStart:
		// The linkage a run started on a tool call reports (runLinkAttrs):
		// weft.parent.* for a Subagent's child, weft.caller.* for any
		// other. Absent on a top-level run. Read once at execute's start
		// (the run's own context no longer carries the call).
		attrs = append(attrs, r.link...)
		if r.manifestHash != "" {
			attrs = append(attrs, attrManifestHash.String(r.manifestHash))
		}
		if e.InstructionsHash != "" {
			attrs = append(attrs, attrInstructionsHash.String(e.InstructionsHash))
		}
		if r.resumeCalls != "" {
			attrs = append(attrs, attrResumeCalls.String(r.resumeCalls))
		}
		if r.resumedFrom != "" {
			attrs = append(attrs, attrResumedFrom.String(r.resumedFrom))
		}
		attrs = append(attrs, attrVersion.String(version))
	}
	attrs = append(attrs, metadataAttrs(ctx)...)
	rec.AddAttributes(attrs...)
	r.emitLog(ctx, rec)
}

// recordMessages reports one batch of messages joining the run's
// transcript — the five growth points (S1.3 [D1]): the repaired input at
// run start, the tool message a resume creates or rebuilds, each
// assistant message, each tool message, each steered batch. Emitted only
// when capture is on: pure content, unlike the event records, which keep
// their stripped shape. step is the step the batch belongs to (0 for the
// input); input marks index 0. The body is a JSON array of Message,
// never capped — a capped transcript is not replay-grade (ADR 0024 D1).
// Like the event records, nothing is reported once the run's context
// has ended (S1.3's cancellation rule): the transcript may still grow —
// a cancelled batch's results ride RunError.Result — but the record
// stream stops where the event stream does.
func (r *recorder) recordMessages(ctx context.Context, step int, msgs []Message, input bool) {
	defer r.contain(ctx, step, eventNameMessages)
	if len(msgs) == 0 || ctx.Err() != nil || !r.captureOn(ctx) {
		return
	}
	if !r.logEnabled(ctx, log.EnabledParameters{EventName: eventNameMessages}) {
		return
	}
	b, err := json.Marshal(msgs)
	if err != nil {
		// A tool call whose arguments are not JSON (quoteInvalidArgs):
		// dropping the batch would lose the message from the stored
		// transcript — and the input record of every later run it is fed
		// back into.
		if b, err = json.Marshal(quoteInvalidMessageArgs(msgs)); err != nil {
			return
		}
	}

	idx := r.messagesIdx.Add(1) - 1
	r.growthTop.Store(idx + 1)
	r.growthCount.Add(1)
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
		attrMessagesIndex.Int64(idx),
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
	r.emitLog(ctx, rec)
}

// quoteInvalidArgs returns a tool call's arguments in a form a record
// body can carry. Args is the model's raw bytes, and a model can emit
// bytes that are not JSON — a cut-off object, stray text; the loop keeps
// them in the transcript and answers the call with INVALID_INPUT — but
// encoding/json refuses to embed an invalid RawMessage, which would fail
// the whole record: the tool_start event, the assistant's messages
// batch, a parked call's run_finish. Such arguments are recorded as a
// JSON string holding the raw bytes — still not an object, so a re-run
// fed the recorded transcript still fails the call with INVALID_INPUT.
// Empty arguments encode as null, which the tool decoder reads as {}
// too.
// Valid arguments pass through untouched; these helpers only run after
// a marshal has already failed.
func quoteInvalidArgs(args json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(args)) == 0 {
		return nil
	}
	if json.Valid(args) {
		return args
	}
	quoted, err := json.Marshal(string(args))
	if err != nil {
		return nil // unreachable: a string always encodes
	}
	return quoted
}

// quoteInvalidEventArgs applies quoteInvalidArgs to every tool-call
// argument an event carries: ToolStart's own, RunFinish's pending
// calls', a Steered batch's messages'.
func quoteInvalidEventArgs(ev Event) Event {
	switch e := ev.(type) {
	case ToolStart:
		e.Args = quoteInvalidArgs(e.Args)
		return e
	case RunFinish:
		pending := make([]ToolCallPart, len(e.Pending))
		for i, c := range e.Pending {
			c.Args = quoteInvalidArgs(c.Args)
			pending[i] = c
		}
		e.Pending = pending
		return e
	case Steered:
		e.Messages = quoteInvalidMessageArgs(e.Messages)
		return e
	default:
		return ev
	}
}

// quoteInvalidMessageArgs applies quoteInvalidArgs to every tool call
// in msgs, on a copy: the run's transcript keeps the model's own bytes.
func quoteInvalidMessageArgs(msgs []Message) []Message {
	out := make([]Message, len(msgs))
	for i, m := range msgs {
		content := make([]Part, len(m.Content))
		for j, p := range m.Content {
			if c, ok := p.(ToolCallPart); ok {
				c.Args = quoteInvalidArgs(c.Args)
				p = c
			}
			content[j] = p
		}
		m.Content = content
		out[i] = m
	}
	return out
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
