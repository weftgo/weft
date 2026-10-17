package otel

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// The bridge under the destinations' content policy (U11.1-fix): an app
// log line is content like a transcript record — redactable at the
// source, capped, stripped for a content-off destination — and the
// bridge mark says only what a destination actually received.

// logUnder logs one line through h under a fresh span of p.
func logUnder(p *Pipeline, h slog.Handler, msg string, args ...any) {
	ctx, span := p.TracerProvider().Tracer("t").Start(context.Background(), "work")
	slog.New(h).InfoContext(ctx, msg, args...)
	span.End()
}

// TestAppLogRedactedAndCapped: a content-on destination applies Redact
// (ContentLog) to the message and every string attribute, then caps each
// at MaxBytes and counts the cut; ints are left alone, and the bridge's
// own marks are never handed to Redact.
func TestAppLogRedactedAndCapped(t *testing.T) {
	exp := &recLogs{}
	var kinds []core.ContentKind
	var seen []string
	redact := func(k core.ContentKind, s string) string {
		kinds = append(kinds, k)
		seen = append(seen, s)
		return strings.ReplaceAll(s, "4111", "####")
	}
	p := startBridgeTest(t, Exporters(nil, exp, WithContent(ContentConfig{MaxBytes: 10, Redact: redact}), AppLogs(true)))
	logUnder(p, p.SlogHandler(nil), "card 4111 charged", "card", "4111", "n", 7, "long", strings.Repeat("x", 25))
	if err := p.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	recs := exp.bridgedRecs()
	if len(recs) != 1 {
		t.Fatalf("got %d bridged records, want 1", len(recs))
	}
	r := recs[0]
	a := recAttrs(r)
	if got := r.Body().AsString(); got != "card #### " {
		t.Errorf("body = %q, want the redacted message cut at 10 bytes", got)
	}
	if a["card"].AsString() != "####" || a["n"].AsInt64() != 7 || a["long"].AsString() != strings.Repeat("x", 10) {
		t.Errorf("attrs = %v, want card redacted, n intact, long cut at 10", a)
	}
	// body 17 → 10 (7 cut) + long 25 → 10 (15 cut)
	if got := a[attrTruncated].AsInt64(); got != 22 {
		t.Errorf("%s = %d, want 22", attrTruncated, got)
	}
	for _, k := range kinds {
		if k != ContentLog {
			t.Errorf("Redact saw kind %q, want %q", k, ContentLog)
		}
	}
	if len(seen) != 3 {
		t.Errorf("Redact saw %q, want the body and the two string attributes", seen)
	}
}

// TestAppLogDefaultCap: with MaxBytes 0 the 32 KiB default bounds a
// line, as it bounds an event field — a megabyte message never leaves
// whole.
func TestAppLogDefaultCap(t *testing.T) {
	exp := &recLogs{}
	p := startBridgeTest(t, Exporters(nil, exp, WithContent(), AppLogs(true)))
	logUnder(p, p.SlogHandler(nil), strings.Repeat("secret ", 150_000))
	_ = p.ForceFlush(context.Background())
	recs := exp.bridgedRecs()
	if len(recs) != 1 {
		t.Fatalf("got %d bridged records", len(recs))
	}
	if n := len(recs[0].Body().AsString()); n != 32<<10 {
		t.Errorf("body is %d bytes, want the 32 KiB default cap", n)
	}
	if got := recAttrs(recs[0])[attrTruncated].AsInt64(); got != 1_050_000-32<<10 {
		t.Errorf("%s = %d", attrTruncated, got)
	}
}

// TestAppLogStrippedWhenContentOff: AppLogs(true) admits the lines to a
// content-off destination, never bypasses its policy — the message and
// string values emptied, weft.content=stripped, the severity, span and
// other attributes kept as metadata.
func TestAppLogStrippedWhenContentOff(t *testing.T) {
	exp := &recLogs{}
	called := 0
	p := startBridgeTest(t, Content(ContentConfig{Redact: func(_ core.ContentKind, s string) string { called++; return s }}),
		Exporters(nil, exp, NoContent(), AppLogs(true)))
	logUnder(p, p.SlogHandler(nil), "user said: my password is hunter2", "prompt", "hunter2", "tokens", 12)
	_ = p.ForceFlush(context.Background())
	recs := exp.bridgedRecs()
	if len(recs) != 1 {
		t.Fatalf("got %d bridged records, want the line admitted stripped", len(recs))
	}
	r := recs[0]
	a := recAttrs(r)
	if r.Body().AsString() != "" || a["prompt"].AsString() != "" {
		t.Errorf("content left a content-off destination: body %q, prompt %q", r.Body().AsString(), a["prompt"].AsString())
	}
	if a[attrContentKey].AsString() != contentStripped || a["tokens"].AsInt64() != 12 || r.Severity() != otellog.SeverityInfo {
		t.Errorf("metadata lost: %v, severity %d", a, r.Severity())
	}
	if !r.SpanID().IsValid() {
		t.Error("the stripped line lost its span")
	}
	if called != 0 {
		t.Errorf("Redact ran %d times on a content-off destination (nothing to redact)", called)
	}
}

