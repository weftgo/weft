package clickhouse

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/weftgo/weft/obsdb"
)

// The pinned exporter version whose INSERT shape the tables must stay
// compatible with (S3.6, Q6). The migration file records it; this test
// fails when the pin moves, which is a schema decision.
const pinnedExporterVersion = "v0.162.0"

func TestMigrationPinsExporterVersion(t *testing.T) {
	body, err := migrationsFS.ReadFile("migrations/" + migrations[1])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), pinnedExporterVersion) {
		t.Errorf("migration 0001 does not name the pinned exporter version %s", pinnedExporterVersion)
	}
	if highestMigration() != 14 {
		t.Errorf("highest migration = %d, want 14 (0001 init, 0002 experiments, 0003 status spelling, 0004 request record, 0005 raw record, 0006 experiment command, 0007 the experiment index, 0008 the command store, 0009 the derived flag, 0010 root ended, 0011 stopped by, 0012 the root's version, 0013 the emitted counts, 0014 records.dropped out of meta)", highestMigration())
	}
}

// The collector-shape columns must exist in our DDL: every column the
// pinned exporter's INSERT names (its traces_insert.sql and
// logs_insert.sql at that tag, plus the EventName feature column it
// fills when DESC TABLE finds it) has to appear in the CREATE TABLE.
// The server-backed collector-shape test proves the insert lands; this
// one catches a rename offline.
func TestCollectorInsertColumnsExist(t *testing.T) {
	body, err := migrationsFS.ReadFile("migrations/" + migrations[1])
	if err != nil {
		t.Fatal(err)
	}
	ddl := string(body)
	tracesInsert := []string{
		"Timestamp", "TraceId", "SpanId", "ParentSpanId", "TraceState", "SpanName",
		"SpanKind", "ServiceName", "ResourceAttributes", "ScopeName", "ScopeVersion",
		"SpanAttributes", "Duration", "StatusCode", "StatusMessage",
		"Events.Timestamp", "Events.Name", "Events.Attributes",
		"Links.TraceId", "Links.SpanId", "Links.TraceState", "Links.Attributes",
	}
	logsInsert := []string{
		"Timestamp", "TraceId", "SpanId", "TraceFlags", "SeverityText", "SeverityNumber",
		"ServiceName", "Body", "ResourceSchemaUrl", "ResourceAttributes",
		"ScopeSchemaUrl", "ScopeName", "ScopeVersion", "ScopeAttributes", "LogAttributes",
		"EventName", // the pinned exporter fills it when the column exists; ours does
	}
	for _, table := range []struct {
		name string
		cols []string
	}{
		{"otel_traces", tracesInsert},
		{"otel_logs", logsInsert},
	} {
		create := createBlock(ddl, table.name)
		if create == "" {
			t.Fatalf("no CREATE TABLE %s in migration", table.name)
		}
		for _, col := range table.cols {
			if !columnDefined(create, col) {
				t.Errorf("%s: collector insert column %q missing from our DDL", table.name, col)
			}
		}
	}
}

func createBlock(ddl, table string) string {
	start := strings.Index(ddl, "CREATE TABLE IF NOT EXISTS "+table+" (")
	if start < 0 {
		return ""
	}
	rest := ddl[start:]
	end := strings.Index(rest, ") ENGINE")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// columnDefined reports whether col is declared in a CREATE block. A
// dotted name is the collector's spelling of a Nested element
// (Events.Timestamp): it matches the nested declaration "Events Nested
// ( Timestamp ... )" by its parts.
func columnDefined(create, col string) bool {
	if name, sub, dotted := strings.Cut(col, "."); dotted {
		nested := regexp.MustCompile("(?ms)^\\s*`?" + name + "`?\\s+Nested\\s*\\((.*?)\\)\\s*(CODEC|$)")
		m := nested.FindStringSubmatch(create)
		if m == nil {
			return false
		}
		subRe := regexp.MustCompile("(?m)^\\s*`?" + sub + "`?\\s")
		return subRe.MatchString(m[1])
	}
	re := regexp.MustCompile("(?m)^\\s*`?" + col + "`?\\s")
	return re.MatchString(create)
}

// The engine and ORDER BY lines S3.6 names.
func TestSpecEnginesPresent(t *testing.T) {
	body, err := migrationsFS.ReadFile("migrations/" + migrations[1])
	if err != nil {
		t.Fatal(err)
	}
	ddl := string(body)
	for _, want := range []string{
		"ReplacingMergeTree(InsertTime)\nORDER BY (RunId, Kind, Pos)",
		"AggregatingMergeTree\nORDER BY RunId",
		"ORDER BY (ServiceName, SpanName, toDateTime(Timestamp))",
		"ORDER BY (toStartOfFiveMinutes(Timestamp), ServiceName, Timestamp)",
		"LogAttributes['weft.record'] IN ('event', 'messages')",
		"INDEX idx_weft_session_id SessionId TYPE bloom_filter(0.01)",
		"INDEX idx_weft_public_id PublicId TYPE bloom_filter(0.01)",
		"INDEX idx_weft_agent Agent TYPE bloom_filter(0.01)",
		"INDEX idx_trace_id TraceId TYPE bloom_filter(0.001)",
		"TTL Timestamp + toIntervalSecond(7776000)", // spans: 90 d
		"TTL Timestamp + toIntervalSecond(2592000)", // content: 30 d
		"TTL Time + toIntervalSecond(2592000)",      // weft_records' own content TTL
		"TTL LastSeen + toIntervalSecond(7776000)",  // runs: 90 d
		"SimpleAggregateFunction(min, DateTime64(9))",
		"SimpleAggregateFunction(max, Nullable(DateTime64(9)))",
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("migration missing %q", want)
		}
	}
}

