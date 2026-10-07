package otel

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// The production-readiness pass (2026-10-02): one test per defect, each
// failing on the code as released in otel/v0.1.0.

// An https destination stays https whatever the process environment
// says. The OTLP exporters read OTEL_EXPORTER_OTLP_ENDPOINT themselves
// and take their TLS posture from its scheme; a bare host handed to
// them (WithEndpoint) leaves that posture in force, so a sidecar
// collector's http:// variable sent Studio's bearer token and content
// in cleartext — NoEnv or not.
func TestEnvEndpointDoesNotDowngradeTLS(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	first := make(chan []byte, 16)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
				buf := make([]byte, 5)
				if n, _ := conn.Read(buf); n > 0 {
					first <- buf[:n]
				}
			}()
		}
	}()

	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(),
		Studio("https://"+ln.Addr().String(), "secret-token", Timeout(time.Second)),
		Heartbeat(0),
	)
	if err != nil {
		t.Fatal(err)
	}
	agt := weft.New(
		wefttest.Script(wefttest.Say("tls")),
		weft.TracerProvider(p.TracerProvider()),
		weft.LoggerProvider(p.LoggerProvider()),
	)
	if _, err := agt.Generate(context.Background(), weft.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = p.Shutdown(ctx) // the handshake fails (no TLS server); what left the process is the subject

	select {
	case b := <-first:
		if bytes.HasPrefix(b, []byte("POST")) {
			t.Fatalf("an https:// destination sent a cleartext HTTP request (first bytes %q): "+
				"the environment's http:// endpoint downgraded it", b)
		}
		if b[0] != 0x16 {
			t.Fatalf("first bytes %x are not a TLS handshake", b)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nothing reached the destination")
	}
}

// A run whose invoke_agent span is sampled out still leaves the open
// set when it ends without run_finish (failed or cancelled): the SDK
// never shows a dropped span to a processor, so the tracker kept the
// run forever — an entry and a heartbeat per interval for the rest of
// the process, and a failed run reading running in every sink. An
// unsampled incoming traceparent under the default ParentBased sampler
// is the everyday form.
func TestTrackerForgetsUnsampledFailedRun(t *testing.T) {
	mem := newMemExporter()
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(),
		Exporters(nil, mem),
		Sampler(sdktrace.NeverSample()),
		Heartbeat(0),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(testCtx(t)) }()
	agt := weft.New(
		wefttest.Script(wefttest.Fail(errors.New("boom"))),
		weft.Name("unsampled"),
		weft.TracerProvider(p.TracerProvider()),
		weft.LoggerProvider(p.LoggerProvider()),
	)
	if _, err := agt.Generate(context.Background(), weft.Prompt("go")); err == nil {
		t.Fatal("the scripted failure did not fail the run (test bug)")
	}
	if open := p.tracker.snapshot(); len(open) != 0 {
		t.Fatalf("a failed, sampled-out run stays open forever: %v", open)
	}
}

// The sampled-out span the tracker now sees is still never exported.
func TestUnsampledRunSpansAreNotExported(t *testing.T) {
	spans := &spanCapture{}
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(),
		Exporters(spans, nil, BatchDelay(10*time.Millisecond)),
		Sampler(sdktrace.NeverSample()),
		Heartbeat(0),
	)
	if err != nil {
		t.Fatal(err)
	}
	agt := weft.New(
		wefttest.Script(wefttest.Say("quiet")),
		weft.Name("unsampled"),
		weft.TracerProvider(p.TracerProvider()),
		weft.LoggerProvider(p.LoggerProvider()),
	)
	if _, err := agt.Generate(context.Background(), weft.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	if n := spans.count(); n != 0 {
		t.Errorf("%d spans exported under NeverSample", n)
	}
}

// spanCapture is a span exporter that keeps what it was handed.
type spanCapture struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
	fail  error
}

func (c *spanCapture) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail != nil {
		return c.fail
	}
	c.spans = append(c.spans, spans...)
	return nil
}

func (c *spanCapture) Shutdown(context.Context) error { return nil }

func (c *spanCapture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.spans)
}

