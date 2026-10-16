package otel

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

// The slog bridge (plan U11.1, UX-E-07, record contract §5): the app's
// log/slog lines — its own, weft.Logger's run lines, mw.Log, mw.Audit —
// teed to the pipeline's LoggerProvider as app log records, carrying
// the trace and span ids of the context they were logged under, so
// Studio attributes each to the run (and the step) that produced it.

// bridgeScope is the instrumentation scope of every record the bridge
// emits: what the destination chains and the run tracker recognise a
// bridged line by (the record carries no weft.record: it is an app log,
// stored with the others — other_logs on SQLite, otel_logs on
// ClickHouse).
const bridgeScope = "github.com/weftgo/weft/otel/slog"

// attrLogsBridged is weft.logs.bridged: on the heartbeat, the count of
// lines the pipeline's bridge emitted so far; on a run's invoke_agent
// span, the same count when the run began. Present only while the
// pipeline has a bridge — its absence on a stored root span is what
// the logs route's hint hole reads (obsdb.LogPage.NoBridge).
const attrLogsBridged = "weft.logs.bridged"

// quietKey marks a context whose log lines the bridge never takes: the
// pipeline's own WARNs (a failing destination) go out under quietCtx,
// so a bridged slog.Default never feeds the pipeline's complaints about
// itself back into it.
type quietKey struct{}

var quietCtx = context.WithValue(context.Background(), quietKey{}, true)

// globalBridge is set once any package-level SlogHandler was built: a
// handler that follows the installed pipeline, so the installed one
// counts as bridged from then on.
var globalBridge atomic.Bool

// bridgeState is a pipeline's half of the bridge.
type bridgeState struct {
	wired  atomic.Bool  // Slog, or Pipeline.SlogHandler, bound a handler to this pipeline
	count  atomic.Int64 // lines emitted into the provider
	logger otellog.Logger
	// restore puts slog.Default and the log package back as Slog found
	// them (nil when Slog did not wrap the default).
	restore func()
}

// bridged reports whether the pipeline has a bridge and how many lines
// it emitted so far: weft.logs.bridged's value.
func (p *Pipeline) bridged() (int64, bool) {
	if p.bridge.wired.Load() {
		return p.bridge.count.Load(), true
	}
	if globalBridge.Load() {
		installedMu.RLock()
		installed := installedPipeline == p
		installedMu.RUnlock()
		if installed {
			return p.bridge.count.Load(), true
		}
	}
	return 0, false
}

// Slog bridges the program's default logger: once the pipeline is
// registered, slog.SetDefault wraps the default's handler with
// SlogHandler (bound to this pipeline), so every slog.Default line —
// weft.Logger's run lines (their default is slog.Default, read at log
// time), mw.Log(nil), mw.Audit(slog.Default()), the app's own — that is
// logged under a span reaches the pipeline's app-log destinations
// (AppLogs) as well as where it went before. Shutdown restores the
// previous default and the log package's writer and flags (unless the
// program replaced the default since).
//
// A program still on slog's built-in default handler (it never called
// slog.SetDefault) keeps its lines on the log package's writer, written
// by a slog.TextHandler at INFO and above: wrapping the built-in
// handler itself would deadlock the log package. Under NoGlobal (Start
// only) the default is left alone, like the OTel globals; bind a
// handler with Pipeline.SlogHandler instead.
func Slog() Option {
	return optionFunc(func(c *config) { c.slog = true })
}

