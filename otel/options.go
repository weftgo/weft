package otel

import (
	"time"

	"github.com/weftgo/weft"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Option configures the pipeline (Install/Start).
type Option interface{ apply(*config) }

type optionFunc func(*config)

func (f optionFunc) apply(c *config) { f(c) }

// ContentConfig carries the caps and redaction that live per
// destination, not in the core ([D2]: the core cannot discover them —
// the OTel global never hands out the SDK provider).
type ContentConfig struct {
	// MaxBytes caps one event field or delta body; 0 = 32 KiB; negative
	// = unlimited. Never applied to messages records — a capped
	// transcript is not replay-grade.
	MaxBytes int
	// Redact is applied to a content field before the cap; nil = identity.
	// It sees event bodies and deltas only: weft.messages transcript
	// records are NOT passed through it and reach a content-on
	// destination unredacted.
	Redact func(kind weft.ContentKind, s string) string
}

// Content sets the pipeline-wide default for destinations with content
// on; a destination's WithContent overrides it.
func Content(cfg ContentConfig) Option {
	return optionFunc(func(c *config) { c.content = cfg })
}

// Service sets resource service.name. Else OTEL_SERVICE_NAME, else the
// binary's name.
func Service(name string) Option {
	return optionFunc(func(c *config) { c.service = name })
}

// Resource merges r over the detected resource.
func Resource(r *sdkresource.Resource) Option {
	return optionFunc(func(c *config) {
		if r != nil {
			c.resource = r
		}
	})
}

// Heartbeat sets how often the run tracker emits one weft.heartbeat
// record per open run (the default 10s; 0 or less disables). Three missed
// intervals are what obsdb's InterruptedAfter reads as interrupted.
func Heartbeat(every time.Duration) Option {
	return optionFunc(func(c *config) { c.heartbeat = every })
}

// Sampler sets the trace sampler (default ParentBased(AlwaysOn)).
// Records are log records; sampling never applies to them.
func Sampler(s sdktrace.Sampler) Option {
	return optionFunc(func(c *config) {
		if s != nil {
			c.sampler = s
		}
	})
}

// NoEnv ignores the WEFT_* / OTEL_* destination variables. Explicit
// options are unaffected.
func NoEnv() Option {
	return optionFunc(func(c *config) { c.noEnv = true })
}

// NoGlobal leaves the OTel globals alone (Start only; Install always
// registers). For tests and dependency-injected programs.
func NoGlobal() Option {
	return optionFunc(func(c *config) { c.noGlobal = true })
}

// The destinations — any number, all active at once. Each becomes its
// own processor chain with its own queue, drop counters and content
// policy; a slow or failing destination cannot block the others.

// Local is the local sink: obsdb/sqlite at path ("" = $WEFT_DB or
// ./.weft/weft.db), written synchronously — nothing emitted is lost,
// and the DB's in-process hub is the live lane setup A shares with
// Studio. Content on, traces + logs.
func Local(path string, opts ...DestOption) Option {
	return optionFunc(func(c *config) {
		d := newDest(destLocal, "local")
		d.path = path
		d.apply(opts)
		c.dests = append(c.dests, d)
	})
}

// Studio exports OTLP/HTTP protobuf to a Weft Studio with a bearer
// token. Content on, traces + logs, short batches (logs 200 ms, spans
// 1 s).
func Studio(url, token string, opts ...DestOption) Option {
	return optionFunc(func(c *config) {
		d := newDest(destStudio, "studio")
		d.url, d.token = url, token
		d.apply(opts)
		c.dests = append(c.dests, d)
	})
}

// Datadog exports to the local Datadog Agent's OTLP intake
// (http://localhost:4318; the Agent binds the standard OTLP ports when
// its OTLP receiver is enabled). Content off, traces + logs, SDK
// default batching. DatadogEndpoint overrides the URL.
func Datadog(opts ...DestOption) Option {
	return optionFunc(func(c *config) {
		d := newDest(destDatadog, "datadog")
		d.apply(opts)
		c.dests = append(c.dests, d)
	})
}

// Langfuse exports traces to Langfuse's OTLP endpoint
// (<host>/api/public/otel/v1/traces, Basic auth base64(pk:sk); OTLP/HTTP
// only — gRPC is not accepted). Traces only: weft's spans carry no
// content (ADR 0016 O7) and Langfuse ingests traces, so it gets timing,
// usage, tool names and the identity chain; span-level content is the
// post-v1 follow-up F1.
func Langfuse(host, publicKey, secretKey string, opts ...DestOption) Option {
	return optionFunc(func(c *config) {
		d := newDest(destLangfuse, "langfuse")
		d.host, d.publicKey, d.secretKey = host, publicKey, secretKey
		d.logs = false
		d.apply(opts)
		c.dests = append(c.dests, d)
	})
}

// OTLP exports to any OTLP/HTTP endpoint (http(s)://host:port). Content
// off, traces + logs, SDK default batching; Headers adds request
// headers.
func OTLP(endpoint string, opts ...DestOption) Option {
	return optionFunc(func(c *config) {
		d := newDest(destOTLP, "otlp")
		d.url = endpoint
		d.apply(opts)
		c.dests = append(c.dests, d)
	})
}

// Exporters wires your own exporters (vendor-specific, stdout, tests):
// whichever is non-nil gets a chain. Content off, SDK default batching.
func Exporters(spans sdktrace.SpanExporter, logs sdklog.Exporter, opts ...DestOption) Option {
	return optionFunc(func(c *config) {
		d := newDest(destExporters, "exporters")
		d.spanExp, d.logExp = spans, logs
		if spans == nil {
			d.traces = false
		}
		if logs == nil {
			d.logs = false
		}
		d.apply(opts)
		c.dests = append(c.dests, d)
	})
}

// DestOption configures one destination.
type DestOption interface{ applyDest(*dest) }

type destOptionFunc func(*dest)

func (f destOptionFunc) applyDest(d *dest) { f(d) }

// WithContent turns content on for this destination, optionally
// overriding the pipeline's ContentConfig.
func WithContent(cfg ...ContentConfig) DestOption {
	return destOptionFunc(func(d *dest) {
		on := true
		d.content = &on
		if len(cfg) > 0 {
			c := cfg[0]
			d.contentCfg = &c
		}
	})
}

// NoContent turns content off: the chain strips event and delta bodies
// (weft.StripContent) and drops messages records before its exporter.
func NoContent() DestOption {
	return destOptionFunc(func(d *dest) {
		on := false
		d.content = &on
	})
}

// Signals selects which signals this destination takes. The defaults
// are per destination (Langfuse is traces only; the rest both).
func Signals(traces, logs bool) DestOption {
	return destOptionFunc(func(d *dest) {
		d.traces, d.logs = traces, logs
	})
}

// NoDeltas drops text/reasoning/args deltas before export, to save
// bandwidth when live streaming is not wanted. Deltas are never stored
// by the sinks either way (Q4); the core's durable sequence is
// untouched (they number on their own counter, D3).
func NoDeltas() DestOption {
	return destOptionFunc(func(d *dest) { d.noDeltas = true })
}

// BatchDelay sets the destination's batch flush interval (Studio
// defaults logs 200 ms / spans 1 s; the others the SDK's defaults).
func BatchDelay(d time.Duration) DestOption {
	return destOptionFunc(func(dd *dest) { dd.batchDelay = d })
}

// Headers adds HTTP headers to the destination's requests (OTLP-family
// destinations).
func Headers(h map[string]string) DestOption {
	return destOptionFunc(func(d *dest) {
		if d.headers == nil {
			d.headers = map[string]string{}
		}
		for k, v := range h {
			d.headers[k] = v
		}
	})
}

// Timeout sets the destination's export request timeout.
func Timeout(d time.Duration) DestOption {
	return destOptionFunc(func(dd *dest) { dd.timeout = d })
}

// Insecure allows http:// to a non-loopback host (OTLP-family
// destinations). Loopback http is always allowed, and so is the
// environment's OTEL_EXPORTER_OTLP_ENDPOINT written with http:// (the
// operator's opt-in; see envDestinations) — never WEFT_STUDIO_URL.
func Insecure() DestOption {
	return destOptionFunc(func(d *dest) { d.insecure = true })
}

// DatadogEndpoint overrides the Datadog destination's OTLP intake URL
// (default http://localhost:4318).
func DatadogEndpoint(url string) DestOption {
	return destOptionFunc(func(d *dest) { d.url = url })
}
