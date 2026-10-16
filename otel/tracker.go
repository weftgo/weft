package otel

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// The run tracker keeps the set of open runs — added on a run_start
// record, removed on a run_finish record or when the run's
// invoke_agent span ends (a failed run has no run_finish) — and emits
// one weft.heartbeat record per open run every interval: without it a
// long quiet tool call would look interrupted (S2.4). A crashed process
// stops heartbeating, so obsdb's InterruptedAfter rule stays honest.
//
// The tracker is itself a processor on the provider, so it must answer
// Enabled like every other one: false for the pure-content kinds
// (weft.messages, weft.prompt, weft.tools — a tracker that said yes
// would make every pipeline capture content), true for the rest.
// Heartbeats pass through it and are ignored — no recursion.
type runTracker struct {
	mu   sync.Mutex
	open map[string]map[string]string // runID → metadata attributes
}

func newRunTracker() *runTracker {
	return &runTracker{open: map[string]map[string]string{}}
}

var _ sdklog.Processor = (*runTracker)(nil)
var _ sdktrace.SpanProcessor = (*trackerSpanProc)(nil)

// Enabled answers false for the pure-content kinds — weft.messages,
// weft.prompt, weft.tools (the tracker wants no content; a single
// content-off pipeline must not capture) — and true for everything
// else.
func (t *runTracker) Enabled(_ context.Context, param sdklog.EnabledParameters) bool {
	// weft.raw too: only a destination's chain may ask for wire bodies
	// (RawBodies), never the tracker on every pipeline's behalf.
	// The slog bridge's lines neither: they name no run (an app log is
	// attributed through its span, never tracked).
	return !contentEvent(param.EventName) && param.EventName != eventNameRaw &&
		param.InstrumentationScope.Name != bridgeScope
}

func (t *runTracker) OnEmit(_ context.Context, r *sdklog.Record) error {
	kind := recordKind(r)
	if kind == "heartbeat" {
		return nil // its own output; ignoring it is the no-recursion rule
	}
	runID, eventType := trackerIdentity(r)
	if runID == "" {
		return nil
	}
	switch {
	case kind == "event" && eventType == "run_start":
		// The run's metadata is run_start's string attributes minus
		// the record contract's own keys: a heartbeat is not a
		// run_start (weft.event.type) and has no body (weft.content).
		meta := map[string]string{}
		r.WalkAttributes(func(kv attribute.KeyValue) bool {
			switch kv.Key {
			case "weft.record", "weft.run.id", "weft.event.type", "weft.content":
				return true
			}
			if v, ok := attrString(kv); ok {
				meta[string(kv.Key)] = v
			}
			return true
		})
		t.mu.Lock()
		t.open[runID] = meta
		t.mu.Unlock()
	case kind == "event" && eventType == "run_finish":
		t.remove(runID)
	}
	return nil
}

func (t *runTracker) Shutdown(context.Context) error   { return nil }
func (t *runTracker) ForceFlush(context.Context) error { return nil }

func (t *runTracker) remove(runID string) {
	t.mu.Lock()
	delete(t.open, runID)
	t.mu.Unlock()
}

// snapshot returns the open runs with their metadata.
func (t *runTracker) snapshot() map[string]map[string]string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]map[string]string, len(t.open))
	for k, v := range t.open {
		out[k] = v
	}
	return out
}

func attrString(kv attribute.KeyValue) (string, bool) {
	if kv.Value.Type() == attribute.STRING {
		return kv.Value.AsString(), true
	}
	return "", false
}

// trackerIdentity reads the run id and event type off a record.
func trackerIdentity(r *sdklog.Record) (runID, eventType string) {
	r.WalkAttributes(func(kv attribute.KeyValue) bool {
		switch kv.Key {
		case "weft.run.id":
			runID, _ = attrString(kv)
		case "weft.event.type":
			eventType, _ = attrString(kv)
		}
		return true
	})
	return runID, eventType
}

// trackerSpanProc removes a run from the open set when its
// invoke_agent span ends — the failed-run path (no run_finish record).
type trackerSpanProc struct {
	t *runTracker
}