func TestSplitStatements(t *testing.T) {
	// Multi-line statements (the DDL's shape) must survive intact.
	got := splitStatements("-- a comment\nCREATE TABLE a (\n    x UInt8,\n    s String\n);\n\n-- another\nCREATE TABLE b (y String);\nSELECT 1;")
	want := []string{"CREATE TABLE a (\n    x UInt8,\n    s String\n)", "CREATE TABLE b (y String)", "SELECT 1"}
	if len(got) != len(want) {
		t.Fatalf("statements = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("statement %d = %q, want %q", i, got[i], want[i])
		}
	}
	if s := splitStatements("-- only comments\n"); len(s) != 0 {
		t.Errorf("comment-only body yielded %#v", s)
	}
}

func TestTTLOptionDefaults(t *testing.T) {
	var cfg openConfig
	TTL(0, 0).apply(&cfg)
	if cfg.contentTTL != 0 || cfg.metaTTL != 0 {
		t.Error("zero TTL args must leave the zero config (Open applies the defaults)")
	}
	cfg = openConfig{}
	TTL(time.Hour, 0).apply(&cfg)
	if cfg.contentTTL != time.Hour {
		t.Errorf("content ttl = %v, want 1h", cfg.contentTTL)
	}
	if cfg.metaTTL != 0 {
		t.Errorf("meta ttl = %v, want untouched", cfg.metaTTL)
	}
	// Open seeds the defaults; an override changes one class only.
	cfg = openConfig{contentTTL: DefaultContentTTL, metaTTL: DefaultMetaTTL}
	TTL(0, 48*time.Hour).apply(&cfg)
	if cfg.contentTTL != DefaultContentTTL {
		t.Errorf("content ttl changed by a zero arg: %v", cfg.contentTTL)
	}
	if cfg.metaTTL != 48*time.Hour {
		t.Errorf("meta ttl = %v, want 48h", cfg.metaTTL)
	}
	if DefaultContentTTL != 30*24*time.Hour || DefaultMetaTTL != 90*24*time.Hour {
		t.Error("S3.6 defaults are 30 d content / 90 d spans and runs")
	}
}

