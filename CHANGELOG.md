# Changelog

Notable changes to weft, newest first. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.0.0/); the project
is pre-1.0; since 0.9.0 a release is one tag for the framework plus
`core/vX.Y.Z` for the loop module (ADR 0027; before it, one tag per
module, ADR 0005).

## Unreleased

### Added

- **The devtools panel's honesty** (plan D5): every badge the panel
  draws comes from the A3 table through one module,
  `src/panel/badges.ts` (`<span class="weft-badge" data-hole="…">`, the
  table's label, the reason and fix as its title — the run page's span;
  `badge(hole, note)` is what the Request tab calls): on the turn's
  header, step cards, tool calls (a result cap's cut is `truncated`,
  cause `result_cap`; an unrun call of a `max_tokens` step is
  `max_tokens`), compaction markers and request lines. The table's
  causes (`log_cap`, `result_cap`, `no_public_id`, `no_spans`,
  `dev_token_only`) join `lib/honesty.ts` as `CAUSES`, checked against
  the golden. The footer's content line ("content on · 2 events
  shortened (24.6 KiB cut)", "content off · weft.Content(false)" /
  "· otel.NoContent()") replaces the "content is stripped" suffix; turn
  rows carry "scripted (0 tokens)", "fork of s_…#e_…" and "experiment
  of t3" chips from the run row (its model, `weft.session.forked_from`,
  `weft.forked_from`). `parity.test.ts` proves every hole of the table
  shows on both the panel and the run page for the same run; a grep test
  keeps hole words out of every other panel source. The panel's badge
  attribute is now `data-hole` (was `data-weft-hole`), as the run page's.
- **The devtools panel's views** (plan D4): the open turn has tabs —
  **Story** (the step story), **Request** (a placeholder E1.2 fills),
  **Timeline** (the spans waterfall at full width over a time axis in
  ms, or, on a run without spans, its steps and tool calls over the
  event sequence) and **Raw**: a hand-written JSON tree
  (`src/panel/tree.ts`, no library) of `{doc, events, transcript,
  spans}` (and `requests` when the panel holds them) with per-node
  collapse (`aria-expanded`, `→`/`←`), lazy children (only open nodes
  are built; 200 children, then "+N more"; a string over 2,048 characters shows
  "+N bytes"), a filter over keys and values that opens the path to
  each match and counts them, copy-node, copy-all (the clipboard,
  else the text selected to copy by hand) and a `<run id>.json`
  download. `/` focuses the Raw tab's filter, else the turn list's.
  ARIA tabs (`role="tablist"`, `←`/`→`/`Home`/`End`); the tab is
  remembered as `tab` in `localStorage["weft.devtools"]` (additive to
  v1; `raw` mirrors it). The turn list gets a filter box (text over
  the id, the error and the prompts the panel has read; a status;
  has error — "n of m turns") and pages older turns through the runs
  API's `before=`/`before_id=` cursor as it scrolls to its end (an
  `IntersectionObserver` on a sentinel; a button where there is none,
  and in the narrow dropdown): no 50-turn cap, "all n turns loaded".
  `r` returns to the tab before Raw (the header's `raw` is
  `aria-pressed`), the raw filter is debounced (100 ms) and a redraw
  re-walks nothing; the Story tab's node is kept across tab switches.
  +5.2 KiB gzip of its +5 KiB (with its review fixes).
