package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// TestExperimentRunsUseTheIndex (M2): an experiment's run list — the
// filter GET /api/experiments reads once per experiment — is planned
// on runs_experiment (0006), never a scan of runs; the count and the
// page alike.
func TestExperimentRunsUseTheIndex(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	d := db.(*DB)
	where, args := runWhere(obsdb.RunQuery{ExperimentID: "exp_1"}, time.Now())
	for _, q := range []string{
		`SELECT COUNT(*) FROM runs WHERE ` + where,
		`SELECT run_id FROM runs WHERE ` + where + ` ORDER BY started_ns DESC, run_id DESC LIMIT 500`,
	} {
		rs, err := d.reads.QueryContext(context.Background(), `EXPLAIN QUERY PLAN `+q, args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rs.Next() {
			var id, parent, notused int
			var detail string
			if err := rs.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		_ = rs.Close()
		if p := strings.Join(plan, "; "); !strings.Contains(p, "runs_experiment") {
			t.Errorf("plan of %q = %s, want the runs_experiment index", q, p)
		}
	}
}
