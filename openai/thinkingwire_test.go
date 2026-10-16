package openai

import (
	"testing"

	"github.com/weftgo/weft/core"
)

// TestThinkingWire pins the request record's thinking.wire (O16) for
// each dialect: the field the level goes out in, or none.
func TestThinkingWire(t *testing.T) {
	low := core.ThinkingConfig{Level: core.ThinkLow}
	off := core.ThinkingConfig{Level: core.ThinkOff}
	for _, c := range []struct {
		d    ThinkingDialect
		t    core.ThinkingConfig
		want string
	}{
		{DialectEffort, low, "reasoning_effort"},
		{DialectEffort, off, "none"}, // the API has no off switch
		{DialectObject, low, "thinking"},
		{DialectObject, off, "thinking"},
		{DialectNone, low, "none"},
	} {
		if got := (&model{dialect: c.d}).ThinkingWire(c.t); got != c.want {
			t.Errorf("dialect %d level %d = %q, want %q", c.d, c.t.Level, got, c.want)
		}
	}
}
