package otel

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// pathRecorder is an OTLP endpoint that records the path and the
// Authorization header of every request it receives.
type pathRecorder struct {
	mu   sync.Mutex
	hits []string
}

func (p *pathRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.hits = append(p.hits, r.Method+" "+r.URL.Path+" auth="+r.Header.Get("Authorization"))
	p.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (p *pathRecorder) paths() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := slices.Clone(p.hits)
	slices.Sort(out)
	return slices.Compact(out)
}

// runThrough runs one scripted agent through a pipeline built from opts
// and shuts it down, so every destination has exported.
func runThrough(t *testing.T, opts ...Option) {
	t.Helper()
	p, err := Start(testCtx(t), append([]Option{NoGlobal(), Heartbeat(0)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	agt := weft.New(wefttest.Script(wefttest.Say("wire")),
		weft.TracerProvider(p.TracerProvider()), weft.LoggerProvider(p.LoggerProvider()))
	if _, err := agt.Generate(context.Background(), weft.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// Every OTLP-family destination exports to exactly the path its base
// URL names plus /v1/traces and /v1/logs — on the wire, against a real
// HTTP server — and carries its own credentials only.
func TestDestinationsHitTheirPathsOnTheWire(t *testing.T) {
	for _, c := range []struct {
		name string
		opts func(base string) []Option
		env  map[string]string
		want []string
	}{
		{"studio at the root", func(b string) []Option { return []Option{NoEnv(), Studio(b, "tok")} }, nil,
			[]string{"POST /v1/logs auth=Bearer tok", "POST /v1/traces auth=Bearer tok"}},
		{"studio mounted under a prefix", func(b string) []Option { return []Option{NoEnv(), Studio(b+"/studio/", "tok")} }, nil,
			[]string{"POST /studio/v1/logs auth=Bearer tok", "POST /studio/v1/traces auth=Bearer tok"}},
		{"otlp with a base path", func(b string) []Option { return []Option{NoEnv(), OTLP(b + "/otlp")} }, nil,
			[]string{"POST /otlp/v1/logs auth=", "POST /otlp/v1/traces auth="}},
		{"datadog endpoint", func(b string) []Option { return []Option{NoEnv(), Datadog(DatadogEndpoint(b))} }, nil,
			[]string{"POST /v1/logs auth=", "POST /v1/traces auth="}},
		{"langfuse, traces only", func(b string) []Option { return []Option{NoEnv(), Langfuse(b, "pk", "sk")} }, nil,
			[]string{"POST /api/public/otel/v1/traces auth=Basic cGs6c2s="}},
		{"env otlp with a base path", func(string) []Option { return nil }, map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "/collector"},
			[]string{"POST /collector/v1/logs auth=", "POST /collector/v1/traces auth="}},
		{"env studio", func(string) []Option { return nil }, map[string]string{"WEFT_STUDIO_URL": "", "WEFT_STUDIO_TOKEN": "envtok"},
			[]string{"POST /v1/logs auth=Bearer envtok", "POST /v1/traces auth=Bearer envtok"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := &pathRecorder{}
			srv := httptest.NewServer(rec)
			defer srv.Close()
			prev := envGetenv
			defer func() { envGetenv = prev }()
			envGetenv = func(k string) string {
				v, ok := c.env[k]
				if !ok {
					return ""
				}
				if k == "OTEL_EXPORTER_OTLP_ENDPOINT" || k == "WEFT_STUDIO_URL" {
					return srv.URL + v
				}
				return v
			}
			runThrough(t, c.opts(srv.URL)...)
			if got := rec.paths(); !slices.Equal(got, c.want) {
				t.Errorf("requests = %q\nwant       %q", got, c.want)
			}
		})
	}
}

// Install's WARN-and-skip: destinations that cannot be built are
// skipped, the others record, and the WARN names neither a header nor
// a URL's credentials.
func TestInstallSkipsOnlyTheBrokenDestinations(t *testing.T) {
	buf := &threadSafeBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	defer slog.SetDefault(prev)

	shutdown := Install(NoEnv(), Heartbeat(0),
		OTLP("http://collector.invalid:4318", Headers(map[string]string{"Authorization": "Bearer hdr-secret"})),
		Studio("ftp://user:url-secret@studio.invalid", "tok-secret"),
		Local(filepath.Join(t.TempDir(), "kept.db")),
	)
	db := LocalDB()
	if db == nil {
		shutdown()
		t.Fatal("a broken destination disabled the Local sink")
	}
	agt := weft.New(wefttest.Script(wefttest.Say("kept")))
	res, err := agt.Generate(context.Background(), weft.Prompt("go"))
	if err != nil {
		shutdown()
		t.Fatal(err)
	}
	if _, err := db.Run(context.Background(), res.ID); err != nil {
		shutdown()
		t.Fatalf("the run did not reach the Local sink: %v", err)
	}
	shutdown()
	out := buf.String()
	if strings.Count(out, "destination skipped") != 2 {
		t.Errorf("want one WARN per broken destination, got: %s", out)
	}
	for _, secret := range []string{"hdr-secret", "url-secret", "tok-secret"} {
		if strings.Contains(out, secret) {
			t.Errorf("the WARN leaks %s: %s", secret, out)
		}
	}
}

// A panicking Redact must not put the content it was redacting into the
// process's logs through the drop WARN: a redactor's panic value is
// commonly built from its input (a failed match, a bad pattern applied
// to the text), and the WARN is exactly where Redact's caller does not
// expect content.
func TestRedactPanicWarnCarriesNoContent(t *testing.T) {
	buf := &threadSafeBuffer{}
	drops := newDropCounter("test")
	drops.log = slog.New(slog.NewTextHandler(buf, nil))
	p := &destProc{
		name: "test", inner: sdklog.NewSimpleProcessor(newMemExporter()), content: true,
		contentC: ContentConfig{Redact: func(_ weft.ContentKind, s string) string {
			panic("cannot redact " + s)
		}},
		drops: drops,
	}
	_ = p.OnEmit(context.Background(), sdkRecordWith(t, "weft.event",
		`{"type":"tool_finish","run_id":"r","seq":1,"call_id":"c","name":"pay","content":"card 4111"}`,
		attribute.String("weft.record", "event"), attribute.String("weft.content", "full")))
	if out := buf.String(); strings.Contains(out, "4111") {
		t.Errorf("the drop WARN carries the content Redact panicked on: %s", out)
	} else if !strings.Contains(out, "redaction panicked") {
		t.Errorf("no WARN for the panicking redactor: %q", out)
	}
}
