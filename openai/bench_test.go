package openai

import (
	"context"
	"testing"

	"github.com/weftgo/weft"
)

// benchTool has a mid-sized schema (ten properties, one nested object,
// one array) — enough structure that conversion cost is not just one
// map allocation.
type benchInner struct {
	ID   string `json:"id"`
	Tags []int  `json:"tags,omitempty"`
}

type benchIn struct {
	A     string       `json:"a"`
	B     string       `json:"b"`
	C     string       `json:"c"`
	D     string       `json:"d"`
	E     string       `json:"e"`
	F     string       `json:"f"`
	G     string       `json:"g"`
	Inner benchInner   `json:"inner"`
	List  []benchInner `json:"list,omitempty"`
}

// BenchmarkConvertTool measures what the per-request conversion the
// 2026-09-18 cache deletion rode on costs, against the network round
// trip every request also pays: single-digit microseconds (see
// CHANGELOG). The cache it replaced was a sync.Map keyed by *ToolDef
// pointer and never evicted — an unbounded leak for a ToolSource that
// rebuilds its snapshot per step, the exact scenario ToolSource exists
// for (ADR 0013's amendment).
func BenchmarkConvertTool(b *testing.B) {
	def := weft.Tool("bench", "", func(_ context.Context, _ benchIn) (string, error) {
		return "", nil
	})
	b.ResetTimer()
	for b.Loop() {
		convertTool(def)
	}
}
