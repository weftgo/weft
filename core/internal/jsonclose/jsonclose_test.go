package jsonclose

import "testing"

// The two consumers (partial_json.go, mw/repairjson.go) carry the
// behavioural weight; this pins the contract the package itself
// promises — structure-only closing, attempt-always-returned.
func TestClose(t *testing.T) {
	cases := []struct {
		in     string
		closed string
		valid  bool
	}{
		{`{"a":1`, `{"a":1}`, true},
		{`{"a":`, `{"a":null}`, true},
		{`{"a":1,`, `{"a":1}`, true},
		{`{"a":1,,`, `{"a":1}`, true},
		{`{"a":"b`, `{"a":"b"}`, true},
		{`{"a":"b\`, `{"a":"b\\"}`, true},
		{`{"a":[1,{"b":2`, `{"a":[1,{"b":2}]}`, true},
		{`{"a":1} garbage`, `{"a":1} garbage`, false},
		{`{"a":tru`, `{"a":tru}`, false},
	}
	for _, c := range cases {
		got, valid := Close(c.in)
		if got != c.closed || valid != c.valid {
			t.Errorf("Close(%q) = (%q, %v), want (%q, %v)", c.in, got, valid, c.closed, c.valid)
		}
	}
}
