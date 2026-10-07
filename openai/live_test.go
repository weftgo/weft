//go:build live

package openai_test

import (
	"os"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest/conformance"
	"github.com/weftgo/weft/openai"
)

// TestConformanceLive runs the suite against the real API. Never in CI
// (no keys there); a release-time manual step:
//
//	OPENAI_API_KEY=... OPENAI_MODEL=gpt-4o-mini go test -tags live ./...
func TestConformanceLive(t *testing.T) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("OPENAI_API_KEY not set; the live suite is a release-time manual step")
	}
	name := os.Getenv("OPENAI_MODEL")
	if name == "" {
		name = "gpt-4o-mini"
	}
	conformance.Run(t, conformance.Caps{Files: true, Sequential: true, Usage: true, Live: true},
		func(t *testing.T, caseName string) core.Model {
			opts := []openai.Option{openai.APIKey(key)}
			if caseName == "max_tokens" {
				opts = append(opts, openai.MaxTokens(16))
			}
			return openai.Model(name, opts...)
		})
}
