package anthropic

// Plan A6's Done line for the anthropic adapter: under
// otel.RawBodies(true) and WEFT_ENV=dev, a step's wire pair round-trips
// through the local sink byte for byte, and no credential the SDK (or
// a gateway in front of it) sent reaches any stored byte.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	antsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/otel"
)

// rawEnv is the development environment RawBodies needs, with every
// environment destination and the discovery file switched off, so the
// pipeline is exactly the test's Local sink.
func rawEnv(t *testing.T) {
	t.Setenv("WEFT_ENV", "dev")
	t.Setenv("WEFT_DISCOVERY", "off")
	t.Setenv("WEFT_STUDIO_URL", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
}

// wireServer serves the SSE fixture with response headers that include
// a cookie, and records the request exactly as it arrived.
type wireServer struct {
	*httptest.Server
	sse    []byte
	mu     sync.Mutex
	body   []byte
	header http.Header
}

func newWireServer(t *testing.T) *wireServer {
	t.Helper()
	sse, err := os.ReadFile("testdata/text_only.sse")
	if err != nil {
		t.Fatal(err)
	}
	ws := &wireServer{sse: sse}
	ws.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		ws.mu.Lock()
		ws.body, ws.header = b, r.Header.Clone()
		ws.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Request-Id", "req_wire_1")
		w.Header().Set("Set-Cookie", "session=SECRET-set-cookie")
		_, _ = w.Write(sse)
	}))
	t.Cleanup(ws.Close)
	return ws
}

