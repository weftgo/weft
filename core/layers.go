package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/weftgo/weft/core/internal/contentflag"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	logglobal "go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
)

// The middleware onion, recorded (UX plan U23.1; ADR 0016's 2026-10-26
// amendment). Three levels, each reporting only (ADR 0006): the
// manifest lists the chain outer-to-inner and the hooks present (static,
// once per agent); the spans that already exist carry what a layer
// decided on a call (execute_tool's weft.tool.denied_by /
// weft.tool.error_masked / weft.tool.args_repaired, the attempt span's
// weft.attempt.layer, the chat span's weft.repair event); and under the
// opt-in development detail level (weft/otel's otel.DevDetail) one span
// per layer per call. Nothing here changes a call, a retry, a denial or
// a result.

// MiddlewareInfo describes one middleware layer — or one hook — for
// the manifest: what it is and how it is set, never what it saw.
// A model middleware's wrapper may describe itself with the optional
//
//	interface{ Describe() MiddlewareInfo }
//
// convention, as mw.Retry, mw.Fallback, mw.Log and mw.RepairJSON do; a
// layer that does not is named by its type. A tool middleware is a
// function and cannot carry a method: it is named by the name its
// constructor registered (NameToolMiddleware; weft's own), else by the
// function — a named one by its name with its source position, a
// closure by where it is written ("func@<dir>/<file>:<line>"), the same
// whatever the compiler inlined. Params carry configuration only —
// counts, durations, model names — never a secret or a closure's
// captured value.
type MiddlewareInfo struct {
	// Name is the layer's name: "mw.Retry", "mw.Allow", a type name
	// ("main.cacheModel"), a named function's ("main.audit") or a
	// closure's place ("func@app/main.go:42").
	Name string `json:"name"`
	// Params is the layer's configuration, rendered as text, e.g.
	// {"max_retries": "3", "base_delay": "200ms"}. Omitted when none.
	Params map[string]string `json:"params,omitempty"`
	// Source is the defining position for a function or hook that is
	// not weft's own, in the import-path form: the package's import
	// path, the file's base name and the line
	// ("github.com/x/app/hooks.go:12"), the same on every machine.
	Source string `json:"source,omitempty"`
	// Opaque says the model chain's walk stopped at this layer: it
	// declares no Unwrap, so the layers it wraps, if any, are not
	// listed — never an onion that silently reads shorter than it is.
	Opaque bool `json:"opaque,omitempty"`
}

// describer is the optional Describe convention (a convention, like
// ReportsAttempts and Unwrap, not an interface a caller must name).
type describer interface{ Describe() MiddlewareInfo }

// maxLayers bounds the manifest's walk of the model chain.
const maxLayers = 64

// modelLayers lists the model chain over base, outer to inner: each
// wrapper's Describe, else its type's name, walking Unwrap until base
// is reached, at most n layers (the WrapModel count) — a wrapper whose
// Unwrap is absent ends the walk marked Opaque.
func modelLayers(top, base Model, n int) []MiddlewareInfo {
	var out []MiddlewareInfo
	m := top
	for hops := 0; len(out) < min(n, maxLayers) && hops < 2*maxLayers; hops++ {
		if m == nil || sameModel(m, base) {
			break
		}
		if b, ok := m.(*layerModel); ok { // a detail boundary, not a layer
			m = b.next
			continue
		}
		info := describeModel(m)
		next := Unwrap(m)
		if next == nil {
			info.Opaque = true
			out = append(out, info)
			break
		}
		out = append(out, info)
		m = next
	}
	return out
}

// sameModel compares two models without panicking on an uncomparable
// dynamic type (a func adapter): such a value is never equal.
func sameModel(a, b Model) bool {
	ta, tb := reflect.TypeOf(a), reflect.TypeOf(b)
	return ta == tb && ta != nil && ta.Comparable() && a == b
}

// describeModel is a layer's MiddlewareInfo: its Describe when it has
// one (contained — a panicking Describe names the type instead), else
// the type's name.
func describeModel(m Model) (info MiddlewareInfo) {
	if d, ok := m.(describer); ok {
		func() {
			defer func() {
				if recover() != nil {
					info = MiddlewareInfo{}
				}
			}()
			info = d.Describe()
		}()
	}
	if info.Name == "" {
		info.Name = typeName(m)
	}
	return info
}

