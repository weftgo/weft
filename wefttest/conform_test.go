package wefttest_test

import (
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

// The conformance helper itself: a wrapper that forwards the inner
// model passes; one that forgets Info fails, naming the middleware.
func TestConformInfo(t *testing.T) {
	forwarding := func(next weft.Model) weft.Model { return next }
	if err := wefttest.ConformInfo(forwarding); err != nil {
		t.Errorf("forwarding middleware: %v", err)
	}
	forgetful := func(next weft.Model) weft.Model {
		return struct{ weft.Model }{next} // Stream only — no Info
	}
	if err := wefttest.ConformInfo(forgetful); err == nil {
		t.Error("a middleware that drops Info must fail ConformInfo")
	}
}
