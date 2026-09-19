package google_test

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/google"
	"github.com/weftgo/weft/wefttest/conformance"
	"google.golang.org/genai"
)

// fixtureFile maps each conformance case to its recorded SSE file;
// cases that share behaviour share a recording.
func fixtureFile(name string) string {
	switch name {
	case "usage_nonzero":
		return "tool_roundtrip_struct.sse"
	case "never_contract_violation":
		return "text_only.sse"
	default:
		return name + ".sse"
	}
}

const stallChunk = `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"one"}]}}]}` + "\n\n"

// newModel wires each case to its fixture. Every case but kill_switch
// drives its fixture through an injected SDK client — a test double, so
// the suite runs under WEFT_MODEL_REQUESTS=deny — while kill_switch
// keeps a self-built client: it is the one case that must prove the
// switch fires before I/O.
func newModel(t *testing.T, name string) weft.Model {
	t.Helper()
	at := func(srv *httptest.Server, opts ...google.Option) weft.Model {
		c, err := genai.NewClient(context.Background(), &genai.ClientConfig{
			APIKey:      "test",
			HTTPOptions: genai.HTTPOptions{BaseURL: srv.URL},
		})
		if err != nil {
			t.Fatalf("genai.NewClient: %v", err)
		}
		return google.Model("m", append([]google.Option{google.Client(c)}, opts...)...)
	}
	switch name {
	case "kill_switch":
		// Any request reaching the server fails the test: the switch
		// must be honoured before I/O, not after.
		return google.Model("m", google.BaseURL(conformance.NoRequestServer(t).URL), google.APIKey("test"))
	case "cancel_mid_stream":
		return at(conformance.StallServer(t, stallChunk))
	case "idle_timeout":
		return at(conformance.StallServer(t, stallChunk), google.IdleTimeout(200*time.Millisecond))
	case "slow_stream":
		// Drips every 40ms for 160ms under a 120ms idle timeout: the
		// total exceeds the timeout, each gap does not. The stream ends
		// without a finish marker — Gemini maps that to end_turn.
		return at(conformance.SlowServer(t, stallChunk, "", 40*time.Millisecond, 4), google.IdleTimeout(120*time.Millisecond))
	case "max_tokens":
		return at(conformance.FixtureServer(t, filepath.Join("testdata", fixtureFile(name))), google.MaxTokens(16))
	default:
		return at(conformance.FixtureServer(t, filepath.Join("testdata", fixtureFile(name))))
	}
}

// The adapter's offline conformance run (TODO §3.5). Sequential is off:
// Gemini has no parallel-tool-calls switch — a documented gap in
// ADR 0013, declared rather than silently skipped.
func TestConformance(t *testing.T) {
	conformance.Run(t, conformance.Caps{
		Reasoning: true,
		Files:     true,
		Usage:     true,
	}, newModel)
}