- **The devtools panel renders incrementally and is read by assistive
  tech** (plan D3): a draw patches the dock in place — keyed by run id
  for turn rows and by step ordinal for step cards — so a streaming
  step re-renders only its own card, and focus, the caret, scroll and
  opened `<details>` survive a redraw because their nodes do (the
  manual restore is gone). The dock is `role="complementary"`
  (`aria-label="weft devtools"`), the turn list a `role="list"` of
  `listitem`s with a roving tabindex (`↓`/`↑` move it; `j`/`k` still
  select), the running step's text the one `aria-live="polite"`
  region, the expanders carry `aria-expanded`, every icon button an
  `aria-label`; `Tab` wraps inside an open, focused float (never when
  docked; `Esc` lets go). An axe-core run over every mode in both
  themes reports zero violations (`a11y.test.ts`; axe is a
  devDependency, never in `panel.js`), the experiment drawer and the
  `?` overlay included. The streaming text is `aria-busy` while it
  grows and a step's end is said once on a hidden `role="status"` line;
  per-call lines and dropdown options are keyed; the Studio app's
  Tailwind no longer scans the panel's sources (`@source not
  "./panel"`). +1.5 KiB gzip.
- **The devtools panel's theme** (plan D2): light and dark, following
  the host page without configuration — `data-theme="light|dark"`
  (also `weft:theme`, the script tag, `mount({theme})`) wins, then the
  user's choice from the header's `◐` button (stored as `theme` in
  `localStorage["weft.devtools"]`; auto clears it), then auto: `<html
  data-theme>`, `<html class="dark|light">`, `<html>`'s computed
  `color-scheme`, `prefers-color-scheme`, else dark. A host toggle and a
  system change are followed (passive, removed on disconnect); the
  result is `data-theme-resolved` on the element. The colours are the
  Studio app's palette (`studio/web/src/lib/palette.ts`, held in
  lockstep with `src/styles.css` by a test), contrast-checked to WCAG AA
  in both themes, and every colour, radius and font is a `--weft-*`
  custom property on the panel's `:host` — `weft-devtools { --weft-bg:
  … }` overrides it. The panel's old dark-only `--w-*` variables are
  gone.
- **The devtools panel's layout** (plan D1): the dock floats (dragged
  by its header, resized from its corner, 360×280 up to the viewport
  minus 16 px, clamped on every window resize) or docks to any side —
  `left`, `right`, `bottom`, `top` — resized along its edge; collapsed
  it is the pill (now with the current turn's tokens in→out, `—` while
  it runs), and `data-mode="hidden"` draws nothing while the API and
  events keep working. `data-position` gains `left-dock`, `top-dock` and
  `bottom-dock`; `data-mode`, `data-push="true"` (pads `<html>` on the
  docked side — the bottom sheet by its 70vh — through
  `--weft-devtools-inset`, restored exactly; off by default; a fixed or
  sticky composer uses the variable in its own CSS) and `data-z-index` / `--weft-z` are new. Under 640 px of
  panel the turn column is a dropdown; under a 480 px viewport the panel
  is a bottom sheet. The placement, the selected turn and the raw view
  are remembered per origin in `localStorage["weft.devtools"]` (`{v: 1,
  …}`; `localStorage.weft_debug=1` is migrated into it). Keys: `Alt+W`
  stays the one global; `Alt+Shift+W` (next layout), `Esc`, `j`/`k`
  (turns), `J`/`K` (steps), `g s` (open in Studio), `r`, `?` and the
  reserved `/` fire only with focus inside the panel — the panel no
  longer hears bare keys pressed on the page's body. Every panel build
  prints the per-item size table from the append-only
  `studio/web/panel-budget.json` ledger against the 80 KiB cap.
- **The devtools panel's host API** (plan C4.2): the `<weft-devtools>`
  element's `open()`, `close()`, `toggle()`, `isOpen`, `scope(s)` (a
  `Scope` or its string form; `scope(null)` hands the scope back to the
  ladder; a session-only scope resolves through
  `GET /api/sessions/{id}/public_id` for setup A and the dev token, and
  says why when it cannot), `select(runId, step?)` (a run the list does
  not show joins it when it is the conversation's, else "run r_… not in
  this conversation"), `on(event, cb)` and `studioLink(runId, step?)`
  (through `lib/links.ts`, never a token). The panel dispatches
  `weft:run` `{runId, status, publicId?, sessionId?, step?}` per status
  transition, `weft:parked` `{runId, callId, ackId, name}` once per
  parked call (`ackId` is the call id its approval takes) and
  `weft:error` `{message, runId}` once per failed run — bubbling,
  composed, asynchronous, only for the conversation followed. The
  script-tag install publishes the API as `window.weft.devtools` while a
  panel is connected (never replacing another library's `window.weft`;
  `data-global="off"` / `weft:global` opt out); `@weftgo/devtools` adds
  `select`, `isOpen` and `studioLink` and no global.
  `examples/studio-local`'s page drives the panel from its own
  "debug this" buttons and a "report this run" link, and a refund
  question parks its turn on a `weft.RequireApproval` tool. The host API
  costs +3.1 KiB gzip (ledger row "C4.2 fixes": 45,664 B). Review fixes:
  `scope()` reads `{session}` alone and `sessionId`/`runId` aliases and
  says a scope naming neither; `scope(null)` restores what `mount()`
  named; a frozen `window.weft` is said in the footer; a conversation
  returned to does not re-report its runs; an ordinal the run lacks is
  said and the last step carried; a lookup or run read Studio did not
  answer says so; a host call waits 10 s at most for a hung start;
  `error.runId` is required; the panel gate (`scripts/panel-gate.ts`) drives studio-local's
  own page script (`--page-script`); a failed deny-first in the example
  answers 409 instead of queueing.

- **`examples/devtools-vite`** (the phase 3 gate's first clause): a
  minimal Vite app that installs the packed `@weftgo/devtools` and
  shows the panel with no `<script>` tag; `make devtools-vite-check`
  builds it and proves the panel mounts (jsdom, a fake `/api/meta`)
  and that the installed `panel.js` is the served one by sha256.
- **The devtools panel's DOM-marker rung and scope switcher** (plan
  C3.3): the panel follows `data-weft-scope` markers anywhere on the
  page — the attribute the framework helpers already set — read through
  one MutationObserver (debounced) and one passive `focusin` listener,
  no global touched; with several, the marker around the focused
  element wins. A header switcher (`aria-label="conversation"`) lists
  every conversation known — explicit, markers, headers, each once, at
  most 20 — with its source and a live dot, and choosing one rescopes.
  `data-detect` accepts `markers` and `headers,markers`; the rung is on
  by default except under a read-scoped panel token off loopback, and
  the footer says `detect: markers` / `headers+markers`.
- **The devtools panel's URL rung, live fallback and activity pill**
  (plan C3.4): `?weft_scope=` (read first) or `#weft_scope=` on the host
  page — the scope's string form, URL-decoded — scopes the panel, below
  an explicit `data-scope`/`scope()` and above the marker and header
  rungs; it is re-read on `hashchange`/`popstate` (passive listeners,
  removed on disconnect), never written, and never gated (`data-detect`
  `off` turns rungs 2–3 off only; the footer says `detect: url`). With no
  scope from any rung the header reads "no conversation detected on this
  page · how to scope" (the one-line fixes behind the link) and the
  latest list follows `/api/live?agent=<the newest run's agent>` through
  a grant instead of the 10 s poll, which stays only while no stream
  covers the list (a 403 or a panel token: "streaming needs the server
  token · polling"; a second agent: said). The collapsed pill pulses
  (not under `prefers-reduced-motion`) and shows the running run's step
  count ("● 3", `aria-label` "weft devtools · running, step 3") from
  the streams the panel already holds.

- **Studio's Request pane** (plan E1.1): each run-page step card's
  request now carries the chips the request record's hashes decide —
  "changed by PrepareStep" (the step's `system_hash` moved from the
  previous step's with the tool set unchanged, or at the first step is
  not the run's `instructions_hash` plus the offered tools'
  PromptSnippets as a manifest verified for the run — by its
  `manifest_hash` — names them), the neutral "prompt changed at this
  step" when a changed tool set may explain the move, "overridden by
  experiment" (the run's invoke_agent span carries
  `weft.override.instructions`) and "catalog changed at this step";
  nothing is decided from an unverified weft.json or a ToolSource
  tool — and a bounded line diff of the system prompt against the
  previous step, or at the first step against the registered agent's
  instructions when they differ (never over a cut record), params, tool
  choice and thinking as rows ("adapter default" when nil), and the
  messages sent (count and bytes, the last three inline, the rest as a
  raw tree) from the transcript the page already holds. The story view
  reads `runs/{id}/spans` once the run ends (under the `requests`
  capability) for the override fingerprint; the time axis stays the
  trace view's. A read-scoped token still sees only the `hidden` badge.
  `examples/studio-local`'s agent gains a PrepareStep that trims a
  first-step paragraph from its system prompt from step 1 on
  (`TestPrepareStepTrimsThePrompt` pins the request record's shape).

- **`GET /api/sessions/{id}/public_id`** (plan C4.1): the reverse of
  `GET /api/public/{public_id}` — `{"session_id", "public_id"}`, the
  public id `thread.PublicID` stamped on the session's turns
  (MAX(`weft.public_id`) over its top-level turns: a session whose turns
  carry several answers the greatest, unbadged), for the devtools'
  `scope()` given a session id and no public id. A session whose turns
  carry none answers 200 with `"public_id": ""` and the `not_recorded`
  badge (cause `no_public_id`, fix `thread.Create(…,
  thread.PublicID(id))`, or set `weft.public_id` on every turn);
  an unknown session is 404 `not_found`. The dev (server) token's and
  setup A's open API's alone: every panel token, read or playground,
  is 403 with the `hidden` badge (cause `dev_token_only`) before the
  session is looked up. In the always-on read group, so `/api/meta`'s
  capabilities are unchanged. `obsdb` gains the two causes
  (`CauseNoPublicID`, `CauseDevTokenOnly`) in `HoleCauses()` and
  `studio/testdata/holes.golden.json`'s `causes`.

- **`@weftgo/devtools` on npm** (plan C1, assembled in
  `studio/web/npm`, unpublished until a release publishes it): its
  `panel.js` is `/studio/panel.js` byte for byte (sha256 equal, gated
  by `make studio-check`). `import "@weftgo/devtools"` mounts the
  panel on a page with no `<script>` tag. The typed entry exports
  `mount`, `scope`, `open`, `close`, `toggle`, `on`, `serializeScope`
  and `parseScope` (the `data-weft-scope` marker:
  `pub_…;session=…;flow=…;run=…`). `/react`, `/vue` and `/svelte`
  export marker helpers, not components. Zero runtime dependencies;
  the version is the weft version. `scope()` passes the whole scope
  to the panel (C3.2, below), and `on()`
  registers listeners but the panel dispatches no events yet (C4).
  `make devtools-npm` builds the package and dry-runs `npm pack`.

- **`weft/scope`** (plan C3.1): the Go side of the devtools' Scope.
  `scope.Scope{PublicID, SessionID, FlowID, RunID}` with `String()`,
  `IsZero()` and `scope.Parse(string) Scope`, the same string form as
  the web side's `serializeScope`/`parseScope`
  (`pub_…;session=…;flow=…;run=…`, values percent-encoded, a lenient
  parser that never fails). One golden, `studio/testdata/scope.golden.json`,
  is read by both test suites. `scope.Header(next, func(*http.Request)
  scope.Scope) http.Handler` sets the `Weft-Scope` response header on
  the app's own chat endpoint, and `scope.Set(w, s)` sets it once the
  run id is known. Both append `Weft-Scope` to
  `Access-Control-Expose-Headers`, and the header never carries a
  token. `examples/studio-local`'s `/run` answers `Weft-Scope:
  pub_demo;run=<id>`. The panel reads the header (C3.2, below). The
  package sits beside the root rather than in it: the root package is
  a generated facade over core, which never imports net/http.

- **The live grant** (plan C5): `POST /api/live-grant` (capability
  `live`, authenticated like every API route) takes one `/api/live`
  selector and its `kinds` (JSON body or query) and answers `{sig,
  exp}`; `GET /api/live?<selector>&kinds=…&sig=<sig>` opens that one
  stream as the identity that asked. The sig is HMAC-SHA256 over the
  identity, the selector, the kinds set and the expiry (60 s, never
  past a panel token's own); it is refused (401) on another selector
  or kinds set, tampered or expired. A panel token is granted only a
  stream inside its public id. Without a `Token` the key is random per
  process, so setup A takes the same path. A grant bounds opening only:
  a panel token's stream — opened with a grant or with the bearer —
  now ends at the token's expiry with one `event: expired` frame; a
  server token's stream does not.

- **The panel follows a whole scope, and detects it from response
  headers** (plan C3.2). Detection rung 1 is the explicit forms:
  `data-scope="pub_…;session=…;flow=…;run=…"` on the element, the
  `weft:scope` meta tag or the script tag, `window.__WEFT__ = { scope }`
  (or `{ publicId }`), and `mount({scope})` / `scope()` from
  `@weftgo/devtools`. The public id selects the conversation; `run` pins
  the selected turn and its live tail once, or the panel says `run r_…
  not in this conversation`; `session` narrows the turn list to the runs
  that carry that `weft.session.id`; `flow` is a header chip and filters
  nothing yet. Rung 2 reads the `Weft-Scope` header (`weft/scope`) of the
  page's own `fetch` responses by wrapping `window.fetch`: same-origin
  responses, or cross-origin ones between two loopback origins; headers
  and URL only, never bodies, never sends, never the panel's own Studio
  requests; it chains to whatever `fetch` it found, is installed only
  once Studio has answered and is restored on disconnect. The next
  turn's header of the same conversation re-pins its run unless the
  user has clicked a turn since, and another conversation is followed
  only from the request path that set the current one. It is on by
  default only with the page and the endpoint on loopback and no token
  or a dev token, opt-in elsewhere with `data-detect="headers"` (or
  `mount({detect})`), and `data-detect="off"` turns detection off.
  Under a panel token the page's `fetch` is never touched unless asked. It is fetch-only,
  because a page cannot read an `EventSource`'s headers, and WebSocket is
  never wrapped. The footer names the choice: `detect: headers | off |
  explicit | none`. `examples/studio-local`'s tag drops `data-public-id`, and
  its page's first `/run` scopes the panel to `pub_demo`. Panel:
  34.5 KiB gzip (was 32.0).

### Changed

- **`data-public-id` is deprecated in favour of `data-scope`** (plan
  C3.2). It is still read, as the scope with only its public id, at
  every rung where it was read before (`weft:public-id` too), and
  `data-scope` wins at the same rung. `mount({publicId})` keeps working
  beside the new `mount({scope})`.

### Changed — breaking

- **A token in a URL is refused**: the `?token=` query parameter is
  refused on every `/api` route, in every setup (401, never echoed,
  naming the grant); a token travels in
  `Authorization: Bearer` only. Migration: an `EventSource` (or any
  client that put the token in the URL) requests a grant with `POST
  /api/live-grant` and opens `/api/live?…&sig=`. The `#token=`
  fragment of `weft open` / `weft studio --open` links is unaffected:
  it never reaches the server.
- **Both web clients request a live grant** (plan C5.2): the Studio UI
  and the devtools panel open every live stream with `POST
  /api/live-grant` (bearer in the header) and a `sig`, and put no
  token in any URL; the UI adopts a link's token from its `#token=`
  fragment only (a `?token=` is stripped, not kept) and drops the
  unused `exportUrl`.

## 0.11.0 — 2026-10-08

Phase 2 of the devtools plan (start and find): the `weft` command,
`weft dev`, the port policy, discovery, the stable dev token, the
Agents page from runtime registrations, `weft doctor`, and the panel's
configuration ladder. Tags `core/v0.11.0` (the `weft.version` literal
only; core's API is unchanged since 0.10.1) and `v0.11.0` — a minor
bump, as `studio/cmd` is removed (Changed — breaking).

### Added

- **Agents from runtime registrations** (plan B4): Studio remembers
  each runtime's registration manifest by (service, manifest hash), in
  memory; `/api/manifest` serves it when no `weft.json` is configured
  (the file still wins) and lists every source in `sources[]` with
  `live`; two services registering one agent name in different versions
  both appear. `/api/meta` gains `manifest_sources`, `capabilities_off`
  (why an option left a capability off) and `manifest_check.source` /
  `differs`; the Agents page, playground and debugger empty states show
  the reason.
- **The `weft` command** (plan B1, `cmd/weft`): `go install
  github.com/weftgo/weft/cmd/weft@latest` yields a `weft` binary.
  `weft studio [--addr] [--db] [--token] [--manifest] [--open]
  [--no-playground]` is setup B (what `studio/cmd` served) with the
  playground on (`studio.Playground(true)`, inert until an app's
  runtime connects; `--no-playground` turns it off), the manifest from
  `--manifest` / `WEFT_MANIFEST` else the nearest `weft.json` from the
  working directory upward (one line says which, or that none was
  found), and `--open` (default on when stdout is a terminal) opening
  the UI with the token in the URL fragment. The Studio API over a
  terminal: `weft runs [--agent] [--since] [--failed] [--limit]
  [--json]` (`GET /api/runs`; `--limit` 50 by default, and a limit
  that hid runs says so on stderr), `weft open <run id> [--open]
  [--with-token]` (prints the bare `<url>/runs/<id>`; the token goes
  only to the browser, or to stdout on `--with-token`), `weft export
  <run id> [--format json|jsonl|otlp]` to stdout and `weft export <run
  id> --wefttest <dir> [--test name] [--force]` (the wefttest fixtures
  unzipped into `<dir>/<name>/`, where `wefttest.Replay(t, dir)` reads
  them; a non-empty target needs `--force`, which replaces its `*.json`
  fixtures); `weft doctor`; `weft version`. The API clients never
  follow a redirect. The API clients take
  `--url` (`WEFT_STUDIO_URL`, default `http://127.0.0.1:7331`) and
  `--token` (`WEFT_STUDIO_TOKEN`). Exit codes: 0, 1 a failure, 2 a
  usage error.
- **`weft dev`** (plan B1.2, FEATURES D7): `weft dev [studio's flags]
  [--no-watch] [--watch dir] [-- command…]` (default `go run .`)
  starts Studio in-process as `weft studio` does (same flags and port
  policy) and runs the app with `WEFT_ENV=dev` (kept when already set
  non-empty), `WEFT_STUDIO_URL`, `WEFT_STUDIO_TOKEN` and `WEFT_DB` (the
  Studio's SQLite file) set — plain variables the app can set itself.
  The app runs in its own process group and restarts on a `.go` save
  (fsnotify, 300 ms debounce; SIGTERM, 5 s, SIGKILL); a build failure
  waits for the next save; `--no-watch` exits with the app's code
  (127 when it cannot start); a directory arriving with `.go` files
  restarts too. Ctrl-C, SIGTERM and SIGHUP stop the app, then Studio
  (Linux adds Pdeathsig for `go run`; a SIGKILL of `weft dev` can
  still orphan the app's binary). Each start prints one line:
  `studio <url>[#token=…] · app pid <n> · runtime rt_… registered`
  (the token only when generated; "no runtime registered yet" after
  5 s, then a later line). Reuse follows the port policy: without a
  fixed token a running token-walled Studio is skipped for the next
  port (plan B3's stable token changes this). The
  discovery file is plan B3's. `examples/studio-local` runs under it:
  with `WEFT_STUDIO_URL` set it listens on 8080, keeps its local sink
  (`otel.Local("")`, explicit) and registers its runtime with that
  Studio.
- **`make studio-bin`** builds `./weft` from `./cmd/weft` (ignored by
  git).
- **Studio's port policy** (plan B2, `internal/listen`, wired into
  `weft studio`): `127.0.0.1:7331` is the one default. On a busy port
  the binary asks `GET /api/meta` there (bearer: `--token` /
  `WEFT_STUDIO_TOKEN` when set): a Studio on the same database file is
  reused — `studio already running at http://127.0.0.1:7331 (pid
  1234), reusing`, exit 0, no database or listener opened — and anything else (another
  program, a Studio on another database, one whose meta this token
  cannot read) moves Studio to the next free port in 7331–7340 with
  one line naming the skipped address and why; the banner prints the
  real address. All ten busy is exit 1 naming the range. `studio.New`
  is unchanged: an embedded Studio is the app's own listener.
- **`WEFT_STUDIO_ADDR`**, the `--addr` mirror. Either pins the
  address: busy is exit 1 with the address in the error — no probe, no
  fallback.
- **`/api/meta` `pid`**: the serving process's id, under `db.path`'s
  guard (loopback with no Token, or the server token; never a panel
  token).

- **`weft doctor [--url URL] [--token TOK]`** (plan B5): checks a
  running Studio and prints one line per check — reachable, token
  accepted, the database's path and size, the content it stores,
  connected runtimes (and, when none, what this shell's `WEFT_ENV` and
  `WEFT_STUDIO_URL` say about why), the panel bundle's version, and
  whether `weft.json` is stale against the latest runs. Every line
  reads a field of `GET /api/meta` (`internal/doctor.Lines` is the
  table); the flags mirror `WEFT_STUDIO_URL` / `WEFT_STUDIO_TOKEN`. An
  unreachable Studio is the first line within a 5 s timeout and exit 1;
  a redirect is reported, never followed.
- **`weft studio --manifest path`** (default `$WEFT_MANIFEST`; the flag
  wins): the app's `weft.json`, read once at start and served as
  `studio.Manifest` — `/api/manifest` and the doctor's weft.json check
  in setup B. An unreadable file is a start error.
- **`/api/meta` explains itself**: `content` (Studio's ingest policy,
  `"as_received"`, and the latest run's content mark — `full`,
  `stripped`, `none` or `unmarked` — with a note and the fix naming
  `otel.NoContent()` or `weft.Content(false)`), `pricing` (`false`
  until a pricing table exists), `retention` (`null`: none
  configured), `runtimes` (runtimes holding a command stream now),
  `panel_version` (the embedded `/panel.js` stamp) and
  `manifest_check` (`{agents, checked, stale}`: each manifest agent's
  hash against its latest run's `weft.manifest.hash`; `null` without a
  manifest or for a panel token), and `auth_required` (whether a
  `studio.Token` is configured). `content.error` and
  `manifest_check.error` (omitted when empty) report a read that
  failed or a manifest that does not parse — logged through the
  process's `slog` default too — instead of reading as an empty
  database.
- **obsdb/sqlite**: `(*DB).Path()` — the database file's absolute
  path (`""` for `:memory:`).
- **The discovery file** (plan B3, `internal/discovery`): `weft studio`
  and `weft dev` write `studio.json` — `{url, token, db, pid, started,
  version}`, mode 0600, the bound port in it — to `./.weft/` when it
  exists, else `$XDG_RUNTIME_DIR/weft/`, else `os.UserCacheDir()/weft/`,
  and remove it on a clean exit. `otel.Install`/`otel.Start` and
  `runtime.Install` read it when `WEFT_STUDIO_URL` is unset: an app
  with `defer otel.Install()()` exports to the running Studio (and its
  runtime dials it) with no configuration, one INFO line naming the
  Studio joined. The file is trusted only when its url is loopback, it
  is fresh (pid alive, under 24 h) and, on unix, it is 0600 and the
  reading user's; anything else is ignored at Debug and removed by the
  next writer. A second Studio's exit restores the first's file; the
  writer drops `.weft/.gitignore` (`*`) when there is none. An explicit
  `otel.Studio(...)` or `WEFT_STUDIO_URL` switches the read off;
  **`WEFT_DISCOVERY=off`** turns it off; `otel.NoEnv()` ignores it with
  the rest of the environment.
- **A stable dev token per database**: `<db>.token` beside the SQLite
  file (`.weft/weft.db.token`), 32 random bytes base64url, 0600,
  created on the first start and served by every later one, so the
  token survives a restart and a second bare start's probe reuses the
  running Studio (the probe sends it only to an address the user's
  discovery file names). **`--rotate-token`** (`weft studio`, `weft dev`;
  an action, no environment mirror) writes a new one.
- **`GET <base>/panel-config.json`** (studio): `{endpoint, version,
  capabilities}` for the devtools panel, answered to a loopback (or
  `AllowOrigins`) Host and a same-origin, `AllowOrigins` or loopback
  Origin only (loopback by the Host's rule — `localhost`,
  `*.localhost`, 127.0.0.0/8, `[::1]` — even with `AllowOrigins` set) —
  a 404 otherwise; `/api/meta` lists the new `panel-config` capability.
- **The devtools panel's configuration ladder** (plan C2): each field
  (`endpoint`, `public-id`, `token`, `position`, `open`, `auto`)
  resolves on its own through `mount(opts)` → the `<weft-devtools>`
  element's attributes → `<meta name="weft:endpoint|public-id|token|
  position|open|auto">` → the panel's `<script>` tag (the running
  classic script, else the first with `data-weft`, any `src`) →
  `panel-config.json` beside the script (endpoint only, same origin
  only, never a token; asked only when nothing above named an endpoint)
  → the script's own directory. A `<weft-devtools>` element, a
  `mount(opts)` or `data-auto="false"` with no Studio answering shows
  one line, `Studio not reachable at <endpoint> · retry`; the dock the
  script mounted by itself still removes itself silently. The rung
  table is in `studio/README.md`.

### Dependencies

- `github.com/fsnotify/fsnotify` v1.9.0 (framework module only, for
  `weft dev`'s watcher; `core` is untouched).

### Changed

- `runtime/examples/local` serves on `127.0.0.1:7331` (was 7391, for
  no documented reason): one default port everywhere.
- **The dev token is stable per database and no longer printed by
  default**: `weft studio`'s banner and `weft dev`'s one line print the
  bare URL and name the token file (the stable token is the panel
  tokens' signing key, like a fixed one); `--open` still hands the
  browser the link with the token, `weft open --with-token` prints it,
  and `weft dev`'s app still gets `WEFT_STUDIO_TOKEN`. Only a database
  with no file (`:memory:`, ClickHouse) gets a per-process token,
  printed as before. `--token` / `WEFT_STUDIO_TOKEN` override as
  before and never touch the file.

### Changed — breaking

- **The panel no longer finds its script tag by file name** (the
  `panel(-vX)?.js` pattern is gone). A tag carrying none of the panel's
  `data-*` attributes — the bare `<script type="module"
  src="/studio/panel.js">` that relied on `window.__WEFT__`, or a
  renamed or proxied bundle — is no longer found: its endpoint falls to
  the page's own directory, Studio does not answer there, and the auto
  dock removes itself silently. Migration: add `data-weft` to the
  script tag (`<script type="module" src="/studio/panel.js"
  data-weft>`), or configure a higher rung (a `weft:*` meta tag, the
  element, `mount(opts)`). Tags with `data-public-id`, `data-endpoint`
  or another panel attribute keep working. A `<weft-devtools>` element
  in your markup with no Studio answering now shows the not-reachable
  line instead of an empty element.
- **`studio/cmd` is removed**: the setup-B binary is `weft studio`
  (`go install github.com/weftgo/weft/cmd/weft@latest`), every flag
  kept. `studio --version` is `weft version`; `studio doctor` is
  `weft doctor`. Scripts that ran `go run ./studio/cmd` or
  `go install github.com/weftgo/weft/studio/cmd@…` run `weft studio`.
- **`/api/meta`'s `db` is an object**: `{kind, path, size}` instead of
  the kind string. Read `db.kind` where you read `db`. `path` and
  `size` (bytes, the WAL sidecar included) are served only to a
  loopback request with no `studio.Token` and to the server token —
  omitted for panel tokens and `AllowOrigins` hosts, and for a
  database with no file.

## 0.10.1 — 2026-10-08

### Fixed

- **studio**: `make studio-panel-asset` (the devtools panel as a release
  asset) read the `studio.Version` literal that 0.10.0 removed and
  failed; it reads the one version from `version/version.go` through
  the same helper the panel build stamps from. Tags `core/v0.10.1` (the
  `weft.version` literal only) and `v0.10.1`.

## 0.10.0 — 2026-10-08

The request record (ADR 0028): every model call's system prompt, tool
catalog, parameters and attempts recorded beside the transcript, read
back by Studio and the devtools panel with a closed table of honesty
badges; app logs, the step route, the export in four formats; one
version from the module. Tags `core/v0.10.0` (compatible additions
only) and `v0.10.0` (breaking in the pre-freeze layers listed below).

### Changed — breaking

- **One version, the module's.** The new `github.com/weftgo/weft/version`
  package (standard library only) holds it: `version.Version` is the
  release tag, `version.Runtime()` reads the framework module's version
  from the build info (the main module or the `github.com/weftgo/weft`
  dependency, honouring `replace`) and falls back to `Version` under
  `(devel)`. `studio.Version` is now `version.Version` — `v0.4.1` →
  `v0.9.0` — so `/api/meta`'s `studio_version` and the devtools panel's
  embedded version (stamped from `version/version.go` at build) move
  with the module. A panel built for `v0.4.1` refuses a Studio that
  reports `v0.9.0` as newer.
- **`/api/meta`'s `weft_version` is the framework module's build-info
  version** (`version.Runtime()`), no longer the `core` dependency's;
  inside the workspace it reads the tag instead of `(devel)`.
- **`obsdb.DB` gains `TranscriptBatches`** (ADR 0028 §8): a run's
  messages records with what each stored — index, step
  (`weft.step.index`, -1 when absent), input flag. A third-party `DB`
  reads pos, step, body and the `weft.messages.input` attribute per
  `messages` record, returns them through `obsdb.DedupBatches`, and
  implements `Transcript` as `obsdb.TranscriptBodies(batches)`. A
  backend with no attribute column infers the input flag on index 0 and
  sets `TranscriptBatch.InputDerived`.
- **`/api/runs/{id}/transcript` rows carry what the record stored**: the
  stored `index`, the stored `step` (or `-1` with `"badge":
  "not_recorded"`) and the stored `input` (`"badge": "derived"` where
  the backend inferred it) — no longer derived from assistant-message
  order. Playground `transcript_edits` / `from_step` validation and the
  web client read the same stored step; only a batch without one is
  placed by inference, marked derived.
- **`obsdb.DB` gains `Requests`, `Prompt`, `Tools` and `Catalogs`**
  (ADR 0028 §10, the request record's read side). A third-party `DB`
  reads its `request`, `prompt` and `tools` records (stored under
  `(run, kind, index)`) as `obsdb.StoredRecord`s through
  `obsdb.RequestRecordOf`, `obsdb.PromptRecordOf` and
  `obsdb.ToolsRecordOf`, returns one tools record per hash (the lowest
  index) from `Catalogs`, and answers a missing hash with
  `obsdb.ExplainMissing`. `obsdb.RunRow` gains `InstructionsHash`,
  `CatalogHash` and `RequestCount`, which a backend fills with the
  larger of `run_start`'s and the `invoke_agent` span's
  `weft.instructions.hash`, request index 0's `weft.catalog.hash` and
  max `weft.request.index` + 1.
- **`obsdb/clickhouse`'s unreleased migration 0004 gained
  `weft_records.Input`, `Content`, `TruncatedBytes`, `SystemHash` and
  `CatalogHash`**: a database
  that applied 0004's earlier text (only a development build wrote one)
  lacks them and must be recreated. ClickHouse now reads the transcript
  input flag as stored; only rows written before the column read
  `TranscriptBatch.InputDerived`.
- **`obsdb.DB` gains `Compactions`** (ADR 0028 §8, plan A9): a run's
  compactions as `obsdb.Compaction` — each run-scope view in index
  order with its step, half-open range, hash and body, then thread's
  session markers in emission order. A third-party `DB` builds each through
  `obsdb.CompactionOf` (from a `messages` record whose
  `weft.messages.reason` is set, or a record of kind `compaction`) and
  orders them with `obsdb.SortCompactions`. Its `Transcript` and
  `TranscriptBatches` must skip every `messages` record whose
  `weft.messages.reason` is set, and its messages count must not count
  them (`obsdb.Weft.Reason` carries the attribute).
- **`obsdb.DB` gains `OtherLogs`** (plan A7): a run's app log records —
  the non-weft records (no `weft.run.id`) the writers keep beside
  weft's, attributed to the run through the span they were emitted
  under (its own spans and the non-weft spans below them, never another
  run's) — as an `obsdb.LogPage` of `obsdb.OtherLog`s, paged by
  `obsdb.LogQuery` (`From` inclusive, `Limit` 0 = 100 max 1000,
  `MinSeverity` filtering without renumbering), with `Partial` (the run
  is running: lines under in-flight spans appear when those spans end,
  and indexes may shift), `Gap` (lines naming a span never stored) and
  `Truncated` (more than `obsdb.MaxLogCandidates` lines in the run's
  traces, counted before attribution). A third-party `DB` implements it as
  `obsdb.ReadOtherLogs(ctx, db, runID, q, candidates)`, where
  `candidates` returns the first `limit` non-weft records of the run's
  traces within a time window, in time order. `obsdb.HoleError.Kind` gains `"logs"`: a finished run
  with no span has nothing to attribute through and answers
  `HoleNotRecorded`.

### Changed

- **The system prompt and the tool catalog now reach content-on
  destinations** (`otel.Local`, `otel.Studio`) under the content policy:
  `Redact`-able, capped with `weft.content.truncated_bytes`, dropped by
  content-off destinations. ADR 0028 reverses the old stance that no
  record carries the instructions text. An application whose prompt must
  not leave the process redacts it (`core.ContentPrompt`) or turns
  content off for that destination.

### Added

- **The compaction marker on the run page and in the panel (plan
  A9.2, ADR 0028 §8).** `GET /api/runs/{id}` gains `compactions`: each
  compaction the run's records name, counts and hash only, never a
  message body — a run-scope view `{scope: "run", index, step,
  from_seq, to_seq, hash, replaced, entries}` and thread's session
  marker `{scope: "session", hash, replaced, entries, tokens_before?,
  tokens_after?, reason}`; `[]` for a run that never compacted or was
  written before A9 (the record is optional: no badge). A read-scoped
  panel token reads it. The run page's step card whose request saw a
  view draws "2 messages rewritten into 1 by PrepareStep" ("1 message
  inserted by PrepareStep" when nothing was replaced) with the
  `compacted` badge; a session marker sits at the top of the run it is
  filed under — "12 messages compacted into 2 · 8.1k → 1.2k tokens".
  "show original" (collapsed, no fetch) expands a view's replaced range
  `[from_seq, to_seq)` from the transcript route's growth records the
  page already holds (seq = position in their concatenation); a range
  that cannot be placed is the `gap` badge. The replacement shows its
  count — and, once the step route is loaded, the request's message
  count — and says where its body is (the export's
  `compactions[].messages`). The devtools panel draws the same marker:
  session markers at the top of the turn, views on their step line.

- **A run's app logs and its delta count in Studio (plan A7).**
  `GET /api/runs/{id}/logs?from=&limit=&severity=`, under a new `logs`
  capability in `/api/meta`, pages the app's own log lines (an `slog`
  bridge or the OTel Logs API on the pipeline's `LoggerProvider`) that
  were emitted under the run's spans — a tool handler's lines are the
  run's — in time order: `{logs: [{index, time, severity,
  severity_number, body, attrs, span_id?}], next_from?}`; `severity`
  keeps a level and above (`trace`…`fatal`, or 1–24). `holes` lists
  every hole of the page (`badge`/`reason`/`fix` repeat the first):
  `not_recorded` for a run recorded without a tracer, `truncated` when
  the run's traces hold more than 10 000 log lines in its window (the
  cap applies before attribution: later lines of this run may be
  missing), `gap` for lines in the run's trace naming a span that is
  not stored (they may be another run's). A running run's page carries
  `partial: true` and a `partial_reason` (lines under in-flight spans
  appear once those spans end; indexes may shift) — not a hole.
  App logs may carry anything the app logged, prompts included, so a
  read-scoped panel token is refused them (403, `badge: "hidden"`); a
  playground-scoped token, the server token and loopback read them. The
  run row (`runs`, `runs/{id}`, sessions, the export) gains
  `delta_count`, the streamed deltas counted and never stored. The web
  client gains `fetchLogs` and the types (no UI yet).

- **Subagents on the page (plan A10).** `GET /api/runs` takes `all=1`
  (every run, subagent children included — the same as `parent=*`; a
  `parent=<run id>` beside it wins; a malformed value is a 400) beside
  the existing `parent=` filter, whose absence keeps the list top-level
  only. On the run page a step whose tool call started a child run
  shows it as a nested row — agent, status, usage, its holes, a link to
  its own page (from the run document's children, or the step route's
  `children[]` when cached) — and opening it folds the child's steps
  with the child's own request record, read by the child's id
  (`/api/runs/<child id>/requests`, under the `requests` capability:
  the child's prompt, never the parent's; a read-scoped token sees
  `hidden`). A call joins its child by the child's id, which names the
  step (`<parent>/<step>/<call id>` — call ids may repeat across steps),
  in the story and the trace view's call detail alike; a child id of
  another form falls back to the first unlinked call with its
  `parent_call_id`. The run document's `children[]` rows carry each
  child's own `holes`; a child's usage shows once it ended ("usage at
  finish" while it runs, "—" beside the interrupted badge). The runs table is "top-level only" by default with a toggle
  (`?subagents=all`) and a `?parent=` filter chip; a child row links its
  parent. The devtools panel's subagent badge opens the child inline,
  one level: its row (agent, status, usage), its steps with its request
  line, and an "open in Studio" hand-off carrying the child's id; a
  grandchild is its badge and the hand-off only. A child opened while
  it ran is read again when the parent's reload finds its status moved.

- **Studio's run export (plan A7, A9's byte-faithful fixtures).**
  `GET /api/runs/{id}/export?format=json|jsonl|otlp|wefttest`, under a
  new `export` capability in `/api/meta`, downloads the whole run
  (`Content-Disposition: attachment; filename="<run id>.<ext>"`, the
  quoted name plain ASCII — a child's slashes, quotes, backslashes,
  control and non-ASCII characters spelled `_` — plus RFC 6266's
  `filename*` for a non-ASCII id; HEAD answers the headers alone). `json` is one `weft.run.export/1`
  document — `run` (as `/api/runs/{id}`), `events` (with `gaps`),
  `transcript` (the batches as `/transcript` serves them),
  `compactions`, `requests` (the rows with `refs=1`, plus `prompts` and
  `catalogs` keyed by hash: the record, or `{hash, badge}`), `spans`
  and `holes`; every absent block carries its badge, reason and fix
  (`not_recorded` for a run written before ADR 0028, `stripped` for a
  content-off run, `gap` for records a destination dropped). `jsonl` is
  the same records one per line, `{"record": <kind>, …}`, in a stable
  order (run, badges, events by pos, messages by index, compactions,
  requests by index, prompts, catalogs, spans). `otlp` is
  `{"logs": ExportLogsServiceRequest, "traces": ExportTraceServiceRequest}`
  in OTLP/JSON (ids as hex): the records are rebuilt from what obsdb
  reads, with the run's identity and metadata on each, the events'
  `weft.content` marks, and one heartbeat at its last-seen, so
  `POST /v1/logs` and `/v1/traces` re-ingest the same run, its
  `stripped`, `truncated` and `derived` holes included (an inferred
  input flag is left unstamped, a derived prompt or tools record goes
  out with its hash and an empty body — the malformed original is not
  kept by obsdb). The log records' resource carries only
  `service.name`: obsdb keeps no record's own resource, and the spans
  keep theirs verbatim. `wefttest` is a zip of replay fixtures for
  `wefttest.Replay`. A read-scoped panel token gets `json`/`jsonl` with
  the request block `{badge: "hidden"}` (the transcript still badged
  `stripped` for a content-off run); `otlp` and `wefttest` are 403
  with the hidden badge. An unknown format is 400, an unknown run 404,
  a run with nothing to fixture 409 with its badge (`stripped`, `gap`
  — no transcript, or a step whose request record was dropped — or
  `derived`).
  `web/src/lib/api.ts` gains `exportUrl(runId, format)`.
- **Studio's replay fixtures key on the stored step and the request
  record.** `POST /api/playground/fixtures` and the wefttest export
  share one builder: each fixture is the step its records were stamped
  with (`weft.step.index`), not the Nth assistant message; its key takes
  the tool names, thinking, tool choice and sequential flag from the
  step's answering request record (the caller's `tools` only for a run
  written before the record); a step whose request carried a run-scope
  compaction view keys on the compacted messages the model saw and
  carries a `compacted_at` header (index, step, range, hash); the
  finish event carries the step's recorded reason, raw reason and
  usage; tool-call signatures are kept; and the system prompt is
  filled for the reviewer. One fixture per step, from its answering
  attempt: failed retry attempts get no fixture, so a replay answers
  the step's first call under any middleware. `POST
  /api/playground/fixtures` now refuses a read-scoped panel token (403,
  badge `hidden`: fixtures are request-derived) and answers "nothing to
  fixture" with 409 and the badge, like the export (it was 400).
- **Every hole is a badge from one closed table, on both surfaces
  (plan A3, ADR 0028 §11).** `obsdb.HoleNote(h)` holds each of the ten
  holes' one-line reason and, where one exists, its fix — Studio's
  routes read it (no second copy of the words), and its golden,
  `studio/testdata/holes.golden.json`, is what the web's
  `lib/honesty.ts` (the run page and the devtools panel's one table) is
  checked against, key by key. The events route's rows and the live
  record frames carry `attrs`, limited to the `weft.content.*` keys
  (`weft.content` when not `full` — `stripped`, or the core's own
  `none` — and `weft.content.truncated_bytes`), absent when the chain
  left the content as emitted; `obsdb.PosEvent` gains `Content` and
  `TruncatedBytes` (both backends read them; a ClickHouse row from
  before migration 0004 has neither). `GET /api/runs/{id}` gains
  `holes: [{hole, reason, fix?}]` — the run's own: `not_recorded`
  (written before the request record), `interrupted`, `derived` (no
  stored event: a row built from spans), `stripped` (a content-off
  run) and `gap` (event positions missing, once the run is over). The
  fold turns `attrs` into badges on the event, its call, its step and
  the run ("shortened by the recorder: 12.3 KiB cut", "content not
  captured by this app" with its fix); the run header, every step card,
  the raw view's event rows and the panel's turn header and step lines
  draw them; transcript words whose step has no events render as a
  `gap` row under the last step, and the replay playhead goes through
  the transcript overlay (only steps finished at the playhead take
  their final words), so holes survive scrubbing. The step route's
  holes read its events' attrs too (stripped, truncated with the bytes
  cut). The request routes' reasons are now the table's words.
  `redacted` stays reserved: weft's pipeline does not mark a redaction.
  `studio/testdata/v0.9.0.db` is a database weft v0.9.0 wrote; Studio's
  tests open it (migrated to the current schema) and every pane of its
  run says why it is empty.
- **Studio's step route (plan A7, with A4's attempts and A10's
  children).** `GET /api/runs/{id}/steps/{n}` (`n` the step ordinal)
  answers one step assembled server-side from `obsdb`: `status` (`ok`,
  `error`, `parked`, `running`), `started`/`finished`, `latency_ms`/
  `ttft_ms`, `model` (`requested`, and `answered` — the model that
  answered under `mw.Retry`/`mw.Fallback`), `request` (the requests
  route's row for attempt 1, prompt and catalog inline), `attempts`
  (each request record joined to its `attempt` span on the attempt
  number: model, provider, `outcome`, `error_type`, `retry_after_ms`,
  times, span id, request index; attempt 1 falls back to the `chat`
  span), `messages_in` (the request's `messages_ref` as a count),
  `events` (as the events route serves them), `tool_calls` (args,
  result with bytes and truncation, the `execute_tool` span, the child
  run, pending), `children` (the child runs the step's calls started:
  id, call id, agent, status, usage), `usage`, `compaction` (the
  run-scope view the step's request carried) and `holes` — every hole
  that applies, deduplicated, from ADR 0028 §11's table with a reason
  and fix. A block missing for a reason carries its badge
  (`attempts_badge: not_recorded` for a run without attempt spans or
  A4 timing, `request: {badge: "not_recorded" | "gap"}`). Scoped like
  the events route; a read-scoped panel token reads the whole step with
  `request: {badge: "hidden", reason, fix}`. A step past the run's
  last, or a running run's next, is 404. `/api/meta` lists the new
  `steps` capability; the web client gains `fetchStep` and the
  `StepDoc` types (no UI change yet). `children[].cost` is omitted
  until A5 adds costs (absent, not zero); the answering model and the
  attempts' outcomes are read from spans, so a run recorded without a
  tracer badges its attempts `not_recorded` with the fix to install
  one — `obsdb.HoleNoteFor(h, cause)` words it (`CauseNoSpans`), the
  holes golden lists such causes under their hole. Each call is badged
  `stripped` from its own events' `weft.content` too, so a content-off
  run written before the request record says so per call. With neither
  content nor a tracer, a max_tokens step's calls are not listed; the
  max_tokens hole says the step made some.
- **Compaction in the record (ADR 0028 §8, plan A9.1).** When a
  `PrepareStep` sends a request whose messages are not the run's
  transcript, the core emits one `messages` record with
  `weft.messages.reason = compacted` right before that request's
  record: `weft.messages.from_seq`/`to_seq` (the half-open range of the
  transcript it replaced, by longest common prefix and suffix over wire
  bytes), `weft.compaction.scope = run`, `weft.compaction.hash`, the
  replacement messages as its body. The request's `messages_ref` names
  it; the next request names the growth records again. Nothing is
  emitted when nothing changed or with capture off, and what the model
  receives is unchanged. `weft/thread` reports every session compaction
  (threshold, `Compact`, `ApplyCompaction`, a trim, the overflow
  re-run) as one record of kind `compaction`, emitted when it lands
  under the last run that produced the compacted context — scope
  `session`, the compaction entry's hash, the messages replaced →
  summary entries and tokens before → after; no messages. Studio's live
  lane never forwards a view, and its catch-up places messages records
  by their stored index. Both `obsdb` backends keep the plain
  transcript the growth records alone and read both through
  `DB.Compactions`.
- `(*core.Agent).LoggerProvider()` (and `weft.Agent`'s): the OTel
  logger provider the agent's runs emit records through — the option's,
  or the global one resolved at `New` — for satellites whose records
  must land beside the run's.
- `studio --version` prints `version.Runtime()` and exits.
- **Studio's request record routes (ADR 0028 §10, plan A1).** `GET
  /api/runs/{id}/requests?step=&from=&limit=&refs=1`: one row per
  model-call attempt (`index`, `step`, `attempt`, `time`, `system_hash`,
  `catalog_hash`, `content` — `""`, `stripped` or `derived` —,
  `truncated_bytes`, the parsed `body`), each row's `prompt` and `tools`
  resolved inline by hash unless `refs=1` (`{hash, badge}` when the
  record is missing), `next_from` while pages are full. `GET
  /api/runs/{id}/tools`: `{catalogs: [{hash, tools, content,
  truncated_bytes}]}` in index order. A run written before the record
  answers both with `badge: "not_recorded"` (and a reason and fix)
  beside the empty list; a content-off run's tools answer `badge:
  "stripped"`. Both are refused to a read-scoped panel token (403 with
  `badge: "hidden"`), as `/api/manifest` is.
- `GET /api/runs/{id}` (and every run row) gains `instructions_hash`,
  `catalog_hash`, `request_count` and, for a run written before ADR
  0028, `requests_badge: "not_recorded"`.
- `/api/meta` lists the `requests` capability: the UIs gate the Request
  pane on it.
- `obsdb.TranscriptBatch`, `obsdb.TranscriptBodies`, `obsdb.DedupBatches`
  (ADR 0028 §8).
- `obsdb.Hole` and its ten constants (`obsdb.Holes()`, ADR 0028 §11's
  closed badge table); `obsdb.RequestQuery` (the zero value reads every
  step from index 0; `Step *int`, `From`, `Limit`, `PageLimit()`),
  `RequestRecord`, `RequestBody` (and its parts), `PromptRecord`,
  `ToolsRecord`, `ToolEntry`, `StoredRecord`, `HoleError`,
  `RunRow.RequestsHole` (ADR 0028 §10's reading table). A request,
  prompt or tools record whose body does not parse reads
  `obsdb.HoleDerived`, its hashes from the record's attributes.
- **The request record (ADR 0028).** Three OTel log record kinds beside
  `event`, `delta` and `messages`: `request` (one per model-call attempt:
  step, attempt, system and catalog hashes, messages reference, tool
  names, tool choice, thinking, params, model), `prompt` (the composed
  system text, once per distinct hash per run) and `tools` (the offered
  catalog with its policy chips, once per distinct hash per run).
- `RunStart.InstructionsHash` (`instructions_hash` on the wire, an
  additive field): sha256 of the run's raw configured instructions,
  always set by the loop; mirrored as `weft.instructions.hash` on the
  `run_start` record and the `invoke_agent` span.
- `ContentPrompt` and `ContentStop`, the `ContentKind`s `otel`'s
  `Redact` receives for a prompt's text and a request's stop sequences.
- `weft.Origin(name)`, a tool option naming the tools record's `source`
  (`local` by default; `Subagent` sets `subagent`, `mcp.Tools` sets
  `mcp`).
- **Model-call timing and the answering model (plan A4, ADR 0016's A4
  note).** `StepFinish.LatencyMS` and `StepFinish.TTFTMS`
  (`latency_ms`, `ttft_ms` on the wire, additive, omitted when 0): the
  step's model call timed by the loop as it consumes the stream, whole
  milliseconds rounded up; `ttft_ms` is the first `TextDelta` or
  `ToolArgsDelta` and absent when neither arrived. The `chat` span gains
  `weft.stream` (`true`), `weft.ttft_ms` (when a delta arrived) and
  `gen_ai.response.model`; the `step_finish` record gains
  `weft.latency_ms`, `weft.ttft_ms` (when measured) and
  `gen_ai.response.model`; a successful `attempt` span gains
  `gen_ai.response.model`. The answering model is the last attempt the
  chain reported as a success (`mw.Retry`, `mw.Fallback`, a reporting
  adapter), else the model the call asked for.
  Studio's step card and the panel's step line show it (A4.2): "attempt
  4 of 4 · fallback to glm-b" and "1.2 s · first token 180 ms" from the
  folded `step_finish` and the request rows (no fetch while collapsed),
  the attempt list (model, outcome, `retry_after`, times) from the step
  route on expand under the `steps` capability, the same on the trace
  view's `chat` span, and `not_recorded` on a run from before A4.
- **The reporting hook (plan A8, ADR 0016).** `weft.ReportFromContext(ctx)`
  returns the model call's `Reporter`: `.Attempt(weft.AttemptInfo{…})`
  records one provider request as an `attempt` span under `chat`
  (provider, model, `weft.attempt.index`, `weft.attempt.retry_after_ms`,
  `error.type` or Ok) and, from the second attempt on, its own
  `request` record; `.Raw(weft.RawPair{…})` is accepted and dropped
  (sizes on a Debug line). The reporter numbers attempts itself; a
  layer that reports declares `ReportsAttempts() bool` and an outer
  reporting layer stays silent. `mw.Retry` and `mw.Fallback` report
  every attempt. Outside a run's model call the reporter is a no-op.
- **`obsdb` names the phase-1 readers added above**: `CompactionRun`,
  `CompactionSession`, `RecordCompaction`, `ReasonCompacted`;
  `HoleCause`, `HoleCauses`, `CauseDefault`; `LogSkew`, `ParseSeverity`,
  `SeverityText`; `RunDetail.{InstructionsHash,CatalogHash,RequestCount}`
  beside `RunRow`'s; the `RequestBody` parts `RequestMessagesRef`,
  `RequestModel`, `RequestParams`, `RequestThinking`, `RequestToolChoice`,
  `RequestTools`.

### Fixed

Found by the phase-1 review of the request record (2026-10-08); none
changes what the model sees.

- **core**: a reported attempt that raced the model call's end could
  still add a `request` record; the record is now emitted under the
  lock `end()` takes, so a late report adds none (ADR 0028 §7). A
  cancelled resume whose transcript ended at the assistant message
  (every call parked) records its rebuilt tool message like a resume
  with a held tail does, so the records still concatenate to
  `RunError.Result.Messages`. A further attempt that reports a model
  is recorded under the provider it reported, even an empty one — a
  fallback to another vendor no longer carries the primary's provider.
  The agent's instructions hash is computed once at `New` (two
  allocations fewer per run). The Debug line for a contained recorder
  panic names the panic's type, never its value.
- **otel**: `capTools` encodes the catalog once instead of once per
  dropped entry (a 400-tool catalog took 86 ms on the run goroutine).
  A `Redact` panic on `params.stop` drops the stop sequences only; the
  request keeps `messages_ref.index` on a destination that received
  the messages records.
- **obsdb**: a run row with request records but no instructions hash
  (the `run_start` batch lost or not yet landed) reads recorded, and a
  prompt or catalog its requests name but the store lacks is a `gap` —
  not `not_recorded` with "upgrade weft" (ADR 0028 §10's table gains
  the row). `weft.attempt.retry_after_ms` and the ten
  `weft.override.*` fingerprint fields join the contract keys on both
  backends (and 0004's tuples), so a non-string value no longer lands
  in ClickHouse's run meta and not SQLite's. A `messages` record
  without its index — growth or view — and an index attribute present
  but empty read position -1 on both backends and stay out of the
  transcript and the messages count (before, SQLite stored a growth
  record at 0 where the input record dropped it and ClickHouse read it
  first). A non-string `weft.messages.reason` reads as "not growth" on
  SQLite as it does on ClickHouse. Two session markers with distinct
  non-hex hashes keep distinct positions on SQLite (a 60-bit FNV
  fallback). App-log lines equal in time, span, severity and body sort
  by event name and attributes on both backends, so paging by
  `next_from` neither skips nor repeats a line.
- **thread**: a compaction in a forked session no longer files its
  marker under the origin session's run — it is held for the fork's
  first run. A mid-run overflow's compaction keeps the earlier turn's
  span context and metadata on its marker (the Session remembers the
  last four runs it saw). After a reopen the marker carries
  `weft.turn`, `weft.public_id` and the lineage pairs, not the session
  id alone. A held marker that loses a race with `Close` is dropped
  with the "compaction markers dropped at close" Debug line.
- **version**: `version.Runtime()` maps a VCS-stamped pseudo-version
  and a `+dirty` suffix to `Version`, as it does `(devel)`, so a local
  `go build` reports the same version its runs stamp.
- **studio (server)**: a read-scoped token no longer sees tool names
  through the `invoke_agent` span's `weft.override.tools`,
  `weft.override.park_on`, `weft.override.park_all_except` (and a named
  `tool_choice`) on `runs/{id}/spans`, `traces/{id}` or the json/jsonl
  export, nor a compaction view's messages in the export (`null` with
  the `hidden` badge); `/api/manifest`'s 403 carries the hidden badge
  like every other prompt-bearing refusal; the fixtures route checks
  scope before it decodes the body. The step route joins a child to
  the step its id names (a call id repeated across steps no longer
  borrows another step's child); a running step is no longer badged
  `not_recorded`/`gap` for a chat span that has not ended; a running
  run's step without a stored `step_start` reads running, not ok with
  a gap; `weft.model.tool_calls` is clamped before it sizes a response;
  a lone unnumbered request record merges into attempt 1 instead of
  counting the call twice; a `messages_ref` naming a dropped view reads
  `gap` on `messages_in`. The run document and the export share one
  rule for lost events (`gap` when requests or steps exist with no
  spans, `derived` when spans exist); one unreadable child no longer
  fails the parent's document; export children rows carry `holes`;
  the export's top-level `holes` also lists `truncated`, non-final
  `max_tokens` and per-event `stripped`. Every hole's fix comes from
  `obsdb.HoleNote`/`HoleNoteFor` (two new causes: `result_cap`,
  `log_cap`). The auth matrix gains `runs?all=1`, a child's otlp/jsonl
  export, `steps/{bad}` (403 wins), HEAD on every export format and
  the span-attribute check.
- **studio (web, panel)**: `linkView` is idempotent (a re-render no
  longer links one child to a second call with the same id); call
  rows and trace span keys are step-qualified (`c:<step>:<call>`,
  `c:resume:<call>`), so a repeated call id selects the right call and
  React sees no duplicate keys in a resumed step 0 — old `?sel=c:<id>`
  links no longer resolve; the step document is re-read while a step
  runs and once the run ends, and its running-time holes are never
  shown as final; the requests query re-reads from the first missing
  index on the terminal refetch (an out-of-order row no longer leaves a
  false `gap`); `applyTranscript` clones the steps it overlays (a late
  delta no longer appends to transcript text); the `derived` attempt
  badge says what the server means and lists an unnumbered record by
  its request index; the header and the panel share one status-hole
  rule (`statusHoles`); "show original" reports a transcript read
  error instead of loading forever; `findCall` closes the most recent
  open call; no bare "1 attempt". `logs.test.ts` reads the Go goldens
  and a drift guard checks every step and logs golden against the TS
  types.

## 0.9.0 — 2026-10-07

One module is the framework; `core` is the loop alone (ADR 0027).
`MIGRATION-0.9.md` is the migration guide, written to be applied by a
person or a coding agent.

### Changed — breaking

- **`github.com/weftgo/weft` is now the whole framework, one module at
  one version.** The adapters (`openai`, `anthropic`, `google`, `mcp`),
  `thread` (and `thread/sqlite`), `otel`, `obsdb` (and
  `obsdb/clickhouse`), `studio` (and `studio/cmd`) and `runtime` are
  packages of this module, no longer modules of their own. Their import
  paths are unchanged; their per-module tags stop at the last ones cut
  (thread/v0.9.1, thread/sqlite/v0.3.1, otel/v0.2.1, obsdb/v0.2.0,
  studio/v0.4.1, studio/cmd/v0.2.0, runtime/v0.2.0, the adapters'),
  which keep requiring weft v0.8.0. A go.mod that requires one of the
  retired module paths beside `github.com/weftgo/weft v0.9.0` fails
  with an ambiguous import: drop the line (`go mod edit -droprequire`)
  and `go mod tidy`.
- **The loop alone is `github.com/weftgo/weft/core`**, a separate
  module whose only dependency is the OpenTelemetry API, tagged
  `core/v0.9.0`. It holds what the root package held: `core.New`,
  `core.Tool`, the contracts, plus `core/wefttest` (with
  `conformance`) and `core/mw`.
- **The root package, `mw`, `wefttest` and `wefttest/conformance` are
  generated facades over their `core` counterparts**: every exported
  name is a type alias, a bound constant or variable, or a one-line
  wrapper with the original signature and doc. Code that used
  `weft.New`, `wefttest.Script`, `mw.Retry` compiles unchanged;
  `weft.Agent` and `core.Agent` are one type. `go generate ./...`
  regenerates them (`internal/cmd/genfacade`) and
  `TestFacadesAreComplete` fails when a facade and its source
  disagree.
- The layers' signatures name `core.X` where they named `weft.X`; same
  types, no call-site change.
- `mw.IsContextOverflow(err)` is exported: the overflow marker table
  the adapters' error mapping and `mw.Retry` share now lives in `mw`
  (the adapters' internal helper delegates to it).

### Fixed

- A tool defined through the facade records the caller's file and line
  as its definition site, not the facade's: `Tool`, `Output` and
  `Subagent` skip the framework's wrapper frame. Committed manifest
  golden files do not change.

### Release process

- Two tags per release, in order: `core/vX.Y.Z`, then the root tidied
  against it and tagged `vX.Y.Z`. `go.work` joins the two modules and
  the examples. The apidiff gate enforces `core` against its last
  `core/v*` tag and reports the root.

## thread/sqlite 0.3.1 — 2026-10-07

### Fixed

- A write whose context ends while its transaction is open now fails
  with an error matching the context's (`context.Canceled`,
  `context.DeadlineExceeded`), wrapping `sql.ErrTxDone` beside it.
  database/sql rolls such a transaction back on its own, and the bare
  `sql.ErrTxDone` read as a storage failure: a `thread/pool` child
  canceled while its first prompt was being written settled `failed`
  instead of `canceled` (the crash matrix's `pool_canceled` point, seen
  once in CI). The write still does not land; only the error changed.
- Requires weft v0.8.0 and thread v0.9.1.

## otel 0.2.1 — 2026-10-07

### Fixed

- The OTLP golden fixtures live in the module's own `testdata/otlp`, so
  `go test` runs from the module cache (it read `../obsdb/testdata`,
  which a module zip does not carry). Test-only; no code change.

## studio 0.4.1 — 2026-10-07

### Fixed

- Panel tokens decode strictly: `base64.RawURLEncoding` ignored the
  unused low bits of a signature's last character, so several spellings
  of one token were accepted (not a forgery — each needed the real
  signature). Now exactly one spelling is valid.
- The OTLP test fixtures live in the module's own `testdata/otlp`, so
  `go test` runs from the module cache (otel's copies too, test-only).

## 0.8.0 — 2026-10-07

The 2026-10-02 production-readiness pass and its follow-ups, tagged in
dependency order: root `v0.8.0` (additive: `ParkAllExcept`), then
`thread/v0.9.1` and `obsdb/v0.2.0`, then `obsdb/clickhouse/v0.2.0` and
`otel/v0.2.0`, then `studio/v0.4.0`, then `studio/cmd/v0.2.0` and
`runtime/v0.2.0`. Every module requires its siblings' new tags, no
replaces. `thread/sqlite` (v0.3.0) and the adapters (`openai`,
`anthropic`, `google` v0.3.8, `mcp` v0.1.9) are unchanged and not
retagged. The pre-freeze modules (obsdb/clickhouse, otel, studio,
runtime) carry breaking changes, listed first in their sections; their
tags are minor bumps.

### weft 0.8.0

- Added `ParkAllExcept(names...)` — the default-deny park rule: every tool
  call whose tool is not named parks at the approval boundary (ADR 0007),
  evaluated against each step's dispatch snapshot (so `ToolSource` tools
  are covered) and inherited by Subagent child runs; park rules only add
  up; an agent's own `Output` submission never parks under it;
  `weft.override.park_all_except` joins the fingerprint (ADR 0024
  amendment under "Per-run configuration is not a seam").
- Fixed: run records are no longer dropped when a model emits tool-call
  arguments that are not JSON — the `tool_start` event, the assistant's
  `weft.messages` batch, a parked call's `run_finish` and later runs'
  input records carry the raw bytes as a JSON string.
- Fixed: no `weft.messages` record is emitted after the run's context
  ends (S1.3's cancellation rule now covers transcript batches).
- Fixed: `Replay` — the last option wins; `Replay(ReplayNever)` after
  `Replay(ReplaySafe)` is never's.
- Fixed: `weft.override.hash` / `.tools` / `.park_on` treat tool names as
  a sorted, de-duplicated set — equal experiments hash equal.
- Fixed: `Agent.CallTool` called from a tool handler dispatches a call of
  its own — never approved, so a `RequireApproval` tool still returns
  `ErrApprovalRequired` under an approved outer call; its handler's
  `CallFromContext` reports its own id and name.
- Changed: under `Metadata`'s 64-key cap, `weft.*` keys are kept first,
  so a large caller tag set no longer drops `weft.session.id` /
  `weft.turn` / `weft.public_id`.

### thread 0.9.1

- A steer that cannot join the running turn (it meets the approval
  boundary or a StopWhen end, or arrives after the last drain point)
  now becomes a follow-up turn under the run options of the turn it was
  aimed at — the in-flight turn, or the parked turn when only a
  boundary holds the session — with the steer's own options after
  them, and the aimed turn's context values beneath the steer's
  context. A turn sent with `weft.ParkAllExcept` keeps its steer
  follow-ups parked (they ran unparked before). A steer to an idle
  session still runs under its own options. ADR 0019 amendment.
- A resume (after `Decide`, `Resume`, `Send` or `Continue`) runs on the
  arming call's context for cancellation and on the parked turn's
  context values for every key the arming context lacks, so a park
  rule or metadata that rode the parked turn's context binds the
  resumed steps whoever decides. ADR 0021 amendment.
- thread/pool: an async child's context takes the pool's cancellation
  and the delegating call's context values, so the delegating run's
  `ParkAllExcept` list and metadata bind it as they bind a sync child;
  a resumed child (sync or async) keeps the rule on every step. ADR
  0022 §M.

### obsdb 0.2.0

- Production-readiness pass (2026-10-02):
  - `weft.playground` is read in its string spelling (`"true"`) — real
    playground runs stopped being listed as session turns.
  - `FromOTLP*`: hex ids of real OTLP/JSON senders survive a protojson
    decode; nil elements at any layer are skipped; structured log bodies
    render as JSON.
  - Hub: a resume cursor from a previous process no longer mutes the
    subscription; an overflow drop releases its watcher goroutine.
  - New `DedupTranscript`: a resume's rebuilt tool message supersedes the
    partial one; every backend's `Transcript` reads through it.
  - New `MaxGaps` (1000) bounds `EventPage.Gaps`; new `MaxTies` (500)
    bounds how far a page runs past `Limit` to finish a tie; new exact
    cursor `RunQuery.BeforeID`/`RunPage.NextBeforeID` and
    `SessionQuery.BeforeID`/`SessionPage.NextBeforeID`;
    `SessionPage.Total` no longer shrinks with the cursor.
  - obsdbtest: Idempotence reads spans back; new subtests PipelineSpellings,
    PagingTies, SessionPaging, TranscriptRebuilt, NonFiniteAttrs, ZeroTimes,
    OutOfOrder, HeartbeatOnly, Strings, MetaFilter, SessionUncapped,
    ReadErrorIsNotNotFound, FilterCombinations, PagingBigTie,
    ManyDaysOneBatch, CloseRace; status fixtures run on the wall clock
    (no expiry under slow or repeated runs).
- obsdb/sqlite:
  - `Events` never reports `Done` with stored events missing; gap
    detection no longer scans the run on every page.
  - `Write`: a NaN/Inf attribute no longer fails the batch; zero
    timestamps are stored as unknown and records fall back to Observed;
    a run's `Started` is the earliest time seen of any record or span.
  - `Session` returns every turn (the 500 cap dropped the newest); paging
    never skips rows tied on the cursor time; one clock for the status
    filter and row status.
  - `Open`: paths containing `%`, `?`, `#` or starting with `//` open the
    file they name (a database previously created at the misparsed
    location is no longer the one opened for such a path).
  - Calls racing `Close` return `ErrClosed`; `Experiment` reports a failed
    read as that error, not `ErrNotFound`.

### obsdb/clickhouse 0.2.0 (breaking)

#### Breaking

- Migration 0003 changes the stored `otel_traces.SpanKind`/`StatusCode`
  strings to the pinned collector's spelling (`Internal`, `Error`, …);
  anyone querying those columns directly reads the new spelling (both
  are read back by this module). Rows stored before 0003 are not
  backfilled.

#### Changes

- Production-readiness pass (2026-10-02):
  - Runs/Sessions filters read each run's merged row: subagent runs no
    longer surface as top-level runs or session turns before a merge.
  - Migration 0003: the traces view reads the pinned collector's real
    status spelling (`Error`); spans are written as `Internal`/`Error`/…
    and both spellings read (breaking for anyone querying
    `otel_traces.SpanKind/StatusCode` strings directly). Collector-written
    failures stored before 0003 are not backfilled.
  - Paging carries tied rows (bounded by `obsdb.MaxTies`) and honours the
    exact `BeforeID` cursor; Sessions `Total` is the whole match;
    `Transcript` reads through `obsdb.DedupTranscript`.
  - Events reads the terminal flags before the events; `Gaps` capped;
    `Session` returns every turn and no longer exceeds `max_query_size`
    on huge sessions.
  - Non-finite attributes no longer cost a span its typed attributes; a
    zero record time falls back to Observed and the epoch reads back as
    the zero time; a batch may span any number of day-partitions.
  - Experiments keep nanosecond Created/Updated; a failed read is no
    longer `ErrNotFound`; rows closed on every path.
  - `ResolvePublicID` orders by (turn, started) like sqlite and reads by
    primary key; TTL windows round up to whole seconds; detail reads
    narrow `FINAL` by primary key; calls racing `Close` return `ErrClosed`.

### otel 0.2.0 (breaking)

#### Breaking

- `Pipeline` is no longer comparable (apidiff: "Pipeline: old is
  comparable, new is not"): code that compared `Pipeline` values with
  `==` or used them as map keys compares the `*Pipeline` instead.

#### Changes

- Production-readiness pass (2026-10-02):
  - Security: an https destination stays https when
    `OTEL_EXPORTER_OTLP_ENDPOINT` is an http:// URL (bearer tokens and
    content were sent in cleartext).
  - Security: redacted tool arguments that are not JSON are exported as a
    JSON string; a failed re-encode strips the record instead of exporting
    the unredacted body. A panicking `Redact` no longer unwinds the run
    (the record is exported stripped and counted) and is named in the
    WARN by type only.
  - Shutdown and `ForceFlush` flush every destination in parallel under
    the shared budget — a hung destination no longer costs a healthy one
    its last records.
  - Sampled-out runs that fail or are cancelled leave the run tracker (no
    endless heartbeats); heartbeats no longer carry `weft.event.type` /
    `weft.content`; `Heartbeat` ≤ 0 disables instead of panicking.
  - A failed `Start` releases everything it built (including exporters
    passed to `Exporters`); every `Local` DB closes at Shutdown; `Install`
    builds each destination once; `LocalDB()`/`StudioEndpoint()` return
    nil/"" after the installed pipeline shuts down.
  - Failed exports are counted per destination with the cause in the
    throttled WARN; policy filtering counts without warning; Local write
    failures no longer flood OTel's error handler.
  - `OTEL_EXPORTER_OTLP_HEADERS` values are percent-decoded; de-dup
    compares resolved export URLs; scheme-less endpoints read as https,
    non-http(s) schemes are refused, endpoint errors do not echo the URL.
  - `BatchDelay` applies to `Exporters` destinations; the Local path keeps
    bytes/slice/map attributes and renders structured bodies as JSON.
  - `ContentConfig.Redact` godoc states it sees event bodies and deltas
    only — `weft.messages` transcript records are not passed through it.
- Security: `ContentConfig.Redact` now applies to `weft.messages`
  transcript records too, part by part with the event path's kinds (text
  of every role `ContentText`, reasoning `ContentReasoning`, tool-call
  args `ContentArgs`, tool results `ContentResult`); ids, names, roles,
  signatures, file parts and record attributes are untouched, and the
  transcript is still never capped. A batch that cannot be decoded for
  redaction, or whose `Redact` panics, is dropped and counted — never
  exported unredacted. A masking redactor previously leaked the whole
  transcript (Local sink included).
- An `OTEL_EXPORTER_OTLP_ENDPOINT` written with `http://` is the
  operator's plaintext opt-in for that environment destination (the
  docker-compose/sidecar `http://otel-collector:4318` was refused as
  "needs Insecure()"). Code-configured `OTLP("http://…")` to a
  non-loopback host still needs `Insecure()`; an http:// `WEFT_STUDIO_URL`
  to a non-loopback host stays refused (bearer token).
- `Install` with no destination option falls back to the local sink, with
  one WARN, when every environment destination fails to build (it
  recorded nothing); `Start` still returns the error.

### studio 0.4.0 (breaking)

#### Breaking

- Without a `Token`, the API answers only a loopback `Host` or an
  `AllowOrigins` origin's `host:port` (the DNS-rebinding guard, below):
  an embedded Studio served on a real hostname must set
  `studio.AllowOrigins(...)` or `studio.Token`.
- studio/runtime: `RuntimeServer.Enqueue` (and the approval/steer
  paths) refuse what they used to accept — a caller's command id outside
  `ValidCommandID` (`ErrInvalidCommandID`), decisions for calls not in
  the run's pending set, out-of-range option values.

#### Changes

- Production-readiness pass (2026-10-02) — security:
  - A caller's `command_id` is validated (`[A-Za-z0-9._:-]{1,128}`) — a line
    break in it forged frames on the runtime's command stream.
  - Read-scoped panel tokens can no longer decide approvals, steer, or read
    `/api/manifest`; `/api/runtimes` omits agent instructions for them.
  - A playground-scoped token's source run must be inside its public id;
    it may not set `experiment_id`; experiments routes refuse panel tokens;
    `/api/live` checks every frame against a panel token's public id; scope
    checks fail closed on database errors; panel tokens' 500s no longer
    carry database error text; tokens compare in constant time.
  - Loopback-open ingest refuses forwarded requests and foreign browser
    Origins (a proxied Studio must set `IngestToken`).
- API and behaviour:
  - Request bodies capped at 4 MiB (413); option values validated
    (temperature 0..2); approvals validate `call_id` against the run's
    pending set; a fork-mode run is steered like an ephemeral one (an
    accepted ack naming the fork's turn maps it; no 409).
  - Transcript batches carry `input` and the real `step`; transcript edits,
    `from_step` and fixtures count the run's own steps (the input record is
    context).
  - `/api/runs` and `/api/sessions` take `before_id` and answer
    `next_before_id` (an exact cursor inside a tie); the live backfill and
    experiment detail page with it; experiment detail returns every
    top-level run, oldest first.
  - Command rows expose `status` (succeeded|failed) and the finished run's
    `error`; a panel token can poll its own approval decision and steer the
    resumed run.
  - `/api/live`: per-frame write deadline, HEAD answers headers only,
    backfill honours cancellation, bounded dedup set, `X-Accel-Buffering:
    no`; `Vary: Origin` on every CORS answer; error bodies always JSON.
- studio/runtime: `ValidCommandID`, `ErrInvalidCommandID`, `Retention`
  (24 h), `WriteTimeout`, `RuntimeView.Breakpoints`; terminal
  commands, run routes and dead runtimes are pruned; replaced or stalled
  streams end at once and their accepted commands get a finish watch;
  duplicate acks no longer cancel timers; HEAD on the stream is 405;
  breakpoints are stored only when delivered, adopted from the register
  payload and sent before the backlog on every stream open.
- Web UI: a token prompt on a walled API and `#token=`/`?token=` hand-over;
  a thread turn shows its own prompt and reply; live streams reconnect with
  backoff and lose nothing at the walk/stream seam; live renders coalesce
  on large runs; finished runs show stored transcript words; event gaps are
  surfaced; sessions list pages and nests experiments under their source
  turn; the playground targets the source run's agent, tracks every
  variant, shows held approval decisions and the run's outcome, waits for
  the run to settle, caps live cards, reads the hand-off from the URL
  fragment and handles fork mode; malformed stored data and cyclic or huge
  traces no longer crash or hang a page.
- Devtools panel: attribute changes and remounts no longer remove or
  mis-scope it, and `window.__WEFT__` scopes every connected panel; the
  keyboard never takes the host page's keys; redraws keep focus and caret
  and are throttled while streaming; nothing is thrown into the host page;
  streams and polls are bounded; verbs and typed text never carry across a
  conversation switch; the tail subscribes before reading pages; stale
  "running" rows are re-read; approval controls act on the run's pending
  set with per-call decisions; read-scoped tokens are offered no write
  verb; the hand-off rides the URL fragment; the build fails on any
  package code or over the 80 KiB gzip budget.
- Playground: `side_effects: "allow"` accepts a ReplaySafe tool that is
  not opted in (it was refused 403); the panel drawer and `/playground`
  side-effect selects say what each mode does — only `allow` runs the
  app's `AllowSideEffects` tools for real.
- Security (behaviour change): without a `Token`, the API — the whole
  `/api` tree, `/api/live` and the runtime link included — answers only a
  loopback `Host` (`localhost`, `*.localhost`, `127.0.0.0/8`, `[::1]`, any
  port) or the `host:port` of an `AllowOrigins` origin. A DNS-rebinding page
  (`http://evil.example:7331` resolving to 127.0.0.1) could start playground
  runs, approve parked calls, steer and read transcripts; it now gets a 403
  naming what to configure. An embedded Studio served on a real hostname
  lists its origin (`studio.AllowOrigins("http://myapp.internal:8080")`) or
  sets `studio.Token`. `X-Forwarded-Host`/`Forwarded` are never trusted;
  in-process callers (`runtime.Local`) pass; `/panel.js` and the UI shell
  are unchecked; nothing changes with a `Token`.

#### Release asset

- `panel-v0.4.0.js` (87779 bytes, sha256
  `c1265cf322d1bed2779ef9e1c2bef969ae604af1be6af831f424387865d15221`
  beside it) — the devtools panel for non-Go backends; serve it from
  your app and add `<script type="module"
  src="/static/panel-v0.4.0.js" data-endpoint=… data-token=…
  data-public-id=…></script>`. Staged by `make studio-panel-asset`
  (RELEASE_DIR, default `studio/web/dist-release` relative to the repo
  root); byte-identical to the committed `studio/dist/panel/panel.js`.

### studio/cmd 0.2.0

- Production-readiness pass (2026-10-02): ReadHeaderTimeout/IdleTimeout
  set; shutdown ends open streams at once; the ClickHouse handle closes on
  shutdown; a generated dev token is printed as an openable
  `http://addr/#token=…` link, a fixed `--token`/`WEFT_STUDIO_TOKEN` is
  never printed; an unopenable `--db` is an error, not a panic; `sqlite://`
  without a path is a usage error; the DSN mask handles passwords
  containing `@`.

### runtime 0.2.0 (breaking)

#### Breaking

- `AllowSideEffects` is honoured only in `side_effects: "allow"`: an
  opted-in tool no longer runs for real in `substitute` (the default) or
  `park` mode (details below).
- Side-effect parking is default-deny (`weft.ParkAllExcept`): tools a
  `ToolSource` supplies and Subagent children's tools park unless
  vouched safe or opted in.

#### Changes

- Production-readiness pass (2026-10-02) — safety:
  - Side-effect parking is default-deny (`weft.ParkAllExcept`): tools a
    `ToolSource` supplies and a Subagent child's tools park unless vouched
    safe or opted in; an Output agent's `submit_output` no longer parks;
    runs carry `weft.override.park_all_except`. Requires the root release
    that adds `ParkAllExcept`.
  - Approval decisions are checked against the parked run's pending calls;
    a parked run resumes once, when every pending call has a decision;
    concurrent decisions can no longer run an approved handler twice; a
    second decision on a call is rejected; a parked run whose resume never
    started is restored.
  - Fork mode: forks revoke the source session's approval grants; a steer
    into a fork's turn is a thread steer under the turn's run options (the
    park rule binds its follow-up — requires thread's steer follow-up fix);
    the fork's turn is acked accepted again naming its run id once in
    flight, so Studio can steer it; forking a
    source waiting on approval fails fast; a parked fork call is decided
    through the fork session, stays decidable after its park record is
    evicted, and "decide first" names the calls; naming an older fork turn
    forks from that turn; the accepted ack carries no invented run id.
  - A scripted command never runs on the live model; breakpoints are never
    substituted; the substitute chain answers repeated calls in recorded
    order, keeps recorded errors as errors, matches canonical JSON args and
    is bounded by MaxSteps.
- Correctness and robustness:
  - Source transcripts are split once into input + the run's own steps on
    every path (thread, obsdb, Studio): from_step, edits, the default
    prompt, scripted indexing and substitution count the run's own steps;
    resumed turns split the same way everywhere; the thread path refuses
    turns that ran over a summarized context; the scripted engine replays
    reasoning/tool-call signatures.
  - Budgets count failed runs and every chain leg, reserve at admission and
    release a reservation whose command never ran.
  - The runtime rejects unknown engines, modes, thinking levels, options,
    out-of-range values, bad run/command ids and from_step out of range.
  - The link: stop cancels and waits for in-flight runs; bounded
    seen/parked/forks/frames; 16 concurrent runs, 256 admitted commands;
    jittered backoff that resets; register/header timeouts; panics
    contained; the resume cursor is arrival order; oversized frames are
    acked rejected; the in-process transport honours the caller's
    context; the register payload reports `breakpoints`; finished acks
    carry `error`.
  - Fork mode under thread 0.9.0's writer lease: the runtime keeps the
    fork `Session`s it continues in place (it never reopens one, so the
    0.1.1 close-after-every-turn fix is superseded), and closes a fork's
    `Session` once nothing it holds needs it — evicted past the 64-fork
    bound with no park record or in-flight turn on it, or at `stop` —
    so another writer can continue that session (on jsonl, the file lock
    is given up). The read-side source `Session` is closed after the
    fork, and a fork whose grant revocation fails is closed. A turn
    re-run after an overflow no longer leaves its first run id in the
    steer registry.
- `AllowSideEffects` is honoured only in `side_effects: "allow"`
  (WEFT-PLAYGROUND §5.1/§6.3; behaviour change): an opted-in tool used to
  run for real in every mode. In `substitute` (the default) it is now
  answered from the source's recorded result or parked, in `park` it
  parks; ReplaySafe tools still run in every mode. `allow` no longer
  refuses a ReplaySafe tool that is not opted in.
- The in-process link (`runtime.Local`) addresses Studio as
  `http://localhost` (was `weft.studio.local`), a Host Studio's
  DNS-rebinding guard accepts like any loopback request.

### CI / repo

- The ClickHouse job (its service's invalid `ulimits:` key already
  moved into `options:` with a health check by the thread 0.9.0 train)
  fails when a gated test skips, and studio/cmd's step must report
  `TestNewServerClickhouse` passed. Actions bumped to node24 majors (the
  nightly soak workflow too); Go cache keyed on every go.sum.
- The apidiff gate covers every workspace module against its own tag
  (`make apidiff-all`, which CI runs in place of the per-module steps;
  root/thread/thread/sqlite/adapters enforced,
  obsdb/clickhouse/otel/runtime/studio reported, studio/cmd and examples
  skipped, a module without a policy refused; `APIDIFF_STRICT=1`
  requires building from published tags). A sub-module that needs an
  untagged sibling loads through the workspace with a note;
  thread/sqlite loads through go.work first, as `make apidiff-sqlite`
  always did. `make apidiff-selftest` exercises twelve failure modes
  (root, thread, thread/sqlite, a reported module, the workspace
  fallback and its strict refusal, an unknown module).
- `make studio-check` also fails on dist files the commit does not contain;
  `RELEASE_DIR` resolves from the repo root.
- anthropic/google/mcp go.sum tidied so they build `GOWORK=off`.
- examples/studio-local registers its thread store with the runtime (fork
  mode works in the demo; the panel gate covers it); `/run` takes the
  question from the body.

## thread 0.9.0 / thread/sqlite 0.3.0 — 2026-10-02 (the 2026-10-01 review fix train)

The fixes for the 2026-10-01 production-readiness review
(`WEFT-THREAD-REVIEW-2026-10-01.md`: twelve P1s, some forty-five P2s),
built as seven lanes — session core, compaction, backends, approvals,
the writer lease, the pool, the turn machinery — and a closing pass. It
is a **breaking minor for `thread` and for `thread/sqlite`** (a schema
migration, and it needs the new `thread`); `runtime` carries one fix
and ships as 0.1.1 in the entry below. Pre-1.0: breaking changes ship without
deprecation shims, and the migration checklist below names every one.

**Not a freeze.** The plan's "v0.8 freeze" — the format as a
compatibility promise, `thread.Migrate`, an emptied `.apidiff-allow` —
is deferred by maintainer decision until the API has proven stable in
real use. The API and the stored format may still change. What holds
today is the reader's rule: an unknown entry kind, a newer entry
version or a newer header envelope fails with `ErrNewerFormat`, never
a skip, and goldens pin each format version the build reads. ADR 0011
gained the amendment that records the train's decisions and a format
reference for the format as it is; `docs/thread-operations.md` is new.

### Security

- **A signed decision is bound to the occurrence it was minted for.**
  The challenge covered the call id, and call ids repeat across turns:
  a signature minted for one turn's `call_1` approved the next turn's.
  The request entry's id and the run id are now signed and checked
  against the pending request (`SignedDecision.RequestID`, `RunID`;
  `Request.ID`); a signature for another occurrence is
  `ErrNotPending`. The challenge domain moved to
  `weft/approval-challenge/v2`, so no v1 signature verifies.
- **An empty nonce is a bad signature**, and a nonce must be one the
  session's ring minted for that request. A blank nonce used to skip
  the replay scan, so the same bytes verified twice.
- **The session enforces the request's own expiry**, on every path
  that records a decision or arms a resume — `Decide`, `DecideSigned`,
  `Resume`, `Send`, the runner's pickup — not the expiry the signer
  stated, and not only in `Resume`. `Decide(Approve)` on a lapsed
  request used to run the tool.
- **`RequireSigned` is the session's, durably.** It was a process
  flag, lost on reopen and on `Fork`. `Create` now writes it into the
  header (`weft.require_signed`), every `Open` enforces it, a fork
  inherits it, pool children inherit it, and it no longer wedges the
  session's own machinery: an interrupting `Send`'s denial, an expiry
  denial and a pool delegation's resolution all record under it.
- **A decision is spent by the resume that applied it.** A `Branch`
  back to a decided boundary used to run the approved call again on
  the old approval; the calls are now pending again and need new
  decisions.
- **Quorum counts keys, not names**, for signed approvals: one key
  that signs as two `Who`s is one approver.
- **An unknown key id is indistinguishable from a bad MAC**
  (`ErrBadSignature` for both): `DecideSigned` no longer tells a
  caller which key ids exist.
- Refused signed decisions are audited (`StepSigned`), bounded: at
  most 16 per request, and nothing for a signature that fails its MAC
  and names no pending call.
- Grant predicates read arguments exactly: RFC 6901 array indexes are
  strict (`/01` is not `/1`), `?` in a glob is one code point, integers
  compare digit for digit, and a grant's `MaxUses` holds within one
  turn.

### Fixed

Session core
- `Open` validates the entry tree: an empty, invalid or duplicate
  entry id, a parent that is not an earlier entry, or a leaf entry
  navigating to an entry the file does not hold fails with a
  `*CorruptError` (`ErrCorrupt`) naming the line and the entry. A
  dangling parent used to cut the model's context short, silently.
- `Entries`, `Path` and `Audit` return deep copies of every entry
  kind; mutating a snapshot used to corrupt the in-memory tree.
- A public id can no longer be rotated: `SetInfo` rejects `weft.`
  keys, and `Session.Meta` never lets an info entry override one.
- `Fork` builds its session as `Open` does (compaction resolved,
  measurements recovered), honours the header options, rejects a leaf
  entry as its target, and removes a fork it could not write.
- `Open` never starts a run. Steers a crashed writer left queued used
  to be re-run in the background from inside `Open`.
- `examples/session` runs twice in a row.

Turns
- **A mixed tool batch — one call runs, one parks — resumes.** The
  resume's completed tool message was appended as a second tool
  message, the parked call read dangling for ever, and the session
  either looped on resume or queued every later `Send` silently. The
  completed message now replaces the partial one on the active path
  (ADR 0011 §7, amended).
- **Step writes are exactly-once.** One failed per-step append used to
  drop a message and duplicate another. A failed batch is held and
  written in order by the next step or the turn's end;
  `TurnEntry.LateSteps` counts them, and a turn whose end cannot be
  written says so (`ErrNotPersisted`).
- Run ids are unique across a reopen: the counter is recovered from
  the highest id any entry records (prompt entries now carry theirs),
  not from the number of turn entries.
- A prompt whose flush fails no longer leaves a second copy of the
  user message when the `Send` is retried.
- An interrupting `Send` whose denial cannot be recorded fails and
  leaves nothing queued; `Wait` used to hang.
- `Rollback` of a session's first turn returns to the root.
- A `Branch` or a `Reject` send can no longer slip in while the runner
  picks up a settled boundary.
- A panic in the session's turn machinery, or in the caller's `IDs` or
  `Clock` function, ends the turn with `ErrTurnPanicked` instead of
  wedging the session.
- A call left dangling by a crash mid-step is not read as an approval
  boundary.
- `TurnEntry.Canceled` is set for a deadline as well as a
  cancellation; an overflow attempt's usage is kept (its own turn
  entry).
- A mirrored child request stays decided wherever the leaf moves.

Compaction (ADR 0020, amendment 2026-10-01)
- **`AfterCompact` no longer deadlocks** a hook that touches the
  session: every compaction hook and swap-in runs without the
  session's lock and may call the session.
- **A trim no longer cuts the iterative summary chain**: the previous
  summary is the latest *summary* compaction's, so the first summary
  stays in the model's context.
- **Trims replay from the entry.** A trim records each stubbed tool
  result; the context applies exactly those stubs under any options,
  in any process. A custom `Trimmer` now changes the model's context
  (it never did), and a trim the record cannot represent fails loudly
  (`ErrInvalidCompaction`) and the summary runs instead.
- A summary cut off at `max_tokens` is never stored
  (`ErrSummaryTruncated`: one retry, then the fallback).
- `BeforeCompact`'s edits to `Messages`, `Instructions`, `Pinned` and
  `FirstKept` are honoured — a redaction hook works.
- `TokensBefore` and `Preparation.Context` count the compacted view;
  `SummarizeLeft` summarizes the branch's compacted view, not the raw
  range beside its own summary.
- The trigger stands down after a compaction until the next provider
  report; the rate limits count along the leaf's path; a second
  compaction at the same boundary is refused.
- `AfterCompact` fires for trims; `CompactFailed` fires for failed
  writes and not for dry runs.

Backends
- **A torn final line no longer poisons the session.** The next writer
  removes it before appending — jsonl truncates and fsyncs under its
  lock, sqlite deletes the torn row, Memory clips — and logs it. It
  used to glue the next entry onto the half-line; from the third
  process on the session was `ErrCorrupt`.
- **sqlite `Watch` no longer deadlocks** a consumer that calls the
  same `Storage` inside the loop.
- **sqlite's lock no longer treats a pid as an identity.** A restarted
  container (PID 1 again) was locked out of its own sessions for
  ever. Lock rows carry a process token and the process start time.
- **jsonl `Watch` no longer panics** on a file with no complete line;
  a malformed line ends it with `ErrCorrupt`; it reads only new bytes
  per poll instead of the whole file.
- Both `Watch`es end with `ErrNotFound` when the session is deleted
  and created again under them (they stalled for ever).
- `Query.Before` no longer skips sessions that share a creation time:
  `Query.BeforeID` completes the keyset cursor.
- sqlite `List` pages in SQL (keyset, `LIMIT`, `COUNT` over a new
  index); it was a fleet scan per page.
- jsonl `List` no longer allocates 1 MiB per session file per call;
  jsonl `Create` no longer holds the instance mutex across fsyncs.
- jsonl no longer keeps a file descriptor and a lock per session for
  the life of the process: `Session.Close` gives both up, and
  `Releaser.Release` does it for a `Storage` used directly.
- The listed title is the last non-empty info title on every backend;
  `ErrLocked` names the session id, not a path; sqlite `:memory:`
  survives a replaced connection; a path containing `?`, `#` or `%`
  opens the file named; sqlite migrations return errors instead of
  panicking at init; Windows liveness reads access-denied as alive.

Approvals
- Under `Quorum(n ≥ 2)` a call the chain approved once parks with a
  request entry and fires `OnRequest` (it parked with neither, and
  could never expire).
- `ApproveAlways` mints its grant only when the call's effective
  verdict is approve — never beside a denial — and works on a call
  with no arguments.

Pool (ADR 0022, amendment 2026-10-01)
- **Fan-out × depth no longer deadlocks.** A run whose sync delegation
  is running a child hands its slot back and re-acquires it through
  the queue: slots bound work, not waiting.
- **A child that parks twice resumes.** Only the calls pending in the
  child now are replayed; the decisions of an earlier park are not.
- **The delegating call cannot be decided into a second child**:
  `Decide`, `DecideSigned` and `Request` refuse it with
  `ErrDelegated`.
- A `RequireSigned` parent receives its child's answer; a resumed
  child takes a slot and its receipt records `running` again; a park
  the parent cannot record fails the delegation instead of leaving it
  parked for nobody; nested requests carry a real expiry.
- Receipts always end: settlement is idempotent (`Delegated` counts a
  child once) and bills the child's whole ledger, not its last run.
- `Close` covers wrapped calls made outside any session; the pool
  closes each child's session when its delegation settles.

`runtime`
- The playground's fork mode closes the forked `Session` when its turn
  lands. It only dropped the reference, and under the writer lease the
  next fork command's reopened `Session` could not write ("keep
  chatting" failed with `ErrLocked`).

The closing pass
- The parent's decision chain never decides a delegating call: a grant
  or a live `Approver` matching a pool wrap's tool approved the wrapper
  when it parked, and the resume re-ran the delegation in a second
  child session. Such a call now always parks and only
  `ResolveDelegation` resolves it.
- A decision the parent records on its own reaches the child: an
  interrupting Send's denial, an expiry or a direct `Session.Decide`
  on a mirrored request used to leave the child parked until the next
  `Pool.Decide`. The pool now pumps when one lands.
- A session deleted and created again under its id is stale to the old
  `Session` (`ErrStale`): `Leaser.Acquire` also reports the stored
  header's `Created`.
- jsonl: a failed or short append leaves no prefix of its batch; the
  bytes are cut off again under the session's lock.
- A fork settles a copied queued send as dropped in its own entries.
  `Continue` runs restored queued sends under its own context.
- A `CustomMessage` appended while a turn runs is refused with
  `ErrBusy`: it landed between the run's step entries and `Context`
  read a transcript no run produced.
- `examples/studio-local` keeps one `Session` for the demo's life; it
  reopened per request and the writer lease refused the second.
- Internal path reads no longer deep-copy the transcript (on a
  1000-turn session a turn costs 1.31 ms, `Pending` 0.11 ms and one
  allocation). `Context`, `Path` and `Entries` still return copies.

### Added

- **`sqlite.BreakLock(ctx, st, session)`** — the operator's way out of
  a lock row another host left (a container replaced under a new
  hostname, a restored backup). A holder that was alive after all
  fails its next write with `ErrLocked`.
- **Crash matrix**: eight more points — the expiry sweep, the automatic
  trim record, a queued send's accepted receipt, the resume join, a
  fork's settling entries, and the pool's parked, canceled and capped
  receipts. `threadtest.RunTurns` runs on jsonl and sqlite too.
- **CI**: an apidiff gate for `thread/sqlite` (`make apidiff-sqlite`),
  a nightly soak (`make soak-thread`: thread `-race -count=10`,
  thread/sqlite `-race -count=3`); the gate's self-test no longer
  exercises the deleted store module.

- **`Session.Close(ctx)`** — stop new work, drain, seal (`ErrClosed`),
  release the storage's hold.
- **The per-Session writer lease**: optional capability `thread.Leaser`
  (`Acquire`, `Yield`), implemented by Memory, jsonl and sqlite, and
  `ErrStale` for a Session whose view fell behind.
- `thread.Releaser` (end a backend's hold on a session),
  `thread.NoLock()`, `thread.OpenLogger(l)`; `thread.Memory` takes the
  open options. jsonl has a real writer lock on Windows (`LockFileEx`);
  a platform with no file lock fails `jsonl.Open` unless `NoLock`.
- Package **`thread/backend`** — `Config` and `Resolve`, for backend
  authors.
- `Session.LoadReport()` (`*OpenReport`: torn, skipped, orphaned);
  `CorruptError.Entry`, and `CorruptError` unwraps to its cause too.
- `Session.Continue(ctx)` — run what `Open` restored, now.
- **Queued sends are durable at acceptance** (an `accepted` receipt);
  `Open` restores them, `Session.Queue` lists them
  (`QueuedSteer.Policy`), `ClearQueue` drops them.
- `Turn.Done()`, `Turn.WaitContext(ctx)`, `Turn.Outcome()`
  (`TurnOutcome`: running, answered, parked, delivered, deferred,
  dropped, failed, canceled), `Session.WaitIdle(ctx)`.
- `ErrNotRun`, `ErrDropped`, `ErrTurnPanicked`, `ErrNotPersisted`,
  `ErrClosed`, `ErrCreateOnly`, `ErrReservedKey`.
- `Policy.String()`; `TurnEntry.Policy`, `ReRun`, `LateSteps`;
  `MessageEntry.RunID`; `ReceiptEntry.Unanswered`; `ReceiptAccepted`.
- `thread.Clock(func() time.Time)`.
- `Query.BeforeID`.
- Compaction: `TrimRecord`, `TrimStub`, `Compaction.Trim`,
  `CompactionEntry.Trim`; the sentinels `ErrNothingToCompact`,
  `ErrCompactCanceled`, `ErrNoEntry`, `ErrSummaryTruncated`,
  `ErrInvalidCompaction`, `ErrCompactConfig`, `ErrNotPinnable`,
  `ErrAwaitingApproval`.
- Approvals: `Keyring.Sign`, `Key.Sign`, `Request.ID`,
  `SignedDecision.RequestID`/`RunID`, `ErrInvalidDecision`,
  `ErrDelegated`, `StepSigned`; `ApprovalDecisionEntry.RequestID`,
  `Always`; `ApprovalAuditEntry.GrantID`, `GrantShared`, `KeyID`,
  `Decisions`.
- Pool: `MustWrap`, `Wait`, `Recover`, `DecideSigned`, `MaxDepth`
  (default 8), `Children`, `Descendants`, `State` with `Settled()`,
  `Receipt.Call`, `StateError`, `ErrUnknownReceipt`, `ErrNoAgent`,
  `ErrDepth`, `ErrCycle`, `ErrDuplicateWrap`, `CodeSubagentDepth`,
  `CodeSubagentCanceled`; in `thread`, `PoolParked`,
  `InheritApprovals`, `MirroredRequest` and the pool's plumbing on
  `Session` (`ReplayDecisions`, `CancelDelegated`, `MirroredRequests`,
  `DenyMirrored`, `ResolveDelegation`).
- `threadtest`: `RunTwoWriters`, `RunLeaser`, `RunOneWriter`,
  `RunTurns`, `RawHeaderInjector`; `Run` now runs the `Watch` table for
  a `Watcher`, and gained rows a careless backend cannot pass (invalid
  ids on every method, paging through ties, limit normalisation,
  concurrent appends to one session, a load under a concurrent delete,
  an append after a torn tail, a loud header, `Flusher`, `Releaser`).
- Godoc examples across the surface (sessions, turns, steering,
  compaction, approvals and signing, the pool, the backends).

### Changed (breaking) — the migration checklist

Renamed, removed, re-signed (the compiler finds these):

| Was | Write now |
|---|---|
| `thread.Instructions("…")` (a `CompactOption`) | `thread.SummaryInstructions("…")` |
| `thread.Disabled()` | `thread.NoAutoCompact()` |
| `thread.Proceed`, `thread.Cancel` (variables) | `thread.Proceed()`, `thread.Cancel()` |
| `thread.WithApprover(a)` + `thread.ApproverTimeout(d)` | `thread.WithApprover(a, d)` — `d` must be positive |
| `thread.SignDecision(secret, r, d)` with a hand-kept key map | `ring.Sign(r, d)` or `key.Sign(r, d)` (both return an error); `SignDecision` stays for a bare secret |
| `thread.OpenConfig`, `thread.ResolveOpen` | `backend.Config`, `backend.Resolve` (package `thread/backend`) |
| `Estimator.Estimate(...) int` | returns `int64` |
| `thread.SummaryMaxTokens(int)`, `SummaryInput.MaxTokens int` | `int64` |
| `Trimmer.Trim(ctx, msgs) ([]weft.Message, TrimReport)` | returns `([]weft.Message, error)`; `TrimReport` is gone — the entry's `TrimRecord` is the report |
| `Preparation.SplitPrefix`, `Summary.Reason`, `SummaryInput.SummaryModel`, `Compaction.FilesModified` | removed; nothing set or read them (`files_modified` stays readable on the wire) |
| `thread.ErrNotImplemented` | removed; nothing returned it |
| `(*CorruptError).Unwrap() error` | `Unwrap() []error` — `errors.Is`/`As` reach the class and the cause |
| `thread.Memory()` as a `func() Storage` value | it is `func(...OpenOption) Storage`; calls are unchanged |
| `p.Wrap(name, desc, agent, opts...)` returning a tool | `p.MustWrap(...)`, or `tool, err := p.Wrap(...)`; one name wraps one agent (`ErrDuplicateWrap`) |
| `p.Register(id, agent)` | returns an `error` |
| `p.Decide(ctx, parent, ds...) (*thread.Turn, error)`, blocking | `err := p.Decide(ctx, parent, ds...)` records and arms; then `p.Wait(ctx, parent, receiptID)`, or the parked turn's `Next()` |
| `p.Cancel(receiptID)` | `p.Cancel(ctx, parent, receiptID)` |
| `p.Forward(ctx, receiptID, msg)` | `p.Forward(ctx, parent, receiptID, msg)` |
| `pool.Receipt.State` as a `string` | `pool.State` (same wire strings; `r.State.Settled()`) |
| `errors.Is(err, pool.ErrNotRunning)` for a wrong id | `pool.ErrUnknownReceipt`; `ErrNotRunning` is now only "the receipt exists and is in another state" (`*pool.StateError` says which) |

Behaviour (the compiler does not find these):

- **Close before reopening.** One `Session` writes a session: it holds
  the writer lease from its first write (`Create` is one) until
  `Close`. Code that opened a session a second time without closing
  the first `Session` now gets `ErrLocked` from the second one's first
  write. Call `s.Close(ctx)`; in a test that stands in a fresh process,
  `st.(thread.Releaser).Release(ctx, id)`.
- **Never append behind a `Session`.** A `Session` whose stored session
  gained entries it did not write fails its next write with
  `ErrStale`. Open the session again.
- **`Open` runs nothing and refuses header options.** `WithMeta`,
  `PublicID` and `WithLineage` passed to `Open` fail with
  `ErrCreateOnly` (they were ignored). Steers a crashed writer left
  are restored to `s.Queue()` and wait: send, or `s.Continue(ctx)`.
- **`Open` can now refuse a file it used to open**: a malformed tree
  is `ErrCorrupt`. Open the backend with `thread.Salvage()` to read
  around damage; `s.LoadReport()` says what was skipped and orphaned.
- **`SetInfo` rejects `weft.` keys** (`ErrReservedKey`); set them at
  `Create`. A `SetInfo` with nothing to record writes no entry.
- **A dropped turn returns `ErrDropped`.** `turn.Wait()` on a message
  `ClearQueue` removed returned `nil, nil`; it now returns an error
  wrapping `ErrDropped`. Use `turn.Outcome()` to tell a delivered
  steer from a deferred one (both still `nil, nil`).
- **`Wait` returns before the between-turn compaction.** A turn is
  decided when its entries land. Code that read the session right
  after `Wait` and relied on the automatic compaction having run
  calls `s.WaitIdle(ctx)`.
- **`s.Audit()` returns approval entries only** — requests, chain
  steps, decisions, grants, revocations. Turn entries are no longer in
  it; read them from `s.Entries()`. A resume is two `StepResume`
  entries: `started`, then `completed` or `failed`.
- **`thread.RunOptions` rejects `weft.Approve`, `Deny`, `Resolve`,
  `ResolveError`** (and still `Messages`, `Prompt`, `RunID`,
  `Steering`), with an error wrapping `weft.ErrInvalidRunOption`.
  Record decisions with `s.Decide`.
- **`Query.Meta` needs the key present.** A filter `{"k": ""}` no
  longer matches sessions that lack `k`.
- **`Decide`** rejects two decisions for one call, an empty batch and
  a decision with no outcome (`ErrInvalidDecision`), a decision for an
  expired request (`ErrExpired`) and one for a delegating call
  (`ErrDelegated`); it always records `Via: "user"`.
- **`ErrExpired` reads `thread: approval request expired`** (match
  with `errors.Is`, not the text); `Session.Request` returns it for a
  lapsed request.
- **`DecideSigned`** returns `ErrBadSignature` for an unknown key id
  (was `ErrUnknownKey`, which only `Keyring.Sign` returns now) and
  `ErrNotPending` for a signature minted for an earlier occurrence.
  Signatures minted before this version do not verify: request a new
  challenge.
- **`NewKeyring` needs at least one key**; `RequireSigned` without a
  keyring that has an active key fails `Create` and `Open`;
  `SignDecision` signs an empty `Who` as the key's id.
- **Compaction is between turns**: `Compact`, `ApplyCompaction` and
  `Uncompact` return `ErrBusy` while a turn runs, and
  `ErrAwaitingApproval` while requests are pending. `Create` and `Open`
  validate the knobs (`ErrCompactConfig`: `Reserve` below the window,
  `KeepRecent` below window − `Reserve`). `Pin` refuses an entry that
  carries no message (`ErrNotPinnable`).
- **A custom `Trimmer` may only replace tool-result content**; any
  other change fails the trim and the summary runs.
- **An overflow that is re-run leaves two turn entries** (the failed
  attempt's, then the re-run's); code counting turn entries per `Send`
  reads `TurnEntry.ReRun`.
- **A non-user steer** is refused by `Send` with an error wrapping
  `weft.ErrInvalidSteer`.
- **Pool**: the bound is per `Pool` value; a delegation deeper than
  `MaxDepth` is refused (`SUBAGENT_DEPTH`), a real cycle with
  `SUBAGENT_CYCLE`; `Decide` no longer waits for the child; a resumed
  child queues for a slot; children inherit the parent's
  `RequestExpiry`, `Clock`, `Quorum`, keyring and `RequireSigned`;
  after a restart call `p.Recover(ctx, parent)`.
- **`Fork`** takes its own options (`WithMeta`, `PublicID` and
  `WithLineage` were dropped) and inherits nothing from the origin's
  header or options, with one exception: a fork of a `RequireSigned` session
  requires signed decisions too and takes the origin's keyring when
  given none. Copied queued steers are recorded as dropped, mirrored
  child requests are left out, unsettled pool receipts are settled
  canceled.
- **`jsonl.Open` fails on a platform with no file lock** unless
  `thread.NoLock()` is passed (it used to run unlocked, silently).
- **`thread/sqlite`**: migration 0003 runs on `Open` and is one-way —
  a binary from before it refuses the database
  (`sqlite.ErrNewerSchema`). The module needs the `thread` release
  that carries `thread/backend`.
- **`thread/sqlite` durability**: the fsync policy is now SQLite's
  `synchronous` level. `FsyncEveryAppend` (the default) is
  `synchronous=FULL` — an entry survives a power cut; the backend used
  to run `NORMAL` whatever was asked. `FsyncOnFlush` is `NORMAL` and
  `Flush` checkpoints the log. Expect slower appends on the default.

### Wire

Everything is additive except the trim record; no entry kind was
added and the header envelope is still `"weft": 1`.

- **`compaction` entries carrying a trim record are written with
  `"v": 5`** (`trim: {stubs: [{entry, call_id, content, is_error}]}`;
  golden `testdata/format5/compaction_trim.json`). An older build
  fails on such an entry with `ErrNewerFormat` — on purpose: it would
  replay the trim from its own options. A summary compaction is
  unchanged and carries no `"v"`. A trim entry written before this
  version (no record) is read as it was.
- `message`: `run_id` on a turn's prompt entry.
- `turn`: `policy`, `rerun`, `late_steps`; `canceled` now also set on
  a deadline.
- `receipt` (`"v": 3`): the status `accepted` with `msg`, `turn` and
  `run_id` — a queued send; `unanswered` on a delivered receipt. A
  build from before `accepted` reads the entry and does not restore
  the send.
- `pool_receipt` (`"v": 4`): the status `parked`
  (`testdata/format4/receipt_parked.json`).
- `approval_decision` (`"v": 2`): `request_id`, `always`; new `via`
  values `interrupt`, `child`, `parent`.
- `approval_audit` (`"v": 2`): `grant_id`, `grant_shared`, `key_id`,
  `decisions`; the step `signed`; the `resume` step's outcomes
  `started`, `completed`, `failed`.
- Header metadata key `weft.require_signed` (`"true"`). A build from
  before it ignores the key and does not enforce the rule.
- The signing challenge's domain string is
  `weft/approval-challenge/v2`, covering the request entry id and the
  run id. Not stored; in-flight v1 signatures do not verify.
- `thread/sqlite` migration `0003_leases_keyset`: `session_locks`
  gains `process` and `started`; `sessions` gains `gen` and `envelope`;
  the index `sessions_order(envelope, created DESC, id DESC)` replaces
  `sessions_created`; the title column is re-derived under the
  last-non-empty rule.
- Grant uses are counted from `approval_audit.grant_id`. A file whose
  uses were recorded only as prose (`detail`) is not counted: a
  `MaxUses` grant in such a file starts again.

### Model-visible changes (AGENTS rule 5)

Pool — every string is pinned by a test (ADR 0022 amendment §I):
- `SUBAGENT_DEPTH: delegation depth <n> exceeds the pool's limit <m>`
  — new; the depth refusal used to read `SUBAGENT_CYCLE`.
- `SUBAGENT_CYCLE: …` only for a real cycle.
- `SUBAGENT_CANCELED: agent "<name>" was canceled before it finished`
  — new, when `Cancel` or `Close` ends a sync child.
- `SUBAGENT_FAILED: agent "<name>" parked at an approval the pool could
  not surface: <cause>` and `SUBAGENT_FAILED: agent "<name>" failed:
  <cause>` (a failure outside a child run, or recovered from the
  ledger) — new.
- `DENIED: the delegation was canceled while awaiting approval` — what
  a canceled, parked child's model reads if its session is resumed.
- A delegating call is no longer re-executed by a decision; its result
  is the child's answer, once.

Turns
- After a mixed tool batch resumes, the model sees one complete tool
  message for the step. It used to see the partial one, with the
  parked call unanswered.
- A step whose write failed is no longer shown to the next turn as an
  interrupted call beside a duplicated message.
- A retried `Send` after a failed prompt flush shows the user message
  once.

Approvals
- No text changed. One case moved: a request that expired, or was
  interrupted, after it already held an approval is denied with the
  expiry or interrupt reason, not "conflicting decisions".

Compaction
- A recorded trim shows the same stubs under any options; a custom
  `Trimmer`'s stubs now reach the model; the first summary survives a
  trim; a truncated summary is never shown; `BeforeCompact`'s edits
  shape the summarizer's input; a branch summary made with
  `SummarizeLeft` is built from the compacted view. The marker, the
  stub text and the summary prompt are unchanged.

Session core
- A context is never built across a broken parent link (the file is
  refused); a salvaged session's context starts at the orphaned entry.

### Known limits

- **Windows is unexecuted.** jsonl's `LockFileEx` lock and sqlite's
  Windows liveness and start-time checks compile and pass
  `GOOS=windows go vet` (run by hand; CI has no Windows job). No test
  has ever run them.
- **Stale-writer detection is a count and the header's creation
  time**, not a content comparison of the entries.
- **sqlite's lock does not cross hostnames on its own.** A lock row
  left by a holder with another hostname is never taken over; a
  replacement container with a new hostname calls `sqlite.BreakLock`
  (`docs/thread-operations.md` §4).
- **`Recover`** never re-runs a child; reads a finished child's answer
  as its last assistant text (a structured `Output` answer is not
  recovered as such); cannot rebuild the agent ancestry above a
  rebuilt child (the depth limit still holds); and assumes one pool
  owns a storage's delegations.
- **`Audit()` is an index, not evidence**: entries are unsigned and
  unchained; whoever can write the storage can change them.
- **`s.Grant` is an unsigned, in-process call** under `RequireSigned`
  too.
- **The steer queue is unbounded**; an application that must cap what
  users pile onto a running turn checks `len(s.Queue())`.
- **jsonl `List` reads every header per call**; sqlite pages in SQL.
- **Session plumbing for the pool is still exported** on `Session`
  (`thread/poolplumbing.go`), and the option names still mix three
  dialects (`With*`, bare, `Require*`/`On*`); both wait for the
  freeze.

## obsdb 0.1.1 / obsdb/clickhouse 0.1.1 / otel 0.1.1 / studio 0.3.1 / studio/cmd 0.1.1 / runtime 0.1.1 — 2026-10-02

Patch releases for the fixes found after the 0.7.0 programme, tagged in
dependency order behind `thread/v0.9.0`: obsdb, then obsdb/clickhouse
and otel, then studio, then studio/cmd and runtime (which requires
thread v0.9.0). The root module is unchanged at v0.7.0.

### obsdb

- `DeriveSpan`/`DeriveRecord` read a numeric string attribute as the
  number it spells: thread mints `weft.turn` through metadata
  (`strconv.Itoa`) and the core stamps every metadata value as
  `attribute.String`, so the real chain delivered `"3"` — a spelling
  `attrIntOr` rejected, and every studio/obsdb run row read turn 0
  (sqlite never set the column; the recorded "thread off-by-one"
  diagnosis is refuted, the mint was always correct). Numeric
  spellings still read as before; a non-numeric string stays the
  default. ClickHouse already parsed the string via `toInt32OrZero`
  in its views — now pinned by a live test too. The pushed
  `obsdb/v0.1.0` and `obsdb/clickhouse/v0.1.0` tags carry the
  Go-side read bug (fixed by this patch tag).
- `Session(id)` (sqlite and clickhouse) reads the session's own
  grouped row directly instead of scanning the newest 500 sessions —
  a session older than the newest page 404'd in the detail while the
  list still showed it. Pinned on both backends
  (`TestSessionBeyondNewestPage`: 502 sessions, the oldest resolves).

### studio

- The step 8b routes no longer escape S4.6's panel-token scoping rule
  (programme audit P1-2): the fixtures export (`POST
  /api/playground/fixtures`) scopes by public id like every run-id
  route; the runtime link (`/api/runtime/register|commands|acks`,
  mounted behind a new server-identity guard via
  `RuntimeServer.MountGuarded`) and the breakpoints control refuse
  panel tokens outright — they are server-to-server and not
  public-id-shaped (register could overwrite a victim runtime's
  registration, the commands stream could replace its feed, acks
  could forge the state steer/approval routing trusts); steer now
  mirrors the approval route's fallback (the db row's public id; a
  run with no public id is outside every panel token). Pinned by
  `TestStep8RoutesRefusePanelTokens` (read-scoped token → 403 on each
  surface; the server token keeps working). The pushed `studio/v0.3.0`
  tag carries the gap (fixed by this patch tag).
- The web app's run page sends the bearer token on its paged events
  walk (the one raw fetch without it — under setups B/C every page
  401'd and the run page showed the error instead of the story);
  pinned by `use-run-events.test.tsx` (P1-3).
- The panel follows `next_after` past a terminal full page in all
  three event walks (`done` only means the run ended — a finished
  run with more than one page of events silently lost the rest);
  pinned by a panel test with `done:true` + `next_after` set (P1-5).
- The panel's experiment drawer omits an unchanged instructions
  override (the drawer pre-fills the registered prompt, and the
  scripted engine 400s on an instructions override — agents that
  register instructions could never run scripted); pinned in
  `playground.test.ts` (P1-8).
- CORS `Access-Control-Allow-Methods` includes `PUT` — the breakpoints
  control goes through the panel's `panelPut`, and the preflight
  failed it in cross-origin setups B/C; the CORS pin asserts the verb
  (P1-9).
- The runtime link's full-feed drop terminates the stalled stream
  (the feed channel closes; the SSE ends instead of pinging forever
  while every POST 503s on the nil feed) — `TestFullFeedEndsStalledStream`;
  and a late accepted-ack that resurrects a row the lost sweep took
  arms the finish watch, so a runtime that never finishes cannot leave
  it accepted forever — `TestLateAcceptedAckArmsFinishWatch`.

### otel

- `weftVersion()` reports v0.7.0 — the release step that owns the bump
  missed it (its own comment says so), so every span carried
  `weft.version=v0.6.0` past the release. The pushed `otel/v0.1.0` tag
  carries the stale string (fixed by this patch tag).
- The drop counter's logger is fixed at construction
  (`newDropCounter`): `dropped()`'s lazy `d.log` assignment wrote a
  plain field outside the atomics, a data race under concurrent agent
  runs (the existing WARN-throttle test covers the behaviour; tests
  now build the counter through the constructor).
- Content-on chains shape every content class the core's own
  `StripContent` table names: `Steered` message texts are redacted
  like other user text, `RunFinish.Pending[].Args` follows the
  adjudicated `ToolStart.Args` rule (redact-not-cap), and `Nested`
  recurses into the child event (its cut propagates to
  `weft.content.truncated_bytes`). With `ContentConfig.Redact`
  configured, steered user text and pending-approval tool args no
  longer ride unredacted to Studio/Local. Pinned by
  `TestShapeEventRedactsSteeredPendingNested`.

### runtime

- Registration reports `weft_version: v0.7.0` — the same missed bump
  as otel's. The pushed `runtime/v0.1.0` tag carries the stale string (fixed by this patch tag).

### CI / repo

- ci.yml: the `apidiff.sh "" store` step is gone (the store module was
  deleted; the script exits 2 for any module but root and thread, so
  the first CI run after push would have failed), and the gated
  ClickHouse job from `obsdb/clickhouse/README.md` runs the
  conformance suite (plus studio/cmd's hosted-backend test) against a
  `clickhouse-server` service container.
- `studio/web/dist-release/` — where `make studio-panel-asset` stages
  the release asset — is gitignored, so a staged release no longer
  shows as an untracked stray.

### studio/cmd

- The dev token the banner prints is the token the API wall checks:
  `serve` resolves the token once (new `serveBoot`) and hands the one
  value to both the wall and the banner. With no `--token` and no
  `WEFT_STUDIO_TOKEN` the old code drew two independent generated
  tokens, so the printed token 401'd against every `/api` call —
  setup B's documented "token printed at start" hand-off was broken.
  The pushed `studio/cmd/v0.1.0` tag carries the bug (fixed by this patch tag).
- SIGINT/SIGTERM shut the server down gracefully (the listener closes,
  in-flight requests get five seconds, streams that outlive the window
  force-close, then the studio's resources close) — a bare
  `ListenAndServe` cut SSE streams mid-frame and skipped `srv.Close`;
  and the banner no longer echoes a DSN's password
  (`clickhouse://user:***@host`). Pinned by
  `TestListenShutsDownGracefully` and `TestDBLabelMasksPassword`.

### obsdb/clickhouse

- Paging cursors compare in integer nanoseconds
  (`toUnixTimestamp64Nano` with an int64 bind): the driver renders a
  positional `time.Time` bind at whole-second scale, so the runs
  cursor (`Started < ?`) and the sessions cursor
  (`max(LastSeen) < ?`) floored S.<nanos> to S.000 and silently
  skipped every row in the same second before the boundary — rows
  lost at essentially every page boundary. The status cutoff
  (`LastSeen >= ?`) binds nanoseconds for the same reason (no more
  sub-second running band). Pinned by `TestCursorSubSecondPaging`
  (two runs/sessions within one wall second, paged through the
  boundary). The pushed `obsdb/clickhouse/v0.1.0` tag carries the bug (fixed by this patch tag).
- `SaveExperiment` surfaces every prior-read error except
  `ErrNotFound` (only a genuine not-found means "first save"): a
  transient read failure no longer silently resets an update's
  `Created` to the save time. Pinned by `TestExperimentCreatedSurvivesUpdate`.
- Migration 0002's engine shape is pinned offline
  (`TestSpecExperimentsEnginePresent`): `ReplacingMergeTree(InsertTime)`
  and `InsertTime DEFAULT now64(9)` — the two properties the step 8b
  review fixes rest on, previously guarded by nothing offline.

## 0.7.0 — 2026-10-01

The step 8b playground programme (ADR 0024, WEFT-PLAYGROUND.md P1–P5):
`ReplayPolicy` in the core, the playground's P1–P5 verbs across the
runtime link, Studio and the devtools panel, the experiments table in
obsdb, and the debugger rungs 3–4 (breakpoints, steer) on
runtime-started runs. Root is additive; the lanes' modules below
release and date here with it (the step 8 release, 2026-10-01).

### weft 0.7.0

#### Added

- `weft.ReplayPolicy` — a tool's side-effect class for re-runs
  (WEFT-PLAYGROUND.md §6 rule 3): `ReplayNever` (the zero value, and
  what an unannotated tool counts as — its calls are substituted with
  the recorded result or parked, never silently re-fired) and
  `ReplaySafe` (idempotent, side-effect free — a re-run may execute it
  for real). Set with `weft.Replay(weft.ReplaySafe)` beside the tool's
  other options; read back with `ToolDef.ReplayPolicy()`. The manifest
  records `replay_policy: "safe"` — never is the default and renders
  exactly as before, so no committed weft.json churns. This is the root
  change step 8b needed: the runtime link's register payload now
  reports each tool's real class instead of the honest-all-never
  placeholder of step 8a.

Lanes A2, B1, B2, C1 and C2 of the observability-data programme (ADR
0024), merged to main: the two new modules from A2 (the observability
database and the pipeline), B1's Studio rewrite on obsdb with the
store module deleted, B2's ClickHouse backend, C1's devtools panel,
and C2's `weft/runtime` module with the playground API. All of it
dates at the step 8 release.

### obsdb (new module)

- **The observability database** (ADR 0024 S3): the OTLP-shaped model
  (`Span`, `Record`, `Batch`), the derived weft identity (`Weft`,
  `DeriveSpan`/`DeriveRecord`), the `DB` interface (runs, sessions, the
  positioned event page with its gap detector, the transcript replay
  reads, spans by run or trace, public-id resolution), `DeriveStatus`
  (the four-row table: error span → failed; run_finish → succeeded;
  fresh last-seen → running; stale → interrupted, after
  `InterruptedAfter` = 30 s), and the live-lane hub (`Hub`, `Frame`,
  one hub-wide monotonic `Seq`, bounded per-subscriber queues, overflow
  drops the subscriber).
- **`obsdb/sqlite`**: the default backend — the S3.4 schema
  (`obsdb_migrations`, `spans`, `records`, `other_logs`, `runs` and the
  indexes), one writer connection plus a read pool, `Open(path,
  KeepDeltas())`. `Write` is one idempotent transaction: `INSERT OR
  IGNORE` on (run, kind, pos) and (trace, span); deltas counted and
  never stored (their own counter, so their absence never looks like a
  lost event); heartbeats never stored, they only move last-seen; a
  reordered batch's provisional start corrected when `run_start` lands.
  The returned DB implements `Hub()` and publishes every Write's frames
  before returning.
- **`obsdb/obsdbtest`**: the conformance table every backend runs (the
  storetest pattern) — round trip, idempotence, reordering, gaps,
  status at every boundary including the crash, sessions and public
  ids, children, paging cursors, the delta rule, the heartbeat rule,
  non-weft spans and records stored and returned by `Trace`.
- **`obsdb.FromOTLPTraces` / `obsdb.FromOTLPLogs`**: OTLP/HTTP export
  requests decoded into the model (scalars verbatim, arrays as `[]any`,
  kvlists as maps, bytes as base64), with golden protobuf and JSON
  fixtures under `obsdb/testdata` that step 6's ingest and the SDK path
  both pin against.

### otel (new module)

- **`otel.Install(...)`** — one line, several destinations at once, all
  active: `Local(path)` (the obsdb/sqlite sink, written synchronously,
  content on), `Studio(url, token)` (OTLP/HTTP protobuf, bearer token,
  logs 200 ms / spans 1 s batches, content on), `Datadog()` (the local
  Agent's OTLP intake on `localhost:4318`, content off), `Langfuse(host,
  pk, sk)` (`<host>/api/public/otel`, Basic `pk:sk`, traces only),
  `OTLP(url)` and `Exporters(spans, logs)` (content off). The returned
  function flushes and shuts down; call it on exit
  (`defer otel.Install(...)()`). With no options it writes the local
  sink only. `otel.Start` is Install with errors (and `NoGlobal` for
  tests); Install never panics or fails the program.
- **Per-destination content** (`WithContent(cfg…)`, `NoContent()`,
  `Signals`, `NoDeltas()`, `BatchDelay`, `Headers`, `Timeout`,
  `Insecure`, `DatadogEndpoint`): content-off chains clone each record,
  strip it with `weft.StripContent`, mark it `weft.content=stripped`
  and drop `messages` records; content-on chains apply the
  destination's `Redact` and `MaxBytes` (event and delta bodies only —
  never the transcript) and set `weft.content.truncated_bytes` when a
  cap cut. Every processor on the provider answers the Logs API's
  `Enabled` by event name, so a content-off-only pipeline makes the
  core emit no `messages` records at all.
- **Heartbeats**: the run tracker keeps the open runs (from
  `run_start`/`run_finish` records and the `invoke_agent` span end) and
  emits one `weft.heartbeat` record per open run every interval
  (`Heartbeat(d)`, default 10 s, 0 disables) — a long quiet tool call
  reads running, a crashed process stops heartbeating, and the sinks
  never store a heartbeat row.
- **`otel.FromSDKSpans` / `otel.FromSDKRecords`**: SDK data into the
  obsdb model, value-identical to the OTLP path (pinned against
  obsdb's golden fixtures; the SDK's status codes map onto OTLP's
  numbering).
- The standard variables configure extra destinations
  (`WEFT_STUDIO_URL`, `OTEL_EXPORTER_OTLP_ENDPOINT`/`_HEADERS`,
  `OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT`, `WEFT_DB`);
  explicit options and env destinations combine, same-URL duplicates
  de-duplicate with the explicit one winning; `NoEnv()` turns them off.
  The core reads no environment variable — this module does, here
  only.

### obsdb/clickhouse (new module)

- The ClickHouse backend of `obsdb` (ADR 0024 S3.6, open-source per
  Q5): `Open(dsn string, opts ...Option) (obsdb.DB, error)` connects
  through clickhouse-go v2 with `async_insert=1,
  wait_for_async_insert=1` (a write returns once the server has
  flushed its batch), creates and versions the schema in
  `obsdb_migrations` — the SQLite backend's numbering rule, refusing a
  database whose schema is newer with `ErrNewerSchema` — and serves
  every `obsdb.DB` read (`Runs` through `GROUP BY RunId` with the same
  aggregates the AggregatingMergeTree columns carry, single-run pages
  through `FINAL`, `Sessions` as a `GROUP BY SessionId` over
  per-run-collapsed rows, status always derived through
  `obsdb.DeriveStatus`, never stored).
- Column compatibility with the OTel Collector ClickHouse exporter,
  pinned at **v0.162.0** (the version named in
  `migrations/0001_init.sql`): `otel_traces` and `otel_logs` keep
  every column name and type its `INSERT` names — `EventName`
  included (the one feature column the exporter probes the table
  for); the optional `*AttributesKeys` columns deliberately absent
  because the pinned exporter's default `INSERT` never names them
  (they belong to its json-mode tables) —
  and add the weft identity as materialized columns with
  `bloom_filter` skip indexes on `SessionId`, `PublicId`, `Agent` and
  `TraceId` (S3.6). A stock collector pinned to that version, with
  `create_schema: false`, writes into the same database; the
  collector-shape test inserts with its verbatim template column
  lists and proves the views feed the weft tables from those rows.
- `weft_records` as `ReplacingMergeTree(InsertTime) ORDER BY (RunId,
  Kind, Pos)` — the transport idempotency key (I4) — filled by a
  materialized view from `otel_logs` where `weft.record IN ('event',
  'messages')`: never `delta` (counted in `weft_runs`' `DeltaCount`
  high-water mark, never stored — D3/Q4), never `heartbeat` (no
  position; it only moves a run's last-seen). It carries its own
  content TTL, because a source table's TTL does not cascade through
  a view.
- `weft_runs` as `AggregatingMergeTree ORDER BY RunId` filled by two
  materialized views (from `otel_logs`, heartbeats included for
  last-seen, `run_start`/`run_finish` bodies parsed in SQL; and from
  `otel_traces`, the `invoke_agent` span detected exactly the way
  `obsdb/sqlite`'s `isInvokeAgent` does), its columns
  `SimpleAggregateFunction`: `min` for started, `max` for finished,
  last-seen, identity strings, the terminal flags and usage. Caller
  metadata is stored as the contract-filtered attribute JSON (the
  `obsdb.MetaOf` exclusion set, embedded in the view and pinned to
  `MetaOf` by a test), so `max` keeps real metadata and the metadata
  contract stays in one implementation.
- `TTL(content, meta time.Duration) Option`: overrides the retention
  windows — content (`otel_logs`, `weft_records`, `weft_deltas`) 30
  days, spans and runs 90 by default. Open applies a configured
  non-default window with `ALTER TABLE ... MODIFY TTL` after the
  migrations (idempotent, so the schema stays inspectable); the
  defaults are the migration's own and skip the ALTER. Values ≤ 0
  keep that class's default.
- `KeepDeltas() Option`: turns delta storage on for debugging —
  `Write` additionally inserts delta rows into `weft_deltas`
  (`ReplacingMergeTree` on `(RunId, Pos)`, their own counter so they
  can never touch the durable sequence); the option lives in `Write`,
  not the schema, because a view cannot be option-gated.
- The conformance table `obsdbtest.Run` (S3.5) runs against a live
  server, gated on `WEFT_CLICKHOUSE_DSN`, each subtest on a fresh
  database; the package README carries the one-line container recipe
  (clickhouse/clickhouse-server:25.8-alpine), the docker-compose
  equivalent, and the CI job with the service container.

### runtime (new module)

#### Added (step 8b)

- The playground's P1–P5 verbs over the link: `transcript_edits`
  (D2/D3 — a patch must name a call in the kept prefix, a rewrite may
  not drop a step's calls, the patched prefix must leave no call
  without a result and end at a step boundary, validated on both
  sides); `side_effects` `substitute` (the default: a parked call that
  matches a recorded call is answered with the recorded result over a
  chain of fresh run ids, the handler provably never re-fires; a miss
  stays parked), `park`, and `allow` (the opted-in set); the scripted
  engine (§5.5) — its own `weft.Model` over the source run's messages
  records, keyed like wefttest's fixtures, zero tokens, "no recorded
  turn" on a key miss, and the prompt trap closed (scripted +
  instructions/model override refused on both sides); fork mode (§5.4)
  — `Fork` at the source turn, the input becomes the fork's next turn,
  the fork stays open to the runtime so the panel can keep chatting;
  approval decisions on parked runs (ADR 0007's Approve/Deny/Resolve
  forwarded as `event: approve` commands); breakpoints (§8.3 — the
  stored tool set parked on every run this runtime starts) and steer
  (§8.4 — every ephemeral run carries a `weft.Steering` source; a fork
  in flight steers through thread's Steer policy).

- **New module `weft/runtime`** (WEFT-PLAYGROUND.md §10.2, ADR 0024
  [D6]): the playground's in-app side. One deferred call is the whole
  integration — `defer runtime.Install(
  runtime.Studio(url, token), runtime.Agents(...), runtime.Models(...),
  runtime.Limits(...), runtime.AllowSideEffects(...),
  runtime.Threads(store), runtime.Enabled(true))()`. Without Install
  nothing opens; even with it, only under `WEFT_ENV=dev` or
  `Enabled(true)` (§6 rule 1). The default endpoint is
  `otel.StudioEndpoint()`.
- The runtime link (§10.3): registers on connect and after every
  reconnect (manifests, model allow-lists, per-agent caps, budget,
  side-effect classes — all tools `never` until `ReplayPolicy` lands),
  receives commands over SSE with `Last-Event-ID` resume, acks every
  command **before** executing it (at-most-once: repeated ids
  ignored), handles `cancel` and `ping`. `runtime.Local(srv)` talks to
  the embedded Studio in-process — no socket.
- The executor (§5.2): `engine: live` + `thread: ephemeral` only
  (fork, scripted and `transcript_edits` are 8b); composes the run
  from plain RunOptions (`Instructions`, `OnlyTools`, `UseModel`,
  `Thinking`, lower-only `MaxSteps`/`Parallelism`, `temperature`,
  `ParkOn` for every tool not opted in with `AllowSideEffects`), and
  labels the run `weft.playground`, `weft.playground.command`,
  `weft.experiment.id`, `weft.forked_from`, `weft.public_id`,
  `weft.playground.actor` — never `weft.session.id`. Source
  transcripts resolve thread storage → local obsdb →
  `GET /api/runs/{id}/transcript`, cut at the `from_step` boundary; a
  whole-turn re-run that sends no input defaults its prompt to the
  source's own user message (§5.1: input *replaces* the turn's user
  message — the original exists by default).
  The runtime re-validates tool/model names and limits against its own
  registry and answers `rejected` when Studio's copy disagrees;
  per-experiment budget caps are counted from each command's own
  usage, a breach rejects the next command (`budget_exceeded`), and
  the app's own runs are never touched.

### studio 0.3.0 (breaking — the step 6 rewrite, the devtools panel, the playground)

#### Added (step 8b)

- The playground's P1–P5 in both surfaces: the panel's experiment
  drawer (§3 — registered-config pre-fill, tools off with the
  side-effect warning, model, thinking, input, engine, side-effect
  mode, thread mode `ephemeral | fork`, transcript edits on the kept
  steps, and the rung-3 breakpoint set), the result streaming in
  place labelled t·xN with the inline diff (the shared `lib/diff`),
  continue/skip/resolve on a parked experiment run
  (`POST /api/runs/{id}/approvals`, routed to the runtime that started
  it — the app's own turns are 403 there, viewer-only per PQ7), the
  2-way sibling compare (PQ3), and the saves (`keep as prompt` copies
  the text, PQ2 is post-v1; `save as fixture` hands off to Studio).
- The Studio playground (`/playground`, capability-gated): the split
  view with the variant switcher, per-variant runs side by side with
  their metrics (tokens, latency, tool calls), the pairwise diff and
  the compare table, E9's variants × inputs matrix (the definition
  saved via `POST /api/experiments`, every cell issued under the
  experiment's id so the budget caps the whole matrix), and the
  experiment history. The config column carries the thread mode and,
  on a continued run, the kept steps' transcript edits; the run card
  steers an in-flight run and decides parked calls (§2's parity rule —
  every verb renders in the panel or Studio, never neither). The
  panel hands off into it with run, step and
  the current overrides as query params.
- `POST /api/playground/fixtures`: a run's records as wefttest replay
  fixtures — wefttest's own file shape, key and naming, pinned by a
  round-trip (the files load under `wefttest.Replay` and answer
  byte-for-byte).
- The debugger's rungs 3–4 (WEFT-DEVTOOLS §8.3/§8.4):
  `PUT /api/runtimes/{id}/breakpoints` (capability `breakpoints`) and
  `POST /api/runs/{id}/steer` (capability `steer`), both acting on
  runtime-started runs only — `meta.debug_scope` says so, and both UIs
  repeat it (PQ7). The controls render only for the reported
  capabilities: the panel's drawer carries the breakpoint set (the
  rung-3 gate's "all in the panel"), Studio steers the in-flight run —
  both verbs in both surfaces' reach, §2's parity rule.
- obsdb: the `experiments` table (§10.4, PQ4) behind
  `SaveExperiment`/`Experiments`/`Experiment` on both backends (sqlite
  migration 0002; clickhouse 0002 as a ReplacingMergeTree), with
  `RunQuery.ExperimentID` selecting an experiment's runs and an
  obsdbtest conformance subtest.

#### Added

- `New(opts ...Option) *Server` with `Handler()`, `Close()` (closes
  only the database New opened itself — a DB passed through `DB(...)`
  stays its owner's) and `Runtime()` (the runtime link's in-process
  side — `studio/runtime` — when `Playground(true)` built it, else
  nil). Options: `Live(hub)` (default the DB's own hub
  when it implements `Hub()`, else an in-process `obsdb.NewHub()` fed
  by ingest), `NoIngest()`, `IngestToken(tok)`, `Token(tok)`,
  `AllowOrigins(...)`, `Playground(bool)` beside the kept `DB`, `Open`,
  `Base`, `Manifest`, `Title`, `Capabilities`.
- `studio/ingest`: the OTLP/HTTP receiver — `POST /v1/traces` and
  `POST /v1/logs`, protobuf and JSON, optional gzip, a 16 MiB limit
  after decompression (413 above), the publish-then-write pipeline
  (frames reach the hub before `DB.Write`; 503 `unavailable` on a write
  failure so the exporter retries), and the ingest token (loopback
  open without one — `api/meta` says so via `ingest_open`).
- `GET /api/live`: the SSE live stream — exactly one selector
  (`run`/`session`/`public_id`/`agent`), `kinds` over
  event/delta/messages/run (default `event,run`; heartbeats never
  forwarded), frame ids are the hub's Seq, `Last-Event-ID` resume with
  the gap backfilled from the database and deduped on
  `(run, kind, pos)`, a ping every 15 s, and `event: overflow` plus a
  close when a subscriber's queue drops it.
- Routes: `GET runs/{id}/transcript`, `runs/{id}/spans`,
  `traces/{trace_id}` (any trace, weft or not), `sessions`,
  `sessions/{id}`, `public/{public_id}`, `POST /api/panel-tokens`
  (mint an HMAC-signed `{public_id, scope, exp}` scoped to one public
  id; every data route refuses anything outside it, the agent live
  selector included). Runs and sessions lists gained the session,
  public-id and (runs) playground filters.
- `routes.go`'s route groups: the registration point the panel and
  the playground hook into from their own files (`panel.go`,
  `playground.go` set package-level hooks through var initializers;
  `Playground(true)` enables the playground's), with `api/meta`'s
  capabilities computed from the registered groups — never hard-coded.
- The devtools panel `<weft-devtools>` (WEFT-DEVTOOLS.md §5): a
  self-contained custom element built as a separate Vite library-mode
  artifact (`studio/web/vite.panel.config.ts`, committed at
  `studio/dist/panel/panel.js`, 63,475 B raw / 17.0 KiB gzip — budget
  80), sharing `lib/api.ts`, `lib/live.ts`, `lib/events.ts` and
  `lib/format.ts` with the Studio UI (V6, no React in the bundle).
  Rung 1 (§8.1): the header, the turn list scoped by public id with
  `parked` and the (inert-until-step-8) experiment slot, the turn view
  through the shared fold with reasoning collapsed, tool calls
  `name(args)` → result with truncation badges and span times, usage
  with cached/reasoning splits, read-only approvals, the honesty
  rules (interrupted, gaps, stripped content, max_tokens), the raw
  JSON toggle, the live tail with deltas, ⤢ deep links carrying run
  and step, lazy subagent expansion, a spans waterfall, and the §5.2
  keyboard (Alt+W primary — Q4 closed; Ctrl+Shift+W where delivered).
  The live tail upserts `run` frames by id (T20's fix) — one row per
  run, the newest state winning.
- `GET /panel.js` on `studio.Handler` (S4.2): the embedded bundle,
  static and unauthenticated, registered through routes.go's
  panel-group hook (always on, no capability of its own; `live` is
  what rung 1 gates on). A build without the bundle answers 500 in the
  API error shape.
- Panel mounting per §5.2/§5.3: `data-endpoint` / `data-public-id` /
  `data-token` / `data-position` / `data-open` / `data-auto` on the
  script tag or the element, the `window.__WEFT__.publicId` watch by a
  setter, `?weft=debug` and `localStorage.weft_debug=1` overrides, the
  studio-version check ("Studio is newer than this panel; update
  panel.js"), and fail-silent removal when `/api/meta` does not answer
  (one request, no retries, no console output).
- `studio/web/scripts/panel-gate.ts`: the Dv0–Dv2 gate driver (jsdom
  over a live Studio, driving the committed bundle — the self-hosting
  harness page is `examples/studio-local`'s `/`), and
  `scripts/panel-asset.ts`: stages `panel-<version>.js` + sha256 as
  the release asset for non-Go backends (V2; wired as `make
  studio-panel-asset`).
- **New package `studio/runtime`**: the runtime link's server side —
  `POST /api/runtime/register`, `GET /api/runtime/commands` (SSE),
  `POST /api/runtime/acks`, the registry of connected runtimes with
  `last_seen`, and §10.5's lost-command timers (30 s unacked; queued
  lost at disconnect; accepted lost after 10 min without a finish; a
  late ack still lands).
- **`studio/playground.go`** (routes.go's `playgroundGroupHook`,
  enabled by `Playground(true)`): the playground API — `GET
  /api/runtimes`, `POST /api/playground/runs`, `GET
  /api/playground/commands/{id}` — with §10.4's validation table (400
  unknown tool/model name, `input` with `from_step > 0`, and the
  8b-deferred modes answered "not yet available"; 403 raised limits,
  a refused side-effect tool, a panel token out of scope; 404 unknown
  runtime/agent/source run; 409 a reused command id; 503 no connected
  runtime). Capabilities `playground` and `runtimes` appear in
  `GET /api/meta` when `Playground(true)`.

#### Changed

- `Handler` is `Handler(opts ...Option)` over an `obsdb.DB` instead of
  `Handler(s store.Store, opts ...)`: `DB(db)` serves the database you
  pass (setup A: `DB(otel.LocalDB())` — the same handle weft/otel's
  Local destination writes), `Open(path)` opens an obsdb sqlite file
  (created when missing; panics at Handler time when it cannot), and
  with neither option Handler opens the history database at `$WEFT_DB`
  or `./.weft/weft.db`. The five read routes keep their JSON shapes as
  far as the new model allows: `tags` reads the run row's metadata,
  `model` rebuilds from the row's provider/model, `result` is always
  `null` (the store's result document is gone; the transcript route
  that replaces it is step 6), and events page through
  `obsdb.DB.Events` with the same inclusive `after` cursor, `done`
  now also covering rows that read interrupted at derivation time (a
  crash orphan's polling tail terminates).
- The JSON shapes are S4.3's: the run row carries the identity chain
  in full (`parent_run_id`, `trace_id`, `service`, `session_id`,
  `public_id`, `turn`, `playground`, `experiment_id`, `forked_from`,
  `meta`, `last_seen`, `pending`, `stop_reason`, `message_count`);
  `meta` reports `db`, `ingest_open` and `interrupted_after_ms`; event
  page entries carry `time` and the page carries `gaps`; spans map
  OTLP statuses to `unset`/`ok`/`error`. The UI follows: the live
  client (`lib/live.ts`), the transcript overlay on the fold, children
  joined by `parent_call_id` (subagent blocks fetch on expand), the
  time-axis waterfall when a run has spans, and the sessions, traces
  and live routes.
- `Token(tok)` now walls the whole API (bearer, or `?token=` for
  EventSource); CORS defaults to localhost/127.0.0.1 on any port when
  a token is configured, none in setup A.

#### Removed

- The store-era JSON keys `tags` (now `meta`) and `parent_id` (now
  `parent_run_id`), the dead `result` field on the run document, the
  inline `Nested` folding in the UI (children are separate runs), and
  `meta`'s `store` key (now `db`).
- `eventCache`, `storeKind`, everything typed on `store.RunRecord`,
  and the `weft/store` dependency; the 409 `newer_format` mapping went
  with the store's decode errors (unknown ids stay 404, other database
  errors 500 `internal`). `api/meta`'s `store` field reports the
  obsdb backend (best effort, dynamic type).

#### Notes

- The panel changes no public Go API beyond the bundle route:
  `panel.go` registers through the step-6 route-group hook; the panel
  token endpoints and CORS defaults were already step 6's.

#### Release asset

- `panel-v0.3.0.js` (sha256
  `29652795bc9ab349838bcad951b492af610363c5c1f8a6059c370acf78d672e4`
  beside it) — the devtools panel for non-Go backends; serve it from
  your app and add `<script type="module"
  src="/static/panel-v0.3.0.js" data-endpoint=… data-token=…
  data-public-id=…></script>`. Staged by `make studio-panel-asset`
  (RELEASE_DIR, default `dist-release` relative to `studio/web`);
  byte-identical to the committed `studio/dist/panel/panel.js`.

### studio/cmd (new module)

- Setup B's binary (S4.6, §10.1): UI + ingest + a dev token on
  `127.0.0.1:7331`, `--db sqlite://path` (an obsdb sqlite file,
  created when missing) or `--db
  clickhouse://user:pass@host:9000/db` (the hosted backend, wired at
  merge-B — this module is the one place that imports the
  obsdb/clickhouse driver, so the studio library never carries it),
  `--addr`, `--token`; `WEFT_STUDIO_TOKEN` fixes the token, else one
  is generated and printed. `DevToken()` generates it.

### store

#### Removed

- The module, entirely (step 5 of ADR 0024). Consumers read `obsdb`:
  weft-arena through per-scenario `weft/otel` pipelines
  (`weft.LoggerProvider`/`weft.TracerProvider` from
  `otel.Start(otel.Local(":memory:"), otel.NoGlobal())`, metadata via
  `weft.Metadata` on the run), Studio through `studio.DB`.
  `store/v0.1.3` remains resolvable from the module proxy for
  consumers pinned to it by tag.

## thread 0.8.1 — 2026-10-01

The step 8 release's lockstep tag: the module's only change since
0.8.0 is a comment reword in `thread/pool` (the store-era prose T18's
grep gate cleaned up), and the requirement moves to the tagged root
v0.7.0 (the two-phase rule, ADR 0005). Tagged so the step 8 train —
obsdb, otel, studio, runtime — releases against a current thread.

## thread 0.8.0 — 2026-10-01

The observability-data programme's thread step (ADR 0024 S5): the
session stamps its identity on every run it starts, and `thread/sqlite`
owns its migrations table so a session database can share one SQLite
file with the local sink. Additive for source (one option, one
unexported field); the migrations rename is the one breaking edge,
carried by a one-statement rename on Open. Tagged in lockstep with
`thread/sqlite/v0.2.0`, which bumps to weft v0.6.0 / thread v0.8.0 and
carries that rename; the module requires the tagged root v0.6.0.

### Added

- `thread.PublicID(id)` — the session's public id: an opaque,
  browser-safe handle (WEFT-OTEL-DATA-ARCHITECTURE §5), `WithMeta`
  sugar stamped into the header as `weft.public_id`. Create-time only,
  so it is what every backend's `List` Meta filter matches; a later
  `SetInfo` can add other keys but never rotates it.
- Every run a session starts — a send, a resume, an overflow re-run —
  carries `weft.Metadata` with `weft.session.id`, `weft.turn` (the
  counter the run id was minted from; the re-run names its new turn),
  `weft.public_id` when the session has one, `weft.session.forked_from`
  (`<session>#<entry>`) for a fork, and `weft.session.parent` (+
  `weft.session.parent_call`) for a pool child. It is appended after
  the caller's run options, so the session's keys win over a caller's
  colliding `thread.RunOptions(weft.Metadata(...))`; keys the session
  does not claim pass through. On spans and records alike (the core's
  S1.2/S1.3 wiring); the runs read `Session.Meta()` — the header
  overlaid with every info entry — while `List` keeps matching the
  header's create-time layer.
- The OTel API (`otel`, `otel/log`, `otel/trace`) becomes a direct
  requirement of the thread module (test-only imports; the versions
  the root pins). The SDK stays out — the identity tests implement the
  tracer and Logs API providers on the API's embedded types, the root
  module's stance.

### Changed

- `thread/sqlite`: the migrations table is `thread_migrations`
  (renamed from the goose-shaped `schema_migrations`), so a thread
  database and the local sink's `obsdb_migrations` can share one file
  with each module owning its versions (WEFT-OTEL-DATA-ARCHITECTURE
  §3.4). `Open` moves a pre-rename file across with one `ALTER TABLE`,
  run only when the `sessions` table exists and the new name does not:
  a `store` database pointed at the same `Open` keeps its own tracking
  table and rows untouched — including one whose recorded store
  version used to read as ahead of ours and refuse the open. Old
  thread files keep opening; the versions carry across and the
  migrations resume from the recorded number.

### Fixed

- `thread.Storage`'s `Query.Meta` doc claimed the `List` filter
  matched "the merged view `Load` returns"; no backend merges info-entry
  meta and `Load` returns the header as is (review 2026-09-30 §8.1
  item 4). Now says the header's create-time Meta. Doc only; behaviour
  unchanged.

## 0.6.0 — 2026-10-01

The observability-data programme's core step (ADR 0024): the run's
events, deltas and transcript leave the process as standard OpenTelemetry
log records, and the playground's per-run configuration joins the core.
Additive for source — the three dual options widen their return type to
a superset interface, the cycle's three allow-listed apidiff lines — so
root ships a minor.

### Added — observability data out (ADR 0024)

- The OTel Logs API (`go.opentelemetry.io/otel/log v0.22.0`) joins the
  trace API as the core's one dependency: no version moves (it requires
  exactly the pinned otel v1.46.0).
- `weft.LoggerProvider(lp)` selects the Logs API provider (default: the
  global, delegating, a no-op until an SDK registers).
- Every durable event (`run_start`…`run_finish`) and every delta is
  reported from `deliver` as an OTel log record on two counters —
  `weft.event.pos` contiguous from 0, `weft.delta.pos` for deltas — so
  dropping deltas never opens a hole in the durable sequence; `Nested`
  is not reported (the child run numbers its own). Bodies are the wire
  JSON (ADR 0004), stripped when capture is off; an errored
  `tool_finish` carries WARN; `run_start` carries the parent linkage,
  the manifest hash and `weft.version`.
- `messages` records at the transcript's five growth points — the
  repaired input at run start (index 0, never reported before), the
  tool message a resume creates or rebuilds (`attachResults` widened
  from created-only), each assistant message, each tool message, each
  steered batch. Their concatenation equals `RunResult.Messages`
  byte-for-byte; emitted only when capture is on.
- `weft.Metadata(kv)` (RunOption) and `weft.MetadataFromContext(ctx)`:
  caller pairs on every span and record of the run, inherited by
  subagent runs; limits (64 keys / 128 B key / 1 KiB value) drop and
  count (`weft.metadata.dropped`), never truncate.
- `weft.Content(bool)`, `weft.StripContent(ev)`, `ContentKind`: capture
  is resolved at each emission — the agent's option, else the standard
  `Enabled` question — and the core reads no environment variable.
- The run's identity on every span (S1.2): metadata verbatim plus the
  `gen_ai.conversation.id` / `session.id` / `user.id` mirrors; on the
  run span, `weft.parent.run.id` / `weft.parent.call.id` for subagents,
  `weft.manifest.hash` (computed at New for a named agent) and
  `weft.version`.
- `weft.version` rides at v0.6.0 and is pinned to a source
  (`version_test`): the const must equal the newest `v*` tag reachable
  from HEAD or the CHANGELOG's unreleased heading, so it cannot sit
  stale through a release again.
- A whole run with no tracer and no logger provider — the default
  program — is allocation-bounded (128 allocs/op measured, bound 160):
  the no-SDK path pays nothing for observability nobody asked for.
- `examples/otel` proves the emission end to end through the real SDK
  (an in-memory `sdk/log v0.22.0` exporter, a dependency of that
  example module only): every record correlates to the run's trace,
  event positions are contiguous, and the `messages` records rebuild
  the transcript byte-for-byte.

### Added — per-run configuration [D5, D7]

- `Instructions`, `MaxSteps`, `Parallelism` become dual Option/RunOption
  (the `Thinking` shape); per run the two limits may only lower — a
  raise is `ErrInvalidRunOption`, before any model call.
- The three allow-listed widenings (D9): the constructors return
  `InstructionsOption` / `MaxStepsOption` / `ParallelismOption` —
  superset interfaces every existing use as an `Option` still
  satisfies, which is what lets the same constructor work per run.
  The cycle's exact `.apidiff-allow` lines, reset at this tag.
- `weft.OnlyTools(names...)` narrows the run to named registered tools
  (unknown name → `ErrInvalidRunOption` before any model call);
  `weft.UseModel(m)` replaces the model for the run and rebuilds the
  WrapModel chain over it; `weft.ParkOn(tools...)` parks calls at the
  approval boundary as `RequireApproval` would (ADR 0007 applied per
  run). `Manifest` and `Tools()` keep reporting the static set.
- A changed configuration is recorded on the `invoke_agent` span:
  `weft.override.hash` (sha256 over the canonical JSON of every changed
  value) and `weft.override.*` per knob; absent on a plain run.

## thread 0.7.1 — 2026-09-30

The post-0.7.0 audit's one find (report:
`WEFT-POST-0.7.0-AUDIT-2026-09-30.md`), in the conformance harness,
not the session layer. No API change.

### Fixed

- `threadtest.CrashMatrix`: the steer and clear_queue points accept
  both legitimate durable orderings. A steer accepted while the second
  model call is in flight stays queued (receipt only), and a steer
  accepted in the between-steps window is drained into the next call
  and its message persisted after the receipt (ADR 0019's drain point,
  ADR 0011 §7). A `-race -count=10` soak on a loaded box tripped the
  old single-shape assertion. The clear_queue child no longer fails
  when the queue was already drained (n == 0 is a landing).
- The 0.7.0 entry's crash-matrix bullet now names all twelve crash
  points and the fourteen Append sites behind them.

## thread 0.7.0 — 2026-09-30

Hardening (plan §10): no new features. Every decoder fuzzed, every
session-layer write point crashed under kill -9 on both durable
backends, the operator-visible costs budgeted and enforced in CI, and
the security surface re-reviewed. (v0.6 — `thread/sandbox` — was
abandoned by the maintainer before this release; the train went
v0.5.0 → v0.7.0.)

### Added

- The fuzz gate covers the thread module's decoders (step 7.1), 10s
  per target in CI (`make fuzz-thread`): `FuzzDecodeHeader` — the
  envelope rule and the canonical fixpoint over arbitrary headers;
  `FuzzDecideSigned` — the signed-decision verifier over hostile
  input, which must answer only from its error catalogue and record
  nothing without the MAC; `FuzzGrantMatches` — the grant predicate
  engine, deterministic and never panicking over arbitrary grants,
  pointers, globs and arguments; and `FuzzDecodeEntry`'s seeds now
  span every format's goldens (the steering receipts of 3 and the pool
  receipts of 4 joined), `FuzzLoad` keeping the file-level contract.
- The kill -9 crash matrix (step 7.1) on jsonl and sqlite over every
  write point the session layer owns — fourteen storage.Append sites
  behind twelve crash points (`threadtest.CrashMatrix`): the prompt
  entry, the turn-end batch, an approval's park, a decision, a
  compaction, a steer's acceptance receipt, thread/pool's acceptance,
  mirror batch and settlement, a branch, a fork, the steer queue's
  dropped-receipt settlement, Resume's in-flight step persistence, and
  DecideSigned's decision and grant batch; the remaining two sites —
  Resume's expiry sweep and the auto trim's record — write the same
  entry shapes as the decision and compaction points. At each point a
  child process proves the write durable and dies; a fresh process
  reopens and continues the session. sqlite's run re-proves the lock
  takeover from a dead holder at every point.
- The budget suite (step 7.2), enforced in CI like correctness, each
  with its Benchmark for the number: append latency on Memory (time
  and allocs), opening a 100k-entry session on jsonl and sqlite
  (~3s measured, generous bounds), the context build after 50
  compactions, and List over 10k sessions — the Memory walk with a
  metadata filter, sqlite's page, count, title search and meta filter
  in one call each. The suite's finding, recorded for v0.8: both
  durable backends' List is a fleet scan per call (every header
  decoded, sorted in Go, then paged — Meta filtering needs the decoded
  header), so a full cursor walk pays the scan per page; SQL-side
  paging with the freeze's index work is the v0.8 proposal, Total
  already answers "how many" in one call.

### Reviewed, no findings

- Security (step 7.3): HMAC handling (constant-time compare, the
  length-prefixed canonical encoding, the fail-closed check order),
  id validation (`ValidID` vetted at every backend entry point — an
  id is always one path component), file permissions (files 0600
  umask-exact, created directories 0700), and the lock rules (flock
  per session, takeover from a dead holder).
- API (step 7.3): every exported name maps to a feature its ADR
  names. One observation for v0.8's freeze: the AGENTS.md API block
  has grown to ~175 lines across the release train — the one-screen
  property it was born with is gone, and the v0.8 proposal is to
  restructure it around a one-screen core with godoc carrying the
  rest; beside it, the budget suite's List finding (SQL-side paging
  for both durable backends, with the freeze's index work) joins the
  v0.8 list.

## thread 0.5.0 — 2026-09-29

`thread/pool` — bounded concurrent child runs, receipts, nested
approvals, explicit steering forwarding (plan §8, ADR 0022, decided
2026-09-29).

### Added

- `thread/pool.New(max)` — one process-wide FIFO semaphore over every
  child run the pool starts, wrapped or submitted; the bound doubles
  as the depth guard (a sync chain deeper than `max` could only
  deadlock, refused with `SUBAGENT_CYCLE`). `pool.IDs` for
  deterministic child session ids in tests.
- `p.Wrap(name, description, agent, opts...)` — a delegation tool built
  on the core's Subagent under a tool-level `weft.WrapTools`, no core
  change. Sync by default (the call waits, the result is the child
  session's answer); `pool.Async()` returns the receipt line as the
  tool result (model-visible, golden-pinned) for a later turn to read.
  `pool.ToolOptions` forwards weft tool options. Outside a session run
  the wrap falls back to the ordinary subagent path under the slot.
- Every pool child is a session of its own in the parent's storage,
  its header naming the origin (`Header.Lineage`: parent_session,
  parent_call_id) and, for wrapped children, the wrap name in metadata
  — the resume key a restarted process re-Wraps into existence.
- `p.Submit(ctx, parent, agent, prompt)` — the async primitive the
  application calls directly; `p.Cancel(receiptID)`; `p.Close(ctx)`
  (cancels every running child and drains); `pool.Receipts(parent)`
  (entry-driven, restart-safe); `p.Register(sessionID, agent)` — the
  restart hook for children no wrap names.
- Receipts as entries: the `pool_receipt` kind, `"v":4` (format-4
  goldens), the machine accepted → running → done | failed | canceled
  | capped (capped = a budget death: `ErrMaxSteps` or `ErrUsageLimit`),
  settlement carrying the child's stop text and usage.
- `thread.Usage.Delegated` — the ledger's third bucket, summed from
  settled receipts; every pool child bills there (a session-run child
  has no ride on the core's subagent roll-up, which bare `Subagent`
  children keep).
- Nested approvals (ADR 0021 §6 resolved by ADR 0022 §7): a child that
  parks mirrors its requests onto the parent — namespaced call ids
  (`<child>/<call>`), `Child`/`Wrapper` on the request entry — so
  `Pending()` surfaces them with their lineage and hides the parked
  wrapper; `p.Decide(ctx, parent, ds...)` records in the parent,
  replays into the child, resumes it, and completes the parent's parked
  call with the child's answer (resolve for a done child,
  resolve_error otherwise); a re-parking child mirrors again. Signed
  decisions verify in the parent (`s.Request`/`DecideSigned`) and the
  pool's empty `Decide` pumps the children they decided.
- `p.Forward(receiptID, msg)` — explicit steering of a running child
  through its session (ADR 0019 §7); nothing forwards implicitly.
- thread surface the pool builds on: `Session.Storage`, `Lineage`,
  `SessionFromContext` (a run's context carries its session),
  `AppendPoolReceipt`, `AppendApprovalRequests`, `WithLineage`,
  `WithMeta`; `Request.Child`.

## thread 0.4.0 — 2026-09-29

The second backend, per-step durability, and the live tail (plan §7,
ADR 0011 §7's amendment). The root gains `weft.OnMessages` (its own
0.5.0 entry below); thread requires it, released in lockstep —
`thread/v0.4.0` and the new `thread/sqlite/v0.1.0` beside the root
`v0.5.0` and its requirement bumps (openai, anthropic, google 0.3.8,
mcp 0.1.9, store 0.1.3, studio 0.2.1).

### Added

- `thread/sqlite` — its own module (`github.com/weftgo/weft/thread/sqlite`)
  so the modernc driver never enters `thread`'s dependencies: one
  SQLite file for every session, WAL, embedded migrations (a schema
  ahead of the binary refuses to open), the same wire lines as jsonl,
  the full `threadtest` table including the corruption rows, and the
  one-writer rule as a lock row per session — taken over from a holder
  whose process died, judged per-host. Crash tests kill a helper
  mid-append and mid-turn.
- Per-step durability (ADR 0011 §7): a turn's messages append to the
  tree as they join the run's transcript, so a crash mid-turn loses
  nothing emitted. A failed turn whose final form differs from its raw
  tail (an interrupt's golden completions) rewrites the tail on a fresh
  line; an overflow re-run's failed attempt keeps its messages on their
  own branch of the tree.
- The `thread.Watcher` capability on both durable backends:
  `Watch(ctx, session, afterEntryID)` tails a session — the backlog in
  arrival order, then each new entry exactly once, ending with the
  context. Readers never lock; a watcher is a reader that waits.
- `thread.Query` filters: `Meta` (every pair matched exactly against
  the header's metadata) and `TitleSearch` (the session's current title
  — the last info entry's — matched case-insensitively as a substring),
  with the existing cursor and limit paging the filtered set. sqlite
  keeps the title denormalised (migration 0002, backfilled once) so the
  filter never reads a session's entries.

### Changed

- `thread.Load` (both durable backends) reads one snapshot: a session
  deleted under a concurrent load answers `ErrNotFound` or the whole
  session, never a header whose entries vanished.
- A session header carrying a newer envelope fails `Load` as
  `ErrNewerFormat` (both durable backends) — the class the format rules
  name for it — not as line-1 corruption.
- `Resume` joins an in-flight resume through the arming registry: the
  armed resume, not the dangling tail, is the idempotency key.

## 0.5.0 / openai, anthropic, google 0.3.8 / mcp 0.1.9 / store 0.1.3 / studio 0.2.1 — 2026-09-29

Version 4 of the phase-3 plan (plan §7): the transcript observer in
the core, and the session features that need it. Tags cut in two
phases (ADR 0005): the root at v0.5.0, then the sub-modules — openai,
anthropic, google, mcp, store and studio as requirement bumps only —
all requiring the tagged root v0.5.0, beside thread 0.4.0 and the new
thread/sqlite 0.1.0 above.

### Added — the transcript observer (ADR 0006 note, TODO §5.12 shape (b))

- `weft.OnMessages(fn)` — a run option registering an observer the
  loop calls whenever messages join the run's transcript: the step's
  assistant message in its final shape (signed reasoning included),
  the batched tool message, and the messages a steering drain
  delivered. Exact bytes, deep-copied, in transcript order — what an
  incremental persister (a session layer) writes per step equals what
  `RunResult.Messages` holds at the end, without the lossy rebuild
  from deltas. Run-scoped like `Steering`; not inherited by Subagent
  child runs; panics contained and counted (`TapPanics`).

## 0.4.0 / openai, anthropic, google 0.3.7 / mcp 0.1.8 / store 0.1.2 — 2026-09-29

Version 3 of the phase-3 plan (plan §5): the steering hook in the
core, the overflow sentinel, and the modules that learn them. Tags cut
in two phases (ADR 0005): the root at v0.4.0, then the sub-modules —
openai, anthropic, google at v0.3.7 (the overflow mapping rides the
root's adapterkit, with per-adapter fixture tests), mcp at v0.1.8 and
store at v0.1.2 (the Steered event joins the run-id fold; mcp a
requirement bump only) — all requiring the tagged root v0.4.0, beside
studio 0.2.0 and thread 0.3.0 below.

### Added — the steering hook (ADR 0019)

- `weft.Steering(fn)` — a run option installing a steering source for
  that run: `SteerFunc` returns the messages to deliver at a safe point
  or nil (it must not block — drain a queue, do not wait on one), told
  where the run is through `SteerPoint{RunID, Step, Final}`.
- Two drain points: after a step's tool batch, once every call of the
  batch has its result — success, error, truncated, or denied — so the
  call/result pairing cannot be split; and at a final step, where a
  delivered message redirects the run into one more step instead of
  ending it. Never drained at the approval boundary or after a
  `StopWhen` condition fires: an intended end stays an end, and the
  source keeps its messages for a follow-up.
- Delivered messages are ordinary transcript, appended before the next
  step's `PrepareStep` chain runs, so request rewrites see them and the
  transcript stays the single source of truth. A message with any role
  other than `RoleUser` fails the run with `ErrInvalidSteer`.
- `Steered` — the one new event (wire `"steered"`): the delivered
  messages, between that step's `StepFinish` and the next `StepStart`,
  numbered from the run's Seq counter. `Nested` wraps it for child runs
  like any event.
- A redirect consumes a step and goes through the continuation checks
  (`MaxSteps`, `UsageLimit`, `DetectLoops`); on failure the steer is in
  `RunError.Result.Messages`, delivered but unanswered.
- Run option only: a child run started by a `Subagent` tool does not
  inherit the source — forwarding a steer to a child is a session
  decision made explicitly (consumer: `weft/thread` v0.3).
- `wefttest.NewSteers()` — a deterministic, step-keyed steering source
  (`.At(step, msgs...)`, `.Option()`), the replay-safe way to steer a
  recorded conversation: a steer that differs from the recording misses
  its fixture loudly.

### Added — the overflow sentinel (ADR 0020 §5)

- `weft.ErrContextOverflow` — the adapters wrap their provider's
  context-window overflow in it (`openai`, `anthropic`, `google`), both
  links preserved so `errors.Is` finds the sentinel and `errors.As`
  still reaches the vendor SDK's own error. The marker table the
  mapping reads (and `mw.Retry`'s classifier has always read) lives in
  one place now, `internal/adapterkit`; it gains anthropic's second
  shape ("exceed context limit"). `mw.Retry` never retries the
  sentinel, as it never retried the marker text — the consumer is
  `weft/thread` v0.3's compact-and-retry turn.

## thread 0.3.0 — 2026-09-29

Steering, interrupt, overflow (ADR 0019, ADR 0020 §5): everything a
Send can do with a busy session, on the session tree.

### Added

- **Busy policies**: `Steer`, `Interrupt` and `Rollback` join `Queue`
  (the default) and `Reject` — per session with `BusyPolicy(p)`, per
  Send with the new `As(p)`.
- **`Steer`**: the message is accepted at once — a queued receipt
  entry, flushed — and delivered into the running turn at the core's
  drain points (after the tool batch, every call paired with its
  result, or at what would have been the final step, redirecting it).
  A steer meeting a `StopWhen` end or an open approval boundary never
  drains: it defers to a follow-up turn, linked through `Turn.Next`.
- **Receipts** (`"v":3`, format-3 goldens): `queued → delivered |
  deferred | dropped` entries — delivered receipts join the turn's
  end batch atomically, naming the run; deferred entries name the
  follow-up; dropped is `ClearQueue`. `s.Queue()` lists the live
  queue. A queued receipt whose fate never landed (a crash) defers on
  reopen and its follow-up runs — accepted input is durable input.
  `weft.Steering` in `RunOptions` is refused: the session owns the
  steer queue.
- **`Interrupt`**: cancels the in-flight run (the mark survives the
  arm race — an interrupt landing between Send and the run's start
  fells it at birth); calls the partial left without a real result
  record the golden interruption text; an approval boundary the
  interrupt supersedes is denied with the interrupted reason; the
  message runs as the next turn.
- **`Rollback`**: an interrupt that also branches the leaf back to
  before the interrupted turn's receipt entry — the follow-up answers
  as though it never happened, its entries keeping their own line of
  the tree.
- **The overflow re-run**: a turn failing with
  `weft.ErrContextOverflow` compacts (reason `overflow`) and re-runs
  once over the shrunken path under a fresh run id; a second failure
  fails the turn with both errors joined. `ReRunOnOverflow(false)`
  turns it off.

## studio 0.2.0 — 2026-09-29

The Steered event in the Inspector: a steer folds as a user turn
attached to the step it followed, rendered between that step's card
and the next — accent-bordered, replay-jumpable, its words in the
event summary line. `Version` now reads v0.2.0; the module requires
the tagged root v0.4.0 and store v0.1.2.

## thread 0.2.0 — 2026-09-29

Approvals, complete (ADR 0021): the core's approval boundary made
durable, signed, granted and audited on the session tree.

### The 2026-09-29 review round (deep review of the branch)

Eleven fixes from a full review pass over thread v0.2, each pinned by
a test that fails on the old code:

- **Quorum vs the chain (P1)**: a grant match or an Approver's single
  approval under `Quorum(n>=2)` no longer arms an auto-resume that
  denied the call as "no decision" — the chain-decided hand-off arms
  only when every dangling call holds an effective decision, so the
  boundary waits for the second identity.
- **`Always` on a non-approve mints no grant (P1)**: a `Deny` or
  `Resolve` built with `Always: true` — through `Decide`, `DecideSigned`
  or the Approver — records its decision and nothing else; only an
  approve grants (ADR 0021 §4).
- **Repeated call ids stay separate occurrences (P1)**: the approval
  walk resets a call's request and decisions at the message that
  re-issues it, so ADR 0007's repeatable ids can no longer inherit an
  earlier occurrence's verdicts (a grant-approved re-issue read as
  "conflicting decisions").
- **An approve beside a resolve conflicts** (the fold's own words,
  both orders now), instead of silently discarding the approve.
- **A deny-grant's matches count against `MaxUses`** like an approval
  grant's — a bounded standing refusal stops refusing after its uses.
- **`Branch` while a turn runs fails with `ErrBusy`**: the runner holds
  the line the turn's transcript must land on; branching underneath it
  stranded the turn's messages on a context the model never saw.
  Branching off a parked boundary stays the documented escape hatch.
- **Decisions, grants and revocations flush**: under `FsyncOnFlush`,
  `Decide`, `DecideSigned`, `Resume`'s expiry denials and every
  `appendLocked` write (Grant, Revoke, Pin, …) are durable when they
  return — a decision or a revocation no longer lives only in the page
  cache.
- **The `Estimator` hook runs outside the session lock**: one that
  calls back into the Session (`Leaf`, `Pending`) deadlocked the turn's
  own persistence; every other caller hook was already consulted
  unlocked.
- **`nativeOf` never hashes a Model**: the middleware-chain walk
  compares structurally under a depth cap — an unhashable wrapper
  (a struct value with a slice field) panicked the map, and a
  self-wrapping one now terminates instead of walking forever.
- **`jsonl.readHeader` reads to EOF** (`io.ReadFull`): a short read
  made a valid header look torn and `List` silently skipped the
  session.

Also: the compaction log lines carry the `trace` attribute set ADR
0020 §4 promised; ADR 0020 gains a dated amendment recording that the
split turn is summarized in one pass (merged output, one model call);
`SummarizeLeft`'s and `ErrNotImplemented`'s stale docs, the
"stubbed until step 2.2" comment and the phantom
`testdata/approvals` golden reference are corrected; `ArgPrefix`
documents its empty-prefix sharp edge; the write-only `grantRef.shared`
field is gone; the README status line catches up to v0.3.6.

The release pass (same day) added three more, from the full-version
verification against ADR 0021 and plan §4: ADR 0021 gains a dated
amendment deciding the boundary-holds-the-tail-raw rule its citation
pointed at; a shared grant's audit detail is namespaced ("shared
grant …") so a store id colliding with a session grant's entry id
cannot inflate its use count (pinned); and the trigger's re-arm on
the resolving turn — implemented and cited but never pinned — has its
pin.

- New entry kinds with `"v":2` — `approval_request`,
  `approval_decision`, `approval_audit`, `grant`, `grant_revoked` —
  with goldens in `thread/testdata/format2/`; a v0.1 reader fails
  loudly on a session that used approvals, and every format1 golden
  still reads. A parked call becomes an `approval_request` in the same
  Append as its turn; `s.Pending()` rebuilds from entries, so pending
  approvals survive restarts.
- `s.Decide(ctx, decisions...)` records `thread.Approve`/`Deny`/
  `Resolve`/`ResolveError`/`ApproveAlways` durably — `ErrNotPending`
  before anything lands or runs — and resumes the boundary when it
  completes (`thread.AutoResume`, default on; `s.Resume(ctx)` forces
  it, denying undecided calls with the core's "no decision" text).
  The resumed turn links to the parked one through `Turn.Next()`. A
  `Send` while approvals pend queues behind the boundary.
- The decision chain — grants, then an optional `Approver` bounded by
  `thread.ApproverTimeout`, then the park — runs before a request is
  durable, and every step writes an audit entry.
- Grants: one tool plus argument predicates (`thread.ArgEquals`,
  `ArgPrefix`, `ArgGlob` — `*` spans separators, a command is not a
  path), session-scoped as entries (`s.Grant`, `s.Revoke`) or shared
  behind `thread.WithGrantStore`; expiry, `MaxUses` counted from the
  audit trail, deny-grants whose model-visible default "denied by
  grant" is pinned, and "approve and always allow" via
  `thread.ApproveAlways`.
- Signed decisions for transports that cross a process:
  `thread.NewKeyring` + `thread.WithKeyring`, `s.Request(callID)` mints
  an HMAC-SHA256 challenge (nonce, key id, the request's hashes),
  `thread.SignDecision` signs, `s.DecideSigned` verifies fail-closed —
  `ErrBadSignature` (constant-time), `ErrExpired`, `ErrReplay` (nonces
  are entries; the guard survives restarts), `ErrArgsChanged`,
  `ErrUnknownKey` — and `thread.RequireSigned()` closes the unsigned
  door.
- `thread.Quorum(n)`: approvals from n distinct approver identities
  resolve a call; conflicts resolve to deny with the pinned
  "conflicting decisions". `thread.RequestExpiry(d)` lapses undecided
  requests with a stated, pinned reason on the next resume.
  `thread.OnRequest(fn)` notifies when a request parks; `s.Audit()`
  returns the whole approval trail from the file.
- Decisions fold per parked run, never per bare call id: call ids may
  repeat across turns (ADR 0007), and a decision names its own
  boundary.
- `thread/examples/approvals`: park, restart, a signed decision,
  resume, a rejected replay, the audit trail — offline, output pinned.

## thread 0.1.0 — never tagged (shipped inside thread/v0.2.0, 2026-09-29)

The v0.1 window passed without a tag: the module's first tag is
`thread/v0.2.0`, whose history holds everything below.

First release of `weft/thread`: sessions as an append-only entry tree
(ADR 0011), durable through `jsonl.Open(dir)` or `thread.Memory()`,
with compaction (ADR 0020) on top. New module; it requires root
v0.3.7's `Agent.Model()` and imports nothing else from weft.

### Added — the format and the Storage contract (ADR 0011 §2, §5–§6)

- The sealed entry kinds — `message`, `turn`, `compaction`,
  `branch_summary`, `leaf`, `label`, `info`, `custom`,
  `custom_message` — one wire discriminator each; a `message` entry
  embeds the ADR 0001 wire verbatim. The `{"weft":1}` header
  envelope, the "v" minimum-reader rule (loud on the unknown and the
  newer, never a skip), time-sortable ids with an `IDs(func() string)`
  option, and goldens for every kind in `thread/testdata/format1/`.
- `thread.Storage` (Create, atomic multi-entry Append, Load, List,
  Delete) with `ErrNotFound`/`ErrExists`/`ErrLocked`/
  `ErrNewerFormat`/`ErrCorrupt{Line}`, `LoadReport` for torn tails
  and salvaged lines, and the optional `Flusher` and `Watcher`
  capabilities. `thread.Memory()` and `thread/jsonl` (0700/0600,
  id vetting, one-write appends + fsync, advisory locks, bounded
  header-only List) both pass the shared `threadtest` conformance
  table, crash-tested and fuzzed.

### Added — sessions, turns, branching (ADR 0011 §2–§4)

- `thread.Create`/`Open`/`List`/`Delete`; `Session.Context()` walks
  the leaf's path and repairs it; `Entries`, `Leaf`, `Path`, `Label`,
  `SetInfo` (title last-wins, metadata merged), `Custom`,
  `CustomMessage`, and `Usage` with the summarizer costs in their own
  bucket.
- `Send` appends and flushes the prompt before the run starts, runs
  the session's agent under `<session>-t<n>`, and persists the new
  messages plus the turn entry in one atomic batch under a
  `WithoutCancel` window — the partial transcript kept and repaired
  on failure, a cancel recorded as canceled, pending approval calls
  recorded for a `weft.Approve`/`Deny` resume. `Turn` is the receipt
  (`ID`) and handle (`RunID`, replayable `Events`, `Wait`); busy
  sends Queue (default) or Reject (`ErrBusy`); `thread.RunOptions`
  carries extra run options and refuses `weft.Messages`, `Prompt`
  and `RunID`.
- `Branch` navigates (with `SummarizeLeft` writing a branch summary),
  `Fork` copies the path into a self-contained new session, and
  nothing is ever deleted.

### Added — compaction (ADR 0020)

- The trigger: the provider-reported input of the last step plus the
  estimated tail that report cannot cover (recorded together on the
  turn entry), plus an estimated delta against `window − Reserve`; no
  window known means no automatic compaction and one warning. The cut keeps about `KeepRecent` tokens, lands on
  a user or assistant boundary, never between a call and its result,
  and splits turns larger than the window. Signed reasoning older
  than the compaction is stripped from the context the model sees.
- The summary: the session's own model by default (or `SummaryModel`
  with a fallback chain), the golden skeleton prompt behind the fixed
  `<weft-summary>` marker, iterative (each summary fed the previous),
  output capped at 0.8 × Reserve, and the serialized range with tool
  results capped and files as names. `PreviewCompaction`,
  `ApplyCompaction`, `Compact`, `Uncompact`.
- All five configuration layers: the knobs (`ContextWindow`,
  `ModelWindows`/`ModelReserves`, `Reserve`, `KeepRecent`,
  `TriggerFunc`, `MinTurnsBetween`, `MaxPerSession`,
  `WithEstimator`, `Disabled`), the summary options, the swap
  interfaces (`Summarizer`, `Compactor`, `Trimmer`, plus
  `ClearOldToolResults` with its golden stub), the hooks
  (`BeforeCompact` with Proceed/Cancel/Replace, `AfterCompact`,
  `CompactFailed`, `CheckSummary`) — panics contained — and the
  `NativeCompactor` seam with `PreferNative` and the text fallback.
  Extras: `Pin` (an entry survives every compaction) and the cost
  ledger.

### Added — docs and examples

- `thread/examples/session`: turns, a label, a branch, a fork, a
  previewed compaction and a reopen from disk, offline and
  deterministic. README "Sessions" section; AGENTS block 8; this
  changelog.

## 0.3.7 — 2026-09-29

### Added — the model accessors thread needs (ADR 0006 amendment)

- `(*Agent).Model()` — the model as the loop calls it, `WrapModel`
  middleware included, in the installed order. The session layer
  (`weft/thread`, v0.1) summarizes with the session agent's own model
  by default (ADR 0020 §2).
- The `Unwrap() Model` convention on model middleware wrappers, beside
  `Info()`/`InfoOf`: `mw.Retry`, `mw.Fallback` (its primary — the
  chain's first model, the one `Info` names), `mw.Log` and
  `mw.RepairJSON` implement it; `weft.Unwrap(m)` walks one level and
  returns nil on a non-wrapper. Callers walk a chain one middleware at
  a time without knowing the wrapper types.

## studio 0.1.0 — 2026-09-28 (the Inspector, T1)

First release of `weft/studio`: the Inspector over a run store, served
as one read-only `http.Handler`. New module, no core or store change.

### Added — the mount surface (ADR 0018)

- `Handler(store, opts...)` serves the embedded UI and the JSON API;
  options `Base` (the mount path, default `/studio/`, written into the
  shell's `<base href>` per request — the same bundle mounts
  anywhere), `Manifest` (agent/tool cards), `Title`, and
  `Capabilities(...)` — ADR 0018 §8's hosting seam: the open handler
  reports none, a hosted server declares what it carries, and the UI
  gates deployment-specific screens on the one
  `api/meta.capabilities` list.
- The read-only API, golden-pinned on the Go side and type-mirrored in
  TS against the same fixtures: `api/meta`, `api/runs` (agent,
  status, tag, and cursor paging — `parent` absent means top-level
  only), `api/runs/{id}` (row + the store's own result document +
  children), `api/runs/{id}/events` — **paged, never inline**
  (`after`/`limit`, `next_after`, `done` only when the run finished;
  the T2a live tail is this endpoint read from the last position;
  sliced from one `Get` behind a finished-run LRU), `api/manifest`.
  A recording this weft cannot decode is a 409 `newer_format` with
  the upgrade message; unknown-event results likewise.
- `studio/examples/basic`: records a tool-call run, a subagent run,
  and a failing run into SQLite and serves Studio on 127.0.0.1:7331.

### Added — the Inspector UI (T1: A1–A4, B1–B2, B7, B9–B10, H1, L3)

- Runs list: every status shown (crash-orphaned rows read
  interrupted), filters and the paging cursor in the URL, manual
  refresh, copyable mono ids, token columns with cached/reasoning
  splits on hover.
- Run page: steps with model text, reasoning collapsed, tool calls
  with pretty-printed args and their results (error-as-data in
  mustard with the `ToolError` code), subagents inline with usage
  rollups and links to child run pages, truncation badged from the
  loop's own markers, raw JSON one `r` away, the local-recording
  note. Every view, selection, and replay position is a URL.
- Guaranteed replay over the event index (events carry no
  timestamps): play/pause, 1×/4×, scrubber over the seq gutter, `[`
  and `]` step, `t` in the URL.
- Agent and tool cards from the manifest: schemas as trees, per-tool
  policy chips, source file:line.
- Theme: the landing page's paper-loom tokens (Geist/Geist Mono
  self-hosted, offline), the Studio event/status palette
  (contrast-checked), system/light/dark with no flash.

### The UX review pass (2026-09-28, before tagging)

A second look at the T1 UI as a debugging tool, with the fixes it
found:

- Run page: the prompt and the answer sit above the fold, with the
  error for a failed run; facts read as words (`2 steps · 14 events ·
  30 in / 15 out · took 2ms`, elapsed while running). Tool calls are
  one-line rows — name, an args summary, `ok · 29 B` or the
  `ToolError` code, `running…`, `never completed`, the truncation
  badge — opening to the args and result windows; JSON is
  pretty-printed and coloured; long windows fold at 24 lines. A
  failed run's open step says *failed during this step*, not *in
  flight*. Subagent steps render through the same step body as the
  parent's. Every step and call has *replay to here* (the fold
  carries stream positions).
- Trace, the default view: a flow strip (one pill per step, arrows
  for results fed back, the end state), then a waterfall | detail
  split. The waterfall is the run — steps, tool calls and subagent
  runs (their own steps and calls nested) as spans on the event
  position axis, one tree, one playhead; rows carry a kind icon, a
  badge (stop reason, ok, the ToolError code, running, never) and
  the event range; ↑↓ select, ←→ fold, expand/collapse all; a bar
  click seeks. The detail panel reads the selected span as detail
  (the same step and call bodies as the story), events (that span's
  slice of the stream) or json (the folded node), and renders the
  fold AT the playhead, so scrubbing replays inside it too. The
  trace opens on whatever went wrong (a tool error, the step a run
  died in), else the first step. `?sel=` and `?d=` name the
  selection and mode; `e`/`s`/`r` switch trace/story/raw; `j`/`k`
  walk spans. The split keys on the content width (a container
  query, not the viewport — the sidebar takes 16rem) and a toggle
  forces side by side or stacked, remembered per browser. Open
  spans fade, never-completed ones hatch, the step a run died in
  reads red. The `Waterfall` component is axis-
  agnostic (Span[] over a numeric domain) so T2a's timed spans and
  the live view draw on it as is; the step story stays as `story`.
- Replay: the gutter is bucketed (never wider than its box, a click
  lands where it looks), a playhead line, a readout naming the event
  at the playhead, `[`/`]` jump by step or tool boundary, `,`/`.`
  move one event, Home/End on the focused bar.
- Raw: an events explorer (position, kind, type, one-line summary;
  kind filters with counts, full-text search, expand to the event,
  replay-to-here, paged at 500 rows) and a collapsible document tree;
  copy and download for both.
- Runs list: status as dot + word, the error under a failed run's id,
  the whole row opens the run, elapsed for running runs, filters that
  mirror the URL and apply on enter/blur, active-filter chips, a
  "no runs match" state distinct from "no runs recorded", the select
  shows its label. ⌘K jumps to any recent run.
- Fixes: shortcuts no longer fire with ctrl/⌘/alt held (ctrl-r
  toggled raw *and* reloaded); collapsible chevrons rotate; the
  events tail asks from `last+1` instead of re-fetching the last
  event each poll; the stream array is fresh per publish; `?before=`
  seeds the first page; the shell's location-dependent parts render
  client-only so a deep link can never hydration-mismatch (which
  would drop the runtime `<base>` and break every module script).

### Fixed — the release review (2026-09-28, before tagging)

- `api/runs/{id}/events` panicked (negative slice capacity) on an
  `after` past the end of the stream; it now answers the empty,
  done page. Regression cases cover `len+5` and the int64 maximum.
- A missing hashed asset under `assets/` is a 404, not the HTML shell
  served under a script's name.
- `Title` and `Base` are HTML-escaped where they enter the shell.
- The runs list and the ⌘K palette shared one query-cache key with
  different shapes (a plain page vs. an infinite query): opening the
  palette and then the list crashed it. The list has its own key.
- When a run finished, the closing drain read one page only; a burst
  of more than one page at the end stayed truncated. It follows
  `next_after` to the end.
- Pausing replay writes `t`, as the URL contract says.
- A subagent's tool call keyed its span `c:<callID>` like the
  parent's, so a reused id (`call_1`) made `?sel=` pick the wrong
  span. Child call keys carry the child run id, as step keys do.
- Deep subagent trees fold on first load: the fold is applied once
  the spans arrive, not only at mount.
- CI gains the `studio` job (`make studio-check` under Bun 1.3.14):
  the freshness, budget, and web-test gate this section describes
  now runs on every push and PR.

### Engineering

- The UI is a TanStack Start SPA prebuilt into a committed, embedded
  `studio/dist` (~319 KiB gzipped of a 600 KiB budget): no build step
  for users. `make studio-check` gates dist freshness (two clean
  builds are byte-identical), the size budget, and the web
  typecheck/tests; `make test`/`lint` cover the Go side via go.work.
- Fully offline by construction: every resource the app loads is
  same-origin; the CSP allows no external script, style, or
  connection.

## store 0.1.1 — 2026-09-27 (review pass over 0.1.0)

### Fixed — the durability races a second look found

- **Concurrent first Opens on a fresh file no longer fail.** Two
  things raced when two processes opened a brand-new database at once:
  the per-connection `journal_mode(WAL)` DSN pragma (the mode switch
  wants a brief exclusive lock SQLite will not wait for — instant
  `SQLITE_BUSY`, unretried by busy_timeout) and the migration loser,
  whose pre-lock read of `schema_migrations` made it re-run a migration
  the winner had applied ("table already exists"). WAL is now set once
  by the first `Open` through a converging switch (`setWAL`: the
  winner writes WAL into the file header, everyone else reads `wal`
  back and moves on), and each migration re-checks the applied version
  inside its own transaction — the loser skips what the winner applied.
  Steady-state pragmas (synchronous, busy_timeout, foreign_keys,
  `_txlock=immediate`) stay per-connection, as before.
- **A heartbeat touch in flight when the run ends can no longer land
  after the closing write** and resurrect a `running` row that reads as
  interrupted half a minute later: touch and end serialize on the
  run's write lock, end's write last.
- **A run id that restarts in-process closes the old run's log**: its
  orphaned heartbeat ticker otherwise kept re-saving the stale running
  row under the id and clobbered the new run's — including its closing
  write.

### Fixed — loud where 0.1.0 was silent

- `store.UnmarshalResult` now enforces the `{"weft": N}` envelope
  (ADR 0010 §2.3): a result document from a newer format fails with
  `store.ErrNewerFormat`, and one with a result but no envelope fails
  outright — neither decodes with this build's tags. The migrations
  table already enforced the same rule for the schema; this is the
  document-level half.
- Every JSON column the SQLite backend reads (model, usage, tags,
  result) decodes loudly, naming the run and the column, instead of
  coming back with blanks; `List` still never reads events or results,
  so a run that cannot be opened still lists.
- The JSONL event reader names the first skipped line, not just the
  count, as ADR 0010 §2.5 promises.
- `store.Record(nil)` panics at the call site (Subagent's nil-child
  panic is the core's precedent) instead of failing on — or silently
  dropping — every write.

### Fixed — aliasing and matching

- Neither backend aliases a caller's `RunResult` or tags: Memory clones
  through the store's own codec on Save and Get, and every returned
  tags map is a copy — editing a returned record changes nothing on the
  next read.
- A tag a record does not carry never matches a tag query, even when
  the queried value is `""` — sqlite's `json_each` filter and Memory
  now agree; keys that would need escaping in a JSON path still filter,
  because the key is a bound parameter.

## 0.3.6 — 2026-09-27

### Added — the observation accessors (`AgentFromContext`, `Agent.Logger`)

- Writing the store's `Record` from a satellite found the one thing
  composition cannot reach: `weft.Option` is sealed, so a satellite
  composes `weft.Options(weft.Tap(...), weft.OnRunEnd(...))` and never
  receives the agent it was installed on. `weft.AgentFromContext(ctx)`
  returns the agent running on ctx — the ancestry chain's head, nil
  outside a run — and `(*Agent).Logger()` returns the run lines' sink.
  Both observation-scoped, read-only, no seam (ADR 0016's amendment);
  the store records the run's manifest hash through the first
  (ADR 0010).

## 0.3.5 / store 0.1.0 — 2026-09-27

Phase 2b step 1 (`docs/phase2b-store-plan.md`): the run store, and the
core additions it needs. Tags cut in two phases (ADR 0005): the root
at v0.3.5 (OnRunEnd) and v0.3.6 (the observation accessors), then the
sub-modules — openai, anthropic, google at v0.3.6 and mcp at v0.1.7
(requirement bumps only) — plus the new `store/v0.1.0` requiring the
tagged root v0.3.6.

### Added — core: `weft.OnRunEnd(fn)`, the outcome observer

- Called exactly once per run, after `RunFinish` is delivered or the
  `RunError` is built, before `Run` returns. It is the failure signal a
  tap cannot carry — a failed run emits no event after its last
  delivered one (ADR 0004) — and neither middleware seam wraps the
  run. `res` on failure is the `RunError`'s partial transcript; a child
  run's fires inside the parent's tool call; a panicked run fires
  nothing; a panic in an observer is contained and counted with the
  tap panics. Not a third seam (ADR 0006's note); ADR 0016's amendment
  records it as the fourth observation output.

### Added — module `weft/store` (v0.1.0): run records, SQLite first

- **What a record holds** (`store.RunRecord`): the run's identity —
  id, parent id and parent call id when it is a subagent's child, agent
  name, model, manifest hash, weft version, started/finished/
  heartbeat, status, tags — its event stream (every `weft.Event`,
  `Nested` inline, in `Seq` order), and its result (`RunResult`; on
  failure the partial plus the `RunError` text). **What it is not: a
  checkpoint** — weft records what happened and replays it; resume
  stays the approval boundary (ADR 0007). The format is ADR 0010's.
- **`store.Record(s, store.Tags(...))` is the tap**: one `Append` per
  event as it arrives — nothing buffered, so a crash loses nothing that
  was emitted and the Inspector can tail a live run. The row is
  written at `RunStart` as `running`, the heartbeat bumps per event and
  on a ticker while the run is alive but quiet, and `OnRunEnd` closes
  it — `succeeded` with the result, `failed` with the partial and the
  error text. Store errors are logged through the agent's logger and
  never fail the run; a failed append marks the record degraded in its
  `Err`.
- **`interrupted` is derived, never stored**: `status == running` and a
  heartbeat older than `HeartbeatTimeout` (30 s) reads as interrupted —
  a crash leaves evidence, and a live run in another process is not
  mistaken for a corpse.
- **Child runs own records**, linked by `ParentID`/`ParentCallID`, and
  stay inline in the parent's stream (ADR 0004's total order is the
  record's order). `Query` with an empty `ParentID` lists top-level
  runs only, so children do not flood the list view.
- **`List` returns no events and a `Total`** (Mastra's `listTracesLight`
  lesson), paged by a `Before` cursor on `Started`, not offsets
  (LangGraph); `Get` returns everything; `Delete` is in the contract
  from day one — children survive a deleted parent, orphaned by
  design.
- **`store/sqlite`** (`sqlite.Open(path)`; `":memory:"` works): CGO-free
  `modernc.org/sqlite` with Crush's pragmas (WAL, `synchronous=NORMAL`,
  `busy_timeout`, `foreign_keys`, `_txlock=immediate`), embedded
  migrations tracked in `schema_migrations` — a database written by a
  newer weft fails `Open` with `ErrNewerSchema`, never a silent
  misread. An event whose `type` this weft does not know fails `Get`
  with `ErrUnknownEvent` naming it and the run; `List` still works, so
  an older Inspector shows the run and says why it cannot open it.
- `store.Memory()` is the in-process backend (tests, examples, and the
  reference the SQLite suite is compared against); both run the shared
  `storetest` conformance table under `-race`.

### Fixed — release hygiene

- The OTel instrumentation `version` constant rides at the tag again
  (v0.3.5); it had drifted at v0.3.0 through the 0.3.1–0.3.4 releases
  (ADR 0005's amendment: tagging a release sets it).

## 0.3.4 — 2026-09-27

The pass over 0.3.1–0.3.3 (and mcp 0.1.4–0.1.5): the fixes hold, one
of them changed a behaviour it did not document, and two snippets
drifted. Tags cut together: root, openai, anthropic, google at v0.3.4;
`mcp` at v0.1.6. Root API unchanged (apidiff clean); the adapters'
`MaxRetries` gains a meaning for 0.

### Changed — openai, anthropic: `MaxRetries(0)` switches the SDK's retries off

- **0.3.3 put the wait for response headers under the idle timer**, and
  that wait contains the SDK's own transport retries and their sleeps.
  The Anthropic SDK honours any `retry-after` uncapped and the OpenAI
  SDK any under a minute, two retries each by default — so a 429 whose
  ask the SDK would have slept out and retried successfully now fails
  at `IdleTimeout` (60s), as `ErrStreamIdle`, with the 429 and its
  header discarded. `mw.Retry` retries that on backoff rather than the
  provider's ask; without it the caller reads a stall where the cause
  was rate limiting. The documented gap "0 cannot disable it — keep a
  zero-retry client via `Client(c)`" is closed: `MaxRetries(0)` now
  forwards 0 (a negative n is 0; absent, the SDK default of 2 still
  applies). That is the pairing for `mw.Retry`, which then owns every
  retry, honours the ask itself under `MaxWait`, and shows it in `Log`.
  Pinned in both adapters (a 500 draws one request under 0, three
  under the default). `IdleTimeout`'s doc on all three adapters, and
  `mw.Retry`'s, now say the pre-headers wait is the first gap and what
  it contains; google's SDK retries nothing unless asked, so only its
  doc changes.

### Fixed — docs

- README's mcp snippet and `mcp/doc.go` taught `tools, _ :=
  mcp.Tools(...)`, which since mcp 0.1.4 silently discards the
  `*ImportError` naming the skipped tools — the one outcome the ADR
  0015 amendment rules out. Both show the `errors.As` idiom the client
  example already used.
- "The first occurrence stands" for a repeated mcp tool name is the
  first *importable* occurrence: a skipped earlier entry claims
  nothing, so a later valid tool under that name imports. Stated in
  `Tools`' doc, the ADR amendment, and pinned
  (`TestToolsSkippedFirstEntryDoesNotBlockLaterSameName`).

## mcp 0.1.5 — 2026-09-27

Reviewing 0.1.4 found two defects in the same untrusted-input class;
only `weft/mcp` changes (`mcp/v0.1.5`).

### Fixed — `mcp.Tools`

- **An empty server-side name is skipped under a `Prefix` too.** The
  0.3.1 check tested the composed name, so `{"name": ""}` under
  `Prefix("gh_")` imported as a tool called `gh_` whose handler called
  the remote tool `""`. The check reads the server's name.
- **A name repeated in one listing is skipped and reported** (the
  first occurrence stands). Both used to import and `weft.New`
  panicked on the duplicate later — untrusted input escaping as a
  panic.
- `mcp/examples/client` shows the `errors.As(err, &skipped)` idiom the
  ADR 0015 amendment documents.

## mcp 0.1.4 — 2026-09-27

The one item 0.3.3 left open, decided: ADR 0015's 2026-09-27
amendment. Only `weft/mcp` changes (`mcp/v0.1.4`); the root and the
adapters stay at v0.3.3.

### Changed — `mcp.Tools` fails the tool, not the listing

- **A tool that cannot be imported no longer fails the whole import.**
  `Tools` returns every importable tool and, when any was left out, an
  `*mcp.ImportError` naming each skipped tool (`Name`, `Index`, `Err`
  carrying `ParseSchema`'s cause; `Unwrap() []error`). The slice is
  usable whether or not the error is set — `*weft.RunError`'s
  partial-result shape — so the caller decides with `errors.As`
  whether a skipped tool is a warning or a stop. A nil entry and an
  empty name are reported the same way, by index; a listing failure
  (transport, ctx) is still the plain error with no tools. Callers
  that returned on any error see the same outcome as before; callers
  that want the good tools now get them. The field's clients (AI SDK,
  Mastra, pydantic-ai) pass schemas through unparsed and learn of a
  bad one from a provider 400 that fails the whole step — weft keeps
  its stricter import and now uses what it learns to isolate rather
  than to refuse.

## 0.3.3 — 2026-09-26

The P3s the 2026-09-24 review deferred, landed — every one of them
except the mcp schema-import skip-and-aggregate, which contradicts ADR
0015's standing decision (one bad schema fails the import loudly) and
waits for an ADR, not a patch. One of the corpus rows found a defect
of its own. Tags cut together: root, openai, anthropic, google at
v0.3.3; `mcp` at v0.1.3. Root API is additive only (two conformance
helpers, one `Caps` field).

### Fixed — a schema could advertise fields no argument could reach

- **`Tool` and `Output[T]` (reflected schemas) panic at
  construction on an embedded pointer to an unexported struct type**
  (`type input struct{ *base }`). encoding/json can marshal such a
  field when non-nil but can never unmarshal into it ("cannot set
  embedded pointer to unexported struct type"), and every weft schema
  is decoded into a zero value — so the flattened fields were
  advertised, unreachable, and every argument the model sent came
  back as `field "id": expected string, got string`. The panic names
  the type and the fix (embed the value, or export the type). Found
  by the corpus row the review asked for; an embedded pointer to an
  *exported* type is pinned working.

### Fixed — adapters

- **google folds `toolUsePromptTokenCount` into `InputTokens`.** genai
  bills those tokens outside `promptTokenCount`; tool-heavy runs
  undercounted input and `UsageLimit` budgeting skewed with it.
  Pinned on the tool round-trip fixture (9+3).
- **The idle timer now covers the wait for response headers in openai
  and anthropic.** Both opened the stream synchronously, so a server
  that accepted the connection and never answered stalled until the
  caller's ctx deadline — forever without one — and never produced
  `ErrStreamIdle`. The request is opened on the reader goroutine, as
  google's always was. New conformance case
  `idle_timeout_before_headers`, green on all three.
- **openai and google send no tool choice without a catalog**, the
  guard anthropic already had: `tool_choice` / `toolConfig` alongside
  an empty `tools` is a provider 400 for direct `Model.Stream`
  callers (the loop already refuses a forced choice with no tools).

### Fixed — the core fails loud on a nil tool at `New`

- A nil `*ToolDef` handed to `New` panics naming the mistake, as a
  duplicate name does; it used to be skipped silently while the
  runtime snapshot path fails the run with `ErrNilTool` for the same
  thing.

### Added — conformance

- **`provider_error` case**: a 429 with `Retry-After: 7` from the
  provider must reach the caller as a `*RunError` with the SDK's own
  error type still on the chain — `mw.HTTPStatus` reads 429 off it on
  every adapter, and `mw.RetryAfter` reads the header where the SDK
  keeps the response (`Caps.ErrorHeaders`: openai and anthropic yes;
  google no — genai's `APIError` carries no headers, so `mw.Retry`
  backs off on Gemini 429s, a declared gap). This retires part of the
  live debt: Retry's header extraction now runs against the real SDK
  error types offline.
- `conformance.SilentServer` (accepts, never answers) and
  `conformance.ErrorServer` (status + headers + body) back the two new
  cases; `Caps.ErrorHeaders` declares the header capability.

### Fixed — docs, tests, tooling

- `wefttest.Golden -update` logs each file it created or rewrote and
  leaves up-to-date files untouched, so a regeneration run says what
  was stale instead of rewriting everything silently.
- `mw.Allow(nil)` is documented as a pass-through.
- The empty-user-message rules cross-reference each other: openai and
  anthropic keep a visible placeholder, google drops the message.
- Schema corpus rows for `time.Duration` (integer nanoseconds, the
  documented foot-gun, pinned), embedded pointer structs, and nested
  maps; the mcp import's float64 rounding of integers beyond 2^53 is
  pinned (`9007199254740993` arrives as `9007199254740992`).

### Left open (needs an ADR)

- One unparseable input schema still fails the whole mcp import (ADR
  0015: loud over silent). Failing the tool rather than the listing is
  the review's suggestion; it changes documented behaviour and waits
  for a decision.

## 0.3.2 — 2026-09-26

The release that makes the tags installable. Every sub-module tag so
far (`openai`, `anthropic`, `google` at v0.1.0 … v0.3.1; `mcp` at
v0.1.0, v0.1.1) required the root as a placeholder pseudo-version
(`v0.0.0-00010101000000-000000000000`) resolved through a `replace
=> ../` that only the workspace honours. Consumers ignore `replace`,
so `go get github.com/weftgo/weft/anthropic@v0.3.1` failed with
`invalid version: unknown revision 000000000000` — none of the adapter
or mcp tags could be used outside this checkout. Tags cut together:
root, openai, anthropic, google at v0.3.2; `mcp` at v0.1.2. The root
module's Go API is unchanged (apidiff clean against v0.3.1).

### Fixed — the sub-modules require the tagged root

- **`openai`, `anthropic`, `google`, `mcp` require
  `github.com/weftgo/weft v0.3.2` and carry no `replace`.** The
  committed `go.work` keeps in-repo development resolving the root
  from disk exactly as before (ADR 0005: "a tagged adapter requires a
  tagged root"); outside the workspace the published root is used.
  `examples/otel` follows the same rule. Verified from a clean module
  with `GOPROXY=direct`: `go get github.com/weftgo/weft/anthropic@v0.3.2`
  resolves and builds.

### Fixed — `mw.Retry`: the retry-after bound is strict

- `fitsDuration` now rejects a value that lands exactly on
  `MaxInt64/unit`: `float64(math.MaxInt64)` rounds up to 2^63, so the
  boundary value scaled to a float the `int64` conversion could not
  hold (overflow is implementation-defined). No real header reaches
  it; the 0.3.1 fix's `ask >= 0` guard in `delay` already contained
  the consequence. Pinned.

## 0.3.1 — 2026-09-26

The 2026-09-24 review's findings, landed (full report in the research
checkout; `WEFT-CODE-REVIEW-2026-09-24.md`): all six P2s, the doc
drift, and the P3 edges that touched files already open. Tags cut
together: root, openai, anthropic, google at v0.3.1; `mcp` at v0.1.1.
Everything is fixes — apidiff is clean against v0.3.0 with no
allowances, and the adapter request bytes are unchanged (the one
byte-visible change, multi-valued ExtraHeaders, only adds values the
caller explicitly set).

### Fixed — `mcp`: untrusted-input robustness (review §2.5, §2.6)

- **`Tools` fails loudly on a server tool with an empty name** instead
  of panicking inside `RawTool`. The SDK's server-side name check only
  logs and its list filter drops nil tools but not empty names, so a
  hostile or buggy server listing `{"name":""}` reached the import
  path as a panic — untrusted input escaping as a process crash.
- **A schema'd tool returning non-JSON text is an isError result, not
  a wedged session.** Both `AddTools` and `Serve` set
  structuredContent from the result text whenever the tool advertises
  an output schema; text that is not JSON produced a `CallToolResult`
  the SDK cannot serialize, so the server never wrote a reply and the
  client blocked in `CallTool` forever — a *successful* call hanging
  the session. The result now names the output-schema breach; regular
  `Tool` definitions are unaffected (a string `Out` carries no output
  schema, everything else marshals).

### Fixed — `Resolve` with nothing pending (review §2.1)

- **A `Resolve` against a transcript with zero pending calls now fails
  the run loudly at step 0**, as `Resolve`'s doc and ADR 0007's
  2026-09-22 amendment already promised. The resolved-id validation
  ran only inside the resume block, so with nothing to resume the
  payload was silently dropped (`err = nil`) — while the same Resolve
  alongside another pending call failed as documented. Approve/Deny
  keep ignoring unknown ids, the recorded asymmetry.

### Fixed — adapters: multi-valued `ExtraHeaders` truncated (review §2.2)

- **openai + anthropic keep every value of a multi-valued
  `ExtraHeaders` entry.** Both looped `option.WithHeader` over one
  key's values, which has `Set` semantics — `http.Header{"X-Multi":
  {"a","b"}}` (legal, and `CloneHeaders` deliberately preserves the
  whole slice) lost "a" on the wire despite the verbatim-headers doc.
  The first value now sets and the rest add (`WithHeaderAdd`); a
  single-valued header is byte-identical to before. Google's path was
  already correct (genai assigns the whole `http.Header`).

### Fixed — google: a run-level `MaxTokens: 0` no longer lifts a construction cap (review §2.3)

- The fold took the request value unconditionally, then its
  `if maxTokens > 0` gate sent nothing — so `google.MaxTokens(128)`
  plus a run `Params{MaxTokens: 0}` replaced the 128 cap with the
  provider default (effectively unbounded), while the adapter's own
  comment claimed the fold never replaces a construction value with a
  zero. A request `MaxTokens` of 0 now keeps the construction value,
  mirroring anthropic's fold; neither-set still sends nothing, and
  openai keeps sending 0 as a value (pinned). Both cases are pinned
  in `TestFoldParams`.

### Fixed — anthropic: `ReasoningTokens` is mapped (review §2.4)

- The `MessageDeltaEvent` case read only `OutputTokens`, so thinking
  runs reported `ReasoningTokens: 0` forever while openai mapped
  `reasoning_tokens` and google mapped `thoughtsTokenCount`. The
  finish now carries `output_tokens_details.thinking_tokens` — a
  subset of the inclusive `OutputTokens`, per the 2a.4 splits rule —
  pinned on the extended `thinking_then_tool_use` fixture.

### Fixed — the docs the review caught drifting (§3)

- **README's flagship middleware snippet now teaches the order
  `mw/doc.go` documents** — `Log, Fallback, Retry, RepairJSON`. As
  shipped it showed `Retry` outside `Fallback`, so the primary's
  429/503 never reached Retry's classifier and the snippet's own
  "retry-after honoured" comment was wrong for the primary.
  `mw/doc.go`'s "the primary gets exactly one attempt" is corrected
  too (one per retry cycle, up to MaxRetries+1).
- **Three stale comments still describing the tool cache deleted on
  2026-09-18** (anthropic's Model doc and both adapters'
  `convertTool`) now state the per-request conversion ADR 0013
  records.

### Fixed — P3 edges the same review filed (§3)

- **`mw.Retry` rejects a `retry-after` it cannot convert** — `1e19`,
  `Infinity`, `NaN` parse as floats, and the int64 conversion wraps
  them negative, slipping past the `MaxWait` cap and sleeping zero:
  a misbehaving gateway got up to MaxRetries+1 back-to-back requests
  instead of the documented fail-fast. Such asks now report no
  retry-after and Retry falls back to backoff; `delay` keeps a
  defensive non-negative check.
- **`mcp` expose path forwards the client's argument bytes verbatim.**
  `argumentBytes` re-marshalled the raw wire value, compacting
  whitespace and escaping `<`, `>`, `&` — a RawTool echoing or
  hashing its arguments saw different bytes than the client sent.
  The raw bytes go through untouched (empty/null still become `{}`),
  pinned byte-for-byte.
- **The governance examples are safe to copy**: the response-cache
  example's map now carries the mutex the Agent's concurrent-reuse
  promise requires, and the PII scrubber scrubs `err.Error()` too —
  the loop renders error text into the transcript verbatim, so a
  success-only scrubber leaked through every failure.
- **`wefttest` fixture names pad the sequence to five digits**
  (`00001-x.json`, ADR 0017 amendment): the replayer loads in listing
  order, and the three-digit pad sorted `1000-x` before `999-x`, so a
  1000+-request recording with a recurring key replayed out of order.
  Replay matches on the key, so old-name fixtures still replay; the
  committed ones were renamed.

## 0.3.0 — 2026-09-22

The Phase 2a parity round (TODO §2a, `docs/phase2a-plan.md` — not
published with the repo; ADR 0013's 2026-09-22 amendment records the
decisions): the six capability items the cross-check against the
studied frameworks found missing, plus the three stances recorded in
the same ADR pass. Tags cut together: the root and the three adapters
at v0.3.0; `mcp` untouched at v0.1.0. Everything is additive — apidiff
is clean with no allowances. The cycle then went through the repo's
two-axis review process and the fix pass landed inside the same
release: the standards axis found no hard documented-standard
violations (one doc drift, two duplication smells, five
production-readiness findings — all fixed or pinned below), and the
spec axis confirmed the cycle faithful to the plan, with the one
deviation (the OutputDecoder's within-step rule) now a documented,
pinned decision.

### Added — Tool-choice forcing (TODO §2a.1, ADR 0013)

- **`weft.ToolChoice(ToolChoiceConfig{Mode, Name})`** — force a step
  to call some tool (`any`), a named tool (`tool`), or none (`none`,
  with the catalogue still advertised, so a prompt-cache prefix on the
  tool definitions survives a no-calls final step). Works as agent
  option, run option, and per-step via `PrepareStep` — the `Thinking`
  dual shape. Mapped per provider (`required`/named/`"none"`;
  `any`/`tool`/`none` — `disable_parallel_tool_use` merges onto the
  chosen member; `ANY`/`allowedFunctionNames`/`NONE`). New conformance
  case `tool_choice_forcing` (declared via `Caps.ToolChoice`) also
  asserts the request bytes carry the provider's field.

### Added — Request params and the escape hatch (TODO §2a.3, ADR 0013)

- **`weft.Params(RequestParams{Temperature, TopP, MaxTokens, Stop,
  Seed})`** — per-run/per-step sampling folding over the adapters'
  construction options (nil keeps the construction default; a run
  override replaces the struct whole). Adapters gained `TopP`, `Stop`,
  and `Seed` construction options where the vendor has them (anthropic
  has no seed — dropped, documented; google narrows Seed/MaxTokens to
  int32, failing `ErrUnsupported` past the ceiling).
- **`ExtraBody(map[string]any)` / `ExtraHeaders(http.Header)`** per
  adapter — the caller-wins valve for vendor knobs weft has no option
  for: nested maps deep-merge, every other value replaces, and **your
  key wins on conflict** (yours, not weft's — the default-bytes tests
  do not cover what it sends). Construction-time only.

### Added — Richer `Usage` (TODO §2a.4, ADR 0016)

- **`Usage.CachedInputTokens`, `Usage.CacheWriteTokens`,
  `Usage.ReasoningTokens`** — reporting subsets of the two totals
  (which stay inclusive; `UsageLimit` and `Total` are unchanged).
  Filled by all three adapters (anthropic cache read/write, openai
  cached/reasoning details, google implicit-cache and thought tokens).
  On spans under their semconv/v1.41.0 names
  (`gen_ai.usage.cache_read.input_tokens`,
  `…cache_creation.input_tokens`, `…reasoning.output_tokens`), each
  only when non-zero; the slog lines keep the two totals (line
  stability). Wire is omitempty — old event JSON round-trips.

### Added — Prompt caching (TODO §2a.2, ADR 0013)

- **`anthropic.PromptCache()`** — opt-in `cache_control` breakpoints at
  the three stable prefix edges (system block, final tool definition,
  trailing conversation edge); without it, no marker anywhere. Cache
  writes bill 1.25×, reads 0.1×; see the README cost note and the
  `Usage` splits for the measurement.

### Added — Externally-computed tool results (TODO §2a.5, ADR 0007)

- **`weft.Resolve(callID, content)` / `weft.ResolveError(callID,
  content)`** — resume a parked call with a result computed outside the
  process; the handler never runs, the content becomes the tool result
  verbatim (capped by `MaxResultBytes` as any result), composes with
  `Approve`/`Deny` in one resuming call. **Behaviour note:**
  `Resolve` on a call that is not pending is a loud run error at
  step 0 — a deliberate asymmetry with `Approve`/`Deny`, which ignore
  unknown ids.

### Added — Streaming partial structured output (TODO §2a.6)

- **`weft.NewOutputDecoder[Out]()`** — a decoder value fed from the
  run's events; `Feed` returns best-effort partials as `submit_output`'s
  arguments stream (lenient closed-prefix decode, hand-rolled in
  `partial_json.go`, fuzzed), `Result` follows the `OutputOf` rule.
  UI-only; never model-visible.

### Fixed — the review round (2026-09-22)

- **`ExtraBody`/`ExtraHeaders` snapshot at construction** (all three
  adapters): the options captured the caller's nested maps and header
  slices by reference, so mutating them after `Model()` raced
  concurrent runs. Values are deep-copied when the option applies
  (`adapterkit.CloneJSON`, header slices cloned); pinned per adapter
  by `TestExtraBodySnapshot`.
- **A negative `RequestParams.MaxTokens` fails the run at the step
  that carries it** — named in the error text, the `validateToolChoice`
  stance. Before, the adapters improvised: openai forwarded it into an
  opaque API error, google dropped it silently, anthropic folded it to
  the default. Explicit zero keeps its documented per-adapter meaning.
  Pinned by `TestParamsValidation`.

### Changed — internal (the review round)

- **The truncated-JSON closer lives once**, in `internal/jsonclose`,
  imported by both `partial_json.go` and `mw/repairjson.go`. The two
  copies had already drifted (the trailing-comma rule); the plan's
  "duplicated on purpose" rationale was wrong — `mw` importing an
  `internal/` package of its own module breaks no rule (the
  `adapterkit` precedent). Behaviour change rides along for
  `closedPrefix`: a trailing comma *run* now closes (it dropped back
  to the member boundary before) — strictly more salvage.
- **`OutputDecoder`'s within-step rule is last-call-wins, pinned.** A
  fresh `submit_output` `ToolStart` resets the buffer, so `Result`
  follows the last call — the `OutputOf` rule the godoc always named;
  the plan's "concatenates within a step" wording is amended in place
  (two distinct calls' arguments never decode as one document).
  `BenchmarkOutputDecoder` pins the documented cost shape (~0.9µs per
  delta at a 3 KiB form; the per-delta re-close is quadratic in
  submission size by design).
- The loop skips the resume pending-id set when a run carries no
  decisions, and a `cap`-shadowing local is renamed.

### Docs

- ADR 0013 amended (tool choice, params + escape hatch, cache markers,
  the provider-executed-tools stance); ADR 0007 amended (resolve);
  ADR 0014 noted (handoff isolation stands); ADR 0016 noted (usage
  split attributes); ADR 0017 amended (replay key gains `ToolChoice`,
  not `Params`). README: sampling/forcing paragraph, the prompt-cache
  cost note, and "Seams are the product" (governance middleware stays
  examples — TODO §2a.9). Godoc examples: `ExampleToolChoice`,
  `ExampleParams`, `ExampleOutputDecoder`; `examples/approval` gains
  the `run_sql` resolve path.
- The review round's docs: ADR 0013's capability matrix gains the
  Tool choice column (all three adapters ✓, with each provider's wire
  tokens); `RequestParams`, `ExtraBody`/`ExtraHeaders`, and
  `OutputDecoder` godocs state the new rules;
  `docs/phase2a-plan.md` §8.1 carries the two dated amendments.

### Live runs owed (carried debt — no provider keys on the build machine)

v0.3.0 ships on the offline suites, as v0.2.0 did (ADR 0013's
fixtures-and-live-runs rule). When keys exist, `make live` must prove:
the full `conformance.Run` table including `tool_choice_forcing` on
all three providers; `PromptCache`'s measured payoff
(`CachedInputTokens > 0` on step 2+ of a long-transcript run);
openai/google `CachedInputTokens`/`ReasoningTokens` non-zero against
real responses; and `mw.Retry`'s structural `HTTPStatus`/`RetryAfter`
extraction against real openai-go / anthropic-sdk-go / genai error
values (the same §3.5/§4.1 debt, now grown by 2a's request-byte
changes).

## 0.2.0 — 2026-09-19

Everything unreleased since 0.1.0, tagged as one set (ADR 0005): the
middleware seams and the approval boundary (the 2026-09-14 round), the
loop controls and subagents as tools plus two full review passes (the
2026-09-18 round), and MCP both ways, observability, and wefttest
record/replay (the 2026-09-19 round). Tags cut together: the root and
the three adapters at v0.2.0, `mcp` new at v0.1.0. The two groups
marked "read before upgrading" are the behavior changes to check
first.

### Added — Testing conventions: replay, wefttest growth, fuzz in CI (TODO §9, ADR 0017)

- **`wefttest.Record(t, dir, inner)` / `wefttest.Replay(t, dir)`** —
  record/replay at the `weft.Model` seam, so application tests run
  against what a real model actually said, offline and deterministically.
  Fixtures are one pretty-printed JSON file per request (reviewable in a
  diff; re-recording is the review), keyed on the request's messages,
  tool names, thinking level, and sequential flag — not the system
  prompt, so a prompt tweak does not invalidate fixtures. A miss fails
  loudly with `wefttest.ErrNoFixture` naming the directory, key, and
  first user text; repeated identical requests replay in recorded
  order. Both are ordinary `weft.Model`s (middleware, `PrepareStep`,
  and subagents run unchanged above them); the conformance suite is
  green against a `Replay`, and committed recordings under
  `wefttest/testdata/replay/` prove a fresh checkout replays with no
  key and no network. No new dependency; nothing under `weft/` proper
  changed. Adapters keep their wire-level `.sse` fixtures (ADR 0013) —
  replay answers the *application* question, fixtures the *adapter*
  question.
- **`wefttest` scripting helpers**: `Args(v)` (typed tool arguments),
  `Raw(events...)` (verbatim events — the one-liner for contract
  violations and signed reasoning blocks), `SayThenFail(text, err)`
  (mid-stream failure), `Turn.WithUsage(u)`, `Request` matchers
  (`HasTool`, `ToolNames`, `LastText`), and `Model.LastRequest()`.
- **`make fuzz`** runs every fuzz target of the root module (10 s
  apiece, `FUZZTIME` overridable) and a dedicated CI job gates it,
  uploading crashers on failure; a crasher becomes a committed
  regression seed (the `FuzzRepair` precedent).

### Added — Observability: OTel spans and slog lines (TODO §8, ADR 0016)

- **The core's first and only dependency: the OTel API**
  (`go.opentelemetry.io/otel` v1.46.0, `semconv/v1.41.0` — the newest
  semconv package carrying the GenAI group). Every run reports its own
  spans — `invoke_agent` per run, `chat` per model call, `execute_tool`
  per executed tool call, children nested under their parents, GenAI
  semantic attributes, no message or tool-argument content on any span —
  through the global provider, so setting up an SDK is the whole
  integration; no weft option needed. Cost with no SDK registered: six
  allocations and ~230 ns per span (`BenchmarkObserverNoop`).
- **`weft.TracerProvider(tp)`** replaces the global provider for one
  agent (tests and DI programs never touch the global).
- **`weft.Logger(l)`** writes one Debug line per phase — run start, run
  finish, model call, tool call — ids, model, durations, usage, stop
  reasons, outcomes; default `slog.Default`, resolved at log time,
  silent unless Debug is on; lines carry the span context so an
  OTel-bridging handler correlates them for free.
- New module **`examples/otel`** (own `go.mod`, in `go.work`): the real
  SDK with a stdout exporter, plus a test asserting the span tree
  through the SDK's in-memory exporter — offline.
- Amends ADR 0004 (OTel and slog are the loop's own reporting, not the
  tap's) and ADR 0005 (a `version` constant for instrumentation
  version). `Tap` is unchanged and now receives the span-carrying
  context.
- Review pass (2026-09-19): `error.type` for weft's sentinels is a
  snake_case token (`max_steps`, `model_contract`, …; table in ADR
  0016), not the sentinel's sentence; a cancelled tool call reports
  `context.Canceled` like the run; the `chat` span takes the loop's own
  finished flag instead of inferring it from an empty stop reason;
  the README states precisely what error text travels. A second pass
  the same day: a panic nothing contains — a PrepareStep function —
  ends the run span (`run_panicked`) and re-panics instead of leaking
  the span; ADR 0016's dependency closure drops `golang.org/x/sys`
  (it appears only in `examples/otel`'s go.sum); the ADR 0005
  amendment the entry above cites is written.

### Fixed — ParseSchema: the structured view is lenient (ADR 0003 amendment)

- A foreign schema whose keyword shape the `Schema` struct cannot hold
  — `"additionalProperties": false` (what zod-built TypeScript servers
  emit), a type array `["string","null"]`, tuple or boolean `items`, a
  non-string description — no longer fails `ParseSchema`: the keyword
  stays in the stored bytes (what the model sees) and the structured
  view leaves it zero (what readers walk). The document itself is
  still checked: invalid JSON, trailing data, or a non-object top
  level fail at import. The anthropic adapter now forwards every
  top-level keyword it cannot map (a foreign `$defs`, `oneOf`, …)
  onto the wire, so a `$ref` inside properties never dangles; the
  anonymous-field exemption matches `encoding/json`'s exactly (an
  unexported non-struct anonymous field stays out whatever its tag).

### Added — MCP, both ways (TODO §7, ADR 0015)

- **`mcp.AddTools(s, tools...)`** and **`mcp.Serve(s, agent, description)`**
  expose weft tools and agents as an MCP server: each tool listed with
  its own contract, every failure an `isError` result carrying weft's
  pinned text, the agent tool literally `weft.Subagent`, and the agent's
  tools dispatched through its chain (approval answers loudly; `AddTools`
  refuses gated tools outright). New module **`weft/mcp`**; the official
  Go SDK is pinned at v1.8.0 and aliased `sdk`.
- **`mcp.Tools(ctx, sess, opts...)`** imports a connected session's tools
  as ordinary weft tools (`RawTool`; schema bytes verbatim through
  **`weft.ParseSchema`**), shaped by **`mcp.Prefix`**, **`mcp.Policy`**
  and **`mcp.ErrToolError`**. Foreign tools are `Sequential` unless the
  server marks `readOnlyHint`; `structuredContent` wins in result
  rendering; transport failures are `mcp: `-prefixed tool errors.
- Core, additions only (`make apidiff`): **`weft.ParseSchema`** (foreign
  bytes kept whole; `Schema.MarshalJSON` emits them),
  **`(*Agent).Name`**, **`(*ToolDef).RequiresApproval`**.
- **Wire change, named:** unconstrained schema nodes reach OpenAI and
  Anthropic as `{}` where the hand-built map wrote `{"type":""}` — no
  test pinned the old bytes; ADR 0003's amendment records the change.

### Fixed — review pass (2026-09-19)

- `make build/test/vet/lint/tidy/offline/live` fail fast per module: a
  failing module no longer reads green because a later one passes (the
  loop kept only the last status).
- `TestToolsTransportFailureIsData` could hang the suite: the SDK's
  shutdown waits for an in-flight handler whose request ctx cancels only
  after that wait — the severed-transport call now carries its own
  deadline, the way a run's tool `Timeout` bounds it in production.
- Gemini's fallback for a foreign schema the `genai` decoder rejects maps
  the structured fields recursively — nested properties and items
  survive, not the top level alone.
- `weft.ParseSchema` no longer rejects a legal keyword shape the `Schema`
  struct cannot hold — `"additionalProperties": false` (every zod-built
  TypeScript MCP server emits it), a type array, tuple or boolean
  `items`, a non-string description. The structured view leaves such a
  field zero; the bytes still cross whole. One such tool used to fail
  the whole `mcp.Tools` import.
- `mcp.Tools`' image and audio markers report the payload's own byte
  count; the SDK already decodes the wire's base64, so `DecodedLen` on
  it under-reported by a quarter.
- The 2026-09-19 embed rule is narrowed to what `encoding/json` does: a
  tagged anonymous field of an unexported *non-struct* type is ignored
  on the wire, so the schema no longer advertises it as required.
- **Anthropic dropped a foreign schema's top-level keywords** other than
  `properties`/`required`/`additionalProperties`: `$defs` (so every
  `$ref` dangled and the API rejected the tool), `$schema`, top-level
  `oneOf`. Every other top-level key now rides `ExtraFields`; pinned.
- `Serve`'s tools answer with `structuredContent` when they advertise an
  `outputSchema`, as `AddTools`' already did (the spec's MUST).
- A client that omits `arguments` hands an exposed `RawTool` `{}`, not
  `null` — the consume side's rule, now on both sides.
- An `isError` result with no content reads `ErrToolError`'s text
  instead of an empty string; a `ResourceLink` item renders as
  `[resource <uri>]` instead of the opaque `[content]`.
- `ExampleTools_toolSource` guards the refreshed slice with a mutex, as
  the godoc now says: the SDK's `listChanged` handler and the loop read
  it from different goroutines.
- `TestAddToolsRefusesApprovalGated` ran one of its three cases (a
  recover deferred in a loop unwound the test); all three run.

### Added — option composition (TODO §5.10)

- **`weft.Options(opts...)`** composes agent options into one value,
  applied in order — a plugin is `func(deps) weft.Option`, dependencies
  are parameters, never globals. **`weft.ToolOptions(opts...)`** is the
  per-tool counterpart. Nil entries ignored; duplicates still panic.

### Added — PrepareStep, the one loop knob (TODO §5.5, ADR 0006 amendment)

- **`weft.PrepareStep(fn)`** — a function the loop calls before every
  model call with the request it built (raw instructions, transcript,
  tool snapshot). What it returns is what the step both advertises and
  dispatches against (validated like a tool-source snapshot); snippets
  compose after it, from the returned tools; the model seam sees the
  prepared request; the transcript is never touched. Several options
  chain in order; an error fails the run with the caller's sentinel
  reachable. Not a third seam — the loop-level knob beside `StopWhen`
  (ADR 0006 amendment).

### Added — loop detection (TODO §5.4, ADR 0002 amendment)

- **`weft.DetectLoops(repeats)`** fails a run with
  **`ErrLoopDetected`** when `repeats` consecutive steps request the
  same set of tool calls — names and raw argument bytes, sorted, calls
  only (results never enter the signature). Off by default; the
  manifest records it when on.

### Added — model retry hints (TODO §5.2, ADR 0002 amendment)

- **`weft.ModelRetry(hint)`** — a tool error rendering as
  `RETRY: <hint>` that asks the model to try the call again with the
  hint applied. The loop counts RETRY results per tool name (middleware
  retries included) and fails the run with **`ErrModelRetriesExceeded`**
  after more than **`weft.MaxModelRetries(n)`** (default 3) consecutive
  asks; a success resets the count. The manifest policy records
  `max_model_retries` (the only §5 change that touches a committed
  golden; `examples/getting-started/weft.json` regenerated).

### Added — usage limits (TODO §5.3, ADR 0002 amendment)

- **`weft.UsageLimit(max weft.Usage)`** bounds a run's total token
  usage, subagents included, checked at the continuation point — only
  when the loop would otherwise make another model call; a run-ending
  step may overshoot and still succeed. Breach fails the run with
  **`ErrUsageLimit`** (wrapped with the numbers) and the partial
  transcript. Off by default; the manifest policy records it
  (`usage_limit`, zero fields omitted).

### Added — subagents as tools (TODO §5.1, ADR 0014)

- **`weft.Subagent(name, description, child, opts...)`** — a tool whose
  handler runs another agent on the prompt alone; the child's events
  arrive in the parent's stream wrapped in the new **`weft.Nested`**
  event (wire `nested`, recursive through `UnmarshalEvent`), numbered
  from the parent's counter under the parent's ordering lock; the
  child's usage rolls into `RunResult.Usage` and is recorded per call
  on the new `StepRecord.SubagentUsage`. Every `ToolOption` applies to
  the delegation (`Timeout`, `MaxResultBytes`, `RequireApproval`,
  `Sequential`, `WrapTools`); the parent's `Parallelism` bounds
  concurrent delegations.
- **Child failure is data**: `SUBAGENT_FAILED: agent "x" failed at
  step N: …` (the child's `*RunError` on `ToolError.Err`), a child
  ending pending is `SUBAGENT_PENDING: …`, and a delegation to an
  agent already running in the call chain is refused before any model
  call with `SUBAGENT_CYCLE: …`. Codes exported as
  `CodeSubagentFailed`/`CodeSubagentPending`/`CodeSubagentCycle`.
- **Lineage ids**: a child run's id is `<parent>/<step>/<callID>`
  (`<parent>/resume/<callID>` under `Approve`), visible on the nested
  `RunStart` and on `CallFromContext` inside the child.
- **The late-event rule** (ADR 0004 amendment): no `Nested` event and
  no usage record for a call is delivered after that call's
  `ToolFinish` — the close is atomic with the finish under the
  parent's lock, so replayed streams never show a finished call
  continuing.
- **Manifest**: tools from `Subagent` carry `"subagent": "<child
  name>"` (omitempty; existing goldens unchanged). **wefttest**:
  `Flatten` unwraps `Nested` events recursively for assertions.

### Fixed — §5 review pass (2026-09-19)

- `PrepareStep` functions now receive a deep copy of the request: the
  previous slice-level clone shared each message's parts and the frozen
  registry's `*ToolDef` pointers, so an in-place write — dropping a
  part, re-forming a tool call's argument bytes, rewriting a
  definition's fields — corrupted the run transcript and the agent for
  later runs, against the documented "mutate freely" promise (ADR 0006
  amendment). Message parts, argument bytes, and tool definitions are
  cloned at that boundary; the model seam keeps the lighter slice
  copies under the adapter read-only contract.
- A typed-output child that submitted with no argument bytes (empty
  decodes as `{}`) now counts as submitted: the delegation returns the
  empty bytes instead of falling back to the child's final text,
  matching what `OutputOf` decodes (ADR 0014, G4).
- Added the missing godoc example for `ToolOptions`, the untested
  `resume` lineage id (`<parent>/resume/<callID>`, asserted through an
  approved delegation), and a guard comment that described a sort as a
  counter compare.
- `docs/life-of-a-call.md` drew the budgets before the `StopWhen`
  check; the loop checks `StopWhen` first and the budgets only at the
  continuation point (ADR 0002 amendment, AGENTS rule 13). The diagram
  now matches the code. ADR 0012 now records the three §5 policy keys
  (`max_model_retries`, `usage_limit`, `detect_loops`) the manifest
  had been writing without an entry.
- Pinned four behaviours the §5 suite left implicit: cancelling the
  parent mid-child under `Stream` delivers the cancellation last and no
  `RunFinish` at either level; a child's `RETRY` results feed the
  child's counter, never the parent's; concurrent runs on one
  orchestrator keep `Nested.RunID` and `Seq` per run; `PrepareStep`
  over a `ToolSource` consults the source once per step and dispatches
  against the prepared subset.
- Second round (deep review of the §5 plan against the tree):
  `CodeSubagentFailed`'s godoc claimed the child's cause is "never
  shown to the model" while the handler renders it into the
  model-visible message — the comment now states the real contract
  (message carries the cause; the `*RunError` stays on `ToolError.Err`
  for `errors.As`). `MaxModelRetries`' godoc now records that calls
  resumed under `Approve` do not feed the counter (they belong to no
  step, ADR 0007), as ADR 0002 already did. The "last valid
  `submit_output`" walk existed twice — `OutputOf` and a Subagent
  delegation's `submittedJSON` — and is now one `lastSubmitted` helper
  both call, so the "exactly the bytes `OutputOf` would decode" promise
  cannot drift. `nestFromContext` dropped its never-read ok flag.
  `make lint` now works from a bare shell like `make apidiff` does
  (GOPATH/bin on PATH); golangci-lint is 0-issues across the four
  modules.

### Fixed — go1.26 decode-error compatibility (follow-up to the review pass)

- The schema walk behind `INVALID_INPUT` messages now handles toolchains
  (go1.26) that omit map keys from `encoding/json`'s error field path
  (`meta.when` for an error under `meta["k"]`): a segment at a map
  schema is first tried as a property of the value schema before being
  taken as a key, so the expected type named is still the advertised
  one. The `",string"` mismatch tests accept both toolchain renderings
  — the newer UnmarshalTypeError form (field named) and the older
  plain-error form — both speak the schema's vocabulary.

### Changed — the 2026-09-18 code-review pass (all 43 findings)

Schema correctness (ADR 0003's same-day amendment):

- **Embedded shadowing now tracks depth, encoding/json's real rule**:
  a name claimed at two embedding depths keeps the shallower
  contribution (the schema previously cancelled a name the wire still
  carried), and at equal depth exactly one json-tagged claim beats
  untagged ones (the schema previously kept the last writer — for a
  struct with both a tagged int and an untagged bool claiming "Name",
  it advertised a boolean the wire never emitted). Any other
  equal-depth tie cancels the name, as encoding/json drops it. One
  deliberate, conservative divergence: in a double diamond the schema
  cancels where encoding/json's breadth-first resolver may keep — the
  cancelled side never advertises what decode cannot reliably
  deliver.
- **Two fields of one struct claiming one JSON name panic at
  construction** (always both tagged), at any *named* nesting depth:
  one handler field can never receive a value. Inside an embedded
  struct the same collision instead flattens into the parent's
  dominance rules and drops, matching encoding/json. The
  duplicate-name and non-struct-input panics set the precedent.
- **Decode errors speak the schema's vocabulary**: the expected type
  in `INVALID_INPUT: … field "x": expected T, got U` is read from the
  tool's advertised schema, walked along the error's field path, so
  `[]byte` fields report "expected string" and a `,string` integer
  reports `field "n": expected string, got number` — never the Go kind
  the schema did not show. The Go-type mapping is the fallback for a
  path the walk cannot resolve.

Run and loop correctness:

- **Approval-resume results complete the earlier step's tool message
  in the assistant's call order** (ADR 0007 §3), not appended after
  the earlier run's results — Gemini matches functionResponses by name
  and position, so a reordered tool message could attach a result to
  the wrong call. The seams test is order-sensitive now and a contract
  pin covers the mixed approved/earlier shape.
- **The terminal `RunFinish` decides the run's outcome.** One check now
  governs both the event's delivery and the return value: a delivered
  `RunFinish` is always followed by success, and a cancellation that
  lands between the loop's last ctx check and the emit fails the run
  with the cancellation error (the resumable result on it) instead of
  returning success with no `RunFinish` — or, at the approval boundary,
  delivering `RunFinish` and then failing. Rule 4 and `Run.Events`' one
  terminal element now hold under every interleaving; pinned by a tap
  that cancels on the final `StepFinish`.
- `StepFinish`'s doc now says what the loop does: the event follows the
  step's tool events and precedes the stop-condition check (no wire or
  order change).
- **A tool-source snapshot with a nil entry fails the run with the new
  `ErrNilTool`** (the `ErrDuplicateTool` pattern) instead of silently
  advertising a nil every adapter dereferences into a misleading
  `ErrModelContract` panic.

Deny mode is a gate, not an aspiration:

- **`WEFT_MODEL_REQUESTS=deny go test ./...` is green workspace-wide**
  (`make offline`, pinned in CI). The kill switch now guards clients
  the adapters build from credentials; a client injected through the
  adapters' `Client(c)` option is a test double by construction and
  stays reachable (ADR 0013's amended clause) — the switch no longer
  breaks weft's own fixture suites in the mode it exists for. The
  `ModelRequestsAllowed` meta-test is ambient-aware.

Adapters (ADR 0013's appendices updated in the same change):

- **Empty content never reaches the wire** in openai and google, the
  2026-09-14 rules anthropic already had: a reasoning-only assistant
  message is skipped (`{"role":"assistant"}` was API-rejected), empty
  text parts are dropped, and Google skips Contents reduced to zero
  parts.
- **openai normalizes empty tool-call arguments to `{}`** on the
  convert side, matching its own stream side and the other adapters; a
  replayed nil-args transcript no longer 400s as "arguments is not
  valid JSON".
- **openai's `parallel_tool_calls` hint is sent only alongside a tool
  catalog** — several compatible servers reject the hint without one.
- **Synthesised `call_<i>` ids skip ids the server already populated**
  in the same step (openai, google): a collision failed the run with
  `ErrModelContract`, the failure the synthesis exists to prevent.
- **openai overwrites repeated function-name fragments** instead of
  concatenating them ("pingpingping").
- **google streams a thought signature riding an empty non-thought
  text part** — Gemini validates its return on the next request.
- **google's int32 ceilings fail loudly** wrapping `ErrUnsupported`
  (`MaxTokens`, `Thinking.Budget`) instead of wrapping around into a
  garbage wire value.
- **Tool defs are converted per request.** The pointer-keyed
  `sync.Map` never evicted, and a `ToolSource` that rebuilds its
  snapshot per step — the seam's documented use — grew it without
  bound. Measured first (`openai.BenchmarkConvertTool`): ~2–5µs /
  62 allocs per conversion against the network round trip every
  request also pays.
- **SDK-free helpers moved to `internal/adapterkit`** (schema
  rendering, the terminal-error rule, the FilePart guard): three
  byte-identical copies become one; the root module's public API is
  untouched.

The gate and the tooling:

- **The apidiff gate fails closed.** A tree that does not compile, or
  an apidiff run that dies before writing a report, can no longer read
  as "no incompatible changes"; the gate builds both sides first and
  distinguishes execution failure from a clean diff. A self-test
  exercises all three modes (`make apidiff-selftest`), CI pins the
  apidiff version the way golangci-lint is pinned, and the offline
  deny gate is its own CI job.

Test support and middleware:

- The conformance suite gained the two cases the README already
  claimed: `tool_args_delta` (fragments surface live before the call's
  `ToolStart`; `Caps.ToolArgDeltas` declares it — Google's calls
  arrive whole) and `slow_stream` (a dripping stream under a tight
  idle timeout must succeed; only a true stall fails).
- `wefttest`'s `Requests()` records exhausted calls too (real requests
  that they are), and the scripted model yields `ctx.Err()` before its
  scripted error, matching the Model contract the adapters enforce.
- `mw.RepairJSON` strips any fence language tag (```JSON`,
  ```javascript`), not just lowercase `json`, and no longer
  eats literal "json" content after a bare fence. `mw.MaxWait(0)`
  means "no cap"; every mw option's ignore rule is documented like the
  core's. `Retryable`'s `ErrModelContract` exclusion is pinned in the
  classifier test.
- The loop's own failure codes are exported constants —
  `CodeInvalidInput`, `CodeNoSuchTool`, `CodeDenied` — so external
  policy middleware produces the contract strings without duplicating
  them (`mw.Allow` already uses `CodeDenied`).
- `Replay`/`ReplayPolicy` and the manifest's `replay` key are
  **retracted** until the checkpoint store ships (ADR 0006's
  amendment): the annotation had no consumer before `store` exists.
  They return with it.

### Changed — run & event semantics (read before upgrading)

- **A tool-call ID repeated within one step fails the run** with
  `ErrModelContract`, like an empty ID: a repeated ID made `Repair`
  drop the second result and left `Approve`/`Deny` keyed on it
  ambiguous. IDs may still repeat across steps.
- **Tool arguments must be exactly one JSON value.** Trailing data
  after the arguments object (`{"a":1} {"a":2}`, `{"a":1} x`) is an
  `ErrInvalidToolInput` result in lenient and strict modes alike;
  the decoder previously took the first value and ignored the rest.
- **The truncation marker names the bytes omitted.**
  `…[truncated N bytes]` now carries N = bytes the model did not
  receive; the marker previously printed the cap, which read as "N
  bytes missing" however many were cut. The marker's shape is
  unchanged (ADR 0002, 2026-09-18 amendment).
- **Events carry `RunID`.** Every event except `RunStart` (whose `id`
  is the run's) carries `run_id` on the wire, so taps and stream
  consumers can attribute events under concurrent runs; the per-run
  `Seq` counter was already unique only within its run. Old recordings
  without the field decode with an empty `RunID`.
- **Events are snapshots.** `ToolStart.Args` and `RunFinish.Pending`
  no longer alias the transcript's byte slices: writing into a
  received event cannot corrupt the run. The copies ride tool-event
  frequency, not delta frequency — no benchmark movement.
- **A step consults its `ToolSource` exactly once.** Advertising, the
  sequential barrier, and dispatch resolve against one per-step
  snapshot, so a source that changes mid-step can no longer produce an
  advertised-then-`NO_SUCH_TOOL` failure or a barrier that disagrees
  with the executed def. A tool registered mid-step becomes callable
  on the next step (the per-step refresh `TestToolSource` always
  modelled). A snapshot with a duplicate name now fails the run with
  the new `ErrDuplicateTool` instead of silently resolving
  first-wins; `Agent.CallTool` reports the same condition as an error.
- **Registered tools are frozen at `New`.** The agent keeps a deep
  copy; mutating the value you passed in (fields or schema trees)
  after construction no longer reaches dispatch, advertisement, or a
  running run. `Agent.Tools` returns deep copies too. The "immutable,
  reusable, concurrent" contract now holds by construction.
- **`ModelRequest` hands adapters copies.** `Messages` and `Tools`
  are fresh slice copies per request; a hostile or careless `Model`
  can no longer corrupt the transcript or the agent's tool list at
  slice level. `wefttest`'s mock already cloned both — loop, contract,
  and test double now agree.

### Added — the rest of the 2026-09-18 round

- `Run.Close` releases an abandoned run's resources (idempotent; a
  run you will consume needs no Close — Events and Wait release
  everything themselves).
- `Agent.TapPanics` counts contained tap panics, so a dead observer
  is no longer invisible.
- `Schema.AdditionalProperties` types map values (`map[string]int` →
  an object of integers; `map[string]any` stays a bare object — an
  `any` value type has nothing to say) and rides the wire in the
  manifest and the OpenAI and Anthropic adapters. The Google adapter
  drops it: Gemini's schema subset (and the genai SDK's `Schema`) has
  no `additionalProperties` field. Wire output for schemas without
  typed maps is byte-identical.
- `wefttest.ConformInfo` / `ConformInfoT` check that model middleware
  forwards the inner model's identity (the Info convention, checked).
- `ExampleTap_async`: the supported pattern for slow observers — hand
  each event to a queue inside the tap, drain on your own goroutine.
- `Run.Events` and `Tap` docs now state the consumer-speed coupling:
  tool events are emitted under the step's ordering lock, so a slow
  consumer gates the start of subsequent tools.

### Fixed — the rest of the 2026-09-18 round

- Embedded-struct schema derivation now follows encoding/json's
  shadowing rules: a struct's own field wins over an embedded one with
  the same JSON name (its type *and* its required flag, whatever the
  declaration order — previously the property was last-writer-wins and
  `required` could name it twice, an invalid schema), and the same
  name from two embedded structs cancels instead of surviving with a
  nondeterministic type. `required` stays in declaration order.
- The json `,string` option is reflected: the property is typed
  `string`, the quoted wire form. Previously the schema advertised the
  bare type, so a schema-following model's unquoted value was
  rejected (`invalid use of ,string struct tag`).
- `Repair`'s purity is pinned: the input is never mutated
  (fuzz-checked byte-for-byte), and the synthesis append no longer
  relies on whose backing array the kept tool message uses.

### Changed — model-visible contracts (read before upgrading)

- **A `max_tokens` step with tool calls executes none of them.** Every
  call gets the error result `tool call <name> was not executed: the
  response hit the output token limit` and the loop continues so the
  model retries with a full budget. Previously intact calls ran and
  only cut arguments failed decoding. (ADR 0002 amendment, TODO §5.6a.)
- **The loop's own tool failures are coded.** Undecodable arguments
  render `INVALID_INPUT: tool "x": field "days": expected integer, got
  string` (was `weft: tool input is not valid for its schema: …`) and
  an unknown tool `NO_SUCH_TOOL: no tool named "x"` (was `weft: no tool
  with that name: "x"`). `errors.Is` on the sentinels is unchanged.
- `Sequential` returns `PolicyOption` (source-compatible); `RunFinish`
  gained `Pending` and is no longer `==`-comparable.
- `Agent.CallTool` now runs the tool middleware chain, and applies the
  agent-level `StrictInput` exactly as the loop does.
- A run whose context is canceled while calls are parked for approval
  fails with the cancellation error (the parked calls stay resumable on
  `RunError.Result.Pending`) — cancellation wins, as everywhere else.

### Added — the rest of the 2026-09-14 round

- **The two middleware seams** (ADR 0006): `WrapModel(mw
  ...ModelMiddleware)` and `WrapTools(mw ...ToolMiddleware)` — chi-style,
  first listed outermost; `WrapTools` also works on one tool.
  `ToolCaller`, `ToolMiddleware`, `ModelMiddleware`, `InfoOf`.
- **Package `mw`**, the reference middleware: `Retry` (backoff with
  jitter, retry-after honoured, >60s asks fail fast, overflow never
  retried), `Fallback`/`FallbackWhen`, `Log`, `RepairJSON`; `Allow`,
  `Audit`, `MapErrors`.
- **`ToolError`** — `{Code, Message, Err}` renders `CODE: Message`; the
  cause is for middleware and logs only. `weft.Errorf(code, format,
  ...)`; several `%w` verbs keep every cause reachable.
- **The approval boundary** (ADR 0007): `RequireApproval()`,
  `RunResult.Pending`, `RunFinish.Pending`, `Approve(id)`, `Deny(id,
  reason)`, `Call.Approved`, `ErrApprovalRequired`, `ErrApprovalDenied`;
  `examples/approval`.
- Per-tool policy: `Sequential()` on a tool is a barrier;
  `PromptSnippet(text)` composes into the instructions; `Replay(policy)`
  annotates checkpoint restart (`ReplaySafe`/`ReplayNever`). All in the
  manifest. `Timeout(0)` on a tool removes the agent's default for
  that tool alone (the manifest records `"timeout": "0s"`), the
  timeout analogue of `MaxResultBytes(0)`.
- The apidiff gate (`scripts/apidiff.sh`, `make apidiff`, CI job) with
  the pre-1.0 `.apidiff-allow` acknowledgement file.
- `docs/life-of-a-call.md`: where every phase of a step and a tool call
  sits.

- Per-run reasoning control: `weft.Thinking(weft.ThinkingConfig{...})`
  — an agent option sets every run's default, a run option overrides
  one — on a provider-neutral scale (`ThinkOff/Low/Medium/High`, plus
  an optional token `Budget`). Anthropic maps it to
  `thinking:{disabled|adaptive}` or `budget_tokens`; Gemini to
  `thinkingBudget`/`thinkingLevel` (and asks for thought summaries
  back); the OpenAI adapter sends `reasoning_effort` on the official
  API and injects a `thinking` object for the known gateway hosts
  (z.ai, bigmodel.cn, moonshot.ai/cn), with `openai.Dialect(...)`
  pinning the wire form (`DialectNone` for strict servers). Declared
  gaps: Chat Completions has no off switch or budget form — `ThinkOff`
  and `Budget` are dropped in the effort dialect.
- Streamed tool-argument progress: a new `ToolArgsDelta` event (wire
  type `tool_args_delta`) surfaces argument fragments live while the
  model writes a tool call; the assembled call still arrives as
  `ToolStart` when it executes. OpenAI and Anthropic adapters emit it;
  Gemini calls arrive whole and yield none.
- Per-tool policy: trailing options on `Tool` — `Timeout`,
  `MaxResultBytes`, `StrictInput` — override the agent's defaults for
  that tool alone.
- Structured output: `Output[T]`, `GenerateAs`, and `OutputOf`
  constrain the final answer to a struct via a `submit_output` tool
  (ADR 0008); undecodable tool arguments come back as schema-shaped
  errors naming the field.
- Conformance suite: a `thinking_option` case asserting the option
  threads through the public API without breaking the exchange.

### Fixed — the rest of the 2026-09-14 round

- A canceled stream can no longer fabricate a successful
  `ModelFinish`: all three adapters enforce the Model contract's
  `(nil, ctx.Err())` terminal yield when the caller's context ends
  mid-stream — a canceled run previously could report success with
  partial data.
- anthropic: empty tool outputs travel as a visible "(empty tool
  output)" placeholder, empty user messages as "(empty message)", and
  assistant messages with nothing sendable (only unsigned reasoning)
  are skipped — the Messages API rejects empty content, which failed
  the next model call.
- anthropic: an empty stop reason on a later `message_delta` no longer
  overwrites a real one.
- openai: a streamed safety refusal (`delta.refusal`,
  `finish_reason: content_filter`) is surfaced as text instead of
  being dropped.
- google: a transient failure creating the SDK client on first use is
  retried on the next call instead of being cached forever.
- `StopWhen` conditions (`HasToolCall`, structured output's stop) no
  longer panic when called with an empty step slice.
- mw: `Retry` panicked on a `BaseDelay` below 2ns (jitter computed
  `rand.Int64N(0)`); the backoff now returns such delays unchanged.
- mw: `MaxRetries(0)` kept its documented retry-after behaviour only in
  the docs — the attempt-budget check returned the raw error before the
  fail-fast ran, so `ErrRetryAfterTooLong` was unreachable. The fail-fast
  now outranks the budget.
- mw: the doc.go composition example put `Retry` outside `Fallback`,
  which retries the fallback chain as a whole — the primary gets one
  attempt and its transient failures are never retried. The canonical
  order is `Fallback` outside `Retry` (retry, then fail over).
- all adapters: a chunk landing in the same instant as the idle deadline
  could be reported as `ErrStreamIdle` (select picks uniformly among
  ready cases); a waiting chunk now wins over the timer.
- anthropic: `input_json_delta` of non-`tool_use` blocks (server tools
  such as web_search) leaked as `ToolArgsDelta` progress for a call that
  never arrives; only `tool_use` fragments surface now.
- anthropic: an empty assistant text part and a hand-built tool call
  with nil arguments no longer reach the wire (the Messages API rejects
  empty text blocks and `input:null`; the inbound stream already
  normalised empty arguments to `{}`).

### Changed — the 2026-09-14 round (docs)

- Documentation corrected: the anthropic and openai adapters' zero
  configuration inherits the vendor SDK's transport retry default (2
  retries on 429/5xx/connection errors), and `MaxRetries(0)` cannot
  disable retries — supply a zero-retry client via `Client(c)` if you
  need one. No code changed; the previous docs were wrong.
- Docs: ADR 0013 amended (per-run thinking mappings, argument-delta
  progress, enforced cancellation, empty-content and refusal handling);
  adapter package docs updated to match.

## 0.1.0 — 2026-09-11

Initial public release: the core agent loop (tools from plain
functions, parallel tool execution with defined failure semantics,
typed streaming events, transcript repair, the `weft.json` manifest),
the `wefttest` offline model and conformance suite, and the
openai/anthropic/google adapters.
