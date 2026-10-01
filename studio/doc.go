// Package studio is the Inspector: a read-only UI over an obsdb
// database (the observability schema weft/otel writes), served by Go
// alone.
//
// T1 (TODO §12, plan docs/phase2b-studio-plan.md) shows the runs list
// and the run page — steps with their tool calls and results, subagents
// inline, truncation badged, event replay at 1×/4×, raw JSON — plus
// agent and tool cards from the manifest. The UI is a TanStack Start
// SPA (ADR 0018), prebuilt into the committed dist/ and embedded here;
// users of this module never run Bun.
//
// Handler is the whole mount surface:
//
//	mux.Handle("/studio/", http.StripPrefix("/studio",
//		studio.Handler(studio.DB(db))))
//
// It serves the embedded UI with a history fallback (deep links
// survive reload) and a read-only JSON API under api/: meta, runs,
// runs/{id}, runs/{id}/events, manifest. The API is the contract
// boundary (ADR 0018 Consequences): its types are written by hand in
// api.go and mirrored in web/src/lib/api.ts, pinned on the Go side by
// golden tests and on the TS side by type-checking against the same
// golden files.
//
// Two rules keep one bundle able to serve local, self-hosted and
// hosted Studio without hosting code in T1 (ADR 0018 §8):
//
//   - api/meta reports capabilities — the open Handler reports none.
//     The UI gates every deployment-specific screen on a named
//     capability; a hosted server declares them with Capabilities(…).
//   - a run's events are paged through api/runs/{id}/events, never
//     inline in the run document, so neither response size nor client
//     memory grows with run length and a live tail is the same
//     endpoint read from the last position.
package studio
