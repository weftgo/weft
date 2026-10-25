package runtime

import (
	"strings"
	"testing"
)

// TestReasonOfReadsEveryMissShape pins the contract between
// weft/runtime's scripted miss text and Studio's reading of it: the
// step (or none), every component word, and nil for any other error.
func TestReasonOfReadsEveryMissShape(t *testing.T) {
	for _, c := range []struct {
		text  string
		step  int
		comps string
	}{
		{"weft/runtime: no recorded turn for step 0 (scripted engine, key 01e4444150c5180f): x [messages]; y", 0, "messages"},
		{"weft: run failed at step 1: model stream: weft/runtime: no recorded turn for step 12 (scripted engine, key 01e4): a; b [tools, thinking, tool_choice, sequential]; z", 12, "tools,thinking,tool_choice,sequential"},
		{"weft/runtime: no recorded turn for this request (scripted engine, key 00): the source recorded no turn for it [unanswered]; z", -1, "unanswered"},
	} {
		r := ReasonOf(c.text)
		if r == nil || r.Kind != ReasonScriptedMiss || r.Step != c.step || strings.Join(r.Components, ",") != c.comps {
			t.Errorf("ReasonOf(%q) = %+v, want step %d [%s]", c.text, r, c.step, c.comps)
		}
	}
	for _, text := range []string{"", "boom", "weft/runtime: no recorded turn for this request (scripted engine, key 0f): the input changed since the recording"} {
		if r := ReasonOf(text); r != nil {
			t.Errorf("ReasonOf(%q) = %+v, want nil", text, r)
		}
	}
	if got := ScriptedMissReason(-1, []string{"unanswered"}).Message; got != "scripted has no recorded answer for this request: the source never answered it — run on live, or replay from an earlier step" {
		t.Errorf("no-step sentence = %q", got)
	}
}
