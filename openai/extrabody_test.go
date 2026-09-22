package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	openaisdk "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/weftgo/weft"
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

func TestExtraBodyAndHeaders(t *testing.T) {
	h := http.Header{"X-Weft-Test": []string{"yes"}}
	srv, body, header := captureServer(t, "testdata/text_only.sse")
	c := openaisdk.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("test"))
	m := Model("m", Client(&c),
		Temperature(0.5),
		ExtraBody(map[string]any{
			"temperature":    0.123,                       // colliding scalar: caller wins
			"stream_options": map[string]any{"custom": 1}, // nested map: deep merge with weft's own
			"x_vendor_knob":  true,                        // new key: added
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
	so, _ := payload["stream_options"].(map[string]any)
	if so == nil || so["include_usage"] != true || so["custom"] != float64(1) {
		t.Errorf("stream_options = %v, want the deep merge (include_usage + custom)", payload["stream_options"])
	}
	if got := payload["x_vendor_knob"]; got != true {
		t.Errorf("x_vendor_knob = %v, want true (new key added)", got)
	}
	if got := header().Get("X-Weft-Test"); got != "yes" {
		t.Errorf("X-Weft-Test = %q, want passthrough", got)
	}
}

// Neither option set: the body carries no extra keys and no extra
// headers — v0.2.0's bytes (the P3 default-bytes pin).
func TestExtraBodyDefaultBytes(t *testing.T) {
	srv, body, header := captureServer(t, "testdata/text_only.sse")
	c := openaisdk.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("test"))
	m := Model("m", Client(&c))
	if _, err := weft.New(m).Generate(t.Context(), weft.Prompt("hi")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body(), "x_vendor_knob") || strings.Contains(body(), "custom") {
		t.Errorf("default body carries extra keys: %s", body())
	}
	if header().Get("X-Weft-Test") != "" {
		t.Error("default request carries a custom header")
	}
}
