//go:build live

package google_test

import (
	"os"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/google"
	"github.com/weftgo/weft/wefttest/conformance"
)

// TestConformanceLive runs the suite against the real API. Never in CI
// (no keys there); a release-time manual step:
//
//	GEMINI_API_KEY=... GEMINI_MODEL=gemini-2.5-flash go test -tags live ./...
func TestConformanceLive(t *testing.T) {
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		key = os.Getenv("GOOGLE_API_KEY")
	}
	if key == "" {
		t.Skip("GEMINI_API_KEY / GOOGLE_API_KEY not set; the live suite is a release-time manual step")
	}
	name := os.Getenv("GEMINI_MODEL")
	if name == "" {
		name = "gemini-2.5-flash"
	}
	conformance.Run(t, conformance.Caps{
		Reasoning: true,
		Files:     true,
		Usage:     true,
		Live:      true,
	}, func(t *testing.T, caseName string) weft.Model {
		opts := []google.Option{google.APIKey(key)}
		if caseName == "max_tokens" {
			opts = append(opts, google.MaxTokens(16))
		}
		return google.Model(name, opts...)
	})
}
