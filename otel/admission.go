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
// the queue's size. The SDK queue is built with the same size (the span
// queue a few places larger: spanQueueHeadroom), and a record is
// released only when the export it rides begins — after the SDK dequeued
// it — so the SDK queue never holds more than admission let in and never
// drops on its own: every overflow is weft's refusal, counted
// (dropCounter.overflowed), warned once per destination, and reported as
// weft.records.dropped on the heartbeat and on every later run_start and
// run_finish record.
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
// for room instead of being refused. Deltas never wait. The bound is per
// stall, not per record: once one wait times out, the chain refuses at
// once (counted, as without Blocking) until an export begins and gives
// places back — a hung exporter costs the app one blockMax, not one per
// record. The latch's first setting logs one WARN per destination
// (dropCounter.stalled): the overflow WARN says the queue is full, this
// one that the exporter stopped.

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

// spanQueueHeadroom is the SDK span queue's room above admission's size.
// The span BatchSpanProcessor's ForceFlush puts a marker into the same
// channel as the spans; admission counts spans, not markers, so without
// the headroom a flush under a full queue would leave an admitted span
// no place — the SDK drops it silently (the root, likely: the only tier
// admitted at the brim) and its place is never released. Each
// concurrent flush holds one marker; eight is more than the pipeline's
// flushes ever overlap. The log BatchProcessor queues no marker.
const spanQueueHeadroom = 8

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
	// wait is one Blocking wait's bound (blockMax; tests shorten it).
	wait time.Duration
	// stalled latches a timed-out Blocking wait: until the next release
	// a Blocking chain refuses at once instead of waiting again.
	stalled atomic.Bool
	// onStall, when set, is told each time the latch sets (the
	// destination's once-only WARN).
	onStall func()
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
	return &admission{size: int64(size), blocking: blocking, wait: blockMax,
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
	if !a.blocking || t == tierDelta || a.stalled.Load() {
		return false
	}
	timer := time.NewTimer(a.wait)
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
			// No export began for a whole wait: the exporter is stuck.
			// Every later record is refused at once until one does. The
			// timer is the only bound of the chain's own making — a span
			// End's context has no deadline (admittedSpanProc.OnEnd) —
			// so a hung exporter always reaches this branch.
			if a.stalled.CompareAndSwap(false, true) && a.onStall != nil {
				a.onStall()
			}
			return false
		case <-ctx.Done():
			return false
		case <-a.done:
			return false
		}
	}
}

// release gives back n places: the records began their export (the
// exporter is moving again: a stalled Blocking chain waits again).
func (a *admission) release(n int) {
	a.inflight.Add(-int64(n))
	a.stalled.Store(false)
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

// newAdmittedLogBatch is a batch log chain's SDK processor over exp:
// admission sized queue, the SDK queue the same size, each export
// releasing its records' places, a stall warned through drops. The
// destination's destProc admits ahead of it (buildDest; the tests'
// logChain).
func newAdmittedLogBatch(exp sdklog.Exporter, queue int, blocking bool, drops *dropCounter,
	opts ...sdklog.BatchProcessorOption) (*sdklog.BatchProcessor, *admission) {
	adm := newAdmission(queue, blocking)
	adm.onStall = drops.stalled
	opts = append([]sdklog.BatchProcessorOption{sdklog.WithMaxQueueSize(int(adm.size))}, opts...)
	return sdklog.NewBatchProcessor(releasingLogExporter{Exporter: exp, adm: adm}, opts...), adm
}

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

// newAdmittedSpanProc is a batch span chain over exp: admission sized
// queue, the SDK queue sized queue + spanQueueHeadroom (the flush
// markers' room), each export releasing its spans' places.
func newAdmittedSpanProc(exp sdktrace.SpanExporter, queue int, blocking bool, drops *dropCounter,
	opts ...sdktrace.BatchSpanProcessorOption) admittedSpanProc {
	adm := newAdmission(queue, blocking)
	adm.onStall = drops.stalled
	opts = append([]sdktrace.BatchSpanProcessorOption{sdktrace.WithMaxQueueSize(int(adm.size) + spanQueueHeadroom)}, opts...)
	bsp := sdktrace.NewBatchSpanProcessor(releasingSpanExporter{SpanExporter: exp, adm: adm}, opts...)
	return admittedSpanProc{SpanProcessor: bsp, adm: adm, drops: drops}
}

func (p admittedSpanProc) OnEnd(s sdktrace.ReadOnlySpan) {
	if s.SpanContext().IsSampled() {
		// A span's End carries no context: a Blocking wait is bounded
		// by admission's own timer alone (the record side waits under
		// the run's context too). No deadline of its own here: one set
		// to the same wait fires first and returns without the latch,
		// so every later End would pay the wait again.
		if !p.adm.admit(context.Background(), spanTier(s)) {
			p.drops.overflowed(1, p.adm.size)
			return
		}
	}
	p.SpanProcessor.OnEnd(s)
}

func (p admittedSpanProc) ForceFlush(ctx context.Context) error {
	return flushDetached(ctx, p.adm, p.SpanProcessor.ForceFlush)
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
// than ctx. The detached flush ends with the chain: its context is
// cancelled when the chain shuts down (adm.done), so a flush whose
// answer a shut-down processor never sends (sdklog's flush then waits on
// its response and its context alone) is a bounded goroutine, not a
// leak.
func flushDetached(ctx context.Context, adm *admission, flush func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	go func() {
		select {
		case <-adm.done:
			cancel()
		case <-fctx.Done():
		}
	}()
	done := make(chan error, 1)
	go func() {
		defer cancel()
		done <- flush(fctx)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
