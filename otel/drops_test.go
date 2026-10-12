package otel

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// U5 (UX-E-05, UX-A-41): where an event storm's records go missing, and
// what weft does about it.

// stormAgent is an event storm: steps tool steps, each streaming deltas
// text deltas and requesting calls parallel tool calls, then a final
// answer — thousands of deltas and hundreds of durable events in
// milliseconds, as the stress suite's event-storm scenario emits.
func stormAgent(steps, deltas, calls int, opts ...core.Option) *core.Agent {
	echo := core.Tool("echo", "Echoes.", func(context.Context, struct{}) (string, error) { return "ok", nil })
	turns := make([]wefttest.Turn, 0, steps+1)
	for s := range steps {
		evs := make([]core.ModelEvent, 0, deltas+calls+1)
		for range deltas {
			evs = append(evs, core.ModelTextDelta{Text: "x"})
		}
		for c := range calls {
			evs = append(evs, core.ModelToolCall{ID: fmt.Sprintf("c%d_%d", s, c), Name: "echo", Args: []byte(`{}`)})
		}
		evs = append(evs, core.ModelFinish{Reason: core.StopToolCalls, Usage: core.Usage{InputTokens: 1, OutputTokens: 1}})
		turns = append(turns, wefttest.Raw(evs...))
	}
	turns = append(turns, wefttest.Say("done"))
	return core.New(wefttest.Script(turns...), append([]core.Option{core.Name("storm"), echo, core.MaxSteps(steps + 1)}, opts...)...)
}

