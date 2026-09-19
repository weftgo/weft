package conformance_test

import (
	"os"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
	"github.com/weftgo/weft/wefttest/conformance"
)

// TestSuiteGreenAgainstReplay proves the replay double obeys the Model
// contract the way an adapter does (TODO §9.2, ADR 0017): every
// transcript case runs from committed fixtures recorded once from
// scriptedFor (`WEFT_RECORD=1 go test ./wefttest/conformance/`
// regenerates them). The four transport cases test behaviour a
// recording cannot carry — a stream that must block until cancelled, a
// stalled stream, a dripping one, the kill switch — and keep their
// purpose-built models.
func TestSuiteGreenAgainstReplay(t *testing.T) {
	transport := map[string]bool{
		"cancel_mid_stream": true,
		"idle_timeout":      true,
		"slow_stream":       true,
		"kill_switch":       true,
	}
	conformance.Run(t, conformance.Caps{
		Reasoning:     true,
		Files:         true,
		Sequential:    true,
		ToolArgDeltas: true,
		Usage:         true,
	}, func(t *testing.T, name string) weft.Model {
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
