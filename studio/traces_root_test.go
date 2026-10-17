package studio

import "testing"

// TestRootRunOf pins the trace row's top run (UX-A-39): the first id
// without "/" (core's child id rule), else the shortest, "" for none.
func TestRootRunOf(t *testing.T) {
	for _, c := range []struct {
		ids  []string
		want string
	}{
		{nil, ""},
		{[]string{"r1"}, "r1"},
		{[]string{"r1/0/c", "r1"}, "r1"},
		{[]string{"r1/0/c/1/d", "r1/0/c"}, "r1/0/c"},
	} {
		if got := rootRunOf(c.ids); got != c.want {
			t.Errorf("rootRunOf(%v) = %q, want %q", c.ids, got, c.want)
		}
	}
}
