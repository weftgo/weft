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
	heartbeat  time.Duration // 0 = the 10s default; <0 = disabled
	sampler    sdktrace.Sampler
	noEnv      bool
	noGlobal   bool
	dropFailed bool // Install's WARN-and-skip mode
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

// defaultLocalPath is where the Local sink writes when no path is
// given: $WEFT_DB or ./.weft/weft.db.
func defaultLocalPath(getenv func(string) string) string {
	if p := getenv("WEFT_DB"); p != "" {
		return p
	}
	return ".weft/weft.db"
}
