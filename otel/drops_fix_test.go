package otel

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// The U5 review's fixes (admission.go, proc.go): the flush marker's
// room, the drop count on the run's bracket, the Blocking latch, and no
// place leaked whatever the exporter or the flush does.

// gateSpanExporter blocks every export until open is closed, saying on
// entered (non-blocking) that one began.
type gateSpanExporter struct {
	entered chan struct{}
	open    chan struct{}
	mu      sync.Mutex
	n       int
}

func newGateSpanExporter() *gateSpanExporter {
	return &gateSpanExporter{entered: make(chan struct{}, 1), open: make(chan struct{})}
}

func (g *gateSpanExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	select {
	case g.entered <- struct{}{}:
	default:
	}
	<-g.open
	g.mu.Lock()
	g.n += len(spans)
	g.mu.Unlock()
	return nil
}
func (g *gateSpanExporter) Shutdown(context.Context) error { return nil }
func (g *gateSpanExporter) exported() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.n
}

// endSpans ends n spans on tr: invoke_agent roots when root, else
// ordinary (durable-tier) spans.
func endSpans(tr trace.Tracer, n int, root bool) {
	op := "chat"
	if root {
		op = "invoke_agent"
	}
	for range n {
		_, s := tr.Start(context.Background(), op, trace.WithAttributes(attribute.String("gen_ai.operation.name", op)))
		s.End()
	}
}

// waitFor polls cond for up to 5 s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestSpanFlushMarkerDropsNothing is the review's finding 1: the SDK's
// span ForceFlush queues a marker in the spans' own channel. A flush
// under a full queue — the exporter busy, a batch just released, the
// channel at admission's brim — used to leave the root admitted last no
// place: the SDK dropped it silently and its place was never released.
// The SDK queue's headroom holds the marker: every admitted span is
// exported, none refused, no place leaked, and the queue admits as much
// afterwards as before.
func TestSpanFlushMarkerDropsNothing(t *testing.T) {
	const queue = 8
	exp := newGateSpanExporter()
	drops := newDropCounter("t")
	proc := newAdmittedSpanProc(exp, queue, false, drops,
		sdktrace.WithMaxExportBatchSize(4), sdktrace.WithBatchTimeout(time.Hour))
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(proc))
	tr := tp.Tracer("t")
	defer func() {
		select {
		case <-exp.open:
		default:
			close(exp.open)
		}
		_ = tp.Shutdown(context.Background())
	}()

	// A full batch: the processor takes it, the export releases its four
	// places and hangs.
	endSpans(tr, 4, false)
	select {
	case <-exp.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first batch never reached the exporter")
	}
	// The channel to the durable limit (7 of 8), then a flush's marker.
	endSpans(tr, queue-1, false)
	flushed := make(chan error, 1)
	go func() { flushed <- proc.ForceFlush(context.Background()) }()
	time.Sleep(50 * time.Millisecond) // the marker joins the channel
	// The root, admitted at the brim: the channel's ninth item.
	endSpans(tr, 1, true)
	if got := proc.adm.refused.Load(); got != 0 {
		t.Fatalf("admission refused %d spans, want 0 (test bug: the queue was not at the brim)", got)
	}
	close(exp.open)
	select {
	case err := <-flushed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ForceFlush never returned")
	}
	if err := proc.ForceFlush(context.Background()); err != nil { // what followed the marker
		t.Fatal(err)
	}
	if got, want := exp.exported(), 4+queue-1+1; got != want {
		t.Errorf("exported %d spans, want %d: the SDK dropped an admitted span (the flush marker took its place)", got, want)
	}
	if n := proc.adm.inflight.Load(); n != 0 {
		t.Errorf("in flight after the flush = %d, want 0: a place leaked", n)
	}
	// The queue's whole size is still admitted.
	endSpans(tr, queue, true)
	if err := proc.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := proc.adm.refused.Load(); got != 0 {
		t.Errorf("refused %d spans after the flush, want 0: the queue shrank", got)
	}
	if n := proc.adm.inflight.Load(); n != 0 {
		t.Errorf("in flight = %d, want 0", n)
	}
	if got := drops.lost(); got != 0 {
		t.Errorf("drops = %d, want 0", got)
	}
}

