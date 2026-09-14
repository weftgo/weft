package mw

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"time"

	"github.com/weftgo/weft"
)

// Fallback tries the given models, in order, when the wrapped model's
// stream fails before yielding any event — a provider outage, a
// capability gap (weft.ErrUnsupported), a denied request. A failure
// after events were yielded is not retried anywhere: the loop already
// consumed part of the reply, so it surfaces as the run error. Context
// cancellation and the WEFT_MODEL_REQUESTS kill switch never fall
// through. A max_tokens finish is a successful stream, not a failure —
// Fallback does not switch on it. Info reports the primary model.
func Fallback(models ...weft.Model) weft.ModelMiddleware {
	return FallbackWhen(defaultFallback, models...)
}

// FallbackWhen is Fallback with a predicate: the next model is tried
// only for errors that satisfy when. A nil predicate uses Fallback's
// default (anything but cancellation and the kill switch).
func FallbackWhen(when func(error) bool, models ...weft.Model) weft.ModelMiddleware {
	if when == nil {
		when = defaultFallback
	}
	return func(next weft.Model) weft.Model {
		chain := make([]weft.Model, 0, 1+len(models))
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
		!errors.Is(err, weft.ErrModelRequestsDenied)
}

type fallbackModel struct {
	chain []weft.Model
	when  func(error) bool
}

func (m *fallbackModel) Info() weft.ModelInfo { return weft.InfoOf(m.chain[0]) }

func (m *fallbackModel) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	return func(yield func(weft.ModelEvent, error) bool) {
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

// replay streams one model attempt into yield. It reports whether any
// event reached the consumer and the stream error, if one ended it. A
// consumer that stops early is reported as yielded with no error.
func replay(ctx context.Context, model weft.Model, req weft.ModelRequest, yield func(weft.ModelEvent, error) bool) (yielded bool, failed error) {
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
func Log(l *slog.Logger) weft.ModelMiddleware {
	return func(next weft.Model) weft.Model {
		return &logModel{next: next, log: l}
	}
}

type logModel struct {
	next weft.Model
	log  *slog.Logger
}

func (m *logModel) Info() weft.ModelInfo { return weft.InfoOf(m.next) }

func (m *logModel) logger() *slog.Logger {
	if m.log != nil {
		return m.log
	}
	return slog.Default()
}

func (m *logModel) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	return func(yield func(weft.ModelEvent, error) bool) {
		info := weft.InfoOf(m.next)
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
			case weft.ModelToolCall:
				calls++
			case weft.ModelFinish:
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
