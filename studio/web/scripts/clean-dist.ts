// Copies the static client build into ../dist — the committed, embedded
// tree (ADR 0018 §3). ../dist is rebuilt from scratch on every build,
// so the committed tree is always exactly this script's output and the
// freshness gate (make studio-check) can diff it byte-for-byte.
//
// It also copies the devtools panel's library-mode output (step 7,
// S4.7) from dist/panel-tmp to ../dist/panel/panel.js, beside the
// app: studio.Handler serves that file at /panel.js, and non-Go
// backends ship it as a release asset (V2).
//
// Dropped on the way: the server bundle and prerender scratch (the SPA
// build runs the server once to render the shell), sourcemaps, and any
// Vite internals — nothing the Go handler must not serve.

import { cp, mkdir, readFile, rm, writeFile } from "node:fs/promises"

const src = new URL("../dist/client", import.meta.url).pathname
const dst = new URL("../../dist", import.meta.url).pathname

await rm(dst, { recursive: true, force: true })
await cp(src, dst, {
  recursive: true,
  filter: (path) => {
    if (path.endsWith(".map")) return false
    // The client output is flat: hashed assets under assets/ plus the
    // shell and public files. Anything else is build internals.
    const rel = path.slice(src.length)
    if (rel.startsWith("/server") || rel === "/_server") return false
    return true
  },
})

// The panel (vite.panel.config.ts wrote dist/panel-tmp/panel.js).
const panelSrc = new URL("../dist/panel-tmp/panel.js", import.meta.url).pathname
const panelDst = new URL("../../dist/panel", import.meta.url).pathname
await mkdir(panelDst, { recursive: true })
await cp(panelSrc, `${panelDst}/panel.js`)

// The router renders preload URLs as "/./assets/…" — root-absolute,
// which would escape the mount (ADR 0018 §6). Under the runtime
// <base href> a plain "./assets/…" resolves correctly anywhere.
//
// The head manager hoists its tags above the <base> rendered by the
// root document, but a base only affects URLs after it in the
// document — so it is moved to the very top of <head>, where every
// relative URL in the shell resolves under the mount.
const shellPath = new URL("../../dist/index.html", import.meta.url).pathname
const shell = await readFile(shellPath, "utf8")
let fixed = shell.replaceAll('"/./', '"./')
fixed = fixed.replace(/<base href="[^"]*"\s*\/?>/, "")
fixed = fixed.replace("<head>", '<head><base href="/">')
// The router stamps its prerendered match with Date.now() — the one
// nondeterministic byte in the output. Pinned to 0 (permanently stale
// preloads, which a static shell re-resolves anyway) so two builds
// from the same tree are byte-identical (ADR 0018 §4).
fixed = fixed.replace(/([,{[]u:)\d{12,}(,s:)/g, "$10$2")
if (fixed !== shell) {
  await writeFile(shellPath, fixed)
}
console.log(`studio: static client copied to ${dst}`)