// typeName is a value's type without the pointer star and with the
// package's last path element only: "main.cacheModel".
func typeName(v any) string {
	t := reflect.TypeOf(v)
	if t == nil {
		return "nil"
	}
	s := strings.TrimLeft(t.String(), "*")
	return s
}

// toolNames is the registry NameToolMiddleware fills: a tool
// middleware's code pointer → its name. Filled where the closure is
// made, so it holds whatever pointer the compiler gave it: a constructor
// inlined into its caller makes its closure a copy with another pointer
// and another runtime name ("caller.Allow.func9") — registered as made,
// it is still found by name.
var toolNames sync.Map // uintptr → string

// NameToolMiddleware names a tool middleware for the manifest
// (tool_middleware, a tool's middleware) and its development detail
// layer span, and returns it unchanged: weft's own (mw.Allow, mw.Audit,
// mw.MapErrors, thread/pool's) are named through it. A middleware not
// named so is named by the function it is: a named function or method
// by its name, a closure by where it is written ("func@<file>:<line>"),
// never by the runtime's name for it, which inlining changes. The name
// is configuration — the same for every closure of one literal; no
// secret. Calling it again for the same literal keeps the last name.
func NameToolMiddleware(name string, mw ToolMiddleware) ToolMiddleware {
	if mw != nil && name != "" {
		toolNames.Store(reflect.ValueOf(mw).Pointer(), name)
	}
	return mw
}

// funcInfo names a function value for the manifest and the layer
// spans, the same whatever the compiler inlined (P10b-inline-names):
//
//   - a tool middleware NameToolMiddleware named: that name alone;
//   - a named function or method: its name — the last import-path
//     element and the function ("main.audit", "app.(*S).Hook"), its
//     source position beside it;
//   - a closure: "func@<source>", its source the file:line it is written
//     at (sourceRel's rule) — the runtime's name for a closure is the
//     chain of functions it was compiled into, which inlining decides.
func funcInfo(fn any) MiddlewareInfo {
	v := reflect.ValueOf(fn)
	if v.Kind() != reflect.Func || v.IsNil() {
		return MiddlewareInfo{Name: "func"}
	}
	pc := v.Pointer()
	if name, ok := toolNames.Load(pc); ok {
		return MiddlewareInfo{Name: name.(string)}
	}
	f := runtime.FuncForPC(pc)
	if f == nil {
		return MiddlewareInfo{Name: "func"}
	}
	full := strings.TrimSuffix(f.Name(), "-fm") // a method value
	file, line := f.FileLine(f.Entry())
	if isClosure(full) {
		src := "unknown"
		if file != "" {
			src = closureSource(file) + ":" + strconv.Itoa(line)
		}
		return MiddlewareInfo{Name: "func@" + src, Source: src}
	}
	info := MiddlewareInfo{Name: full[strings.LastIndex(full, "/")+1:]}
	if file != "" {
		info.Source = sourceRel(file, full) + ":" + strconv.Itoa(line)
	}
	return info
}

// closureSource places a closure by its file alone — the directory's
// name and the file's ("mw/tool.go"; a module cache directory's
// @version dropped) — never by its runtime name, whose package is the
// function it was compiled into: a closure-returning helper inlined
// into another package's caller would read as that package's. The same
// source tree gives the same place on every machine, toolchain and
// inlining decision, never an absolute path.
func closureSource(file string) string {
	slashed := filepath.ToSlash(file)
	dir := path.Base(path.Dir(slashed))
	if at := strings.Index(dir, "@"); at > 0 {
		dir = dir[:at]
	}
	if dir == "." || dir == "/" || dir == "" {
		return path.Base(slashed)
	}
	return unescapeModPath(dir) + "/" + path.Base(slashed)
}

// isClosure reports a runtime name that is a function literal's (its
// last element "func1", "1", "gowrap2"), not a named function's.
func isClosure(full string) bool {
	short := full[strings.LastIndex(full, "/")+1:]
	i := strings.LastIndex(short, ".")
	return i > 0 && closureSuffix(short[i+1:])
}

