package openai_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/openai"
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

const stallChunk = `data: {"id":"c1","object":"chat.completion.chunk","created":1700000000,"model":"m","choices":[{"index":0,"delta":{"content":"one"},"finish_reason":null}]}` + "\n\n"

// newModel wires each case to its fixture: FixtureServer for recorded
// exchanges, StallServer for the never-finishing stream, and the
// token-limit option only for max_tokens.
func newModel(t *testing.T, name string) weft.Model {
	t.Helper()
	switch name {
	case "kill_switch":
		// Any request reaching the server fails the test: the switch
		// must be honoured before I/O, not after.
		return openai.Model("m", openai.BaseURL(conformance.NoRequestServer(t).URL), openai.APIKey("test"))
	case "cancel_mid_stream":
		return openai.Model("m", openai.BaseURL(conformance.StallServer(t, stallChunk).URL), openai.APIKey("test"))
	case "idle_timeout":
		return openai.Model("m",
			openai.BaseURL(conformance.StallServer(t, stallChunk).URL),
			openai.APIKey("test"),
			openai.IdleTimeout(200*time.Millisecond))
	case "max_tokens":
		return openai.Model("m",
			openai.BaseURL(conformance.FixtureServer(t, filepath.Join("testdata", fixtureFile(name))).URL),
			openai.APIKey("test"),
			openai.MaxTokens(16))
	default:
		return openai.Model("m",
			openai.BaseURL(conformance.FixtureServer(t, filepath.Join("testdata", fixtureFile(name))).URL),
			openai.APIKey("test"))
	}
}

// The adapter's offline conformance run (TODO §3.5). Reasoning is off:
// reasoning_content is a compatible-server extension, not an OpenAI
// field — a compatible-server user can turn the cap on.
func TestConformance(t *testing.T) {
	conformance.Run(t, conformance.Caps{Files: true, Sequential: true, Usage: true}, newModel)
}
