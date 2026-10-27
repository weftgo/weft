# scripts/ux-gate — the UX audit's gate tooling

- `pw.js` — the Playwright helper (CommonJS): `launch(baseURL)`, `signIn(page, token)`
  (Studio takes the token from a `#token=` fragment), `go`, `shot(page, name)` →
  `$GATE_SHOTS/<name>.png`, `text`, `openPanel(page, base, {token, scope, query})`
  (a host page served on the Studio origin by a Playwright route, loading
  `<base>/panel.js` with `data-endpoint` + `data-token`), `panelText`, `panelClick`,
  `a11y(page)` (axe-core from `$AXE_DIR`, else `studio/web/node_modules`; `null` when absent).
  Playwright is not a repo dependency: point `PLAYWRIGHT_DIR` at a throwaway
  `npm i -D playwright@<version>` whose Chromium revision is in `~/.cache/ms-playwright`
  (1.62 ↔ chromium-1234).
- `gate7.sh` + `gate7.js` + `panel-size.ts` — the phase 7 gate (audit §9, gate 7).
  Run it with
  `ARENA=<weft-arena checkout> RUN=<scratch dir> PLAYWRIGHT_DIR=<playwright install> scripts/ux-gate/gate7.sh`
  (`REPO` defaults to the arena's `go.work` sibling `$ARENA/../weft`, `GO_TEST_DIR` to `$REPO`):
  it builds `weft` from `$REPO`, starts `weft studio` on a fresh `$RUN/studio.db`
  (127.0.0.1:7341, token `gate7tok`), builds the arena as it is and seeds it
  `run --offline --studio --no-save --jobs 8` under `strace -e connect`, ingests the
  `tracer_only` contract fixture, walks the journeys (API, Studio UI, the panel) and
  the principle gate, and prints one PASS / FAIL / RECORDED line per clause to
  `$RUN/gate7.txt` (screenshots in `$RUN/shots`); exit 1 when a clause FAILs.
- `gate8.sh` + `gate8.js` — the phase 8 gate (audit §9, gate 8). Run it with
  `ARENA=<weft-arena checkout> RUN=<scratch dir> PLAYWRIGHT_DIR=<playwright install> scripts/ux-gate/gate8.sh`
  (`REPO` defaults to this script's own tree — the Studio, the demo and the principle
  gate's tests are built from it; the arena still compiles against its own `go.work`
  sibling, which the header names): a fresh `weft studio` on `$RUN/studio.db`
  (127.0.0.1:7341, token `gate8tok`), the arena seeded `--offline` under `strace -e connect`,
  one `examples/studio-local` turn exported to it (its `otel.Slog()` lines: the logs path),
  then gate8.js — (a) the failed-run journey on rw.budget-guards, click by click; (b) the
  subagent tree in place on rw.deep-delegation (`?sel=` after a reload, the graph scale);
  (c) the panel's Timeline against Studio's tree, row for row, and its ⤢; (f) U25's named
  shapes on the run page and the panel's Story; (g) I3's `status=parked`; (h) U11's logs —
  then (e) `h9.js` with a scripted director it writes and builds under `$RUN/demo` (never
  in the repo), (d) `make studio-check` (`STUDIO_CHECK=0` skips it) with the panel ledger,
  and the k-clauses. One PASS / FAIL / RECORDED line per clause in `$RUN/gate8.txt`
  (screenshots in `$RUN/shots`); exit 1 when a clause FAILs.
- `gate9.sh` + `gate9.js` — the phase 9 gate (the conversation, end to end). Run it with
  `ARENA=<weft-arena checkout> RUN=<scratch dir> PLAYWRIGHT_DIR=<playwright install> scripts/ux-gate/gate9.sh`
  (`REPO` defaults to this script's own tree; `PREV_TAG` to v0.17.0, the phase's range): a fresh
  `weft studio` on `$RUN/studio.db` (127.0.0.1:7341, token `gate9tok`); the arena built against
  `$REPO` through a scratch `$RUN/arena.work` (`go 1.26.0`, `use $ARENA $REPO $REPO/core` — an arena
  built against an older weft emits no thread records; the arena is never edited) and seeded
  `--offline` under `strace -e connect` (the known stale check `studio16.search-stopped-by` is
  RECORDED); a content-off thread session exported twice by a demo it writes and builds under
  `$RUN/cdemo` (never in the repo: the agent's `weft.Content(false)`, then the destination's
  `otel.NoContent()`); then gate9.js — (a) acme-support's timeline (thread.support-approvals: four
  parks, their decisions and actors, the grant, the resumes), (b) the denial's drawer and its
  t1 step 1 / t2 links, followed, (c) the pool child session from its parent, (d) the panel
  scoped to acme-support's public id (minted per seed) — its `parked → tN` chips and its
  `timeline ⤢` hand-off in a signed-in browser, (e) content-off on the API (every session and
  turn route) and the page, (f) ADR 0021's "index, not evidence" sentence, (g)–(i) U34's loop
  graph on rw.flaky-infra, rw.budget-guards' poller and rw.tool-storm, (j) the panel's loop line
  against it, (p) the polish batch (the orchestrator's) — then (s) `make studio-check` and the
  panel ledger (the phase 9 floor: headroom ≥ 6,820 B with the last row P9-pre-web-fix6,
  UX-B-41's accepted exception) and k1–k7. One PASS / FAIL / RECORDED line per clause in
  `$RUN/gate9.txt` (screenshots in `$RUN/shots`); exit 1 when a clause FAILs.
- `gate10a.sh` + `gate10a.js` — the phase 10a gate (experiments you can trust, runtimes you can
  see). Run it with
  `ARENA=<weft-arena checkout> RUN=<scratch dir> PLAYWRIGHT_DIR=<playwright install> scripts/ux-gate/gate10a.sh`
  (`REPO` defaults to this script's own tree): a fresh `weft studio` on `$RUN/studio.db`
  (127.0.0.1:7341, token `gate10atok`); the arena built against `$REPO` through `$RUN/arena.work`,
  seeded `--offline` (the failures in `KNOWN_STALE` are RECORDED with their arena notes), then
  `weft-arena serve --offline` (127.0.0.1:7789) with an rw.budget-guards batch and an offline thread
  session — three runtime links of one process; then gate10a.js — (a) the §4.4 experiment journey on
  rw.budget-guards in Studio and the panel (scripted by default, the tool-mismatch hold, the recorded
  build, a scripted Run and its compare, "edit this result" empty; only a Run reading
  `data-run-engine="scripted"` is ever pressed), (b) `/runtimes` told apart, gone, restored after a
  Studio restart over the same db, and the drawer's Runs in refusal, (c) `/agents`' stale marks and
  stats strips, (d) the error-class facet → the matrix form's estimate (never run) — (e) `weft dev --
  studio-local` (7397 / 8093) driven from the page's panel, its app killed, (f) `make mcp-check`,
  (g) `make studio-check` + the ledger (headroom ≥ 4,096 B under 106,496 B, dist ≤ 800 KiB), and
  h1–h7. Every process it starts runs under `strace -e connect` (h4). One line per clause in
  `$RUN/gate10a.txt` (screenshots in `$RUN/shots`), ending `GATE 10a: PASSED` or
  `GATE 10a: FAILED (<clauses>)`; exit 1 when a clause FAILs.
- `gate10b.sh` + `gate10b.js` + `g10bdemo.go` — the phase 10b gate (one system: shared words, scope,
  the onion, the CLI). Run it with
  `ARENA=<weft-arena checkout> RUN=<scratch dir> PLAYWRIGHT_DIR=<playwright install> scripts/ux-gate/gate10b.sh`
  (`REPO` defaults to this script's own tree; `IN_FLIGHT` names ledger items whose open rows are in flight):
  a fresh `weft studio` on `$RUN/studio.db` (127.0.0.1:7351, token `gate10btok`) with every weft process in
  a private discovery world (XDG_RUNTIME_DIR / XDG_CACHE_HOME under `$RUN`, a cwd with no ./.weft); the arena
  built against `$REPO` and seeded `--offline` (`KNOWN_STALE` failures RECORDED with their arena notes);
  `g10bdemo.go` (an ignore-tagged program built under `$RUN/demo`, never in the repo) stamps flow runs, calls
  an `mcp.Server("billing")` in-memory server (a tool error, a refusal, a transport failure), registers a
  governance-shaped agent so its manifest is stored, and keeps a run in flight for /live; the stock_genai
  fixtures through /v1/traces; then (a) the vocab audit and every sampled `<Term>` reached by Tab, the `?`
  Concepts tab, a fresh Studio's empty state and first-run card, (b) the panel by batch tag, flow, tag token
  and token wall, (c) the manifest's chain and the onion in the drawer and the panel's chips, (d) the
  `mcp:billing` graph node, (e) bare `weft doctor`, a second `weft studio`, `weft open --with-token`, the
  wall's `<db>.token`, (f) derived runs, (g) `make studio-check` + the ledger (headroom ≥ 7,500 B, every
  panel.js commit since v0.19.0 with its row), (h) axe over the panel in both themes, (i) `make mcp-check`,
  j1–j7 and (k) the Coverage ledger. One line per clause in `$RUN/gate10b.txt`, ending `GATE 10b: PASSED` or
  `GATE 10b: FAILED (<clauses>)`; exit 1 when a clause FAILs.
- `h9.js` — item H9's done line in a real browser: the runs list (toolbar folded, "with
  subagents" nested), `/compare` (its picker, two picks) and a subagent parent's run page fit
  1440 and 360 px with no sideways page scroll, and `/live` during an offline run draws the
  retrying step from the live `attempt` frame, the parent above its child, and keeps the rows a
  minute (a reload included). Run it against a Studio holding the contract fixtures, with
  `DEMO` a command that runs such a director (a wefttest script whose first model call fails
  retryably under `mw.Retry`, delegating to a subagent, through `otel.Install(otel.Studio(…))`):
  `STUDIO=… TOKEN=… DEMO="<binary> <studio url> <token>" PLAYWRIGHT_DIR=… node scripts/ux-gate/h9.js`.