// TestAppLogRedactPanicDrops: a Redact panic drops the line — counted as
// the destination's loss — and never sends it unredacted.
func TestAppLogRedactPanicDrops(t *testing.T) {
	exp := &recLogs{}
	p := startBridgeTest(t, Exporters(nil, exp, WithContent(ContentConfig{Redact: func(core.ContentKind, string) string { panic("boom") }}), AppLogs(true)))
	logUnder(p, p.SlogHandler(nil), "secret")
	_ = p.ForceFlush(context.Background())
	if n := len(exp.bridgedRecs()); n != 0 {
		t.Errorf("%d bridged records left after Redact panicked, want 0", n)
	}
	if got := p.dests[0].logProc.(*destProc).drops.lost(); got != 1 {
		t.Errorf("lost = %d, want the dropped line counted", got)
	}
}

// TestAppLogWeftKeysRenamed: every app key under weft. is renamed
// slog.weft.…, so a line cannot pose as a weft record — routed as a
// transcript batch (and dropped) or tiered as a run's bracket.
func TestAppLogWeftKeysRenamed(t *testing.T) {
	exp := &recLogs{}
	p := startBridgeTest(t, Exporters(nil, exp, WithContent(ContentConfig{Redact: func(_ core.ContentKind, s string) string { return s }}), AppLogs(true)))
	h := p.SlogHandler(nil)
	logUnder(p, h, "spoof", "weft.record", "messages")
	logUnder(p, h.WithAttrs([]slog.Attr{slog.String("weft.session.id", "s_fake")}), "spoof2",
		"weft.event.type", "run_start", "weft.record", "event", slog.Group("weft", "run.id", "r"))
	_ = p.ForceFlush(context.Background())
	recs := exp.bridgedRecs()
	if len(recs) != 2 {
		t.Fatalf("got %d bridged records, want both spoofs delivered as app logs", len(recs))
	}
	for _, r := range recs {
		for k := range recAttrs(r) {
			if strings.HasPrefix(k, "weft.") {
				t.Errorf("%q: app key %s left the bridge unrenamed", r.Body().AsString(), k)
			}
		}
	}
	a := recAttrs(recs[1])
	for _, k := range []string{"slog.weft.session.id", "slog.weft.event.type", "slog.weft.record", "slog.weft.run.id"} {
		if _, ok := a[k]; !ok {
			t.Errorf("missing %s: %v", k, a)
		}
	}
	if got := p.dests[0].logProc.(*destProc).drops.lost(); got != 0 {
		t.Errorf("lost = %d: a spoofed line was treated as a weft record", got)
	}
}

// TestAppLogShedWithDeltas: a bridged line is refused at the delta
// threshold (half the queue), where a weft record is still admitted.
func TestAppLogShedWithDeltas(t *testing.T) {
	exp := &recLogs{}
	p := startBridgeTest(t, Exporters(nil, exp, WithContent(), AppLogs(true), QueueSize(64)))
	dp := p.dests[0].logProc.(*destProc)
	dp.adm.inflight.Store(dp.adm.limit(tierDelta)) // half full
	logUnder(p, p.SlogHandler(nil), "chatty debug")
	if got := dp.adm.refused.Load(); got != 1 {
		t.Fatalf("refused = %d, want the bridged line shed at half the queue", got)
	}
	var rec otellog.Record
	rec.SetBody(attribute.StringValue("app record"))
	p.lp.Logger("app").Emit(context.Background(), rec)
	if got := dp.adm.refused.Load(); got != 1 {
		t.Errorf("refused = %d: a durable record was shed at the delta threshold", got)
	}
	dp.adm.inflight.Add(-dp.adm.limit(tierDelta))
}

