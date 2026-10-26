package otel

import (
	"context"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// The development detail level's destination rule (DevDetail; ADR
// 0016's 2026-10-26 amendment): the layer spans and a repair's original
// arguments are for the Local and Studio destinations with content on
// alone — the rawOn rule — so an OTLP preset (Datadog, Langfuse, OTLP,
// Exporters) never receives one, even in development. The layer spans
// parent nothing but each other (core keeps them off the context's
// span), so dropping them orphans no span.

const (
	// attrLayerName marks a layer span (core's weft.layer.name).
	attrLayerName = "weft.layer.name"
	// eventRepair and attrRepairOriginal are the chat span's repair
	// event and the original arguments it carries under the level.
	eventRepair        = "weft.repair"
	attrRepairOriginal = "weft.repair.original"
	attrRepairCut      = "weft.repair.original_cut"
	// eventPanic is a contained panic's stack, under the level only.
	eventPanic = "weft.panic"
)

// detailFor wraps a destination's span exporter with the rule when the
// level is on and the destination may not see the detail.
func detailFor(d dest, cfg config, exp sdktrace.SpanExporter) sdktrace.SpanExporter {
	if !cfg.devDetail || d.rawOn() {
		return exp
	}
	return detailSpanExporter{exp}
}

// detailSpanExporter drops layer spans, the repair event's original and
// the weft.panic event (a contained panic's stack) from each batch, copying the batch only when it holds one.
type detailSpanExporter struct{ sdktrace.SpanExporter }

func (e detailSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	var out []sdktrace.ReadOnlySpan
	for i, s := range spans {
		layer := strings.HasPrefix(s.Name(), "layer ") && hasSpanAttr(s, attrLayerName)
		original := !layer && (hasRepairOriginal(s) || hasEvent(s, eventPanic))
		if !layer && !original {
			if out != nil {
				out = append(out, s)
			}
			continue
		}
		if out == nil { // the batch is the processor's: never written in place
			out = append(make([]sdktrace.ReadOnlySpan, 0, len(spans)), spans[:i]...)
		}
		if original {
			out = append(out, unoriginalSpan{s})
		}
	}
	if out == nil {
		out = spans
	}
	if len(out) == 0 {
		return nil
	}
	return e.SpanExporter.ExportSpans(ctx, out)
}

func hasEvent(s sdktrace.ReadOnlySpan, name string) bool {
	for _, ev := range s.Events() {
		if ev.Name == name {
			return true
		}
	}
	return false
}

func hasRepairOriginal(s sdktrace.ReadOnlySpan) bool {
	for _, ev := range s.Events() {
		if ev.Name != eventRepair {
			continue
		}
		for _, kv := range ev.Attributes {
			if string(kv.Key) == attrRepairOriginal {
				return true
			}
		}
	}
	return false
}

// unoriginalSpan is a span read without its repair events' original
// and without its weft.panic events.
type unoriginalSpan struct{ sdktrace.ReadOnlySpan }

func (s unoriginalSpan) Events() []sdktrace.Event {
	in := s.ReadOnlySpan.Events()
	out := make([]sdktrace.Event, 0, len(in))
	for _, ev := range in {
		if ev.Name == eventPanic {
			continue // the stack is all it carries
		}
		if ev.Name == eventRepair {
			attrs := make([]attribute.KeyValue, 0, len(ev.Attributes))
			for _, kv := range ev.Attributes {
				if k := string(kv.Key); k != attrRepairOriginal && k != attrRepairCut {
					attrs = append(attrs, kv)
				}
			}
			ev.Attributes = attrs
		}
		out = append(out, ev)
	}
	return out
}
