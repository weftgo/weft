package clickhouse

import (
	"strings"
	"testing"
)

// TestMigration0032RestatesRunViews (U33.1-fix): 0032 is 0023's logs
// view and 0030's traces view, each with the same two changes — the
// caller columns empty when weft.caller.remote is 'true', and the whole
// current exclusion tuple (weft.caller.remote appended; the logs view
// catching up from 0023's) — and nothing else.
func TestMigration0032RestatesRunViews(t *testing.T) {
	read := func(v int) []string {
		b, err := migrationsFS.ReadFile("migrations/" + migrations[v])
		if err != nil {
			t.Fatal(err)
		}
		return splitStatements(string(b))
	}
	tuple := func(s string) string {
		i := strings.Index(s, "NOT has([") + len("NOT has([")
		return s[i : i+strings.Index(s[i:], "], k)")]
	}
	normal := func(s, attrs string) string {
		s = strings.Replace(s, tuple(s), "<tuple>", 1)
		for _, k := range []string{"run", "call"} {
			s = strings.Replace(s, "if("+attrs+"['weft.caller.remote'] = 'true', '', "+attrs+"['weft.caller."+k+".id'])",
				attrs+"['weft.caller."+k+".id']", 1)
		}
		return s
	}
	var logs23 string
	for _, s := range read(23) {
		if strings.HasPrefix(s, "ALTER TABLE weft_runs_logs_mv MODIFY QUERY") {
			logs23 = s
		}
	}
	traces30, now := read(30), read(32)
	if len(traces30) != 1 || len(now) != 2 || logs23 == "" {
		t.Fatalf("statements: 0030 %d, 0032 %d, 0023 logs view found %v", len(traces30), len(now), logs23 != "")
	}
	// 0033 (U30.3) appends its two keys after 0032's tuple.
	want := strings.Join(append([]string{}, contractKeys[:len(contractKeys)-u303Keys]...), "', '")
	for i, c := range []struct{ was, attrs string }{{logs23, "LogAttributes"}, {traces30[0], "SpanAttributes"}} {
		got := now[i]
		if normal(got, c.attrs) != normal(c.was, c.attrs) {
			t.Errorf("0032 statement %d is not its source view with the caller and tuple changes alone", i)
		}
		if !strings.Contains(got, "if("+c.attrs+"['weft.caller.remote'] = 'true', '', "+c.attrs+"['weft.caller.run.id']) AS CallerRunID") {
			t.Errorf("0032 statement %d does not empty CallerRunID for a remote link", i)
		}
		if strings.Join(strings.Fields(strings.ReplaceAll(tuple(got), "\n", " ")), " ") != "'"+want+"'" {
			t.Errorf("0032 statement %d's tuple is not contractKeys as of 0032", i)
		}
	}
}
