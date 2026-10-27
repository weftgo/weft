package clickhouse

import (
	"strings"
	"testing"
)

// Migration 0033 (U30.3; UX-S2-02, UX-S1-06) is 0032's two views with
// their tuple alone changed: the attempt's weft.attempt.giveup_reason and
// the Output run's weft.output.type appended, the whole contractKeys —
// the logs view too, so both views exclude obsdb.MetaOf's one set.
func TestMigration0033RestatesTracesView(t *testing.T) {
	read := func(v int) []string {
		b, err := migrationsFS.ReadFile("migrations/" + migrations[v])
		if err != nil {
			t.Fatal(err)
		}
		return splitStatements(string(b))
	}
	was, now := read(32), read(33)
	if len(was) != 2 || len(now) != 2 {
		t.Fatalf("0032 has %d statements, 0033 %d; want 2 each (the logs view, the traces view)", len(was), len(now))
	}
	for i, view := range []string{"weft_runs_logs_mv", "weft_runs_traces_mv"} {
		if !strings.HasPrefix(was[i], "ALTER TABLE "+view+" MODIFY QUERY") || !strings.HasPrefix(now[i], "ALTER TABLE "+view+" MODIFY QUERY") {
			t.Fatalf("statement %d is not %s's restatement in both", i, view)
		}
		want := strings.Replace(was[i], "'weft.caller.remote'], k)",
			"'weft.caller.remote',\n        'weft.attempt.giveup_reason', 'weft.output.type'], k)", 1)
		if now[i] != want {
			t.Errorf("0033's %s is not 0032's with the two keys appended:\n%s", view, now[i])
		}
	}
	if contractKeys[len(contractKeys)-2] != "weft.attempt.giveup_reason" || contractKeys[len(contractKeys)-1] != "weft.output.type" {
		t.Errorf("contractKeys does not end with 0033's keys: %v", contractKeys[len(contractKeys)-2:])
	}
}
