package wefttest_test

import (
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
)

// The conformance helper itself: a wrapper that forwards the inner
// model passes; one that forgets Info fails, naming the middleware.
func TestConformInfo(t *testing.T) {
	forwarding := func(next core.Model) core.Model { return next }
	if err := wefttest.ConformInfo(forwarding); err != nil {
		t.Errorf("forwarding middleware: %v", err)
	}
	forgetful := func(next core.Model) core.Model {
		return struct{ core.Model }{next} // Stream only — no Info
	}
	if err := wefttest.ConformInfo(forgetful); err == nil {
		t.Error("a middleware that drops Info must fail ConformInfo")
	}
}
