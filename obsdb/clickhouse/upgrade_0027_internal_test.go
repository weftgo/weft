package clickhouse

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/weftgo/weft/obsdb/obsdbtest"
)

// stale0024 is migration 0024's first text, as development builds
// before P9-pre-go-fix2 applied it: a summarizer span (weft.chat.role
// set) still feeds the run row, behind isSummarizer guards. 0024 was
// amended in place; 0027 restates the amended view for the databases
// that applied this one.
const stale0024 = `ALTER TABLE weft_runs_traces_mv MODIFY QUERY
WITH if(mapContains(SpanAttributes, 'gen_ai.operation.name'),
       SpanAttributes['gen_ai.operation.name'] = 'invoke_agent',
       startsWith(SpanName, 'invoke_agent')) AS isInvoke,
     StatusCode IN ('Error', 'STATUS_CODE_ERROR') AS isError,
     SpanAttributes['gen_ai.operation.name'] AS op,
     SpanAttributes['weft.chat.role'] != '' AS isSummarizer,
     mapFilter((k, v) -> NOT has([
        'weft.run.id', 'weft.parent.run.id', 'weft.parent.call.id',
        'weft.session.id', 'weft.public_id', 'weft.turn', 'gen_ai.agent.name',
        'weft.record', 'weft.event.type', 'weft.event.pos', 'weft.delta.pos',
        'weft.messages.index', 'weft.step.index', 'weft.tool.seq',
        'weft.playground', 'weft.experiment.id', 'weft.forked_from',
        'weft.messages.count', 'weft.messages.input', 'weft.content',
        'weft.content.truncated_bytes', 'weft.version', 'weft.manifest.hash',
        'weft.run.steps', 'weft.run.pending', 'weft.run.stop_reason',
        'weft.stop.raw', 'weft.model.tool_calls', 'weft.tool.approved',
        'weft.tool.pending', 'weft.tool.result_bytes', 'weft.metadata.dropped',
        'weft.override.hash',
        'gen_ai.operation.name', 'gen_ai.provider.name', 'gen_ai.request.model',
        'gen_ai.response.finish_reasons', 'gen_ai.usage.input_tokens',
        'gen_ai.usage.output_tokens', 'gen_ai.usage.cache_read.input_tokens',
        'gen_ai.usage.cache_creation.input_tokens', 'gen_ai.usage.reasoning.output_tokens',
        'gen_ai.tool.name', 'gen_ai.tool.call.id', 'gen_ai.conversation.id',
        'session.id', 'user.id', 'error.type',
        'weft.request.index', 'weft.prompt.index', 'weft.tools.index',
        'weft.system.hash', 'weft.catalog.hash', 'weft.attempt.index',
        'weft.instructions.hash', 'weft.messages.reason', 'weft.messages.from_seq',
        'weft.messages.to_seq', 'weft.compaction.hash', 'weft.compaction.scope',
        'gen_ai.response.model', 'weft.stream', 'weft.ttft_ms', 'weft.latency_ms',
        'weft.attempt.retry_after_ms',
        'weft.override.instructions', 'weft.override.max_steps', 'weft.override.model',
        'weft.override.parallelism', 'weft.override.params', 'weft.override.park_all_except',
        'weft.override.park_on', 'weft.override.thinking', 'weft.override.tool_choice',
        'weft.override.tools',
        'weft.raw.index',
        'weft.run.stopped_by', 'weft.cancel.cause',
        'weft.run.events', 'weft.run.messages',
        'weft.records.dropped',
        'weft.run.denied', 'weft.tool.truncated_bytes', 'weft.tool.input_error',
        'weft.panic.layer',
        'weft.attempt.layer',
        'weft.tool.retry', 'weft.tool.retry_budget', 'weft.tap.panics',
        'weft.resumed_from', 'weft.resume.calls', 'weft.tool.denied', 'weft.tool.denied_kind',
        'weft.logs.bridged',
        'weft.attempt.wait_ms', 'http.response.status_code', 'weft.tool.pending.source',
        'weft.tool.timeout_ms', 'weft.tool.queue_ms', 'weft.subagent.pending', 'weft.output.submitted',
        'weft.caller.run.id', 'weft.caller.call.id',
        'weft.chat.role', 'weft.tool.approved_by', 'weft.grant.id'], k), SpanAttributes) AS metaMap
SELECT
    SpanAttributes['weft.run.id'] AS RunId,
    SpanAttributes['weft.parent.run.id'] AS ParentRunID,
    SpanAttributes['weft.parent.call.id'] AS ParentCallID,
    if(isSummarizer, '', TraceId) AS TraceID,
    SpanAttributes['gen_ai.agent.name'] AS Agent,
    if(isSummarizer, '', SpanAttributes['gen_ai.provider.name']) AS Provider,
    if(isSummarizer, '', SpanAttributes['gen_ai.request.model']) AS Model,
    SpanAttributes['weft.manifest.hash'] AS ManifestHash,
    if(isInvoke, if(SpanAttributes['weft.version'] != '', SpanAttributes['weft.version'], ResourceAttributes['weft.version']), '') AS WeftVersion,
    ServiceName AS Service,
    SpanAttributes['weft.session.id'] AS SessionID,
    SpanAttributes['weft.public_id'] AS PublicID,
    toInt32OrZero(SpanAttributes['weft.turn']) AS Turn,
    toUInt8(SpanAttributes['weft.playground'] = 'true') AS Playground,
    SpanAttributes['weft.experiment.id'] AS ExperimentID,
    SpanAttributes['weft.forked_from'] AS ForkedFrom,
    if(isSummarizer OR length(metaMap) = 0, '', toJSONString(metaMap)) AS Meta,
    Timestamp AS Started,
    if(isInvoke, Timestamp + toIntervalNanosecond(toInt64(Duration)), NULL) AS Finished,
    Timestamp + toIntervalNanosecond(toInt64(Duration)) AS LastSeen,
    toUInt8(0) AS FinishedOK,
    toUInt8(isInvoke AND isError) AS Failed,
    if(isInvoke AND isError, StatusMessage, '') AS Err,
    if(isInvoke, toInt32OrZero(SpanAttributes['weft.run.steps']), 0) AS Steps,
    if(isInvoke, toInt32OrZero(SpanAttributes['weft.run.pending']), 0) AS Pending,
    if(isInvoke, SpanAttributes['weft.run.stop_reason'], '') AS StopReason,
    if(isInvoke, toInt64OrZero(SpanAttributes['gen_ai.usage.input_tokens']), 0) AS InputTokens,
    if(isInvoke, toInt64OrZero(SpanAttributes['gen_ai.usage.output_tokens']), 0) AS OutputTokens,
    if(isInvoke, toInt64OrZero(SpanAttributes['gen_ai.usage.cache_read.input_tokens']), 0) AS CachedInputTokens,
    if(isInvoke, toInt64OrZero(SpanAttributes['gen_ai.usage.cache_creation.input_tokens']), 0) AS CacheWriteTokens,
    if(isInvoke, toInt64OrZero(SpanAttributes['gen_ai.usage.reasoning.output_tokens']), 0) AS ReasoningTokens,
    toInt64(0) AS DeltaCount,
    if(isInvoke, SpanAttributes['weft.instructions.hash'], '') AS InstructionsHash,
    '' AS CatalogHash,
    toInt64(0) AS RequestCount,
    toUInt8(isInvoke) AS RootEnded,
    if(isInvoke, SpanAttributes['weft.run.stopped_by'], '') AS StoppedBy,
    if(isInvoke, SpanAttributes['weft.cancel.cause'], '') AS CancelCause,
    if(isInvoke, toInt64OrZero(SpanAttributes['weft.run.events']), 0) AS EventsEmitted,
    if(isInvoke, toInt64OrZero(SpanAttributes['weft.run.messages']), 0) AS MessagesEmitted,
    if(isInvoke, multiIf(SpanAttributes['error.type'] IN ('context.Canceled', 'context.DeadlineExceeded'), 'cancelled', SpanAttributes['error.type']), '') AS ErrorType,
    arrayFilter(x -> x != '', [
        if(SpanName = 'attempt' AND op = '',
           concat('attempt:', SpanId, ':', ParentSpanId, ':', toString(toInt64OrZero(SpanAttributes['weft.attempt.index'])), ':', if(isError, '1', '0')), ''),
        if(SpanName = 'attempt' AND op = '' AND NOT isError AND SpanAttributes['weft.attempt.layer'] = 'fallback', concat('fallback_answer:', SpanId), ''),
        if(op = 'execute_tool' AND isError AND SpanAttributes['weft.tool.denied'] != 'true' AND SpanAttributes['error.type'] NOT IN ('RETRY', 'context.Canceled', 'context.DeadlineExceeded'), concat(if(SpanAttributes['error.type'] = 'DENIED', 'denied:', 'tool_failed:'), SpanId), ''),
        if(op = 'execute_tool' AND toInt64OrZero(SpanAttributes['weft.tool.truncated_bytes']) > 0, concat('truncated:', SpanId), ''),
        if(op = 'invoke_agent' AND toInt64OrZero(SpanAttributes['weft.run.denied']) > 0,
           concat('denied_approval:', SpanId, ':', toString(toInt64OrZero(SpanAttributes['weft.run.denied']))), ''),
        if(op = 'invoke_agent' AND toInt64OrZero(SpanAttributes['weft.tap.panics']) > 0,
           concat('tap_panics:', SpanId, ':', toString(toInt64OrZero(SpanAttributes['weft.tap.panics']))), ''),
        if(op = 'invoke_agent' AND toInt64OrZero(SpanAttributes['weft.metadata.dropped']) > 0,
           concat('metadata_dropped:', SpanId, ':', toString(toInt64OrZero(SpanAttributes['weft.metadata.dropped']))), '')
    ]) AS Problems,
    SpanAttributes['weft.resumed_from'] AS ResumedFrom,
    SpanAttributes['weft.caller.run.id'] AS CallerRunID,
    SpanAttributes['weft.caller.call.id'] AS CallerCallID,
    SpanAttributes['weft.session.parent'] AS SessionParent
FROM otel_traces
WHERE SpanAttributes['weft.run.id'] != '';
`

