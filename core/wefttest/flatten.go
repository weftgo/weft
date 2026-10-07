package wefttest

import "github.com/weftgo/weft/core"

// Flatten returns evs with every Nested event replaced by its inner
// event, recursively — the child's events as the child emitted them —
// for asserting a nested run's behaviour without unwrapping by hand.
//
//	evs := collect(parentStream)
//	want := []core.Event{core.RunStart{}, core.StepStart{}, ...}
//	slices.Equal(want, Flatten(evs))
func Flatten(evs []core.Event) []core.Event {
	out := make([]core.Event, 0, len(evs))
	for _, ev := range evs {
		if n, ok := ev.(core.Nested); ok {
			out = append(out, Flatten([]core.Event{n.Event})...)
			continue
		}
		out = append(out, ev)
	}
	return out
}
