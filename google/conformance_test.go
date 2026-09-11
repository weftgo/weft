package google_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/google"
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

const stallChunk = `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"one"}]}}]}` + "\n\n"

func newModel(t *testing.T, name string) weft.Model {
	t.Helper()
	switch name {
	case "kill_switch":
		// Any request reaching the server fails the test: the switch
		// must be honoured before I/O, not after.
		return google.Model("m", google.BaseURL(conformance.NoRequestServer(t).URL), google.APIKey("test"))
	case "cancel_mid_stream":
		return google.Model("m", google.BaseURL(conformance.StallServer(t, stallChunk).URL), google.APIKey("test"))
	case "idle_timeout":
		return google.Model("m",
			google.BaseURL(conformance.StallServer(t, stallChunk).URL),
			google.APIKey("test"),
			google.IdleTimeout(200*time.Millisecond))
	case "max_tokens":
		return google.Model("m",
			google.BaseURL(conformance.FixtureServer(t, filepath.Join("testdata", fixtureFile(name))).URL),
			google.APIKey("test"),
			google.MaxTokens(16))
	default:
		return google.Model("m",
			google.BaseURL(conformance.FixtureServer(t, filepath.Join("testdata", fixtureFile(name))).URL),
			google.APIKey("test"))
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
