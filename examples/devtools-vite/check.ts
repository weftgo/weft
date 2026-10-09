// The smoke test (plan §12 phase 3, the gate's first clause): build the
// app with `vite build`, load dist/index.html and its built JS into
// jsdom with a fake Studio answering /api/meta, and prove the npm
// package — not a <script> tag — put the panel on the page. Runs with
// `bun run check.ts`; no browser, no network, no Go.
import { spawnSync } from "node:child_process"
import { createHash } from "node:crypto"
import { existsSync, readFileSync } from "node:fs"
import path from "node:path"
import { fileURLToPath } from "node:url"
import { JSDOM, VirtualConsole } from "jsdom"

const here = path.dirname(fileURLToPath(import.meta.url))
const fail = (msg: string): never => {
  console.error(`DEVTOOLS VITE CHECK FAIL: ${msg}`)
  process.exit(1)
}

// 1. Build.
const vite = path.join(here, "node_modules", ".bin", "vite")
const build = spawnSync(vite, ["build"], { cwd: here, stdio: "inherit" })
if (build.status !== 0) fail(`vite build exited ${build.status}`)

// 2. The built page: no script tag for the panel, one module entry.
const dist = path.join(here, "dist")
const html = readFileSync(path.join(dist, "index.html"), "utf8")
if (/<script[^>]*src=["'][^"']*panel\.js/i.test(html)) fail("dist/index.html has a <script src=…panel.js> tag")
const srcs = [...html.matchAll(/<script[^>]*src="([^"]+)"[^>]*><\/script>/g)].map((m) => m[1])
if (srcs.length !== 1) fail(`want one built script in dist/index.html, got ${JSON.stringify(srcs)}`)
const js = readFileSync(path.join(dist, srcs[0].replace(/^\//, "")), "utf8")

// 3. A fake Studio: /api/meta in the shape the panel's own suite uses
//    (studio/web/src/panel/testkit.ts META, copied — the example stands
//    alone), every other path a JSON 404.
const META = {
  weft_version: "v0.0.0",
  studio_version: "",
  db: { kind: "sqlite" },
  title: "weft studio",
  has_manifest: false,
  ingest_open: true,
  interrupted_after_ms: 30000,
  capabilities: ["live", "ingest"],
}
const metaCalls: string[] = []
// Page errors (uncaught exceptions in the bundle) fail the check; the
// panel's own console is forwarded so any noise shows.
const errors: unknown[] = []
const virtualConsole = new VirtualConsole()
virtualConsole.on("jsdomError", (e) => errors.push(e))
if (process.env.DEBUG) virtualConsole.on("jsdomError", (e) => console.error((e as { detail?: { stack?: string } }).detail?.stack ?? e.stack))
for (const level of ["error", "warn"] as const) virtualConsole.on(level, (...a: unknown[]) => errors.push(`console.${level}: ${a.join(" ")}`))
const page = new JSDOM(html.replace(/<script[^>]*src="[^"]+"[^>]*><\/script>/g, ""), {
  url: "http://127.0.0.1:5173/",
  runScripts: "outside-only",
  pretendToBeVisual: true,
  virtualConsole,
})
type Win = Window & typeof globalThis & { eval: (s: string) => unknown }
// The context's own global (under bun's vm it is not page.window's
// wrapper object): what the bundle's bare `fetch` and `EventSource` read.
const win = (page.window as unknown as Win).eval("globalThis") as Win
// jsdom has no fetch, Request or Response: the runtime's own stand in.
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } })
win.fetch = (async (input: RequestInfo | URL) => {
  const url = new URL(input instanceof Request ? input.url : String(input), win.location.href)
  if (url.pathname.endsWith("/api/meta")) {
    metaCalls.push(url.href)
    return json(META)
  }
  return json({ error: { code: "not_found", message: url.pathname } }, 404)
}) as typeof fetch
// The live stream: an EventSource that never connects (jsdom has none).
class QuietEventSource {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSED = 2
  readyState = 0
  onmessage = null
  onerror = null
  onopen = null
  url: string
  constructor(url: string) {
    this.url = url
  }
  addEventListener() {}
  removeEventListener() {}
  close() {
    this.readyState = 2
  }
}
Object.assign(win, { EventSource: QuietEventSource })

// 4. Run the bundle as the browser would once the page has parsed.
try {
  // A module's semantics for a classic eval: strict, its own scope (the
  // bundle has no import or export left; jsdom runs no module scripts).
  // window and self are rebound to the context's globalThis: under
  // bun's vm, the bare `window` in a jsdom context is not the wrapper
  // jsdom's EventTarget methods accept (globalThis is). import.meta,
  // a module-only form, becomes a parameter carrying what a browser
  // would give the built module: its own URL.
  const metaURL = new URL(srcs[0], win.location.href).href
  const body = js.replaceAll("import.meta", "__weftImportMeta")
  win.eval(
    `"use strict";((window, self, __weftImportMeta) => {\n${body}\n})(globalThis, globalThis, ${JSON.stringify({ url: metaURL })})`,
  )
} catch (err) {
  fail(`the built JS threw: ${err instanceof Error ? (err.stack ?? err.message) : String(err)}`)
}
// Wait for the panel's first /api/meta request (the mount is async),
// polling with a deadline rather than sleeping a fixed time.
for (const deadline = Date.now() + 5000; metaCalls.length === 0 && Date.now() < deadline; ) {
  await new Promise((r) => setTimeout(r, 10))
}
// One more turn for the response to land and any error it raises.
await new Promise((r) => setTimeout(r, 0))
if (errors.length) fail(`uncaught page errors: ${errors.map(String).join("; ")}`)

// 5. The assertions.
if (!win.customElements.get("weft-devtools")) fail('customElements.get("weft-devtools") is undefined')
const docks = win.document.querySelectorAll("weft-devtools")
if (docks.length !== 1) fail(`want one <weft-devtools> on the page, got ${docks.length}`)
if (!metaCalls.some((u) => u === "http://127.0.0.1:5173/studio/api/meta"))
  fail(`the panel never asked /studio/api/meta (asked: ${JSON.stringify(metaCalls)})`)
console.log(`ok: <weft-devtools> defined, one on the page, /api/meta asked (${metaCalls.length}x)`)

// 6. Same bytes: the installed package's panel.js is the served one.
const sha = (p: string) => createHash("sha256").update(readFileSync(p)).digest("hex")
const installed = path.join(here, "node_modules", "@weftgo", "devtools", "panel.js")
const served = path.resolve(here, "../../studio/dist/panel/panel.js")
if (!existsSync(installed)) fail(`${installed} is missing: install the package first`)
if (existsSync(served)) {
  const [a, b] = [sha(installed), sha(served)]
  if (a !== b) fail(`installed panel.js sha256 ${a} != studio/dist/panel/panel.js ${b}`)
  console.log(`ok: panel.js sha256 ${a} = studio/dist/panel/panel.js`)
} else {
  console.log("note: studio/dist/panel/panel.js not found (outside the weft repo): sha256 comparison skipped")
}

page.window.close()
console.log("DEVTOOLS VITE CHECK PASS")
