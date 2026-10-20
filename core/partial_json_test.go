package core

import (
	"encoding/json"
	"math/rand/v2"
	"strconv"
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
		{`{"a":1,"b":tru`, `{"a":1,"b":null}`}, // a bare literal cut mid-token: the token goes, the key closes as null
		{`{"a":1,"b":1e`, `{"a":1,"b":1}`},     // a number keeps its longest valid prefix
		{`{"a":1,"b":-`, `{"a":1,"b":null}`},
		{`{"a":[1,2,tru`, `{"a":[1,2]}`},                                    // an open array keeps its complete elements (0.18.1)
		{`{"a":1,"l":[{"x":1},{"x`, `{"a":1,"l":[{"x":1},{}]}`},             // a key cut mid-string: the innermost member goes
		{`{"a":1,"l":[{"x":1},{"x":2,"y"`, `{"a":1,"l":[{"x":1},{"x":2}]}`}, // a key with no colon yet
		{`{"a":"x\u00`, `{"a":"x"}`},                                        // a string cut inside a \u escape
		{`{"a":"xé`, `{"a":"xé"}`},
		{`[1,2,`, `[1,2]`},
		{`[1,2`, `[1,2]`},
		{`{"a":"x\\"`, `{"a":"x\\"}`}, // dangling escape closed before the quote
		// A string's tail that a later delta would take back is held
		// back instead (0.18.1: each flickered — a backslash, then the
		// escape it began; U+FFFD, then the rune).
		{`{"a":"x\`, `{"a":"x"}`},       // a lone backslash: its escape has not arrived
		{`{"a":"x\\\`, `{"a":"x\\"}`},   // an odd run: the last one dangles
		{`{"a":"x\\`, `{"a":"x\\"}`},    // an even run: one whole escaped backslash
		{"{\"a\":\"x\xc3", `{"a":"x"}`}, // a rune cut mid-sequence
		{"{\"a\":\"x\xf0\x9f\x8e", `{"a":"x"}`},
		{`{"a":"x\ud83d`, `{"a":"x"}`}, // a high surrogate waits for its pair
		{`{"a":"x\ud83d\`, `{"a":"x"}`},
		{`{"a":"x\ud83d\ude`, `{"a":"x"}`},
		{`{"a":"x😀`, `{"a":"x😀"}`},
		// Only a \u escape waits for a low surrogate: a \\, \n or \t
		// followed by four hex characters is whole text (0.18.1-fix3:
		// C:\DBF, then C:).
		{`{"p":"C:\\DBFF`, `{"p":"C:\\DBFF"}`},
		{`{"p":"C:\\DBFF\\x"}`, `{"p":"C:\\DBFF\\x"}`},
		{`{"n":"a\nDAD0`, `{"n":"a\nDAD0"}`},
		{`{"a":"x\nD83D`, `{"a":"x\nD83D"}`},
		{`{"a":"x\tD800`, `{"a":"x\tD800"}`},
		{`{"a":1}}`, `{"a":1}`}, // garbage after the close: keep the valid prefix
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
// (TODO §2a.6) — and, from one truncation to the next, never loses a
// field or an array element the shorter one had, never changes a
// scalar's type, and grows a string only by prefix (grownFrom): the
// decoder's partials are monotonic (0.18.1), whatever the chunking.
func FuzzPartialJSON(f *testing.F) {
	for _, seed := range []string{
		`{"name":"Ada","count":4}`,
		`{"a":{"b":[1,2,{"c":true}]},"d":null}`,
		`{"esc":"a\"b\\c","t":1e5}`,
		`[1,2,3]`,
		`{"unicode":"héllo","emoji":"🎮"}`,
		`{"destination":"Lisbon","nights":3,"legs":[{"from":"AMS","to":"LIS","flight":"KL1691"},{"from":"LIS","to":"AMS","flight":"KL1692"}]}`,
		`{"l":[true,false,null,-1.5e+3,"é"],"n":[[1],[{"k":[]}]]}`,
		// Strings whose cuts end in a dangling escape, a cut rune or
		// an unpaired high surrogate (0.18.1).
		`{"a":"x\ny\\z\"wé😀","b":[true,false,null],"c":-1.5e+3}`,
		`{"s":"\\\\\\","t":"a\\"}`,
		`{"pair":"😀 é","k🎮":1}`,
		`{"r":"é😀🎮","l":["ü","\/"]}`,
		// A non-u escape followed by four hex characters in D800–DBFF
		// is no high surrogate (0.18.1-fix3).
		`{"p":"C:\\DBFF\\x"}`,
		`{"n":"a\nDAD0b"}`,
		`{"a":"x\nD83Dz"}`,
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
		var prev any
		for i := 0; i <= len(data); i++ {
			part := closedPrefix(data[:i])
			if part == "" {
				// Nothing is allowed only while nothing was had: "{" closes
				// to {}, `{"na` to nothing (no partial yet) — the decoder
				// keeps its last good partial across it.
				if prev != nil && !grownFrom(prev, map[string]any{}) && !grownFrom(prev, []any{}) {
					t.Fatalf("prefix %q of %q closes to nothing after an earlier prefix closed to %#v", data[:i], data, prev)
				}
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
			if prev != nil && !grownFrom(prev, partDoc) {
				t.Fatalf("prefix %q of %q shrank the partial:\n%#v\nthen\n%#v", data[:i], data, prev, partDoc)
			}
			prev = partDoc
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

// grownFrom reports whether next keeps everything prev had: every key
// of an object (recursively grown), every element of an array (the
// array no shorter, each element grown) and every scalar's type: a
// string grows only by prefix, a bool never changes, a number stays a
// number (its value may grow, 1 → 1e5), a dangling key's null fills
// with anything (0.18.1: strings were not held to a prefix).
// (contract_test.go's twin holds the public decoder to the same rule.)
func grownFrom(prev, next any) bool {
	switch p := prev.(type) {
	case map[string]any:
		n, ok := next.(map[string]any)
		if !ok {
			return false
		}
		for k, v := range p {
			nv, ok := n[k]
			if !ok || !grownFrom(v, nv) {
				return false
			}
		}
		return true
	case []any:
		n, ok := next.([]any)
		if !ok || len(n) < len(p) {
			return false
		}
		for i := range p {
			if !grownFrom(p[i], n[i]) {
				return false
			}
		}
		return true
	case string:
		// A string only grows by appending: never a byte that a
		// later delta takes back (a dangling escape, a cut rune).
		n, ok := next.(string)
		return ok && strings.HasPrefix(n, p)
	case bool:
		return next == p
	case float64:
		// A number still being written may change value (1 → 1e5),
		// never type.
		_, ok := next.(float64)
		return ok
	default:
		return true // null: a dangling key fills with any value
	}
}

// Every truncation of a generated document closes to a partial grown
// from the previous one by grownFrom's strict rule — strings only by
// prefix — whatever its escapes, multi-byte runes and nesting
// (0.18.1: a lone trailing backslash closed as an escaped one, a cut
// rune as U+FFFD, a lone high surrogate as U+FFFD, each taken back by
// the next byte). FuzzPartialJSON holds the same rule over arbitrary
// documents; this is its deterministic, always-run sweep.
func TestClosedPrefixStringsGrowByPrefix(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 9))
	n := 20000
	if testing.Short() {
		n = 2000
	}
	for range n {
		doc := `{"root":` + genPartialDoc(r, 0) + "}"
		if !json.Valid([]byte(doc)) {
			t.Fatalf("generator wrote invalid JSON %q", doc)
		}
		var prev any
		for i := 0; i <= len(doc); i++ {
			part := closedPrefix(doc[:i])
			if part == "" {
				continue
			}
			var v any
			if err := json.Unmarshal([]byte(part), &v); err != nil {
				t.Fatalf("prefix %q of %q closes to undecodable %q", doc[:i], doc, part)
			}
			if prev != nil && !grownFrom(prev, v) {
				t.Fatalf("prefix %q of %q took back what an earlier one had:\n%#v\nthen\n%#v", doc[:i], doc, prev, v)
			}
			prev = v
		}
	}
}

// genPartialDoc writes a random JSON value, whitespace and all, its
// strings built from escapes, multi-byte runes and surrogate pairs.
func genPartialDoc(r *rand.Rand, depth int) string {
	k := r.IntN(8)
	if depth > 5 {
		k = r.IntN(4)
	}
	ws := func() string { return []string{"", "", "", " ", "\n  "}[r.IntN(5)] }
	switch k {
	case 0:
		return []string{"true", "false", "null"}[r.IntN(3)]
	case 1:
		return []string{"-1", "0", "12.5", "-3e+2", "1E-7", "123456", "-0.0e0"}[r.IntN(7)]
	case 2, 3:
		return genPartialString(r)
	case 4, 5:
		var parts []string
		for range r.IntN(4) {
			parts = append(parts, ws()+genPartialDoc(r, depth+1)+ws())
		}
		return "[" + strings.Join(parts, ",") + "]"
	default:
		var parts []string
		for i := range r.IntN(4) {
			key := `"k` + strconv.Itoa(i) + genPartialString(r)[1:]
			parts = append(parts, ws()+key+ws()+":"+ws()+genPartialDoc(r, depth+1)+ws())
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
}

// genPartialString writes a JSON string from pieces a streamed one is
// cut inside: escapes of every kind (\uXXXX ones included — surrogate
// pairs written as escapes, lone high and low surrogates, a high one
// doubled), a non-u escape followed by four hex characters (the
// shape of a high surrogate escape that is not one: C:\\DBFF), raw
// multi-byte runes and invalid UTF-8.
func genPartialString(r *rand.Rand) string {
	pieces := []string{
		"a", "bc", `\n`, `\\`, `\"`, `\t`, `\/`, `é`, `😀`, "é", "😀", "🎮", ",", ":", "{", "]",
		`\u00e9`, `\u0041`, `\uD83D\uDE00`, `\uDBFF\uDFFF`, `\uD83D`, `\uDE00`, `\uD83D\uD83D`,
		`\\DBFF`, `\nDAD0`, `\tD800`, `\\uD83D`, `\\\\`, "D83D", "uDBFF",
		"\xff", "\xe2\x82", "\xc0\xaf", "\xed\xa0\x80", "\xf0\x9f\x98",
	}
	var b strings.Builder
	b.WriteByte('"')
	for range r.IntN(7) {
		b.WriteString(pieces[r.IntN(len(pieces))])
	}
	b.WriteByte('"')
	return b.String()
}
