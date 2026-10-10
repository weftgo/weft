package otel

// otel.RawBodies (ADR 0028 §12, plan A6): dev only, one WARN and off
// elsewhere; when on, weft.raw is asked for and delivered by the Local
// and Studio chains alone.

import (
	"bytes"
	"context"
	"iter"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/obsdb"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

func enabledSDK(name string) sdklog.EnabledParameters {
	return sdklog.EnabledParameters{EventName: name}
}

// rawReporter is a model middleware that asks WantsRaw and reports one
// exchange per call, the way a first-party adapter's capture does.
type rawReporter struct {
	next   core.Model
	wanted *bool
}

func (m rawReporter) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	r := core.ReportFromContext(ctx)
	*m.wanted = r.WantsRaw()
	r.Raw(core.RawPair{
		Method: "POST", URL: "https://api.example/v1/messages",
		Request: []byte(`{"q":"wire"}`), MediaType: "application/json",
		RequestHeaders: map[string][]string{"Authorization": {"Bearer SECRET"}},
		Status:         200, Response: []byte("data: ok\n\n"), ResponseMediaType: "text/event-stream",
	})
	return m.next.Stream(ctx, req)
}

// rawThrough runs one step through p reporting one exchange; it returns
// what WantsRaw said and the run id.
func rawThrough(t *testing.T, p *Pipeline) (bool, string) {
	t.Helper()
	var wanted bool
	agt := core.New(wefttest.Script(wefttest.Say("done")),
		core.WrapModel(func(next core.Model) core.Model { return rawReporter{next: next, wanted: &wanted} }),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	res, err := agt.Generate(context.Background(), core.Prompt("hi"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	return wanted, res.ID
}

// devEnv swaps the pipeline's environment for one holding only env
// (and the discovery file off).
func devEnv(t *testing.T, env map[string]string) {
	prev := envGetenv
	envGetenv = func(k string) string {
		if k == "WEFT_DISCOVERY" {
			return "off"
		}
		return env[k]
	}
	t.Cleanup(func() { envGetenv = prev })
}

func captureSlog(t *testing.T) *bytes.Buffer {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

const rawWarn = "otel: RawBodies ignored: WEFT_ENV is not dev"

// Outside WEFT_ENV=dev — or under NoEnv, which reads no environment —
// RawBodies(true) is one WARN line and nothing else: Enabled says no
// for weft.raw, the reporter tells the adapter so, and the run stores no
// raw record. Under dev it is on, silently.
func TestRawBodiesDevOnly(t *testing.T) {
	for _, c := range []struct {
		name  string
		env   map[string]string
		noEnv bool
		on    bool
	}{
		{"WEFT_ENV unset", nil, false, false},
		{"WEFT_ENV=prod", map[string]string{"WEFT_ENV": "prod"}, false, false},
		{"NoEnv under dev", map[string]string{"WEFT_ENV": "dev"}, true, false},
		{"WEFT_ENV=dev", map[string]string{"WEFT_ENV": "dev"}, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			devEnv(t, c.env)
			logs := captureSlog(t)
			opts := []Option{NoGlobal(), Heartbeat(0), Local(filepath.Join(t.TempDir(), "weft.db")), RawBodies(true)}
			if c.noEnv {
				opts = append(opts, NoEnv())
			}
			p, err := Start(testCtx(t), opts...)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = p.Shutdown(context.Background()) }()
			warns := strings.Count(logs.String(), "level=WARN")
			if n := strings.Count(logs.String(), rawWarn); (c.on && (n != 0 || warns != 0)) || (!c.on && (n != 1 || warns != 1)) {
				t.Errorf("WARN lines: %d of %d mention RawBodies; on=%v\n%s", n, warns, c.on, logs.String())
			}
			enabled := p.LoggerProvider().Logger(instrumentationName).Enabled(context.Background(), enabledParams("weft.raw"))
			wanted, id := rawThrough(t, p)
			reqs, err := p.LocalDB().Requests(context.Background(), id, obsdb.RequestQuery{})
			if err != nil || len(reqs) != 1 {
				t.Fatalf("requests = %d, %v", len(reqs), err)
			}
			if enabled != c.on || wanted != c.on || (reqs[0].Wire != nil) != c.on {
				t.Errorf("Enabled %v, WantsRaw %v, stored %v; want all %v", enabled, wanted, reqs[0].Wire != nil, c.on)
			}
			if c.on && strings.Contains(reqs[0].Wire.Request.Body+strings.Join(reqs[0].Wire.RedactedHeaders, ","), "SECRET") {
				t.Error("a credential was stored")
			}
		})
	}
	t.Run("absent", func(t *testing.T) {
		devEnv(t, nil)
		logs := captureSlog(t)
		p, err := Start(testCtx(t), NoGlobal(), Heartbeat(0), Local(filepath.Join(t.TempDir(), "weft.db")))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = p.Shutdown(context.Background()) }()
		if strings.Contains(logs.String(), "RawBodies") {
			t.Errorf("a pipeline without RawBodies warned: %s", logs.String())
		}
		if p.LoggerProvider().Logger(instrumentationName).Enabled(context.Background(), enabledParams("weft.raw")) {
			t.Error("weft.raw enabled without RawBodies")
		}
	})
}

// Each destination kind's chain under RawBodies in force: Local and
// Studio take raw records (content on), every third party refuses them
// whatever its content setting, and a content-off Local or Studio too.
func TestRawBodiesPerDestination(t *testing.T) {
	ctx := testCtx(t)
	cfg := config{rawBodies: true}
	local := func(opts ...DestOption) dest {
		d := newDest(destLocal, "local")
		d.path = filepath.Join(t.TempDir(), "weft.db")
		d.apply(opts)
		return d
	}
	remote := func(kind destKind, name string, opts ...DestOption) dest {
		d := newDest(kind, name)
		d.url, d.host, d.token = "http://127.0.0.1:1", "http://127.0.0.1:1", "tok"
		d.apply(opts)
		return d
	}
	exporters := func(opts ...DestOption) dest {
		d := newDest(destExporters, "exporters")
		d.logExp, d.traces = newMemExporter(), false
		d.apply(opts)
		return d
	}
	for _, c := range []struct {
		name string
		d    dest
		want bool
	}{
		{"local", local(), true},
		{"local NoContent", local(NoContent()), false},
		{"studio", remote(destStudio, "studio"), true},
		{"studio NoContent", remote(destStudio, "studio", NoContent()), false},
		{"datadog", remote(destDatadog, "datadog"), false},
		{"datadog WithContent", remote(destDatadog, "datadog", WithContent()), false},
		{"otlp", remote(destOTLP, "otlp"), false},
		{"otlp WithContent", remote(destOTLP, "otlp", WithContent()), false},
		{"exporters", exporters(), false},
		{"exporters WithContent", exporters(WithContent()), false},
		{"langfuse", remote(destLangfuse, "langfuse", Signals(true, true), WithContent()), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			rt, err := buildDest(ctx, c.d, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer rt.release(ctx)
			dp, ok := rt.logProc.(*destProc)
			if !ok {
				t.Fatalf("log chain = %T", rt.logProc)
			}
			if dp.raw != c.want || dp.Enabled(ctx, enabledSDK("weft.raw")) != c.want {
				t.Errorf("raw %v, Enabled(weft.raw) %v; want %v", dp.raw, dp.Enabled(ctx, enabledSDK("weft.raw")), c.want)
			}
		})
	}
	// The tracker never asks on any pipeline's behalf.
	if newRunTracker().Enabled(ctx, enabledSDK("weft.raw")) {
		t.Error("the run tracker asks for weft.raw")
	}
}

// End to end under dev: the Local sink stores the raw record, a
// content-on Exporters destination beside it receives none (counted as
// filtered), and the record shows on the Local side only.
func TestRawBodiesNeverToThirdParty(t *testing.T) {
	devEnv(t, map[string]string{"WEFT_ENV": "dev"})
	mem := newMemExporter()
	p, err := Start(testCtx(t), NoGlobal(), Heartbeat(0), RawBodies(true),
		Local(filepath.Join(t.TempDir(), "weft.db")), Exporters(nil, mem, WithContent()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(context.Background()) }()
	wanted, id := rawThrough(t, p)
	if !wanted {
		t.Fatal("WantsRaw = false with a Local sink under RawBodies and dev")
	}
	if n := len(recordsOfKind(mem.snapshot(), "raw")); n != 0 {
		t.Errorf("the Exporters destination received %d raw records", n)
	}
	if len(recordsOfKind(mem.snapshot(), "request")) != 1 {
		t.Error("the Exporters destination lost its request record")
	}
	reqs, err := p.LocalDB().Requests(context.Background(), id, obsdb.RequestQuery{})
	if err != nil || len(reqs) != 1 || reqs[0].Wire == nil {
		t.Fatalf("local requests = %+v, %v; want one with its raw record", reqs, err)
	}
	w := reqs[0].Wire
	if w.Request.Body != `{"q":"wire"}` || w.Response.Body != "data: ok\n\n" || w.RedactedHeaders[0] != "authorization" {
		t.Errorf("stored wire = %+v / %+v", w.Request, w.Response)
	}
}

// A content-on destination's Redact sees each raw body as
// core.ContentRaw; a base64 body is left alone.
func TestRawBodiesRedact(t *testing.T) {
	devEnv(t, map[string]string{"WEFT_ENV": "dev"})
	var kinds []core.ContentKind
	p, err := Start(testCtx(t), NoGlobal(), Heartbeat(0), RawBodies(true),
		Local(filepath.Join(t.TempDir(), "weft.db"), WithContent(ContentConfig{Redact: func(k core.ContentKind, s string) string {
			if k == core.ContentRaw {
				kinds = append(kinds, k)
				return strings.ReplaceAll(s, "wire", "[X]")
			}
			return s
		}})))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(context.Background()) }()
	_, id := rawThrough(t, p)
	reqs, err := p.LocalDB().Requests(context.Background(), id, obsdb.RequestQuery{})
	if err != nil || len(reqs) != 1 || reqs[0].Wire == nil {
		t.Fatalf("requests = %+v, %v", reqs, err)
	}
	if reqs[0].Wire.Request.Body != `{"q":"[X]"}` || len(kinds) != 2 {
		t.Errorf("redacted body = %q, Redact calls = %d", reqs[0].Wire.Request.Body, len(kinds))
	}
}

// A stock SDK LoggerProvider — whose Simple and Batch processors answer
// Enabled true for every event name — never receives a raw record: it
// carries no RawBodies marker, so the core never builds one and the
// adapter is told not to buffer (review finding 1 of A6.1).
func TestRawBodiesStockSDKProviderNever(t *testing.T) {
	devEnv(t, map[string]string{"WEFT_ENV": "dev"})
	mem := newMemExporter()
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(mem)))
	defer func() { _ = lp.Shutdown(context.Background()) }()
	if !lp.Logger("x").Enabled(context.Background(), enabledParams("weft.raw")) {
		t.Fatal("precondition: the stock provider answers Enabled true for weft.raw")
	}
	var wanted bool
	agt := core.New(wefttest.Script(wefttest.Say("done")), core.LoggerProvider(lp),
		core.WrapModel(func(next core.Model) core.Model { return rawReporter{next: next, wanted: &wanted} }))
	if _, err := agt.Generate(context.Background(), core.Prompt("hi")); err != nil {
		t.Fatal(err)
	}
	if wanted {
		t.Error("WantsRaw = true under a stock SDK provider")
	}
	if n := len(recordsOfKind(mem.snapshot(), "raw")); n != 0 {
		t.Errorf("the stock provider exported %d raw records", n)
	}
	if len(recordsOfKind(mem.snapshot(), "request")) != 1 {
		t.Error("the stock provider lost the request record (the gate must touch raw only)")
	}
}

