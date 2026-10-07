package otel

import (
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
)

// destsOf collects what destination options build — S2.2's defaults
// table, pinned field by field.
func destsOf(opts ...Option) []dest {
	cfg := config{heartbeat: defaultHeartbeat}
	for _, o := range opts {
		o.apply(&cfg)
	}
	return cfg.dests
}

func mustDest(t *testing.T, opts ...Option) dest {
	t.Helper()
	ds := destsOf(opts...)
	if len(ds) != 1 {
		t.Fatalf("options built %d destinations, want 1", len(ds))
	}
	return ds[0]
}

// Local: in-process obsdb write, traces + logs, content ON, simple
// (synchronous) processors — the last is pinned behaviourally in the S7
// tests (a record is stored before Emit returns).
func TestDefaultLocal(t *testing.T) {
	d := mustDest(t, Local("x.db"))
	if d.kind != destLocal || !d.traces || !d.logs || !d.contentOn() {
		t.Errorf("local defaults = %+v", d)
	}
}

// Studio: OTLP/HTTP protobuf with a bearer token, traces + logs,
// content ON, batch logs 200 ms / spans 1 s.
func TestDefaultStudio(t *testing.T) {
	d := mustDest(t, Studio("https://studio.example", "tok"))
	if !d.traces || !d.logs || !d.contentOn() {
		t.Errorf("studio defaults = %+v", d)
	}
	logs, spans := d.batchDelays()
	if logs != 200*time.Millisecond || spans != time.Second {
		t.Errorf("studio batch delays = %v/%v, want 200ms/1s", logs, spans)
	}
	endpoint, path, headers, insecure, err := transportOf(d)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "studio.example" || path != "" || headers["Authorization"] != "Bearer tok" || insecure {
		t.Errorf("studio transport = %q %q %v %v", endpoint, path, headers, insecure)
	}
}

// Datadog: the local Agent's OTLP intake on http://localhost:4318,
// traces + logs, content OFF, SDK-default batching (zero delays).
// Endpoint re-checked against Datadog's docs (the Agent binds 4317/4318
// when OTLP is on) and pinned here.
func TestDefaultDatadog(t *testing.T) {
	d := mustDest(t, Datadog())
	if !d.traces || !d.logs || d.contentOn() {
		t.Errorf("datadog defaults = %+v", d)
	}
	if logs, spans := d.batchDelays(); logs != 0 || spans != 0 {
		t.Errorf("datadog batch delays = %v/%v, want SDK defaults", logs, spans)
	}
	endpoint, _, _, insecure, err := transportOf(d)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "localhost:4318" || !insecure {
		t.Errorf("datadog transport = %q insecure=%v, want localhost:4318 over http", endpoint, insecure)
	}
	// DatadogEndpoint overrides the intake.
	d2 := mustDest(t, Datadog(DatadogEndpoint("http://dd-agent.internal:4318"), Insecure()))
	if ep, _, _, _, err := transportOf(d2); err != nil || ep != "dd-agent.internal:4318" {
		t.Errorf("datadog override = %q %v", ep, err)
	}
}

// Langfuse: <host>/api/public/otel with Basic auth base64(pk:sk), traces
// ONLY, content off. Endpoint re-checked against Langfuse's docs
// (/api/public/otel/v1/traces, Basic auth, OTLP/HTTP only) and pinned
// here; span-level content is F1, post-v1.
func TestDefaultLangfuse(t *testing.T) {
	d := mustDest(t, Langfuse("https://cloud.langfuse.com", "pk-lf", "sk-lf"))
	if !d.traces || d.logs || d.contentOn() {
		t.Errorf("langfuse defaults = %+v (traces only, content off)", d)
	}
	endpoint, path, headers, insecure, err := transportOf(d)
	if err != nil {
		t.Fatal(err)
	}
	wantAuth := "Basic " + base64Std("pk-lf:sk-lf")
	if endpoint != "cloud.langfuse.com" || path != "/api/public/otel" ||
		headers["Authorization"] != wantAuth || insecure {
		t.Errorf("langfuse transport = %q %q %v", endpoint, path, headers)
	}
}