// SlogHandler returns a slog.Handler that tees every record to next
// (when next is non-nil and enables its level) and to the installed
// pipeline (Install, or Start without NoGlobal), looked up per record:
// built before Install, it starts teeing once Install has run, and
// stops after the pipeline's shutdown. Use it to bridge a logger the
// program builds itself — slog.New(otel.SlogHandler(h)) — or pass that
// logger to weft.Logger, mw.Log or mw.Audit; Slog does the same for
// slog.Default.
//
// A bridged record is an app log record (record contract §5): the
// slog message as its body; the slog level as its severity (DEBUG 5,
// INFO 9, WARN 13, ERROR 17 — level + 9, held to 1–24) and severity
// text; the attributes flattened, a group's keys joined with "." (a
// key "weft.run.id", which would make the record read as weft's own,
// is renamed "slog.weft.run.id"); durations and times as their string
// forms; the trace and span ids of ctx. Only a line logged under a
// valid span context is teed — a line with no span names no run and
// stays with next alone — and only at a level some destination takes:
// the bridge itself has no level, every slog level (DEBUG included:
// weft's own lines are Debug) goes to the pipeline. Which destinations
// receive the lines is AppLogs': Local and Studio with content on by
// default, never a third party unless it opts in.
func SlogHandler(next slog.Handler) slog.Handler {
	globalBridge.Store(true)
	return &slogHandler{next: next}
}

// SlogHandler is the package-level SlogHandler bound to this pipeline
// rather than the installed one — for NoGlobal pipelines and tests. It
// marks the pipeline as bridged (weft.logs.bridged) from now on.
func (p *Pipeline) SlogHandler(next slog.Handler) slog.Handler {
	p.bridge.wired.Store(true)
	return &slogHandler{next: next, pipe: p}
}

// installSlog is Slog's wrap of the default (Start, after the globals).
func (p *Pipeline) installSlog() {
	prev := slog.Default()
	w, flags := log.Writer(), log.Flags()
	next := prev.Handler()
	if isBuiltinSlogHandler(next) {
		next = slog.NewTextHandler(w, nil)
	}
	h := p.SlogHandler(next)
	slog.SetDefault(slog.New(h))
	p.bridge.restore = func() {
		if slog.Default().Handler() != h {
			return // the program replaced it since: theirs stays
		}
		slog.SetDefault(prev)
		log.SetOutput(w)
		log.SetFlags(flags)
	}
}

// isBuiltinSlogHandler reports whether h is slog's own default handler
// (unexported: matched by its type's name).
func isBuiltinSlogHandler(h slog.Handler) bool {
	return fmt.Sprintf("%T", h) == "*slog.defaultHandler"
}

// slogHandler is the bridge. Immutable: WithAttrs and WithGroup return
// copies, so one handler is safe on any number of goroutines.
type slogHandler struct {
	next   slog.Handler
	pipe   *Pipeline // nil: the installed pipeline, per record
	prefix string    // the open groups, "a.b."
	attrs  []attribute.KeyValue
}

var _ slog.Handler = (*slogHandler)(nil)

func (h *slogHandler) target() *Pipeline {
	p := h.pipe
	if p == nil {
		installedMu.RLock()
		p = installedPipeline
		installedMu.RUnlock()
	}
	if p == nil || p.closing.Load() || p.bridge.logger == nil {
		return nil
	}
	return p
}

// pipelineEnabled reports whether the pipeline takes a line at level
// logged under ctx: a valid span context, not the pipeline's own quiet
// context, and a destination that takes app logs at that severity.
func (h *slogHandler) pipelineEnabled(ctx context.Context, level slog.Level) (*Pipeline, bool) {
	if ctx == nil || ctx.Value(quietKey{}) != nil || !trace.SpanContextFromContext(ctx).IsValid() {
		return nil, false
	}
	p := h.target()
	if p == nil {
		return nil, false
	}
	return p, p.bridge.logger.Enabled(ctx, otellog.EnabledParameters{Severity: slogSeverity(level)})
}

func (h *slogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if h.next != nil && h.next.Enabled(ctx, level) {
		return true
	}
	_, ok := h.pipelineEnabled(ctx, level)
	return ok
}

