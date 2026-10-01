package weft_test

// Metadata's contract (ADR 0024 S1.1): merge order, context placement
// before any span starts, inheritance by subagent runs, and the limits.

import (
	"context"
	"strings"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

func TestMetadataMergesOntoRunContext(t *testing.T) {
	var got map[string]string
	agt := weft.New(wefttest.Script(wefttest.Say("ok")),
		weft.Tap(func(ctx context.Context, _ weft.Event) {
			if got == nil {
				got = weft.MetadataFromContext(ctx)
			}
		}))
	if _, err := agt.Generate(context.Background(),
		weft.Metadata(map[string]string{"tenant": "acme", "weft.session.id": "s_1"}),
		weft.Metadata(map[string]string{"tenant": "acme-pro", "env": "dev"}),
	); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"tenant": "acme-pro", "weft.session.id": "s_1", "env": "dev"}
	if len(got) != len(want) {
		t.Fatalf("metadata = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("metadata[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestMetadataNilWithoutOption(t *testing.T) {
	var seen bool
	agt := weft.New(wefttest.Script(wefttest.Say("ok")),
		weft.Tap(func(ctx context.Context, _ weft.Event) {
			seen = true
			if md := weft.MetadataFromContext(ctx); md != nil {
				t.Errorf("MetadataFromContext = %v, want nil", md)
			}
		}))
	if _, err := agt.Generate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("tap never ran")
	}
}

// A Subagent's child run inherits the parent's metadata: the merged map
// rides the context the child executes on, and the child's own pairs
// overlay it.
func TestMetadataInheritedAndOverlaidBySubagentRun(t *testing.T) {
	var childGot map[string]string
	child := weft.New(wefttest.Script(wefttest.Say("done")),
		weft.Tap(func(ctx context.Context, ev weft.Event) {
			if _, ok := ev.(weft.RunStart); ok && childGot == nil {
				childGot = weft.MetadataFromContext(ctx)
			}
		}))
	delegate := weft.Subagent("research", "Do the research.", child)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"x"}`}),
		wefttest.Say("final"),
	), delegate)
	if _, err := parent.Generate(context.Background(),
		weft.Metadata(map[string]string{"tenant": "acme", "weft.session.id": "s_9"}),
		weft.Metadata(map[string]string{"env": "dev"}),
	); err != nil {
		t.Fatal(err)
	}
	if childGot == nil {
		t.Fatal("child run saw no metadata")
	}
	want := map[string]string{"tenant": "acme", "weft.session.id": "s_9", "env": "dev"}
	if len(childGot) != len(want) {
		t.Fatalf("child metadata = %v, want %v", childGot, want)
	}
	for k, v := range want {
		if childGot[k] != v {
			t.Errorf("child metadata[%q] = %q, want %q", k, childGot[k], v)
		}
	}
}

// Metadata is identity, not a payload: at most 64 keys, a key at most
// 128 bytes, a value at most 1024 bytes. Over-limit entries are dropped,
// never truncated; an empty key drops too.
func TestMetadataLimits(t *testing.T) {
	overKey := strings.Repeat("k", 129)
	overValue := strings.Repeat("v", 1025)
	kv := map[string]string{
		"":         "an empty key drops",
		overKey:    "an over-long key drops",
		"bigvalue": overValue,
		"keep":     "kept",
	}
	for i := 0; i < 66; i++ { // past the 64-key cap even before the bad entries
		kv["k"+string(rune('a'+i%26))+string(rune('0'+i/26))] = "v"
	}
	var got map[string]string
	agt := weft.New(wefttest.Script(wefttest.Say("ok")),
		weft.Tap(func(ctx context.Context, _ weft.Event) {
			if got == nil {
				got = weft.MetadataFromContext(ctx)
			}
		}))
	if _, err := agt.Generate(context.Background(), weft.Metadata(kv)); err != nil {
		t.Fatal(err)
	}
	if len(got) > 64 {
		t.Errorf("%d keys survived, want at most 64", len(got))
	}
	if _, ok := got[overKey]; ok {
		t.Error("an over-long key survived")
	}
	if v, ok := got["bigvalue"]; ok {
		t.Errorf("an over-long value survived (%d bytes)", len(v))
	}
	if got["keep"] != "kept" {
		t.Errorf("a valid entry was dropped: %v", got)
	}
}