// eventRecord is a weft event record of the given type.
func eventRecord(t *testing.T, typ string) *sdklog.Record {
	t.Helper()
	return sdkRecordWith(t, "weft.event", `{"type":"`+typ+`"}`,
		attribute.String("weft.record", "event"), attribute.String("weft.event.type", typ),
		attribute.String("weft.run.id", "r"))
}

// droppedOf is a record's weft.records.dropped.
func droppedOf(r sdklog.Record) (int64, bool) {
	var n int64
	var ok bool
	r.WalkAttributes(func(kv attribute.KeyValue) bool {
		if string(kv.Key) == attrRecordsDropped {
			n, ok = kv.Value.AsInt64(), true
		}
		return true
	})
	return n, ok
}

// TestRunBracketCarriesDestinationDrops (finding 2): run_start and
// run_finish carry the destination's loss so far, as the heartbeat does
// — so every run's end reports it, however short the run; no other
// event does, and none carries it while nothing was lost.
func TestRunBracketCarriesDestinationDrops(t *testing.T) {
	mem := newMemExporter()
	drops := newDropCounter("studio")
	p := &destProc{name: "studio", inner: sdklog.NewSimpleProcessor(mem), content: true, drops: drops}
	emit := func(typ string) (int64, bool) {
		_ = p.OnEmit(context.Background(), eventRecord(t, typ))
		recs := mem.snapshot()
		return droppedOf(recs[len(recs)-1])
	}
	if _, ok := emit("run_start"); ok {
		t.Error("run_start carries weft.records.dropped before any loss")
	}
	drops.filtered(4)
	if _, ok := emit("run_finish"); ok {
		t.Error("run_finish carries weft.records.dropped after policy filtering alone")
	}
	drops.overflowed(3, 64)
	for _, typ := range []string{"run_start", "run_finish"} {
		if n, ok := emit(typ); !ok || n != 3 {
			t.Errorf("%s: weft.records.dropped = %d (set %v), want 3", typ, n, ok)
		}
	}
	if _, ok := emit("step_start"); ok {
		t.Error("step_start carries weft.records.dropped: only the bracket and the heartbeat do")
	}
}

// hangingSpanExporter blocks every export until its context ends.
type hangingSpanExporter struct{}

func (hangingSpanExporter) ExportSpans(ctx context.Context, _ []sdktrace.ReadOnlySpan) error {
	<-ctx.Done()
	return ctx.Err()
}
func (hangingSpanExporter) Shutdown(context.Context) error { return nil }

// hangingLogExporter is its log twin.
type hangingLogExporter struct{}

func (hangingLogExporter) Export(ctx context.Context, _ []sdklog.Record) error {
	<-ctx.Done()
	return ctx.Err()
}
func (hangingLogExporter) Shutdown(context.Context) error   { return nil }
func (hangingLogExporter) ForceFlush(context.Context) error { return nil }

type sleepySpanExporter struct{ d time.Duration }

func (s sleepySpanExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	time.Sleep(s.d)
	return nil
}
func (sleepySpanExporter) Shutdown(context.Context) error { return nil }

// logChain is a batch log chain as buildDest assembles it, over exp,
// with the SDK options given.
func logChain(exp sdklog.Exporter, queue int, blocking bool, opts ...sdklog.BatchProcessorOption) *destProc {
	adm := newAdmission(queue, blocking)
	opts = append([]sdklog.BatchProcessorOption{sdklog.WithMaxQueueSize(queue)}, opts...)
	return &destProc{name: "t", content: true, drops: newDropCounter("t"), adm: adm,
		inner: sdklog.NewBatchProcessor(releasingLogExporter{Exporter: exp, adm: adm}, opts...)}
}