// captureSlogSafe routes slog.Default to a buffer for the test (the drop
// counters fix their logger at construction: call it before Start).
func captureSlogSafe(t *testing.T) *threadSafeBuffer {
	t.Helper()
	buf := &threadSafeBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// gateExporter blocks every Export until open is closed.
type gateExporter struct {
	open chan struct{}
	mem  *memExporter
}

func (g *gateExporter) Export(ctx context.Context, recs []sdklog.Record) error {
	<-g.open
	return g.mem.Export(ctx, recs)
}
func (g *gateExporter) Shutdown(context.Context) error   { return nil }
func (g *gateExporter) ForceFlush(context.Context) error { return nil }

// TestSDKBatchQueueDropsOldestSilently is the proof of where the stress
// runs' records went (UX-E-05's verify note): the SDK's log
// BatchProcessor, its exporter slower than the emitter, overwrites its
// oldest queued record when the queue is full — OnEmit still answers
// nil, nothing reaches weft's drop counter, and the survivors are the
// newest. A run's run_start (position 0) is the first record such a
// storm loses: the gap the seed batch showed at positions 0–12.
func TestSDKBatchQueueDropsOldestSilently(t *testing.T) {
	gate := &gateExporter{open: make(chan struct{}), mem: newMemExporter()}
	bp := sdklog.NewBatchProcessor(gate, sdklog.WithMaxQueueSize(8), sdklog.WithExportMaxBatchSize(8),
		sdklog.WithExportInterval(time.Hour))
	for i := range 40 {
		var r sdklog.Record
		r.SetEventName(fmt.Sprint(i))
		if err := bp.OnEmit(context.Background(), &r); err != nil {
			t.Fatalf("OnEmit(%d) = %v: the SDK said something", i, err)
		}
	}
	close(gate.open)
	if err := bp.ForceFlush(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range gate.mem.snapshot() {
		got[r.EventName()] = true
	}
	if len(got) >= 40 {
		t.Fatalf("exported %d of 40: the queue did not overflow (test bug)", len(got))
	}
	for i := 32; i < 40; i++ {
		if !got[fmt.Sprint(i)] {
			t.Errorf("record %d (among the newest) was dropped; the SDK keeps the newest", i)
		}
	}
	if got["0"] {
		t.Errorf("record 0 survived; the SDK drops the oldest first (run_start goes first)")
	}
	_ = bp.Shutdown(testCtx(t))
}

// TestQueueOverflowCountedShedDeltasFirst floods a batch destination
// whose queue nothing drains during the run: admission (admission.go)
// lets exactly the queue in — deltas until half full, durable records
// until seven eighths, the run's bracket to the brim — and refuses the
// rest, every refusal counted on the destination's counter, one WARN
// for the destination however many it refused. run_start and run_finish
// survive (the run row's sources), and the events exported plus the
// events refused are the weft.run.events the root span reports.
func TestQueueOverflowCountedShedDeltasFirst(t *testing.T) {
	logBuf := captureSlogSafe(t)
	logs, spans := newMemExporter(), &spanCapture{}
	const queue = 64
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(), Heartbeat(0),
		Exporters(spans, logs, QueueSize(queue), BatchDelay(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	const steps, deltas, calls = 20, 100, 3
	res, err := stormAgent(steps, deltas, calls,
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider())).
		Generate(context.Background(), core.Prompt("storm"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ForceFlush(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	var events, deltasOut int
	types := map[string]int{}
	for _, r := range logs.snapshot() {
		switch recordKind(&r) {
		case "event":
			events++
			r.WalkAttributes(func(kv attribute.KeyValue) bool {
				if string(kv.Key) == "weft.event.type" {
					types[kv.Value.AsString()]++
				}
				return true
			})
		case "delta":
			deltasOut++
		}
	}
	exported := len(logs.snapshot())
	// The queue drains only at the flush: the destination took exactly
	// its durable share plus the run's bracket (run_start was admitted
	// below every limit; run_finish is the one record past seven eighths).
	if want := queue - queue/8 + 1; exported != want {
		t.Errorf("exported %d records, want %d (the durable limit + run_finish)", exported, want)
	}
	if types["run_start"] != 1 || types["run_finish"] != 1 {
		t.Errorf("run_start/run_finish exported = %d/%d, want 1/1: the bracket is shed last", types["run_start"], types["run_finish"])
	}
	if deltasOut >= queue/2 {
		t.Errorf("deltas exported = %d, want fewer than half the queue (%d): deltas are shed first", deltasOut, queue/2)
	}
	emitted := rootEvents(t, spans)
	if emitted <= int64(events) {
		t.Fatalf("weft.run.events = %d, exported %d: the storm lost no event (test bug)", emitted, events)
	}
	drops := p.dests[0].logProc.(*destProc).drops
	// events + the storm's deltas and the final answer's one + one request
	// record per model call
	totalEmitted := emitted + int64(steps*deltas+1) + int64(len(res.Steps))
	// The span chain shares the destination's counter: one root, one
	// chat span per step, one execute_tool span per call — the root
	// among those exported (it is shed last).
	spansEnded := int64(1 + len(res.Steps) + steps*calls)
	spansOut := int64(spans.count())
	logAdm, spanAdm := p.dests[0].logProc.(*destProc).adm, p.dests[0].spanProc.(admittedSpanProc).adm
	if got, want := logAdm.refused.Load(), totalEmitted-int64(exported); got != want {
		t.Errorf("log chain refused %d, want %d (every record emitted and not exported)", got, want)
	}
	if got, want := spanAdm.refused.Load(), spansEnded-spansOut; got != want {
		t.Errorf("span chain refused %d, want %d (every span ended and not exported)", got, want)
	}
	if got := drops.overflow.Load(); got != logAdm.refused.Load()+spanAdm.refused.Load() {
		t.Errorf("overflow counter = %d, want both chains' refusals %d", got, logAdm.refused.Load()+spanAdm.refused.Load())
	}
	if got := drops.lost(); got != drops.overflow.Load() {
		t.Errorf("lost = %d, want the overflow %d", got, drops.overflow.Load())
	}
	if n := strings.Count(logBuf.String(), "destination queue full"); n != 1 {
		t.Errorf("queue-full WARN lines = %d, want 1 per destination:\n%s", n, logBuf.String())
	}
	if !strings.Contains(logBuf.String(), "dest=exporters") || !strings.Contains(logBuf.String(), "otel.QueueSize") {
		t.Errorf("the WARN names neither the destination nor the knob:\n%s", logBuf.String())
	}
	_ = p.Shutdown(testCtx(t))
}

// rootEvents is the storm run's weft.run.events.
func rootEvents(t *testing.T, spans *spanCapture) int64 {
	t.Helper()
	spans.mu.Lock()
	defer spans.mu.Unlock()
	for _, s := range spans.spans {
		if s.Name() != "invoke_agent storm" {
			continue
		}
		for _, kv := range s.Attributes() {
			if kv.Key == "weft.run.events" {
				return kv.Value.AsInt64()
			}
		}
	}
	t.Fatal("no invoke_agent span with weft.run.events")
	return 0
}

// TestHeartbeatCarriesDestinationDrops: a heartbeat through a chain
// that lost records carries the destination's own count as
// weft.records.dropped (record contract §4); one through a chain that
// lost none, or only filtered by policy, carries none.
func TestHeartbeatCarriesDestinationDrops(t *testing.T) {
	heartbeat := func(p *destProc, mem *memExporter) (int64, bool) {
		_ = p.OnEmit(context.Background(), sdkRecordWith(t, eventHeartbeat, "",
			attribute.String("weft.record", "heartbeat"), attribute.String("weft.run.id", "r")))
		recs := mem.snapshot()
		var n int64
		var ok bool
		recs[len(recs)-1].WalkAttributes(func(kv attribute.KeyValue) bool {
			if string(kv.Key) == "weft.records.dropped" {
				n, ok = kv.Value.AsInt64(), true
			}
			return true
		})
		return n, ok
	}
	mem := newMemExporter()
	drops := newDropCounter("studio")
	p := &destProc{name: "studio", inner: sdklog.NewSimpleProcessor(mem), content: true, drops: drops, noDeltas: true}
	drops.filtered(5)
	if _, ok := heartbeat(p, mem); ok {
		t.Error("a heartbeat after policy filtering alone carries weft.records.dropped")
	}
	drops.overflowed(7, 64)
	drops.dropped(2, nil)
	if n, ok := heartbeat(p, mem); !ok || n != 9 {
		t.Errorf("weft.records.dropped = %d (set %v), want 9 (overflow + failed exports, filters excluded)", n, ok)
	}
}

// TestLocalFloodLosesNothing: the Local destination writes each record
// synchronously on the emitting goroutine — no queue — so a storm costs
// it time, never records: every event the root reports emitted is
// stored, and nothing is counted lost.
func TestLocalFloodLosesNothing(t *testing.T) {
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(), Heartbeat(0),
		Local(filepath.Join(t.TempDir(), "weft.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(testCtx(t)) }()
	res, err := stormAgent(10, 200, 3, core.TracerProvider(p.TracerProvider()),
		core.LoggerProvider(p.LoggerProvider())).Generate(context.Background(), core.Prompt("storm"))
	if err != nil {
		t.Fatal(err)
	}
	det, err := p.LocalDB().Run(context.Background(), res.ID)
	if err != nil {
		t.Fatal(err)
	}
	if det.EventsEmitted == 0 || det.EventCount != det.EventsEmitted {
		t.Errorf("stored %d of %d emitted events: Local lost records", det.EventCount, det.EventsEmitted)
	}
	if got := p.dests[0].logProc.(*destProc).drops.lost(); got != 0 {
		t.Errorf("Local counted %d lost", got)
	}
}

// TestBlockingIsDevOnly: Blocking outside WEFT_ENV=dev is refused with
// one WARN and the destination refuses on a full queue as without it;
// under dev it waits, and a storm through a queue of 16 with a slow
// exporter loses no durable record (deltas are still shed).
func TestBlockingIsDevOnly(t *testing.T) {
	logBuf := captureSlogSafe(t)
	env := map[string]string{"WEFT_DISCOVERY": "off"}
	prev := envGetenv
	envGetenv = func(k string) string { return env[k] }
	t.Cleanup(func() { envGetenv = prev })

	p, err := Start(testCtx(t), NoGlobal(), Heartbeat(0),
		Exporters(nil, newMemExporter(), Blocking()))
	if err != nil {
		t.Fatal(err)
	}
	if p.dests[0].logProc.(*destProc).adm.blocking {
		t.Error("Blocking kept outside WEFT_ENV=dev")
	}
	_ = p.Shutdown(testCtx(t))
	if n := strings.Count(logBuf.String(), "Blocking ignored"); n != 1 {
		t.Errorf("Blocking refusal WARN lines = %d, want 1:\n%s", n, logBuf.String())
	}

	env["WEFT_ENV"] = "dev"
	logs, spans := &slowExporter{mem: newMemExporter(), delay: time.Millisecond}, &spanCapture{}
	p, err = Start(testCtx(t), NoGlobal(), Heartbeat(0),
		Exporters(spans, logs, QueueSize(16), Blocking(), BatchDelay(5*time.Millisecond)))
	if err != nil {
		t.Fatal(err)
	}
	if !p.dests[0].logProc.(*destProc).adm.blocking {
		t.Fatal("Blocking refused under WEFT_ENV=dev")
	}
	if _, err := stormAgent(10, 50, 3, core.TracerProvider(p.TracerProvider()),
		core.LoggerProvider(p.LoggerProvider())).Generate(context.Background(), core.Prompt("storm")); err != nil {
		t.Fatal(err)
	}
	if err := p.ForceFlush(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	var events int64
	for _, r := range logs.mem.snapshot() {
		if recordKind(&r) == "event" {
			events++
		}
	}
	if want := rootEvents(t, spans); events != want {
		t.Errorf("blocking destination exported %d of %d events", events, want)
	}
	_ = p.Shutdown(testCtx(t))
}

// slowExporter sleeps per export: a destination slower than the run.
type slowExporter struct {
	mu    sync.Mutex
	mem   *memExporter
	delay time.Duration
}

func (s *slowExporter) Export(ctx context.Context, recs []sdklog.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	time.Sleep(s.delay)
	return s.mem.Export(ctx, recs)
}
func (s *slowExporter) Shutdown(context.Context) error   { return nil }
func (s *slowExporter) ForceFlush(context.Context) error { return nil }
