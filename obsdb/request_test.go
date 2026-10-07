package obsdb_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/weftgo/weft/obsdb"
)

// The hole vocabulary is ADR 0028 §11's closed table, in its order:
// a new badge is an amendment to that ADR, so this list changes only
// with it (and with Studio's TypeScript twin).
func TestHolesClosedTable(t *testing.T) {
	var got []string
	for _, h := range obsdb.Holes() {
		got = append(got, string(h))
	}
	const want = "truncated stripped redacted max_tokens interrupted gap not_recorded derived hidden compacted"
	if strings.Join(got, " ") != want {
		t.Errorf("Holes() = %s\nwant      %s", strings.Join(got, " "), want)
	}
}

// The reading table (ADR 0028 §10) over a run row.
func TestRequestsHoleReadingTable(t *testing.T) {
	for _, c := range []struct {
		row  obsdb.RunRow
		want obsdb.Hole
	}{
		{obsdb.RunRow{}, obsdb.HoleNotRecorded},
		{obsdb.RunRow{InstructionsHash: "h"}, ""},
		{obsdb.RunRow{InstructionsHash: "h", RequestCount: 3}, ""},
	} {
		if got := c.row.RequestsHole(); got != c.want {
			t.Errorf("%+v.RequestsHole() = %q, want %q", c.row, got, c.want)
		}
	}
}

// One parse for every backend: the content mark, a capped record, and
// a malformed producer's body — derived, its hashes from the attributes.
func TestRecordOfConstructors(t *testing.T) {
	r := obsdb.RequestRecordOf(obsdb.StoredRecord{Index: 2, Step: 1, Content: "stripped",
		Body: []byte(`{"step":1,"attempt":2,"system_hash":"s","tools":{"catalog_hash":"c","names":["a"]}}`)})
	if r.Attempt != 2 || r.SystemHash != "s" || r.CatalogHash != "c" || r.Content != obsdb.HoleStripped {
		t.Errorf("RequestRecordOf = %+v", r)
	}
	if st := obsdb.RequestRecordOf(obsdb.StoredRecord{Body: []byte(`not json`), Content: "stripped", SystemHash: "as"}); st.Content != obsdb.HoleStripped || st.SystemHash != "as" {
		t.Errorf("RequestRecordOf(stripped, undecodable) = %+v; want stripped over derived", st)
	}
	if w := obsdb.RequestRecordOf(obsdb.StoredRecord{Body: []byte(`{"system_hash":"body"}`), SystemHash: "attr"}); w.SystemHash != "attr" {
		t.Errorf("RequestRecordOf(disagreeing) system hash = %q, want the attribute's", w.SystemHash)
	}
	bad := obsdb.RequestRecordOf(obsdb.StoredRecord{Body: []byte(`not json`), Content: "full", SystemHash: "as", CatalogHash: "ac"})
	if bad.Content != obsdb.HoleDerived || string(bad.Raw) != "not json" || bad.SystemHash != "as" || bad.CatalogHash != "ac" {
		t.Errorf("RequestRecordOf(undecodable) = %+v; want derived, raw kept, the attributes' hashes", bad)
	}
	if p := obsdb.PromptRecordOf(obsdb.StoredRecord{Body: []byte(`{"hash":"h","text":"x"}`), TruncatedBytes: 3}); p.Content != obsdb.HoleTruncated || p.Hash != "h" {
		t.Errorf("PromptRecordOf(cut) = %+v", p)
	}
	if p := obsdb.PromptRecordOf(obsdb.StoredRecord{Body: []byte(`{`), SystemHash: "as"}); p.Content != obsdb.HoleDerived || p.Hash != "as" {
		t.Errorf("PromptRecordOf(undecodable) = %+v", p)
	}
	if tr := obsdb.ToolsRecordOf(obsdb.StoredRecord{Body: []byte(`{`), CatalogHash: "ac"}); tr.Content != obsdb.HoleDerived || tr.Hash != "ac" {
		t.Errorf("ToolsRecordOf(undecodable) = %+v", tr)
	}
}

// The zero query reads everything: every step, from index 0, 100 a page.
func TestRequestQueryZeroValue(t *testing.T) {
	var q obsdb.RequestQuery
	if q.Step != nil || q.From != 0 || q.PageLimit() != 100 {
		t.Errorf("zero RequestQuery = %+v, limit %d", q, q.PageLimit())
	}
	if (obsdb.RequestQuery{Limit: 5000}).PageLimit() != 1000 {
		t.Error("PageLimit does not cap at 1000")
	}
}

// A HoleError is an ErrNotFound that names its badge.
func TestHoleErrorIsNotFound(t *testing.T) {
	var err error = &obsdb.HoleError{Kind: "prompt", Hash: "h", Hole: obsdb.HoleStripped}
	var hole *obsdb.HoleError
	if !errors.Is(err, obsdb.ErrNotFound) || !errors.As(err, &hole) || hole.Hole != obsdb.HoleStripped {
		t.Errorf("HoleError = %v; want ErrNotFound carrying stripped", err)
	}
}
