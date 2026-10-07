package mw

import (
	"context"
	"errors"
	"iter"
	"log/slog"
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
// (core.ReportFromContext) — reporting only, a no-op outside a run.
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

// Unwrap declares the primary — the chain's first model, the same one
// Info names — so a caller walking with core.Unwrap sees the chain's
// face, not its backups.
func (m *fallbackModel) Unwrap() core.Model { return m.chain[0] }

func (m *fallbackModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return func(yield func(core.ModelEvent, error) bool) {
		for i, model := range m.chain {
			yielded, failed := replay(ctx, model, req, yield)
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
// (core.ReportFromContext; a no-op outside a run). A model that is
// itself a Retry or Fallback layer (directly or under other wrappers)
// reports its own attempts, so the layer above stays silent: one
// report per provider request, however Retry and Fallback compose. It
// returns whether any event reached the consumer and the stream error,
// if one ended it. A consumer that stops early is reported as yielded
// with no error.
func replay(ctx context.Context, model core.Model, req core.ModelRequest, yield func(core.ModelEvent, error) bool) (bool, error) {
	if reportsItself(model) {
		return stream(ctx, model, req, yield)
	}
	info, start := core.InfoOf(model), time.Now()
	yielded, failed := stream(ctx, model, req, yield)
	end := time.Now()
	ask, _ := RetryAfter(failed, end)
	core.ReportFromContext(ctx).Attempt(core.AttemptInfo{Model: info.Name, Provider: info.Provider,
		Start: start, End: end, Err: failed, RetryAfter: ask})
	return yielded, failed
}

// reportsItself walks the Unwrap chain for a Retry or Fallback layer.
func reportsItself(m core.Model) bool {
	for ; m != nil; m = core.Unwrap(m) {
		switch m.(type) {
		case *retryModel, *fallbackModel:
			return true
		}
	}
	return false
}

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
