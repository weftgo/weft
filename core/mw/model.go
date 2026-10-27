package mw

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/weftgo/weft/core"
)

// Fallback tries the given models, in order, when the wrapped model's
// stream fails before yielding any event — a provider outage, a
// capability gap (core.ErrUnsupported). A failure
// after events were yielded is not retried anywhere: the loop already
// consumed part of the reply, so it surfaces as the run error. Context
// cancellation and the WEFT_MODEL_REQUESTS kill switch never fall
// through. A max_tokens finish is a successful stream, not a failure —
// Fallback does not switch on it. Info reports the primary model. Each
// model tried is reported as one attempt on the run's record
// (core.ReportFromContext) — reporting only, a no-op outside a run —
// a backup's with AttemptInfo.Layer core.AttemptLayerFallback, the
// marker a reader's fallbacks counter keys on. A backup that reports
// its own attempts (a Retry around it) reports them itself, unmarked.
// Each model tried is handed its own copy of the request's slices and
// Params values, so what a failed model wrote into its request never
// reaches the next one.
func Fallback(models ...core.Model) core.ModelMiddleware {
	return FallbackWhen(defaultFallback, models...)
}

// FallbackWhen is Fallback with a predicate: the next model is tried
// only for errors that satisfy when. A nil predicate uses Fallback's
// default (anything but cancellation and the kill switch).
func FallbackWhen(when func(error) bool, models ...core.Model) core.ModelMiddleware {
	if when == nil {
		when = defaultFallback
	}
	return func(next core.Model) core.Model {
		chain := make([]core.Model, 0, 1+len(models))
		chain = append(chain, next)
		for _, m := range models {
			if m != nil {
				chain = append(chain, m)
			}
		}
		return &fallbackModel{chain: chain, when: when}
	}
}

func defaultFallback(err error) bool {
	return !errors.Is(err, context.Canceled) &&
		!errors.Is(err, context.DeadlineExceeded) &&
		!errors.Is(err, core.ErrModelRequestsDenied)
}

type fallbackModel struct {
	chain []core.Model
	when  func(error) bool
}

func (m *fallbackModel) Info() core.ModelInfo { return core.InfoOf(m.chain[0]) }

// Describe names the layer for the manifest (core.MiddlewareInfo): the
// backups, in order, as provider/model, and whether a custom predicate
// (FallbackWhen) decides.
func (m *fallbackModel) Describe() core.MiddlewareInfo {
	names := make([]string, 0, len(m.chain)-1)
	for _, b := range m.chain[1:] {
		names = append(names, modelName(core.InfoOf(b)))
	}
	p := map[string]string{"models": strings.Join(names, ", ")}
	if reflect.ValueOf(m.when).Pointer() != reflect.ValueOf(defaultFallback).Pointer() {
		p["when"] = "custom"
	}
	return core.MiddlewareInfo{Name: "mw.Fallback", Params: p}
}

// modelName renders a model for a layer's description: provider/name,
// or the one that is set.
func modelName(i core.ModelInfo) string {
	switch {
	case i.Provider != "" && i.Name != "":
		return i.Provider + "/" + i.Name
	case i.Name != "":
		return i.Name
	case i.Provider != "":
		return i.Provider
	}
	return "unnamed"
}

// Unwrap declares the primary — the chain's first model, the same one
// Info names — so a caller walking with core.Unwrap sees the chain's
// face, not its backups.
func (m *fallbackModel) Unwrap() core.Model { return m.chain[0] }

func (m *fallbackModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return func(yield func(core.ModelEvent, error) bool) {
		for i, model := range m.chain {
			// The wait an enclosing Retry slept before this try, and its
			// retry mark, belong to the chain's first request alone; a
			// backup is sent at once, as a fallback — and a backup that
			// reports its own attempts (a Retry around it) reads the
			// fallback mark from the context, its retries keeping it, so
			// the fallbacks counter counts the run the backup answered.
			t := try{layer: core.AttemptLayerFallback}
			actx := withTry(ctx, t)
			if i == 0 {
				actx, t = ctx, tryOf(ctx)
			}
			yielded, failed := replay(actx, model, attemptRequest(req), t, yield, nil)
			if failed == nil {
				return
			}
			last := i == len(m.chain)-1
			if yielded || last || !m.when(failed) {
				yield(nil, failed)
				return
			}
		}
	}
}