// closureSuffix reports a compiler-made name element: "func1", "1",
// "gowrap2".
func closureSuffix(s string) bool {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "func"), "gowrap")
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func toolLayers(mws []ToolMiddleware) []MiddlewareInfo {
	if len(mws) == 0 {
		return nil
	}
	out := make([]MiddlewareInfo, len(mws))
	for i, m := range mws {
		out[i] = funcInfo(m)
	}
	return out
}

// funcInfos names a list of hook functions.
func funcInfos[F any](fns []F) []MiddlewareInfo {
	if len(fns) == 0 {
		return nil
	}
	out := make([]MiddlewareInfo, len(fns))
	for i, fn := range fns {
		out[i] = funcInfo(fn)
	}
	return out
}

// --- level 2: what a tool layer decided, on the execute_tool span ---

// ToolReporter is the reporting path from the tool chain into the
// call's own record (ADR 0016's A8 path, the tool side of Reporter): a
// ToolMiddleware tells the call's execute_tool span what it decided,
// which the loop cannot see from outside the chain. Reporting only
// (ADR 0006): a report changes no result, no error and no denial. A
// report never returns an error, never blocks and never panics into
// the caller. The zero ToolReporter, and the one ToolReportFromContext
// returns outside a loop-dispatched tool call, discards every report;
// it is safe for concurrent use. The last report of a kind wins.
type ToolReporter struct {
	n *nest
}

// ToolReportFromContext returns the reporter of the tool call whose
// context ctx is (or derives from): the loop puts one on the context it
// hands the tool chain. Outside one — Agent.CallTool, a model call, a
// bare context — it is a no-op ToolReporter, never nil.
func ToolReportFromContext(ctx context.Context) ToolReporter {
	if ctx == nil {
		return ToolReporter{}
	}
	n := nestFromContext(ctx)
	if n == nil || n.callID == "" {
		return ToolReporter{}
	}
	// A call dispatched under another (Agent.CallTool inside a handler)
	// carries its own Call but the outer call's nest: its reports are
	// not the outer call's.
	if c, ok := CallFromContext(ctx); !ok || c.CallID != n.callID {
		return ToolReporter{}
	}
	return ToolReporter{n: n}
}

// Denied reports that a layer refused the call: by names the deciding
// layer's kind — mw.Allow reports "allow". The execute_tool span
// carries it as weft.tool.denied_by when the call ends in an error.
// An id or an enum, never a reason.
func (r ToolReporter) Denied(by string) {
	if r.n == nil || by == "" {
		return
	}
	r.n.deniedBy.Store(&by)
}

// Masked reports that a layer replaced the call's error with another
// (mw.MapErrors' INTERNAL): original is the error it hid. The span
// carries the original's kind as weft.tool.error_masked — never its
// text: the first of its optional ErrorKind() string (a convention for
// an app's own sentinels), a context error's name, its ErrorType(), a
// ToolError's code, a standard library sentinel it matches with
// errors.Is ("io.EOF", "fs.ErrNotExist", …), else its Go type past the
// standard wrappers ("*net.OpError").
func (r ToolReporter) Masked(original error) {
	if r.n == nil || original == nil {
		return
	}
	kind := maskedKind(original)
	r.n.masked.Store(&kind)
}

// maskedKind is weft.tool.error_masked's value for an error a layer
// hid. The error's methods are its owner's: a panic in them reads
// "unknown", never escapes into the middleware.
func maskedKind(err error) (kind string) {
	defer func() {
		if recover() != nil {
			kind = "unknown"
		}
	}()
	if k, ok := errors.AsType[errorKinder](err); ok {
		if kind := k.ErrorKind(); kind != "" {
			return kind
		}
	}
	if name, ok := ctxErrorType(err); ok {
		return name
	}
	if typed, ok := errors.AsType[errorTyper](err); ok {
		if t := typed.ErrorType(); t != "" {
			return t
		}
	}
	if te, ok := errors.AsType[*ToolError](err); ok && te.Code != "" {
		return te.Code
	}
	for _, s := range stdSentinels {
		if errors.Is(err, s.err) {
			return s.name
		}
	}
	for range maxLayers {
		switch fmt.Sprintf("%T", err) {
		case "*fmt.wrapError", "*fmt.wrapErrors", "*errors.joinError":
			if next := errors.Unwrap(err); next != nil {
				err = next
				continue
			}
		}
		break
	}
	return fmt.Sprintf("%T", err)
}

