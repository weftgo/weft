package reqread

import (
	"testing"

	"github.com/weftgo/weft/obsdb"
)

func TestLookups(t *testing.T) {
	cats := UniqueCatalogs([]obsdb.ToolsRecord{{Index: 0, Hash: "a"}, {Index: 1, Hash: "b"}, {Index: 2, Hash: "a"}})
	if len(cats) != 2 || cats[0].Index != 0 || cats[1].Index != 1 {
		t.Errorf("UniqueCatalogs = %+v", cats)
	}
	if got, ok := FindTools(cats, "b"); !ok || got.Index != 1 {
		t.Errorf("FindTools(b) = %+v, %v", got, ok)
	}
	if _, ok := FindTools(cats, ""); ok {
		t.Error("FindTools matched the empty hash")
	}
	if _, ok := FindPrompt([]obsdb.PromptRecord{{Hash: ""}}, ""); ok {
		t.Error("FindPrompt matched the empty hash")
	}
}
