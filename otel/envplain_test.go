package otel

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

// clearEnvDestinations blanks every variable envDestinations reads, so a
// test sees only the ones it sets.
func clearEnvDestinations(t *testing.T) {
	t.Helper()
	for _, k := range []string{"WEFT_STUDIO_URL", "WEFT_STUDIO_TOKEN", "OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_HEADERS", "OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT"} {
		t.Setenv(k, "")
	}
}

// nonLoopbackName is a host form of the test server's 127.0.0.1 address
// that transportOf does not read as loopback (the IPv4-mapped IPv6
// literal): it reaches the plaintext test server while taking the
// non-loopback branch of the TLS guard, as http://otel-collector:4318
// does in a docker-compose network.
func nonLoopbackName(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	host := "[::ffff:127.0.0.1]:" + port
	conn, err := net.Dial("tcp", host)
	if err != nil {
		t.Skipf("cannot reach the test server as %s: %v", host, err)
	}
	_ = conn.Close()
	return host
}

// An http:// OTEL_EXPORTER_OTLP_ENDPOINT is the operator's plaintext
// opt-in (the OpenTelemetry-standard meaning; the sidecar-collector
// setup): records and spans reach a plaintext collector on a
// non-loopback host, on the standard paths. Pre-fix the destination was
// refused: `http:// to non-loopback … needs Insecure()`.
func TestEnvHTTPEndpointIsPlaintextOptIn(t *testing.T) {
	clearEnvDestinations(t)
	rec := &pathRecorder{}
	srv := httptest.NewServer(rec)
	defer srv.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://"+nonLoopbackName(t, srv))

	runThrough(t)
	got := rec.paths()
	for _, want := range []string{"POST /v1/logs auth=", "POST /v1/traces auth="} {
		if !slices.Contains(got, want) {
			t.Errorf("the plaintext collector did not receive %q; got %v", want, got)
		}
	}
}

// The opt-in is the environment variable's alone: a code-configured
// OTLP("http://non-loopback") without Insecure() is still refused, and so
// is an http:// WEFT_STUDIO_URL to a non-loopback host (the Studio
// destination carries a bearer token; plaintext takes Insecure() in
// code).
func TestPlaintextGuardStaysForCodeAndStudio(t *testing.T) {
	clearEnvDestinations(t)
	_, err := Start(testCtx(t), NoGlobal(), NoEnv(), Heartbeat(0), OTLP("http://otel-collector:4318"))
	if err == nil || !strings.Contains(err.Error(), "needs Insecure()") {
		t.Errorf("code OTLP(http://non-loopback) without Insecure: err = %v, want the refusal", err)
	}

	t.Setenv("WEFT_STUDIO_URL", "http://studio.internal:7331")
	t.Setenv("WEFT_STUDIO_TOKEN", "tok")
	_, err = Start(testCtx(t), NoGlobal(), Heartbeat(0))
	if err == nil || !strings.Contains(err.Error(), "needs Insecure()") {
		t.Errorf("env WEFT_STUDIO_URL=http://non-loopback: err = %v, want the refusal", err)
	}

	// An env OTLP endpoint without a scheme stays https (the OTLP
	// convention): no plaintext without the operator writing http://.
	t.Setenv("WEFT_STUDIO_URL", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "otel-collector:4318")
	if d := envDestinations(envGetenv); len(d) != 1 || d[0].insecure {
		t.Errorf("schemeless env endpoint = %+v, want https (not insecure)", d)
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "HTTP://otel-collector:4318")
	if d := envDestinations(envGetenv); len(d) != 1 || !d[0].insecure {
		t.Errorf("HTTP:// env endpoint = %+v, want the plaintext opt-in", d)
	}
}

// Install with only environment destinations, none of which builds,
// falls back to the local sink with one WARN — the run is recorded.
// Pre-fix nothing was installed (errAllFailed) and LocalDB() was nil.
// Start keeps returning the error; a program that named a destination
// gets no fallback (TestInstallSkipsBrokenDestinations).
func TestInstallFallsBackToLocalWhenEnvDestinationsFail(t *testing.T) {
	clearEnvDestinations(t)
	t.Setenv("WEFT_DB", filepath.Join(t.TempDir(), "fallback.db"))
	t.Setenv("WEFT_STUDIO_URL", "http://studio.internal:7331") // plaintext + token: refused
	t.Setenv("WEFT_STUDIO_TOKEN", "tok")

	if _, err := Start(testCtx(t), NoGlobal(), Heartbeat(0)); err == nil {
		t.Fatal("Start with only a broken env destination succeeded; want the error")
	}

	buf := &threadSafeBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	defer slog.SetDefault(prev)

	shutdown := Install(Heartbeat(0))
	defer shutdown()
	db := LocalDB()
	if db == nil {
		t.Fatalf("Install recorded nothing: no local fallback after every env destination failed; log: %s", buf.String())
	}
	res, err := weft.New(wefttest.Script(wefttest.Say("kept"))).Generate(context.Background(), weft.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Run(context.Background(), res.ID); err != nil {
		t.Fatalf("the run did not reach the fallback local sink: %v", err)
	}
	if out := buf.String(); strings.Count(out, "falling back to the local sink") != 1 {
		t.Errorf("want one fallback WARN, got: %s", out)
	}

	// A named destination that fails gets no fallback.
	shutdown()
	shutdown2 := Install(NoEnv(), Heartbeat(0), OTLP("http://otel-collector:4318"))
	defer shutdown2()
	if LocalDB() != nil {
		t.Error("Install handed a program that named its destinations an unrequested local sink")
	}
}

// The plaintext opt-in does not reach an https:// code destination on
// another URL: it stays TLS (TestEnvEndpointDoesNotDowngradeTLS pins the
// wire), and the env destination's headers stay its own.
func TestEnvPlaintextOptInStaysOnTheEnvDestination(t *testing.T) {
	clearEnvDestinations(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://otel-collector:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "x-env=1")
	var cfg config
	for _, o := range []Option{Studio("https://studio.example", "tok"), OTLP("https://other.example")} {
		o.apply(&cfg)
	}
	for _, d := range cfg.dests {
		_, _, headers, insecure, err := transportOf(d)
		if err != nil {
			t.Fatal(err)
		}
		if insecure {
			t.Errorf("%s: an https destination became plaintext", d.name)
		}
		if _, ok := headers["x-env"]; ok {
			t.Errorf("%s: carries the env headers", d.name)
		}
	}
	env := envDestinations(envGetenv)
	_, _, _, insecure, err := transportOf(env[0])
	if err != nil || !insecure {
		t.Errorf("env destination: insecure=%v err=%v, want the plaintext opt-in", insecure, err)
	}
}

var _ http.Handler = (*pathRecorder)(nil)