// errorKinder is the optional ErrorKind convention: an error naming its
// own kind for weft.tool.error_masked (an app's sentinel), never its
// text.
type errorKinder interface {
	error
	ErrorKind() string
}

// stdSentinels are the standard library's sentinel errors a hidden
// error is named by (weft.tool.error_masked): every errors.New sentinel
// is a *errors.errorString, so its type says nothing. Checked in order;
// os.ErrNotExist and the others are the fs values themselves.
var stdSentinels = []struct {
	err  error
	name string
}{
	{io.EOF, "io.EOF"},
	{io.ErrUnexpectedEOF, "io.ErrUnexpectedEOF"},
	{io.ErrClosedPipe, "io.ErrClosedPipe"},
	{io.ErrShortWrite, "io.ErrShortWrite"},
	{io.ErrNoProgress, "io.ErrNoProgress"},
	{fs.ErrNotExist, "fs.ErrNotExist"},
	{fs.ErrExist, "fs.ErrExist"},
	{fs.ErrPermission, "fs.ErrPermission"},
	{fs.ErrInvalid, "fs.ErrInvalid"},
	{fs.ErrClosed, "fs.ErrClosed"},
	{os.ErrDeadlineExceeded, "os.ErrDeadlineExceeded"},
	{os.ErrProcessDone, "os.ErrProcessDone"},
	{errors.ErrUnsupported, "errors.ErrUnsupported"},
}

// --- the repair report (model side) ---

// RepairInfo is one tool call whose arguments a model middleware
// repaired before the loop saw them (mw.RepairJSON).
type RepairInfo struct {
	// CallID is the repaired call's id.
	CallID string
	// Kind names the repair: weft's mw.RepairJSON reports "fence" (a
	// Markdown fence stripped), "unterminated" (open strings, objects or
	// arrays closed), "fence+unterminated", or "trim" (whitespace or an
	// empty argument made {}).
	Kind string
	// Original and Repaired are the arguments before and after. Their
	// lengths always reach the record; the original's bytes only under
	// the development detail level with content on.
	Original, Repaired []byte
}

// The repair event and the layer spans' attributes (ADR 0016's
// 2026-10-26 amendment; docs/record-contract.md §3).
const (
	eventRepair           = "weft.repair"
	attrRepairKind        = attribute.Key("weft.repair.kind")
	attrRepairBytesBefore = attribute.Key("weft.repair.bytes_before")
	attrRepairBytesAfter  = attribute.Key("weft.repair.bytes_after")
	attrRepairOriginal    = attribute.Key("weft.repair.original")
	attrRepairOriginalCut = attribute.Key("weft.repair.original_cut")
	attrToolDeniedBy      = attribute.Key("weft.tool.denied_by")
	attrToolErrorMasked   = attribute.Key("weft.tool.error_masked")
	attrToolArgsRepaired  = attribute.Key("weft.tool.args_repaired")
	attrLayerName         = attribute.Key("weft.layer.name")
	attrLayerKind         = attribute.Key("weft.layer.kind")
	attrLayerIndex        = attribute.Key("weft.layer.index")
	layerKindModel        = "model"
	layerKindTool         = "tool"
	repairOriginalCap     = 4 << 10
	logRepairKind         = "kind"
	logRepairBytesBefore  = "bytes_before"
	logRepairBytesAfter   = "bytes_after"
	layerSpanPrefix       = "layer "
	deniedByApproval      = "approval"
	deniedByGrant         = "grant"
)

// Repair reports one repaired tool call of the current model call. With
// a tracer recording, it becomes a weft.repair event on the step's chat
// span (gen_ai.tool.call.id, weft.repair.kind, weft.repair.bytes_before,
// weft.repair.bytes_after; the original bytes, capped at 4 KiB, only
// under the development detail level with content on), and the call's
// execute_tool span carries weft.tool.args_repaired; with the logger at
// Debug, a "model repair" line (sizes, never bytes).
func (r Reporter) Repair(ri RepairInfo) {
	if r.s == nil || ri.CallID == "" {
		return
	}
	r.s.repair(ri)
}