// rawRound runs one step of an anthropic model on c under a Local sink
// with RawBodies, and returns the attempt's request record, the sink's
// file bytes (database and WAL, after shutdown) and the run id.
func rawRound(t *testing.T, c antsdk.Client) (obsdb.RequestRecord, []byte) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "weft.db")
	p, err := otel.Start(ctx, otel.NoGlobal(), otel.Local(path), otel.RawBodies(true))
	if err != nil {
		t.Fatal(err)
	}
	agt := core.New(Model("claude-test", Client(&c)),
		core.LoggerProvider(p.LoggerProvider()), core.TracerProvider(p.TracerProvider()))
	res, err := agt.Generate(ctx, core.Prompt("hi"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	reqs, err := p.LocalDB().Requests(ctx, res.ID, obsdb.RequestQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 {
		t.Fatalf("request records = %d, want 1", len(reqs))
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	var stored []byte
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		stored = append(stored, b...)
	}
	return reqs[0], stored
}

// stripList is ADR 0028 §12's header floor plus anthropic-sdk-go's own
// sensitive list.
var stripList = []string{
	"authorization", "proxy-authorization", "x-api-key", "api-key",
	"cookie", "set-cookie", "x-goog-api-key",
}

func assertNoStripped(t *testing.T, side string, h map[string][]string) {
	t.Helper()
	for k := range h {
		for _, s := range stripList {
			if strings.EqualFold(k, s) {
				t.Errorf("%s header %q survived", side, k)
			}
		}
	}
}

func TestRawBodiesRoundTripLocalSink(t *testing.T) {
	rawEnv(t)
	ws := newWireServer(t)
	c := antsdk.NewClient(option.WithBaseURL(ws.URL), option.WithAPIKey("SECRET-api-key"),
		option.WithAuthToken("SECRET-auth-token"), option.WithMaxRetries(0),
		option.WithHeader("Cookie", "c=SECRET-cookie"),
		option.WithHeader("Proxy-Authorization", "Basic SECRET-proxy"))
	rec, stored := rawRound(t, c)
	w := rec.Wire
	if w == nil {
		t.Fatal("the attempt's request record carries no raw record")
	}
	ws.mu.Lock()
	sent, got := ws.body, ws.header
	ws.mu.Unlock()
	if w.Request.Body != string(sent) || w.Request.Encoding != "" {
		t.Errorf("stored request body differs from what the server received:\n got %q\nwant %q", w.Request.Body, sent)
	}
	if w.Response == nil || w.Response.Body != string(ws.sse) {
		t.Fatalf("stored response body differs from what the server sent: %+v", w.Response)
	}
	if w.Request.Method != http.MethodPost || !strings.HasSuffix(w.Request.URL, "/v1/messages") ||
		w.Request.MediaType != "application/json" || w.Response.Status != 200 ||
		w.Response.MediaType != "text/event-stream" || w.Step != 0 || w.Attempt != 1 {
		t.Errorf("wire = %s %s (%s) → %d (%s), step %d attempt %d", w.Request.Method, w.Request.URL,
			w.Request.MediaType, w.Response.Status, w.Response.MediaType, w.Step, w.Attempt)
	}
	if w.Response.Headers["Request-Id"][0] != "req_wire_1" || w.Request.Headers["Anthropic-Version"] == nil {
		t.Errorf("kept headers = %v / %v", w.Request.Headers, w.Response.Headers)
	}
	assertNoStripped(t, "request", w.Request.Headers)
	assertNoStripped(t, "response", w.Response.Headers)
	for _, h := range []string{"authorization", "cookie", "proxy-authorization", "set-cookie", "x-api-key"} {
		listed := false
		for _, r := range w.RedactedHeaders {
			listed = listed || r == h
		}
		if !listed {
			t.Errorf("redacted_headers %v does not name %s", w.RedactedHeaders, h)
		}
	}
	// The server did receive the credentials: the capture stripped them
	// from the record, never from the request.
	if got.Get("X-Api-Key") != "SECRET-api-key" || got.Get("Authorization") != "Bearer SECRET-auth-token" {
		t.Errorf("the request lost its credentials: %v", got)
	}
	if bytes.Contains(stored, []byte("SECRET")) {
		t.Error("a credential reached the local sink's file")
	}
}

// Every case variant of every credential header the SDKs send — set on
// the wire by a client middleware outside the adapter's, so the
// non-canonical spellings reach the capture as written — is stripped
// from the stored record, its value in no stored byte.
func TestRawBodiesNoCredentialStored(t *testing.T) {
	rawEnv(t)
	for _, name := range []string{
		"authorization", "AUTHORIZATION", "Authorization", "X-API-Key", "x-api-key", "X-Api-Key",
		"api-key", "Api-Key", "Proxy-Authorization", "cookie", "COOKIE", "x-goog-api-key",
		"X-Goog-Api-Key", "X-Auth-Token", "X-Client-Secret",
	} {
		t.Run(name, func(t *testing.T) {
			ws := newWireServer(t)
			secret := "SECRET-" + strings.ToLower(name)
			c := antsdk.NewClient(option.WithBaseURL(ws.URL), option.WithAPIKey("SECRET-key"), option.WithMaxRetries(0),
				option.WithMiddleware(func(r *http.Request, next option.MiddlewareNext) (*http.Response, error) {
					r.Header[name] = []string{secret}
					return next(r)
				}))
			rec, stored := rawRound(t, c)
			if rec.Wire == nil {
				t.Fatal("no raw record")
			}
			assertNoStripped(t, "request", rec.Wire.Request.Headers)
			for k := range rec.Wire.Request.Headers {
				if strings.EqualFold(k, name) {
					t.Errorf("%q survived as %q", name, k)
				}
			}
			listed := false
			for _, r := range rec.Wire.RedactedHeaders {
				listed = listed || r == strings.ToLower(name)
			}
			if !listed {
				t.Errorf("redacted_headers %v does not name %q", rec.Wire.RedactedHeaders, name)
			}
			if bytes.Contains(stored, []byte(secret)) || bytes.Contains(stored, []byte("SECRET-key")) {
				t.Errorf("the value of %q reached the local sink's file", name)
			}
			b, _ := json.Marshal(rec)
			if bytes.Contains(b, []byte("SECRET")) {
				t.Errorf("the requests row carries a credential: %s", b)
			}
		})
	}
}

// Outside WEFT_ENV=dev the option is a no-op: the adapter's capture
// asks and is told no, the request goes out untouched, and the run
// holds no raw record.
func TestRawBodiesOffOutsideDev(t *testing.T) {
	rawEnv(t)
	t.Setenv("WEFT_ENV", "")
	ws := newWireServer(t)
	var asked, wanted bool
	c := antsdk.NewClient(option.WithBaseURL(ws.URL), option.WithAPIKey("k"), option.WithMaxRetries(0),
		option.WithMiddleware(func(r *http.Request, next option.MiddlewareNext) (*http.Response, error) {
			asked, wanted = true, core.ReportFromContext(r.Context()).WantsRaw()
			return next(r)
		}))
	rec, _ := rawRound(t, c)
	if !asked || wanted {
		t.Errorf("asked %v, WantsRaw %v; want the adapter told no", asked, wanted)
	}
	if rec.Wire != nil {
		t.Errorf("a raw record was stored outside dev: %+v", rec.Wire)
	}
}
