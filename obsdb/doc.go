// Package obsdb is the observability database: what weft/otel's local
// sink writes and Weft Studio reads, in one module so neither owns the
// schema (ADR 0024, S3).
//
// The model is OTLP-shaped — spans and log records, resource included —
// so a stock OpenTelemetry pipeline's data and weft's own land in the
// same tables. Weft identity (the run → session → public-id chain) is
// derived from attributes, never parsed out of ids, and is stored as
// indexed columns through the Weft struct.
//
// Three moving parts:
//
//   - DB is the backend interface: Write (idempotent on
//     (run, record kind, position) and (trace, span)), run/session
//     queries, the event page with its gap detector, the transcript a
//     replay reads, and the spans of a run or trace. Status is derived
//     from the four-row table (DeriveStatus), never stored.
//   - The hub (Hub, Frame) is the live lane: every Write publishes its
//     frames before returning, so an in-process subscriber — Studio's
//     SSE handler in setup A — sees a record without a network hop.
//   - obsdbtest is the conformance table every backend runs (the
//     storetest pattern); sqlite is the default backend, clickhouse the
//     hosted one (its own module).
//
// Deltas travel but are not stored (their own counter means their
// absence never looks like a lost event); heartbeats are never stored,
// they only move a run's last-seen. Both rules are Write's, shared by
// every backend.
package obsdb
