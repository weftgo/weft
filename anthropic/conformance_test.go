package anthropic_test

import (
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	antsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/weftgo/weft"
	"github.com/weftgo/weft/anthropic"
	"github.com/weftgo/weft/wefttest/conformance"
)

// fixtureFile maps each conformance case to its recorded SSE file;
// cases that share behaviour share a recording.
func fixtureFile(name string) string {
	switch name {
	case "usage_nonzero", "tool_args_delta":
		return "tool_roundtrip_struct.sse"
	case "never_contract_violation":
		return "text_only.sse"
	default:
		return name + ".sse"
	}
}

const stallChunk = `event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"one"}}

`

// newModel wires each case to its fixture. Every case but kill_switch
// drives its fixture through an injected SDK client — a test double, so
// the suite runs under WEFT_MODEL_REQUESTS=deny — while kill_switch
// keeps a self-built client: it is the one case that must prove the
// switch fires before I/O.
func newModel(t *testing.T, name string) weft.Model {
	t.Helper()
	at := func(srv *httptest.Server, opts ...anthropic.Option) weft.Model {
		c := antsdk.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("test"))
		return anthropic.Model("m", append([]anthropic.Option{anthropic.Client(&c)}, opts...)...)
	}
	switch name {
	case "kill_switch":
		// Any request reaching the server fails the test: the switch
		// must be honoured before I/O, not after.
		return anthropic.Model("m", anthropic.BaseURL(conformance.NoRequestServer(t).URL), anthropic.APIKey("test"))
	case "cancel_mid_stream":
		return at(conformance.StallServer(t, stallChunk))
	case "idle_timeout":
		return at(conformance.StallServer(t, stallChunk), anthropic.IdleTimeout(200*time.Millisecond))
	case "slow_stream":
		done := `event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}

`
		// Drips every 40ms for 160ms under a 120ms idle timeout: the
		// total exceeds the timeout, each gap does not.
		return at(conformance.SlowServer(t, stallChunk, done, 40*time.Millisecond, 4), anthropic.IdleTimeout(120*time.Millisecond))
	case "max_tokens":
		return at(conformance.FixtureServer(t, filepath.Join("testdata", fixtureFile(name))), anthropic.MaxTokens(16))
	default:
		return at(conformance.FixtureServer(t, filepath.Join("testdata", fixtureFile(name))))
	}
}

// The adapter's offline conformance run (TODO §3.5): reasoning
// round-trips with signatures, images and PDFs are accepted, the
// sequential hint maps to disable_parallel_tool_use, usage is reported.
func TestConformance(t *testing.T) {
	conformance.Run(t, conformance.Caps{
		Reasoning:     true,
		Files:         true,
		Sequential:    true,
		ToolArgDeltas: true,
		Usage:         true,
	}, newModel)
}
