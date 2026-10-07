package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
)

// ReplayPolicy pins WEFT-PLAYGROUND.md §6 rule 3's core half: the class
// a tool declares, the honest default for an unannotated tool, and the
// manifest line the runtime link's register payload reads it back
// through. Nothing may re-fire silently before this exists — it is the
// one class a refund belongs to.
func TestReplayPolicy(t *testing.T) {
	safe := core.Tool("lookup_order", "Look up an order.", func(_ context.Context, _ struct{}) (string, error) {
		return "shipped", nil
	}, core.Replay(core.ReplaySafe))
	refund := core.Tool("refund", "Refund an order.", func(_ context.Context, _ struct{}) (string, error) {
		return "refunded", nil
	})
	bogus := core.Tool("bogus", "", func(_ context.Context, _ struct{}) (string, error) {
		return "", nil
	}, core.Replay(core.ReplayPolicy("sometimes")))

	if got := safe.ReplayPolicy(); got != core.ReplaySafe {
		t.Errorf("safe tool ReplayPolicy() = %q, want %q", got, core.ReplaySafe)
	}
	if got := refund.ReplayPolicy(); got != core.ReplayNever {
		t.Errorf("unannotated tool ReplayPolicy() = %q, want %q (never is the honest default)", got, core.ReplayNever)
	}
	if got := bogus.ReplayPolicy(); got != core.ReplayNever {
		t.Errorf("unknown class ReplayPolicy() = %q, want %q (an unknown class is never's)", got, core.ReplayNever)
	}

	agt := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("replay-test"), safe, refund)
	b, err := core.Manifest(agt)
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

// The last Replay option wins, like every other tool option: a tool
// composed from shared defaults that vouch safe and then marked never —
// or given a class nobody knows — is never's. The unsafe reading (the
// first safe sticks) would re-fire a side effect the caller had just
// said must not re-fire.
func TestReplayLastOptionWins(t *testing.T) {
	handler := func(_ context.Context, _ struct{}) (string, error) { return "", nil }
	for _, tc := range []struct {
		name string
		opts []core.ToolOption
		want core.ReplayPolicy
	}{
		{"safe then never", []core.ToolOption{core.Replay(core.ReplaySafe), core.Replay(core.ReplayNever)}, core.ReplayNever},
		{"safe then unknown", []core.ToolOption{core.Replay(core.ReplaySafe), core.Replay(core.ReplayPolicy("sometimes"))}, core.ReplayNever},
		{"never then safe", []core.ToolOption{core.Replay(core.ReplayNever), core.Replay(core.ReplaySafe)}, core.ReplaySafe},
	} {
		if got := core.Tool("t", "", handler, tc.opts...).ReplayPolicy(); got != tc.want {
			t.Errorf("%s: ReplayPolicy() = %q, want %q", tc.name, got, tc.want)
		}
	}
}
