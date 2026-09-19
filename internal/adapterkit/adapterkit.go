// Package adapterkit holds the helpers every first-party adapter needs
// but no vendor SDK touches: schema rendering, the terminal-error
// rule, and the FilePart exactly-one guard. They lived as byte-identical
// copies in all three adapters until the 2026-09-18 review flagged the
// drift risk; they move here rather than into the root's public API —
// THE-END-GOAL principle 3 names internal/ as the tool that keeps the
// guaranteed surface small, and Go's path-based internal rule lets
// github.com/weftgo/weft/{openai,anthropic,google} import the package
// while third-party adapters cannot. Helpers that mention an SDK type
// (the stream readers) stay per-adapter by design (ADR 0013).
package adapterkit

import (
	"context"
	"fmt"

	"github.com/weftgo/weft"
)

// SchemaMap renders a weft.Schema as the plain JSON map the vendors'
// tool-parameter fields expect (openai-go's FunctionParameters,
// anthropic-sdk-go's InputSchema). Nil stays nil: a schema-less tool
// takes the provider's default shape.
func SchemaMap(s *weft.Schema) map[string]any {
	if s == nil {
		return nil
	}
	m := map[string]any{"type": s.Type}
	if s.Format != "" {
		m["format"] = s.Format
	}
	if s.Description != "" {
		m["description"] = s.Description
	}
	if s.Items != nil {
		m["items"] = SchemaMap(s.Items)
	}
	if s.AdditionalProperties != nil {
		m["additionalProperties"] = SchemaMap(s.AdditionalProperties)
	}
	if s.Properties != nil {
		props := make(map[string]any, len(s.Properties))
		for k, v := range s.Properties {
			props[k] = SchemaMap(v)
		}
		m["properties"] = props
	}
	if len(s.Required) > 0 {
		m["required"] = s.Required
	}
	return m
}

// TerminalErr reports a stream's terminal error the way the Model
// contract expects: ctx.Err() when the caller's context ended (the
// vendor SDKs wrap cancellation in their own error types, and the
// reader goroutine can exit its handshake with a nil SDK error), the
// error unchanged otherwise — so callers can errors.As the SDK's type.
func TerminalErr(ctx context.Context, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	return err
}

// NextCallID synthesises the id for a streamed tool call whose server
// omitted one: "call_<n>" with n starting at ordinal+1, skipping ids
// the step has already claimed and marking the chosen id in used.
// Skipping matters: a repeated id within one step fails the run with
// ErrModelContract — the exact failure the synthesis exists to
// prevent — when a server populates some ids of the step itself
// (llama.cpp-style) with the same call_<n> shape.
func NextCallID(used map[string]bool, ordinal int) string {
	for i := ordinal; ; i++ {
		id := fmt.Sprintf("call_%d", i+1)
		if !used[id] {
			used[id] = true
			return id
		}
	}
}

// FilePartSource validates that a FilePart sets exactly one of Data and
// URL — the shape every provider's file input takes. Both or neither is
// a caller bug, refused wrapping ErrUnsupported before any request.
func FilePartSource(p weft.FilePart) error {
	if (len(p.Data) == 0) == (p.URL == "") {
		return fmt.Errorf("%w: a file part must set exactly one of Data or URL", weft.ErrUnsupported)
	}
	return nil
}
