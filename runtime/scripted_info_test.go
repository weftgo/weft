package runtime

import (
	"testing"

	"github.com/weftgo/weft/core"
)

// TestScriptedModelInfo pins the scripted engine's ModelInfo: the
// devtools panel names a run "scripted" from exactly this pair on the
// run row's model (studio/web/src/panel/badges.ts's SCRIPTED_MODEL) —
// no weft.* attribute names the engine.
func TestScriptedModelInfo(t *testing.T) {
	got := newScriptedModel(&sourceRun{}, nil).Info()
	if want := (core.ModelInfo{Provider: "weft/runtime", Name: "scripted"}); got != want {
		t.Fatalf("scripted ModelInfo = %+v, want %+v (the panel's SCRIPTED_MODEL reads this pair)", got, want)
	}
}
