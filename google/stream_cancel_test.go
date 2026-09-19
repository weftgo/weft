package google

import (
	"context"
	"errors"
	"testing"

	"github.com/weftgo/weft"
)

// A consumer that cancels mid-stream but keeps consuming must get the
// contract's (nil, ctx.Err()) — never a fabricated ModelFinish. The
// reader goroutine can exit its ready/ack handshake on cancellation
// without the SDK's iterator yielding an error, leaving streamErr nil;
// before the ctx check after the read loop, that window turned a
// canceled run into a normal-looking finish.
func TestCancelMidStreamYieldsContextError(t *testing.T) {
	m := fixtureModel(t, "text_only")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		canceled   bool
		fabricated bool
		finalErr   error
	)
	for ev, err := range m.Stream(ctx, weft.ModelRequest{Messages: []weft.Message{weft.User("hi")}}) {
		if err != nil {
			finalErr = err
			break
		}
		if _, ok := ev.(weft.ModelFinish); ok {
			fabricated = true
		}
		if !canceled {
			canceled = true
			cancel() // keep consuming: the bug window needs a live consumer
		}
	}
	if fabricated {
		t.Error("canceled stream fabricated a ModelFinish")
	}
	if !errors.Is(finalErr, context.Canceled) {
		t.Errorf("terminal error = %v, want context.Canceled", finalErr)
	}
}
