package carry

import (
	"context"
	"errors"
	"testing"
	"time"
)

type key string

func TestValues(t *testing.T) {
	origin, cancelOrigin := context.WithCancel(context.WithValue(context.Background(), key("rule"), "origin"))
	origin = context.WithValue(origin, key("shared"), "origin")
	live, cancelLive := context.WithCancelCause(context.WithValue(context.Background(), key("shared"), "live"))
	defer cancelLive(nil)

	c := Values(live, origin)
	if got := c.Value(key("rule")); got != "origin" {
		t.Errorf("rule = %v, want the origin's value for a key live lacks", got)
	}
	if got := c.Value(key("shared")); got != "live" {
		t.Errorf("shared = %v, want live's value to win", got)
	}
	cancelOrigin()
	if c.Err() != nil {
		t.Fatal("the origin's cancellation reached the carried context")
	}
	child, stop := context.WithCancel(c)
	defer stop()
	cause := errors.New("live went away")
	cancelLive(cause)
	select {
	case <-child.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a context derived from the carried one missed live's cancellation")
	}
	if !errors.Is(context.Cause(child), cause) {
		t.Errorf("Cause = %v, want live's", context.Cause(child))
	}
	if Values(live, nil) != live {
		t.Error("a nil origin should return live itself")
	}
}