// Heartbeat's documented "0 disables" extends to a negative interval:
// time.NewTicker panics on one, on the heartbeat goroutine, where
// nothing can recover it — the program died at Install.
func TestNegativeHeartbeatDisables(t *testing.T) {
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(),
		Exporters(nil, newMemExporter()),
		Heartbeat(-time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // the goroutine's own panic, were it started
	if err := p.Shutdown(testCtx(t)); err != nil {
		t.Fatal(err)
	}
}

// goroutinesSettle waits for the goroutine count to come back to at
// most want (processor goroutines exit asynchronously after Shutdown).
func goroutinesSettle(want int) int {
	deadline := time.Now().Add(2 * time.Second)
	n := runtime.NumGoroutine()
	for n > want && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		n = runtime.NumGoroutine()
	}
	return n
}

// A Start that fails halfway shuts down what it had already built: the
// batch processors of the destinations before the failing one each run
// a goroutine that only Shutdown ends.
func TestStartFailureReleasesBuiltDestinations(t *testing.T) {
	before := runtime.NumGoroutine()
	const rounds = 10
	for range rounds {
		_, err := Start(testCtx(t), NoGlobal(), NoEnv(),
			Exporters(&spanCapture{}, newMemExporter()),
			OTLP("http://collector.invalid:4318"), // non-loopback http without Insecure(): cannot build
		)
		if err == nil {
			t.Fatal("Start built an http:// non-loopback destination without Insecure()")
		}
	}
	if after := goroutinesSettle(before + rounds - 1); after >= before+rounds {
		t.Fatalf("goroutines grew %d → %d over %d failed Starts: the built destinations leak", before, after, rounds)
	}
}

// openFilesUnder counts this process's open descriptors on files under
// dir (Linux's /proc; the test skips elsewhere).
func openFilesUnder(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skip("no /proc/self/fd on this platform")
	}
	var n int
	for _, e := range entries {
		if target, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name())); err == nil && strings.HasPrefix(target, dir) {
			n++
		}
	}
	return n
}

// Every Local destination's database closes at Shutdown, not only the
// last one configured; and a Start that fails after opening one closes
// it.
func TestEveryLocalDBCloses(t *testing.T) {
	dir := t.TempDir()
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(),
		Local(filepath.Join(dir, "a.db")),
		Local(filepath.Join(dir, "b.db")),
		Heartbeat(0),
	)
	if err != nil {
		t.Fatal(err)
	}
	if openFilesUnder(t, dir) == 0 {
		t.Fatal("no open database files while running (test bug)")
	}
	if err := p.Shutdown(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	if n := openFilesUnder(t, dir); n != 0 {
		t.Errorf("%d database descriptors still open after Shutdown with two Local destinations", n)
	}

	// The half-built path: a Local with neither signal opened its DB
	// before failing.
	dir2 := t.TempDir()
	if _, err := Start(testCtx(t), NoGlobal(), NoEnv(),
		Local(filepath.Join(dir2, "c.db"), Signals(false, false))); err == nil {
		t.Fatal("a destination with neither signal built")
	}
	if n := openFilesUnder(t, dir2); n != 0 {
		t.Errorf("%d database descriptors still open after a failed Start", n)
	}
}

// The package accessors stop handing out a shut-down pipeline's closed
// database and dead endpoint.
func TestPackageAccessorsClearOnShutdown(t *testing.T) {
	shutdown := Install(NoEnv(),
		Local(filepath.Join(t.TempDir(), "acc.db")),
		Studio("https://studio.invalid", "tok", Signals(true, false)),
	)
	if LocalDB() == nil {
		shutdown()
		t.Fatal("LocalDB() is nil while installed")
	}
	if u, tok := StudioEndpoint(); u != "https://studio.invalid" || tok != "tok" {
		shutdown()
		t.Fatalf("StudioEndpoint() = %q, %q while installed", u, tok)
	}
	shutdown()
	if db := LocalDB(); db != nil {
		t.Error("LocalDB() still returns the closed database after shutdown")
	}
	if u, _ := StudioEndpoint(); u != "" {
		t.Errorf("StudioEndpoint() = %q after shutdown", u)
	}
}

// OTEL_EXPORTER_OTLP_HEADERS values are percent-encoded (the OTel
// spec's W3C-baggage form; every vendor's "Authorization=Basic%20…"
// snippet). Sent undecoded, the collector answers 401 to every export.
func TestEnvHeadersPercentDecoded(t *testing.T) {
	got := parseHeaderList("Authorization=Basic%20dXNlcjpwYXNz, x-team = a%2Cb ,bare")
	want := map[string]string{"Authorization": "Basic dXNlcjpwYXNz", "x-team": "a,b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseHeaderList = %v, want %v", got, want)
	}
	// A value that is not valid percent-encoding rides as written.
	if got := parseHeaderList("k=100%"); got["k"] != "100%" {
		t.Errorf("undecodable value = %q, want it verbatim", got["k"])
	}
}

