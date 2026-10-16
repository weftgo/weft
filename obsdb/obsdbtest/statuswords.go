package obsdbtest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// statusWords pins the read-time status words on every backend (I3,
// issue 003; docs/record-contract.md §6): RunQuery.Status parked lists
// the open parks — succeeded, pending > 0, no run whose resumed_from
// names it — and cancelled the failed runs whose error_type is
// cancelled; the four derived words are unchanged; Runs and Search
// agree; and obsdb.ParseStatus refuses a word outside StatusWords.
func statusWords(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		old := time.Now().UTC().Add(-2 * obsdb.InterruptedAfter)
		var n byte
		root := func(id string, status int, extra map[string]any) obsdb.Span {
			n++
			s := invokeSpan(id, status, extra)
			s.TraceID, s.SpanID = "d"+string('0'+n)+s.TraceID[2:], "740000000000000"+string('0'+n)
			s.Start, s.End = old.Add(-time.Second), old
			return s
		}
		b := obsdb.Batch{Spans: []obsdb.Span{
			root("sw_open", 1, map[string]any{"weft.run.pending": int64(1)}),
			root("sw_decided", 1, map[string]any{"weft.run.pending": int64(2)}),
			root("sw_resumer", 1, map[string]any{"weft.resumed_from": "sw_decided"}),
			root("sw_cancel", 2, map[string]any{"error.type": "context.Canceled"}),
			root("sw_maxsteps", 2, map[string]any{"error.type": "max_steps"}),
			root("sw_done", 1, nil),
		}}
		if err := db.Write(ctx(), b); err != nil {
			t.Fatal(err)
		}
		ids := func(rows []obsdb.RunRow) []string {
			var out []string
			for _, r := range rows {
				if len(r.ID) > 3 && r.ID[:3] == "sw_" {
					out = append(out, r.ID)
				}
			}
			slices.Sort(out)
			return out
		}
		srch, canSearch := db.(obsdb.Searcher)
		for _, c := range []struct {
			st   obsdb.Status
			want []string
		}{
			{obsdb.StatusParked, []string{"sw_open"}},
			{obsdb.StatusCancelled, []string{"sw_cancel"}},
			{obsdb.StatusFailed, []string{"sw_cancel", "sw_maxsteps"}},
			{obsdb.StatusSucceeded, []string{"sw_decided", "sw_done", "sw_open", "sw_resumer"}},
		} {
			page, err := db.Runs(ctx(), obsdb.RunQuery{Status: c.st, Limit: 50})
			if err != nil {
				t.Fatalf("Runs status %s: %v", c.st, err)
			}
			if got := ids(page.Runs); !slices.Equal(got, c.want) || page.Total != len(c.want) {
				t.Errorf("Runs status %s = %v (total %d), want %v", c.st, got, page.Total, c.want)
			}
			if !canSearch {
				continue
			}
			sp, err := srch.Search(ctx(), obsdb.SearchQuery{RunQuery: obsdb.RunQuery{Status: c.st, Limit: 50}})
			if err != nil {
				t.Fatalf("Search status %s: %v", c.st, err)
			}
			if got := ids(sp.Runs); !slices.Equal(got, c.want) || sp.Total != len(c.want) {
				t.Errorf("Search status %s = %v (total %d), want %v", c.st, got, sp.Total, c.want)
			}
		}
		// The rows themselves keep their derived status: parked and
		// cancelled are filters, never a row's Status.
		page, err := db.Runs(ctx(), obsdb.RunQuery{Status: obsdb.StatusParked, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range page.Runs {
			if r.Status != obsdb.StatusSucceeded {
				t.Errorf("a parked filter's row reads status %q, want succeeded", r.Status)
			}
		}
		for _, w := range obsdb.StatusWords {
			if got, err := obsdb.ParseStatus(string(w)); err != nil || got != w {
				t.Errorf("ParseStatus(%q) = %q, %v", w, got, err)
			}
		}
		if _, err := obsdb.ParseStatus("bogus"); !errors.Is(err, obsdb.ErrInvalidStatus) {
			t.Errorf("ParseStatus(bogus) = %v, want ErrInvalidStatus", err)
		}
	}
}
