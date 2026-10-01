package otel

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/weftgo/weft"
	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// The attribute names the chain heads read and set. They mirror the
// core's (observe.go); pinned by the pipeline tests.
const (
	attrRecordKey   = "weft.record"
	attrContentKey  = "weft.content"
	attrTruncated   = "weft.content.truncated_bytes"
	contentStripped = "stripped"

	eventNameMessages = "weft.messages"
)

// destProc is the head of one destination's log chain — the only
// processor registered on the provider for that destination. It owns
// the Enabled rule and the clone rule:
//
//   - Enabled answers weft.messages with the destination's content
//     setting and everything else true. The core's one Enabled question
//     is therefore true exactly when some destination wants content,
//     and a content-off-only pipeline captures nothing (S2.4).
//   - OnEmit clones the record first: the SDK hands every processor
//     the same *Record, so in-place edits would leak between
//     destinations. The clone is what this chain's inner processor
//     (simple or batch) exports.
//
// Content-off chains decode the body with weft.UnmarshalEvent, apply
// weft.StripContent, re-encode and set weft.content=stripped, and drop
// messages records. Content-on chains apply the destination's Redact
// and MaxBytes to event and delta bodies — never messages records —
// and set weft.content.truncated_bytes when a cap cut.
type destProc struct {
	name     string
	inner    sdklog.Processor
	content  bool
	contentC ContentConfig
	noDeltas bool
	drops    *dropCounter
	logger   *slog.Logger
}

var _ sdklog.Processor = (*destProc)(nil)

func (p *destProc) Enabled(_ context.Context, param sdklog.EnabledParameters) bool {
	if param.EventName == eventNameMessages {
		return p.content
	}
	return true
}

func (p *destProc) OnEmit(ctx context.Context, r *sdklog.Record) error {
	clone := r.Clone()
	kind := recordKind(&clone)
	switch kind {
	case "messages":
		if !p.content {
			p.drops.dropped(1)
			return nil // pure content; a content-off destination gets none
		}
	case "delta":
		if p.noDeltas {
			p.drops.dropped(1)
			return nil
		}
		if p.content {
			p.shapeEvent(&clone)
		} else {
			p.stripEvent(&clone)
		}
	case "event", "":
		if kind == "event" && p.content {
			p.shapeEvent(&clone)
		} else if kind == "event" {
			p.stripEvent(&clone)
		}
	case "heartbeat":
		// No body, no content: through unchanged.
	}
	return p.inner.OnEmit(ctx, &clone)
}

func (p *destProc) Shutdown(ctx context.Context) error   { return p.inner.Shutdown(ctx) }
func (p *destProc) ForceFlush(ctx context.Context) error { return p.inner.ForceFlush(ctx) }

// recordKind reads weft.record off a record ("" for non-weft).
func recordKind(r *sdklog.Record) string {
	var kind string
	r.WalkAttributes(func(kv attribute.KeyValue) bool {
		if string(kv.Key) == attrRecordKey {
			kind = kv.Value.AsString()
			return false
		}
		return true
	})
	return kind
}

// stripEvent empties the body's content fields and marks the record
// stripped — what a content-off destination receives.
func (p *destProc) stripEvent(clone *sdklog.Record) {
	body := clone.Body()
	if body.Type() != attribute.STRING {
		return
	}
	ev, err := weft.UnmarshalEvent([]byte(body.AsString()))
	if err != nil {
		return // not a weft event body (a foreign record): through as is
	}
	b, err := json.Marshal(weft.StripContent(ev))
	if err != nil {
		return
	}
	clone.SetBody(attribute.StringValue(string(b)))
	setAttr(clone, attrContentKey, attribute.StringValue(contentStripped))
}

// shapeEvent applies the destination's Redact then MaxBytes to the
// body's content fields (event and delta bodies only — a capped
// transcript is not replay-grade), and records the cut.
func (p *destProc) shapeEvent(clone *sdklog.Record) {
	body := clone.Body()
	if body.Type() != attribute.STRING {
		return
	}
	ev, err := weft.UnmarshalEvent([]byte(body.AsString()))
	if err != nil {
		return
	}
	cfg := p.contentC
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = 32 << 10
	}
	var cut int
	redact := cfg.Redact
	if e, ok := ev.(weft.TextDelta); ok {
		e.Text, cut = shapeString(e.Text, weft.ContentText, redact, cfg.MaxBytes)
		ev = e
	} else if e, ok := ev.(weft.ReasoningDelta); ok {
		e.Text, cut = shapeString(e.Text, weft.ContentReasoning, redact, cfg.MaxBytes)
		ev = e
	} else if e, ok := ev.(weft.ToolArgsDelta); ok {
		e.Args, cut = shapeString(e.Args, weft.ContentArgs, redact, cfg.MaxBytes)
		ev = e
	} else if e, ok := ev.(weft.ToolStart); ok {
		// Args is a JSON document: a byte cap mid-document would make it
		// undecodable, so redaction may rewrite it but the cap does not
		// touch it.
		if redact != nil {
			e.Args = json.RawMessage(redact(weft.ContentArgs, string(e.Args)))
		}
		ev = e
	} else if e, ok := ev.(weft.ToolFinish); ok {
		e.Content, cut = shapeString(e.Content, weft.ContentResult, redact, cfg.MaxBytes)
		ev = e
	}
	// Steered messages and run_finish pending args stay: they are
	// transcript batches and identifiers, and the messages records are
	// the transcript contract.
	if redact != nil || cut > 0 {
		b, err := json.Marshal(ev)
		if err != nil {
			return
		}
		clone.SetBody(attribute.StringValue(string(b)))
		if cut > 0 {
			addAttr(clone, attrTruncated, attribute.Int64Value(int64(cut)))
		}
	}
}

// shapeString applies redaction then the byte cap (on a rune boundary),
// returning the shaped string and how many bytes the cap removed.
func shapeString(s string, kind weft.ContentKind, redact func(weft.ContentKind, string) string, maxBytes int) (string, int) {
	if redact != nil {
		s = redact(kind, s)
	}
	if maxBytes < 0 || len(s) <= maxBytes {
		return s, 0
	}
	// Cut on a rune boundary at or below the cap.
	cut := maxBytes
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return s[:cut], len(s) - cut
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// setAttr replaces (or adds) one attribute on the clone.
func setAttr(r *sdklog.Record, key string, value attribute.Value) {
	var kept []attribute.KeyValue
	r.WalkAttributes(func(kv attribute.KeyValue) bool {
		if string(kv.Key) != key {
			kept = append(kept, kv)
		}
		return true
	})
	kept = append(kept, attribute.KeyValue{Key: attribute.Key(key), Value: value})
	r.SetAttributes(kept...)
}

func addAttr(r *sdklog.Record, key string, value attribute.Value) {
	r.AddAttributes(attribute.KeyValue{Key: attribute.Key(key), Value: value})
}

// dropCounter counts one destination's dropped records and logs a WARN
// at most once a minute while drops happen — a slow or failing
// destination starves only itself.
type dropCounter struct {
	name  string
	count atomic.Int64
	last  atomic.Int64 // unix nano of the last WARN
	log   *slog.Logger
}

func (d *dropCounter) dropped(n int64) {
	total := d.count.Add(n)
	now := time.Now().UnixNano()
	last := d.last.Load()
	if now-last > int64(time.Minute) && d.last.CompareAndSwap(last, now) {
		if d.log == nil {
			d.log = slog.Default()
		}
		d.log.Warn("weft/otel: destination dropped records",
			"dest", d.name, "dropped", total)
	}
}
