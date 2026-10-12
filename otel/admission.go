package otel

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Queue accounting (U5, UX-E-05). A batch destination's records wait in
// the SDK's batch processor queue between Emit and export. The SDK
// handles a full queue silently: the log BatchProcessor overwrites its
// oldest record (so a storm loses a run's run_start first, and its
// dropped count is unexported — one internal-logger line nobody sees),
// the span processor refuses the newest. Neither reaches weft's drop
// counter. So each batch chain admits its records itself, ahead of the
// SDK: admission counts the records it handed the SDK and has not yet
// seen leave for export, and refuses a record once that count reaches
// the queue's size. The SDK queue is built with the same size, and a
// record is released only when the export it rides begins — after the
// SDK dequeued it — so the SDK queue never holds more than admission
// let in and never drops on its own: every overflow is weft's refusal,
// counted (dropCounter.overflowed), warned once per destination, and
// reported on the heartbeat (weft.records.dropped).
//
// Admission sheds by tier, so the records a reader cannot do without go
// last: deltas (never stored, a live-tail nicety) are refused once the
// queue is half full, ordinary durable records at seven eighths, and the
// run's bracket — run_start, run_finish, the heartbeat — only when it is
// full. A storm therefore loses its deltas first and keeps the records
// the run row is derived from.
//
// Admission never blocks the app unless the destination asked for
// Blocking (development only, refused outside WEFT_ENV=dev): then a
// durable record waits — bounded by blockMax and the emitting context —
// for room instead of being refused. Deltas never wait.

// Default queue sizes: the SDK's 2,048 for third-party destinations,
// sixteen thousand for Studio — a dev loop's event storm (thousands of
// deltas and events in milliseconds) fits.
const (
	defaultQueueSize       = 2048
	defaultStudioQueueSize = 16384
)

// blockMax bounds one Blocking wait: past it the record is refused and
// counted like any overflow — a hung exporter never hangs the app for
// longer.
const blockMax = 5 * time.Second

// tier is a record's shedding class.
type tier int

const (
	tierDelta    tier = iota // refused at half the queue
	tierDurable              // refused at seven eighths
	tierPriority             // refused only when the queue is full
)

// admission is one batch chain's in-flight count against its queue
// size.
type admission struct {
	size     int64
	inflight atomic.Int64
	// refused counts this chain's refusals (the destination's counter
	// sums both chains).
	refused  atomic.Int64
	blocking bool
	// space is signalled (never blocking) on every release, so a
	// Blocking waiter re-tries; one waiter wakes per signal and passes
	// the baton on when room is left.
	space chan struct{}
	done  chan struct{}
	once  sync.Once
}

func newAdmission(size int, blocking bool) *admission {
	if size <= 0 {
		size = defaultQueueSize
	}
	return &admission{size: int64(size), blocking: blocking,
		space: make(chan struct{}, 1), done: make(chan struct{})}
}

// limit is the in-flight count below which a record of tier t is
// admitted.
func (a *admission) limit(t tier) int64 {
	switch t {
	case tierDelta:
		return max(1, a.size/2)
	case tierDurable:
		return max(1, a.size-a.size/8)
	default:
		return a.size
	}
}

func (a *admission) try(limit int64) bool {
	for {
		n := a.inflight.Load()
		if n >= limit {
			return false
		}
		if a.inflight.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

// admit reports whether one record of tier t may go on to the SDK
// queue, taking its place when it may.
func (a *admission) admit(ctx context.Context, t tier) bool {
	if a.admitOrWait(ctx, t) {
		return true
	}
	a.refused.Add(1)
	return false
}

func (a *admission) admitOrWait(ctx context.Context, t tier) bool {
	limit := a.limit(t)
	if a.try(limit) {
		return true
	}
	if !a.blocking || t == tierDelta {
		return false
	}
	timer := time.NewTimer(blockMax)
	defer timer.Stop()
	for {
		select {
		case <-a.space:
			if a.try(limit) {
				if a.inflight.Load() < limit {
					a.signal() // room left: the next waiter's turn
				}
				return true
			}
		case <-timer.C:
			return false
		case <-ctx.Done():
			return false
		case <-a.done:
			return false
		}
	}
}

// release gives back n places: the records began their export.
func (a *admission) release(n int) {
	a.inflight.Add(-int64(n))
	a.signal()
}

func (a *admission) signal() {
	select {
	case a.space <- struct{}{}:
	default:
	}
}

// close wakes every Blocking waiter for good (the chain shut down).
func (a *admission) close() { a.once.Do(func() { close(a.done) }) }

// releasingLogExporter is the exporter a batch log chain hands the
// SDK's BatchProcessor: each Export call releases its records' places
// before exporting them.
type releasingLogExporter struct {
	sdklog.Exporter
	adm *admission
}

func (e releasingLogExporter) Export(ctx context.Context, recs []sdklog.Record) error {
	e.adm.release(len(recs))
	return e.Exporter.Export(ctx, recs)
}

// releasingSpanExporter is the span side's twin.
type releasingSpanExporter struct {
	sdktrace.SpanExporter
	adm *admission
}

func (e releasingSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.adm.release(len(spans))
	return e.SpanExporter.ExportSpans(ctx, spans)
}

// admittedSpanProc admits each ended span ahead of the SDK's batch span
// processor, in two tiers: a run's invoke_agent span — its status, its
// totals, the weft.run.events a reader counts the run's losses against —
// is admitted until the queue is full, every other span until seven
// eighths. An unsampled span is not the SDK's to queue, so it takes no
// place.
type admittedSpanProc struct {
	sdktrace.SpanProcessor
	adm   *admission
	drops *dropCounter
}

func (p admittedSpanProc) OnEnd(s sdktrace.ReadOnlySpan) {
	if s.SpanContext().IsSampled() {
		if !p.adm.admit(context.Background(), spanTier(s)) {
			p.drops.overflowed(1, p.adm.size)
			return
		}
	}
	p.SpanProcessor.OnEnd(s)
}

func (p admittedSpanProc) ForceFlush(ctx context.Context) error {
	return flushDetached(ctx, p.SpanProcessor.ForceFlush)
}

func (p admittedSpanProc) Shutdown(ctx context.Context) error {
	p.adm.close()
	return p.SpanProcessor.Shutdown(ctx)
}

// spanTier is a span's shedding class: the run's root last.
func spanTier(s sdktrace.ReadOnlySpan) tier {
	for _, kv := range s.Attributes() {
		if kv.Key == "gen_ai.operation.name" {
			if kv.Value.AsString() == "invoke_agent" {
				return tierPriority
			}
			break
		}
	}
	return tierDurable
}

// flushDetached runs a batch processor's ForceFlush on a context that
// does not end, waiting for it under ctx. The SDK's log flush exports in
// chunks and stops between chunks when its context ends: the records of
// the chunks it skipped would never be exported and their places never
// released. Detached, every chunk is exported (each bounded by the
// SDK's export timeout) and released; the caller still waits no longer
// than ctx.
func flushDetached(ctx context.Context, flush func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- flush(context.WithoutCancel(ctx)) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
