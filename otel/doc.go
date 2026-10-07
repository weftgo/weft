// Package otel wires weft's observability to one or more
// OpenTelemetry destinations at once — the local sink, Weft Studio,
// Datadog, Langfuse, any OTLP endpoint or your own exporters — each
// with its own signals and content policy (ADR 0024, S2).
//
// One Install, one set of spans and records, fanned out:
//
//	defer otel.Install(
//	    otel.Local("weft.db"),                          // local sink: offline, replay-grade, content on
//	    otel.Studio("https://studio.example", token),   // content on, traces + logs
//	    otel.Datadog(),                                 // the local Datadog Agent's OTLP intake, content off
//	    otel.Langfuse("https://cloud.langfuse.com", pk, sk), // traces only
//	)()
//
// The rules that shape it:
//
//   - Content is captured once and shaped per destination [D2]. Every
//     processor registered here answers the Logs API's Enabled by event
//     name: the core asks one question ("weft.messages") and emits
//     content when any destination wants it. Content-off chains clone
//     the record first (the SDK hands every processor the same pointer),
//     strip the body with core.StripContent and drop messages records;
//     content-on chains redact and cap event and delta bodies and
//     redact (never cap) the transcript records part by part. The core reads no environment variable — this
//     module does, here only.
//   - The run tracker keeps the set of open runs and emits one
//     weft.heartbeat record per open run per interval, so a 45 s tool
//     call reads running and a crashed process stops heartbeating (the
//     "interrupted" rule stays honest). Sinks never store heartbeats.
//   - The Local destination writes obsdb/sqlite synchronously: nothing
//     emitted is lost, and the DB's hub is setup A's live lane.
//   - Install never panics and never fails the program; Start returns
//     errors and leaves the globals alone under NoGlobal (tests).
//
// This module depends on the OTel SDK and the OTLP/HTTP exporters; the
// core never does. It never imports weft/thread (the runtime link is
// its own module, weft/runtime, D6).
package otel
