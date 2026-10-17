package otel

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/weftgo/weft/core"

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
// pipeline has a live bridge, and only on what reaches a destination
// that takes app logs (AppLogs) — its absence on a stored root span is
// what the logs route's hint hole reads (obsdb.LogPage.NoBridge).
const attrLogsBridged = "weft.logs.bridged"

// attrLogAttrsDropped is weft.log.attrs_dropped: on a bridged line, how
// many of its slog attributes the bridge left off past maxBridgedAttrs.
const attrLogAttrsDropped = "weft.log.attrs_dropped"

// maxBridgedAttrs is how many slog attributes one bridged line carries:
// below the OTel SDK's default record limit (128), so the marks weft
// adds itself (weft.log.attrs_dropped, weft.content,
// weft.content.truncated_bytes) always fit and a cut is counted, never
// the SDK's silent drop.
const maxBridgedAttrs = 120

// ContentLog is the kind the destination's Redact receives for a
// bridged app log line (Slog, SlogHandler): its message and each of its
// string attributes. It is not a core kind — the core never logs
// content into a record; the bridge carries what the app logged.
const ContentLog core.ContentKind = "log"

// quietKey marks a context whose log lines the bridge never takes: the
// pipeline's own WARNs (a failing destination) go out under quietCtx,
// so a bridged slog.Default never feeds the pipeline's complaints about
// itself back into it.
type quietKey struct{}

var quietCtx = context.WithValue(context.Background(), quietKey{}, true)

// bridgeState is a pipeline's half of the bridge.
type bridgeState struct {
	wired   atomic.Bool  // Pipeline.SlogHandler bound a handler to this pipeline
	contact atomic.Bool  // a handler teed a line into this pipeline
	count   atomic.Int64 // lines emitted into the provider
	logger  otellog.Logger
	// installed is Slog's wrap of slog.Default (nil without Slog): live
	// while it still is the default's handler.
	installed atomic.Pointer[slogHandler]
	// prev, prevW and prevFlags are what Slog found: the default logger
	// and the log package's writer and flags (restore's target, and a
	// later pipeline's when this one closed first).
	prev      *slog.Logger
	prevW     io.Writer
	prevFlags int
	// restore puts slog.Default and the log package back as Slog found
	// them (nil when Slog did not wrap the default).
	restore func()
}