func (s *stepReport) repair(ri RepairInfo) {
	if s.ended.Load() {
		return
	}
	defer s.contain()
	if s.rec != nil {
		s.rec.noteRepaired(ri.CallID)
	}
	ctx := s.Context
	func() {
		defer s.containTraces()
		span := trace.SpanFromContext(ctx)
		if !span.IsRecording() {
			return
		}
		attrs := []attribute.KeyValue{
			semconv.GenAIToolCallID(ri.CallID),
			attrRepairBytesBefore.Int(len(ri.Original)),
			attrRepairBytesAfter.Int(len(ri.Repaired)),
		}
		if ri.Kind != "" {
			attrs = append(attrs, attrRepairKind.String(ri.Kind))
		}
		if len(ri.Original) > 0 && s.detailContent() {
			orig, cut := capUTF8(ri.Original, repairOriginalCap)
			attrs = append(attrs, attrRepairOriginal.String(orig))
			if cut > 0 {
				attrs = append(attrs, attrRepairOriginalCut.Int(cut))
			}
		}
		span.AddEvent(eventRepair, trace.WithAttributes(attrs...))
	}()
	if l := s.obs.logger(); l.Enabled(ctx, slog.LevelDebug) {
		l.LogAttrs(ctx, slog.LevelDebug, "model repair",
			slog.String(logRun, s.runID),
			slog.Int(logStep, s.step),
			slog.String(logCall, ri.CallID),
			slog.String(logRepairKind, ri.Kind),
			slog.Int(logRepairBytesBefore, len(ri.Original)),
			slog.Int(logRepairBytesAfter, len(ri.Repaired)))
	}
}

// detailContent reports whether content may ride the development
// detail: the detail level on, and the run's content not off.
func (s *stepReport) detailContent() bool {
	if s.rec == nil || (s.rec.capture != nil && !*s.rec.capture) || contentflag.Off(s.Context) {
		return false
	}
	return s.obs.devDetailMarked()
}

// capUTF8 cuts b to at most n bytes on a rune boundary, returning the
// text and the bytes cut.
func capUTF8(b []byte, n int) (string, int) {
	if len(b) <= n {
		return string(b), 0
	}
	k := n
	for k > 0 && !utf8.RuneStart(b[k]) {
		k--
	}
	return string(b[:k]), len(b) - k
}

// --- the panic stack, under the development detail level ---

// The weft.panic event (ADR 0016's 2026-10-12 amendment parked it behind
// this switch): a contained tool or middleware panic's stack on its
// execute_tool span, under the development detail level with content on
// alone. The stack is content — paths, function names, the values a
// panic message carries — so third-party destinations never get it
// (otel/devdetail.go drops the event).
const (
	eventPanic        = "weft.panic"
	attrPanicStack    = attribute.Key("weft.panic.stack")
	attrPanicStackCut = attribute.Key("weft.panic.stack_cut")
	panicStackCap     = 16 << 10
)

// panicStackWanted is handlerMark.wantStack's answer, asked once a call
// panicked: the detail level on, and the run's content not off (the
// agent's Content, a parent run's mark, the recorder's capture).
func (a *Agent) panicStackWanted(ctx context.Context, rec *recorder) bool {
	if a.content != nil && !*a.content || contentflag.Off(ctx) {
		return false
	}
	if rec != nil && rec.capture != nil && !*rec.capture {
		return false
	}
	return a.obs.devDetailMarked()
}

// addPanicEvent puts the stack on the span, capped at 16 KiB on a rune
// boundary with the rest counted. The caller contains the span calls.
func addPanicEvent(span trace.Span, stack []byte) {
	text, cut := capUTF8(stack, panicStackCap)
	attrs := []attribute.KeyValue{attrPanicStack.String(text)}
	if cut > 0 {
		attrs = append(attrs, attrPanicStackCut.Int(cut))
	}
	span.AddEvent(eventPanic, trace.WithAttributes(attrs...))
}