// replay streams one model attempt into yield and, when model is the
// provider side of the chain, reports it on the run's record
// (core.ReportFromContext; a no-op outside a run). A model that
// reports its own attempts (the ReportsAttempts marker, see
// reportsItself) is not reported again: one report per provider
// request, however Retry, Fallback and reporting adapters compose. It
// returns whether any event reached the consumer and the stream error,
// if one ended it. A consumer that stops early is reported as yielded
// with no error. t is the report's AttemptInfo.Layer ("" for a first
// try) and Wait (the backoff slept before this request); the report
// carries the HTTP status HTTPStatus finds on the attempt's error.
// giveUp, when not nil, is the calling layer's verdict on a failed
// attempt, read before the report: AttemptInfo.GiveUp when it stops
// there ("" when it tries again).
func replay(ctx context.Context, model core.Model, req core.ModelRequest, t try, yield func(core.ModelEvent, error) bool, giveUp func(yielded bool, failed error) string) (bool, error) {
	if reportsItself(model) {
		yielded, failed := stream(ctx, model, req, yield)
		if giveUp != nil && failed != nil {
			giveUp(yielded, failed)
		}
		return yielded, failed
	}
	info, start := core.InfoOf(model), time.Now()
	yielded, failed := stream(ctx, model, req, yield)
	end := time.Now()
	ask, _ := RetryAfter(failed, end)
	status, _ := HTTPStatus(failed)
	var why string
	if giveUp != nil && failed != nil {
		why = giveUp(yielded, failed)
	}
	core.ReportFromContext(ctx).Attempt(core.AttemptInfo{Model: info.Name, Provider: info.Provider,
		Start: start, End: end, Err: failed, RetryAfter: ask, Layer: t.layer, Wait: t.wait, HTTPStatus: status, GiveUp: why})
	return yielded, failed
}

// attemptRequest is one attempt's own copy of the request Retry or
// Fallback was handed: fresh Messages, Content and Tools slices and
// fresh Params values, so what a failed attempt's model — or a
// middleware inside the layer — reassigns or writes through a knob's
// pointer (ModelRequest's godoc allows both) never reaches the next
// attempt, the next model of the chain, or the caller's request. The
// loop's own copy (core's modelRequest) is the same shape; the part
// values, the bytes behind them and the ToolDefs stay shared and
// read-only. Paid per attempt, on the Retry/Fallback path only. A
// field added to ModelRequest, Message or RequestParams belongs here
// too: core's TestRequestParamsFieldsAreCopied fails until it is.
func attemptRequest(req core.ModelRequest) core.ModelRequest {
	if req.Messages != nil {
		n := 0
		for _, m := range req.Messages {
			n += len(m.Content)
		}
		msgs := make([]core.Message, len(req.Messages))
		parts := make([]core.Part, n)
		for i, m := range req.Messages {
			if m.Content != nil {
				k := copy(parts, m.Content)
				m.Content, parts = parts[:k:k], parts[k:]
			}
			msgs[i] = m
		}
		req.Messages = msgs
	}
	req.Tools = slices.Clone(req.Tools)
	p := &req.Params
	if p.Temperature != nil || p.TopP != nil || p.MaxTokens != nil || p.Seed != nil {
		box := new(struct {
			temperature, topP float64
			maxTokens         int
			seed              int64
		})
		if p.Temperature != nil {
			box.temperature = *p.Temperature
			p.Temperature = &box.temperature
		}
		if p.TopP != nil {
			box.topP = *p.TopP
			p.TopP = &box.topP
		}
		if p.MaxTokens != nil {
			box.maxTokens = *p.MaxTokens
			p.MaxTokens = &box.maxTokens
		}
		if p.Seed != nil {
			box.seed = *p.Seed
			p.Seed = &box.seed
		}
	}
	p.Stop = slices.Clone(p.Stop)
	return req
}

// try is what the layer that made a request knows of it for its
// report: the backoff slept before it (AttemptInfo.Wait) and the layer
// that sent it (AttemptInfo.Layer: retry, fallback, "" for a first try).
type try struct {
	wait  time.Duration
	layer string
}

// tryKey carries a Retry's try to the layer that reports it, when that
// is a Fallback beneath it (a self-reporting model Retry stays silent
// over) or a Retry nested further down, whose first try keeps it; a
// Fallback's backups are given their own.
type tryKey struct{}