// A heartbeat carries the run's metadata, not run_start's record
// contract: with weft.event.type=run_start inherited, every heartbeat
// counted as a run start in any backend that groups on the attribute.
func TestHeartbeatCarriesNoEventContract(t *testing.T) {
	tr := newRunTracker()
	_ = tr.OnEmit(context.Background(), sdkRecordWith(t, "weft.event", `{"type":"run_start","id":"r1"}`,
		attribute.String("weft.record", "event"),
		attribute.String("weft.run.id", "r1"),
		attribute.String("weft.event.type", "run_start"),
		attribute.String("weft.content", "full"),
		attribute.Int64("weft.event.pos", 0),
		attribute.String("gen_ai.agent.name", "hb"),
		attribute.String("weft.session.id", "s1"),
		attribute.String("tenant", "acme"),
	))
	capture := &recordCapture{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(capture)))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	heartbeats(context.Background(), provider.Logger(instrumentationName), tr)
	if len(capture.records) != 1 {
		t.Fatalf("%d heartbeats for one open run", len(capture.records))
	}
	hb := capture.records[0]
	got := map[string]string{}
	hb.WalkAttributes(func(kv attribute.KeyValue) bool {
		got[string(kv.Key)] = kv.Value.AsString()
		return true
	})
	want := map[string]string{
		"weft.record": "heartbeat", "weft.run.id": "r1",
		"gen_ai.agent.name": "hb", "weft.session.id": "s1", "tenant": "acme",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("heartbeat attributes = %v\nwant %v", got, want)
	}
}

// A panicking Redact is the caller's bug, but it must not unwind the
// agent's run (Emit runs on the run's goroutine) nor let the record
// through unredacted: the chain falls back to the content-off shape.
func TestRedactPanicStripsInsteadOfUnwinding(t *testing.T) {
	mem := newMemExporter()
	p := &destProc{
		name: "test", inner: sdklog.NewSimpleProcessor(mem), content: true,
		contentC: ContentConfig{Redact: func(weft.ContentKind, string) string { panic("redact bug") }},
		drops:    newDropCounter("test"),
	}
	r := sdkRecordWith(t, "weft.event",
		`{"type":"tool_finish","run_id":"r","seq":1,"call_id":"c","name":"slow","content":"card 4111"}`,
		attribute.String("weft.record", "event"), attribute.String("weft.content", "full"))
	func() {
		defer func() {
			if v := recover(); v != nil {
				t.Fatalf("Redact's panic unwound through OnEmit into the run: %v", v)
			}
		}()
		_ = p.OnEmit(context.Background(), r)
	}()
	recs := mem.snapshot()
	if len(recs) != 1 {
		t.Fatalf("%d records exported, want the one durable event", len(recs))
	}
	if body := recs[0].Body().AsString(); strings.Contains(body, "4111") {
		t.Errorf("unredacted content exported after Redact panicked: %s", body)
	}
	if attrOf(recs[0], "weft.content") != "stripped" {
		t.Errorf("weft.content = %q, want stripped", attrOf(recs[0], "weft.content"))
	}
	if p.drops.count.Load() != 1 {
		t.Errorf("drop counter = %d, want the stripped record counted", p.drops.count.Load())
	}
}

// De-duplication compares the URL a destination actually exports to:
// Datadog()'s default is the same Agent an OTEL_EXPORTER_OTLP_ENDPOINT
// of http://localhost:4318 names, and a trailing slash is not another
// endpoint. Either way every span and record was exported twice.
func TestDedupResolvedURL(t *testing.T) {
	env := func(endpoint string) []dest {
		return envDestinations(func(k string) string {
			if k == "OTEL_EXPORTER_OTLP_ENDPOINT" {
				return endpoint
			}
			return ""
		})
	}
	if kept := dedupe(destsOf(Datadog()), env("http://localhost:4318")); len(kept) != 0 {
		t.Errorf("Datadog() + the same Agent from the environment kept %d env destinations", len(kept))
	}
	if kept := dedupe(destsOf(OTLP("https://collector:4318/")), env("https://collector:4318")); len(kept) != 0 {
		t.Errorf("a trailing slash defeated de-duplication: kept %d", len(kept))
	}
	if kept := dedupe(destsOf(Datadog(DatadogEndpoint("http://dd:4318"))), env("http://localhost:4318")); len(kept) != 1 {
		t.Errorf("an unrelated env destination was dropped: kept %d", len(kept))
	}
}

