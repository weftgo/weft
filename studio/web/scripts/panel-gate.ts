// The Dv0/Dv1/Dv2 panel gates (WEFT-DEVTOOLS.md §10): drive the BUILT
// panel bundle — dist/panel/panel.js, exactly what studio.Handler
// serves — inside a plain HTML page (jsdom), over a real HTTP Studio.
// This is the spike's gate harness: "a scripted turn streams into the
// docked panel on a plain HTML page" and the five-line-setup gate
// "a thread's turns live, grouped, with content and timing".
//
// Usage (the Studio must be running; examples/studio-local is the
// setup-A shape, studio/cmd the setup-B one):
//
//   bun run scripts/panel-gate.ts --endpoint http://127.0.0.1:7331/studio \
//       --page http://127.0.0.1:7331/ --trigger http://127.0.0.1:7331/run \
//       [--public-id pub_demo] [--token dev_…] [--turns 2] [--text "…"]
//
// --page is the host page's own origin (any origin works: the script
// tag is cross-origin already in setups B and C). Prints PASS lines
// and exits non-zero on the first failed expectation.

import { readFileSync } from "node:fs"
import { JSDOM } from "jsdom"

interface Args {
  endpoint: string
  page: string
  trigger: string
  publicId: string
  token: string
  turns: number
  text: string
}

function parseArgs(argv: string[]): Args {
  const a: Record<string, string> = {}
  for (let i = 0; i < argv.length; i += 2) a[argv[i]?.replace(/^--/, "")] = argv[i + 1]
  let endpoint = a.endpoint ?? "http://127.0.0.1:7331/studio"
  if (!endpoint.endsWith("/")) endpoint += "/" // a base URL, not a file
  return {
    endpoint,
    page: a.page ?? new URL("/", endpoint).toString(),
    trigger: a.trigger ?? new URL("run", endpoint).toString(),
    publicId: a["public-id"] ?? "pub_demo",
    token: a.token ?? "",
    turns: Number(a.turns ?? 1),
    text: a.text ?? "where is order 42?",
  }
}

/** A fetch-backed EventSource: jsdom has no SSE, so the driver pours
 * the stream through the same parsing the browser would do. */
class FetchEventSource {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSED = 2
  readyState = 0
  onerror: ((e: unknown) => void) | null = null
  onopen: ((e: unknown) => void) | null = null
  onmessage: ((e: unknown) => void) | null = null
  private listeners = new Map<string, Set<(e: unknown) => void>>()
  private ctrl = new AbortController()
  private closed = false

  constructor(readonly url: string) {
    void this.pump()
  }

  addEventListener(type: string, cb: (e: unknown) => void) {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set())
    this.listeners.get(type)!.add(cb)
  }

  close() {
    this.closed = true
    this.readyState = 2
    this.ctrl.abort()
  }

  private dispatch(type: string, ev: { data?: string; lastEventId?: string }) {
    this.listeners.get(type)?.forEach((cb) => cb(ev))
    if (type === "message") this.onmessage?.(ev)
  }

  private async pump() {
    try {
      const res = await fetch(this.url, {
        headers: { Accept: "text/event-stream" },
        signal: this.ctrl.signal,
      })
      if (!res.ok || !res.body) throw new Error(`SSE ${res.status}`)
      this.readyState = 1
      this.onopen?.({})
      const reader = res.body.getReader()
      const dec = new TextDecoder()
      let buf = ""
      let event = "message"
      let data = ""
      let lastId = ""
      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        buf += dec.decode(value, { stream: true })
        let nl: number
        while ((nl = buf.indexOf("\n")) >= 0) {
          const line = buf.slice(0, nl).replace(/\r$/, "")
          buf = buf.slice(nl + 1)
          if (line === "") {
            if (data !== "") this.dispatch(event, { data, lastEventId: lastId })
            event = "message"
            data = ""
          } else if (line.startsWith(":")) {
            // comment / keep-alive
          } else if (line.startsWith("event:")) event = line.slice(6).trim()
          else if (line.startsWith("data:")) data += (data ? "\n" : "") + line.slice(5).trimStart()
          else if (line.startsWith("id:")) lastId = line.slice(3).trim()
        }
      }
      if (!this.closed) this.onerror?.(new Error("stream ended"))
    } catch (e) {
      if (!this.closed) this.onerror?.(e)
    }
  }
}

