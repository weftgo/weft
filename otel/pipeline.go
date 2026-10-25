package otel

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/version"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otlploghttp "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	otlptracehttp "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/semconv/v1.41.0"
)

// envGetenv is the one environment seam (tests swap it); the core reads
// no environment variable — this module does, here only.
var envGetenv = os.Getenv

// Instrumentation names, shared with the core's.
const (
	instrumentationName = "github.com/weftgo/weft/core"
	eventHeartbeat      = "weft.heartbeat"
)

// Pipeline is the running fan-out: one TracerProvider and one
// LoggerProvider, one processor chain per destination, one run tracker.
// Create it with Start (or Install, which never fails).
type Pipeline struct {
	tp *sdktrace.TracerProvider
	lp *LoggerProvider

	local       obsdb.DB   // the Local destination's DB (the last one configured): what LocalDB returns
	locals      []obsdb.DB // every Local destination's DB; all close at Shutdown
	dests       []*destRuntime
	studioURL   string
	studioToken string

	tracker  *runTracker
	bridge   bridgeState // the slog bridge (slog.go)
	beatMu   sync.Mutex
	beatStop chan struct{}
	beatDone chan struct{}
	beatCfg  time.Duration

	// Shutdown's once: the first call runs it and closes closed; every
	// other call waits for that (or its own context) and returns
	// closeErr, written before closed closes.
	closing  atomic.Bool
	closed   chan struct{}
	closeErr error
}

// TracerProvider returns the pipeline's tracer provider.
func (p *Pipeline) TracerProvider() *sdktrace.TracerProvider { return p.tp }

// LoggerProvider returns the pipeline's logger provider: the SDK's,
// carrying the raw-record marker (RawBodies).
func (p *Pipeline) LoggerProvider() *LoggerProvider { return p.lp }

// LoggerProvider is the pipeline's OpenTelemetry logger provider: the
// SDK provider every method of which it forwards, plus the marker the
// core asks before it builds a raw record (ADR 0028 §12). A stock SDK
// provider has no marker, so an agent handed one never records wire
// bodies, whatever its processors answer Enabled.
type LoggerProvider struct {
	*sdklog.LoggerProvider
	raw bool
}

// RawBodies reports whether this pipeline records provider wire bodies:
// otel.RawBodies(true) and WEFT_ENV=dev at Start (never under NoEnv).
// It is the duck-typed marker core.Reporter.WantsRaw asks for; the
// destinations still decide per chain (Local and Studio only).
func (p *LoggerProvider) RawBodies() bool { return p != nil && p.raw }

// LocalDB returns the pipeline's local obsdb.DB — the handle setup A
// shares with Studio (studio.DB(otel.LocalDB())). nil without a Local
// destination.
//
// The spec's S2.1 names this accessor otel.Local(), which cannot
// coexist with the Local(path) destination option in Go (one package,
// one name); LocalDB is the recorded deviation — see
// weft-otel-build/notes-lane-a2.md.
func (p *Pipeline) LocalDB() obsdb.DB { return p.local }

// StudioEndpoint returns the pipeline's Studio destination (url and
// token), which weft/runtime dials by default. "" without one.
func (p *Pipeline) StudioEndpoint() (string, string) { return p.studioURL, p.studioToken }

// ForceFlush flushes every destination, all at once: each
// destination's span and log chains flush on their own goroutines
// under ctx, so a slow or hung destination costs only its own data,
// never the budget of the one behind it (flushed through the providers
// they go one after another).
func (p *Pipeline) ForceFlush(ctx context.Context) error {
	var flushes []func(context.Context) error
	for _, rt := range p.dests {
		if rt.spanProc != nil {
			flushes = append(flushes, rt.spanProc.ForceFlush)
		}
		if rt.logProc != nil {
			flushes = append(flushes, rt.logProc.ForceFlush)
		}
	}
	errs := make(chan error, len(flushes))
	for _, flush := range flushes {
		go func() { errs <- flush(ctx) }()
	}
	var err error
	for range flushes {
		select {
		case e := <-errs:
			err = errors.Join(err, e)
		case <-ctx.Done():
			// A flush that ignores its context is not waited for.
			return errors.Join(err, ctx.Err())
		}
	}
	return err
}

