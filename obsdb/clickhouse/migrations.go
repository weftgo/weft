package clickhouse

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"

	"github.com/weftgo/weft/obsdb"
)

// The migrations are embedded SQL applied in order on Open, tracked in
// obsdb_migrations — the same table name and numbering rule as the
// SQLite backend (S3.6), so one ClickHouse database's schema is
// versioned exactly like one SQLite file's. A database whose recorded
// version is ahead of this binary's highest fails Open with
// ErrNewerSchema: never run nothing and say nothing.

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrations maps version → file name; derived from the embedded
// directory so a new .sql file is picked up by adding it.
var migrations = func() map[int]string {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		panic("clickhouse: embedded migrations unreadable: " + err.Error())
	}
	out := map[int]string{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		v, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			panic("clickhouse: migration " + name + " must start with its numeric version")
		}
		out[v] = name
	}
	return out
}()

func sortedVersions() []int {
	versions := make([]int, 0, len(migrations))
	for v := range migrations {
		versions = append(versions, v)
	}
	sort.Ints(versions)
	return versions
}

func highestMigration() int {
	v := sortedVersions()
	if len(v) == 0 {
		return 0
	}
	return v[len(v)-1]
}

// postMigrations are Go steps a migration runs after its SQL: what SQL
// alone cannot derive. A step must be safe to run twice (two racing
// Opens may both run it): re-deriving inserts rows the max aggregates
// fold together. from is the version the database held when this Open
// began (0 for a new one): a step whose work depends on what an older
// writer left (0031's) reads it.
var postMigrations = map[int]func(ctx context.Context, conn ch.Conn, from int) error{
	31: rederiveStock,
}

// derivedRunsMigration is the migration that first wrote derived runs
// (0009): a database opened below it holds stock spans no writer
// derived.
const derivedRunsMigration = 9

// rederiveChunk is how many traces migration 0031's step derives per
// read and insert.
const rederiveChunk = 1000

// rederiveStock is migration 0031's Go step: the traces whose derivation
// the 0031 rule changes (chRederive0031; every stock candidate trace,
// chStockOp, for a database older than 0009, which never had derived
// runs written) re-derived. Open waits for it: one INFO line before,
// with the count, and one after, with the time.
//
// It is rederiveTraces' order in chunks of rederiveChunk traces — read
// the chunk's spans, derive, insert every row in one synchronous insert
// (async_insert=0: a migration waits for no flush timer), read the spans
// again and reconcile each trace against one read of the chunk's stored
// derived rows — so the step costs a few queries per thousand traces,
// not several round trips (and an async flush) per trace. Re-deriving is
// idempotent: an interrupted step leaves 0031 unrecorded, and the next
// Open runs it again from the start.
func rederiveStock(ctx context.Context, conn ch.Conn, from int) error {
	where := chRederive0031
	if from < derivedRunsMigration {
		where = chStockOp
	}
	rs, err := conn.Query(ctx, `SELECT DISTINCT TraceId FROM otel_traces WHERE `+where)
	if err != nil {
		return err
	}
	var traces []string
	for rs.Next() {
		var t string
		if err := rs.Scan(&t); err != nil {
			_ = rs.Close()
			return err
		}
		traces = append(traces, t)
	}
	if err := rs.Close(); err != nil {
		return err
	}
	if len(traces) == 0 {
		return nil
	}
	slog.Default().InfoContext(ctx, fmt.Sprintf("weft/obsdb/clickhouse: migrating: re-deriving %d stock traces (ClickHouse 0031); Open waits for it", len(traces)),
		"traces", len(traces))
	began := time.Now()
	ctx = ch.Context(ctx, ch.WithSettings(ch.Settings{"async_insert": 0, "max_partitions_per_insert_block": 0}))
	d := &DB{conn: conn}
	for len(traces) > 0 {
		n := min(rederiveChunk, len(traces))
		if err := d.rederiveChunk(ctx, traces[:n]); err != nil {
			return err
		}
		traces = traces[n:]
	}
	slog.Default().InfoContext(ctx, "weft/obsdb/clickhouse: migration 0031 re-derive done",
		"duration", time.Since(began).Round(time.Millisecond).String())
	return nil
}

// rederiveChunk is rederiveStock's work on one chunk of traces.
func (d *DB) rederiveChunk(ctx context.Context, traces []string) error {
	const read = "SELECT " + spanColumns + " FROM otel_traces WHERE TraceId IN ?" + spanDedup
	spans, err := d.querySpans(ctx, read, traces)
	if err != nil {
		return err
	}
	if err := d.insertDerived(ctx, rowsOf(obsdb.DeriveStockRuns(spans))); err != nil {
		return err
	}
	fresh, err := d.querySpans(ctx, read, traces)
	if err != nil {
		return err
	}
	byTrace := map[string][]obsdb.Span{}
	for _, s := range fresh {
		byTrace[s.TraceID] = append(byTrace[s.TraceID], s)
	}
	lower := make([]string, len(traces))
	for i, t := range traces {
		lower[i] = strings.ToLower(t)
	}
	stored, err := d.storedDerived(ctx, `SELECT RunId, max(ParentRunID), max(Agent), max(SessionID) FROM weft_runs
		WHERE startsWith(RunId, 'otel-') AND substring(RunId, 6, 32) IN ? AND Derived = 1 GROUP BY RunId`, lower)
	if err != nil {
		return err
	}
	byTraceStored := map[string][]derivedRow{}
	for _, r := range stored {
		t, _, _ := obsdb.ParseStockRunID(r.id)
		byTraceStored[t] = append(byTraceStored[t], r)
	}
	for _, t := range traces {
		sp := byTrace[t]
		if _, err := d.reconcileRows(ctx, t, sp, obsdb.DeriveStockRuns(sp), byTraceStored[strings.ToLower(t)]); err != nil {
			return err
		}
	}
	return nil
}

