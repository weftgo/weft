package bpset

import (
	"reflect"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

func TestEncodeDecode(t *testing.T) {
	at := time.Date(2026, 10, 27, 9, 30, 0, 123456789, time.UTC)
	for _, c := range []struct {
		name string
		in   obsdb.BreakpointSet
		text string
	}{
		{"an empty set is [] (the prune rules compare with it)", obsdb.BreakpointSet{SetAt: at, SetBy: "api"}, `[]`},
		{"no provenance is the plain array", obsdb.BreakpointSet{Tools: []string{"refund"}}, `["refund"]`},
		{"provenance is the object", obsdb.BreakpointSet{Tools: []string{"refund"}, SetAt: at, SetBy: "runtimes"},
			`{"tools":["refund"],"set_at_ns":1793093400123456789,"set_by":"runtimes"}`},
	} {
		got, err := Encode(c.in)
		if err != nil || got != c.text {
			t.Errorf("%s: Encode = %s, %v; want %s", c.name, got, err, c.text)
		}
	}
}

func TestDecode(t *testing.T) {
	at := time.Date(2026, 10, 27, 9, 30, 0, 123456789, time.UTC)
	for _, c := range []struct {
		text string
		want obsdb.BreakpointSet
	}{
		{`["refund","x"]`, obsdb.BreakpointSet{Tools: []string{"refund", "x"}}},
		{`[]`, obsdb.BreakpointSet{Tools: []string{}}},
		{`null`, obsdb.BreakpointSet{Tools: []string{}}},
		{`{"tools":null}`, obsdb.BreakpointSet{Tools: []string{}}},
		{`{"tools":["a"],"set_at_ns":1793093400123456789,"set_by":"runtimes"}`,
			obsdb.BreakpointSet{Tools: []string{"a"}, SetAt: at, SetBy: "runtimes"}},
		{` {"tools":["a"]}`, obsdb.BreakpointSet{Tools: []string{"a"}}},
	} {
		got, err := Decode(c.text)
		if err != nil || !reflect.DeepEqual(got.Tools, c.want.Tools) || !got.SetAt.Equal(c.want.SetAt) || got.SetBy != c.want.SetBy {
			t.Errorf("Decode(%s) = %+v, %v; want %+v", c.text, got, err, c.want)
		}
	}
	for _, bad := range []string{``, `garbage`, `{"tools":"refund"}`, `[1,2]`, `{"tools":[`} {
		if _, err := Decode(bad); err == nil {
			t.Errorf("Decode(%q) = nil error, want one", bad)
		}
	}
}
