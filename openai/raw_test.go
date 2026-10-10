package openai

// The wire capture (ADR 0028 §12, plan A6) for this adapter: under
// otel.RawBodies(true) and WEFT_ENV=dev a step's exchange round-trips
// through the local sink byte for byte, with no credential stored.
// (The anthropic adapter's raw_test.go holds the case-variant table.)

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/otel"

	oaisdk "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

func TestRawBodiesRoundTripLocalSink(t *testing.T) {
	t.Setenv("WEFT_ENV", "dev")
	t.Setenv("WEFT_DISCOVERY", "off")
	t.Setenv("WEFT_STUDIO_URL", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	sse, err := os.ReadFile("testdata/text_only.sse")
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu   sync.Mutex
		sent []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		sent = b
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Set-Cookie", "s=SECRET-cookie")
		_, _ = w.Write(sse)
	}))
	defer srv.Close()

	ctx := context.Background()
	dir := t.TempDir()
	p, err := otel.Start(ctx, otel.NoGlobal(), otel.Local(filepath.Join(dir, "weft.db")), otel.RawBodies(true))
	if err != nil {
		t.Fatal(err)
	}
	c := oaisdk.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("SECRET-openai"), option.WithMaxRetries(0))
	m := Model("gpt-test", Client(&c))
	agt := core.New(m, core.LoggerProvider(p.LoggerProvider()), core.TracerProvider(p.TracerProvider()))
	res, err := agt.Generate(ctx, core.Prompt("hi"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	reqs, err := p.LocalDB().Requests(ctx, res.ID, obsdb.RequestQuery{})
	if err != nil || len(reqs) != 1 || reqs[0].Wire == nil {
		t.Fatalf("requests = %+v, %v; want one with its raw record", reqs, err)
	}
	w := reqs[0].Wire
	mu.Lock()
	defer mu.Unlock()
	if w.Request.Body != string(sent) || w.Response == nil || w.Response.Body != string(sse) {
		t.Errorf("stored exchange differs from the wire:\nrequest %q\n   sent %q", w.Request.Body, sent)
	}
	for _, side := range []map[string][]string{w.Request.Headers, w.Response.Headers} {
		for k := range side {
			for _, s := range []string{"authorization", "x-goog-api-key", "api-key", "set-cookie", "cookie"} {
				if strings.EqualFold(k, s) {
					t.Errorf("header %q survived", k)
				}
			}
		}
	}
	if len(w.RedactedHeaders) == 0 {
		t.Error("redacted_headers names nothing")
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if bytes.Contains(b, []byte("SECRET")) {
			t.Errorf("a credential reached %s", e.Name())
		}
	}
}
