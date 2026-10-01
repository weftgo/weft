package weft_test

import (
	"context"
	"strings"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

// ReplayPolicy pins WEFT-PLAYGROUND.md §6 rule 3's core half: the class
// a tool declares, the honest default for an unannotated tool, and the
// manifest line the runtime link's register payload reads it back
// through. Nothing may re-fire silently before this exists — it is the
// one class a refund belongs to.
func TestReplayPolicy(t *testing.T) {
	safe := weft.Tool("lookup_order", "Look up an order.", func(_ context.Context, _ struct{}) (string, error) {
		return "shipped", nil
	}, weft.Replay(weft.ReplaySafe))
	refund := weft.Tool("refund", "Refund an order.", func(_ context.Context, _ struct{}) (string, error) {
		return "refunded", nil
	})
	bogus := weft.Tool("bogus", "", func(_ context.Context, _ struct{}) (string, error) {
		return "", nil
	}, weft.Replay(weft.ReplayPolicy("sometimes")))

	if got := safe.ReplayPolicy(); got != weft.ReplaySafe {
		t.Errorf("safe tool ReplayPolicy() = %q, want %q", got, weft.ReplaySafe)
	}
	if got := refund.ReplayPolicy(); got != weft.ReplayNever {
		t.Errorf("unannotated tool ReplayPolicy() = %q, want %q (never is the honest default)", got, weft.ReplayNever)
	}
	if got := bogus.ReplayPolicy(); got != weft.ReplayNever {
		t.Errorf("unknown class ReplayPolicy() = %q, want %q (an unknown class is never's)", got, weft.ReplayNever)
	}

	agt := weft.New(wefttest.Script(wefttest.Say("ok")), weft.Name("replay-test"), safe, refund)
	b, err := weft.Manifest(agt)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	if !strings.Contains(doc, `"replay_policy": "safe"`) {
		t.Errorf("manifest omits the safe class:\n%s", doc)
	}
	// never is the default: exactly one replay_policy line, on the safe
	// tool — an unannotated tool renders exactly as before.
	if n := strings.Count(doc, `"replay_policy"`); n != 1 {
		t.Errorf("manifest carries %d replay_policy lines, want 1 (never is omitted):\n%s", n, doc)
	}
}