// bridged reports whether the pipeline has a live bridge and how many
// lines it emitted so far: weft.logs.bridged's value. Live is a handler
// bound to this pipeline (Pipeline.SlogHandler), Slog's wrap while it is
// still slog.Default's handler, or any handler — the package-level
// SlogHandler, or a program's own wrap of Slog's — that has teed a line
// into this pipeline. Merely building a package-level SlogHandler marks
// nothing: it follows whichever pipeline is installed when it logs.
// Which destinations see the mark is theirs to say (AppLogs: a
// destination that takes no app logs never carries it).
func (p *Pipeline) bridged() (int64, bool) {
	live := p.bridge.wired.Load() || p.bridge.contact.Load()
	if !live {
		if h := p.bridge.installed.Load(); h != nil && slog.Default().Handler() == slog.Handler(h) {
			live = true
		}
	}
	if !live {
		return 0, false
	}
	return p.bridge.count.Load(), true
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
// by a slog.TextHandler at the level slog.SetLogLoggerLevel set (INFO
// unless changed; read once, at Install), each line after log.Prefix()
// — wrapping the built-in handler itself would deadlock
// the log package. The format changes while the bridge is installed:
// the TextHandler's key=value lines (time=… level=… msg=…) replace the
// built-in "2009/11/10 23:00:00 INFO msg k=v" form, and the log
// package's flags no longer shape them; both come back at shutdown.
//
// One Slog pipeline at a time: a second Install(…, Slog()) wraps the
// first's bridge. Shut them down in reverse order; out of order, the
// last shutdown still skips a closed pipeline's wrap and restores what
// the first found. Under NoGlobal (Start only) the default is left
// alone, like the OTel globals; bind a handler with
// Pipeline.SlogHandler instead.
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
// key starting "weft." — "weft.run.id", "weft.record", which would make
// the record read as weft's own — is renamed "slog.weft.…"), the first
// 120 kept and the rest counted in weft.log.attrs_dropped; durations
// and times as their string forms; the trace and span ids of ctx. Only a line logged under a
// valid span context is teed — a line with no span names no run and
// stays with next alone — and only at a level some destination takes:
// the bridge itself has no level, every slog level (DEBUG included:
// weft's own lines are Debug) goes to the pipeline. Which destinations
// receive the lines is AppLogs': Local and Studio with content on by
// default, never a third party unless it opts in. Each destination
// shapes a line by its content policy (ContentConfig): Redact over the
// message and string attributes (ContentLog), each capped at MaxBytes
// with weft.content.truncated_bytes; a content-off destination that
// opted in gets the line stripped — severity, span and non-string
// attributes kept, message and string values emptied, weft.content
// "stripped". Under a full queue bridged lines are shed with the
// deltas, before weft's own records.
func SlogHandler(next slog.Handler) slog.Handler {
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
		// The level is slog.SetLogLoggerLevel's at Install, read once; the
		// log package's prefix (log.SetPrefix) still leads each line.
		tw := w
		if pre := log.Prefix(); pre != "" {
			tw = prefixWriter{w: w, prefix: []byte(pre)}
		}
		next = slog.NewTextHandler(tw, &slog.HandlerOptions{Level: logLoggerLevel()})
	}
	h := &slogHandler{next: next, pipe: p, viaSlog: true}
	p.bridge.prev, p.bridge.prevW, p.bridge.prevFlags = prev, w, flags
	p.bridge.installed.Store(h)
	slog.SetDefault(slog.New(h))
	p.bridge.restore = func() {
		if slog.Default().Handler() != slog.Handler(h) {
			return // the program (or a later Slog pipeline) replaced it since: theirs stays
		}
		target, tw, tf := prev, w, flags
		// Out-of-order shutdown: what this one found may be an earlier
		// Slog pipeline's wrap, closed since — restore what that one
		// found instead, never a dead bridge's TextHandler.
		for {
			ph, ok := target.Handler().(*slogHandler)
			if !ok || ph.pipe == nil || ph.pipe.bridge.installed.Load() != ph || !ph.pipe.closing.Load() {
				break
			}
			b := &ph.pipe.bridge
			target, tw, tf = b.prev, b.prevW, b.prevFlags
		}
		slog.SetDefault(target)
		log.SetOutput(tw)
		log.SetFlags(tf)
	}
}

// prefixWriter puts the log package's prefix before each line the
// stand-in TextHandler writes (one Write per record).
type prefixWriter struct {
	w      io.Writer
	prefix []byte
}

func (p prefixWriter) Write(b []byte) (int, error) {
	line := make([]byte, 0, len(p.prefix)+len(b))
	line = append(append(line, p.prefix...), b...)
	if _, err := p.w.Write(line); err != nil {
		return 0, err
	}
	return len(b), nil
}

// logLoggerLevel reads the level slog.SetLogLoggerLevel set (it has no
// getter: set and put back).
func logLoggerLevel() slog.Level {
	l := slog.SetLogLoggerLevel(slog.LevelInfo)
	slog.SetLogLoggerLevel(l)
	return l
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
	// viaSlog marks Slog's wrap of the default (and its WithAttrs /
	// WithGroup copies): its lines mark contact only when the default's
	// handler is no longer that wrap.
	viaSlog bool
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
		if n := len(kvs) - maxBridgedAttrs; n > 0 {
			kvs = append(kvs[:maxBridgedAttrs:maxBridgedAttrs], attribute.Int64(attrLogAttrsDropped, int64(n)))
		}
		rec.AddAttributes(kvs...)
		p.bridge.logger.Emit(ctx, rec)
		p.bridge.count.Add(1)
		// A line through Slog's own wrap while it is still the default's
		// handler is that wrap's liveness, which bridged reads directly:
		// marking contact would keep the pipeline bridged after the
		// program replaced the default. Any other path — a bound or
		// package-level handler, a program's wrap of Slog's — is contact.
		if !h.viaSlog || slog.Default().Handler() != slog.Handler(p.bridge.installed.Load()) {
			p.bridge.contact.Store(true)
		}
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
	if strings.HasPrefix(key, "weft.") {
		// weft.* keys are weft's own (weft.run.id, weft.record,
		// weft.event.type, …): an app's would make the line read as a
		// weft record — routed, shaped and tiered as one.
		key = "slog." + key
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