// OTLP: any endpoint, traces + logs, content OFF; http to a
// non-loopback host needs Insecure() or the build fails loudly.
func TestDefaultOTLP(t *testing.T) {
	d := mustDest(t, OTLP("https://collector:4318"))
	if !d.traces || !d.logs || d.contentOn() {
		t.Errorf("otlp defaults = %+v", d)
	}
	if _, _, _, insecure, err := transportOf(d); err != nil || insecure {
		t.Errorf("https otlp = insecure %v err %v", insecure, err)
	}
	plain := mustDest(t, OTLP("http://collector:4318"))
	if _, _, _, _, err := transportOf(plain); err == nil {
		t.Error("http to a non-loopback host without Insecure() built")
	}
	allowed := mustDest(t, OTLP("http://127.0.0.1:4318"))
	if _, _, _, insecure, err := transportOf(allowed); err != nil || !insecure {
		t.Errorf("loopback http = %v %v, want allowed insecure", insecure, err)
	}
	forced := mustDest(t, OTLP("http://collector:4318", Insecure()))
	if _, _, _, insecure, err := transportOf(forced); err != nil || !insecure {
		t.Errorf("explicit Insecure() = %v %v", insecure, err)
	}
}

// Exporters: whichever is non-nil; content off; nil spans turns traces
// off by construction.
func TestDefaultExporters(t *testing.T) {
	d := mustDest(t, Exporters(nil, nil))
	if d.traces || d.logs || d.contentOn() {
		t.Errorf("nil exporters = %+v, want both signals off", d)
	}
}

// DestOptions shape their destination: WithContent/NoContent override
// the kind default; Signals; NoDeltas; BatchDelay; Headers; Timeout.
func TestDestOptions(t *testing.T) {
	d := mustDest(t, OTLP("https://c:4318", WithContent(), NoDeltas(), BatchDelay(time.Second), Headers(map[string]string{"x": "y"}), Timeout(2*time.Second)))
	if !d.contentOn() || !d.noDeltas || d.batchDelay != time.Second ||
		d.headers["x"] != "y" || d.timeout != 2*time.Second {
		t.Errorf("shaped otlp = %+v", d)
	}
	l := mustDest(t, Local("p", NoContent(), Signals(false, true)))
	if l.contentOn() || l.traces || !l.logs {
		t.Errorf("shaped local = %+v", l)
	}
	// WithContent can carry its own ContentConfig.
	c := ContentConfig{MaxBytes: 7}
	d2 := mustDest(t, Exporters(nil, nil, WithContent(c)))
	if !d2.contentOn() || d2.contentCfg == nil || d2.contentCfg.MaxBytes != 7 {
		t.Errorf("WithContent(cfg) = %+v", d2)
	}
}

// Environment destinations (S2.3): the standard variables configure an
// extra destination; NoEnv turns them off; two destinations on one URL
// de-duplicate with the explicit one winning.
func TestEnvDestinations(t *testing.T) {
	t.Setenv("WEFT_STUDIO_URL", "https://studio.example")
	t.Setenv("WEFT_STUDIO_TOKEN", "tok")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://collector:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "k=v,a=b")
	env := envDestinations(envGetenv)
	if len(env) != 2 {
		t.Fatalf("env built %d destinations, want 2", len(env))
	}
	var studio, otlpD dest
	for _, d := range env {
		switch d.kind {
		case destStudio:
			studio = d
		case destOTLP:
			otlpD = d
		}
	}
	if studio.url != "https://studio.example" || studio.token != "tok" || !studio.contentOn() {
		t.Errorf("env studio = %+v (content on)", studio)
	}
	if otlpD.url != "https://collector:4318" || otlpD.contentOn() ||
		otlpD.headers["k"] != "v" || otlpD.headers["a"] != "b" {
		t.Errorf("env otlp = %+v (content off, headers parsed)", otlpD)
	}

	// The GenAI capture variable turns content on for the env OTLP.
	t.Setenv("OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT", "true")
	if d := envDestinations(envGetenv); !d[1].contentOn() {
		t.Errorf("capture variable: %+v", d[1])
	}

	// A schemeless endpoint reads as https (the OTLP convention).
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "collector:4318")
	t.Setenv("OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT", "")
	if d := envDestinations(envGetenv); d[1].url != "https://collector:4318" {
		t.Errorf("schemeless endpoint = %q", d[1].url)
	}
}