// Endpoints: a scheme-less URL is https (the env OTLP rule, for every
// OTLP-family destination — WEFT_STUDIO_URL=studio.example:7331 used
// to fail the build), anything but http(s) is refused instead of
// silently exported as https, and errors never echo credentials.
func TestTransportEndpointForms(t *testing.T) {
	d := mustDest(t, Studio("studio.example:7331", "tok"))
	endpoint, _, _, insecure, err := transportOf(d)
	if err != nil {
		t.Fatalf("scheme-less Studio URL: %v", err)
	}
	if endpoint != "studio.example:7331" || insecure {
		t.Errorf("scheme-less Studio URL resolved to %q (insecure=%v)", endpoint, insecure)
	}
	if _, _, _, _, err := transportOf(mustDest(t, OTLP("grpc://collector:4317"))); err == nil {
		t.Error("a grpc:// endpoint built as OTLP/HTTP")
	}
	_, _, _, _, err = transportOf(mustDest(t, OTLP("https://user:hunter2@")))
	if err == nil {
		t.Fatal("an endpoint with no host built")
	}
	if strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "%!") {
		t.Errorf("endpoint error leaks or is malformed: %v", err)
	}
	// The env Studio URL is normalized where it is read, so
	// StudioEndpoint (what weft/runtime dials) carries the scheme too.
	envD := envDestinations(func(k string) string {
		if k == "WEFT_STUDIO_URL" {
			return "studio.example:7331"
		}
		return ""
	})
	if len(envD) != 1 || envD[0].url != "https://studio.example:7331" {
		t.Errorf("env Studio URL = %+v", envD)
	}
}

// failingLogExporter fails every export.
type failingLogExporter struct{ calls atomic.Int64 }

func (e *failingLogExporter) Export(context.Context, []sdklog.Record) error {
	e.calls.Add(1)
	return errors.New("destination down")
}
func (e *failingLogExporter) Shutdown(context.Context) error   { return nil }
func (e *failingLogExporter) ForceFlush(context.Context) error { return nil }

// A failing destination's lost records and spans are counted (S2.4:
// "drops only its own records, and counts them"); before, only the
// Local sink's write errors were, and a dead OTLP endpoint lost
// everything with a zero counter and no WARN.
func TestExportFailuresAreCounted(t *testing.T) {
	logs := &failingLogExporter{}
	spans := &spanCapture{fail: errors.New("destination down")}
	d := mustDest(t, Exporters(spans, logs))
	rt, err := buildDest(testCtx(t), d, config{})
	if err != nil {
		t.Fatal(err)
	}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(rt.logProc))
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rt.spanProc))
	for range 3 {
		var r log.Record
		r.SetEventName("weft.event")
		r.AddAttributes(attribute.String("weft.record", "event"))
		lp.Logger("t").Emit(context.Background(), r)
		_, sp := tp.Tracer("t").Start(context.Background(), "s")
		sp.End()
	}
	_ = lp.Shutdown(context.Background())
	_ = tp.Shutdown(context.Background())
	if logs.calls.Load() == 0 {
		t.Fatal("the exporter was never called (test bug)")
	}
	if got := rt.logProc.(*destProc).drops.count.Load(); got != 6 {
		t.Errorf("drop counter = %d after 3 records and 3 spans failed to export, want 6", got)
	}
}

// What a destination's own policy filters (messages records on a
// content-off chain, deltas under NoDeltas) is counted but is not a
// WARN: "destination dropped records" once a minute, forever, on a
// healthy Local + Datadog pipeline reads as an outage.
func TestPolicyFilteringDoesNotWarn(t *testing.T) {
	buf := &threadSafeBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	defer slog.SetDefault(prev)

	drops := newDropCounter("datadog")
	p := &destProc{name: "datadog", inner: sdklog.NewSimpleProcessor(newMemExporter()),
		content: false, noDeltas: true, drops: drops}
	_ = p.OnEmit(context.Background(), sdkRecordWith(t, "weft.messages", `[]`,
		attribute.String("weft.record", "messages")))
	_ = p.OnEmit(context.Background(), sdkRecordWith(t, "weft.delta", `{"type":"text_delta","run_id":"r","text":"x"}`,
		attribute.String("weft.record", "delta")))
	if strings.Contains(buf.String(), "dropped") {
		t.Errorf("policy filtering logged a drop WARN: %s", buf.String())
	}
	if drops.count.Load() != 2 {
		t.Errorf("drop counter = %d, want 2 (filtered records still count)", drops.count.Load())
	}
}

