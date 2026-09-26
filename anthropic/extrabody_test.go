package anthropic

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	antsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
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
	c := antsdk.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("test"))
	m := Model("m", Client(&c),
		Temperature(0.5),
		ExtraBody(map[string]any{
			"temperature":   0.123,                        // colliding scalar: caller wins
			"metadata":      map[string]any{"user_id": 7}, // nested map: deep merge with a weft-level object shape
			"x_vendor_knob": true,                         // new key: added
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
	md, _ := payload["metadata"].(map[string]any)
	if md == nil || md["user_id"] != float64(7) {
		t.Errorf("metadata = %v, want the nested map", payload["metadata"])
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
	c := antsdk.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("test"))
	m := Model("m", Client(&c))
	if _, err := weft.New(m).Generate(t.Context(), weft.Prompt("hi")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body(), "x_vendor_knob") || strings.Contains(body(), "user_id") {
		t.Errorf("default body carries extra keys: %s", body())
	}
	if header().Get("X-Weft-Test") != "" {
		t.Error("default request carries a custom header")
	}
}

// Review 2026-09-24 §2.2: a header carrying several values (legal
// http.Header, and CloneHeaders deliberately preserves the whole
// slice) reaches the wire whole. option.WithHeader has Set semantics,
// so looping it per value kept only the last one — the verbatim-
// headers promise silently truncated.
func TestExtraHeadersMultiValued(t *testing.T) {
	h := http.Header{"X-Multi": []string{"a", "b"}}
	srv, _, header := captureServer(t, "testdata/text_only.sse")
	c := antsdk.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("test"))
	m := Model("m", Client(&c), ExtraHeaders(h))
	if _, err := weft.New(m).Generate(t.Context(), weft.Prompt("hi")); err != nil {
		t.Fatal(err)
	}
	if got := header()["X-Multi"]; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("X-Multi = %q, want [a b]", got)
	}
}

// The options snapshot what they are given: mutating the maps or header
// values after Model returns never reaches the request — a Model is
// safe to hand to concurrent runs while the caller keeps its config
// alive (ADR 0013's immutable-Model stance; the 2026-09-22 review's
// reference-capture fix).
func TestExtraBodySnapshot(t *testing.T) {
	nested := map[string]any{"user_id": 7}
	fields := map[string]any{"metadata": nested, "x_vendor_knob": true}
	h := http.Header{"X-Weft-Test": []string{"yes"}}
	srv, body, header := captureServer(t, "testdata/text_only.sse")
	c := antsdk.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("test"))
	m := Model("m", Client(&c), ExtraBody(fields), ExtraHeaders(h))
	nested["user_id"] = 8           // caller mutates after construction
	fields["x_vendor_knob"] = false // top level too
	h["X-Weft-Test"][0] = "no"      // and the header slice
	if _, err := weft.New(m).Generate(t.Context(), weft.Prompt("hi")); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body()), &payload); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, body())
	}
	md, _ := payload["metadata"].(map[string]any)
	if md == nil || md["user_id"] != float64(7) {
		t.Errorf("metadata.user_id = %v, want 7 (the construction-time value)", payload["metadata"])
	}
	if got := payload["x_vendor_knob"]; got != true {
		t.Errorf("x_vendor_knob = %v, want true (the construction-time value)", got)
	}
	if got := header().Get("X-Weft-Test"); got != "yes" {
		t.Errorf("X-Weft-Test = %q, want the construction-time value", got)
	}
}