// A window renders as whole seconds, rounded up — never the zero
// interval a truncated sub-second window produced, which expires every
// row on arrival.
func TestTTLSecondsRoundUp(t *testing.T) {
	for _, c := range []struct {
		in   time.Duration
		want int64
	}{
		{500 * time.Millisecond, 1}, {time.Nanosecond, 1}, {time.Second, 1},
		{1500 * time.Millisecond, 2}, {time.Hour, 3600}, {DefaultContentTTL, 2592000}, {DefaultMetaTTL, 7776000},
	} {
		if got := ttlSeconds(c.in); got != c.want {
			t.Errorf("ttlSeconds(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestKeepDeltasOption(t *testing.T) {
	var cfg openConfig
	KeepDeltas().apply(&cfg)
	if !cfg.keepDeltas {
		t.Error("KeepDeltas did not arm delta storage")
	}
}

// The span kind and status names the pinned exporter writes (pdata's
// String() spellings — exporter_traces.go at v0.162.0), round-tripped,
// and the OTLP proto value names older rows carry still read.
func TestOTLPEnumNames(t *testing.T) {
	for kind := 0; kind <= 6; kind++ {
		if got := spanKindInt(spanKindName(kind)); got != kind && kind <= 5 {
			t.Errorf("span kind %d round trip = %d", kind, got)
		}
	}
	if spanKindName(0) != "Unspecified" || spanKindName(1) != "Internal" ||
		spanKindName(3) != "Client" {
		t.Error("span kind names must be pdata's SpanKind.String() spellings the exporter writes")
	}
	if spanKindInt("weird") != 0 {
		t.Error("unknown kind reads unspecified")
	}
	for code := 0; code <= 3; code++ {
		if got := statusCodeInt(statusCodeName(code)); got != code && code <= 2 {
			t.Errorf("status code %d round trip = %d", code, got)
		}
	}
	if statusCodeName(2) != "Error" || statusCodeName(1) != "Ok" || statusCodeName(0) != "Unset" {
		t.Error("status code names must be pdata's StatusCode.String() spellings the exporter writes")
	}
	// Rows from before migration 0003 (and older exporters).
	if spanKindInt("SPAN_KIND_SERVER") != 2 || statusCodeInt("STATUS_CODE_ERROR") != 2 ||
		statusCodeInt("STATUS_CODE_OK") != 1 {
		t.Error("the OTLP proto value names must still read")
	}
}

func TestAttrStringify(t *testing.T) {
	got := stringAttrs(map[string]any{
		"s": "x", "b": true, "i": int64(7), "f": 1.5,
		"arr": []any{"a", int64(1)}, "nil": nil,
	})
	want := map[string]string{
		"s": "x", "b": "true", "i": "7", "f": "1.5", "arr": `["a",1]`, "nil": "",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("attr %q = %q, want %q", k, got[k], v)
		}
	}
	if got := stringAttrs(nil); len(got) != 0 {
		t.Errorf("nil attrs = %v, want empty non-nil map for the driver", got)
	}
}

func TestJSONColumns(t *testing.T) {
	if jsonOrNull(nil) != "" || jsonOrNull(map[string]any{}) != "" {
		t.Error("empty maps must serialize to '' (the stock-collector default)")
	}
	if js := jsonOrNull(map[string]any{"n": int64(1)}); js != `{"n":1}` {
		t.Errorf("typed json = %q", js)
	}
	if s := eventsJSON(nil); s != "" {
		t.Errorf("no events = %q, want ''", s)
	}
	if s := eventsJSON([]obsdb.SpanEvent{{Name: "exception"}}); !strings.Contains(s, `"Name":"exception"`) {
		t.Errorf("events json = %q", s)
	}
}

// eventsFromJSONOrNested must decode the weft events column with the
// UseNumber rule — an int64 event attribute stays int64 (S3.1/S3.3,
// the same guarantee the top-level Attrs round trip pins; plain
// json.Unmarshal would return float64) — and the collector's nested
// arrays carry their string values through.
func TestEventsDecodeTyped(t *testing.T) {
	at := time.Unix(0, 1790845923120000000).UTC()
	js := eventsJSON([]obsdb.SpanEvent{{
		Time:  at,
		Name:  "gen_ai.content.prompt",
		Attrs: map[string]any{"retry.count": int64(3), "ratio": 0.5, "ok": true},
	}})
	got := eventsFromJSONOrNested(js, nil, nil, nil)
	if len(got) != 1 {
		t.Fatalf("events from json = %d, want 1", len(got))
	}
	if n, ok := got[0].Attrs["retry.count"].(int64); !ok || n != 3 {
		t.Errorf("int64 event attr = %#v, want int64(3)", got[0].Attrs["retry.count"])
	}
	if f, ok := got[0].Attrs["ratio"].(float64); !ok || f != 0.5 {
		t.Errorf("float event attr = %#v, want float64(0.5)", got[0].Attrs["ratio"])
	}
	if b, ok := got[0].Attrs["ok"].(bool); !ok || !b {
		t.Errorf("bool event attr = %#v, want true", got[0].Attrs["ok"])
	}
	if !got[0].Time.Equal(at) || got[0].Name != "gen_ai.content.prompt" {
		t.Errorf("event = %v %q, want %v %q", got[0].Time, got[0].Name, at, "gen_ai.content.prompt")
	}
	fallback := eventsFromJSONOrNested("", []time.Time{at}, []string{"exception"},
		[]map[string]string{{"exception.type": "RuntimeError"}})
	if len(fallback) != 1 || fallback[0].Name != "exception" ||
		fallback[0].Attrs["exception.type"] != "RuntimeError" {
		t.Errorf("nested fallback = %+v", fallback)
	}
}

func TestSpanDuration(t *testing.T) {
	start := time.Now()
	if d := spanDuration(start, start.Add(1500*time.Millisecond)); d != 1500000000 {
		t.Errorf("duration = %d", d)
	}
	if d := spanDuration(start, start); d != 0 {
		t.Errorf("zero-length span = %d", d)
	}
	if d := spanDuration(start.Add(time.Second), start); d != 0 {
		t.Errorf("inverted span must floor at 0, got %d", d)
	}
}

func TestSeverityNumberClamp(t *testing.T) {
	for _, c := range []struct{ in, want int }{{-1, 0}, {0, 0}, {9, 9}, {24, 24}, {25, 24}} {
		if got := severityNumber(c.in); int(got) != c.want {
			t.Errorf("severityNumber(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// contractKeys must be exactly obsdb.MetaOf's exclusion set: every key
// in the copy drops a "v"-valued attribute from the metadata, and a
// sample of non-contract keys survives. MetaOf stays the one
// implementation; this pins the SQL filter's copy to it.
func TestContractKeysMatchMetaOf(t *testing.T) {
	for _, k := range contractKeys {
		if meta := obsdb.MetaOf(map[string]any{k: "v"}); len(meta) != 0 {
			t.Errorf("contract key %q survives MetaOf; the SQL copy is stale", k)
		}
	}
	for _, k := range []string{"tenant", "enduser.id", "weft.session.parent", "env"} {
		if meta := obsdb.MetaOf(map[string]any{k: "v"}); meta[k] != "v" {
			t.Errorf("metadata key %q dropped by MetaOf", k)
		}
	}
}

// The Runs meta filter compiles to a JSON condition over the metadata
// column — the SQL shape of a MetaOf subset match — applied to the
// grouped rows, never to weft_runs' stored ones.
func TestRunsInnerMetaFilter(t *testing.T) {
	sql, _ := runsInner(obsdb.RunQuery{Meta: map[string]string{"tenant": "acme"}})
	for _, want := range []string{
		"JSONHas(Meta, ?)",
		"JSONExtractString(Meta, ?) = ?",
		"FROM weft_runs WHERE 1=1 GROUP BY RunId) WHERE ParentRunID = '' AND JSONHas(Meta, ?)",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("runsInner missing %q:\n%s", want, sql)
		}
	}
}

// Every Runs filter reads the grouped row: nothing but the narrowing
// RunId IN (...) may sit between weft_runs and its GROUP BY, or an
// unmerged run is filtered row by row (the child-run leak). An identity
// equality binds twice — once to narrow the scan, once on the grouped
// row — in text order.
func TestRunsInnerFiltersAfterGrouping(t *testing.T) {
	yes := true
	sql, args := runsInner(obsdb.RunQuery{
		Agent: "a", SessionID: "s", PublicID: "p", ParentRunID: "par",
		ExperimentID: "e", Playground: &yes,
	})
	inner, outer, ok := strings.Cut(sql, " GROUP BY RunId) WHERE ")
	if !ok {
		t.Fatalf("runsInner has no grouped subquery:\n%s", sql)
	}
	_, where, _ := strings.Cut(inner, " FROM weft_runs WHERE ")
	for _, cond := range strings.Split(where, " AND ") {
		if !strings.HasPrefix(cond, "RunId IN (SELECT r.RunId FROM weft_runs AS r WHERE r.") {
			t.Errorf("row-level filter before GROUP BY: %q", cond)
		}
	}
	if outer != "Agent = ? AND SessionID = ? AND PublicID = ? AND ParentRunID = ? AND Playground = ? AND ExperimentID = ?" {
		t.Errorf("grouped-row filter = %q", outer)
	}
	want := []any{"a", "s", "p", "par", "e", "a", "s", "p", "par", &yes, "e"}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %d binds", args, len(want))
	}
	for i, w := range want {
		if p, isPtr := w.(*bool); isPtr {
			if args[i] != *p {
				t.Errorf("arg %d = %v, want %v", i, args[i], *p)
			}
			continue
		}
		if args[i] != w {
			t.Errorf("arg %d = %v, want %v", i, args[i], w)
		}
	}
}

// The migrations' mapFilter tuples must be exactly obsdb.MetaOf's
// exclusion set — the SQL copy of contractKeys. The latest restatement
// of both run views (0004) carries the whole set; 0001's two views and
// 0003's restated traces view carry the set as it was before ADR 0028,
// contractKeys without its last adr0028Keys entries (ADR 0028's twelve,
// plan A4's four, the attempt span's retry-after and the ten
// weft.override.* fields, all added by 0004). 0005 restates the logs
// view alone with the raw record's weft.raw.index (rawKeys) appended;
// 0010 the traces view with 0004's tuple. 0011 restates both with the
// whole set: stopped_by's two keys (stoppedByKeys) appended, the traces
// tuple gaining weft.raw.index on the way; 0012 the traces view again
// with the same set; 0013 (U5) the traces view again with the two
// emitted counts — span attributes, so the logs view keeps 0011's; 0014
// (U5U7-fix) the logs view with the whole set, weft.records.dropped last
// (droppedKeys) — a record attribute, so the traces view keeps 0013's.
const (
	adr0028Keys   = 12 + 4 + 1 + 10
	rawKeys       = 1
	stoppedByKeys = 2
	emittedKeys   = 2
	droppedKeys   = 1
)

func TestMigrationContractTuple(t *testing.T) {
	at0013 := contractKeys[:len(contractKeys)-droppedKeys]
	at0011 := at0013[:len(at0013)-emittedKeys]
	at0005 := at0011[:len(at0011)-stoppedByKeys]
	at0004 := at0005[:len(at0005)-rawKeys]
	before := at0004[:len(at0004)-adr0028Keys]
	for _, c := range []struct {
		version, tuples int
		keys            []string
	}{{1, 2, before}, {3, 1, before}, {4, 2, at0004}, {5, 1, at0005}, {10, 1, at0004}, {11, 2, at0011}, {12, 1, at0011}, {13, 1, at0013}, {14, 1, contractKeys}} {
		body, err := migrationsFS.ReadFile("migrations/" + migrations[c.version])
		if err != nil {
			t.Fatal(err)
		}
		marker := "NOT has(["
		count := strings.Count(string(body), marker)
		if count != c.tuples {
			t.Fatalf("migration %d: expected the tuple %d time(s), found %d", c.version, c.tuples, count)
		}
		rest := string(body)
		for i := 0; i < count; i++ {
			start := strings.Index(rest, marker) + len(marker)
			end := strings.Index(rest[start:], "], k)")
			if end < 0 {
				t.Fatal("tuple not terminated")
			}
			tuple := rest[start : start+end]
			var sqlKeys []string
			for _, part := range strings.Split(tuple, ",") {
				sqlKeys = append(sqlKeys, strings.Trim(strings.TrimSpace(part), "'"))
			}
			if len(sqlKeys) != len(c.keys) {
				t.Fatalf("migration %d: tuple has %d keys, want %d", c.version, len(sqlKeys), len(c.keys))
			}
			for i, k := range c.keys {
				if sqlKeys[i] != k {
					t.Errorf("migration %d: tuple key %d = %q, want %q", c.version, i, sqlKeys[i], k)
				}
			}
			rest = rest[start+end:]
		}
	}
}

// Migration 0003 is 0001's traces view with one change — the error
// status read in both spellings. Pinned offline against 0001's text so
// the restated select cannot drift from the view it replaces.
func TestMigration0003RestatesTracesView(t *testing.T) {
	init, err := migrationsFS.ReadFile("migrations/" + migrations[1])
	if err != nil {
		t.Fatal(err)
	}
	fix, err := migrationsFS.ReadFile("migrations/" + migrations[3])
	if err != nil {
		t.Fatal(err)
	}
	const create = "CREATE MATERIALIZED VIEW IF NOT EXISTS weft_runs_traces_mv TO weft_runs AS"
	const alter = "ALTER TABLE weft_runs_traces_mv MODIFY QUERY"
	_, was, ok := strings.Cut(string(init), create)
	if !ok {
		t.Fatal("0001 has no weft_runs_traces_mv")
	}
	_, now, ok := strings.Cut(string(fix), alter)
	if !ok {
		t.Fatal("0003 does not MODIFY QUERY weft_runs_traces_mv")
	}
	want := strings.NewReplacer(
		"startsWith(SpanName, 'invoke_agent')) AS isInvoke,\n",
		"startsWith(SpanName, 'invoke_agent')) AS isInvoke,\n     StatusCode IN ('Error', 'STATUS_CODE_ERROR') AS isError,\n",
		"StatusCode = 'STATUS_CODE_ERROR'", "isError",
	).Replace(was)
	if now != want {
		t.Errorf("0003's select is not 0001's with the status spelling widened:\n%s", now)
	}
	stmts := splitStatements(string(fix))
	if len(stmts) != 1 || !strings.HasPrefix(stmts[0], alter) {
		t.Errorf("0003 must be the one ALTER statement, got %d", len(stmts))
	}
}

// Migration 0010 restates 0004's traces view with two changes —
// RootEnded from isInvoke, and WeftVersion falling back to the span's
// resource — then backfills from the spans already stored. Pinned
// offline against 0004's text so the restated select cannot drift.
func TestMigration0010RestatesTracesView(t *testing.T) {
	read := func(v int) []string {
		b, err := migrationsFS.ReadFile("migrations/" + migrations[v])
		if err != nil {
			t.Fatal(err)
		}
		return splitStatements(string(b))
	}
	const alter = "ALTER TABLE weft_runs_traces_mv MODIFY QUERY"
	var was string
	for _, st := range read(4) {
		if strings.HasPrefix(st, alter) {
			was = st
		}
	}
	now := read(10)
	if was == "" || len(now) != 3 {
		t.Fatalf("0004's traces view found %v; 0010 has %d statements, want 3 (add column, view, backfill)", was != "", len(now))
	}
	if now[0] != "ALTER TABLE weft_runs ADD COLUMN IF NOT EXISTS RootEnded SimpleAggregateFunction(max, UInt8)" {
		t.Errorf("0010's first statement = %q", now[0])
	}
	want := strings.NewReplacer(
		"SpanAttributes['weft.version'] AS WeftVersion,",
		"if(SpanAttributes['weft.version'] != '', SpanAttributes['weft.version'], ResourceAttributes['weft.version']) AS WeftVersion,",
		"toInt64(0) AS RequestCount\n", "toInt64(0) AS RequestCount,\n    toUInt8(isInvoke) AS RootEnded\n",
	).Replace(was)
	if now[1] != want {
		t.Errorf("0010's select is not 0004's with RootEnded and the version fallback:\n%s", now[1])
	}
	if !strings.HasPrefix(now[2], "INSERT INTO weft_runs (RunId, Started, LastSeen, ManifestHash, WeftVersion, RootEnded)") {
		t.Errorf("0010's third statement is not the backfill: %q", now[2])
	}
}

// Migration 0011 (U3.2) adds the two columns, restates 0005's logs
// view with StoppedBy from a run_finish body and 0010's traces view with
// StoppedBy and CancelCause from the invoke_agent span — each tuple
// gaining the two keys, the traces one weft.raw.index too — then
// backfills from both signals. Pinned offline against the selects it
// replaces.
func TestMigration0011RestatesRunViews(t *testing.T) {
	read := func(v int) []string {
		b, err := migrationsFS.ReadFile("migrations/" + migrations[v])
		if err != nil {
			t.Fatal(err)
		}
		return splitStatements(string(b))
	}
	const (
		logsAlter   = "ALTER TABLE weft_runs_logs_mv MODIFY QUERY"
		tracesAlter = "ALTER TABLE weft_runs_traces_mv MODIFY QUERY"
	)
	find := func(stmts []string, prefix string) string {
		for _, s := range stmts {
			if strings.HasPrefix(s, prefix) {
				return s
			}
		}
		t.Fatalf("no %q", prefix)
		return ""
	}
	now := read(11)
	wantPrefixes := []string{
		"ALTER TABLE weft_runs ADD COLUMN IF NOT EXISTS StoppedBy SimpleAggregateFunction(max, String)",
		"ALTER TABLE weft_runs ADD COLUMN IF NOT EXISTS CancelCause SimpleAggregateFunction(max, String)",
		logsAlter, tracesAlter,
		"INSERT INTO weft_runs (RunId, Started, LastSeen, StoppedBy, CancelCause)",
		"INSERT INTO weft_runs (RunId, Started, LastSeen, StoppedBy)",
	}
	if len(now) != len(wantPrefixes) {
		t.Fatalf("0011 has %d statements, want %d", len(now), len(wantPrefixes))
	}
	for i, p := range wantPrefixes {
		if !strings.HasPrefix(now[i], p) {
			t.Errorf("0011 statement %d = %q, want prefix %q", i, now[i], p)
		}
	}
	logs := strings.NewReplacer(
		`'weft.raw.index'], k)`, `'weft.raw.index',
        'weft.run.stopped_by', 'weft.cancel.cause'], k)`,
		"toInt64OrZero(LogAttributes['weft.request.index']) + 1, 0) AS RequestCount\n",
		"toInt64OrZero(LogAttributes['weft.request.index']) + 1, 0) AS RequestCount,\n    if(LogAttributes['weft.event.type'] = 'run_finish', JSONExtractString(Body, 'stopped_by'), '') AS StoppedBy\n",
	).Replace(find(read(5), logsAlter))
	if now[2] != logs {
		t.Errorf("0011's logs select is not 0005's with StoppedBy:\n%s\nwant:\n%s", now[2], logs)
	}
	traces := strings.NewReplacer(
		`'weft.override.tools'], k)`, `'weft.override.tools',
        'weft.raw.index',
        'weft.run.stopped_by', 'weft.cancel.cause'], k)`,
		"    toUInt8(isInvoke) AS RootEnded\n",
		"    toUInt8(isInvoke) AS RootEnded,\n    if(isInvoke, SpanAttributes['weft.run.stopped_by'], '') AS StoppedBy,\n    if(isInvoke, SpanAttributes['weft.cancel.cause'], '') AS CancelCause\n",
	).Replace(find(read(10), tracesAlter))
	if now[3] != traces {
		t.Errorf("0011's traces select is not 0010's with StoppedBy and CancelCause:\n%s\nwant:\n%s", now[3], traces)
	}
}

// Migration 0012 (the U2 review) restates 0011's traces view with one
// change — WeftVersion from the invoke_agent span alone, its attribute
// else its resource's — then backfills RootEnded for derived runs.
// Pinned offline against 0011's select so the restatement cannot drift.
func TestMigration0012RestatesTracesView(t *testing.T) {
	read := func(v int) []string {
		b, err := migrationsFS.ReadFile("migrations/" + migrations[v])
		if err != nil {
			t.Fatal(err)
		}
		return splitStatements(string(b))
	}
	const alter = "ALTER TABLE weft_runs_traces_mv MODIFY QUERY"
	var was string
	for _, st := range read(11) {
		if strings.HasPrefix(st, alter) {
			was = st
		}
	}
	now := read(12)
	if was == "" || len(now) != 2 {
		t.Fatalf("0011's traces view found %v; 0012 has %d statements, want 2 (view, backfill)", was != "", len(now))
	}
	want := strings.Replace(was,
		"    if(SpanAttributes['weft.version'] != '', SpanAttributes['weft.version'], ResourceAttributes['weft.version']) AS WeftVersion,",
		"    if(isInvoke, if(SpanAttributes['weft.version'] != '', SpanAttributes['weft.version'], ResourceAttributes['weft.version']), '') AS WeftVersion,", 1)
	if want == was || now[0] != want {
		t.Errorf("0012's select is not 0011's with the version from the root alone:\n%s", now[0])
	}
	if !strings.HasPrefix(now[1], "INSERT INTO weft_runs (RunId, Started, LastSeen, RootEnded)") ||
		!strings.Contains(now[1], "HAVING max(Derived) = 1 AND max(RootEnded) = 0") {
		t.Errorf("0012's second statement is not the derived RootEnded backfill: %q", now[1])
	}
}

// Migration 0013 (U5) adds the two columns, restates 0012's traces view
// with EventsEmitted and MessagesEmitted from the invoke_agent span and
// the two keys in its tuple, then backfills from the roots stored.
// Pinned offline against 0012's select so the restatement cannot drift.
func TestMigration0013RestatesTracesView(t *testing.T) {
	read := func(v int) []string {
		b, err := migrationsFS.ReadFile("migrations/" + migrations[v])
		if err != nil {
			t.Fatal(err)
		}
		return splitStatements(string(b))
	}
	was := read(12)[0]
	now := read(13)
	if len(now) != 4 {
		t.Fatalf("0013 has %d statements, want 4 (two columns, view, backfill)", len(now))
	}
	for i, p := range []string{
		"ALTER TABLE weft_runs ADD COLUMN IF NOT EXISTS EventsEmitted SimpleAggregateFunction(max, Int64)",
		"ALTER TABLE weft_runs ADD COLUMN IF NOT EXISTS MessagesEmitted SimpleAggregateFunction(max, Int64)",
		"ALTER TABLE weft_runs_traces_mv MODIFY QUERY",
		"INSERT INTO weft_runs (RunId, Started, LastSeen, EventsEmitted, MessagesEmitted)",
	} {
		if !strings.HasPrefix(now[i], p) {
			t.Errorf("0013 statement %d = %q, want prefix %q", i, now[i], p)
		}
	}
	want := strings.NewReplacer(
		`'weft.run.stopped_by', 'weft.cancel.cause'], k)`, `'weft.run.stopped_by', 'weft.cancel.cause',
        'weft.run.events', 'weft.run.messages'], k)`,
		"    if(isInvoke, SpanAttributes['weft.cancel.cause'], '') AS CancelCause\n",
		"    if(isInvoke, SpanAttributes['weft.cancel.cause'], '') AS CancelCause,\n    if(isInvoke, toInt64OrZero(SpanAttributes['weft.run.events']), 0) AS EventsEmitted,\n    if(isInvoke, toInt64OrZero(SpanAttributes['weft.run.messages']), 0) AS MessagesEmitted\n",
	).Replace(was)
	if want == was || now[2] != want {
		t.Errorf("0013's select is not 0012's with the two counts:\n%s", now[2])
	}
}

// Migration 0014 (U5U7-fix 6) restates 0011's logs view with one change:
// weft.records.dropped (and 0013's two emitted counts) in its contract
// tuple, so ClickHouse's stored Meta no longer keeps a destination's
// loss count. Pinned offline against 0011's select.
func TestMigration0014RestatesLogsView(t *testing.T) {
	read := func(v int) []string {
		b, err := migrationsFS.ReadFile("migrations/" + migrations[v])
		if err != nil {
			t.Fatal(err)
		}
		return splitStatements(string(b))
	}
	const alter = "ALTER TABLE weft_runs_logs_mv MODIFY QUERY"
	var was string
	for _, st := range read(11) {
		if strings.HasPrefix(st, alter) {
			was = st
		}
	}
	now := read(14)
	if was == "" || len(now) != 1 {
		t.Fatalf("0011's logs view found %v; 0014 has %d statements, want 1 (the view)", was != "", len(now))
	}
	want := strings.Replace(was, `'weft.run.stopped_by', 'weft.cancel.cause'], k)`, `'weft.run.stopped_by', 'weft.cancel.cause',
        'weft.run.events', 'weft.run.messages',
        'weft.records.dropped'], k)`, 1)
	if want == was || now[0] != want {
		t.Errorf("0014's select is not 0011's with weft.records.dropped:\n%s", now[0])
	}
}

// TestContractKeysCoverNonMeta is TestContractKeysMatchMetaOf's reverse
// (U5U7-fix 6): every key obsdb.MetaOf excludes is in contractKeys, so
// the SQL filters keep no contract key in stored Meta — or it is
// exempt here, with the reason. obsdb.NonMetaKeys lists them.
func TestContractKeysCoverNonMeta(t *testing.T) {
	exempt := map[string]string{}
	for _, k := range obsdb.NonMetaKeys() {
		if slices.Contains(contractKeys, k) {
			continue
		}
		if why, ok := exempt[k]; ok {
			t.Logf("%s exempt: %s", k, why)
			continue
		}
		t.Errorf("MetaOf excludes %q but contractKeys lacks it: ClickHouse's stored Meta keeps it", k)
	}
}

// Migration 0005 (ADR 0028 §12) restates two of 0004's views: the
// records view with the raw kind and its index, the logs run view with
// weft.raw.index in its contract tuple — nothing else.
func TestMigration0005RestatesViews(t *testing.T) {
	read := func(v int) string {
		b, err := migrationsFS.ReadFile("migrations/" + migrations[v])
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	const (
		recordsAlter = "ALTER TABLE weft_records_mv MODIFY QUERY"
		logsAlter    = "ALTER TABLE weft_runs_logs_mv MODIFY QUERY"
	)
	was, now := splitStatements(read(4)), splitStatements(read(5))
	if len(now) != 2 || !strings.HasPrefix(now[0], recordsAlter) || !strings.HasPrefix(now[1], logsAlter) {
		t.Fatalf("0005 = %d statements, want the records then the logs view restated", len(now))
	}
	find := func(stmts []string, prefix string) string {
		for _, s := range stmts {
			if strings.HasPrefix(s, prefix) {
				return s
			}
		}
		t.Fatalf("0004 has no %q", prefix)
		return ""
	}
	records := strings.NewReplacer(
		`LogAttributes['weft.tools.index'] != '', toInt64OrZero(LogAttributes['weft.tools.index']), -1) AS Pos,`,
		`LogAttributes['weft.tools.index'] != '', toInt64OrZero(LogAttributes['weft.tools.index']),
      LogAttributes['weft.raw.index'] != '', toInt64OrZero(LogAttributes['weft.raw.index']), -1) AS Pos,`,
		`IN ('event', 'messages', 'request', 'prompt', 'tools')`, `IN ('event', 'messages', 'request', 'prompt', 'tools', 'raw')`,
	).Replace(find(was, recordsAlter))
	if now[0] != records {
		t.Errorf("0005's records select is not 0004's with the raw kind:\n%s\nwant:\n%s", now[0], records)
	}
	logs := strings.Replace(find(was, logsAlter), `'weft.override.tools'], k)`, `'weft.override.tools',
        'weft.raw.index'], k)`, 1)
	if now[1] != logs {
		t.Errorf("0005's logs select is not 0004's with weft.raw.index:\n%s\nwant:\n%s", now[1], logs)
	}
}

// Migration 0004 (ADR 0028) is additive: the request record's columns,
// then three restated views. Each restated select is pinned offline
// against the select it replaces (0001's records and logs views,
// 0003's traces view), so it cannot drift in anything but the named
// changes.
func TestMigration0004RestatesViews(t *testing.T) {
	read := func(v int) string {
		b, err := migrationsFS.ReadFile("migrations/" + migrations[v])
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	// selectAfter returns the statement text after marker, up to its ';'.
	selectAfter := func(body, marker string) string {
		_, rest, ok := strings.Cut(body, marker)
		if !ok {
			t.Fatalf("no %q", marker)
		}
		sel, _, _ := strings.Cut(rest, ";")
		return strings.TrimSpace(sel)
	}
	init, fix := read(1), read(3)
	const (
		recordsAlter = "ALTER TABLE weft_records_mv MODIFY QUERY"
		logsAlter    = "ALTER TABLE weft_runs_logs_mv MODIFY QUERY"
		tracesAlter  = "ALTER TABLE weft_runs_traces_mv MODIFY QUERY"
	)
	stmts := splitStatements(read(4))
	wantPrefixes := []string{
		"ALTER TABLE weft_records ADD COLUMN IF NOT EXISTS Step Int32 DEFAULT -1 AFTER EventType",
		"ALTER TABLE weft_records ADD COLUMN IF NOT EXISTS Reason LowCardinality(String) DEFAULT '' AFTER Step",
		"ALTER TABLE weft_records ADD COLUMN IF NOT EXISTS Input Int8 DEFAULT -1 AFTER Reason",
		"ALTER TABLE weft_records ADD COLUMN IF NOT EXISTS Content LowCardinality(String) DEFAULT '' AFTER Input",
		"ALTER TABLE weft_records ADD COLUMN IF NOT EXISTS TruncatedBytes Int64 DEFAULT 0 AFTER Content",
		"ALTER TABLE weft_records ADD COLUMN IF NOT EXISTS SystemHash LowCardinality(String) DEFAULT '' AFTER TruncatedBytes",
		"ALTER TABLE weft_records ADD COLUMN IF NOT EXISTS CatalogHash LowCardinality(String) DEFAULT '' AFTER SystemHash",
		"ALTER TABLE weft_runs ADD COLUMN IF NOT EXISTS InstructionsHash SimpleAggregateFunction(max, String)",
		"ALTER TABLE weft_runs ADD COLUMN IF NOT EXISTS CatalogHash SimpleAggregateFunction(max, String)",
		"ALTER TABLE weft_runs ADD COLUMN IF NOT EXISTS RequestCount SimpleAggregateFunction(max, Int64)",
		recordsAlter, logsAlter, tracesAlter,
	}
	if len(stmts) != len(wantPrefixes) {
		t.Fatalf("0004 has %d statements, want %d", len(stmts), len(wantPrefixes))
	}
	for i, p := range wantPrefixes {
		if !strings.HasPrefix(stmts[i], p) {
			t.Errorf("0004 statement %d = %q, want prefix %q", i, stmts[i], p)
		}
	}
	got := func(i int, alter string) string { return strings.TrimSpace(strings.TrimPrefix(stmts[i], alter)) }

	records := strings.NewReplacer(
		`    if(LogAttributes['weft.event.pos'] != '', toInt64OrZero(LogAttributes['weft.event.pos']),
      if(LogAttributes['weft.messages.index'] != '', toInt64OrZero(LogAttributes['weft.messages.index']), -1)) AS Pos,`,
		`    multiIf(LogAttributes['weft.event.pos'] != '', toInt64OrZero(LogAttributes['weft.event.pos']),
      LogAttributes['weft.messages.index'] != '', toInt64OrZero(LogAttributes['weft.messages.index']),
      LogAttributes['weft.request.index'] != '', toInt64OrZero(LogAttributes['weft.request.index']),
      LogAttributes['weft.prompt.index'] != '', toInt64OrZero(LogAttributes['weft.prompt.index']),
      LogAttributes['weft.tools.index'] != '', toInt64OrZero(LogAttributes['weft.tools.index']), -1) AS Pos,`,
		`    LogAttributes['weft.event.type'] AS EventType,
`,
		`    LogAttributes['weft.event.type'] AS EventType,
    if(LogAttributes['weft.step.index'] != '', toInt32OrZero(LogAttributes['weft.step.index']), -1) AS Step,
    LogAttributes['weft.messages.reason'] AS Reason,
    toInt8(LogAttributes['weft.messages.input'] = 'true') AS Input,
    LogAttributes['weft.content'] AS Content,
    toInt64OrZero(LogAttributes['weft.content.truncated_bytes']) AS TruncatedBytes,
    LogAttributes['weft.system.hash'] AS SystemHash,
    LogAttributes['weft.catalog.hash'] AS CatalogHash,
`,
		`IN ('event', 'messages')`, `IN ('event', 'messages', 'request', 'prompt', 'tools')`,
	).Replace(selectAfter(init, "CREATE MATERIALIZED VIEW IF NOT EXISTS weft_records_mv TO weft_records AS"))
	if now := got(10, recordsAlter); now != records {
		t.Errorf("0004's records select is not 0001's with the request kinds, Step, Reason, Input, Content, TruncatedBytes and the two hashes:\n%s\nwant:\n%s", now, records)
	}

	tuple := `'error.type',
        'weft.request.index', 'weft.prompt.index', 'weft.tools.index',
        'weft.system.hash', 'weft.catalog.hash', 'weft.attempt.index',
        'weft.instructions.hash', 'weft.messages.reason', 'weft.messages.from_seq',
        'weft.messages.to_seq', 'weft.compaction.hash', 'weft.compaction.scope',
        'gen_ai.response.model', 'weft.stream', 'weft.ttft_ms', 'weft.latency_ms',
        'weft.attempt.retry_after_ms',
        'weft.override.instructions', 'weft.override.max_steps', 'weft.override.model',
        'weft.override.parallelism', 'weft.override.params', 'weft.override.park_all_except',
        'weft.override.park_on', 'weft.override.thinking', 'weft.override.tool_choice',
        'weft.override.tools'], k)`
	logsDelta := "toInt64OrZero(LogAttributes['weft.delta.pos']) + 1, 0) AS DeltaCount"
	logs := strings.NewReplacer(
		"'error.type'], k)", tuple,
		logsDelta, logsDelta+`,
    if(LogAttributes['weft.event.type'] = 'run_start', LogAttributes['weft.instructions.hash'], '') AS InstructionsHash,
    if(LogAttributes['weft.record'] = 'request' AND LogAttributes['weft.request.index'] = '0', LogAttributes['weft.catalog.hash'], '') AS CatalogHash,
    if(LogAttributes['weft.record'] = 'request' AND LogAttributes['weft.request.index'] != '', toInt64OrZero(LogAttributes['weft.request.index']) + 1, 0) AS RequestCount`,
	).Replace(selectAfter(init, "CREATE MATERIALIZED VIEW IF NOT EXISTS weft_runs_logs_mv TO weft_runs AS"))
	if now := got(11, logsAlter); now != logs {
		t.Errorf("0004's logs run view is not 0001's with the tuple and the three columns:\n%s\nwant:\n%s", now, logs)
	}

	traces := strings.NewReplacer(
		"'error.type'], k)", tuple,
		"    toInt64(0) AS DeltaCount", `    toInt64(0) AS DeltaCount,
    if(isInvoke, SpanAttributes['weft.instructions.hash'], '') AS InstructionsHash,
    '' AS CatalogHash,
    toInt64(0) AS RequestCount`,
	).Replace(selectAfter(fix, tracesAlter))
	if now := got(12, tracesAlter); now != traces {
		t.Errorf("0004's traces run view is not 0003's with the tuple and the three columns:\n%s\nwant:\n%s", now, traces)
	}
}

func TestRunsInnerParentScoping(t *testing.T) {
	for _, c := range []struct {
		q    obsdb.RunQuery
		want string
	}{{obsdb.RunQuery{}, "ParentRunID = ''"}, {obsdb.RunQuery{ParentRunID: "p"}, "ParentRunID = ?"}} {
		sql, _ := runsInner(c.q)
		if !strings.Contains(sql, c.want) {
			t.Errorf("runsInner(%+v) missing %q", c.q, c.want)
		}
	}
	sql, _ := runsInner(obsdb.RunQuery{ParentRunID: "*"})
	if strings.Contains(sql, "ParentRunID =") {
		t.Error("'*' must not scope parentage")
	}
}

// Migration 0002's engine shape (offline): the two properties the
// step 8b review fixes rest on — ReplacingMergeTree(InsertTime) (the
// newest write wins under FINAL; the version column, not a timestamp
// of ours) and InsertTime DEFAULT now64(9) (the engine's own
// microsecond-precise write time the ordering reads). TestSpecEnginesPresent
// reads only 0001; without this pin a 0002 regression is caught by
// nothing offline (the audit's P2-5).
func TestSpecExperimentsEnginePresent(t *testing.T) {
	body, err := migrationsFS.ReadFile("migrations/" + migrations[2])
	if err != nil {
		t.Fatal(err)
	}
	ddl := string(body)
	for _, want := range []string{
		"InsertTime DateTime64(9, 'UTC') DEFAULT now64(9)",
		"ENGINE = ReplacingMergeTree(InsertTime)\nORDER BY Id",
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("migration 0002 missing %q:\n%s", want, ddl)
		}
	}
}

// stubConn is a ch.Conn whose Query returns canned rows: the offline
// seat for the failure paths a live server will not produce on demand.
type stubConn struct {
	ch.Conn
	rows *stubRows
}

func (c stubConn) Query(context.Context, string, ...any) (driver.Rows, error) { return c.rows, nil }

// stubRows yields n rows whose Scan fails with scanErr (when set), then
// ends with err; it records Close.
type stubRows struct {
	driver.Rows
	n       int
	scanErr error
	err     error
	closed  bool
}

func (r *stubRows) Next() bool {
	if r.n == 0 {
		return false
	}
	r.n--
	return true
}
func (r *stubRows) Scan(...any) error { return r.scanErr }
func (r *stubRows) Err() error        { return r.err }
func (r *stubRows) Close() error      { r.closed = true; return nil }

// A read that failed mid-stream is not a missing experiment: Next
// reports false and Err carries the failure. Experiment answered
// ErrNotFound for it, which SaveExperiment takes as "first save" — the
// update then reset Created (P2-4's bug through the stream's door).
func TestExperimentSurfacesStreamError(t *testing.T) {
	boom := errors.New("code: 241, memory limit exceeded")
	rows := &stubRows{err: boom}
	d := &DB{conn: stubConn{rows: rows}}
	if _, err := d.Experiment(context.Background(), "exp_1"); !errors.Is(err, boom) {
		t.Errorf("Experiment on a failed read = %v, want the read's error (never ErrNotFound)", err)
	}
	if !rows.closed {
		t.Error("Experiment left its rows open")
	}
}

// Experiments closes its rows on every path: an early return from a
// row that fails to scan left the stream — and the pooled connection
// its reader holds — open.
func TestExperimentsClosesRowsOnError(t *testing.T) {
	boom := errors.New("scan failed")
	rows := &stubRows{n: 1, scanErr: boom}
	d := &DB{conn: stubConn{rows: rows}}
	if _, err := d.Experiments(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("Experiments = %v, want the scan error", err)
	}
	if !rows.closed {
		t.Error("Experiments returned on a scan error without closing its rows")
	}
}
