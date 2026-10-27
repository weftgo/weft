package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The runs page's count of GenAI traces that make no run (I6-fix2,
// UX-H-02) asks TraceQuery{NotRun, Since: 7 days ago}: the window
// narrows by the spans_start index and each trace's spans by its key,
// never a scan of every span; the derived runs' traces come off the
// runs_derived_trace index.
func TestNotRunWindowUsesIndexes(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	group, having, args := traceGroup(obsdb.TraceQuery{NotRun: true, Since: time.Now().Add(-7 * 24 * time.Hour)})
	rs, err := db.(*DB).reads.QueryContext(context.Background(), `EXPLAIN QUERY PLAN SELECT COUNT(*) FROM (`+group+having+`)`, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rs.Close() }()
	var plan []string
	for rs.Next() {
		var a, b, c int
		var detail string
		if err := rs.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	all := strings.Join(plan, "; ")
	for _, want := range []string{"spans_start", "runs_derived_trace"} {
		if !strings.Contains(all, want) {
			t.Errorf("plan %q does not use %s", all, want)
		}
	}
	for _, p := range plan {
		if p == "SCAN spans" {
			t.Errorf("plan %q scans every span", all)
		}
	}
}