// The global path: otel.Install's zero-config local sink registered
// globally and an agent with no LoggerProvider option. Under dev with
// RawBodies the marker is reached through the registered global
// provider (the core re-reads the global at each ask) and the raw
// record lands in the sink. The agent is built after Install: an
// earlier test in this process may have left its own (shut-down)
// provider registered, which an agent built then would emit to.
func TestRawBodiesThroughTheGlobalProvider(t *testing.T) {
	t.Setenv("WEFT_ENV", "dev")
	t.Setenv("WEFT_DISCOVERY", "off")
	t.Setenv("WEFT_STUDIO_URL", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("WEFT_DB", filepath.Join(t.TempDir(), "weft.db"))
	shutdown := Install(RawBodies(true), Heartbeat(0))
	defer shutdown()
	var wanted bool
	agt := core.New(wefttest.Script(wefttest.Say("done")),
		core.WrapModel(func(next core.Model) core.Model { return rawReporter{next: next, wanted: &wanted} }))
	res, err := agt.Generate(context.Background(), core.Prompt("hi"))
	if err != nil {
		t.Fatal(err)
	}
	if !wanted {
		t.Fatal("WantsRaw = false through the globally installed pipeline")
	}
	db := LocalDB()
	if db == nil {
		t.Fatal("no local sink installed")
	}
	reqs, err := db.Requests(context.Background(), res.ID, obsdb.RequestQuery{})
	if err != nil || len(reqs) != 1 || reqs[0].Wire == nil || reqs[0].Wire.Request.Body != `{"q":"wire"}` {
		t.Fatalf("requests = %+v, %v; want one with its raw record", reqs, err)
	}
}
