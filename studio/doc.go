// Package studio is the Inspector: the UI, the JSON API, the live
// stream and the OTLP receiver over one observability database
// (weft/obsdb), served by Go alone.
//
// # The three setups (S4.6)
//
// Setup A embeds the handler next to the app's own pipeline — the
// five lines of WEFT-OTEL-DATA-ARCHITECTURE §10.1:
//
//	defer otel.Install()() // local sink ./.weft/weft.db, content on, no network
//	mux.Handle("/studio/", http.StripPrefix("/studio",
//		studio.Handler(studio.DB(otel.LocalDB())))) // same DB, same live hub [D4]
//
// Passing the pipeline's handle is what makes it live: writes publish
// to the handle's hub and the /api/live stream follows, no network
// (examples/studio-local is the whole thing, with a thread session).
// Without a Token the API answers only a loopback Host (localhost,
// *.localhost, 127.0.0.0/8, [::1]) or one an AllowOrigins origin
// names — DNS rebinding makes any site same-origin to a loopback
// Studio — so an app served on a real hostname lists its origin in
// AllowOrigins or sets a Token.
//
// Setup B runs the binary (`weft studio`, cmd/weft): the UI, OTLP ingest, SQLite
// and a dev token on 127.0.0.1:7331 — any language's app points
// WEFT_STUDIO_URL or OTEL_EXPORTER_OTLP_ENDPOINT at it.
//
// Setup C hosts it behind a Token: the panel's scoped tokens are
// HMAC-signed public-id handles minted through POST /api/panel-tokens.
//
// # Routes are groups; capabilities are computed
//
// The serving surface is a list of route groups (routes.go): New
// registers the core groups (the read API, the live stream, ingest,
// panel-token minting), and the lanes that follow add theirs in their
// own files without editing anyone else's:
//
//   - step 7 (lane C1) adds studio/panel.go, which sets
//     panelGroupHook through a package-level var initializer — no
//     init(), no registry — and the panel group is always on;
//   - step 8 (lane C2) adds studio/playground.go the same way
//     (playgroundGroupHook), enabled by the Playground(true) option.
//
// Both files exist now: the panel group registers always and names no
// capability (the panel is a client of the API), and Playground(true)
// registers the playground's groups — capabilities playground,
// runtimes, breakpoints and steer. api/meta's capabilities list is
// computed from the registered groups (live, ingest, auth with a
// Token, and the playground's) — never hard-coded — plus anything a
// hosting wrapper declares with Capabilities(...). The UI gates every
// deployment-specific screen on those names (ADR 0018 §8).
//
// # The API is the contract
//
// The JSON API (S4.2/S4.3) is the one contract between Go and the UI
// (and the panel): its types are written by hand in api.go and
// mirrored in web/src/lib/api.ts, pinned on the Go side by golden
// tests (testdata/api) and on the TS side by type-checking. Events are
// paged (ADR 0018 §8); the live tail is GET /api/live, an SSE stream
// whose frame ids are the hub's Seq — the resume cursor.
package studio
