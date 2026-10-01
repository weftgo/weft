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
// Enabled like every other one: false for weft.messages (a tracker that
// said yes would make every pipeline capture content), true for the
// rest. Heartbeats pass through it and are ignored — no recursion.
type runTracker struct {
	mu   sync.Mutex
	open map[string]map[string]string // runID → metadata attributes
}

func newRunTracker() *runTracker {
	return &runTracker{open: map[string]map[string]string{}}
}

var _ sdklog.Processor = (*runTracker)(nil)
var _ sdktrace.SpanProcessor = (*trackerSpanProc)(nil)

// Enabled answers false for weft.messages (the tracker wants no
// content; a single content-off pipeline must not capture) and true for
// everything else.
func (t *runTracker) Enabled(_ context.Context, param sdklog.EnabledParameters) bool {
	return param.EventName != eventNameMessages
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
		meta := map[string]string{}
		r.WalkAttributes(func(kv attribute.KeyValue) bool {
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

// isInvokeAgentName matches the span-name form ("invoke_agent <agent>")
// when the operation attribute was dropped.
func isInvokeAgentName(name string) bool {
	return len(name) >= len("invoke_agent") && name[:len("invoke_agent")] == "invoke_agent"
}

// heartbeats emits one weft.heartbeat record per open run through the
// provider's logger: weft.run.id plus the run's metadata, no body, no
// position. Sinks never store it; it only moves last_seen.
func heartbeats(ctx context.Context, emit log.Logger, t *runTracker) {
	for runID, meta := range t.snapshot() {
		var r log.Record
		r.SetTimestamp(time.Now())
		r.SetEventName("weft.heartbeat")
		attrs := []attribute.KeyValue{
			attribute.String("weft.record", "heartbeat"),
			attribute.String("weft.run.id", runID),
		}
		for k, v := range meta {
			if k == "weft.record" || k == "weft.run.id" {
				continue
			}
			attrs = append(attrs, attribute.String(k, v))
		}
		r.AddAttributes(attrs...)
		emit.Emit(ctx, r)
	}
}
