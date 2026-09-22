package google

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/weftgo/weft"
	"google.golang.org/genai"
)

// captureServer serves a recorded SSE fixture and captures the request
// body and headers the adapter sent — the escape hatch's tests assert
// on exactly the bytes that reached the wire.
func captureServer(t *testing.T, fixture string) (srv *httptest.Server, body func() string, header func() http.Header) {
	t.Helper()
	sse, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var gotBody string
	var gotHeader http.Header
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		gotBody = string(b)
		gotHeader = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(sse)
	}))
	t.Cleanup(srv.Close)
	return srv, func() string { return gotBody }, func() http.Header { return gotHeader }
}

func atModel(t *testing.T, srv *httptest.Server, opts ...Option) weft.Model {
	t.Helper()
	c, err := genaiClient(t.Context(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return Model("m", append([]Option{Client(c)}, opts...)...)
}

// genaiClient is the test double client every google test builds; the
// wrapper keeps the escape-hatch tests on the injected path.
func genaiClient(ctx context.Context, url string) (*genai.Client, error) {
	return genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:      "test",
		HTTPOptions: genai.HTTPOptions{BaseURL: url},
	})
}

func TestExtraBodyAndHeaders(t *testing.T) {
	h := http.Header{"X-Weft-Test": []string{"yes"}}
	srv, body, header := captureServer(t, "testdata/text_only.sse")
	m := atModel(t, srv,
		Temperature(0.5),
		ExtraBody(map[string]any{
			"temperature":   0.123,                                       // colliding scalar: caller wins (the SDK's recursiveMapMerge)
			"x_nested":      map[string]any{"a": map[string]any{"b": 1}}, // nested map: carried whole
			"x_vendor_knob": true,                                        // new key: added
		}),
		ExtraHeaders(h),
	)
	if _, err := weft.New(m).Generate(t.Context(), weft.Prompt("hi")); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body()), &payload); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, body())
	}
	if got := payload["temperature"]; got != 0.123 {
		t.Errorf("temperature = %v, want 0.123 (caller wins)", got)
	}
	if got := payload["x_vendor_knob"]; got != true {
		t.Errorf("x_vendor_knob = %v, want true (new key added)", got)
	}
	nested, _ := payload["x_nested"].(map[string]any)
	if nested == nil {
		t.Errorf("x_nested = %v, want the nested map", payload["x_nested"])
	}
	if got := header().Get("X-Weft-Test"); got != "yes" {
		t.Errorf("X-Weft-Test = %q, want passthrough", got)
	}
}

// Neither option set: the body carries no extra keys and no extra
// headers — v0.2.0's bytes (the P3 default-bytes pin).
func TestExtraBodyDefaultBytes(t *testing.T) {
	srv, body, header := captureServer(t, "testdata/text_only.sse")
	m := atModel(t, srv)
	if _, err := weft.New(m).Generate(t.Context(), weft.Prompt("hi")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body(), "x_vendor_knob") || strings.Contains(body(), "x_nested") {
		t.Errorf("default body carries extra keys: %s", body())
	}
	if header().Get("X-Weft-Test") != "" {
		t.Error("default request carries a custom header")
	}
}

// The options snapshot what they are given: mutating the maps or header
// values after Model returns never reaches the request — a Model is
// safe to hand to concurrent runs while the caller keeps its config
// alive (ADR 0013's immutable-Model stance; the 2026-09-22 review's
// reference-capture fix).
func TestExtraBodySnapshot(t *testing.T) {
	nested := map[string]any{"custom": 1}
	fields := map[string]any{"x_vendor_knob": nested}
	h := http.Header{"X-Weft-Test": []string{"yes"}}
	srv, body, header := captureServer(t, "testdata/text_only.sse")
	m := atModel(t, srv, ExtraBody(fields), ExtraHeaders(h))
	nested["custom"] = 2       // caller mutates after construction
	h["X-Weft-Test"][0] = "no" // and the header slice
	if _, err := weft.New(m).Generate(t.Context(), weft.Prompt("hi")); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body()), &payload); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, body())
	}
	knob, _ := payload["x_vendor_knob"].(map[string]any)
	if knob == nil || knob["custom"] != float64(1) {
		t.Errorf("x_vendor_knob = %v, want the construction-time value", payload["x_vendor_knob"])
	}
	if got := header().Get("X-Weft-Test"); got != "yes" {
		t.Errorf("X-Weft-Test = %q, want the construction-time value", got)
	}
}
