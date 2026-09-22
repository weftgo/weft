package mw

import (
	"context"
	"encoding/json"
	"iter"
	"strings"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/internal/jsonclose"
)

// RepairJSON re-encodes a tool call's arguments once when they are not
// valid JSON — the common damage is a reply cut mid-object or wrapped
// in a Markdown fence. Unterminated strings, objects, and arrays are
// closed and fences stripped; when the result parses, it replaces the
// original. Arguments that cannot be repaired pass through unchanged
// and fail decoding as they would have, so the model still sees an
// INVALID_INPUT result. Only tool-call events are touched.
func RepairJSON() weft.ModelMiddleware {
	return func(next weft.Model) weft.Model {
		return &repairModel{next: next}
	}
}

type repairModel struct{ next weft.Model }

func (m *repairModel) Info() weft.ModelInfo { return weft.InfoOf(m.next) }

func (m *repairModel) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	return func(yield func(weft.ModelEvent, error) bool) {
		for ev, err := range m.next.Stream(ctx, req) {
			if err != nil {
				yield(nil, err)
				return
			}
			if call, ok := ev.(weft.ModelToolCall); ok && !json.Valid(call.Args) {
				if fixed, ok := repairJSON(string(call.Args)); ok {
					call.Args = json.RawMessage(fixed)
					ev = call
				}
			}
			if !yield(ev, nil) {
				return
			}
		}
	}
}

// repairJSON attempts one mechanical repair of s. The first return is
// the attempt — the stripped-and-closed text, handed back even when
// it still is not valid JSON, so payload content survives byte-for-
// byte — and the second reports whether the result is valid JSON.
func repairJSON(s string) (string, bool) {
	s = strings.TrimSpace(s)
	// Markdown fences: ```json ... ```, ```javascript ... ```, ``` ... ```.
	// A language tag is letters followed by whitespace or the newline;
	// any tag is stripped, and anything else survives — an untagged
	// fence whose payload starts with the literal letters "json" must
	// not lose them (TrimPrefix("json") ate exactly that).
	if rest, ok := strings.CutPrefix(s, "```"); ok {
		if head, tail, ok := strings.Cut(rest, "\n"); ok {
			if head = strings.TrimRight(head, " \t\r"); head != "" && isASCIILetters(head) {
				rest = tail
			}
		}
		rest = strings.TrimSuffix(strings.TrimSpace(rest), "```")
		s = strings.TrimSpace(rest)
	}
	if s == "" {
		return "{}", true
	}
	if json.Valid([]byte(s)) {
		return s, true
	}
	// Close what is open — the state machine the OutputDecoder's
	// prefix salvage shares (internal/jsonclose).
	return jsonclose.Close(s)
}

// isASCIILetters reports whether s is one or more ASCII letters — the
// shape of a fenced block's language tag (json, JSON, javascript).
func isASCIILetters(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}
