package main

import (
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

// The committed weft.json is the generated view of this agent. A change
// to the agent or a tool without regenerating (go test -update) fails
// here — the manifest is a gated artifact, never hand-edited.
func TestManifest(t *testing.T) {
	b, err := weft.Manifest(newSupportAgent())
	if err != nil {
		t.Fatal(err)
	}
	wefttest.Golden(t, "weft.json", b)
}