// TestNoPlaceLeaked (finding 5b): a failing exporter, a timing-out one,
// and a ForceFlush whose context is cut leave no admission place behind
// — on the log chain and the span chain — and the queue admits its
// whole size afterwards.
func TestNoPlaceLeaked(t *testing.T) {
	t.Run("failing", func(t *testing.T) {
		p, err := Start(testCtx(t), NoGlobal(), NoEnv(), Heartbeat(0),
			Exporters(&spanCapture{fail: errors.New("down")}, &failingLogExporter{}, QueueSize(512), BatchDelay(time.Hour)))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = p.Shutdown(testCtx(t)) }()
		logAdm, spanAdm := p.dests[0].logProc.(*destProc).adm, p.dests[0].spanProc.(admittedSpanProc).adm
		for range 3 {
			if _, err := stormAgent(3, 10, 2, core.TracerProvider(p.TracerProvider()),
				core.LoggerProvider(p.LoggerProvider())).Generate(context.Background(), core.Prompt("x")); err != nil {
				t.Fatal(err)
			}
			_ = p.ForceFlush(testCtx(t)) // the exports fail: an error, counted
			if l, s := logAdm.inflight.Load(), spanAdm.inflight.Load(); l != 0 || s != 0 {
				t.Fatalf("in flight after failed exports = %d records, %d spans; want 0", l, s)
			}
		}
		if l, s := logAdm.refused.Load(), spanAdm.refused.Load(); l != 0 || s != 0 {
			t.Errorf("refused %d records, %d spans: failed exports leaked places", l, s)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		const queue = 16
		lp := logChain(hangingLogExporter{}, queue, false, sdklog.WithExportTimeout(10*time.Millisecond),
			sdklog.WithExportInterval(time.Hour), sdklog.WithExportMaxBatchSize(4))
		defer func() { _ = lp.Shutdown(context.Background()) }()
		sp := newAdmittedSpanProc(hangingSpanExporter{}, queue, false, newDropCounter("t"),
			sdktrace.WithExportTimeout(10*time.Millisecond), sdktrace.WithBatchTimeout(time.Hour), sdktrace.WithMaxExportBatchSize(4))
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sp))
		defer func() { _ = tp.Shutdown(context.Background()) }()
		for range 3 {
			for range queue - queue/8 {
				_ = lp.OnEmit(context.Background(), eventRecord(t, "step_start"))
			}
			endSpans(tp.Tracer("t"), queue-queue/8, false)
			_ = lp.ForceFlush(context.Background())
			_ = sp.ForceFlush(context.Background())
			waitFor(t, "the timed-out exports to release", func() bool {
				return lp.adm.inflight.Load() == 0 && sp.adm.inflight.Load() == 0
			})
		}
		if l, s := lp.adm.refused.Load(), sp.adm.refused.Load(); l != 0 || s != 0 {
			t.Errorf("refused %d records, %d spans: timed-out exports leaked places", l, s)
		}
	})

	t.Run("flush_cut", func(t *testing.T) {
		// The log flush exports its records in chunks of the batch size
		// and stops between chunks when its context ends: the records of
		// the chunks it skipped would never be exported nor released.
		// The first export hangs until the flush is waiting; the flush's
		// first chunk then cuts the flush's context.
		const queue = 64
		ctx, cut := context.WithCancel(context.Background())
		defer cut()
		exp := &cuttingExporter{gate: make(chan struct{}), cut: cut, mem: newMemExporter()}
		lp := logChain(exp, queue, false, sdklog.WithExportInterval(time.Hour), sdklog.WithExportMaxBatchSize(4))
		defer func() { _ = lp.Shutdown(context.Background()) }()
		const n = 40
		for range n {
			_ = lp.OnEmit(context.Background(), eventRecord(t, "step_start"))
		}
		flushed := make(chan error, 1)
		go func() { flushed <- lp.ForceFlush(ctx) }()
		time.Sleep(20 * time.Millisecond) // the flush request waits on the busy processor
		close(exp.gate)
		<-flushed
		waitFor(t, "the cut flush to finish its chunks", func() bool { return lp.adm.inflight.Load() == 0 })
		waitFor(t, "every record exported", func() bool { return len(exp.mem.snapshot()) == n })
		for range queue - queue/8 {
			_ = lp.OnEmit(context.Background(), eventRecord(t, "step_start"))
		}
		if got := lp.adm.refused.Load(); got != 0 {
			t.Errorf("refused %d records after a cut flush: places leaked", got)
		}

		// The span flush exports its batch in one call: a cut context
		// still leaves every span exported and released.
		sp := newAdmittedSpanProc(sleepySpanExporter{d: 5 * time.Millisecond}, queue, false, newDropCounter("t"),
			sdktrace.WithBatchTimeout(time.Hour), sdktrace.WithMaxExportBatchSize(4))
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sp))
		defer func() { _ = tp.Shutdown(context.Background()) }()
		endSpans(tp.Tracer("t"), n, false)
		sctx, scancel := context.WithTimeout(context.Background(), time.Millisecond)
		_ = sp.ForceFlush(sctx)
		scancel()
		waitFor(t, "the cut span flush to finish", func() bool { return sp.adm.inflight.Load() == 0 })
		endSpans(tp.Tracer("t"), queue-queue/8, false)
		if got := sp.adm.refused.Load(); got != 0 {
			t.Errorf("refused %d spans after a cut flush: places leaked", got)
		}
	})
}

