package otel

import (
	"time"

	"github.com/weftgo/weft/core"
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
	// MaxBytes caps one event field or delta body, a prompt record's
	// text and a tools record's body (whole tool entries dropped from
	// the end, so the body stays JSON); 0 = 32 KiB; negative =
	// unlimited. A cut sets weft.content.truncated_bytes. The default
	// applies to tools bodies too: a large catalog (an MCP server's, say)
	// arrives truncated, with truncated_bytes set — raise MaxBytes for
	// the local sink when the full catalog is wanted. Never applied to
	// messages records — a capped transcript is not replay-grade.
	MaxBytes int
	// Redact is applied to a content field before the cap; nil = identity.
	// It sees event and delta bodies and weft.messages transcript
	// records, the latter part by part with the kind the same content
	// has on the event path: text parts (every role) core.ContentText,
	// reasoning core.ContentReasoning, tool-call args core.ContentArgs (an
	// output that is not JSON is carried as a JSON string), tool results
	// core.ContentResult. It also sees a prompt record's text
	// (core.ContentPrompt) and each stop sequence of a request record
	// (core.ContentStop); a tools record is capped, never redacted (ADR
	// 0028 §6). Ids, names, roles, signatures, file parts and hashes are
	// not passed to it; a transcript record is redacted, never capped. It
	// runs on the run's goroutine: a panic in it is contained — the event
	// goes out stripped, the transcript record not at all — and counted
	// as the destination's drop; so is a transcript record that cannot be
	// decoded for redaction. Nothing it was given is ever sent unredacted.
	Redact func(kind core.ContentKind, s string) string
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

// NoEnv ignores the WEFT_* / OTEL_* destination variables, and the
// discovery file a running `weft studio` / `weft dev` wrote (which
// otherwise stands in for WEFT_STUDIO_URL when that is unset). Explicit
// options are unaffected. It is otel's alone: the runtime link's own
// discovery read (runtime.Install under WEFT_ENV=dev, when the pipeline
// has no Studio) is governed by runtime's options and
// WEFT_DISCOVERY=off.
func NoEnv() Option {
	return optionFunc(func(c *config) { c.noEnv = true })
}

// RawBodies records each model call attempt's wire exchange — the
// provider request and response the first-party adapters sent and
// received, headers included minus the credential ones, each body
// capped at 256 KiB — as one weft.raw record per attempt (ADR 0028
// §12), for the Local and Studio destinations only (never Datadog,
// Langfuse, OTLP or Exporters, whatever their content setting, and not
// a Local or Studio destination under NoContent). Off by default, and
// refused outside development: it takes effect only when WEFT_ENV is
// "dev" when the pipeline starts; otherwise — and always under NoEnv,
// which does not read WEFT_ENV — Start logs one WARN line ("otel:
// RawBodies ignored: WEFT_ENV is not dev") and the pipeline behaves as
// if the option were absent: no destination asks for weft.raw, so the
// adapters buffer nothing (core.Reporter.WantsRaw is false). The gate
// is the pipeline's LoggerProvider marker (LoggerProvider.RawBodies):
// pass Pipeline.LoggerProvider() to weft.LoggerProvider, or rely on
// Install's global registration — a stock SDK provider never records
// raw bodies. Under it a Studio destination's log batch is capped at 16
// records and each export is split at 12 MiB, keeping every post under
// Studio's 16 MiB ingest limit: a raw record is ≈ 650 KiB with two full
// ordinary bodies and at worst ≈ 3 MiB (bodies of control bytes, each
// six bytes in JSON; it is encoded without HTML escaping).
func RawBodies(on bool) Option {
	return optionFunc(func(c *config) { c.rawBodies = on })
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
// 1 s). Naming a Studio here switches the discovery file off: the
// explicit destination is the pipeline's one Studio from the file's
// point of view (WEFT_STUDIO_URL still adds its own, de-duplicated).
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
// (core.StripContent) and drops messages records before its exporter.
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

// QueueSize sets how many records (and, separately, spans) the
// destination holds between emission and export — its batch queue
// (default 16,384 for Studio, the SDK's 2,048 for the rest; n <= 0
// keeps the default). A full queue never blocks the app: it refuses the
// record, deltas first (at half full), then ordinary records (at seven
// eighths), a run's run_start and run_finish last. Every refusal is
// counted: one WARN per destination ("weft/otel: destination queue
// full"), the count on the heartbeat (weft.records.dropped, which
// Studio's ingest health shows), and the run's gap badge, which counts
// the events lost against weft.run.events. Local has no queue (it writes
// synchronously) and ignores it. The OTEL_BLRP_MAX_QUEUE_SIZE and
// OTEL_BSP_MAX_QUEUE_SIZE variables do not apply to weft's
// destinations.
func QueueSize(n int) DestOption {
	return destOptionFunc(func(d *dest) { d.queueSize = n })
}

// Blocking makes a full queue wait for room instead of refusing a
// durable record — up to 5 s per record and never past the emitting
// run's context; deltas are still shed. Development only, like
// RawBodies: it takes effect only when WEFT_ENV is "dev" when the
// pipeline starts; otherwise (and always under NoEnv) Start logs one
// WARN ("otel: Blocking ignored: WEFT_ENV is not dev") and the
// destination refuses as without it — observability never slows a
// production app. Local ignores it (no queue).
func Blocking() DestOption {
	return destOptionFunc(func(d *dest) { d.blocking = true })
}
