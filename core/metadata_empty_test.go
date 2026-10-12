package core_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
)

// An empty value is "none": a run's own empty pair removes the key it
// would inherit from the run it is started under (the nested-session
// case: thread stamps a session's own weft.public_id, "" when it has
// none), and is never stored, recorded on a span, or counted as dropped.
// Pairs without an empty value inherit exactly as before.
func TestMetadataEmptyValueRemovesInherited(t *testing.T) {
	tp := newRecProvider()
	var childGot map[string]string
	childSeen := false
	child := core.New(wefttest.Script(wefttest.Say("done")), core.Name("inner"), core.TracerProvider(tp),
		core.Tap(func(ctx context.Context, ev core.Event) {
			if _, ok := ev.(core.RunStart); ok {
				childSeen = true
				childGot = core.MetadataFromContext(ctx)
			}
		}))
	type in struct{}
	nested := core.Tool("nested", "Run the inner agent.", func(ctx context.Context, _ in) (string, error) {
		_, err := child.Generate(ctx, core.Metadata(map[string]string{
			"weft.public_id": "", "weft.session.parent": "", "weft.session.id": "s_inner",
		}))
		return "ok", err
	})
	parent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "nested", Args: `{}`}),
		wefttest.Say("final"),
	), core.TracerProvider(tp), nested)
	if _, err := parent.Generate(context.Background(), core.Metadata(map[string]string{
		"weft.public_id": "pub_outer", "weft.session.id": "s_outer", "tenant": "acme",
	})); err != nil {
		t.Fatal(err)
	}
	if !childSeen {
		t.Fatal("the inner run never started")
	}
	want := map[string]string{"weft.session.id": "s_inner", "tenant": "acme"}
	if len(childGot) != len(want) {
		t.Fatalf("inner metadata = %v, want %v", childGot, want)
	}
	for k, v := range want {
		if childGot[k] != v {
			t.Errorf("inner metadata[%q] = %q, want %q", k, childGot[k], v)
		}
	}
	attrs := tp.find(t, "invoke_agent inner").attrsMap()
	for _, k := range []string{"weft.public_id", "weft.session.parent", "weft.metadata.dropped"} {
		if v, ok := attrs[k]; ok {
			t.Errorf("inner invoke_agent span carries %s = %q, want absent", k, v)
		}
	}
	if attrs["tenant"] != "acme" || attrs["weft.session.id"] != "s_inner" {
		t.Errorf("inner span attrs = %v, want tenant inherited and its own session id", attrs)
	}
}

// WithMetadata's empty value removes the pair the context carried, and a
// run whose own empty pairs remove every inherited one reads nil.
func TestMetadataEmptyValueEmptiesContext(t *testing.T) {
	ctx := core.WithMetadata(context.Background(), map[string]string{"tenant": "acme", "env": "dev"})
	ctx = core.WithMetadata(ctx, map[string]string{"env": ""})
	if got := core.MetadataFromContext(ctx); len(got) != 1 || got["tenant"] != "acme" {
		t.Fatalf("WithMetadata(env:\"\") = %v, want only tenant", got)
	}
	var got map[string]string
	seen := false
	agt := core.New(wefttest.Script(wefttest.Say("ok")),
		core.Tap(func(ctx context.Context, _ core.Event) {
			if !seen {
				seen = true
				got = core.MetadataFromContext(ctx)
			}
		}))
	if _, err := agt.Generate(ctx, core.Metadata(map[string]string{"tenant": ""})); err != nil {
		t.Fatal(err)
	}
	if !seen || got != nil {
		t.Fatalf("run metadata = %v (seen %v), want nil: its empty pair removed the only inherited one", got, seen)
	}
}

// At the 64-key cap a run's own removal frees its slot before any of its
// additions is weighed: own {"a_new": "x", "zz_old": ""} over 64
// inherited pairs (zz_old among them) keeps a_new, removes zz_old and
// drops nothing — although a_new sorts first, which once found the map
// still full and dropped it.
func TestMetadataEmptyValueFreesSlotAtCap(t *testing.T) {
	inherited := map[string]string{"zz_old": "v"}
	for i := 0; len(inherited) < 64; i++ {
		inherited[fmt.Sprintf("k%02d", i)] = "v"
	}
	ctx := core.WithMetadata(context.Background(), inherited)
	if got := core.MetadataFromContext(ctx); len(got) != 64 {
		t.Fatalf("inherited = %d pairs, want 64", len(got))
	}
	tp := newRecProvider()
	var got map[string]string
	seen := false
	agt := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("capped"), core.TracerProvider(tp),
		core.Tap(func(ctx context.Context, _ core.Event) {
			if !seen {
				seen = true
				got = core.MetadataFromContext(ctx)
			}
		}))
	if _, err := agt.Generate(ctx, core.Metadata(map[string]string{"a_new": "x", "zz_old": ""})); err != nil {
		t.Fatal(err)
	}
	if !seen || got["a_new"] != "x" || len(got) != 64 {
		t.Fatalf("run metadata = %d pairs, a_new %q; want 64 with a_new kept", len(got), got["a_new"])
	}
	if _, ok := got["zz_old"]; ok {
		t.Error("zz_old present, want removed")
	}
	if d, ok := tp.find(t, "invoke_agent capped").attrsMap()["weft.metadata.dropped"]; ok {
		t.Errorf("weft.metadata.dropped = %q, want absent: nothing was dropped", d)
	}
}
