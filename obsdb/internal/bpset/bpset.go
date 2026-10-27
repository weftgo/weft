// Package bpset is the breakpoints row's one text form, shared by
// obsdb's two backends (obsdb.BreakpointSet): the set's JSON in the
// tools column.
//
//   - An empty set is "[]" — what PruneCommands' rules compare with, so
//     a cleared set is pruned as before.
//   - A set with no provenance (no SetAt, no SetBy) is the plain array
//     every Studio before U30.2 wrote.
//   - Otherwise an object: {"tools":[…],"set_at_ns":N,"set_by":"…"}.
//
// Decode reads all three (a row an older Studio wrote reads with a
// zero SetAt and an empty SetBy).
package bpset

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/weftgo/weft/obsdb"
)

type object struct {
	Tools   []string `json:"tools"`
	SetAtNs int64    `json:"set_at_ns,omitempty"`
	SetBy   string   `json:"set_by,omitempty"`
}

// Encode is set's column text.
func Encode(set obsdb.BreakpointSet) (string, error) {
	tools := set.Tools
	if tools == nil {
		tools = []string{}
	}
	if len(tools) == 0 || (set.SetAt.IsZero() && set.SetBy == "") {
		b, err := json.Marshal(tools)
		return string(b), err
	}
	o := object{Tools: tools, SetBy: set.SetBy}
	if !set.SetAt.IsZero() {
		o.SetAtNs = set.SetAt.UnixNano()
	}
	b, err := json.Marshal(o)
	return string(b), err
}

// Decode reads a column text Encode (or an older Studio) wrote. Tools
// is never nil.
func Decode(s string) (obsdb.BreakpointSet, error) {
	var out obsdb.BreakpointSet
	if strings.HasPrefix(strings.TrimSpace(s), "{") {
		var o object
		if err := json.Unmarshal([]byte(s), &o); err != nil {
			return out, err
		}
		out.Tools, out.SetBy = o.Tools, o.SetBy
		if o.SetAtNs != 0 {
			out.SetAt = time.Unix(0, o.SetAtNs).UTC()
		}
	} else if err := json.Unmarshal([]byte(s), &out.Tools); err != nil {
		return out, err
	}
	if out.Tools == nil {
		out.Tools = []string{}
	}
	return out, nil
}
