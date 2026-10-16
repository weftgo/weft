package google

import (
	"testing"

	"github.com/weftgo/weft/core"
)

// TestThinkingWire pins the request record's thinking.wire (O16): the
// thinkingConfig field the request sets for each level.
func TestThinkingWire(t *testing.T) {
	for _, c := range []struct {
		t    core.ThinkingConfig
		want string
	}{
		{core.ThinkingConfig{Level: core.ThinkOff}, "thinking_budget"},
		{core.ThinkingConfig{Level: core.ThinkHigh, Budget: 1024}, "thinking_budget"},
		{core.ThinkingConfig{Level: core.ThinkMedium}, "thinking_level"},
		{core.ThinkingConfig{}, "none"},
	} {
		if got := (&model{}).ThinkingWire(c.t); got != c.want {
			t.Errorf("%+v = %q, want %q", c.t, got, c.want)
		}
	}
}
