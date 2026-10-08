# @weftgo/devtools

The weft devtools panel for apps that bundle everything and ship no
`<script>` tags (Vite, Next, SvelteKit, Nuxt). It is a second way to
get the same panel, not the main one. The main install is the Go
handler: `studio.Handler` serves `/studio/panel.js`, and one script tag
loads it (see `studio/README.md` in the weft repository). This package
ships that same file byte for byte, so `sha256sum` of `panel.js` here
equals the served asset's and the `panel-<version>.js.sha256` release
asset's. Beside it you get a small typed ES module and three helpers.

- Zero runtime dependencies. React, Vue and Svelte are optional peer
  dependencies, and the helpers do not import them.
- Versioned with your backend. The package version is the weft version
  (`0.11.0` is weft `v0.11.0`). Pin it in your lockfile next to your
  Go module. At connect, the panel compares its stamp with
  `/api/meta`'s `studio_version`. If Studio is newer, the panel shows
  "Studio is newer than this panel; update panel.js" instead of
  guessing.
- One UI. The `<weft-devtools>` custom element in its shadow DOM is the
  whole panel. The helpers only set a DOM marker. They are not
  components.

## Install

```sh
npm install --save-dev @weftgo/devtools
```

```ts
// Client-only code (the panel defines a custom element at import time):
import "@weftgo/devtools"
```

That one import does what the script tag does. It defines
`<weft-devtools>` and mounts the dock. The panel configures itself
from the same ladder as the script tag, minus the tag itself:
`mount(opts)`, the element's `data-*` attributes, then
`<meta name="weft:endpoint|scope|public-id|token|detect|position|open|auto">`. With
none of these set, the endpoint is the page's own directory. If Studio
does not answer, the dock removes itself silently.

Server-rendered frameworks: import the root entry from client code
only, for example `useEffect(() => { import("@weftgo/devtools") }, [])`,
`onMount`, or a `"use client"` module loaded with `ssr: false`. The
framework helpers below are safe to import anywhere. They load the
panel only when an element is bound, which never happens on the server.

Tokens: in pages you ship, pass a per-page panel token that your
backend mints (`POST /api/panel-tokens`). Never pass a dev token or a
server token.

## API

`import { … } from "@weftgo/devtools"`

| Export | What it does |
|---|---|
| `mount(opts?)` | Appends a `<weft-devtools>` configured by `opts` (`endpoint`, `scope`, `publicId`, `token`, `detect`, `position`, `open`, `auto`, `target`), which is configuration rung 1, above attributes and meta tags. This is the host's own mount: if Studio does not answer, it shows `Studio not reachable at … · retry` and stays. It replaces the dock the bundle mounted by itself and leaves the page's own markup alone. Returns the element. |
| `scope(s)` | Points every panel on the page at `s`: a `Scope`, or its string form (`"pub_…"`, `"pub_…;session=…;run=…"`, read by `parseScope`). The new scope overrides `data-scope`, `data-public-id`, `window.__WEFT__` and any detected scope. The panel follows all of it: `publicId` selects the conversation, `session` narrows the turn list, `run` pins the selected turn, and `flow` shows as a chip without filtering anything yet. It also writes `s` into each element's `data-weft-scope` attribute. A later `mount` starts in this scope. `scope(null)` clears it: the ladder (the URL, markers, headers, the fallback) decides again. A scope with a `session` and no `publicId` is resolved through Studio's `GET /api/sessions/{id}/public_id`, which only setup A and the dev token may ask; under a panel token, or when the session has no public id, the panel says so in one line and keeps its scope. |
| `select(runId, step?)` | Selects that turn in every panel, and the step by its ordinal (the `n` of `runs/{id}/steps/{n}`), once a `scope()` called just before it has settled. The panel's ⤢ link then carries the step. A run the list does not show is read by id and joins the list when it belongs to the conversation followed; otherwise the panel shows `run r_… not in this conversation`. |
| `open()`, `close()`, `toggle()`, `isOpen()` | Expand, collapse or flip every panel on the page; `isOpen()` reports whether one is expanded. A panel mounted after `open()` or `close()` starts in that state. |
| `on(event, cb)` | Calls `cb(detail)` for each `weft:<event>` CustomEvent a panel dispatches, and returns the unsubscribe function. A `cb` that throws is swallowed. The events, typed by `DevtoolsEvents`: `run` `{runId, status, publicId?, sessionId?, step?}` (a run starts, and each status change: `running`, `succeeded`, `failed`, `parked`, `interrupted`), `parked` `{runId, callId, ackId, name}` (once per call a run parked on; `ackId` is the id its approval names, the call id, so it equals `callId`; your app approves its own turns with `s.Decide(ctx, thread.Approve(ackId))`, Studio's approvals route is for playground runs only), `error` `{message, runId}` (a failed run of the conversation, once; run errors only). Only runs of the conversation the panel follows, and only transitions it sees: the list it first reads is history, except a running run and the run the scope pins. |
| `studioLink(runId, step?)` | The Studio page of that run (at that step) as the panel builds it, the link its ⤢ carries. It never carries a token. Empty before a panel knows its endpoint. |
| `serializeScope(s)`, `parseScope(str)` | The marker's one string form: `pub_…;session=s_…;flow=f_…;run=r_…`. The public id comes first. The other fields are optional and appear in that order, each value percent-encoded. |
| types `Scope`, `MountOptions`, `Position`, `WeftDevtoolsElement`, `DevtoolsEvents` | |

The package adds no global: its exports are the API. (The script-tag
install publishes the same methods as `window.weft.devtools`.)

`import { serializeScope, parseScope, type Scope } from "@weftgo/devtools/scope"`
loads only the marker's serialiser. It touches no DOM, so it is safe in
server code: in a server-rendered component, use it to write
`data-weft-scope` into the markup yourself.

Calls made while the page is still parsing take effect once the dock is
mounted.

## Framework helpers

Each helper sets `data-weft-scope` on the element that shows the
conversation and calls `scope()`. If you pass `endpoint` or `token` and
the page has no panel of its own, the helper first mounts one with
`mount({ endpoint, token })`, replacing the bundle's self-mounted dock.
With neither option it mounts nothing: the page's own configuration
(meta tags, markup) decides. On detach it removes the marker.
Re-rendering with the same options does nothing. Each helper is about
20 lines and contains no component.

```tsx
// React 18/19: a callback ref.
import { useWeftDevtools } from "@weftgo/devtools/react"
<div ref={useWeftDevtools({ scope: { publicId }, endpoint: "/studio/" })}>…</div>
```

```vue
<!-- Vue 3: a function ref; pass a getter to follow reactive state -->
<script setup>
import { useWeftDevtools } from "@weftgo/devtools/vue"
const weft = useWeftDevtools(() => ({ scope: { publicId: id.value } }))
</script>
<template><div :ref="weft">…</div></template>
```

```svelte
<!-- Svelte 3/4/5: an action -->
<script>import { weftDevtools } from "@weftgo/devtools/svelte"</script>
<div use:weftDevtools={{ scope: { publicId } }}>…</div>
```

## The file itself

`@weftgo/devtools/panel.js` is the side-effect bundle on its own. It is
the same file as `/studio/panel.js`, with `panel.js.sha256` beside it.
It is built in the weft repository by `make devtools-npm`, which runs
the build, checks the hash and lists the tarball. Publishing is done by
hand at release time.
