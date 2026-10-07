// Package clickhouse is obsdb's hosted backend (ADR 0024, S3.6): the
// observability schema as ClickHouse tables that a stock OTel Collector
// can also feed.
//
// # The stance: column-compatible, not DDL-verbatim
//
// otel_traces and otel_logs keep every column name and type the OTel
// Collector's ClickHouse exporter INSERT expects — pinned at exporter
// v0.162.0, the version recorded in migrations/0001_init.sql — with the
// weft identity materialized beside them (skip-indexed bloom filters on
// SessionId, PublicId, Agent and TraceId, S3.6). The exporter's README
// tells production users to manage their own schema (create_schema:
// false); ours owns the engines, the TTLs and the weft columns, and a
// stock collector pinned to that version can write into the same
// database — the collector-shape test inserts with its exact column
// lists. Attribute values in the collector's map columns are strings by
// that schema's design; the package writes the typed maps into its own
// JSON columns (WeftAttrs and friends, defaulted empty so collector
// inserts never name them) and reads them back exactly, falling back to
// the map columns for collector-written rows.
//
// # The weft tables
//
// weft_records is a ReplacingMergeTree(InsertTime) ORDER BY
// (RunId, Kind, Pos) — the transport idempotency key (I4) — filled by
// a materialized view from otel_logs where weft.record IN ('event',
// 'messages'): never 'delta' (D3: counted in weft_runs, never stored)
// and never 'heartbeat' (no position; it only moves last-seen). It
// carries its own content TTL, because a source table's TTL does not
// cascade through a view. Exact reads use FINAL or LIMIT 1 BY on the
// dedup key; weft records are immutable by construction, so dedup only
// ever collapses transport duplicates.
//
// weft_runs is an AggregatingMergeTree ORDER BY RunId filled by two
// materialized views, from otel_logs (heartbeats included, for
// last-seen) and otel_traces: min for started, max for finished,
// last-seen, identity strings, the terminal flags and usage — only one
// non-empty value exists per run, so max never invents one, and a
// retried batch's identical bytes cannot move a max. Event and message
// counts are derived at read from weft_records (uniqExact over the
// dedup key), which a retry cannot inflate. Sessions are a GROUP BY
// SessionId over weft_runs.
//
// # Open, writes, reads
//
// Open(dsn, opts...) returns an obsdb.DB over the DSN's database,
// creating and versioning the schema in obsdb_migrations (the SQLite
// backend's numbering rule). Writes go through async inserts
// (async_insert=1, wait_for_async_insert=1), batched per Write call.
// Lists read through GROUP BY RunId with the same aggregates; single
// run pages read FINAL. Status is never stored: every read derives it
// through obsdb.DeriveStatus. KeepDeltas() turns delta storage on for
// debugging (rows into weft_deltas, nothing else feeds it); TTL(...)
// overrides the retention windows (content 30 days, spans and runs 90
// by default) with ALTER TABLE ... MODIFY TTL.
package clickhouse
