package clickhouse

import (
	"regexp"
	"strings"
	"testing"
	"time"

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
	if highestMigration() != 1 {
		t.Errorf("highest migration = %d, want 1", highestMigration())
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

func TestKeepDeltasOption(t *testing.T) {
	var cfg openConfig
	KeepDeltas().apply(&cfg)
	if !cfg.keepDeltas {
		t.Error("KeepDeltas did not arm delta storage")
	}
}

// The OTLP enum names the pinned exporter writes, round-tripped.
func TestOTLPEnumNames(t *testing.T) {
	for kind := 0; kind <= 6; kind++ {
		if got := spanKindInt(spanKindName(kind)); got != kind && kind <= 5 {
			t.Errorf("span kind %d round trip = %d", kind, got)
		}
	}
	if spanKindName(0) != "SPAN_KIND_UNSPECIFIED" || spanKindName(1) != "SPAN_KIND_INTERNAL" ||
		spanKindName(3) != "SPAN_KIND_CLIENT" {
		t.Error("span kind names must be the OTLP proto value names the exporter writes")
	}
	if spanKindInt("weird") != 0 {
		t.Error("unknown kind reads unspecified")
	}
	for code := 0; code <= 3; code++ {
		if got := statusCodeInt(statusCodeName(code)); got != code && code <= 2 {
			t.Errorf("status code %d round trip = %d", code, got)
		}
	}
	if statusCodeName(2) != "STATUS_CODE_ERROR" {
		t.Error("error status code name must be the OTLP proto value name")
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
// column — the SQL shape of a MetaOf subset match.
func TestRunsInnerMetaFilter(t *testing.T) {
	sql, _ := runsInner(obsdb.RunQuery{Meta: map[string]string{"tenant": "acme"}})
	for _, want := range []string{
		"JSONHas(Meta, ?)",
		"JSONExtractString(Meta, ?) = ?",
		"GROUP BY RunId",
		"FROM (SELECT * FROM weft_runs WHERE",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("runsInner missing %q:\n%s", want, sql)
		}
	}
}

// The migration's mapFilter tuples must be exactly obsdb.MetaOf's
// exclusion set — the SQL copy of contractKeys, in both run views.
func TestMigrationContractTuple(t *testing.T) {
	body, err := migrationsFS.ReadFile("migrations/" + migrations[1])
	if err != nil {
		t.Fatal(err)
	}
	marker := "NOT has(["
	count := strings.Count(string(body), marker)
	if count != 2 {
		t.Fatalf("expected the tuple in both run views, found %d", count)
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
		if len(sqlKeys) != len(contractKeys) {
			t.Fatalf("tuple has %d keys, contractKeys has %d", len(sqlKeys), len(contractKeys))
		}
		for i, k := range contractKeys {
			if sqlKeys[i] != k {
				t.Errorf("tuple key %d = %q, want %q", i, sqlKeys[i], k)
			}
		}
		rest = rest[start+end:]
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