func withTry(ctx context.Context, t try) context.Context {
	if t == (try{}) && tryOf(ctx) == (try{}) {
		return ctx
	}
	return context.WithValue(ctx, tryKey{}, t)
}

func tryOf(ctx context.Context) try {
	t, _ := ctx.Value(tryKey{}).(try)
	return t
}

// maxUnwrap bounds reportsItself's walk: a chain deeper than this is
// treated as not self-reporting rather than walked further.
const maxUnwrap = 64

// reportsItself walks the Unwrap chain (core.Unwrap) for the optional
// marker interface{ ReportsAttempts() bool } — what an adapter or
// middleware that reports its own attempts implements (ADR 0013). The
// first layer carrying the marker decides: true, the layer reports and
// replay stays silent; false, it does not (a router that may bypass
// the layers it unwraps to stops the walk this way). No marker on any
// layer: replay reports. The walk is bounded — at most maxUnwrap hops,
// and it stops when Unwrap returns the same value — so a
// self-unwrapping wrapper cannot hang the run.
func reportsItself(m core.Model) bool {
	for range maxUnwrap {
		if m == nil {
			return false
		}
		if r, ok := m.(interface{ ReportsAttempts() bool }); ok {
			return r.ReportsAttempts()
		}
		next := core.Unwrap(m)
		if next != nil && reflect.TypeOf(next) == reflect.TypeOf(m) && reflect.TypeOf(m).Comparable() && next == m {
			return false
		}
		m = next
	}
	return false
}

// ReportsAttempts marks Fallback as reporting its own attempts (one
// per model of the chain it streams), so a Retry around it stays
// silent.
func (m *fallbackModel) ReportsAttempts() bool { return true }

func stream(ctx context.Context, model core.Model, req core.ModelRequest, yield func(core.ModelEvent, error) bool) (yielded bool, failed error) {
	for ev, err := range model.Stream(ctx, req) {
		if err != nil {
			return yielded, err
		}
		yielded = true
		if !yield(ev, nil) {
			return true, nil
		}
	}
	return yielded, nil
}

// Log records one line per model call at Debug level — the request
// summary before the call and the finish (or error) after it — on the
// given logger, or slog.Default when nil. Lines carry the model, the
// message and tool counts, and on finish the stop reason, usage, tool
// call count, and duration. Placed outermost it sees the outcome of
// retries and fallbacks; placed innermost, each attempt.
func Log(l *slog.Logger) core.ModelMiddleware {
	return func(next core.Model) core.Model {
		return &logModel{next: next, log: l}
	}
}

type logModel struct {
	next core.Model
	log  *slog.Logger
}

func (m *logModel) Info() core.ModelInfo { return core.InfoOf(m.next) }

// Describe names the layer for the manifest (core.MiddlewareInfo).
func (m *logModel) Describe() core.MiddlewareInfo { return core.MiddlewareInfo{Name: "mw.Log"} }

// Unwrap declares the model the log lines are about (the Unwrap
// convention, beside Info).
func (m *logModel) Unwrap() core.Model { return m.next }

func (m *logModel) logger() *slog.Logger {
	if m.log != nil {
		return m.log
	}
	return slog.Default()
}

func (m *logModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return func(yield func(core.ModelEvent, error) bool) {
		info := core.InfoOf(m.next)
		l := m.logger().With("provider", info.Provider, "model", info.Name)
		l.DebugContext(ctx, "model request",
			"messages", len(req.Messages),
			"tools", len(req.Tools),
			"system_bytes", len(req.System),
			"thinking", req.Thinking.Level,
		)
		start := time.Now()
		calls := 0
		for ev, err := range m.next.Stream(ctx, req) {
			if err != nil {
				l.DebugContext(ctx, "model error", "err", err, "dur", time.Since(start))
				yield(nil, err)
				return
			}
			switch e := ev.(type) {
			case core.ModelToolCall:
				calls++
			case core.ModelFinish:
				l.DebugContext(ctx, "model finish",
					"reason", e.Reason,
					"raw", e.Raw,
					"input_tokens", e.Usage.InputTokens,
					"output_tokens", e.Usage.OutputTokens,
					"tool_calls", calls,
					"dur", time.Since(start),
				)
			}
			if !yield(ev, nil) {
				return
			}
		}
	}
}
