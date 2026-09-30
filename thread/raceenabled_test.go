//go:build race

package thread_test

// raceEnabled reports whether this build carries the race detector —
// the budget suite asserts alloc counts only without it, whose
// bookkeeping is not the code's.

var raceEnabled = true
