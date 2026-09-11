package anthropic_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/anthropic"
	"github.com/weftgo/weft/wefttest/conformance"
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

const stallChunk = `event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"one"}}

`

func newModel(t *testing.T, name string) weft.Model {
	t.Helper()
	switch name {
	case "kill_switch":
		// Any request reaching the server fails the test: the switch
		// must be honoured before I/O, not after.
		return anthropic.Model("m", anthropic.BaseURL(conformance.NoRequestServer(t).URL), anthropic.APIKey("test"))
	case "cancel_mid_stream":
		return anthropic.Model("m", anthropic.BaseURL(conformance.StallServer(t, stallChunk).URL), anthropic.APIKey("test"))
	case "idle_timeout":
		return anthropic.Model("m",
			anthropic.BaseURL(conformance.StallServer(t, stallChunk).URL),
			anthropic.APIKey("test"),
			anthropic.IdleTimeout(200*time.Millisecond))
	case "max_tokens":
		return anthropic.Model("m",
			anthropic.BaseURL(conformance.FixtureServer(t, filepath.Join("testdata", fixtureFile(name))).URL),
			anthropic.APIKey("test"),
			anthropic.MaxTokens(16))
	default:
		return anthropic.Model("m",
			anthropic.BaseURL(conformance.FixtureServer(t, filepath.Join("testdata", fixtureFile(name))).URL),
			anthropic.APIKey("test"))
	}
}

// The adapter's offline conformance run (TODO §3.5): reasoning
// round-trips with signatures, images and PDFs are accepted, the
// sequential hint maps to disable_parallel_tool_use, usage is reported.
func TestConformance(t *testing.T) {
	conformance.Run(t, conformance.Caps{
		Reasoning:  true,
		Files:      true,
		Sequential: true,
		Usage:      true,
	}, newModel)
}