// chRederive0031 is rederiveStock's selection over an otel_traces row:
// no weft.run.id, and either an invoke_agent span (chStockOp's operation
// rule) without a string gen_ai.conversation.id — empty in the map, or,
// on a row this package wrote, not a non-empty JSON string in WeftAttrs
// (obsdb's attr reads only a string) — or a chat span carrying a key
// obsdb.StockHasMessages reads: gen_ai.system_instructions,
// gen_ai.input.messages, gen_ai.output.messages, gen_ai.prompt,
// gen_ai.completion, gen_ai.content.prompt, gen_ai.content.completion,
// or a key starting gen_ai.prompt. or gen_ai.completion. Every other
// stock trace derives exactly as the 0.19 writer derived it: a top-level
// agent without a conversation id is the rule's one new root, and a
// chat span's message content the row's one new count.
const chRederive0031 = `(RunId = '' AND (
	((SpanAttributes['gen_ai.operation.name'] = 'invoke_agent'
		OR (SpanAttributes['gen_ai.operation.name'] = '' AND startsWith(SpanName, 'invoke_agent')))
	AND (SpanAttributes['gen_ai.conversation.id'] = ''
		OR (WeftAttrs != '' AND (JSONType(WeftAttrs, 'gen_ai.conversation.id') != 'String'
			OR JSONExtractString(WeftAttrs, 'gen_ai.conversation.id') = ''))))
	OR ((SpanAttributes['gen_ai.operation.name'] = 'chat'
		OR (SpanAttributes['gen_ai.operation.name'] = '' AND (SpanName = 'chat' OR startsWith(SpanName, 'chat '))))
	AND (mapContains(SpanAttributes, 'gen_ai.system_instructions')
		OR mapContains(SpanAttributes, 'gen_ai.input.messages')
		OR mapContains(SpanAttributes, 'gen_ai.output.messages')
		OR mapContains(SpanAttributes, 'gen_ai.prompt')
		OR mapContains(SpanAttributes, 'gen_ai.completion')
		OR mapContains(SpanAttributes, 'gen_ai.content.prompt')
		OR mapContains(SpanAttributes, 'gen_ai.content.completion')
		OR arrayExists(k -> startsWith(k, 'gen_ai.prompt.') OR startsWith(k, 'gen_ai.completion.'), mapKeys(SpanAttributes))))))`

// ErrNewerSchema is returned by Open when the database's
// obsdb_migrations is ahead of this binary's highest migration: a
// database written by a newer weft fails loudly instead of silently
// ignoring tables it does not know.
var ErrNewerSchema = errors.New("clickhouse: database schema is newer than this obsdb")

// migrate creates obsdb_migrations when missing, refuses a schema ahead
// of this binary, and applies pending migrations in order. ClickHouse
// DDL is idempotent by construction here (every statement is CREATE ...
// IF NOT EXISTS), so two racing Opens converge without the re-read dance
// the SQLite runner needs.
func migrate(ctx context.Context, conn ch.Conn) error {
	if err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS obsdb_migrations (
		version UInt32,
		name String,
		applied_at DateTime DEFAULT now()
	) ENGINE = MergeTree ORDER BY version`); err != nil {
		return err
	}
	var current *uint32
	if err := conn.QueryRow(ctx, `SELECT max(version) FROM obsdb_migrations`).Scan(&current); err != nil {
		return err
	}
	if current != nil && int(*current) > highestMigration() {
		return fmt.Errorf("%w: database has migration %d, this binary knows up to %d",
			ErrNewerSchema, *current, highestMigration())
	}
	for _, v := range sortedVersions() {
		if current != nil && int(*current) >= v {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + migrations[v])
		if err != nil {
			return err
		}
		for _, stmt := range splitStatements(string(body)) {
			if err := conn.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("clickhouse: migration %s: %w", migrations[v], err)
			}
		}
		if post := postMigrations[v]; post != nil {
			from := 0
			if current != nil {
				from = int(*current)
			}
			if err := post(ctx, conn, from); err != nil {
				return fmt.Errorf("clickhouse: migration %s: %w", migrations[v], err)
			}
		}
		if err := conn.Exec(ctx, `INSERT INTO obsdb_migrations (version, name) VALUES (?, ?)`,
			uint32(v), migrations[v]); err != nil {
			return err
		}
	}
	return nil
}

// splitStatements splits a migration body into single statements: the
// runner sends one query per Exec. Only whole-line `--` comments are
// stripped (the migration files carry no inline comment markers), then
// semicolons separate statements; the OTLP shapes contain no string
// literals with semicolons.
func splitStatements(body string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		for {
			idx := strings.Index(line, ";")
			if idx < 0 {
				cur.WriteString(line)
				cur.WriteString("\n")
				break
			}
			cur.WriteString(line[:idx])
			if stmt := strings.TrimSpace(cur.String()); stmt != "" {
				out = append(out, stmt)
			}
			cur.Reset()
			line = line[idx+1:]
		}
	}
	if stmt := strings.TrimSpace(cur.String()); stmt != "" {
		out = append(out, stmt)
	}
	return out
}
