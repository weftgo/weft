package thread_test

import (
	"context"
	"errors"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
)

// Claim is the first write's lease check asked ahead of it: the writer
// claims for free; a second Session on the same Storage value is
// refused ErrLocked and writes nothing; once the writer has written and
// closed, the second one is stale; a fresh Open claims, and from then
// on the lease is its own (the old writer, closed, is ErrClosed).
func TestClaim(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	agent := core.New(wefttest.Script())

	writer, err := thread.Create(ctx, st, agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Claim(ctx); err != nil {
		t.Fatalf("the writer's own claim = %v", err)
	}
	reader, err := thread.Open(ctx, st, writer.ID(), agent)
	if err != nil {
		t.Fatal(err)
	}
	before := len(reader.Entries())
	if err := reader.Claim(ctx); !errors.Is(err, thread.ErrLocked) {
		t.Fatalf("a second Session's claim = %v, want ErrLocked", err)
	}
	_, entries, _, err := st.Load(ctx, writer.ID())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != before {
		t.Fatalf("a refused claim wrote: %d entries, had %d", len(entries), before)
	}
	if err := writer.SetInfo(ctx, "moved on", nil); err != nil {
		t.Fatalf("the writer still writes after a refused claim: %v", err)
	}
	if err := writer.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := writer.Claim(ctx); !errors.Is(err, thread.ErrClosed) {
		t.Fatalf("a closed Session's claim = %v, want ErrClosed", err)
	}
	if err := reader.Claim(ctx); !errors.Is(err, thread.ErrStale) {
		t.Fatalf("a stale Session's claim = %v, want ErrStale", err)
	}
	_ = reader.Close(ctx)
	again, err := thread.Open(ctx, st, writer.ID(), agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := again.Claim(ctx); err != nil {
		t.Fatalf("a fresh Open's claim = %v", err)
	}
	third, err := thread.Open(ctx, st, writer.ID(), agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := third.SetInfo(ctx, "x", nil); !errors.Is(err, thread.ErrLocked) {
		t.Fatalf("a write behind the claimant = %v, want ErrLocked", err)
	}
	if err := again.Close(ctx); err != nil {
		t.Fatal(err)
	}
	ctx2, cancel := context.WithCancel(ctx)
	cancel()
	if err := third.Claim(ctx2); !errors.Is(err, context.Canceled) {
		t.Fatalf("a canceled claim = %v", err)
	}
}
