package obsdb_test

import (
	"errors"
	"strings"
	"testing"
	"time"

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

// One parse for every backend: the content mark, the cut in any of
// its stored spellings, a body that does not decode.
func TestRecordOfConstructors(t *testing.T) {
	for _, attrs := range []map[string]any{
		{"weft.content.truncated_bytes": int64(7)},
		{"weft.content.truncated_bytes": float64(7)},
		{"weft.content.truncated_bytes": "7"},
	} {
		if _, cut := obsdb.RecordContent(attrs); cut != 7 {
			t.Errorf("RecordContent(%v) cut = %d, want 7", attrs, cut)
		}
	}
	if mark, cut := obsdb.RecordContent(map[string]any{"weft.content": "stripped"}); mark != "stripped" || cut != 0 {
		t.Errorf("RecordContent(stripped) = %q, %d", mark, cut)
	}
	r := obsdb.RequestRecordOf(2, 1, time.Time{}, []byte(`{"step":1,"attempt":2,"system_hash":"s","tools":{"catalog_hash":"c","names":["a"]}}`), "stripped", 0)
	if r.Attempt != 2 || r.SystemHash != "s" || r.CatalogHash != "c" || r.Content != obsdb.HoleStripped {
		t.Errorf("RequestRecordOf = %+v", r)
	}
	if bad := obsdb.RequestRecordOf(0, 0, time.Time{}, []byte(`not json`), "full", 0); bad.Content != "" || string(bad.Raw) != "not json" || bad.SystemHash != "" {
		t.Errorf("RequestRecordOf(undecodable) = %+v; want the raw body kept, fields zero", bad)
	}
	if p := obsdb.PromptRecordOf(0, time.Time{}, []byte(`{"hash":"h","text":"x"}`), 3); p.Content != obsdb.HoleTruncated || p.Hash != "h" {
		t.Errorf("PromptRecordOf(cut) = %+v", p)
	}
	cats := obsdb.UniqueCatalogs([]obsdb.ToolsRecord{{Index: 0, Hash: "a"}, {Index: 1, Hash: "b"}, {Index: 2, Hash: "a"}})
	if len(cats) != 2 || cats[0].Index != 0 || cats[1].Index != 1 {
		t.Errorf("UniqueCatalogs = %+v", cats)
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
