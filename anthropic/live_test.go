//go:build live

package anthropic_test

import (
	"os"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/anthropic"
	"github.com/weftgo/weft/wefttest/conformance"
)

// TestConformanceLive runs the suite against the real API. Never in CI
// (no keys there); a release-time manual step:
//
//	ANTHROPIC_API_KEY=... ANTHROPIC_MODEL=claude-... go test -tags live ./...
func TestConformanceLive(t *testing.T) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("ANTHROPIC_API_KEY not set; the live suite is a release-time manual step")
	}
	name := os.Getenv("ANTHROPIC_MODEL")
	if name == "" {
		name = "claude-sonnet-4-5"
	}
	conformance.Run(t, conformance.Caps{
		Reasoning:  true,
		Files:      true,
		Sequential: true,
		Usage:      true,
		Live:       true,
	}, func(t *testing.T, caseName string) weft.Model {
		opts := []anthropic.Option{anthropic.APIKey(key), anthropic.Thinking(true)}
		if caseName == "max_tokens" {
			opts = append(opts, anthropic.MaxTokens(16))
		}
		return anthropic.Model(name, opts...)
	})
}
