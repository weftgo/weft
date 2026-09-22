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
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	"github.com/weftgo/weft"
)

// SchemaMap renders a weft.Schema as the plain JSON map the vendors'
// tool-parameter fields expect (openai-go's FunctionParameters,
// anthropic-sdk-go's InputSchema). Nil stays nil: a schema-less tool
// takes the provider's default shape. The rendering goes through
// json.Marshal, which honours Schema.MarshalJSON — so a schema parsed
// with weft.ParseSchema reaches the provider as its verbatim foreign
// bytes (enum, oneOf and all), where the hand-built map this replaced
// degraded them to the struct's own vocabulary. One rendering change
// rode along: an unconstrained node (a recursion cut, an interface
// field) marshalled as {"type": ""} before and is {} now (ADR 0003's
// 2026-09-19 amendment).
func SchemaMap(s *weft.Schema) map[string]any {
	if s == nil {
		return nil
	}
	b, err := json.Marshal(s)
	if err != nil {
		// Unreachable: every Schema field is a string, slice, or map,
		// and ParseSchema validated the raw bytes before storing them.
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
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

// MergeBody deep-merges extra into a JSON request body and returns the
// re-encoded bytes: nested maps merge recursively, every other value —
// scalars, arrays, null — replaces. The caller's key wins on conflict
// at every level; this is the one semantic all three adapters'
// ExtraBody options share, matching genai's native recursiveMapMerge
// (ADR 0013's 2026-09-22 amendment). A body that is not a JSON object
// fails, rather than being silently replaced.
func MergeBody(body []byte, extra map[string]any) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("request body is not a JSON object: %w", err)
	}
	mergeMaps(payload, extra)
	return json.Marshal(payload)
}

// mergeMaps folds src into dest with caller-wins semantics: map values
// merge recursively, everything else replaces.
func mergeMaps(dest, src map[string]any) {
	for k, v := range src {
		if m, ok := v.(map[string]any); ok {
			if d, ok := dest[k].(map[string]any); ok {
				mergeMaps(d, m)
				continue
			}
		}
		dest[k] = v
	}
}

// CloneJSON deep-copies a JSON-shaped value — the container shapes
// MergeBody recurses through (maps and slices), with everything else
// returned as is: scalars and structs are copied by their interface
// conversion already, and only the decoded-JSON shapes alias. The
// ExtraBody options snapshot the caller's map at construction with
// this, so a Model stays safe for concurrent runs while the caller
// keeps mutating what it passed (ADR 0013's Models-are-immutable
// stance).
func CloneJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = CloneJSON(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = CloneJSON(val)
		}
		return out
	default:
		return v
	}
}

// CloneHeaders copies h with each value slice cloned — ExtraHeaders'
// construction-time snapshot, the same isolation CloneJSON gives
// ExtraBody.
func CloneHeaders(h http.Header) http.Header {
	if h == nil {
		return nil
	}
	out := make(http.Header, len(h))
	for k, vs := range h {
		out[k] = slices.Clone(vs)
	}
	return out
}