// BatchDelay applies to an Exporters destination like any other batch
// destination; it was read and then ignored, leaving the SDK's 5 s
// span interval.
func TestExportersHonourBatchDelay(t *testing.T) {
	spans := &spanCapture{}
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(),
		Exporters(spans, nil, BatchDelay(20*time.Millisecond)),
		Heartbeat(0),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(testCtx(t)) }()
	_, sp := p.TracerProvider().Tracer("t").Start(context.Background(), "s")
	sp.End()
	deadline := time.Now().Add(2 * time.Second)
	for spans.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if spans.count() == 0 {
		t.Fatal("no span exported within 2 s under BatchDelay(20ms): the option was ignored")
	}
}

// The Local path keeps the attribute types the OTLP path keeps (S3.3:
// both make identical rows): bytes → base64, slices → []any, maps →
// map[string]any. They were dropped here and stored by Studio ingest.
func TestAttrValueStructuredTypes(t *testing.T) {
	cases := []struct {
		name string
		in   attribute.Value
		want any
	}{
		{"bytes", attribute.ByteSliceValue([]byte("hi")), "aGk="},
		{"slice", attribute.SliceValue(attribute.StringValue("a"), attribute.Int64Value(2)), []any{"a", int64(2)}},
		{"map", attribute.MapValue(attribute.String("k", "v"), attribute.Bool("b", true)),
			map[string]any{"k": "v", "b": true}},
		{"nested", attribute.MapValue(attribute.KeyValue{Key: "l", Value: attribute.SliceValue(attribute.Float64Value(1.5))}),
			map[string]any{"l": []any{1.5}}},
	}
	for _, c := range cases {
		if got := attrValue(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: attrValue = %#v, want %#v", c.name, got, c.want)
		}
	}
}

// weftVersion (the resource's weft.version) is the root module's
// version. Root's constant is unexported, but every run_start record
// carries it, so the pin reads it off the wire: when root's version
// moves and this module's copy does not, this fails (it sat at v0.6.0
// through the v0.7.0 release once).
func TestWeftVersionMatchesRoot(t *testing.T) {
	mem := newMemExporter()
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(), Exporters(nil, mem), Heartbeat(0))
	if err != nil {
		t.Fatal(err)
	}
	agt := weft.New(
		wefttest.Script(wefttest.Say("v")),
		weft.TracerProvider(p.TracerProvider()),
		weft.LoggerProvider(p.LoggerProvider()),
	)
	if _, err := agt.Generate(context.Background(), weft.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	var root string
	for _, r := range mem.snapshot() {
		if attrOf(r, "weft.event.type") == "run_start" {
			root = attrOf(r, "weft.version")
		}
	}
	if root == "" {
		t.Fatal("no run_start record with weft.version (test bug)")
	}
	if weftVersion() != root {
		t.Errorf("weftVersion() = %q but the root module reports %q: bump otel/pipeline.go", weftVersion(), root)
	}
}

