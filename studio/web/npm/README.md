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
`<meta name="weft:endpoint|public-id|token|position|open|auto">`. With
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

| Export | What it does | Status |
|---|---|---|
| `mount(opts?)` | Appends a `<weft-devtools>` configured by `opts` (`endpoint`, `publicId`, `token`, `position`, `open`, `auto`, `target`), which is configuration rung 1, above attributes and meta tags. This is the host's own mount: if Studio does not answer, it shows `Studio not reachable at … · retry` and stays. It replaces the dock the bundle mounted by itself and leaves the page's own markup alone. Returns the element. | complete in C1 |
| `scope(s)` | Points every panel on the page at `s` (a `Scope`, or a string public id). The new scope overrides `data-public-id` and `window.__WEFT__`. It also writes `s` into each element's `data-weft-scope` attribute. A later `mount` starts in this scope. | C1 follows `publicId` only. `session`, `flow` and `run` are carried in the marker, and C3.1 makes the panel follow them. |
| `open()`, `close()`, `toggle()` | Expand, collapse or flip every panel on the page. A panel mounted after `open()` or `close()` starts in that state. | complete in C1 |
| `on(event, cb)` | Calls `cb(detail)` for each `weft:<event>` CustomEvent a panel dispatches (`"run"`, `"parked"`, `"error"`; details typed by `DevtoolsEvents`). Returns the unsubscribe function. | Registration works in C1, but the panel does not dispatch any of these events yet, so `cb` is never called. C4 adds the dispatches. |
| `serializeScope(s)`, `parseScope(str)` | The marker's one string form: `pub_…;session=s_…;flow=f_…;run=r_…`. The public id comes first. The other fields are optional and appear in that order, each value percent-encoded. | complete in C1 |
| types `Scope`, `MountOptions`, `Position`, `WeftDevtoolsElement`, `DevtoolsEvents` | | complete in C1 |

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