// TestMigration0027Restates0024 (P9-pre-go-fix3): 0027 is 0024's
// amended statement verbatim — one MODIFY QUERY, so a fresh database
// (amended 0024, then 0027) and an upgraded one (stale 0024, then 0027)
// hold the same view. Offline.
func TestMigration0027Restates0024(t *testing.T) {
	read := func(v int) []string {
		b, err := migrationsFS.ReadFile("migrations/" + migrations[v])
		if err != nil {
			t.Fatal(err)
		}
		return splitStatements(string(b))
	}
	was, now := read(24), read(27)
	if len(was) != 1 || len(now) != 1 || was[0] != now[0] {
		t.Fatalf("0027 is not 0024's one statement verbatim (%d vs %d statements)", len(was), len(now))
	}
	if !strings.Contains(now[0], "AND SpanAttributes['weft.chat.role'] = ''") || strings.Contains(now[0], "isSummarizer") {
		t.Error("0027 does not leave a summarizer span out of the run row")
	}
	if s := splitStatements(stale0024); len(s) != 1 || !strings.Contains(s[0], "isSummarizer") || s[0] == now[0] {
		t.Error("stale0024 is not the first 0024 text")
	}
}

// TestUpgradeFromStale0024View (P9-pre-go-fix3): a development database
// that applied 0024's first text, stopped at 0025 or at 0026, is opened
// by this binary — 0027 restates the view — and passes
// SummarizerSpanIsNotRunFact, every check of it, on the upgraded view.
func TestUpgradeFromStale0024View(t *testing.T) {
	base := os.Getenv("WEFT_CLICKHOUSE_DSN")
	if base == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: the 0027 upgrade needs a clickhouse server (README has the one-line container recipe)")
	}
	for _, stop := range []int{25, 26} {
		t.Run(fmt.Sprintf("at %04d", stop), func(t *testing.T) {
			ctx := context.Background()
			u, err := url.Parse(base)
			if err != nil {
				t.Fatal(err)
			}
			suffix := make([]byte, 6)
			if _, err := rand.Read(suffix); err != nil {
				t.Fatal(err)
			}
			name := "weft_up_" + hex.EncodeToString(suffix)
			admin := rawConn(t, base)
			if err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE `%s`", name)); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = admin.Exec(context.Background(), fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", name))
				_ = admin.Close()
			})
			u.Path = "/" + name
			dsn := u.String()
			conn := rawConn(t, dsn)
			if err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS obsdb_migrations (
				version UInt32, name String, applied_at DateTime DEFAULT now()
			) ENGINE = MergeTree ORDER BY version`); err != nil {
				t.Fatal(err)
			}
			for v := 1; v <= stop; v++ {
				var body string
				if v == 24 {
					body = stale0024
				} else {
					b, err := migrationsFS.ReadFile("migrations/" + migrations[v])
					if err != nil {
						t.Fatal(err)
					}
					body = string(b)
				}
				for _, stmt := range splitStatements(body) {
					if err := conn.Exec(ctx, stmt); err != nil {
						t.Fatalf("migration %d: %v", v, err)
					}
				}
				if err := conn.Exec(ctx, `INSERT INTO obsdb_migrations (version, name) VALUES (?, ?)`, uint32(v), migrations[v]); err != nil {
					t.Fatal(err)
				}
			}
			view := func() string {
				t.Helper()
				var q string
				if err := conn.QueryRow(ctx, `SELECT create_table_query FROM system.tables
					WHERE database = currentDatabase() AND name = 'weft_runs_traces_mv'`).Scan(&q); err != nil {
					t.Fatal(err)
				}
				return q
			}
			if !strings.Contains(view(), "isSummarizer") {
				t.Fatal("the seeded database does not hold 0024's first view")
			}
			db, err := Open(dsn)
			if err != nil {
				t.Fatalf("open a database at %04d with the stale 0024 view: %v", stop, err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if q := view(); strings.Contains(q, "isSummarizer") || !strings.Contains(q, "weft.chat.role") {
				t.Fatalf("the view after Open is still 0024's first text (isSummarizer, no weft.chat.role filter): %d bytes", len(q))
			}
			var version uint32
			if err := conn.QueryRow(ctx, `SELECT max(version) FROM obsdb_migrations`).Scan(&version); err != nil {
				t.Fatal(err)
			}
			if version != WantSchemaVersion {
				t.Errorf("migration max after the upgrade = %d, want %d", version, WantSchemaVersion)
			}
			_ = conn.Close()
			obsdbtest.SummarizerSpanIsNotRunFact(t, db)
		})
	}
}
