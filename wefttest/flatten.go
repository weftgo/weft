package wefttest

import "github.com/weftgo/weft"

// Flatten returns evs with every Nested event replaced by its inner
// event, recursively — the child's events as the child emitted them —
// for asserting a nested run's behaviour without unwrapping by hand.
//
//	evs := collect(parentStream)
//	want := []weft.Event{weft.RunStart{}, weft.StepStart{}, ...}
//	slices.Equal(want, Flatten(evs))
func Flatten(evs []weft.Event) []weft.Event {
	out := make([]weft.Event, 0, len(evs))
	for _, ev := range evs {
		if n, ok := ev.(weft.Nested); ok {
			out = append(out, Flatten([]weft.Event{n.Event})...)
			continue
		}
		out = append(out, ev)
	}
	return out
}
