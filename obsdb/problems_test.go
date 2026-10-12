package obsdb_test

import (
	"testing"

	"github.com/weftgo/weft/obsdb"
)

// CountProblems' fold (docs/record-contract.md §6, U4-fix): a failed
// attempt counts only below its call's highest attempt index — a
// non-retryable failure (one attempt) and an exhausted call's last
// attempt never; denied_approval keys by span, its count the last
// field; U4's replaced kinds count nowhere.
func TestCountProblemsFold(t *testing.T) {
	for _, c := range []struct {
		name   string
		tokens []string
		want   obsdb.Problems
	}{
		{"one attempt, failed: not retried", []string{"attempt:a1:c1:1:1"}, obsdb.Problems{}},
		{"exhausted at three: two retried", []string{"attempt:a1:c1:1:1", "attempt:a2:c1:2:1", "attempt:a3:c1:3:1"},
			obsdb.Problems{AttemptsRetried: 2}},
		{"retried then answered", []string{"attempt:a1:c1:1:1", "attempt:a2:c1:2:0"}, obsdb.Problems{AttemptsRetried: 1}},
		{"calls apart", []string{"attempt:a1:c1:1:1", "attempt:a2:c2:2:0"}, obsdb.Problems{}},
		{"fallback answer", []string{"attempt:a1:c1:1:1", "attempt:a2:c1:2:0", "fallback_answer:a2"},
			obsdb.Problems{AttemptsRetried: 1, Fallbacks: 1}},
		{"two roots, one count", []string{"denied_approval:r1:2", "denied_approval:r2:2"}, obsdb.Problems{Denied: 4}},
		{"duplicates once", []string{"tool_failed:t1", "tool_failed:t1", "denied:t2", "truncated:t3"},
			obsdb.Problems{ToolErrors: 1, Denied: 1, Truncated: 1}},
		{"U4's kinds count nowhere", []string{"retried:a1", "fallback:c1", "tool_error:t1", "denied_approval:2"}, obsdb.Problems{}},
	} {
		if got := obsdb.CountProblems(c.tokens); got != c.want {
			t.Errorf("%s: CountProblems(%q) = %+v, want %+v", c.name, c.tokens, got, c.want)
		}
	}
}