func (h *slogHandler) Handle(ctx context.Context, r slog.Record) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if p, ok := h.pipelineEnabled(ctx, r.Level); ok {
		var rec otellog.Record
		t := r.Time
		if t.IsZero() {
			t = time.Now()
		}
		rec.SetTimestamp(t)
		rec.SetObservedTimestamp(time.Now())
		rec.SetSeverity(slogSeverity(r.Level))
		rec.SetSeverityText(r.Level.String())
		rec.SetBody(attribute.StringValue(r.Message))
		kvs := append([]attribute.KeyValue(nil), h.attrs...)
		r.Attrs(func(a slog.Attr) bool {
			kvs = appendSlogAttr(kvs, h.prefix, a)
			return true
		})
		rec.AddAttributes(kvs...)
		p.bridge.logger.Emit(ctx, rec)
		p.bridge.count.Add(1)
	}
	if h.next != nil && h.next.Enabled(ctx, r.Level) {
		return h.next.Handle(ctx, r)
	}
	return nil
}

func (h *slogHandler) WithAttrs(as []slog.Attr) slog.Handler {
	if len(as) == 0 {
		return h
	}
	c := *h
	c.attrs = append([]attribute.KeyValue(nil), h.attrs...)
	for _, a := range as {
		c.attrs = appendSlogAttr(c.attrs, h.prefix, a)
	}
	if h.next != nil {
		c.next = h.next.WithAttrs(as)
	}
	return &c
}

func (h *slogHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	c := *h
	c.prefix = h.prefix + name + "."
	if h.next != nil {
		c.next = h.next.WithGroup(name)
	}
	return &c
}

// slogSeverity maps a slog level onto the OTLP severity numbers: level
// + 9 (DEBUG -4 → 5, INFO 0 → 9, WARN 4 → 13, ERROR 8 → 17), held to
// 1–24 — the slog bridges' common rule.
func slogSeverity(l slog.Level) otellog.Severity {
	n := int(l) + 9
	switch {
	case n < 1:
		n = 1
	case n > 24:
		n = 24
	}
	return otellog.Severity(n)
}

// appendSlogAttr flattens one slog attribute under prefix: a group's
// members under "<prefix><key>.", an empty-keyed group inline (slog's
// rule), an empty attribute dropped.
func appendSlogAttr(kvs []attribute.KeyValue, prefix string, a slog.Attr) []attribute.KeyValue {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return kvs
	}
	if a.Value.Kind() == slog.KindGroup {
		inner := prefix
		if a.Key != "" {
			inner = prefix + a.Key + "."
		}
		for _, m := range a.Value.Group() {
			kvs = appendSlogAttr(kvs, inner, m)
		}
		return kvs
	}
	key := prefix + a.Key
	if key == "weft.run.id" {
		key = "slog.weft.run.id" // a weft.run.id would make the line read as weft's own record
	}
	return append(kvs, attribute.KeyValue{Key: attribute.Key(key), Value: slogValue(a.Value)})
}

// slogValue converts a resolved, non-group slog value.
func slogValue(v slog.Value) attribute.Value {
	switch v.Kind() {
	case slog.KindString:
		return attribute.StringValue(v.String())
	case slog.KindInt64:
		return attribute.Int64Value(v.Int64())
	case slog.KindUint64:
		if u := v.Uint64(); u <= 1<<63-1 {
			return attribute.Int64Value(int64(u))
		}
		return attribute.StringValue(strconv.FormatUint(v.Uint64(), 10))
	case slog.KindFloat64:
		return attribute.Float64Value(v.Float64())
	case slog.KindBool:
		return attribute.BoolValue(v.Bool())
	case slog.KindDuration:
		return attribute.StringValue(v.Duration().String())
	case slog.KindTime:
		return attribute.StringValue(v.Time().Format(time.RFC3339Nano))
	}
	if err, ok := v.Any().(error); ok && err != nil {
		return attribute.StringValue(err.Error())
	}
	return attribute.StringValue(fmt.Sprint(v.Any()))
}
