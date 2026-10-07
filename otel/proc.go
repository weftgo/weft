package otel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/weftgo/weft/core"
	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// The attribute names the chain heads read and set. They mirror the
// core's (observe.go); pinned by the pipeline tests.
const (
	attrRecordKey   = "weft.record"
	attrContentKey  = "weft.content"
	attrTruncated   = "weft.content.truncated_bytes"
	contentStripped = "stripped"

	eventNameMessages = "weft.messages"
	eventNamePrompt   = "weft.prompt"
	eventNameTools    = "weft.tools"
)

// contentEvent reports whether a record kind is pure content, asked of
// Enabled by its EventName: the transcript batches and ADR 0028's
// prompt and tools records. A content-off destination answers false for
// them and drops them when they arrive anyway.
func contentEvent(eventName string) bool {
	switch eventName {
	case eventNameMessages, eventNamePrompt, eventNameTools:
		return true
	}
	return false
}

// destProc is the head of one destination's log chain — the only
// processor registered on the provider for that destination. It owns
// the Enabled rule and the clone rule:
//
//   - Enabled answers weft.messages, weft.prompt and weft.tools (the
//     pure-content kinds) with the destination's content setting and
//     everything else true. The core's Enabled questions are therefore
//     true exactly when some destination wants content, and a
//     content-off-only pipeline captures nothing (S2.4).
//   - OnEmit clones the record first: the SDK hands every processor
//     the same *Record, so in-place edits would leak between
//     destinations. The clone is what this chain's inner processor
//     (simple or batch) exports.
//
// Content-off chains decode the body with core.UnmarshalEvent, apply
// core.StripContent, re-encode and set weft.content=stripped, and drop
// messages records. Content-on chains apply the destination's Redact
// and MaxBytes to event and delta bodies, set
// weft.content.truncated_bytes when a cap cut, and apply Redact — never
// MaxBytes — to messages records, part by part.
//
// ADR 0028's three kinds (§6): a request record is kept everywhere —
// content-off empties its params.stop and marks it stripped, content-on
// redacts each stop sequence (core.ContentStop); prompt and tools
// records are dropped by content-off chains like messages records, and
// content-on chains redact the prompt text (core.ContentPrompt) and cap
// it, and cap the tools body, with weft.content.truncated_bytes.
type destProc struct {
	name     string
	inner    sdklog.Processor
	content  bool
	contentC ContentConfig
	noDeltas bool
	drops    *dropCounter
}

var _ sdklog.Processor = (*destProc)(nil)

