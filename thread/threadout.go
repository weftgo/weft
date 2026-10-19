package thread

import (
	"context"
	"fmt"
	"maps"
	"sync"

	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

// The thread records' emitter (record contract §4, kind thread). A
// record is decided under s.mu — its kind, fields, the run it is filed
// under, that run's span and metadata — and queued; the provider is
// called only by the emitter, one goroutine per Session started on the
// first queued record and gone once the queue is empty, so no
// LoggerProvider — an obsdb insert, a Blocking() destination, a slow
// or panicking exporter — ever runs while s.mu is held, and a reader
// (Pending, Entries) never waits on one. The queue is FIFO and drained
// by that one goroutine: records leave in the order they were queued —
// the order the session decided them, except a held record (one waiting
// for its run to report, heldRec), which joins the queue when that run
// reports or at Close, after records decided later than it; the held
// ones keep their own decided order among themselves. Close waits for
// the emitter (bounded by its context).

// heldRec is a record waiting for a run to be filed under: run is the
// run it waits for ("" for the next run that reports).
type heldRec struct {
	rec threadRec
	run string
}

// outItem is one decided record: the span context it is filed under
// (ctx, never a caller's), the run, that run's metadata, and whether a
// Close emitted it because no run reported to take it.
type outItem struct {
	ctx  context.Context
	run  string
	md   map[string]string
	rec  threadRec
	held bool
}

// outbox is the emitter's queue. Its own mutex: it is taken under s.mu
// (enqueue) and by the emitter alone (drain), never the other way.
type outbox struct {
	mu    sync.Mutex
	queue []outItem
	busy  bool
	idle  chan struct{} // closed when the running emitter drains; nil when none ran
}

// queueRecLocked queues one record and starts the emitter when none
// runs. Callers hold s.mu.
func (s *Session) queueRecLocked(it outItem) {
	if it.md != nil {
		it.md = maps.Clone(it.md)
	}
	o := &s.out
	o.mu.Lock()
	o.queue = append(o.queue, it)
	start := !o.busy
	if start {
		o.busy = true
		o.idle = make(chan struct{})
	}
	o.mu.Unlock()
	if start {
		go s.drainOut()
	}
}

// drainOut is the emitter: it writes the queue in order until it is
// empty, then exits.
func (s *Session) drainOut() {
	o := &s.out
	for {
		o.mu.Lock()
		if len(o.queue) == 0 {
			o.busy = false
			close(o.idle)
			o.mu.Unlock()
			return
		}
		batch := o.queue
		o.queue = nil
		o.mu.Unlock()
		for _, it := range batch {
			s.emitItem(it)
		}
	}
}

// waitOut waits until the emitter has written everything queued so far
// — and whatever is queued meanwhile — or ctx ends.
func (s *Session) waitOut(ctx context.Context) {
	o := &s.out
	o.mu.Lock()
	busy, idle := o.busy, o.idle
	o.mu.Unlock()
	if !busy {
		return
	}
	select {
	case <-idle:
	case <-ctx.Done():
	}
}

// emitItem writes one decided record. Observation only: a provider that
// panics or refuses changes nothing the session does — the panic is
// contained into a Debug line, the record dropped.
func (s *Session) emitItem(it outItem) {
	defer func() {
		if p := recover(); p != nil {
			s.agent.Logger().Debug("thread: session record dropped", "session", s.header.ID, "kind", it.rec.kind, "panic", fmt.Sprint(p))
		}
	}()
	l := s.threadLogger()
	if l == nil || !l.Enabled(it.ctx, log.EnabledParameters{EventName: threadEventName(it.rec.kind)}) {
		return
	}
	s.emitThread(it.ctx, l, it.run, it.md, it.rec, it.held)
}

// closeItemLocked is a held record as Close emits it (held: true), on
// a context carrying no span: under the run it waited for, with
// that turn's metadata; with no run, filed under none — weft.run.id
// absent — and keyed by the session's metadata alone (weft.session.id
// and the rest of its identity, no weft.turn). Callers hold s.mu.
func (s *Session) closeItemLocked(h heldRec) outItem {
	// Always spanless: context.Background(), no trace or span id — a
	// held record must never read as a line under some run's trace.
	if h.run != "" {
		it := s.itemForRunLocked(h.run, h.rec)
		it.ctx, it.held = context.Background(), true
		return it
	}
	md := s.unseenRunMetadataLocked(0)
	delete(md, "weft.turn")
	return outItem{ctx: context.Background(), md: md, rec: h.rec, held: true}
}

// flushHeldLocked queues what seeRun took: mine under the reporting
// run (on its span, with its metadata), stale ones spanless under the
// run they waited for. Callers hold s.mu.
func (s *Session) flushHeldLocked(rctx context.Context, runID string, md map[string]string, mine, stale []heldRec) {
	ctx := trace.ContextWithSpanContext(context.Background(), trace.SpanContextFromContext(rctx))
	for _, h := range mine {
		s.queueRecLocked(outItem{ctx: ctx, run: runID, md: md, rec: h.rec})
	}
	for _, h := range stale {
		s.queueRecLocked(s.itemForRunLocked(h.run, h.rec))
	}
}

// sealHeldLocked is Close's share of the record family: every record
// still held goes out as a held record (closeItemLocked); the records
// the bound pushed out are counted (dropped). The held summarizer
// calls are dropped and counted (sums): a call held for no run, or for
// a run that never reported by Close, has no span to open under, and
// one opened bare would be a root span in a trace of its own — read as
// a run (record contract §3). Callers hold s.mu.
func (s *Session) sealHeldLocked() (sums, dropped int) {
	for _, h := range s.heldRecs {
		s.queueRecLocked(s.closeItemLocked(h))
	}
	dropped, sums = s.heldDropped, len(s.heldSummaries)
	s.heldRecs, s.heldDropped, s.heldSummaries = nil, 0, nil
	return sums, dropped
}