// TestAppLogAttrsBounded: past 120 attributes the bridge keeps the first
// 120 and counts the rest in weft.log.attrs_dropped — never the SDK's
// silent cut.
func TestAppLogAttrsBounded(t *testing.T) {
	exp := &recLogs{}
	p := startBridgeTest(t, Exporters(nil, exp, WithContent(), AppLogs(true)))
	var args []any
	for i := range 1000 {
		args = append(args, fmt.Sprintf("k%d", i), i)
	}
	logUnder(p, p.SlogHandler(nil), "wide", args...)
	_ = p.ForceFlush(context.Background())
	recs := exp.bridgedRecs()
	if len(recs) != 1 {
		t.Fatalf("got %d records", len(recs))
	}
	a := recAttrs(recs[0])
	if got := a[attrLogAttrsDropped].AsInt64(); got != 880 {
		t.Errorf("%s = %d, want 880", attrLogAttrsDropped, got)
	}
	if recs[0].DroppedAttributes() != 0 {
		t.Errorf("the SDK dropped %d attributes silently", recs[0].DroppedAttributes())
	}
	if _, ok := a["k119"]; !ok {
		t.Error("the first 120 attributes are not all kept")
	}
}

// bridgeMarks runs one agent on p and reports, per span exporter,
// whether its invoke_agent span carries weft.logs.bridged.
func bridgeMarks(t *testing.T, p *Pipeline, exps ...*tracetest.InMemoryExporter) []bool {
	t.Helper()
	agt := core.New(wefttest.Script(wefttest.Say("done")), core.Name("m"),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	if _, err := agt.Generate(context.Background(), core.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	if err := p.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	var out []bool
	for _, e := range exps {
		marked := false
		for _, s := range e.GetSpans() {
			if strings.HasPrefix(s.Name, "invoke_agent") {
				for _, kv := range s.Attributes {
					marked = marked || string(kv.Key) == attrLogsBridged
				}
			}
		}
		out = append(out, marked)
	}
	return out
}

// TestBridgeMarkPerDestination: the mark rides only to a destination
// that takes the bridged lines — a content-off one, an AppLogs(false)
// one and a third party that did not opt in store the root unmarked (so
// their empty logs page carries the hint), on its spans and its
// heartbeats alike.
func TestBridgeMarkPerDestination(t *testing.T) {
	on, off, optedOut, third := tracetest.NewInMemoryExporter(), tracetest.NewInMemoryExporter(),
		tracetest.NewInMemoryExporter(), tracetest.NewInMemoryExporter()
	onLogs, thirdLogs := &recLogs{}, &recLogs{}
	p := startBridgeTest(t,
		Exporters(on, onLogs, WithContent(), AppLogs(true)),
		Exporters(off, &recLogs{}, NoContent()),
		Exporters(optedOut, &recLogs{}, WithContent(), AppLogs(false)),
		Exporters(third, thirdLogs))
	_ = p.SlogHandler(nil)
	if got := fmt.Sprint(bridgeMarks(t, p, on, off, optedOut, third)); got != "[true false false false]" {
		t.Errorf("root marked per destination (app logs on, content-off, AppLogs(false), third party) = %s, want [true false false false]", got)
	}
	tr := newRunTracker()
	tr.open["r1"] = map[string]string{}
	heartbeats(context.Background(), p.lp.Logger(instrumentationName), tr, nil, p.bridged)
	_ = p.ForceFlush(context.Background())
	beat := func(e *recLogs) bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		for _, r := range e.recs {
			if r.EventName() == eventHeartbeat {
				_, ok := recAttrs(r)[attrLogsBridged]
				return ok
			}
		}
		t.Fatal("no heartbeat exported")
		return false
	}
	if !beat(onLogs) || beat(thirdLogs) {
		t.Errorf("heartbeat mark: app-log destination %v (want true), third party %v (want false)", beat(onLogs), beat(thirdLogs))
	}
}

// TestBridgeMarkNotSticky: building a package-level SlogHandler marks
// nothing — the installed pipeline is bridged once a line was teed into
// it — and Slog's wrap stops marking once the program replaces
// slog.Default.
func TestBridgeMarkNotSticky(t *testing.T) {
	prevDefault := slog.Default()
	defer slog.SetDefault(prevDefault)

	spans := tracetest.NewInMemoryExporter()
	shutdown := Install(NoEnv(), Heartbeat(0), Exporters(spans, &recLogs{}, WithContent(), AppLogs(true)))
	installedMu.RLock()
	p := installedPipeline
	installedMu.RUnlock()
	h := SlogHandler(nil) // built, unused
	if _, ok := p.bridged(); ok {
		shutdown()
		t.Fatal("an unused package-level SlogHandler marked the installed pipeline")
	}
	logUnder(p, h, "first line")
	if _, ok := p.bridged(); !ok {
		t.Error("a line teed through the package-level handler did not mark the pipeline")
	}
	shutdown()

	// A later pipeline is not marked by the earlier handler's use.
	q := startBridgeTest(t, Exporters(nil, &recLogs{}, WithContent(), AppLogs(true)))
	if _, ok := q.bridged(); ok {
		t.Error("a later pipeline inherited the bridge mark")
	}

	// Slog(), then the program replaces slog.Default: no longer live.
	shutdown = Install(NoEnv(), Heartbeat(0), Exporters(nil, &recLogs{}, WithContent(), AppLogs(true)), Slog())
	defer shutdown()
	installedMu.RLock()
	r := installedPipeline
	installedMu.RUnlock()
	if _, ok := r.bridged(); !ok {
		t.Fatal("Slog's wrap is not live right after Install")
	}
	// A line teed through Slog's own wrap does not make the mark sticky.
	logUnder(r, slog.Default().Handler(), "through the default")
	if n, _ := r.bridged(); n != 1 {
		t.Fatalf("bridged count = %d, want 1", n)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	if _, ok := r.bridged(); ok {
		t.Error("the program replaced slog.Default, yet the pipeline still reads bridged")
	}
}

// TestSlogNonLIFOShutdown: two Slog pipelines shut down out of order
// leave the program on what the first found (slog's built-in default
// and the log package's writer), never a closed bridge's TextHandler;
// and the substitute TextHandler honours slog.SetLogLoggerLevel.
func TestSlogNonLIFOShutdown(t *testing.T) {
	prevDefault := slog.Default()
	prevW, prevFlags := log.Writer(), log.Flags()
	var out bytes.Buffer
	log.SetOutput(&out)
	prevLevel := slog.SetLogLoggerLevel(slog.LevelDebug)
	defer func() {
		slog.SetLogLoggerLevel(prevLevel)
		log.SetOutput(prevW)
		log.SetFlags(prevFlags)
		slog.SetDefault(prevDefault)
	}()
	if !isBuiltinSlogHandler(slog.Default().Handler()) {
		t.Skip("the test process is not on slog's built-in handler")
	}
	builtin := slog.Default()
	prevPrefix := log.Prefix()
	log.SetPrefix("[app] ")
	defer log.SetPrefix(prevPrefix)

	a := Install(NoEnv(), Heartbeat(0), Exporters(nil, &recLogs{}, WithContent(), AppLogs(true)), Slog())
	slog.Debug("debug through the substitute")
	if !strings.Contains(out.String(), "debug through the substitute") {
		a()
		t.Fatalf("SetLogLoggerLevel(DEBUG) not honoured by the substitute handler:\n%s", out.String())
	}
	if !strings.HasPrefix(out.String(), "[app] time=") {
		a()
		t.Fatalf("the substitute handler dropped log.Prefix():\n%s", out.String())
	}
	b := Install(NoEnv(), Heartbeat(0), Exporters(nil, &recLogs{}, WithContent(), AppLogs(true)), Slog())
	a() // out of order
	b()
	if slog.Default() != builtin {
		t.Errorf("after a non-LIFO shutdown slog.Default's handler is %T, want slog's built-in default", slog.Default().Handler())
	}
	if log.Writer() != &out || log.Flags() != prevFlags {
		t.Error("the log package's writer and flags were not restored")
	}
}

// TestBridgeMarkLocalContentOff: a content-off Local sink beside a
// bridged handler (the line goes nowhere it is stored) stores the run's
// root unmarked — its logs page then carries the hint.
func TestBridgeMarkLocalContentOff(t *testing.T) {
	p := startBridgeTest(t, Local(filepath.Join(t.TempDir(), "off.db"), NoContent()))
	_ = p.SlogHandler(nil)
	agt := core.New(wefttest.Script(wefttest.Say("done")), core.Name("m"),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	res, err := agt.Generate(context.Background(), core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	_ = p.ForceFlush(context.Background())
	spans, err := p.dests[0].localDB.RunSpans(context.Background(), res.ID)
	if err != nil || len(spans) == 0 {
		t.Fatalf("spans %d, err %v", len(spans), err)
	}
	for _, s := range spans {
		if _, ok := s.Attrs[attrLogsBridged]; ok {
			t.Errorf("span %s stored with %s on a content-off Local sink that takes no app logs", s.Name, attrLogsBridged)
		}
	}
}
