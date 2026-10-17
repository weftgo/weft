// Package caps holds obsdb's read and sweep bounds as obsdb and its
// backends apply them: the exported constants (obsdb.TreeSpanCap,
// TreeTraceCap, TreeMissingCap, TreeRowCap, ChildRunsCap, SweepBatch,
// MaxLogCandidates), the same values, as variables the conformance
// table (obsdbtest) lowers around a past-the-cap case, so that case
// writes tens of rows, not tens of thousands, and proves the same
// behaviour at the lowered bound. Nothing outside a test changes them;
// caps_test.go pins each to its constant and its released value.
package caps

// The bounds as read: each equals its obsdb constant.
var (
	TreeSpan      = 10_000 // obsdb.TreeSpanCap
	TreeTrace     = 50_000 // obsdb.TreeTraceCap
	TreeMissing   = 100    // obsdb.TreeMissingCap
	TreeRow       = 10_000 // obsdb.TreeRowCap
	ChildRuns     = 10_000 // obsdb.ChildRunsCap
	SweepBatch    = 500    // obsdb.SweepBatch
	LogCandidates = 10_000 // obsdb.MaxLogCandidates
)

// Lower sets *v to n and returns the restore. A test calls it, and its
// restore, with no read or sweep running: the variables are plain.
func Lower(v *int, n int) (restore func()) {
	old := *v
	*v = n
	return func() { *v = old }
}