// cuttingExporter hangs its first export until gate closes and cuts
// the flush's context (cut) on its second — the flush's first chunk.
type cuttingExporter struct {
	gate  chan struct{}
	cut   func()
	calls atomic.Int64
	mem   *memExporter
}

func (c *cuttingExporter) Export(ctx context.Context, recs []sdklog.Record) error {
	switch c.calls.Add(1) {
	case 1:
		<-c.gate
	case 2:
		c.cut()
	}
	return c.mem.Export(ctx, recs)
}
func (*cuttingExporter) Shutdown(context.Context) error   { return nil }
func (*cuttingExporter) ForceFlush(context.Context) error { return nil }

// TestBlockingLatch (finding 3): one timed-out Blocking wait latches the
// chain — every later record is refused at once, not after its own
// wait — until an export begins and releases places; then a record
// waits again.
func TestBlockingLatch(t *testing.T) {
	a := newAdmission(2, true)
	a.wait = 30 * time.Millisecond
	if !a.admit(context.Background(), tierDurable) || !a.admit(context.Background(), tierPriority) {
		t.Fatal("the first two records were refused")
	}
	start := time.Now()
	if a.admit(context.Background(), tierPriority) {
		t.Fatal("a full queue admitted a record")
	}
	if el := time.Since(start); el < a.wait {
		t.Errorf("the first refusal took %v, want a whole wait (%v)", el, a.wait)
	}
	if !a.stalled.Load() {
		t.Fatal("a timed-out wait did not latch")
	}
	start = time.Now()
	for range 10 {
		if a.admit(context.Background(), tierPriority) {
			t.Fatal("a latched full queue admitted a record")
		}
	}
	if el := time.Since(start); el >= a.wait {
		t.Errorf("ten latched refusals took %v, want each at once (< one wait, %v)", el, a.wait)
	}
	a.release(1)
	if a.stalled.Load() {
		t.Error("a release did not clear the latch")
	}
	if !a.admit(context.Background(), tierPriority) {
		t.Error("the released place was refused")
	}
	// Unlatched, a record waits again — and is admitted when a release
	// comes during its wait.
	a.wait = 5 * time.Second
	go func() { time.Sleep(20 * time.Millisecond); a.release(1) }()
	if !a.admit(context.Background(), tierPriority) {
		t.Error("a waiting record was not admitted on release")
	}
	if got, want := a.refused.Load(), int64(11); got != want {
		t.Errorf("refused = %d, want %d", got, want)
	}
}