func (p *destProc) Enabled(_ context.Context, param sdklog.EnabledParameters) bool {
	if contentEvent(param.EventName) {
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
			p.drops.filtered(1)
			return nil // pure content; a content-off destination gets none
		}
		if !p.redactMessages(&clone) {
			return nil // redaction failed: the batch is dropped, never sent unredacted
		}
	case "delta":
		if p.noDeltas {
			p.drops.filtered(1)
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
	case "prompt", "tools":
		if !p.content {
			p.drops.filtered(1)
			return nil // pure content, like a messages record
		}
		if !p.shapeContentRecord(&clone, kind) {
			return nil // never sent unredacted
		}
	case "request":
		if p.content {
			p.redactRequest(&clone)
		} else {
			p.stripRequest(&clone)
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
	ev, err := core.UnmarshalEvent([]byte(body.AsString()))
	if err != nil {
		return // not a weft event body (a foreign record): through as is
	}
	b, err := json.Marshal(core.StripContent(ev))
	if err != nil {
		return
	}
	clone.SetBody(attribute.StringValue(string(b)))
	setAttr(clone, attrContentKey, attribute.StringValue(contentStripped))
}

// shapeEvent applies the destination's Redact then MaxBytes to the
// body's content fields (event and delta bodies only — a capped
// transcript is not replay-grade), and records the cut.
//
// Redact is the caller's function running on the run's goroutine
// (Emit): a panic in it must neither unwind the run nor let the record
// through unredacted, so the chain falls back to the content-off shape
// for that record — the durable sequence keeps its position, the
// content does not leave — and counts it as the destination's drop.
func (p *destProc) shapeEvent(clone *sdklog.Record) {
	defer func() {
		if v := recover(); v != nil {
			p.stripEvent(clone)
			// The panic's type only: its value is commonly built from
			// the very content Redact was given.
			p.drops.dropped(1, fmt.Errorf("content redaction panicked (%T): the record was exported stripped", v))
		}
	}()
	body := clone.Body()
	if body.Type() != attribute.STRING {
		return
	}
	ev, err := core.UnmarshalEvent([]byte(body.AsString()))
	if err != nil {
		return
	}
	cfg := p.contentC
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = 32 << 10
	}
	shaped, cut, changed := shapeEventValue(ev, cfg.Redact, cfg.MaxBytes)
	if !changed && cut == 0 {
		return
	}
	b, err := json.Marshal(shaped)
	if err != nil {
		// Never the original body: it is the unredacted one.
		p.stripEvent(clone)
		return
	}
	clone.SetBody(attribute.StringValue(string(b)))
	if cut > 0 {
		addAttr(clone, attrTruncated, attribute.Int64Value(int64(cut)))
	}
}

// shapeEventValue is the shaping table for one event: redact then cap
// the content fields the core's own StripContent table names (content.go)
// — the same classes the content-off chain strips must not ride
// unredacted on a content-on chain with Redact configured. Steered
// message texts are user text (redacted like any other); run_finish
// pending args follow the adjudicated ToolStart.Args rule —
// redact-not-cap, because a byte cap mid-JSON would make the args
// undecodable; Nested recurses into the child event.
func shapeEventValue(ev core.Event, redact func(core.ContentKind, string) string, maxBytes int) (out core.Event, cut int, changed bool) {
	switch e := ev.(type) {
	case core.TextDelta:
		s, c := shapeString(e.Text, core.ContentText, redact, maxBytes)
		e.Text, cut, changed = s, c, s != ev.(core.TextDelta).Text
		return e, cut, changed
	case core.ReasoningDelta:
		s, c := shapeString(e.Text, core.ContentReasoning, redact, maxBytes)
		e.Text, cut, changed = s, c, s != ev.(core.ReasoningDelta).Text
		return e, cut, changed
	case core.ToolArgsDelta:
		s, c := shapeString(e.Args, core.ContentArgs, redact, maxBytes)
		e.Args, cut, changed = s, c, s != ev.(core.ToolArgsDelta).Args
		return e, cut, changed
	case core.ToolStart:
		// Args is a JSON document: a byte cap mid-document would make it
		// undecodable, so redaction may rewrite it but the cap does not
		// touch it.
		if redact != nil {
			s := redact(core.ContentArgs, string(e.Args))
			if s != string(e.Args) {
				e.Args, changed = redactedArgs(s), true
			}
		}
		return e, 0, changed
	case core.ToolFinish:
		s, c := shapeString(e.Content, core.ContentResult, redact, maxBytes)
		e.Content, cut, changed = s, c, s != ev.(core.ToolFinish).Content
		return e, cut, changed
	case core.Steered:
		// The delivered messages are ordinary transcript; their text
		// parts are user content (the core's StripContent empties them
		// on content-off chains).
		for i := range e.Messages {
			for j, part := range e.Messages[i].Content {
				tp, ok := part.(core.TextPart)
				if !ok {
					continue
				}
				s, c := shapeString(tp.Text, core.ContentText, redact, maxBytes)
				if s != tp.Text || c > 0 {
					changed = true
					cut += c
					tp.Text = s
					e.Messages[i].Content[j] = tp
				}
			}
		}
		return e, cut, changed
	case core.RunFinish:
		// Pending[].Args is the same class as the adjudicated
		// ToolStart.Args: redact, never cap.
		if redact != nil {
			for i, c := range e.Pending {
				s := redact(core.ContentArgs, string(c.Args))
				if s != string(c.Args) {
					changed = true
					c.Args = redactedArgs(s)
					e.Pending[i] = c
				}
			}
		}
		return e, 0, changed
	case core.Nested:
		inner, c, ch := shapeEventValue(e.Event, redact, maxBytes)
		if ch || c > 0 {
			e.Event, changed, cut = inner, true, c
		}
		return e, cut, changed
	default:
		return ev, 0, false
	}
}

// redactMessages applies the destination's Redact to a messages record
// (a transcript batch, a JSON array of core.Message) part by part, with
// the kind the same content carries on the event path: text parts
// ContentText (every role — as Steered user text already is), reasoning
// ContentReasoning, tool-call args ContentArgs (a non-JSON redactor
// output becomes a JSON string, redactedArgs), tool results
// ContentResult. Ids, names, roles, signatures and file parts are left
// as they are; the record's attributes (index, count, step) are never
// touched. Never capped — a capped transcript is not replay-grade (ADR
// 0024 D1). An unchanged batch keeps its bytes.
//
// It reports whether the record may go on. A batch that does not decode
// or re-encode, or whose Redact panicked, is dropped and counted as the
// destination's loss — a messages record is pure content, so its
// stripped form is no record at all (what a content-off chain does);
// sending it unredacted is never the fallback.
func (p *destProc) redactMessages(clone *sdklog.Record) (keep bool) {
	redact := p.contentC.Redact
	if redact == nil {
		return true
	}
	defer func() {
		if v := recover(); v != nil {
			keep = false
			// The panic's type only: its value is commonly built from
			// the very content Redact was given.
			p.drops.dropped(1, fmt.Errorf("content redaction panicked (%T): the messages record was dropped", v))
		}
	}()
	body := clone.Body()
	if body.Type() != attribute.STRING {
		p.drops.dropped(1, errors.New("a messages record without a string body cannot be redacted: dropped"))
		return false
	}
	var msgs []core.Message
	if err := json.Unmarshal([]byte(body.AsString()), &msgs); err != nil {
		// The decode error only: its text can quote the body.
		p.drops.dropped(1, errors.New("a messages record that does not decode cannot be redacted: dropped"))
		return false
	}
	if !redactMessageParts(msgs, redact) {
		return true
	}
	b, err := json.Marshal(msgs)
	if err != nil {
		p.drops.dropped(1, errors.New("a redacted messages record did not re-encode: dropped"))
		return false
	}
	clone.SetBody(attribute.StringValue(string(b)))
	return true
}

// redactMessageParts redacts msgs in place and reports whether any part
// changed.
func redactMessageParts(msgs []core.Message, redact func(core.ContentKind, string) string) (changed bool) {
	for i := range msgs {
		for j, part := range msgs[i].Content {
			switch pt := part.(type) {
			case core.TextPart:
				if s := redact(core.ContentText, pt.Text); s != pt.Text {
					pt.Text, changed = s, true
					msgs[i].Content[j] = pt
				}
			case core.ReasoningPart:
				if s := redact(core.ContentReasoning, pt.Text); s != pt.Text {
					pt.Text, changed = s, true
					msgs[i].Content[j] = pt
				}
			case core.ToolCallPart:
				if s := redact(core.ContentArgs, string(pt.Args)); s != string(pt.Args) {
					pt.Args, changed = redactedArgs(s), true
					msgs[i].Content[j] = pt
				}
			case core.ToolResultPart:
				if s := redact(core.ContentResult, pt.Content); s != pt.Content {
					pt.Content, changed = s, true
					msgs[i].Content[j] = pt
				}
			}
		}
	}
	return changed
}

// redactedArgs renders a redactor's output as the JSON value Args must
// be: the document itself when it still is one, a JSON string otherwise
// ("[REDACTED]" for the whole value is the natural redactor). An
// invalid RawMessage would fail the body's re-encode.
func redactedArgs(s string) json.RawMessage {
	if json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	b, _ := json.Marshal(s)
	return b
}

// shapeString applies redaction then the byte cap (on a rune boundary),
// returning the shaped string and how many bytes the cap removed.
func shapeString(s string, kind core.ContentKind, redact func(core.ContentKind, string) string, maxBytes int) (string, int) {
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
// at most once a minute while it loses them — a slow or failing
// destination starves only itself. Two ways a record does not arrive:
// lost (a failed export or write — dropped, which warns) and filtered
// by the destination's own policy (messages records on a content-off
// chain, deltas under NoDeltas — filtered, which counts and stays
// quiet: a healthy pipeline must not log drops). newDropCounter fixes
// the logger at construction, before any goroutine can emit; the field
// is immutable from then on (the old lazy assignment raced concurrent
// runs).
type dropCounter struct {
	name   string
	count  atomic.Int64 // every record that did not arrive, lost or filtered
	policy atomic.Int64 // the filtered share of count
	last   atomic.Int64 // unix nano of the last WARN
	log    *slog.Logger
}

func newDropCounter(name string) *dropCounter {
	return &dropCounter{name: name, log: slog.Default()}
}

// dropped counts n lost records; cause is the export or write error
// the throttled WARN names.
func (d *dropCounter) dropped(n int64, cause error) {
	total := d.count.Add(n)
	if throttle(&d.last) {
		attrs := []any{"dest", d.name, "dropped", total - d.policy.Load()}
		if cause != nil {
			attrs = append(attrs, "err", cause.Error())
		}
		d.log.Warn("weft/otel: destination dropped records", attrs...)
	}
}

func (d *dropCounter) filtered(n int64) {
	d.policy.Add(n)
	d.count.Add(n)
}

// throttle reports whether a minute has passed since it last said yes.
func throttle(last *atomic.Int64) bool {
	now := time.Now().UnixNano()
	prev := last.Load()
	return now-prev > int64(time.Minute) && last.CompareAndSwap(prev, now)
}

// countingLogExporter and countingSpanExporter wrap a batch
// destination's exporters: what a failed export loses is the
// destination's own drops, counted and warned like the Local sink's
// failed writes. (The OTLP exporters retry inside Export; an error here
// is after they gave up.)
type countingLogExporter struct {
	sdklog.Exporter
	drops *dropCounter
}

func (e countingLogExporter) Export(ctx context.Context, recs []sdklog.Record) error {
	err := e.Exporter.Export(ctx, recs)
	if err != nil {
		e.drops.dropped(int64(len(recs)), err)
	}
	return err
}

type countingSpanExporter struct {
	sdktrace.SpanExporter
	drops *dropCounter
}

func (e countingSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	err := e.SpanExporter.ExportSpans(ctx, spans)
	if err != nil {
		e.drops.dropped(int64(len(spans)), err)
	}
	return err
}
