package otel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/mw"
	"github.com/weftgo/weft/core/wefttest"
	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// recLogs is a concurrency-safe log exporter that keeps every record.
type recLogs struct {
	mu   sync.Mutex
	recs []sdklog.Record
}

func (e *recLogs) Export(_ context.Context, recs []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, r := range recs {
		e.recs = append(e.recs, r.Clone())
	}
	return nil
}
func (e *recLogs) Shutdown(context.Context) error   { return nil }
func (e *recLogs) ForceFlush(context.Context) error { return nil }

// bridgedRecs is what the exporter kept from the bridge's scope.
func (e *recLogs) bridgedRecs() []sdklog.Record {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []sdklog.Record
	for _, r := range e.recs {
		if r.InstrumentationScope().Name == bridgeScope {
			out = append(out, r)
		}
	}
	return out
}

func recAttrs(r sdklog.Record) map[string]attribute.Value {
	m := map[string]attribute.Value{}
	r.WalkAttributes(func(kv attribute.KeyValue) bool {
		m[string(kv.Key)] = kv.Value
		return true
	})
	return m
}

func startBridgeTest(t *testing.T, opts ...Option) *Pipeline {
	t.Helper()
	p, err := Start(context.Background(), append([]Option{NoGlobal(), NoEnv(), Heartbeat(0)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	return p
}

// TestSlogHandlerTeesUnderASpan pins the bridge's record (record
// contract §4's app log records): a line logged under a span reaches
// the opted-in destination with the span's ids, the level mapped onto
// the OTLP severity, the message as the body and the attributes
// flattened; next still gets it at its own level; a line with no span,
// and one under the pipeline's quiet context, is not bridged.
func TestSlogHandlerTeesUnderASpan(t *testing.T) {
	exp := &recLogs{}
	p := startBridgeTest(t, Exporters(nil, exp, AppLogs(true)))
	var text bytes.Buffer
	logger := slog.New(p.SlogHandler(slog.NewTextHandler(&text, nil))).With("svc", "orders").WithGroup("req")
	ctx, span := p.TracerProvider().Tracer("t").Start(context.Background(), "work")
	logger.DebugContext(ctx, "looking", "id", 42, "dur", 1500*time.Millisecond,
		slog.Group("db", "table", "orders"), "err", errors.New("boom"), "weft.run.id", "spoof")
	logger.WarnContext(ctx, "slow", "ok", true)
	logger.Info("no span") // context.Background: never bridged
	logger.InfoContext(context.WithValue(ctx, quietKey{}, true), "quiet")
	span.End()
	if err := p.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := exp.bridgedRecs()
	if len(got) != 2 {
		t.Fatalf("bridged %d records, want the two under the span", len(got))
	}
	sc := span.SpanContext()
	d, w := got[0], got[1]
	if d.TraceID() != sc.TraceID() || d.SpanID() != sc.SpanID() {
		t.Errorf("trace/span = %s/%s, want the span's %s/%s", d.TraceID(), d.SpanID(), sc.TraceID(), sc.SpanID())
	}
	if d.Severity() != 5 || d.SeverityText() != "DEBUG" || w.Severity() != 13 || w.SeverityText() != "WARN" {
		t.Errorf("severities = %d %q, %d %q; want 5 DEBUG, 13 WARN", d.Severity(), d.SeverityText(), w.Severity(), w.SeverityText())
	}
	if d.Body().AsString() != "looking" {
		t.Errorf("body = %q", d.Body().AsString())
	}
	a := recAttrs(d)
	want := map[string]string{"svc": "orders", "req.id": "42", "req.dur": "1.5s", "req.db.table": "orders",
		"req.err": "boom", "req.weft.run.id": "spoof"}
	for k, v := range want {
		if got := a[k].String(); got != v {
			t.Errorf("attr %s = %q, want %q (all: %v)", k, got, v, a)
		}
	}
	if a["req.id"].Type() != attribute.INT64 {
		t.Errorf("req.id is %s, want an int", a["req.id"].Type())
	}
	if _, ok := a["weft.run.id"]; ok {
		t.Error("a weft.run.id attribute left the bridge: the line would read as weft's own record")
	}
	if n, ok := p.bridged(); !ok || n != 2 {
		t.Errorf("bridged() = %d, %v; want 2, true", n, ok)
	}
	// next: INFO and above, the no-span and quiet lines included.
	out := text.String()
	for _, s := range []string{"msg=slow", `msg="no span"`, "msg=quiet"} {
		if !strings.Contains(out, s) {
			t.Errorf("next did not get %s:\n%s", s, out)
		}
	}
	if strings.Contains(out, "looking") {
		t.Errorf("next got a DEBUG line it does not enable:\n%s", out)
	}
}

// TestAppLogsDefaults pins AppLogs' table: an Exporters (or OTLP)
// destination takes no bridged line unless it opts in, a Local one does
// unless it is content-off; with no destination taking them the handler
// is not even enabled (slog builds no record).
func TestAppLogsDefaults(t *testing.T) {
	dir := t.TempDir()
	third := &recLogs{}
	p := startBridgeTest(t, Exporters(nil, third), Local(filepath.Join(dir, "on.db")),
		Local(filepath.Join(dir, "off.db"), NoContent()))
	h := p.SlogHandler(nil)
	ctx, span := p.TracerProvider().Tracer("t").Start(context.Background(), "invoke_agent probe")
	if !h.Enabled(ctx, slog.LevelDebug) {
		t.Fatal("the content-on Local destination takes app logs: the handler should be enabled")
	}
	slog.New(h).InfoContext(ctx, "hello")
	span.End()
	if err := p.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(third.bridgedRecs()); n != 0 {
		t.Errorf("the Exporters destination got %d bridged lines without AppLogs(true)", n)
	}
	var flags []string
	for _, rt := range p.dests {
		flags = append(flags, fmt.Sprint(rt.logProc.(*destProc).appLogs))
	}
	if strings.Join(flags, " ") != "false true false" {
		t.Errorf("appLogs per destination (exporters, local, local content-off) = %v, want false true false", flags)
	}
	// A pipeline whose only destination takes none: not enabled.
	q := startBridgeTest(t, Exporters(nil, &recLogs{}))
	qctx, qs := q.TracerProvider().Tracer("t").Start(context.Background(), "x")
	defer qs.End()
	if q.SlogHandler(nil).Enabled(qctx, slog.LevelError) {
		t.Error("no destination takes app logs, yet the handler is enabled")
	}
}

// TestCoreRunLinesBridged: an agent whose weft.Logger is a bridged
// logger sends its run lines (Debug) and mw.Log's to the pipeline, each
// under the span it was logged in — the run's invoke_agent span for
// "run start", the chat span for mw.Log's "model request" — and the
// run's root span carries weft.logs.bridged.
func TestCoreRunLinesBridged(t *testing.T) {
	exp := &recLogs{}
	spans := tracetest.NewInMemoryExporter()
	p := startBridgeTest(t, Exporters(spans, exp, AppLogs(true)))
	logger := slog.New(p.SlogHandler(nil))
	agt := core.New(wefttest.Script(wefttest.Say("done")), core.Name("bridged"),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()),
		core.Logger(logger), core.WrapModel(mw.Log(logger)))
	if _, err := agt.Generate(context.Background(), core.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	if err := p.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	byID := map[string]string{}
	var root string
	for _, s := range spans.GetSpans() {
		byID[s.SpanContext.SpanID().String()] = s.Name
		if strings.HasPrefix(s.Name, "invoke_agent") {
			root = s.SpanContext.SpanID().String()
			found := false
			for _, kv := range s.Attributes {
				if string(kv.Key) == attrLogsBridged {
					found = true
				}
			}
			if !found {
				t.Errorf("the root span carries no %s: %v", attrLogsBridged, s.Attributes)
			}
		}
	}
	lines := map[string]string{}
	for _, r := range exp.bridgedRecs() {
		lines[r.Body().AsString()] = byID[r.SpanID().String()]
	}
	if lines["run start"] != byID[root] || lines["run finish"] != byID[root] {
		t.Errorf("the core's run lines under %q / %q, want the root %q (lines: %v)", lines["run start"], lines["run finish"], byID[root], lines)
	}
	if !strings.HasPrefix(lines["model request"], "chat") || !strings.HasPrefix(lines["model call"], "chat") {
		t.Errorf("mw.Log's and the core's model lines under %q / %q, want the chat span (lines: %v)", lines["model request"], lines["model call"], lines)
	}
}

// TestNoBridgeNoMark: a pipeline with no bridge stamps neither the root
// span nor the heartbeat — the absence the logs route's hint reads.
func TestNoBridgeNoMark(t *testing.T) {
	spans := tracetest.NewInMemoryExporter()
	p := startBridgeTest(t, Exporters(spans, &recLogs{}))
	agt := core.New(wefttest.Script(wefttest.Say("done")), core.Name("plain"),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	if _, err := agt.Generate(context.Background(), core.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	if err := p.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, s := range spans.GetSpans() {
		for _, kv := range s.Attributes {
			if string(kv.Key) == attrLogsBridged {
				t.Errorf("span %s carries %s without a bridge", s.Name, attrLogsBridged)
			}
		}
	}
	if _, ok := p.bridged(); ok {
		t.Error("bridged() reports a bridge nobody built")
	}
}

// TestHeartbeatCarriesBridgedCount: with a bridge, every heartbeat says
// how many lines it emitted (weft.logs.bridged, record contract §4).
func TestHeartbeatCarriesBridgedCount(t *testing.T) {
	exp := &recLogs{}
	p := startBridgeTest(t, Exporters(nil, exp))
	tr := newRunTracker()
	tr.open["r1"] = map[string]string{}
	heartbeats(context.Background(), p.lp.Logger(instrumentationName), tr, nil, func() (int64, bool) { return 7, true })
	heartbeats(context.Background(), p.lp.Logger(instrumentationName), tr, nil, func() (int64, bool) { return 0, false })
	if err := p.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	var vals []string
	exp.mu.Lock()
	for _, r := range exp.recs {
		if r.EventName() == eventHeartbeat {
			v, ok := recAttrs(r)[attrLogsBridged]
			vals = append(vals, fmt.Sprintf("%v %s", ok, v.String()))
		}
	}
	exp.mu.Unlock()
	if strings.Join(vals, ",") != "true 7,false " {
		t.Errorf("heartbeats' %s = %v, want [true 7, false]", attrLogsBridged, vals)
	}
}

// TestSlogOptionWrapsDefault: Install(…, Slog()) wraps slog.Default —
// a plain slog.DebugContext under a span reaches the pipeline, the
// line still reaches the program's own writer at INFO — and the
// shutdown puts the default, the log package's writer and its flags
// back.
func TestSlogOptionWrapsDefault(t *testing.T) {
	prevDefault := slog.Default()
	var out bytes.Buffer
	prevW, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&out)
	defer func() { log.SetOutput(prevW); log.SetFlags(prevFlags); slog.SetDefault(prevDefault) }()

	exp := &recLogs{}
	shutdown := Install(NoEnv(), Heartbeat(0), Exporters(nil, exp, AppLogs(true)), Slog())
	if _, ok := slog.Default().Handler().(*slogHandler); !ok {
		shutdown()
		t.Fatalf("slog.Default's handler is %T, want the bridge", slog.Default().Handler())
	}
	installedMu.RLock()
	p := installedPipeline
	installedMu.RUnlock()
	ctx, span := p.TracerProvider().Tracer("t").Start(context.Background(), "work")
	slog.DebugContext(ctx, "debug line")
	slog.InfoContext(ctx, "info line")
	log.Print("classic line") // the log package now goes through the bridge's next
	span.End()
	_ = p.ForceFlush(context.Background())
	shutdown()

	var bodies []string
	for _, r := range exp.bridgedRecs() {
		bodies = append(bodies, r.Body().AsString())
	}
	if strings.Join(bodies, ",") != "debug line,info line" {
		t.Errorf("bridged %v, want the two lines under the span", bodies)
	}
	if s := out.String(); !strings.Contains(s, "info line") || !strings.Contains(s, "classic line") || strings.Contains(s, "debug line") {
		t.Errorf("the program's writer got:\n%s\nwant the INFO line and the log.Print line, not the DEBUG one", s)
	}
	if slog.Default() != prevDefault {
		t.Error("shutdown left the bridge as slog.Default")
	}
	if log.Writer() != &out || log.Flags() != prevFlags {
		t.Error("shutdown did not restore the log package's writer and flags")
	}
}

// TestSlogHandlerConcurrent: one handler (and its With/WithGroup
// copies) on many goroutines — the stress gate's target.
func TestSlogHandlerConcurrent(t *testing.T) {
	exp := &recLogs{}
	p := startBridgeTest(t, Exporters(nil, exp, AppLogs(true)))
	base := slog.New(p.SlogHandler(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError + 4})))
	ctx, span := p.TracerProvider().Tracer("t").Start(context.Background(), "work")
	defer span.End()
	const g, n = 16, 50
	var wg sync.WaitGroup
	for i := range g {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l := base.With("g", i).WithGroup("w")
			for j := range n {
				l.InfoContext(ctx, "line", "j", j)
			}
		}()
	}
	wg.Wait()
	if err := p.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(exp.bridgedRecs()); got != g*n {
		t.Errorf("bridged %d lines, want %d", got, g*n)
	}
	if c, _ := p.bridged(); c != g*n {
		t.Errorf("count %d, want %d", c, g*n)
	}
}