// --- level 3: one span per layer, under the development detail level ---

// devDetailMarker is the duck-typed marker a LoggerProvider carries when
// the development detail level is on: weft/otel's pipeline provider
// answers true under otel.DevDetail() and the dev gate. A convention,
// like the RawBodies marker (raw.go), so a stock provider never turns
// it on.
type devDetailMarker interface{ DevDetail() bool }

// devDetailMarked asks the agent's LoggerProvider for the marker, the
// way rawMarked asks for RawBodies.
func (o *observer) devDetailMarked() bool {
	lp := o.lp
	if o.globalLP {
		lp = logglobal.GetLoggerProvider()
	}
	for range maxLayers {
		if lp == nil {
			return false
		}
		if m, ok := lp.(devDetailMarker); ok {
			return m.DevDetail()
		}
		u, ok := lp.(interface{ Unwrap() log.LoggerProvider })
		if !ok {
			return false
		}
		lp = u.Unwrap()
	}
	return false
}

// layerSpanKey carries the enclosing layer span to the next boundary
// down the chain — on its own key, never as the context's span, so the
// spans user code starts inside a layer keep the call's span (chat,
// execute_tool) as their parent: a destination that drops layer spans
// orphans nothing.
type layerSpanKey struct{}

// layerParent is the context a layer span starts under: the enclosing
// layer's span when there is one, else ctx's own (chat, execute_tool).
func layerParent(ctx context.Context) context.Context {
	if p, ok := ctx.Value(layerSpanKey{}).(trace.Span); ok && p != nil {
		return trace.ContextWithSpan(ctx, p)
	}
	return ctx
}

// layerModel is the boundary the loop puts outside each model layer
// under the detail level: a "layer <name>" span around that layer's
// stream. It forwards Info and Unwrap (so the ReportsAttempts walk and
// core.Unwrap see through it) and every event and error unchanged.
type layerModel struct {
	next  Model
	name  string
	index int
}

func (m *layerModel) Info() ModelInfo { return InfoOf(m.next) }

func (m *layerModel) Unwrap() Model { return m.next }

func (m *layerModel) Stream(ctx context.Context, req ModelRequest) iter.Seq2[ModelEvent, error] {
	s, _ := ctx.Value(reportKey{}).(*stepReport)
	if s == nil {
		return m.next.Stream(ctx, req)
	}
	return func(yield func(ModelEvent, error) bool) {
		inner, span := s.startLayer(ctx, m.name, m.index)
		var failed error
		defer func() {
			// A panic through the layer ends its span as one: Error,
			// error.type panic — then goes on unwinding unchanged.
			if p := recover(); p != nil {
				s.endLayerPanic(span)
				panic(p)
			}
			s.endLayer(span, failed)
		}()
		for ev, err := range m.next.Stream(inner, req) {
			if err != nil {
				failed = err
			}
			if !yield(ev, err) {
				return
			}
		}
	}
}

func (s *stepReport) startLayer(ctx context.Context, name string, index int) (context.Context, trace.Span) {
	var span trace.Span
	func() {
		defer s.containTraces()
		_, span = s.obs.tracer.Start(layerParent(ctx), layerSpanPrefix+name, trace.WithSpanKind(trace.SpanKindInternal))
		span.SetAttributes(attrRunID.String(s.runID), attrStepIndex.Int(s.step),
			attrLayerName.String(name), attrLayerKind.String(layerKindModel), attrLayerIndex.Int(index))
	}()
	if span == nil {
		return ctx, nil
	}
	return context.WithValue(ctx, layerSpanKey{}, span), span
}

// endLayerPanic ends a layer span a panic went through.
func (s *stepReport) endLayerPanic(span trace.Span) {
	if span == nil {
		return
	}
	defer s.containTraces()
	span.SetAttributes(semconv.ErrorTypeKey.String(layerPanic))
	span.SetStatus(codes.Error, "")
	span.End()
}

// layerPanic is a layer span's error.type when a panic went through it.
const layerPanic = "panic"

