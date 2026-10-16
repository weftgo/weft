package anthropic

import (
	"testing"

	"github.com/weftgo/weft/core"
)

// TestThinkingWire pins the request record's thinking.wire (O16): the
// thinking member params sends for each level.
func TestThinkingWire(t *testing.T) {
	for _, c := range []struct {
		t    core.ThinkingConfig
		want string
	}{
		{core.ThinkingConfig{Level: core.ThinkOff, Budget: 2048}, "thinking.disabled"},
		{core.ThinkingConfig{Level: core.ThinkHigh, Budget: 2048}, "thinking.budget_tokens"},
		{core.ThinkingConfig{Level: core.ThinkLow}, "thinking.adaptive"},
		{core.ThinkingConfig{}, "none"},
	} {
		if got := (&model{}).ThinkingWire(c.t); got != c.want {
			t.Errorf("%+v = %q, want %q", c.t, got, c.want)
		}
	}
}
