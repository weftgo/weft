package weft

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClosedPrefix(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{``, ``},
		{`{"a":1}`, `{"a":1}`},            // already valid: unchanged
		{`{"a":1`, `{"a":1}`},             // close-only: member is whole
		{`{"a":1,`, `{"a":1}`},            // dangling comma trimmed
		{`{"a":1,"b":2`, `{"a":1,"b":2}`}, // closes whole: the 2 may grow, fields do not
		{`{"a":1,"b":2,`, `{"a":1,"b":2}`},
		{`{"a":{"x":1},"b":"te`, `{"a":{"x":1},"b":"te"}`}, // open string closes over its content
		{`{"a":"he`, `{"a":"he"}`},
		{`{"a":1,"b":{"c":2`, `{"a":1,"b":{"c":2}}`},
		{`{"a":1,"b":tru`, `{"a":1}`}, // a bare literal cut mid-token cannot close: member dropped
		{`{"a":1,"b":1e`, `{"a":1}`},
		{`[1,2,`, `[1,2]`},
		{`[1,2`, `[1,2]`},
		{`{"a":"x\\"`, `{"a":"x\\"}`}, // dangling escape closed before the quote
		{`{"a":1}}`, `{"a":1}`},       // garbage after the close: keep the valid prefix
		{`not json`, ``},
		{`12`, ``}, // scalars never carry tool args
	}
	for _, tc := range cases {
		if got := closedPrefix(tc.in); got != tc.want {
			t.Errorf("closedPrefix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// FuzzPartialJSON checks the prefix closer: any byte string must not
// panic, and for any decodable JSON document, the closed prefix of a
// truncation never decodes to more populated fields than the closed
// prefix of the whole string — the decoder's filling-form promise
// (TODO §2a.6).
func FuzzPartialJSON(f *testing.F) {
	for _, seed := range []string{
		`{"name":"Ada","count":4}`,
		`{"a":{"b":[1,2,{"c":true}]},"d":null}`,
		`{"esc":"a\"b\\c","t":1e5}`,
		`[1,2,3]`,
		`{"unicode":"héllo","emoji":"🎮"}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data string) {
		var whole any
		if err := json.Unmarshal([]byte(data), &whole); err != nil {
			return // not JSON: only the no-panic guarantee applies
		}
		if hasDuplicateKeys(data) {
			// Duplicate keys collapse last-wins in Go's decoder, so a
			// truncation can hold the first value where the whole holds
			// the second — the property assumes unique keys, as any
			// real submit_output document has.
			return
		}
		full := closedPrefix(data)
		if full == "" {
			return
		}
		var fullDoc any
		if err := json.Unmarshal([]byte(full), &fullDoc); err != nil {
			t.Fatalf("closedPrefix(%q) = %q, which does not decode: %v", data, full, err)
		}
		for i := 0; i <= len(data); i++ {
			part := closedPrefix(data[:i])
			if part == "" {
				continue
			}
			var partDoc any
			if err := json.Unmarshal([]byte(part), &partDoc); err != nil {
				t.Fatalf("closedPrefix(%q) = %q, which does not decode: %v", data[:i], part, err)
			}
			if !subsetOf(partDoc, fullDoc) {
				t.Fatalf("prefix %q of %q decodes to more than the full closed prefix %q:\n%#v\nvs\n%#v",
					part, data, full, partDoc, fullDoc)
			}
		}
	})
}

// hasDuplicateKeys walks the JSON token stream and reports whether any
// object repeats a key. Go's decoder collapses duplicates last-wins,
// which would make the subset property compare a truncation's first
// value against the whole document's second.
func hasDuplicateKeys(data string) bool {
	type frame struct {
		seen  map[string]bool
		isKey bool // objects: the next string token is a key
		isAry bool
	}
	dec := json.NewDecoder(strings.NewReader(data))
	var stack []*frame
	for {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{':
				stack = append(stack, &frame{seen: map[string]bool{}, isKey: true})
			case '[':
				stack = append(stack, &frame{isAry: true})
			case '}', ']':
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				if len(stack) > 0 && !stack[len(stack)-1].isAry {
					stack[len(stack)-1].isKey = true // a value just completed
				}
			}
		case string:
			if len(stack) > 0 {
				f := stack[len(stack)-1]
				if f.isKey && !f.isAry {
					if f.seen[t] {
						return true
					}
					f.seen[t] = true
				}
				f.isKey = !f.isKey
			}
		default:
			if len(stack) > 0 {
				f := stack[len(stack)-1]
				f.isKey = !f.isKey
			}
		}
	}
}

// subsetOf reports whether every field present in part is present in
// whole with a subset value (recursively); arrays compare by length.
func subsetOf(part, whole any) bool {
	switch p := part.(type) {
	case map[string]any:
		w, ok := whole.(map[string]any)
		if !ok {
			return false
		}
		for k, v := range p {
			wv, ok := w[k]
			if !ok || !subsetOf(v, wv) {
				return false
			}
		}
		return true
	case []any:
		w, ok := whole.([]any)
		return ok && len(p) <= len(w)
	default:
		return true // a scalar present in part: whole carries some value
	}
}
