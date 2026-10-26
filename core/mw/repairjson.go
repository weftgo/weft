package mw

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
	"unicode"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/internal/jsonclose"
)

// RepairJSON re-encodes a tool call's arguments once when they are not
// valid JSON — the common damage is a reply cut mid-object or wrapped
// in a Markdown fence. Unterminated strings, objects, and arrays are
// closed and fences stripped; when the result parses, it replaces the
// original. Whitespace around the arguments is trimmed, but at a cut
// inside an open string only JSON whitespace (space, tab, CR, LF):
// any other rune there, Unicode whitespace included, is the model's
// content and reaches the tool byte-for-byte. Arguments that cannot be
// repaired pass through unchanged and fail decoding as they would
// have, so the model still sees an INVALID_INPUT result. Only
// tool-call events are touched.
func RepairJSON() core.ModelMiddleware {
	return func(next core.Model) core.Model {
		return &repairModel{next: next}
	}
}

type repairModel struct{ next core.Model }

func (m *repairModel) Info() core.ModelInfo { return core.InfoOf(m.next) }

// Describe names the layer for the manifest (core.MiddlewareInfo).
func (m *repairModel) Describe() core.MiddlewareInfo {
	return core.MiddlewareInfo{Name: "mw.RepairJSON"}
}

// Unwrap declares the model whose JSON it repairs (the Unwrap
// convention, beside Info).
func (m *repairModel) Unwrap() core.Model { return m.next }

func (m *repairModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return func(yield func(core.ModelEvent, error) bool) {
		for ev, err := range m.next.Stream(ctx, req) {
			if err != nil {
				yield(nil, err)
				return
			}
			if call, ok := ev.(core.ModelToolCall); ok && !json.Valid(call.Args) {
				if fixed, kind, ok := repairJSONKind(string(call.Args)); ok {
					// Reported on the run's record (core.Reporter.Repair:
					// the chat span's weft.repair event, the call's
					// weft.tool.args_repaired) — reporting only.
					core.ReportFromContext(ctx).Repair(core.RepairInfo{
						CallID: call.ID, Kind: kind, Original: call.Args, Repaired: []byte(fixed)})
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
//
// Whitespace outside the document (before it, after a document that
// closes, around a fence's payload) is trimmed as unicode.IsSpace
// reads it, as in 0.18.0: NBSP before ``` still opens a fence. At a
// cut inside an open string only JSON whitespace (space, tab, CR, LF)
// goes: a raw NBSP, NEL, U+2028/2029, U+2000-200A or U+3000 there is
// payload (0.18.0's strings.TrimSpace took it, and the tool got
// "prix" for "prix\u00a0").
func repairJSON(s string) (string, bool) {
	fixed, _, ok := repairJSONKind(s)
	return fixed, ok
}

// The repair kinds RepairJSON reports (core.RepairInfo.Kind).
const (
	repairFence        = "fence"
	repairUnterminated = "unterminated"
	repairBoth         = "fence+unterminated"
	repairTrim         = "trim"
)

// repairJSONKind is repairJSON with the kind of repair it made: a fence
// stripped, open values closed, both, or only whitespace trimmed (or an
// empty argument made {}).
func repairJSONKind(s string) (string, string, bool) {
	fenced := false
	s = trimOutside(s)
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
		rest = strings.TrimSuffix(trimOutside(rest), "```")
		s = trimOutside(rest)
		fenced = true
	}
	kind := repairTrim
	if fenced {
		kind = repairFence
	}
	if s == "" {
		return "{}", kind, true
	}
	if json.Valid([]byte(s)) {
		return s, kind, true
	}
	// Close what is open — the state machine the OutputDecoder's
	// prefix salvage shares (internal/jsonclose).
	if fenced {
		kind = repairBoth
	} else {
		kind = repairUnterminated
	}
	closed, ok := jsonclose.Close(s)
	return closed, kind, ok
}

// trimOutside trims the whitespace around s that no JSON string holds:
// any Unicode whitespace in front (nothing has opened yet), and behind
// it unless s ends inside an open string, where the cut's tail is the
// string's content and only JSON's own whitespace (RFC 8259 §2: space,
// tab, CR, LF) goes — the rule jsonclose.Close keeps when it closes.
func trimOutside(s string) string {
	s = strings.TrimLeftFunc(s, unicode.IsSpace)
	if jsonclose.OpenString(s) {
		return strings.TrimRight(s, " \t\r\n")
	}
	return strings.TrimRightFunc(s, unicode.IsSpace)
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