// TestBlockingBoundPerStall (finding 3): a hung exporter under Blocking
// costs the app one wait, not one per record — on the record chain
// (bounded by the wait and the emitting context) and the span chain
// (whose End has no context: its own deadline bounds it).
func TestBlockingBoundPerStall(t *testing.T) {
	const wait = 50 * time.Millisecond
	const n = 60 // unbounded per record: n × wait = 3 s
	gate := &gateExporter{open: make(chan struct{}), mem: newMemExporter()}
	lp := logChain(gate, 4, true, sdklog.WithExportInterval(time.Hour), sdklog.WithExportMaxBatchSize(4))
	lp.adm.wait = wait
	spans := newGateSpanExporter()
	sp := newAdmittedSpanProc(spans, 4, true, newDropCounter("t"),
		sdktrace.WithBatchTimeout(time.Hour), sdktrace.WithMaxExportBatchSize(4))
	sp.adm.wait = wait
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sp))
	defer func() {
		close(gate.open)
		close(spans.open)
		_ = lp.Shutdown(context.Background())
		_ = tp.Shutdown(context.Background())
	}()

	start := time.Now()
	for range n {
		_ = lp.OnEmit(context.Background(), eventRecord(t, "step_start"))
	}
	if el := time.Since(start); el > 20*wait {
		t.Errorf("%d records through a hung Blocking chain took %v, want about one wait (%v)", n, el, wait)
	}
	if lp.adm.refused.Load() == 0 || !lp.adm.stalled.Load() {
		t.Errorf("record chain: refused %d, stalled %v; want refusals and the latch", lp.adm.refused.Load(), lp.adm.stalled.Load())
	}

	start = time.Now()
	endSpans(tp.Tracer("t"), n, false)
	if el := time.Since(start); el > 20*wait {
		t.Errorf("%d span ends through a hung Blocking chain took %v, want about one wait (%v)", n, el, wait)
	}
	if sp.adm.refused.Load() == 0 || !sp.adm.stalled.Load() {
		t.Errorf("span chain: refused %d, stalled %v; want refusals and the latch", sp.adm.refused.Load(), sp.adm.stalled.Load())
	}
}

// TestOverflowWarnsOncePerDestination (finding 5d): two destinations
// that overflow log one WARN each, naming each.
func TestOverflowWarnsOncePerDestination(t *testing.T) {
	logBuf := captureSlogSafe(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer ts.Close()
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(), Heartbeat(0),
		Studio(ts.URL, "", QueueSize(64), BatchDelay(time.Hour)),
		Exporters(&spanCapture{}, newMemExporter(), QueueSize(64), BatchDelay(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(testCtx(t)) }()
	for range 2 {
		if _, err := stormAgent(10, 50, 3, core.TracerProvider(p.TracerProvider()),
			core.LoggerProvider(p.LoggerProvider())).Generate(context.Background(), core.Prompt("storm")); err != nil {
			t.Fatal(err)
		}
	}
	out := logBuf.String()
	if n := strings.Count(out, "destination queue full"); n != 2 {
		t.Errorf("queue-full WARN lines = %d, want 2 (one per destination):\n%s", n, out)
	}
	for _, d := range []string{"dest=studio", "dest=exporters"} {
		if strings.Count(out, d) != 1 {
			t.Errorf("WARN lines naming %s = %d, want 1:\n%s", d, strings.Count(out, d), out)
		}
	}
}

// TestDetachedFlushEndsWithTheChain (finding 6a): a detached flush
// whose answer never comes — the processor shut down between the
// request and its response — ends when the chain shuts down: its
// context is cancelled, so its goroutine is bounded, not leaked.
func TestDetachedFlushEndsWithTheChain(t *testing.T) {
	adm := newAdmission(1, false)
	ctx, cancel := context.WithCancel(context.Background())
	started, ended := make(chan struct{}), make(chan struct{})
	returned := make(chan error, 1)
	go func() {
		returned <- flushDetached(ctx, adm, func(c context.Context) error {
			close(started)
			<-c.Done() // sdklog's flush: its response or its context, nothing else
			close(ended)
			return c.Err()
		})
	}()
	<-started
	cancel() // the caller stops waiting; the detached flush goes on
	if err := <-returned; !errors.Is(err, context.Canceled) {
		t.Errorf("flushDetached = %v, want the caller's context.Canceled", err)
	}
	select {
	case <-ended:
		t.Fatal("the detached flush ended with its caller's context: it is not detached")
	case <-time.After(20 * time.Millisecond):
	}
	adm.close() // the chain shuts down
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the detached flush outlived the chain's shutdown")
	}
}