func (s *stepReport) endLayer(span trace.Span, err error) {
	if span == nil {
		return
	}
	var errType string
	if err != nil {
		errType = runErrorType(err)
	}
	defer s.containTraces()
	if err != nil {
		span.SetAttributes(semconv.ErrorTypeKey.String(errType))
		span.SetStatus(codes.Error, "")
	}
	span.End()
}

// detailedChain builds the WrapModel chain over base with a layer
// boundary outside each layer, first registered outermost; nil when a
// middleware returned a nil Model.
func (a *Agent) detailedChain(base Model) Model {
	m := base
	for i := len(a.modelMW) - 1; i >= 0; i-- {
		m = a.modelMW[i](m)
		if isNilModel(m) {
			return nil
		}
		m = &layerModel{next: m, name: describeModel(m).Name, index: i}
	}
	return m
}

// detailModel is the agent's model chain under the detail level. When
// the marker was known at New (a LoggerProvider option carrying it) New
// built the boundaries into the agent's one chain and this is it.
// Otherwise (the global provider, registered after New) it is a second
// chain built once, the first time a run asks: the middleware
// constructors run a second time, beside New's chain — the precedent of
// UseModel's rebuild — so a stateful middleware holds two states, and
// a caller that streams Agent.Model() itself (weft/thread's compaction
// summary) uses the plain chain. The agent's plain chain if that build
// fails.
func (a *Agent) detailModel() Model {
	a.detailOnce.Do(func() {
		if len(a.modelMW) > 0 {
			a.detailed = a.detailedChain(a.base)
		}
		if a.detailed == nil {
			a.detailed = a.model
		}
	})
	return a.detailed
}

// toolLayerSpans is the tool side's boundary state for one call under
// the detail level: the tracer, the run's recorder for containment, and
// the call's names.
type toolLayerSpans struct {
	tracer trace.Tracer
	rec    *recorder
}

// wrap puts a "layer <name>" span around one tool layer.
func (t *toolLayerSpans) wrap(next ToolCaller, name string, index int) ToolCaller {
	return func(ctx context.Context, call ToolCallPart) (out string, err error) {
		var span trace.Span
		func() {
			defer t.rec.containTraces(ctx)
			_, span = t.tracer.Start(layerParent(ctx), layerSpanPrefix+name, trace.WithSpanKind(trace.SpanKindInternal))
			attrs := []attribute.KeyValue{
				attrLayerName.String(name), attrLayerKind.String(layerKindTool), attrLayerIndex.Int(index),
				semconv.GenAIToolName(call.Name), semconv.GenAIToolCallID(call.ID),
			}
			if c, ok := CallFromContext(ctx); ok {
				attrs = append(attrs, attrRunID.String(c.RunID), attrStepIndex.Int(c.Step))
			}
			span.SetAttributes(attrs...)
		}()
		if span == nil {
			return next(ctx, call)
		}
		defer func() {
			if p := recover(); p != nil {
				// A panic through the layer ends its span as one, then
				// goes on unwinding unchanged (the containment outside
				// the chain reads the same value).
				func() {
					defer t.rec.containTraces(ctx)
					span.SetAttributes(semconv.ErrorTypeKey.String(layerPanic))
					span.SetStatus(codes.Error, "")
					span.End()
				}()
				panic(p)
			}
			kind := ""
			if err != nil && !errors.Is(err, ErrApprovalRequired) {
				kind = layerErrorType(err)
			}
			defer t.rec.containTraces(ctx)
			if kind != "" {
				span.SetAttributes(semconv.ErrorTypeKey.String(kind))
				span.SetStatus(codes.Error, "")
			}
			span.End()
		}()
		return next(context.WithValue(ctx, layerSpanKey{}, span), call)
	}
}

// layerErrorType is a tool layer span's error.type: the ToolError's
// code, a context error's name, else tool_error. Contained: the
// error's methods are its owner's.
func layerErrorType(err error) (kind string) {
	defer func() {
		if recover() != nil {
			kind = "tool_error"
		}
	}()
	if te, ok := errors.AsType[*ToolError](err); ok && te.Code != "" {
		return te.Code
	}
	if name, ok := ctxErrorType(err); ok {
		return name
	}
	return "tool_error"
}