// The de-duplication rule: an explicit destination wins over an
// environment one on the same URL.
func TestEnvDedup(t *testing.T) {
	t.Setenv("WEFT_STUDIO_URL", "https://studio.example")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://collector:4318")
	explicit := destsOf(Studio("https://studio.example", "real"), OTLP("https://collector:4318", WithContent()))
	env := envDestinations(envGetenv)
	kept := dedupe(explicit, env)
	if len(kept) != 0 {
		t.Errorf("dedupe kept %+v; explicit destinations win", kept)
	}
	other := destsOf(OTLP("https://elsewhere:4318"))
	if kept := dedupe(other, env); len(kept) != 2 {
		t.Errorf("dedupe dropped unrelated env destinations: %+v", kept)
	}
}

// buildResource precedence (S2.1:899): the caller's Resource merges
// over the detected base, the Service option over the environment, and
// the environment over the binary-name fallback — Merge's second
// argument wins, the SDK's own WithResource order
// (sdk/trace/provider.go: Merge(resource.Environment(), r)). With the
// merge inverted the detected base overwrote every explicit attribute.
func TestBuildResourcePrecedence(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "svc-from-env")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=prod")

	// (c) Without either option the env name and the env attributes
	// apply over the binary-name fallback.
	base, err := buildResource(config{})
	if err != nil {
		t.Fatal(err)
	}
	if got := resAttr(base, "service.name"); got != "svc-from-env" {
		t.Errorf("(c) service.name = %q, want the env name", got)
	}
	if got := resAttr(base, "deployment.environment"); got != "prod" {
		t.Errorf("(c) deployment.environment = %q, want the env attributes kept", got)
	}

	// (a) Service wins over the environment; the env attributes stay.
	svcCfg := config{}
	Service("explicit").apply(&svcCfg)
	svc, err := buildResource(svcCfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := resAttr(svc, "service.name"); got != "explicit" {
		t.Errorf("(a) service.name = %q, want the Service option's", got)
	}
	if got := resAttr(svc, "deployment.environment"); got != "prod" {
		t.Errorf("(a) deployment.environment = %q, want the env attributes kept", got)
	}

	// (b) Resource wins over both.
	topCfg := config{}
	Service("explicit").apply(&topCfg)
	Resource(sdkresource.NewSchemaless(
		attribute.String("service.name", "from-resource"),
	)).apply(&topCfg)
	top, err := buildResource(topCfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := resAttr(top, "service.name"); got != "from-resource" {
		t.Errorf("(b) service.name = %q, want the Resource option's", got)
	}

	// (d) weft.version rides on every shape. The literal is the pin:
	// the release step bumps weftVersion() with the tag and this line
	// must follow it (the 0.7.0 release missed the bump — the audits'
	// P0-3).
	for name, r := range map[string]*sdkresource.Resource{
		"base": base, "service": svc, "resource": top,
	} {
		if got := resAttr(r, "weft.version"); got != "v0.9.0" {
			t.Errorf("(d) %s: weft.version = %q, want v0.8.0", name, got)
		}
	}
}

// resAttr reads one string attribute off a resource.
func resAttr(r *sdkresource.Resource, key string) string {
	if v, ok := r.Set().Value(attribute.Key(key)); ok {
		return v.AsString()
	}
	return ""
}

// The pipeline's Enabled question: a provider whose every destination is
// content-off answers false for weft.messages — the core then emits no
// messages records (the Enabled rule, behaviourally in the S7 tests).
// A single content-on destination flips it true.
func TestPipelineEnabledRule(t *testing.T) {
	p, err := Start(testCtx(t), NoGlobal(),
		Exporters(nil, newMemExporter()),
		OTLP("https://collector:4318"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(testCtx(t)) }()
	lg := p.LoggerProvider().Logger(instrumentationName)
	if lg.Enabled(testCtx(t), enabledParams("weft.messages")) {
		t.Error("content-off-only pipeline answers Enabled(weft.messages) true")
	}
	if !lg.Enabled(testCtx(t), enabledParams("weft.event")) {
		t.Error("content-off pipeline answers Enabled(weft.event) false")
	}

	p2, err := Start(testCtx(t), NoGlobal(),
		Exporters(nil, newMemExporter()),
		OTLP("https://collector:4318", WithContent()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p2.Shutdown(testCtx(t)) }()
	if !p2.LoggerProvider().Logger(instrumentationName).Enabled(testCtx(t), enabledParams("weft.messages")) {
		t.Error("one content-on destination must make Enabled(weft.messages) true")
	}
}
