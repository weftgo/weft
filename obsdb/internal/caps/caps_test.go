package caps_test

import (
	"testing"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/internal/caps"
)

// The released bounds: the variables obsdb and the backends apply are
// obsdb's constants, and the constants are the documented values. A
// test that lowers one (caps.Lower) restores it.
func TestDefaultsAreTheReleasedBounds(t *testing.T) {
	for _, c := range []struct {
		name            string
		read, cons, was int
	}{
		{"TreeSpanCap", caps.TreeSpan, obsdb.TreeSpanCap, 10_000},
		{"TreeTraceCap", caps.TreeTrace, obsdb.TreeTraceCap, 50_000},
		{"TreeMissingCap", caps.TreeMissing, obsdb.TreeMissingCap, 100},
		{"TreeRowCap", caps.TreeRow, obsdb.TreeRowCap, 10_000},
		{"ChildRunsCap", caps.ChildRuns, obsdb.ChildRunsCap, 10_000},
		{"SweepBatch", caps.SweepBatch, obsdb.SweepBatch, 500},
		{"MaxLogCandidates", caps.LogCandidates, obsdb.MaxLogCandidates, 10_000},
	} {
		if c.read != c.cons || c.cons != c.was {
			t.Errorf("%s: applied as %d, constant %d; want both %d", c.name, c.read, c.cons, c.was)
		}
	}
	restore := caps.Lower(&caps.TreeSpan, 3)
	if caps.TreeSpan != 3 {
		t.Errorf("Lower: TreeSpan = %d, want 3", caps.TreeSpan)
	}
	restore()
	if caps.TreeSpan != obsdb.TreeSpanCap {
		t.Errorf("Lower's restore: TreeSpan = %d, want %d", caps.TreeSpan, obsdb.TreeSpanCap)
	}
}
