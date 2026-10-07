package conformance_test

import (
	"os"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/core/wefttest/conformance"
)

// TestSuiteGreenAgainstReplay proves the replay double obeys the Model
// contract the way an adapter does (TODO §9.2, ADR 0017): every
// transcript case runs from committed fixtures recorded once from
// scriptedFor (`WEFT_RECORD=1 go test ./wefttest/conformance/`
// regenerates them). The six transport cases test behaviour a
// recording cannot carry — a stream that must block until cancelled, a
// stalled stream (mid-stream and before headers), a dripping one, a
// provider refusing the call, the kill switch — and keep their
// purpose-built models.
func TestSuiteGreenAgainstReplay(t *testing.T) {
	transport := map[string]bool{
		"cancel_mid_stream":           true,
		"idle_timeout":                true,
		"idle_timeout_before_headers": true,
		"slow_stream":                 true,
		"provider_error":              true,
		"kill_switch":                 true,
	}
	conformance.Run(t, conformance.Caps{
		Reasoning:     true,
		Files:         true,
		Sequential:    true,
		ToolArgDeltas: true,
		Usage:         true,
	}, func(t *testing.T, name string) core.Model {
		if transport[name] {
			return scriptedFor(t, name)
		}
		if os.Getenv("WEFT_RECORD") != "" {
			inner := scriptedFor(t, name)
			if name == "file_input" {
				// The every-cap shape, as in TestSuiteGreenWithEveryCap.
				inner = wefttest.Script(wefttest.Say("A transparent square."))
			}
			return wefttest.Record(t, "testdata/replay", inner)
		}
		return wefttest.Replay(t, "testdata/replay")
	})
}