// Many concurrent runs against a pipeline that shuts down under them:
// no race, no panic, no deadlock, and the tracker ends empty for the
// runs that finished. (-race is the assertion.)
func TestConcurrentRunsAcrossShutdown(t *testing.T) {
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(),
		Local(filepath.Join(t.TempDir(), "race.db")),
		Exporters(&spanCapture{}, newMemExporter(), WithContent(ContentConfig{
			MaxBytes: 8,
			Redact:   func(_ weft.ContentKind, s string) string { return strings.ToUpper(s) },
		})),
		Exporters(nil, newMemExporter(), NoDeltas()),
		Heartbeat(time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	echo := weft.Tool("echo", "Echoes.", func(_ context.Context, in struct {
		Note string `json:"note"`
	}) (string, error) {
		return "echo:" + in.Note, nil
	})
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 5 {
				agt := weft.New(
					wefttest.Script(
						wefttest.Think("plan", wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"note":"n"}`})),
						wefttest.Say("done"),
					),
					weft.Name("race"),
					weft.TracerProvider(p.TracerProvider()),
					weft.LoggerProvider(p.LoggerProvider()),
					echo,
				)
				_, _ = agt.Generate(context.Background(), weft.Prompt("go"),
					weft.Metadata(map[string]string{"weft.session.id": "race"}))
			}
		}()
	}
	time.Sleep(15 * time.Millisecond)
	done := make(chan error, 2)
	for range 2 { // concurrent Shutdowns, mid-run
		go func() { done <- p.Shutdown(context.Background()) }()
	}
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Shutdown: %v", err)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("Shutdown deadlocked under concurrent runs")
		}
	}
	wg.Wait()
	_ = p.ForceFlush(context.Background()) // use after shutdown: an error at most, never a panic
}

// Redacted tool arguments stay redacted when the redactor's output is
// not a JSON document ("[REDACTED]" for the whole value is the natural
// redactor): the re-encode failed on the invalid RawMessage and the
// chain exported the ORIGINAL body, arguments in the clear.
func TestRedactedArgsNeverFallBackToOriginal(t *testing.T) {
	red := func(kind weft.ContentKind, s string) string {
		if kind == weft.ContentArgs {
			return "[REDACTED]"
		}
		return s
	}
	p := &destProc{content: true, contentC: ContentConfig{Redact: red}, drops: newDropCounter("test")}
	for _, body := range []string{
		`{"type":"tool_start","run_id":"r","seq":1,"call_id":"c","name":"pay","args":{"card":"4111-1111"}}`,
		`{"type":"run_finish","run_id":"r","steps":1,"usage":{},"pending":[{"type":"tool_call","id":"c","name":"pay","args":{"card":"4111-1111"}}]}`,
	} {
		r := sdkRecordWith(t, "weft.event", body, attribute.String("weft.record", "event"))
		p.shapeEvent(r)
		out := r.Body().AsString()
		if strings.Contains(out, "4111") {
			t.Errorf("unredacted arguments exported: %s", out)
		}
		if !strings.Contains(out, "[REDACTED]") {
			t.Errorf("the redactor's output is not in the body: %s", out)
		}
		if _, err := weft.UnmarshalEvent([]byte(out)); err != nil {
			t.Errorf("shaped body no longer decodes: %v (%s)", err, out)
		}
	}
}

// errorHandlerFunc counts what reaches OTel's global error handler.
type errorHandlerFunc func(error)

func (f errorHandlerFunc) Handle(err error) { f(err) }

// A failing local database does not flood the process's error output:
// every lost record is counted and the throttled WARN names the cause;
// the write error is not also handed to OTel's global error handler —
// one stderr line per record, per delta, for as long as the disk is
// full.
func TestLocalWriteFailureIsCountedNotFlooded(t *testing.T) {
	buf := &threadSafeBuffer{}
	prevLog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	defer slog.SetDefault(prevLog)
	var handled atomic.Int64
	prev := otelapi.GetErrorHandler()
	otelapi.SetErrorHandler(errorHandlerFunc(func(error) { handled.Add(1) }))
	defer otelapi.SetErrorHandler(prev)

	p, err := Start(testCtx(t), NoGlobal(), NoEnv(),
		Local(filepath.Join(t.TempDir(), "gone.db")), Heartbeat(0))
	if err != nil {
		t.Fatal(err)
	}
	_ = p.LocalDB().Close() // the sink fails from here on
	agt := weft.New(
		wefttest.Script(wefttest.Say("x")),
		weft.TracerProvider(p.TracerProvider()),
		weft.LoggerProvider(p.LoggerProvider()),
	)
	if _, err := agt.Generate(context.Background(), weft.Prompt("go")); err != nil {
		t.Fatalf("a failing sink failed the run: %v", err)
	}
	_ = p.Shutdown(testCtx(t))
	if n := handled.Load(); n != 0 {
		t.Errorf("%d write errors reached OTel's global error handler (one line each on stderr)", n)
	}
	out := buf.String()
	if strings.Count(out, "destination dropped records") != 1 || !strings.Contains(out, "dest=local") {
		t.Errorf("want exactly one throttled WARN for the local sink, got: %s", out)
	}
	if !strings.Contains(out, "err=") {
		t.Errorf("the WARN does not name the cause: %s", out)
	}
}

// The clone rule with every shaping at once: a redacting, capping
// content-on chain and a content-off chain beside the Local sink — no
// chain sees another's edits.
func TestCloneRuleAcrossShapingChains(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clone.db")
	shaped, stripped := newMemExporter(), newMemExporter()
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(), Heartbeat(0),
		Exporters(nil, shaped, WithContent(ContentConfig{
			MaxBytes: 6,
			Redact:   func(_ weft.ContentKind, s string) string { return strings.ToUpper(s) },
		})),
		Exporters(nil, stripped, NoContent()),
		Local(path),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, runID := agentThrough(t, p)
	if err := p.Shutdown(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	toolFinish := func(recs []sdklog.Record) (string, sdklog.Record) {
		for _, r := range recs {
			if attrOf(r, "weft.event.type") == "tool_finish" {
				ev, err := weft.UnmarshalEvent([]byte(r.Body().AsString()))
				if err != nil {
					t.Fatalf("tool_finish body: %v", err)
				}
				return ev.(weft.ToolFinish).Content, r
			}
		}
		t.Fatal("no tool_finish record")
		return "", sdklog.Record{}
	}
	if got, r := toolFinish(shaped.snapshot()); got != "DONE:O" {
		t.Errorf("shaped chain content = %q, want the redacted, capped %q", got, "DONE:O")
	} else {
		var cut int64
		r.WalkAttributes(func(kv attribute.KeyValue) bool {
			if kv.Key == "weft.content.truncated_bytes" {
				cut = kv.Value.AsInt64()
			}
			return true
		})
		if cut != int64(len("done:one")-6) {
			t.Errorf("weft.content.truncated_bytes = %d, want %d", cut, len("done:one")-6)
		}
	}
	if got, r := toolFinish(stripped.snapshot()); got != "" || attrOf(r, "weft.content") != "stripped" {
		t.Errorf("stripped chain content = %q (weft.content=%q)", got, attrOf(r, "weft.content"))
	}
	db, err := sqliteOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	page, err := db.Events(context.Background(), runID, -1, 100)
	if err != nil {
		t.Fatal(err)
	}
	var local string
	for _, e := range page.Events {
		if ev, err := weft.UnmarshalEvent(e.Event); err == nil {
			if tf, ok := ev.(weft.ToolFinish); ok {
				local = tf.Content
			}
		}
	}
	if local != "done:one" {
		t.Errorf("Local stored %q: another chain's edit leaked into it (want the original)", local)
	}
}

// The cap cuts on a rune boundary and reports the bytes it removed.
func TestShapeStringRuneBoundary(t *testing.T) {
	for _, c := range []struct {
		in       string
		max      int
		want     string
		wantCut  int
		wantDesc string
	}{
		{"héllo", 2, "h", 5, "mid two-byte rune"},
		{"日本語", 4, "日", 6, "mid three-byte rune"},
		{"👍x", 3, "", 5, "mid four-byte rune"},
		{"abc", 3, "abc", 0, "exactly the cap"},
		{"abc", -1, "abc", 0, "unlimited"},
	} {
		got, cut := shapeString(c.in, weft.ContentText, nil, c.max)
		if got != c.want || cut != c.wantCut {
			t.Errorf("%s: shapeString(%q, %d) = %q, %d; want %q, %d", c.wantDesc, c.in, c.max, got, cut, c.want, c.wantCut)
		}
	}
}

// OTEL_EXPORTER_OTLP_HEADERS belongs to the env OTLP destination: the
// exporters read the variable themselves, and it must not ride on
// Studio's (or any other destination's) requests.
func TestEnvHeadersStayOffOtherDestinations(t *testing.T) {
	var mu sync.Mutex
	var leaked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if v := r.Header.Get("X-Collector-Key"); v != "" {
			leaked = append(leaked, r.URL.Path+"="+v)
		}
		mu.Unlock()
	}))
	defer srv.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "x-collector-key=secret")
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(), Studio(srv.URL, "tok"), Heartbeat(0))
	if err != nil {
		t.Fatal(err)
	}
	agt := weft.New(
		wefttest.Script(wefttest.Say("x")),
		weft.TracerProvider(p.TracerProvider()),
		weft.LoggerProvider(p.LoggerProvider()),
	)
	if _, err := agt.Generate(context.Background(), weft.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(leaked) != 0 {
		t.Errorf("the environment's OTLP headers rode on Studio's requests: %v", leaked)
	}
}

// A non-string body (a stock bridge's structured record) renders as
// JSON through the attribute mapping — what obsdb.FromOTLPLogs stores
// for the same body — not as "".
func TestBodyStringStructuredBodies(t *testing.T) {
	for _, c := range []struct {
		name string
		in   attribute.Value
		want string
	}{
		{"string", attribute.StringValue(`{"type":"run_start"}`), `{"type":"run_start"}`},
		{"empty", attribute.Value{}, ""},
		{"int", attribute.Int64Value(7), "7"},
		{"bool", attribute.BoolValue(true), "true"},
		{"double", attribute.Float64Value(1.5), "1.5"},
		{"bytes", attribute.ByteSliceValue([]byte("hi")), "aGk="},
		{"map", attribute.MapValue(attribute.String("msg", "hello"), attribute.Int64("n", 2)), `{"msg":"hello","n":2}`},
		{"slice", attribute.SliceValue(attribute.StringValue("a"), attribute.BoolValue(false)), `["a",false]`},
		{"strings", attribute.StringSliceValue([]string{"a", "b"}), `["a","b"]`},
		{"nan", attribute.Float64Value(math.NaN()), "NaN"},
	} {
		if got := bodyString(c.in); got != c.want {
			t.Errorf("%s: bodyString = %q, want %q", c.name, got, c.want)
		}
	}
}

// hangLogExporter and hangSpanExporter block until the test releases
// them or the export's context ends — a dead endpoint mid-retry.
type hangLogExporter struct{ release <-chan struct{} }

func (e hangLogExporter) Export(ctx context.Context, _ []sdklog.Record) error {
	select {
	case <-e.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (hangLogExporter) Shutdown(context.Context) error   { return nil }
func (hangLogExporter) ForceFlush(context.Context) error { return nil }

type hangSpanExporter struct{ release <-chan struct{} }

func (e hangSpanExporter) ExportSpans(ctx context.Context, _ []sdktrace.ReadOnlySpan) error {
	select {
	case <-e.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (hangSpanExporter) Shutdown(context.Context) error { return nil }

// ctxLogExporter and ctxSpanExporter honour the export's context the
// way the OTLP exporters do — nothing leaves once it has ended — and
// close got on their first delivery.
type ctxLogExporter struct {
	once sync.Once
	got  chan struct{}
}

func (e *ctxLogExporter) Export(ctx context.Context, recs []sdklog.Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(recs) > 0 {
		e.once.Do(func() { close(e.got) })
	}
	return nil
}
func (e *ctxLogExporter) Shutdown(context.Context) error   { return nil }
func (e *ctxLogExporter) ForceFlush(context.Context) error { return nil }

type ctxSpanExporter struct {
	once sync.Once
	got  chan struct{}
}

func (e *ctxSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(spans) > 0 {
		e.once.Do(func() { close(e.got) })
	}
	return nil
}
func (e *ctxSpanExporter) Shutdown(context.Context) error { return nil }

// Shutdown flushes every destination in parallel under the one shared
// budget (S2.4): a hung destination costs only its own data. Flushed
// one after another through the providers, the hung one spent the
// whole budget and the healthy destination behind it lost its tail.
//
// No wall-clock race: the hung destination stays hung until the
// healthy one has delivered (the test releases it then), so a pass
// waits on nothing but that delivery; sequential flushing never gets
// to the healthy destination while the hung one holds, and Shutdown
// returns — the budget spent — without the delivery.
func TestShutdownFlushesDestinationsInParallel(t *testing.T) {
	release := make(chan struct{})
	logs := &ctxLogExporter{got: make(chan struct{})}
	spans := &ctxSpanExporter{got: make(chan struct{})}
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(), Heartbeat(0),
		Exporters(hangSpanExporter{release}, hangLogExporter{release}, BatchDelay(time.Hour)),
		Exporters(spans, logs, BatchDelay(time.Hour)),
	)
	if err != nil {
		t.Fatal(err)
	}
	var r log.Record
	r.SetEventName("weft.event")
	r.AddAttributes(attribute.String("weft.record", "event"))
	p.LoggerProvider().Logger("t").Emit(context.Background(), r)
	_, sp := p.TracerProvider().Tracer("t").Start(context.Background(), "s")
	sp.End()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = p.Shutdown(ctx)
		close(done)
	}()
	for name, got := range map[string]chan struct{}{"record": logs.got, "span": spans.got} {
		select {
		case <-got:
		case <-done:
			select {
			case <-got: // delivered as Shutdown returned
			default:
				close(release)
				t.Fatalf("Shutdown returned and the healthy destination never received its %s: "+
					"a hung destination took its tail", name)
			}
		}
	}
	close(release) // the hung destination lets go; Shutdown finishes without waiting out the budget
	<-done
}
