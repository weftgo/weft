package thread

import (
	"context"
	"iter"
	"testing"

	"github.com/weftgo/weft/core"
)

// unhashableModel is a Model whose dynamic type cannot sit in a map
// or survive an ==: a struct value carrying a slice. Middleware in
// the wild is usually a pointer, but nothing in core.Model promises
// comparable — the native lookup must not panic on one.
type unhashableModel struct {
	tags []string
}

func (m unhashableModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return func(yield func(core.ModelEvent, error) bool) {
		yield(core.ModelTextDelta{Text: "x"}, nil)
		yield(core.ModelFinish{Reason: core.StopEndTurn}, nil)
	}
}

func (m unhashableModel) Unwrap() core.Model { return nil }

// unhashableLoop wraps itself forever — the loop guard must terminate
// the walk without ever hashing the wrapper.
type unhashableLoop struct {
	unhashableModel
}

func (m unhashableLoop) Unwrap() core.Model { return m }

// The native lookup terminates on unhashable Model values instead of
// panicking: chains are compared structurally, never hashed (the
// 2026-09-29 review's finding — map-key and == interface comparisons
// both panic on a non-comparable dynamic type).
func TestNativeOfUnhashableModelTerminates(t *testing.T) {
	if nc := nativeOf(unhashableModel{tags: []string{"a"}}); nc != nil {
		t.Fatalf("an unhashable model reported a NativeCompactor: %T", nc)
	}
	if nc := nativeOf(unhashableLoop{}); nc != nil {
		t.Fatalf("an unhashable looping model reported a NativeCompactor: %T", nc)
	}
}
