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
	lp *sdklog.LoggerProvider

	local       obsdb.DB   // the Local destination's DB (the last one configured): what LocalDB returns
	locals      []obsdb.DB // every Local destination's DB; all close at Shutdown
	dests       []*destRuntime
	studioURL   string
	studioToken string

	tracker  *runTracker
	beatMu   sync.Mutex
	beatStop chan struct{}
	beatDone chan struct{}
	beatCfg  time.Duration

	closeOnce sync.Once
	closeErr  error
}

// TracerProvider returns the pipeline's tracer provider.
func (p *Pipeline) TracerProvider() *sdktrace.TracerProvider { return p.tp }

// LoggerProvider returns the pipeline's logger provider.
func (p *Pipeline) LoggerProvider() *sdklog.LoggerProvider { return p.lp }

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
// DB last.
func (p *Pipeline) Shutdown(ctx context.Context) error {
	p.closeOnce.Do(func() {
		// The package accessors stop handing this pipeline out first: a
		// LocalDB() after shutdown is nil, not a closed database.
		installedMu.Lock()
		if installedPipeline == p {
			installedPipeline = nil
		}
		installedMu.Unlock()
		p.stopHeartbeat()
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
	})
	return p.closeErr
}

func (p *Pipeline) stopHeartbeat() {
	p.beatMu.Lock()
	defer p.beatMu.Unlock()
	if p.beatStop != nil {
		close(p.beatStop)
		<-p.beatDone
		p.beatStop, p.beatDone = nil, nil
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
		// Explicit > environment > file: a Studio named in code
		// (otel.Studio) switches the discovery read off, as
		// WEFT_STUDIO_URL does — the file never adds a second Studio,
		// and never becomes the one StudioEndpoint (and runtime.Install)
		// reports.
		if !hasStudio(cfg.dests) {
			getenv, joined = discoverStudio(getenv)
		}
		dests = append(dests, dedupe(cfg.dests, envDestinations(getenv))...)
	}
	if len(dests) == 0 {
		return nil, errNoDestinations
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
			"hint", "WEFT_STUDIO_URL names another; WEFT_DISCOVERY=off ignores the file")
	}
	if !cfg.noGlobal {
		registerGlobals(p)
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

// build assembles the pipeline from the config and destinations. On
// failure nothing stays behind: every destination already built is
// released (its processors' goroutines, its exporters, its database).
func build(ctx context.Context, cfg config, dests []dest) (p *Pipeline, failed error) {
	p = &Pipeline{tracker: newRunTracker()}
	p.beatCfg = cfg.heartbeat

	res, err := buildResource(cfg)
	if err != nil {
		return nil, err
	}
	var spanProcs []sdktrace.SpanProcessor
	var logProcs []sdklog.Processor
	spanProcs = append(spanProcs, &trackerSpanProc{t: p.tracker})
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
	p.lp = sdklog.NewLoggerProvider(lpOpts...)
	p.startHeartbeat()
	return p, nil
}

// destRuntime is one built destination.
type destRuntime struct {
	spanProc sdktrace.SpanProcessor
	logProc  sdklog.Processor
	localDB  obsdb.DB
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
			rt.spanProc = sdktrace.NewSimpleSpanProcessor(&localSpanExporter{db: db, drops: drops})
		}
		if d.logs {
			rt.logProc = &destProc{
				name: d.name, inner: sdklog.NewSimpleProcessor(&localLogExporter{db: db, drops: drops}),
				content: content, contentC: contentCfg, noDeltas: d.noDeltas, drops: drops,
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
	if spanExp != nil {
		counted := countingSpanExporter{SpanExporter: spanExp, drops: drops}
		if spanDelay > 0 {
			rt.spanProc = sdktrace.NewBatchSpanProcessor(counted, sdktrace.WithBatchTimeout(spanDelay))
		} else {
			rt.spanProc = sdktrace.NewBatchSpanProcessor(counted)
		}
	}
	if logExp != nil {
		counted := countingLogExporter{Exporter: logExp, drops: drops}
		var inner sdklog.Processor
		if logDelay > 0 {
			inner = sdklog.NewBatchProcessor(counted, sdklog.WithExportInterval(logDelay))
		} else {
			inner = sdklog.NewBatchProcessor(counted)
		}
		rt.logProc = &destProc{name: d.name, inner: inner,
			content: content, contentC: contentCfg, noDeltas: d.noDeltas, drops: drops}
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
				heartbeats(context.Background(), logger, p.tracker)
			}
		}
	}()
}
