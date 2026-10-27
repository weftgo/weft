package clickhouse

import (
	"strings"
	"testing"
)

// Migration 0033 (U30.3; UX-S2-02, UX-S1-06) is 0032's traces view with
// its tuple alone changed: the attempt's weft.attempt.giveup_reason and
// the Output run's weft.output.type appended, the whole contractKeys.
func TestMigration0033RestatesTracesView(t *testing.T) {
	read := func(v int) []string {
		b, err := migrationsFS.ReadFile("migrations/" + migrations[v])
		if err != nil {
			t.Fatal(err)
		}
		return splitStatements(string(b))
	}
	const alter = "ALTER TABLE weft_runs_traces_mv MODIFY QUERY"
	var was string
	for _, s := range read(32) {
		if strings.HasPrefix(s, alter) {
			was = s
		}
	}
	now := read(33)
	if was == "" || len(now) != 1 {
		t.Fatalf("0032's traces view found %v; 0033 has %d statements, want 1", was != "", len(now))
	}
	want := strings.Replace(was, "'weft.caller.remote'], k)",
		"'weft.caller.remote',\n        'weft.attempt.giveup_reason', 'weft.output.type'], k)", 1)
	if now[0] != want {
		t.Errorf("0033 is not 0032's traces view with the two keys appended:\n%s", now[0])
	}
	if contractKeys[len(contractKeys)-2] != "weft.attempt.giveup_reason" || contractKeys[len(contractKeys)-1] != "weft.output.type" {
		t.Errorf("contractKeys does not end with 0033's keys: %v", contractKeys[len(contractKeys)-2:])
	}
}