async function main() {
  const args = parseArgs(process.argv.slice(2))
  // The committed artifact — the exact bytes studio.Handler embeds —
  // so the gate drives what ships, not what lies beside the build.
  const panelJs = readFileSync(
    new URL("../../dist/panel/panel.js", import.meta.url),
    "utf8"
  )

  const scriptAttrs = [`src="${args.endpoint}panel.js"`, `data-public-id="${args.publicId}"`]
  if (args.token) scriptAttrs.push(`data-token="${args.token}"`)
  // data-open so the gate sees the docked panel without a click; the
  // endpoint attribute pins the API origin exactly like setups B/C.
  scriptAttrs.push(`data-endpoint="${args.endpoint}"`, `data-open="true"`)
  const html = `<!doctype html><html><head><title>app</title></head><body>
<h1>the host app's own page</h1>
<script type="module" ${scriptAttrs.join(" ")}></script>
</body></html>`

  const dom = new JSDOM(html, {
    url: args.page,
    runScripts: "outside-only",
    pretendToBeVisual: true,
  })
  const w = dom.window as unknown as {
    fetch: typeof fetch
    EventSource: typeof EventSource
    eval: (code: string) => void
  }
  w.fetch = fetch
  w.EventSource = FetchEventSource as unknown as typeof EventSource
  // The IIFE keeps bun's jsdom eval honest: a bundle-level `var`
  // would otherwise detach from its own scope under bun's eval shim.
  w.eval(`(function(){\n${panelJs}\n})()`)

  const $ = (sel: string): Element | null => {
    const host = dom.window.document.querySelector("weft-devtools")
    const root = host?.shadowRoot
    return root?.querySelector(sel) ?? null
  }
  const text = (sel: string): string => $(sel)?.textContent ?? ""

  const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))
  const waitFor = async (what: () => string | null, label: string, ms = 15000) => {
    const deadline = Date.now() + ms
    for (;;) {
      const got = what()
      if (got) {
        console.log(`PASS ${label}`)
        return got
      }
      if (Date.now() > deadline) throw new Error(`FAIL ${label}`)
      await sleep(100)
    }
  }

  // 1. the panel mounted on the plain page and Studio answered.
  await waitFor(() => ($(".weft-dock") ? "dock" : null), "panel docked on a plain HTML page")
  await waitFor(
    () => (text(".weft-title").includes(args.publicId) ? text(".weft-title") : null),
    `header scoped to ${args.publicId}`
  )

  // 2. a scripted turn streams into the docked panel.
  for (let t = 0; t < args.turns; t++) {
    const res = await fetch(args.trigger, {
      method: "POST",
      headers: { "content-type": "application/x-www-form-urlencoded" },
      body: `text=${encodeURIComponent(`${args.text} (#${t + 1})`)}`,
    })
    if (!res.ok) throw new Error(`trigger failed: ${res.status} ${await res.text()}`)
    await waitFor(
      () => ($(".weft-turn") ? "row" : null),
      `turn ${t + 1}: run row appears in the turn list`
    )
    await waitFor(() => {
      const body = text(".weft-step-b")
      // The second Say chunk streams while the tail subscription is
      // live; the first can beat the subscription's round trip and is
      // transcript-era (Dv1's applyTranscript covers it).
      return body.includes("shipped this morning") ? body : null
    }, `turn ${t + 1}: model text streams into the turn view`)
  }

  // 3. grouping + timing (Dv1's gate): every turn of the conversation
  // is in the list, and the turn view carries content and timing.
  const rows = dom.window.document
    .querySelector("weft-devtools")
    ?.shadowRoot?.querySelectorAll(".weft-turn")
  console.log(`PASS turns listed: ${rows?.length ?? 0}`)
  if ((rows?.length ?? 0) < args.turns) throw new Error("FAIL not all turns listed")

  // Grouped: Studio resolves the public id to one thread whose turn
  // count covers the conversation (S4.3's SessionRow).
  const sess = await fetch(new URL("api/sessions?public_id=" + args.publicId, args.endpoint))
  if (!sess.ok) throw new Error(`FAIL sessions: ${sess.status}`)
  const sessDoc = (await sess.json()) as { total: number; sessions: { turns: number }[] }
  const thread = sessDoc.sessions[0]
  if (!thread || thread.turns < args.turns)
    throw new Error(`FAIL grouping: ${JSON.stringify(thread)}`)
  console.log(`PASS grouped: one session, ${thread.turns} turns`)

  // Content and timing, in the open turn: the tool call with its
  // arguments and result, the streamed text, and a real duration.
  await waitFor(() => {
    const main = text(".weft-main")
    return main.includes("lookup_order") && main.includes("shipped this morning")
      ? main
      : null
  }, "content: the tool call (name + args + result) and the reply render")
  const row2 = Array.from(
    dom.window.document.querySelector("weft-devtools")?.shadowRoot?.querySelectorAll(".weft-row2") ?? []
  ).map((n) => n.textContent)
  if (!row2.some((t) => /(\d+m?s|\d+ms)/.test(t ?? "")))
    throw new Error(`FAIL timing: no duration on the turn rows (${row2.join(" | ")})`)
  console.log("PASS timing: the turn rows carry durations")

  // 4. studio.Handler serves the panel bundle itself (Dv1): the file
  // the script tag names is the committed, embedded artifact.
  const served = await fetch(new URL("panel.js", args.endpoint))
  if (!served.ok) throw new Error(`FAIL /panel.js: ${served.status}`)
  const servedJs = await served.text()
  if (servedJs !== panelJs) throw new Error("FAIL /panel.js is not the built bundle")
  console.log("PASS studio.Handler serves /panel.js (the committed bundle)")

  console.log("PANEL GATE PASS")
  dom.window.close()
  process.exit(0)
}

main().catch((e) => {
  console.error(String(e))
  process.exit(1)
})
