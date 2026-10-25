package otel

// otel.RawBodies (ADR 0028 §12, plan A6): dev only, one WARN and off
// elsewhere; when on, weft.raw is asked for and delivered by the Local
// and Studio chains alone.

import (
	"bytes"
	"context"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/obsdb"
	"go.opentelemetry.io/otel/attribute"
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

const (
	rawWarn      = "otel: RawBodies ignored: WEFT_ENV is not dev"
	rawWarnNoEnv = "otel: RawBodies ignored: otel.NoEnv reads no WEFT_ENV"
	devModeProd  = "otel: DevMode ignored: WEFT_ENV=prod"
	rawInfo      = "otel: raw bodies on by otel.DevMode()"
)

// Outside the dev gate RawBodies(true) is one WARN line naming the cause
// and nothing else: Enabled says no for weft.raw, the reporter tells the
// adapter so, and the run stores no raw record. The gate holds under
// WEFT_ENV=dev read from the environment, or under DevMode — which an
// isolated NoEnv pipeline needs, NoEnv reading no WEFT_ENV (issue 002)
// — unless an explicit WEFT_ENV other than dev overrules DevMode. Under
// the gate the option is on with no WARN (one INFO when DevMode opened
// it): one weft.raw per attempt.
func TestRawBodiesDevOnly(t *testing.T) {
	for _, c := range []struct {
		name    string
		env     map[string]string
		noEnv   bool
		devMode bool
		warn    string // "" = on
	}{
		{"WEFT_ENV unset", nil, false, false, rawWarn},
		{"WEFT_ENV=prod", map[string]string{"WEFT_ENV": "prod"}, false, false, rawWarn},
		{"NoEnv under dev", map[string]string{"WEFT_ENV": "dev"}, true, false, rawWarnNoEnv},
		{"NoEnv unset", nil, true, false, rawWarnNoEnv},
		{"NoEnv with DevMode", nil, true, true, ""},
		{"DevMode, WEFT_ENV unset", nil, false, true, ""},
		{"DevMode, WEFT_ENV=prod", map[string]string{"WEFT_ENV": "prod"}, false, true, devModeProd},
		{"DevMode, WEFT_ENV=dev", map[string]string{"WEFT_ENV": "dev"}, false, true, ""},
		{"WEFT_ENV=dev", map[string]string{"WEFT_ENV": "dev"}, false, false, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			devEnv(t, c.env)
			logs := captureSlog(t)
			on := c.warn == ""
			opts := []Option{NoGlobal(), Heartbeat(0), Local(filepath.Join(t.TempDir(), "weft.db")), RawBodies(true)}
			if c.noEnv {
				opts = append(opts, NoEnv())
			}
			if c.devMode {
				opts = append(opts, DevMode())
			}
			p, err := Start(testCtx(t), opts...)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = p.Shutdown(context.Background()) }()
			warns := strings.Count(logs.String(), "level=WARN")
			if n := strings.Count(logs.String(), c.warn); (on && warns != 0) || (!on && (n != 1 || warns != 1)) {
				t.Errorf("WARN lines: %d of %d name %q; on=%v\n%s", n, warns, c.warn, on, logs.String())
			}
			// The INFO line exactly when DevMode is what opened the gate.
			byDevMode := on && c.devMode && (c.noEnv || c.env["WEFT_ENV"] != "dev")
			if n := strings.Count(logs.String(), rawInfo); n != map[bool]int{true: 1}[byDevMode] {
				t.Errorf("%q lines = %d, DevMode opened the gate: %v\n%s", rawInfo, n, byDevMode, logs.String())
			}
			if !on && !strings.Contains(logs.String(), c.warn) {
				t.Errorf("the refusal does not name its cause %q:\n%s", c.warn, logs.String())
			}
			if p.LoggerProvider().RawBodies() != on {
				t.Errorf("LoggerProvider.RawBodies = %v, want %v", !on, on)
			}
			enabled := p.LoggerProvider().Logger(instrumentationName).Enabled(context.Background(), enabledParams("weft.raw"))
			wanted, id := rawThrough(t, p)
			reqs, err := p.LocalDB().Requests(context.Background(), id, obsdb.RequestQuery{})
			if err != nil || len(reqs) != 1 {
				t.Fatalf("requests = %d, %v", len(reqs), err)
			}
			if enabled != on || wanted != on || (reqs[0].Wire != nil) != on {
				t.Fatalf("Enabled %v, WantsRaw %v, stored %v; want all %v", enabled, wanted, reqs[0].Wire != nil, on)
			}
			if on && strings.Contains(reqs[0].Wire.Request.Body+strings.Join(reqs[0].Wire.RedactedHeaders, ","), "SECRET") {
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

// An isolated NoEnv pipeline opts into raw bodies with DevMode (issue
// 002): the operator's WEFT_ENV, WEFT_STUDIO_URL and discovery file stay
// unread, and each model call attempt of a two-step run stores its one
// weft.raw — with no WARN.
func TestRawBodiesNoEnvDevModePerAttempt(t *testing.T) {
	devEnv(t, map[string]string{"WEFT_STUDIO_URL": "http://127.0.0.1:1"})
	logs := captureSlog(t)
	p, err := Start(testCtx(t), NoGlobal(), Heartbeat(0), NoEnv(), DevMode(),
		Local(filepath.Join(t.TempDir(), "weft.db")), RawBodies(true))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(context.Background()) }()
	if len(p.dests) != 1 {
		t.Fatalf("destinations = %d, want the Local alone (NoEnv)", len(p.dests))
	}
	var wanted bool
	echo := core.Tool("echo", "Echo.", func(_ context.Context, in struct{}) (string, error) { return "ok", nil })
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{}`}),
		wefttest.Say("done"),
	), echo,
		core.WrapModel(func(next core.Model) core.Model { return rawReporter{next: next, wanted: &wanted} }),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	res, err := agt.Generate(context.Background(), core.Prompt("hi"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("NoEnv+DevMode warned:\n%s", logs.String())
	}
	if n := strings.Count(logs.String(), `level=INFO msg="`+rawInfo+`"`); n != 1 {
		t.Errorf("want one INFO %q, got %d:\n%s", rawInfo, n, logs.String())
	}
	reqs, err := p.LocalDB().Requests(context.Background(), res.ID, obsdb.RequestQuery{})
	if err != nil || len(reqs) != 2 {
		t.Fatalf("requests = %d, %v; want 2 attempts", len(reqs), err)
	}
	for i, r := range reqs {
		if r.Wire == nil {
			t.Errorf("attempt %d stored no weft.raw", i)
		}
	}
	if !wanted {
		t.Error("WantsRaw false under NoEnv+DevMode")
	}
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

// The byte split is wired into every batch chain that takes raw
// records, and only those — pinned on the chain itself, not on how a
// batch forms (under -race emission is slow and batches stay small, so
// TestRawBodiesBatchBoundedByBytes alone would not notice the split
// dropped). Local has no batch chain: its simple processor writes the
// database, with no ingest limit to respect.
func TestRawBodiesSplitWired(t *testing.T) {
	ctx := testCtx(t)
	remote := func(kind destKind, name string, opts ...DestOption) dest {
		d := newDest(kind, name)
		d.url, d.host, d.token = "http://127.0.0.1:1", "http://127.0.0.1:1", "tok"
		d.apply(opts)
		return d
	}
	local := newDest(destLocal, "local")
	local.path = filepath.Join(t.TempDir(), "weft.db")
	for _, c := range []struct {
		name  string
		d     dest
		raw   bool
		split bool
	}{
		{"studio", remote(destStudio, "studio"), true, true},
		{"studio without RawBodies", remote(destStudio, "studio"), false, false},
		{"studio NoContent", remote(destStudio, "studio", NoContent()), true, false},
		{"otlp WithContent", remote(destOTLP, "otlp", WithContent()), true, false},
		{"datadog WithContent", remote(destDatadog, "datadog", WithContent()), true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			rt, err := buildDest(ctx, c.d, config{rawBodies: c.raw})
			if err != nil {
				t.Fatal(err)
			}
			defer rt.release(ctx)
			split, isSplit := rt.logExp.(byteSplitLogExporter)
			if isSplit != c.split {
				t.Fatalf("log exporter = %T, byte split %v; want %v", rt.logExp, isSplit, c.split)
			}
			if isSplit && split.limit != rawExportBytes {
				t.Errorf("byte split limit = %d, want %d", split.limit, rawExportBytes)
			}
		})
	}
	t.Run("local", func(t *testing.T) {
		rt, err := buildDest(ctx, local, config{rawBodies: true})
		if err != nil {
			t.Fatal(err)
		}
		defer rt.release(ctx)
		if rt.logExp != nil {
			t.Errorf("Local has a batch exporter %T; want its simple processor alone", rt.logExp)
		}
	})
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

// worstRawModel reports n exchanges whose bodies are the raw record's
// worst case for its encoded size: C0 controls (six bytes each under
// JSON's own escaping, \u0001) on both sides, past the 256 KiB cap.
type worstRawModel struct {
	next core.Model
	n    int
}

func (m worstRawModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	r := core.ReportFromContext(ctx)
	ctl := []byte(strings.Repeat("\x01", 300<<10))
	for range m.n {
		r.Raw(core.RawPair{Method: "POST", URL: "https://api.example/v1/messages", Request: ctl,
			Status: 200, Response: ctl})
		r.Attempt(core.AttemptInfo{Model: "m"})
	}
	return m.next.Stream(ctx, req)
}

// Under RawBodies a Studio destination's log export never posts a body
// over Studio's 16 MiB ingest limit, whatever the raw records hold: a
// burst of worst-case records (each ≈ 3 MiB encoded) is split by bytes
// across posts, and every record still arrives.
func TestRawBodiesBatchBoundedByBytes(t *testing.T) {
	devEnv(t, map[string]string{"WEFT_ENV": "dev"})
	const ingestMax = 16 << 20 // studio/ingest.MaxBody
	var (
		mu      sync.Mutex
		largest int
		total   int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/v1/logs" {
			mu.Lock()
			largest = max(largest, len(b))
			total += len(b)
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p, err := Start(testCtx(t), NoGlobal(), Heartbeat(0), Studio(srv.URL, "tok"), RawBodies(true))
	if err != nil {
		t.Fatal(err)
	}
	const n = 24
	agt := core.New(wefttest.Script(wefttest.Say("done")),
		core.WrapModel(func(next core.Model) core.Model { return worstRawModel{next: next, n: n} }),
		core.LoggerProvider(p.LoggerProvider()), core.TracerProvider(p.TracerProvider()))
	if _, err := agt.Generate(context.Background(), core.Prompt("hi")); err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if largest > ingestMax {
		t.Errorf("largest log export = %d bytes, over the %d-byte ingest limit", largest, ingestMax)
	}
	if want := n * 2 * (256 << 10); total < want {
		t.Errorf("log exports total %d bytes, want at least %d (every raw record delivered)", total, want)
	}
}

// callsExporter records each Export's record count.
type callsExporter struct {
	memExporter
	calls []int
}

func (c *callsExporter) Export(ctx context.Context, recs []sdklog.Record) error {
	c.calls = append(c.calls, len(recs))
	return c.memExporter.Export(ctx, recs)
}

// The byte split leaves an ordinary raw batch (16 records of ≈ 650 KiB)
// in one export, splits a worst-case one (16 × ≈ 3 MiB) into exports
// under the limit, and keeps every record in order.
func TestRawExportSplitByBytes(t *testing.T) {
	batch := func(n, size int) []sdklog.Record {
		recs := make([]sdklog.Record, n)
		for i := range recs {
			recs[i].SetEventName("weft.raw")
			recs[i].SetBody(attribute.StringValue(strings.Repeat("x", size)))
		}
		return recs
	}
	for _, c := range []struct {
		name      string
		size      int
		wantCalls []int
	}{
		{"ordinary", 650 << 10, []int{16}},
		{"worst case", 3 << 20, []int{3, 3, 3, 3, 3, 1}},
	} {
		t.Run(c.name, func(t *testing.T) {
			inner := &callsExporter{}
			e := byteSplitLogExporter{Exporter: inner, limit: rawExportBytes}
			if err := e.Export(context.Background(), batch(16, c.size)); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(inner.calls, c.wantCalls) || len(inner.snapshot()) != 16 {
				t.Errorf("exports %v (%d records), want %v", inner.calls, len(inner.snapshot()), c.wantCalls)
			}
		})
	}
}
