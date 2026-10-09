# devtools-vite: the panel from npm in a Vite app

A minimal Vite + TypeScript app (no framework) with a tiny fake chat:
a form that POSTs to `/run` and renders the reply, inside
`<div data-weft-scope="pub_demo">`. The devtools panel comes from the
npm package `@weftgo/devtools`. `src/main.ts` imports it and calls
`mount()`, and no `<script>` tag for the panel appears anywhere: Vite
bundles the package into the app's own JS.

npm is the second delivery of the panel, not the main one. The
canonical install is the Go handler: `studio.Handler` serves
`/studio/panel.js` and one script tag loads it (see
`studio/README.md`). The package ships the same file byte for byte;
this example proves that too.

The package is not published from here. `make devtools-vite-check`
packs `studio/web/npm` (the tarball `npm publish` would upload) into
`vendor/weftgo-devtools.tgz` and installs it beside the pinned
dependencies; `package.json` does not name it.

## The check

From the repository root, after `make studio-build`:

```sh
make devtools-vite-check
```

It packs the package, runs `npm ci` (vite and jsdom at the exact
versions `package.json` and the committed `package-lock.json` pin),
installs the tarball with `npm install --offline --no-save`, then runs
`check.ts` with bun. CI runs the same target after `make studio-check`. The check runs `vite build`, fails if `dist/index.html` has a
`<script src=…panel.js>` tag, loads the built page and JS into jsdom
with a fake Studio answering `/api/meta`, and asserts that
`<weft-devtools>` is defined, exactly one is on the page, and it
asked `/studio/api/meta`. It then compares the installed package's
`panel.js` with `studio/dist/panel/panel.js` by sha256 (skipped, with
a note, outside the weft repository). It prints
`DEVTOOLS VITE CHECK PASS`, or exits 1. No browser, no network
beyond `npm ci` (the registry or a warm npm cache), no model calls.

The install is npm's, not bun's: bun keeps serving a cached copy of a
`file:` tarball after it changes, and npm reinstalls an explicit
tarball spec. `vendor/`, `node_modules/` and `dist/` are git-ignored.
The lockfile is committed and holds vite, jsdom and their tree only:
the tarball stays out of it (installed unsaved), since its integrity
changes with every panel build.

## The live demo

Install the package once (the check above does it), then run these
two commands in two terminals:

```sh
weft dev -- go run ./examples/studio-local        # repository root
cd examples/devtools-vite && npm run dev          # http://localhost:5173/
```

`weft dev` runs Studio on 127.0.0.1:7331 and studio-local beside it
on 127.0.0.1:8080. Vite proxies `/run` and `/studio` to 8080
(`vite.config.ts`; `WEFT_BACKEND` overrides it). The panel's endpoint
is `/studio/`, studio-local's embedded Studio, on the page's own
origin: no token and no CORS. Ask a question in the chat; studio-local's
offline echo model answers, `/run`'s `Weft-Scope` response header
scopes the panel to the turn, and the dock shows it. Set
`VITE_WEFT_STUDIO` to point the panel at another Studio. Without
`weft dev` installed, use
`go run ./cmd/weft dev -- go run ./examples/studio-local`.

`mount()` is the host's own mount: if Studio does not answer, it
shows one "Studio not reachable" line and stays. The other form,
described in `src/main.ts`, is a bare `import "@weftgo/devtools"`
with the endpoint in a `weft:endpoint` meta tag. That dock removes
itself silently when Studio does not answer.
