package thread_test

import (
	"context"
	"errors"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
)

// Claim is the first write's lease check asked ahead of it: the writer
// claims for free; a second Session on the same Storage value is
// refused ErrLocked and writes nothing; once the writer has written and
// closed, the second one is stale; a fresh Open claims, and from then
// on the lease is its own (the old writer, closed, is ErrClosed).
func TestClaim(t *testing.T) {
	t.Run("memory", func(t *testing.T) { testClaim(t, thread.Memory()) })
	t.Run("jsonl", func(t *testing.T) {
		st, err := jsonl.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		testClaim(t, st)
	})
}

func testClaim(t *testing.T, st thread.Storage) {
	ctx := context.Background()
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
	// The refused claim gave the lease back: a fresh Session claims
	// while the stale one is still open (never closed — a resolver's
	// cached Session with no release).
	again, err := thread.Open(ctx, st, writer.ID(), agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := again.Claim(ctx); err != nil {
		t.Fatalf("a fresh Open's claim after another's ErrStale = %v, want nil", err)
	}
	defer func() { _ = reader.Close(ctx) }()
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