func (p *trackerSpanProc) OnStart(context.Context, sdktrace.ReadWriteSpan) {}

func (p *trackerSpanProc) OnEnd(s sdktrace.ReadOnlySpan) {
	if s.Name() == "" {
		return
	}
	attrs := s.Attributes()
	var runID, op string
	for _, kv := range attrs {
		switch kv.Key {
		case "weft.run.id":
			runID, _ = attrString(kv)
		case "gen_ai.operation.name":
			op, _ = attrString(kv)
		}
	}
	if runID == "" {
		return
	}
	if op == "invoke_agent" || isInvokeAgentName(s.Name()) {
		p.t.remove(runID)
	}
}

func (p *trackerSpanProc) Shutdown(context.Context) error   { return nil }
func (p *trackerSpanProc) ForceFlush(context.Context) error { return nil }

// runEndSampler makes the tracker see every run end. The SDK never
// shows a dropped span to a processor, so a sampled-out run that failed
// or was cancelled (no run_finish record) would stay in the open set
// for the life of the process — heartbeating, reading running in every
// sink. A Drop decision on an invoke_agent span becomes RecordOnly: the
// span reaches trackerSpanProc.OnEnd, and is still never exported (the
// simple and batch processors skip unsampled spans) nor sampled
// downstream (its flags are unchanged).
type runEndSampler struct{ sdktrace.Sampler }

func (s runEndSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	res := s.Sampler.ShouldSample(p)
	if res.Decision == sdktrace.Drop && isInvokeAgentName(p.Name) {
		res.Decision = sdktrace.RecordOnly
	}
	return res
}

// isInvokeAgentName matches the span-name form ("invoke_agent <agent>")
// when the operation attribute was dropped.
func isInvokeAgentName(name string) bool {
	return len(name) >= len("invoke_agent") && name[:len("invoke_agent")] == "invoke_agent"
}

// heartbeats emits one weft.heartbeat record per open run through the
// provider's logger: weft.run.id plus the run's metadata, no body, no
// position. Sinks never store it; it only moves last_seen. A closed
// stop ends the pass before its next record: each emit may be a
// synchronous write (the Local sink) queued behind every run's, and
// Shutdown waits for this goroutine.
//
// bridged is the pipeline's slog bridge count (Pipeline.bridged): with
// a bridge, every heartbeat carries weft.logs.bridged.
func heartbeats(ctx context.Context, emit log.Logger, t *runTracker, stop <-chan struct{}, bridged func() (int64, bool)) {
	for runID, meta := range t.snapshot() {
		select {
		case <-stop:
			return
		default:
		}
		var r log.Record
		r.SetTimestamp(time.Now())
		r.SetEventName("weft.heartbeat")
		attrs := []attribute.KeyValue{
			attribute.String("weft.record", "heartbeat"),
			attribute.String("weft.run.id", runID),
		}
		for k, v := range meta {
			attrs = append(attrs, attribute.String(k, v))
		}
		if bridged != nil {
			if n, ok := bridged(); ok {
				attrs = append(attrs, attribute.Int64(attrLogsBridged, n))
			}
		}
		r.AddAttributes(attrs...)
		emit.Emit(ctx, r)
	}
}

// bridgeSpanProc stamps weft.logs.bridged on every run's invoke_agent
// span as it starts, while the pipeline has a slog bridge: the durable
// mark the logs route reads (obsdb.LogPage.NoBridge) — a stored root
// without it ran with no bridge, so an empty logs page says why.
type bridgeSpanProc struct{ p *Pipeline }

func (b bridgeSpanProc) OnStart(_ context.Context, s sdktrace.ReadWriteSpan) {
	if !isInvokeAgentName(s.Name()) {
		return
	}
	if n, ok := b.p.bridged(); ok {
		s.SetAttributes(attribute.Int64(attrLogsBridged, n))
	}
}

func (bridgeSpanProc) OnEnd(sdktrace.ReadOnlySpan)      {}
func (bridgeSpanProc) Shutdown(context.Context) error   { return nil }
func (bridgeSpanProc) ForceFlush(context.Context) error { return nil }
