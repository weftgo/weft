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
	instrumentationName = "github.com/weftgo/weft"
	eventHeartbeat      = "weft.heartbeat"
)

// Pipeline is the running fan-out: one TracerProvider and one
// LoggerProvider, one processor chain per destination, one run tracker.
// Create it with Start (or Install, which never fails).
type Pipeline struct {
	tp *sdktrace.TracerProvider
	lp *sdklog.LoggerProvider

	local       obsdb.DB
	studioURL   string
	studioToken string

	tracker  *runTracker
	procs    []*destProc
	stop     map[string]func() // per-destination heartbeat-independent stops
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

// ForceFlush flushes every destination.
func (p *Pipeline) ForceFlush(ctx context.Context) error {
	// A pipeline whose build failed has no providers yet (Start shuts
	// the half-built value down on its error path); flush is then a
	// no-op, not a panic.
	var err error
	if p.tp != nil {
		err = errors.Join(err, p.tp.ForceFlush(ctx))
	}
	if p.lp != nil {
		err = errors.Join(err, p.lp.ForceFlush(ctx))
	}
	return err
}

// Shutdown runs S2.4's order: stop heartbeats → flush every destination
// in parallel (5 s budget) → shut down the providers → close the local
// DB last.
func (p *Pipeline) Shutdown(ctx context.Context) error {
	p.closeOnce.Do(func() {
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
		if p.local != nil {
			p.closeErr = p.local.Close()
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
// winning). It fails if any destination fails to build; NoGlobal leaves
// the OTel globals alone (tests).
func Start(ctx context.Context, opts ...Option) (*Pipeline, error) {
	cfg := config{heartbeat: defaultHeartbeat}
	for _, o := range opts {
		o.apply(&cfg)
	}
	dests := append([]dest{}, cfg.dests...)
	if !cfg.noEnv {
		dests = append(dests, dedupe(cfg.dests, envDestinations(envGetenv))...)
	}
	if len(dests) == 0 {
		return nil, errNoDestinations
	}
	p, failed := build(ctx, cfg, dests)
	if failed != nil {
		_ = p.Shutdown(ctx)
		return nil, failed
	}
	if !cfg.noGlobal {
		registerGlobals(p)
	}
	return p, nil
}

// errNoDestinations is Start's answer when nothing is configured —
// Install's cue to fall back to the zero-config local sink.
var errNoDestinations = errors.New("otel: no destinations configured")

// Install starts the pipeline and registers it globally. It never
// panics and never fails the program: with no options at all (and no
// environment destinations) it writes the local sink only (the
// zero-config rule), and a destination that cannot be built is logged
// (slog, WARN) and skipped. The returned function flushes and shuts
// everything down; call it on exit.
func Install(opts ...Option) func() {
	// NoGlobal is Start's test escape hatch; Install always registers.
	p, err := Start(context.Background(), append(opts, forceGlobal{})...)
	if errors.Is(err, errNoDestinations) {
		p, err = Start(context.Background(), append(opts, Local(""), forceGlobal{})...)
	}
	if err != nil {
		slog.Warn("weft/otel: install: skipping destinations that cannot be built", "err", err.Error())
		p, err = Start(context.Background(), append(opts, forceGlobal{}, dropFailed{})...)
		if err != nil {
			slog.Warn("weft/otel: install failed", "err", err.Error())
			return func() {}
		}
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.Shutdown(ctx)
	}
}

// forceGlobal clears the NoGlobal flag (Install always registers).
type forceGlobal struct{}

func (forceGlobal) apply(c *config) { c.noGlobal = false }

// dropFailed makes Start skip destinations that fail to build instead
// of failing (Install's WARN-and-skip).
type dropFailed struct{}

func (dropFailed) apply(c *config) { c.dropFailed = true }

const defaultHeartbeat = 10 * time.Second

// build assembles the pipeline from the config and destinations.
func build(ctx context.Context, cfg config, dests []dest) (p *Pipeline, failed error) {
	p = &Pipeline{tracker: newRunTracker()}
	p.beatCfg = cfg.heartbeat

	res, err := buildResource(cfg)
	if err != nil {
		return p, err
	}
	var spanProcs []sdktrace.SpanProcessor
	var logProcs []sdklog.Processor
	spanProcs = append(spanProcs, &trackerSpanProc{t: p.tracker})
	logProcs = append(logProcs, p.tracker)

	for _, d := range dests {
		rt, err := buildDest(ctx, d, cfg)
		if err != nil {
			if cfg.dropFailed {
				slog.Warn("weft/otel: destination skipped", "dest", d.name, "err", err.Error())
				continue
			}
			return p, fmt.Errorf("otel: destination %s: %w", d.name, err)
		}
		if rt.spanProc != nil {
			spanProcs = append(spanProcs, rt.spanProc)
		}
		if rt.logProc != nil {
			logProcs = append(logProcs, rt.logProc)
		}
		if rt.localDB != nil {
			p.local = rt.localDB
		}
		if d.kind == destStudio {
			p.studioURL, p.studioToken = d.url, d.token
		}
	}
	if len(spanProcs) == 1 && len(logProcs) == 1 {
		return p, errNoDestinations
	}
	tpOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(samplerOf(cfg)),
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
	close    func()
}

// buildDest builds one destination's exporters and processor chains.
func buildDest(ctx context.Context, d dest, cfg config) (*destRuntime, error) {
	rt := &destRuntime{}
	content := d.contentOn()
	contentCfg := cfg.content
	if d.contentCfg != nil {
		contentCfg = *d.contentCfg
	}
	drops := &dropCounter{name: d.name}

	logDelay, spanDelay := d.batchDelays()
	switch d.kind {
	case destLocal:
		db, err := openLocal(d.path)
		if err != nil {
			return nil, err
		}
		rt.localDB = db
		rt.close = func() { _ = db.Close() }
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
			spanExp, err := traceExporter(ctx, endpoint, pathPrefix+"/v1/traces", headers, insecure, d.timeout)
			if err != nil {
				return nil, err
			}
			if spanDelay > 0 {
				rt.spanProc = sdktrace.NewBatchSpanProcessor(spanExp, sdktrace.WithBatchTimeout(spanDelay))
			} else {
				rt.spanProc = sdktrace.NewBatchSpanProcessor(spanExp)
			}
		}
		if d.logs {
			logExp, err := logExporter(ctx, endpoint, pathPrefix+"/v1/logs", headers, insecure, d.timeout)
			if err != nil {
				return nil, err
			}
			var inner sdklog.Processor
			if logDelay > 0 {
				inner = sdklog.NewBatchProcessor(logExp, sdklog.WithExportInterval(logDelay))
			} else {
				inner = sdklog.NewBatchProcessor(logExp)
			}
			rt.logProc = &destProc{name: d.name, inner: inner,
				content: content, contentC: contentCfg, noDeltas: d.noDeltas, drops: drops}
		}
	case destExporters:
		if d.traces && d.spanExp != nil {
			rt.spanProc = sdktrace.NewBatchSpanProcessor(d.spanExp)
		}
		if d.logs && d.logExp != nil {
			rt.logProc = &destProc{name: d.name, inner: sdklog.NewBatchProcessor(d.logExp),
				content: content, contentC: contentCfg, noDeltas: d.noDeltas, drops: drops}
		}
	default:
		return nil, fmt.Errorf("unknown destination kind %d", d.kind)
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
			raw = "http://localhost:4318"
		}
	case destLangfuse:
		raw = d.host
		if !strings.Contains(raw, "://") {
			raw = "https://" + raw
		}
		headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString(
			[]byte(d.publicKey+":"+d.secretKey))
		pathPrefix = "/api/public/otel"
	case destOTLP:
		raw = d.url
	default:
		return "", "", nil, false, fmt.Errorf("not an OTLP destination: %d", d.kind)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", "", nil, false, fmt.Errorf("bad endpoint %q: %w", raw, err)
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

func traceExporter(ctx context.Context, endpoint, path string, headers map[string]string, insecure bool, timeout time.Duration) (sdktrace.SpanExporter, error) {
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithURLPath(path),
		otlptracehttp.WithHeaders(headers),
	}
	if insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	if timeout > 0 {
		opts = append(opts, otlptracehttp.WithTimeout(timeout))
	}
	return otlptracehttp.New(ctx, opts...)
}

func logExporter(ctx context.Context, endpoint, path string, headers map[string]string, insecure bool, timeout time.Duration) (sdklog.Exporter, error) {
	opts := []otlploghttp.Option{
		otlploghttp.WithEndpoint(endpoint),
		otlploghttp.WithURLPath(path),
		otlploghttp.WithHeaders(headers),
	}
	if insecure {
		opts = append(opts, otlploghttp.WithInsecure())
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

// buildResource assembles the resource: service.name from the Service
// option, OTEL_SERVICE_NAME or the binary's name, the standard
// OTEL_RESOURCE_ATTRIBUTES, the caller's Resource option merged over,
// and weft.version.
func buildResource(cfg config) (*sdkresource.Resource, error) {
	name := cfg.service
	if name == "" {
		name = envGetenv("OTEL_SERVICE_NAME")
	}
	if name == "" {
		name = filepath.Base(os.Args[0])
	}
	attrs := []attribute.KeyValue{
		semconv.ServiceName(name),
		attribute.String("weft.version", weftVersion()),
	}
	base, err := sdkresource.New(context.Background(),
		sdkresource.WithAttributes(attrs...),
		sdkresource.WithFromEnv(),
	)
	if err != nil {
		return nil, err
	}
	if cfg.resource != nil {
		return sdkresource.Merge(cfg.resource, base)
	}
	return base, nil
}

// weftVersion is the root module's version attribute value.
func weftVersion() string { return "v0.6.0" }

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
// studio.DB(otel.LocalDB()) (setup A, [D4]). nil before Install or
// without a Local destination.
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
// which weft/runtime dials by default. "" without one.
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
// Heartbeat(0) at Start disables it.
func (p *Pipeline) startHeartbeat() {
	if p.beatCfg == 0 {
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
