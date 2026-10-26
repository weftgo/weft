package otel

import (
	"time"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// config is what the options built. The zero config plus Start's
// defaults is the whole pipeline: default content 32 KiB, default
// heartbeat 10 s, default sampler ParentBased(AlwaysOn).
type config struct {
	dests      []dest
	content    ContentConfig
	service    string
	resource   *sdkresource.Resource
	heartbeat  time.Duration // Start seeds the 10s default; <= 0 = disabled
	sampler    sdktrace.Sampler
	noEnv      bool
	noGlobal   bool
	dropFailed bool // Install's WARN-and-skip mode
	// rawBodies is RawBodies' ask; Start keeps it only when the dev
	// gate holds (devGate), else one WARN and off.
	rawBodies bool
	// devDetail is DevDetail's ask; Start keeps it only when the dev
	// gate holds, like rawBodies.
	devDetail bool
	// devMode is DevMode's attestation: the dev gate holds without
	// WEFT_ENV=dev — under NoEnv, or with WEFT_ENV unset (an explicit
	// other value wins).
	devMode bool
	// noDiscovery is NoDiscovery's ask: skip the discovery file alone
	// (the rest of the environment is still read).
	noDiscovery bool
	// slog is Slog's ask: wrap slog.Default with the bridge once the
	// globals are registered (never under NoGlobal).
	slog bool
}

type destKind int

const (
	destLocal destKind = iota
	destStudio
	destDatadog
	destLangfuse
	destOTLP
	destExporters
)

// dest is one destination's configuration before Start builds it.
type dest struct {
	kind destKind
	name string // the drop-counter and WARN label

	path                       string                // Local
	url, token                 string                // Studio; url also OTLP/DatadogEndpoint
	host, publicKey, secretKey string                // Langfuse
	spanExp                    sdktrace.SpanExporter // Exporters
	logExp                     sdklog.Exporter       // Exporters
	content                    *bool                 // nil = the kind's default
	contentCfg                 *ContentConfig
	traces, logs               bool
	noDeltas                   bool
	batchDelay                 time.Duration
	headers                    map[string]string
	timeout                    time.Duration
	insecure                   bool
	// queueSize is QueueSize's ask (0: the kind's default, queueSizeOf);
	// blocking is Blocking's, kept by Start only under the dev gate
	// (devGate: WEFT_ENV=dev read without NoEnv, or DevMode).
	queueSize int
	blocking  bool
	// appLogs is AppLogs' ask (nil: the kind's default, appLogsOn).
	appLogs *bool
}

func newDest(kind destKind, name string) dest {
	return dest{kind: kind, name: name, traces: true, logs: true}
}

func (d *dest) apply(opts []DestOption) {
	for _, o := range opts {
		o.applyDest(d)
	}
}

// contentDefault is S2.2's table: Local and Studio on, everything else
// off — content never reaches a third party by accident.
func (d dest) contentDefault() bool {
	switch d.kind {
	case destLocal, destStudio:
		return true
	default:
		return false
	}
}

// rawOn is whether this destination takes raw records (ADR 0028 §12)
// when the pipeline's RawBodies is in force: Local and Studio with
// content on, never a third party whatever its content setting.
func (d dest) rawOn() bool {
	return (d.kind == destLocal || d.kind == destStudio) && d.contentOn()
}

// appLogsOn is whether this destination takes the slog bridge's app
// log records (AppLogs): AppLogs' answer when given, else Local and
// Studio with content on — an app log may carry anything, prompts
// included, so a third party never gets one by default.
func (d dest) appLogsOn() bool {
	if d.appLogs != nil {
		return *d.appLogs
	}
	return (d.kind == destLocal || d.kind == destStudio) && d.contentOn()
}

func (d dest) contentOn() bool {
	if d.content != nil {
		return *d.content
	}
	return d.contentDefault()
}

// batchDelays returns the log and span batch intervals: Studio's short
// pair (200 ms / 1 s), an explicit BatchDelay for both, else zero (the
// SDK's defaults).
func (d dest) batchDelays() (logs, spans time.Duration) {
	if d.batchDelay != 0 {
		return d.batchDelay, d.batchDelay
	}
	if d.kind == destStudio {
		return 200 * time.Millisecond, time.Second
	}
	return 0, 0
}

// queueSizeOf is the destination's batch queue size, for its log and
// its span chain each: QueueSize's when given, else 16,384 for Studio
// (a dev loop's storm fits) and the SDK's 2,048 for the rest. Local has
// no queue (written synchronously).
func (d dest) queueSizeOf() int {
	switch {
	case d.queueSize > 0:
		return d.queueSize
	case d.kind == destStudio:
		return defaultStudioQueueSize
	default:
		return defaultQueueSize
	}
}

// datadogDefaultURL is the Datadog Agent's OTLP/HTTP intake.
const datadogDefaultURL = "http://localhost:4318"

// defaultLocalPath is where the Local sink writes when no path is
// given: $WEFT_DB or ./.weft/weft.db.
func defaultLocalPath(getenv func(string) string) string {
	if p := getenv("WEFT_DB"); p != "" {
		return p
	}
	return ".weft/weft.db"
}