// Shutdown runs S2.4's order: stop heartbeats → flush every destination
// in parallel (5 s budget) → shut down the providers → close the local
// DB last. Every wait in it is bounded by ctx: a destination stuck in a
// write (the Local sink behind another process's lock, a hung
// exporter) costs the caller its context, never more. Shutdown runs
// once; a concurrent or later call returns the first's result, or its
// own ctx's error should that come first.
func (p *Pipeline) Shutdown(ctx context.Context) error {
	if !p.closing.CompareAndSwap(false, true) {
		select {
		case <-p.closed:
			return p.closeErr
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	defer close(p.closed)
	// The default logger first (Slog): the program's lines go back to
	// where they went before, while every destination is still open.
	if p.bridge.restore != nil {
		p.bridge.restore()
	}
	// The package accessors stop handing this pipeline out first: a
	// LocalDB() after shutdown is nil, not a closed database.
	installedMu.Lock()
	if installedPipeline == p {
		installedPipeline = nil
	}
	installedMu.Unlock()
	p.stopHeartbeat(ctx)
	flushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	_ = p.ForceFlush(flushCtx)
	cancel()
	if p.lp != nil {
		_ = p.lp.Shutdown(ctx)
	}
	if p.tp != nil {
		_ = p.tp.Shutdown(ctx)
	}
	for _, db := range p.locals {
		p.closeErr = errors.Join(p.closeErr, db.Close())
	}
	return p.closeErr
}

// stopHeartbeat stops the heartbeat goroutine and waits for it under
// ctx. The goroutine checks stop between records, so the wait is at
// most the one emit in flight — and no longer than ctx even when that
// emit is stuck in a destination: an emit is a provider operation,
// which the provider's own Shutdown bounds by the same ctx.
func (p *Pipeline) stopHeartbeat(ctx context.Context) {
	p.beatMu.Lock()
	stop, done := p.beatStop, p.beatDone
	p.beatStop, p.beatDone = nil, nil
	p.beatMu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// Start builds and starts the pipeline: one provider pair, the run
// tracker, one processor chain per destination (explicit options and
// environment combined, de-duplicated with the explicit destination
// winning). It fails if any destination fails to build — what was
// already built is shut down again, exporters handed to Exporters
// included; NoGlobal leaves the OTel globals alone (tests).
func Start(ctx context.Context, opts ...Option) (*Pipeline, error) {
	cfg := config{heartbeat: defaultHeartbeat}
	for _, o := range opts {
		o.apply(&cfg)
	}
	dests := append([]dest{}, cfg.dests...)
	var joined *discovered
	if !cfg.noEnv {
		getenv := envGetenv
		// Explicit > environment > file: a Studio or a Local named in
		// code (otel.Studio, otel.Local) switches the discovery read
		// off, as WEFT_STUDIO_URL and NoDiscovery do — the file never
		// adds a second Studio, never writes runs into a store the
		// caller did not name (issue 001), and never becomes the one
		// StudioEndpoint (and runtime.Install) reports.
		if !cfg.noDiscovery && !hasStudio(cfg.dests) && !hasLocal(cfg.dests) {
			getenv, joined = discoverStudio(getenv)
		}
		dests = append(dests, dedupe(cfg.dests, envDestinations(getenv))...)
	}
	if len(dests) == 0 {
		return nil, errNoDestinations
	}
	// RawBodies is a development switch (ADR 0028 §12): kept only under
	// WEFT_ENV=dev as this pipeline reads the environment; NoEnv reads
	// none, so it refuses it. A refusal is one WARN, after a successful
	// build (Install may call Start more than once).
	rawRefused := cfg.rawBodies && (cfg.noEnv || envGetenv("WEFT_ENV") != "dev")
	if rawRefused {
		cfg.rawBodies = false
	}
	// Blocking is the same kind of switch (U5): a destination that waits
	// for queue room slows the app, which only development may ask for.
	blockingRefused := false
	if cfg.noEnv || envGetenv("WEFT_ENV") != "dev" {
		for i := range dests {
			if dests[i].blocking {
				dests[i].blocking, blockingRefused = false, true
			}
		}
	}
	p, failed := build(ctx, cfg, dests)
	if failed != nil {
		return nil, failed
	}
	if joined != nil && p.studioURL == normalizeEnvEndpoint(joined.info.URL) {
		// The one INFO line: which Studio this process joined, and how
		// to stop it doing so.
		slog.Info("weft/otel: joined the running Studio", "url", joined.info.URL,
			"pid", joined.info.PID, "file", joined.path,
			"hint", "WEFT_STUDIO_URL names another; otel.NoDiscovery() or WEFT_DISCOVERY=off ignores the file")
	}
	if rawRefused {
		slog.Warn("otel: RawBodies ignored: WEFT_ENV is not dev",
			"hint", "set WEFT_ENV=dev (and drop otel.NoEnv) to record provider wire bodies")
	}
	if blockingRefused {
		slog.Warn("otel: Blocking ignored: WEFT_ENV is not dev",
			"hint", "a full queue refuses records (counted, warned); set WEFT_ENV=dev (and drop otel.NoEnv) to wait instead, or raise otel.QueueSize")
	}
	if !cfg.noGlobal {
		registerGlobals(p)
		if cfg.slog {
			p.installSlog()
		}
	}
	return p, nil
}

// errNoDestinations is Start's answer when nothing is configured —
// Install's cue to fall back to the zero-config local sink.
var errNoDestinations = errors.New("otel: no destinations configured")

// errAllFailed is Install's WARN-and-skip mode ending with nothing
// built. Not errNoDestinations: a program that named its destinations
// does not get the local sink behind its back (Install falls back only
// when every failed destination came from the environment).
var errAllFailed = errors.New("otel: every destination failed to build")

// Install starts the pipeline and registers it globally. It never
// panics and never fails the program: with no options at all (and no
// environment destinations) it writes the local sink only (the
// zero-config rule), and a destination that cannot be built is logged
// (slog, WARN) and skipped. When no destination option was passed and
// every environment destination failed to build, it falls back to the
// local sink with one WARN rather than recording nothing (Start returns
// the error instead); a program that passed its own destinations gets
// no destination it did not name. The returned function flushes and
// shuts everything down; call it on exit.
func Install(opts ...Option) func() {
	// NoGlobal is Start's test escape hatch; Install always registers.
	// dropFailed from the first attempt: each destination is built
	// once (a failed Start shuts down what it built, which a retry over
	// the same Exporters would then be handed dead).
	opts = append(append([]Option{}, opts...), forceGlobal{}, dropFailed{})
	p, err := Start(context.Background(), opts...)
	switch {
	case errors.Is(err, errNoDestinations):
		p, err = Start(context.Background(), append(opts, Local(""))...)
	case errors.Is(err, errAllFailed) && !explicitDestinations(opts):
		// Only environment destinations were configured and none built:
		// the zero-config local sink rather than recording nothing. A
		// program that named its own destinations is never handed one.
		slog.Warn("weft/otel: every environment destination failed to build; falling back to the local sink",
			"path", defaultLocalPath(envGetenv))
		p, err = Start(context.Background(), append(opts, NoEnv(), Local(""))...)
	}
	if err != nil {
		slog.Warn("weft/otel: install failed", "err", err.Error())
		return func() {}
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.Shutdown(ctx)
	}
}

// explicitDestinations reports whether opts name any destination
// (Local, Studio, Datadog, Langfuse, OTLP, Exporters).
func explicitDestinations(opts []Option) bool {
	var c config
	for _, o := range opts {
		o.apply(&c)
	}
	return len(c.dests) > 0
}

// forceGlobal clears the NoGlobal flag (Install always registers).
type forceGlobal struct{}

func (forceGlobal) apply(c *config) { c.noGlobal = false }

// dropFailed makes Start skip destinations that fail to build instead
// of failing (Install's WARN-and-skip).
type dropFailed struct{}

func (dropFailed) apply(c *config) { c.dropFailed = true }

const defaultHeartbeat = 10 * time.Second

// rawBatchSize is a raw-taking batch destination's log batch size under
// RawBodies (ADR 0028 §12): 16 ordinary raw records (≈ 650 KiB each)
// stay under Studio's 16 MiB ingest body limit in one export.
const rawBatchSize = 16

// rawExportBytes bounds one log export of a raw-taking batch
// destination by bytes (ADR 0028 §12): a raw record's worst case is
// ≈ 3 MiB (two 256 KiB bodies of control bytes, each escaped to six
// bytes in JSON), so 16 of them would be ≈ 48 MiB and Studio's ingest
// (studio/ingest.MaxBody, 16 MiB) would refuse the whole batch — every
// record in it lost. A batch whose records sum past this is exported in
// several calls; 4 MiB of margin covers the OTLP envelope. Ordinary
// batches (16 × ≈ 650 KiB) fit in one call, as before.
const rawExportBytes = 12 << 20

// byteSplitLogExporter exports a batch in consecutive chunks of at most
// limit estimated bytes (recordBytes); a record over the limit alone is
// exported alone. Each chunk is its own Export, so a failure loses (and
// the counting exporter beneath counts) only that chunk.
type byteSplitLogExporter struct {
	sdklog.Exporter
	limit int
}

func (e byteSplitLogExporter) Export(ctx context.Context, recs []sdklog.Record) error {
	var errs []error
	start, size := 0, 0
	for i := range recs {
		n := recordBytes(&recs[i])
		if i > start && size+n > e.limit {
			if err := e.Exporter.Export(ctx, recs[start:i]); err != nil {
				errs = append(errs, err)
			}
			start, size = i, 0
		}
		size += n
	}
	if start < len(recs) {
		if err := e.Exporter.Export(ctx, recs[start:]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// recordBytes estimates a log record's encoded size: its body's and
// attributes' keys and values, plus a fixed allowance for the rest.
func recordBytes(r *sdklog.Record) int {
	n := 64 + len(r.EventName()) + valueBytes(r.Body())
	r.WalkAttributes(func(kv attribute.KeyValue) bool {
		n += 8 + len(kv.Key) + valueBytes(kv.Value)
		return true
	})
	return n
}

// valueBytes estimates a value's encoded size.
func valueBytes(v attribute.Value) int {
	switch v.Type() {
	case attribute.STRING:
		return 4 + len(v.AsString())
	case attribute.BYTESLICE:
		return 4 + len(v.AsByteSlice())
	case attribute.STRINGSLICE:
		n := 4
		for _, e := range v.AsStringSlice() {
			n += 4 + len(e)
		}
		return n
	case attribute.SLICE:
		n := 4
		for _, e := range v.AsSlice() {
			n += valueBytes(e)
		}
		return n
	case attribute.MAP:
		n := 4
		for _, kv := range v.AsMap() {
			n += 8 + len(kv.Key) + valueBytes(kv.Value)
		}
		return n
	case attribute.BOOLSLICE:
		return 4 + 2*len(v.AsBoolSlice())
	case attribute.INT64SLICE:
		return 4 + 12*len(v.AsInt64Slice())
	case attribute.FLOAT64SLICE:
		return 4 + 12*len(v.AsFloat64Slice())
	default:
		return 12
	}
}

// build assembles the pipeline from the config and destinations. On
// failure nothing stays behind: every destination already built is
// released (its processors' goroutines, its exporters, its database).
func build(ctx context.Context, cfg config, dests []dest) (p *Pipeline, failed error) {
	p = &Pipeline{tracker: newRunTracker(), closed: make(chan struct{})}
	p.beatCfg = cfg.heartbeat

	res, err := buildResource(cfg)
	if err != nil {
		return nil, err
	}
	var spanProcs []sdktrace.SpanProcessor
	var logProcs []sdklog.Processor
	spanProcs = append(spanProcs, &trackerSpanProc{t: p.tracker}, bridgeSpanProc{p: p})
	logProcs = append(logProcs, p.tracker)

	var built []*destRuntime
	for _, d := range dests {
		rt, err := buildDest(ctx, d, cfg)
		if err != nil {
			if cfg.dropFailed {
				slog.Warn("weft/otel: destination skipped", "dest", d.name, "err", err.Error())
				continue
			}
			for _, b := range built {
				b.release(ctx)
			}
			return nil, fmt.Errorf("otel: destination %s: %w", d.name, err)
		}
		built = append(built, rt)
		p.dests = built
		if rt.spanProc != nil {
			spanProcs = append(spanProcs, rt.spanProc)
		}
		if rt.logProc != nil {
			logProcs = append(logProcs, rt.logProc)
		}
		if rt.localDB != nil {
			p.local = rt.localDB
			p.locals = append(p.locals, rt.localDB)
		}
		if d.kind == destStudio {
			p.studioURL, p.studioToken = normalizeEnvEndpoint(d.url), d.token
		}
	}
	if len(built) == 0 {
		return nil, errAllFailed
	}
	tpOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(runEndSampler{samplerOf(cfg)}),
	}
	for _, sp := range spanProcs {
		tpOpts = append(tpOpts, sdktrace.WithSpanProcessor(sp))
	}
	p.tp = sdktrace.NewTracerProvider(tpOpts...)
	lpOpts := []sdklog.LoggerProviderOption{sdklog.WithResource(res)}
	for _, lp := range logProcs {
		lpOpts = append(lpOpts, sdklog.WithProcessor(lp))
	}
	p.lp = &LoggerProvider{LoggerProvider: sdklog.NewLoggerProvider(lpOpts...), raw: cfg.rawBodies}
	p.bridge.logger = p.lp.Logger(bridgeScope)
	p.startHeartbeat()
	return p, nil
}

// destRuntime is one built destination.
type destRuntime struct {
	spanProc sdktrace.SpanProcessor
	logProc  sdklog.Processor
	localDB  obsdb.DB
	// logExp is the exporter the log batch processor exports through
	// (nil for Local, whose simple processor writes the database) —
	// kept so a test can pin the chain's shape (the raw byte split)
	// without depending on how a batch happens to form.
	logExp sdklog.Exporter
}

// release shuts a built destination down outside a provider — the
// build-failure path, where no provider exists yet to do it: the batch
// processors' goroutines end, the exporters close, the database closes.
func (rt *destRuntime) release(ctx context.Context) {
	if rt.spanProc != nil {
		_ = rt.spanProc.Shutdown(ctx)
	}
	if rt.logProc != nil {
		_ = rt.logProc.Shutdown(ctx)
	}
	if rt.localDB != nil {
		_ = rt.localDB.Close()
	}
}

// buildDest builds one destination's exporters and processor chains.
// A destination that fails partway releases what it had built.
func buildDest(ctx context.Context, d dest, cfg config) (_ *destRuntime, err error) {
	rt := &destRuntime{}
	defer func() {
		if err != nil {
			rt.release(ctx)
		}
	}()
	content := d.contentOn()
	contentCfg := cfg.content
	if d.contentCfg != nil {
		contentCfg = *d.contentCfg
	}
	// The drop counter's logger is fixed here, at construction, before
	// any goroutine can emit (dropped()'s old lazy assignment raced
	// concurrent runs — the atomics guarded the counters, not the
	// field).
	drops := newDropCounter(d.name)

	logDelay, spanDelay := d.batchDelays()
	var spanExp sdktrace.SpanExporter
	var logExp sdklog.Exporter
	switch d.kind {
	case destLocal:
		db, err := openLocal(d.path)
		if err != nil {
			return nil, err
		}
		rt.localDB = db
		if d.traces {
			rt.spanProc = sdktrace.NewSimpleSpanProcessor(unmarkUnlessAppLogs(d, &localSpanExporter{db: db, drops: drops}))
		}
		if d.logs {
			rt.logProc = &destProc{
				name: d.name, inner: sdklog.NewSimpleProcessor(&localLogExporter{db: db, drops: drops}),
				content: content, contentC: contentCfg, noDeltas: d.noDeltas, drops: drops,
				raw: cfg.rawBodies && d.rawOn(), appLogs: d.appLogsOn(),
			}
		}
	case destStudio, destDatadog, destLangfuse, destOTLP:
		endpoint, pathPrefix, headers, insecure, err := transportOf(d)
		if err != nil {
			return nil, err
		}
		if d.traces {
			spanExp, err = traceExporter(ctx, endpoint, pathPrefix+"/v1/traces", headers, insecure, d.timeout)
			if err != nil {
				return nil, err
			}
		}
		if d.logs {
			logExp, err = logExporter(ctx, endpoint, pathPrefix+"/v1/logs", headers, insecure, d.timeout)
			if err != nil {
				if spanExp != nil {
					_ = spanExp.Shutdown(ctx)
				}
				return nil, err
			}
		}
	case destExporters:
		if d.traces {
			spanExp = d.spanExp
		}
		if d.logs {
			logExp = d.logExp
		}
	default:
		return nil, fmt.Errorf("unknown destination kind %d", d.kind)
	}
	// The batch chains (every kind but Local). The exporters are
	// wrapped to count what a failed export lost — the destination's
	// own drops (S2.4).
	//
	// Each batch chain admits its records ahead of the SDK's queue
	// (admission.go), the SDK queue sized to match (the span queue with
	// room for ForceFlush's markers), so a full queue is weft's counted
	// refusal — never the SDK's silent drop.
	queue := d.queueSizeOf()
	if spanExp != nil {
		var spanOpts []sdktrace.BatchSpanProcessorOption
		if spanDelay > 0 {
			spanOpts = append(spanOpts, sdktrace.WithBatchTimeout(spanDelay))
		}
		rt.spanProc = newAdmittedSpanProc(countingSpanExporter{SpanExporter: unmarkUnlessAppLogs(d, spanExp), drops: drops},
			queue, d.blocking, drops, spanOpts...)
	}
	if logExp != nil {
		var exp sdklog.Exporter = countingLogExporter{Exporter: logExp, drops: drops}
		var batch []sdklog.BatchProcessorOption
		if logDelay > 0 {
			batch = append(batch, sdklog.WithExportInterval(logDelay))
		}
		if cfg.rawBodies && d.rawOn() {
			// A raw record is typically ≈ 650 KiB and at worst ≈ 3 MiB
			// (two 256 KiB bodies of control bytes, JSON-escaped): the
			// SDK's default 512-record batch would far exceed Studio's
			// 16 MiB ingest limit (studio/ingest.MaxBody) and be refused
			// whole. rawBatchSize bounds the batch by count, and the
			// byte split each export by size, whatever the records hold.
			batch = append(batch, sdklog.WithExportMaxBatchSize(rawBatchSize))
			exp = byteSplitLogExporter{Exporter: exp, limit: rawExportBytes}
		}
		rt.logExp = exp
		inner, adm := newAdmittedLogBatch(exp, queue, d.blocking, drops, batch...)
		rt.logProc = &destProc{name: d.name, inner: inner,
			content: content, contentC: contentCfg, noDeltas: d.noDeltas, drops: drops,
			raw: cfg.rawBodies && d.rawOn(), adm: adm, appLogs: d.appLogsOn()}
	}
	if rt.spanProc == nil && rt.logProc == nil {
		return nil, errors.New("neither signal enabled")
	}
	return rt, nil
}

// transportOf resolves an OTLP-family destination's endpoint, URL path
// prefix, headers and TLS posture.
func transportOf(d dest) (endpoint, pathPrefix string, headers map[string]string, insecure bool, err error) {
	headers = map[string]string{}
	for k, v := range d.headers {
		headers[k] = v
	}
	var raw string
	switch d.kind {
	case destStudio:
		raw = d.url
		if d.token != "" {
			headers["Authorization"] = "Bearer " + d.token
		}
	case destDatadog:
		raw = d.url
		if raw == "" {
			raw = datadogDefaultURL
		}
	case destLangfuse:
		raw = d.host
		headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString(
			[]byte(d.publicKey+":"+d.secretKey))
		pathPrefix = "/api/public/otel"
	case destOTLP:
		raw = d.url
	default:
		return "", "", nil, false, fmt.Errorf("not an OTLP destination: %d", d.kind)
	}
	if raw == "" {
		return "", "", nil, false, errors.New("no endpoint URL")
	}
	// A scheme-less URL is https, for every kind (the OTLP convention
	// the env endpoint already follows). The errors below never echo
	// the raw value: a URL may carry credentials.
	u, err := url.Parse(normalizeEnvEndpoint(raw))
	if err != nil {
		return "", "", nil, false, errors.New("bad endpoint URL: it does not parse")
	}
	if u.Host == "" {
		return "", "", nil, false, fmt.Errorf("bad endpoint URL %q: no host", u.Redacted())
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", nil, false, fmt.Errorf("endpoint scheme %q: OTLP/HTTP takes http:// or https://", u.Scheme)
	}
	if u.Scheme == "http" {
		host := u.Hostname()
		insecure = d.insecure || host == "localhost" || host == "127.0.0.1" || host == "::1"
		if !insecure {
			return "", "", nil, false, fmt.Errorf("http:// to non-loopback %q needs Insecure()", u.Host)
		}
	}
	if pathPrefix == "" {
		pathPrefix = strings.TrimSuffix(u.Path, "/")
	}
	return u.Host, pathPrefix, headers, insecure, nil
}

// endpointURL renders the full URL the exporters are given. Always the
// whole URL, scheme included: the OTLP exporters read
// OTEL_EXPORTER_OTLP_ENDPOINT themselves and take their TLS posture
// from its scheme, and a bare host (WithEndpoint) leaves that posture
// in force — a sidecar collector's http:// variable would send an
// https destination's token and content in cleartext. The URL form
// sets the posture explicitly.
func endpointURL(endpoint, path string, insecure bool) string {
	if insecure {
		return "http://" + endpoint + path
	}
	return "https://" + endpoint + path
}

func traceExporter(ctx context.Context, endpoint, path string, headers map[string]string, insecure bool, timeout time.Duration) (sdktrace.SpanExporter, error) {
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(endpointURL(endpoint, path, insecure)),
		otlptracehttp.WithHeaders(headers),
	}
	if timeout > 0 {
		opts = append(opts, otlptracehttp.WithTimeout(timeout))
	}
	return otlptracehttp.New(ctx, opts...)
}

func logExporter(ctx context.Context, endpoint, path string, headers map[string]string, insecure bool, timeout time.Duration) (sdklog.Exporter, error) {
	opts := []otlploghttp.Option{
		otlploghttp.WithEndpointURL(endpointURL(endpoint, path, insecure)),
		otlploghttp.WithHeaders(headers),
	}
	if timeout > 0 {
		opts = append(opts, otlploghttp.WithTimeout(timeout))
	}
	return otlploghttp.New(ctx, opts...)
}

func samplerOf(cfg config) sdktrace.Sampler {
	if cfg.sampler != nil {
		return cfg.sampler
	}
	return sdktrace.ParentBased(sdktrace.AlwaysSample())
}

// buildResource assembles the resource in precedence order, lowest
// first: the binary's name, the standard OTEL_SERVICE_NAME /
// OTEL_RESOURCE_ATTRIBUTES, the Service option, and the caller's
// Resource option over everything — the SDK's own WithResource order
// (sdk/trace/provider.go: Merge(resource.Environment(), r)); explicit
// wins over detected, and Merge's second argument wins. weft.version
// rides on the base.
func buildResource(cfg config) (*sdkresource.Resource, error) {
	base, err := sdkresource.New(context.Background(),
		sdkresource.WithAttributes(
			semconv.ServiceName(filepath.Base(os.Args[0])),
			attribute.String("weft.version", weftVersion()),
		),
		sdkresource.WithFromEnv(),
	)
	if err != nil {
		return nil, err
	}
	if cfg.service != "" {
		// The explicit name merges over the detected one. It cannot be
		// an attribute of base itself: inside one New call the later
		// fromEnv detector would overwrite it.
		base, err = sdkresource.Merge(base, sdkresource.NewSchemaless(
			semconv.ServiceName(cfg.service),
		))
		if err != nil {
			return nil, err
		}
	}
	if cfg.resource != nil {
		return sdkresource.Merge(base, cfg.resource)
	}
	return base, nil
}

// weftVersion is the framework module's tag, [version.Version] — the
// constant, not version.Runtime, because the core stamps weft.version
// on the same run's spans and records from its own literal, and one run
// must carry one value. TestWeftVersionMatchesRoot pins the two.
func weftVersion() string { return version.Version }

// The installed pipeline, for the package-level accessors.
var (
	installedMu       sync.RWMutex
	installedPipeline *Pipeline
)

func registerGlobals(p *Pipeline) {
	otel.SetTracerProvider(p.tp)
	global.SetLoggerProvider(p.lp)
	installedMu.Lock()
	installedPipeline = p
	installedMu.Unlock()
}

// LocalDB returns the installed pipeline's local obsdb.DB — for
// studio.DB(otel.LocalDB()) (setup A, [D4]). nil before Install, after
// its shutdown, or without a Local destination.
//
// S2.1 names this otel.Local(), which cannot coexist with the
// Local(path) destination option in Go; LocalDB is the recorded
// deviation (notes-lane-a2.md).
func LocalDB() obsdb.DB {
	installedMu.RLock()
	defer installedMu.RUnlock()
	if installedPipeline == nil {
		return nil
	}
	return installedPipeline.local
}

// StudioEndpoint returns the installed pipeline's Studio destination,
// which weft/runtime dials by default. "" without one (or after the
// pipeline's shutdown).
func StudioEndpoint() (string, string) {
	installedMu.RLock()
	defer installedMu.RUnlock()
	if installedPipeline == nil {
		return "", ""
	}
	return installedPipeline.studioURL, installedPipeline.studioToken
}

// startHeartbeat runs the tracker's ticker: every interval, one
// weft.heartbeat record per open run through the provider's logger.
// Heartbeat(0) at Start disables it; so does a negative interval
// (time.NewTicker panics on one, on a goroutine nothing can recover).
func (p *Pipeline) startHeartbeat() {
	if p.beatCfg <= 0 {
		return
	}
	p.beatMu.Lock()
	defer p.beatMu.Unlock()
	if p.beatStop != nil {
		return
	}
	stop, done := make(chan struct{}), make(chan struct{})
	p.beatStop, p.beatDone = stop, done
	logger := p.lp.Logger(instrumentationName)
	go func() {
		defer close(done)
		ticker := time.NewTicker(p.beatCfg)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				// A tick and stop both ready: select picks either,
				// so stop is asked again before another pass.
				select {
				case <-stop:
					return
				default:
				}
				heartbeats(context.Background(), logger, p.tracker, stop, p.bridged)
			}
		}
	}()
}
