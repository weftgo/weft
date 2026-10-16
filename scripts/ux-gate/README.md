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
- `h9.js` — item H9's done line in a real browser: the runs list (toolbar folded, "with
  subagents" nested), `/compare` (its picker, two picks) and a subagent parent's run page fit
  1440 and 360 px with no sideways page scroll, and `/live` during an offline run draws the
  retrying step from the live `attempt` frame, the parent above its child, and keeps the rows a
  minute (a reload included). Run it against a Studio holding the contract fixtures, with
  `DEMO` a command that runs such a director (a wefttest script whose first model call fails
  retryably under `mw.Retry`, delegating to a subagent, through `otel.Install(otel.Studio(…))`):
  `STUDIO=… TOKEN=… DEMO="<binary> <studio url> <token>" PLAYWRIGHT_DIR=… node scripts/ux-gate/h9.js`.
