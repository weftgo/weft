// The Dv0–Dv2 panel gates (WEFT-DEVTOOLS.md §10): drive the BUILT
// panel bundle — dist/panel/panel.js, exactly what studio.Handler
// serves — inside a plain HTML page (jsdom), over a real HTTP Studio.
// This is the spike's gate harness: "a scripted turn streams into the
// docked panel on a plain HTML page", the five-line-setup gate
// "a thread's turns live, grouped, with content and timing", and the
// setups-B/C gate "a non-Go app's OTLP export appears in the panel,
// cross-origin, behind a token".
//
// Setup A (examples/studio-local is the shape):
//
//   bun run scripts/panel-gate.ts --endpoint http://127.0.0.1:7331/studio \
//       --page http://127.0.0.1:7331/ --trigger http://127.0.0.1:7331/run \
//       [--public-id pub_demo] [--turns 2]
//
// Setup B/C (a token-walled Studio, the page on another origin, no
// app to trigger — an OTLP fixture is posted to /v1/traces first,
// standing in for the Python app; panel-otlp-fixture.json beside this
// script is the recorded export, replayed byte-for-byte):
//
//   bun run scripts/panel-gate.ts --endpoint http://127.0.0.1:7331/studio \
//       --page http://127.0.0.1:8000/ --token dev_… --otlp scripts/panel-otlp-fixture.json
//
// --bundle <path> drives another build of the panel (a scratch build
// of the sources under review) instead of the committed artifact; the
// /panel.js check still compares what Studio serves with the committed
// artifact it embeds.
//
// Setup A also drives the app's own served page (--page-script, on by
// default; "off" skips it): its inline script, window.weft.devtools,
// "debug this", the report link and data-parked-count (plan C4).
//
// Prints PASS lines and exits non-zero on the first failed
// expectation.

import { readFileSync, readdirSync, statSync } from "node:fs"
import { JSDOM } from "jsdom"

interface Args {
  endpoint: string
  page: string
  trigger: string
  publicId: string
  token: string
  turns: number
  text: string
  otlp: string
  /** The app's thread store directory (jsonl): the P1 gate
   * byte-compares it across a playground re-run — the original
   * session's files must not change. Empty skips that half. */
  threads: string
  /** The bundle to drive; "" is the committed dist/panel/panel.js. */
  bundle: string
  /** Setup A only (default on; --page-script off skips it): drive the
   * served page itself — its own inline script, the host API through
   * window.weft.devtools (plan C4) — after the synthesised page's run. */
  pageScript: boolean
}

function parseArgs(argv: string[]): Args {
  const a: Record<string, string | undefined> = {}
  for (let i = 0; i < argv.length; i += 2) a[argv[i].replace(/^--/, "")] = argv.at(i + 1)
  let endpoint = a.endpoint ?? "http://127.0.0.1:7331/studio"
  if (!endpoint.endsWith("/")) endpoint += "/" // a base URL, not a file
  return {
    endpoint,
    page: a.page ?? new URL("/", endpoint).toString(),
    trigger: a.trigger ?? new URL("run", endpoint).toString(),
    publicId: a["public-id"] ?? (a.otlp ? "pub_pyapp" : "pub_demo"),
    token: a.token ?? "",
    turns: Number(a.turns ?? 1),
    text: a.text ?? "where is order 42?",
    /** The setups-B/C mode: post this recorded OTLP JSON export to
     * /v1/traces (the Python app's stand-in) instead of triggering. */
    otlp: a.otlp ?? "",
    threads: a.threads ?? "",
    bundle: a.bundle ?? "",
    pageScript: a["page-script"] !== "off",
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
  const shipped = readFileSync(new URL("../../dist/panel/panel.js", import.meta.url), "utf8")
  const panelJs = args.bundle ? readFileSync(args.bundle, "utf8") : shipped

  // Setup A's page names no scope, like examples/studio-local's: the
  // panel scopes itself from the trigger's Weft-Scope response header
  // (detection rung 2), the trigger going through the page's own
  // window.fetch. Setups B/C (--otlp) have no app to trigger: the tag
  // names the scope (data-scope).
  const scriptAttrs = [`src="${args.endpoint}panel.js"`]
  if (args.otlp) scriptAttrs.push(`data-scope="${args.publicId}"`)
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
  // jsdom's window.addEventListener wrapper refuses calls arriving
  // from bun-evaluated code (its `this` check fails across the shim),
  // so the driver provides the registrar itself: the panel's keydown
  // listeners land here and the rest of jsdom is untouched.
  const keyListeners = new Set<(e: unknown) => void>()
  Object.defineProperty(w, "addEventListener", {
    configurable: true,
    value: (type: string, cb: (e: unknown) => void) => {
      if (type === "keydown") keyListeners.add(cb)
    },
  })
  Object.defineProperty(w, "removeEventListener", {
    configurable: true,
    value: (_type: string, cb: (e: unknown) => void) => {
      keyListeners.delete(cb)
    },
  })
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
  const scoped = () =>
    waitFor(
      () => (text(".weft-title").includes(args.publicId) ? text(".weft-title") : null),
      `header scoped to ${args.publicId}${args.otlp ? "" : " (from the trigger's Weft-Scope header)"}`
    )
  if (args.otlp) await scoped()

  // 2a. setups B/C (--otlp): replay the recorded export into
  // /v1/traces — the Python app's stand-in — and let the panel read
  // the run it lands as, cross-origin, behind the token.
  if (args.otlp) {
    const body = readFileSync(new URL(args.otlp, "file://" + process.cwd() + "/"), "utf8")
    const headers: Record<string, string> = { "content-type": "application/json" }
    if (args.token) headers.Authorization = `Bearer ${args.token}`
    const ingest = await fetch(new URL("v1/traces", args.endpoint), {
      method: "POST",
      headers,
      body,
    })
    if (ingest.status !== 200)
      throw new Error(`FAIL OTLP ingest: ${ingest.status} ${await ingest.text()}`)
    console.log("PASS OTLP JSON export accepted by /v1/traces")
    await waitFor(
      () => ($(".weft-turn")?.textContent.includes(args.publicId === "pub_pyapp" ? "py_run_1" : args.publicId) ? "row" : null),
      "the exported run appears in the panel's turn list"
    )
    const rowText = $(".weft-turn")?.textContent ?? ""
    if (!rowText.includes("gpt-4o") || !rowText.includes("410"))
      throw new Error(`FAIL the GenAI run row lacks model/usage: ${rowText}`)
    console.log("PASS the GenAI run row carries model and token usage")
  }

  // 2b. setup A: a scripted turn streams into the docked panel.
  if (!args.otlp) {
    const rowIds = (): string[] =>
      Array.from(
        dom.window.document.querySelector("weft-devtools")?.shadowRoot?.querySelectorAll(".weft-turn") ?? []
      ).map((n) => n.querySelector(".weft-id")?.textContent ?? "")
    for (let t = 0; t < args.turns; t++) {
      // The rows before this turn (the database may hold earlier
      // conversations' turns too): the turn's own row is one that was
      // not there — "some row exists" would pass on turn 1's.
      const before = new Set(rowIds())
      // The page's own fetch — the panel's header rung wraps it.
      const res = await w.fetch(args.trigger, {
        method: "POST",
        headers: { "content-type": "application/x-www-form-urlencoded" },
        body: `text=${encodeURIComponent(`${args.text} (#${t + 1})`)}`,
      })
      if (!res.ok) throw new Error(`trigger failed: ${res.status} ${await res.text()}`)
      if (t === 0) await scoped()
      const fresh = await waitFor(
        () => rowIds().find((id) => !before.has(id)) ?? null,
        `turn ${t + 1}: its run row appears in the turn list`
      )
      // The panel opens the first turn by itself and then stays on what
      // the user is reading: a later turn is opened the way a user
      // opens it.
      const row = Array.from(
        dom.window.document.querySelector("weft-devtools")?.shadowRoot?.querySelectorAll(".weft-turn") ?? []
      ).find((n) => n.querySelector(".weft-id")?.textContent === fresh)
      if (!row?.classList.contains("weft-sel"))
        row?.dispatchEvent(
          new (dom.window as unknown as { Event: typeof Event }).Event("click", { bubbles: true })
        )
      await waitFor(() => {
        const sel = text(".weft-sel .weft-id")
        const body = text(".weft-step-b")
        // The second Say chunk streams while the tail subscription is
        // live; the first can beat the subscription's round trip and is
        // transcript-era (Dv1's applyTranscript covers it).
        return sel === fresh && body.includes("shipped this morning") ? body : null
      }, `turn ${t + 1}: model text streams into its own turn view`)
    }
  }

  // 3. grouping + timing (Dv1's gate): every turn of the conversation
  // is in the list exactly once, and the turn view carries content and
  // timing. The live lane forwards every change of a run and never
  // dedupes (studio/live.go's liveDedupKey), so the row count must be
  // exact: one row per distinct run id, and precisely the id set the
  // API reports for the public id.
  const rows = Array.from(
    dom.window.document
      .querySelector("weft-devtools")
      ?.shadowRoot?.querySelectorAll(".weft-turn") ?? []
  )
  const ids = rows.map((n) => n.querySelector(".weft-id")?.textContent ?? "")
  const panelIds = new Set(ids)
  if (rows.length < (args.otlp ? 1 : args.turns))
    throw new Error(`FAIL not all turns listed: ${rows.length} rows`)
  console.log(`PASS turns listed: ${rows.length} (${panelIds.size} distinct)`)
  if (rows.length !== panelIds.size)
    throw new Error(
      `FAIL duplicate turn rows: ${rows.length} rows for ${panelIds.size} run ids (${ids.join(", ")})`
    )
  const apiRuns = await fetch(new URL(`api/runs?public_id=${args.publicId}&limit=50`, args.endpoint), {
    headers: args.token ? { Authorization: `Bearer ${args.token}` } : {},
  })
  if (!apiRuns.ok) throw new Error(`FAIL the runs page: ${apiRuns.status}`)
  const apiIds = new Set(
    ((await apiRuns.json()) as { runs: { id: string }[] }).runs.map((r) => r.id)
  )
  if (panelIds.size !== apiIds.size || [...apiIds].some((id) => !panelIds.has(id)))
    throw new Error(
      `FAIL the turn list is not exactly the API's runs: ${panelIds.size} panel rows vs ${apiIds.size} api runs`
    )
  console.log(`PASS one row per run: the list is exactly the API's ${apiIds.size} runs`)

  // Timing on the rows either way; grouping and content are the
  // setup-A shapes (a spans-only export has no events to fold).
  // The finished-run frame may land after the turn view's text: the
  // rows' durations are waited for, not read once.
  const row2 = () =>
    Array.from(
      dom.window.document.querySelector("weft-devtools")?.shadowRoot?.querySelectorAll(".weft-row2") ?? []
    ).map((n) => n.textContent)
  await waitFor(
    () => (row2().some((t) => /(\d+m?s|\d+ms)/.test(t)) ? "timed" : null),
    "timing: a duration on the turn rows"
  )

  if (!args.otlp) {
    // Grouped: Studio resolves the public id to one thread whose turn
    // count covers the conversation (S4.3's SessionRow).
    const sess = await fetch(new URL("api/sessions?public_id=" + args.publicId, args.endpoint))
    if (!sess.ok) throw new Error(`FAIL sessions: ${sess.status}`)
    const sessDoc = (await sess.json()) as { total: number; sessions: { turns: number }[] }
    const thread = sessDoc.sessions.at(0)
    if (!thread || thread.turns < args.turns)
      throw new Error(`FAIL grouping: ${JSON.stringify(thread)}`)
    console.log(`PASS grouped: one session, ${thread.turns} turns`)

    // Content and timing, in the open turn: the tool call with its
    // arguments and result, the streamed text.
    await waitFor(() => {
      const story = text(".weft-main")
      return story.includes("lookup_order") && story.includes("shipped this morning")
        ? story
        : null
    }, "content: the tool call (name + args + result) and the reply render")
  }

  // 4. studio.Handler serves the panel bundle itself (Dv1): the file
  // the script tag names is the committed, embedded artifact.
  const served = await fetch(new URL("panel.js", args.endpoint))
  if (!served.ok) throw new Error(`FAIL /panel.js: ${served.status}`)
  const servedJs = await served.text()
  if (servedJs !== shipped) throw new Error("FAIL /panel.js is not the committed bundle")
  console.log("PASS studio.Handler serves /panel.js (the committed bundle)")

  // 5. the ⤢ deep link carries run and step (Dv3): clicking a step
  // marks it, the link names it, and Studio answers the URL.
  if (!args.otlp) {
    const stepCard = dom.window.document
      .querySelector("weft-devtools")
      ?.shadowRoot?.querySelector("[data-weft-step]")
    if (!stepCard) throw new Error("FAIL no step card to select")
    ;(stepCard as HTMLElement).dispatchEvent(
      new (dom.window as unknown as { Event: typeof Event }).Event("click", { bubbles: true })
    )
    await sleep(200)
    const href = dom.window.document
      .querySelector("weft-devtools")
      ?.shadowRoot?.querySelector(".weft-head a")?.getAttribute("href")
    if (!href || !/[?&]step=\d+&view=story$/.test(href))
      throw new Error(`FAIL the ⤢ link does not carry the step: ${href}`)
    console.log(`PASS the ⤢ deep link carries run and step: ${href}`)
    const deep = await fetch(href)
    const body = await deep.text()
    if (deep.status !== 200 || !body.includes("<!DOCTYPE html>"))
      throw new Error(`FAIL the deep link does not resolve: ${deep.status}`)
    console.log("PASS the deep link resolves in Studio (the SPA shell serves the route)")
  }

  // 5b. the Request tab (plan E1.2): the example's PrepareStep
  // (trimGuidance) drops the guidance paragraph from step 1 on — the
  // tab shows step 1 with the "changed by PrepareStep" chip and the
  // paragraph as a deletion in its diff vs step 0. Back to Story after
  // (the experiment action below lives there).
  if (!args.otlp) {
    const Ev = (dom.window as unknown as { Event: typeof Event }).Event
    const press = (sel: string) => $(sel)?.dispatchEvent(new Ev("click", { bubbles: true }))
    press("#weft-tab-request")
    await waitFor(() => ($('#weft-tp-request [data-weft-rq-step="1"]') ? "step 1" : null), "the Request tab lists the trimmed turn's steps")
    press('#weft-tp-request [data-weft-rq-step="1"]')
    await waitFor(() => {
      const chips = Array.from($("#weft-tp-request")?.querySelectorAll("[data-weft-mark]") ?? []).map((m) => m.textContent)
      return $('#weft-tp-request [data-weft-rq-pane="1"]') && chips.includes("changed by PrepareStep") ? chips.join(", ") : null
    }, 'the Request tab: step 1 carries the "changed by PrepareStep" chip')
    const del = await waitFor(() => {
      const dels = Array.from($("#weft-tp-request")?.querySelectorAll('[data-weft-diff="del"]') ?? []).map((r) => r.textContent)
      const line = dels.find((r) => r.includes("First-step guidance"))
      return line && !$('#weft-tp-request [data-weft-diff="add"]') ? line : null
    }, "the Request tab: step 1's diff vs step 0 deletes the guidance paragraph (no additions)")
    console.log(`     the deleted line: ${del}`)
    press("#weft-tab-story")
    await waitFor(() => ($("#weft-tab-story")?.getAttribute("aria-selected") === "true" ? "story" : null), "back on the Story tab")
  }

  // 6. rung 2's P1 gate (WEFT-PLAYGROUND §10.6, setup A's shape): edit
  // the prompt in the panel, re-run through the runtime link, see the
  // result stream in place with the inline diff — and the original
  // session's thread files are byte-identical afterwards.
  if (!args.otlp) {
    const snapDir = (dir: string): Map<string, string> => {
      const out = new Map<string, string>()
      for (const f of readdirSync(dir, { recursive: true }) as string[]) {
        const p = dir + "/" + f
        if (statSync(p).isFile()) out.set(p, readFileSync(p, "utf8"))
      }
      return out
    }
    const before = args.threads ? snapDir(args.threads) : null
    if (before) console.log(`PASS thread store snapshot: ${before.size} files`)
    const experiment = Array.from(
      dom.window.document
        .querySelector("weft-devtools")
        ?.shadowRoot?.querySelectorAll(".weft-actions .weft-btn") ?? []
    ).find((b) => b.textContent === "✎ Experiment")
    if (!experiment) throw new Error("FAIL no ✎ Experiment action (is the playground capability on?)")
    ;(experiment as HTMLElement).dispatchEvent(
      new (dom.window as unknown as { Event: typeof Event }).Event("click", { bubbles: true })
    )
    await waitFor(() => ($(".weft-drawer") ? "drawer" : null), "the experiment drawer opens")
    const drawerText = text(".weft-drawer")
    if (!drawerText.includes("studio-local")) throw new Error("FAIL the drawer names no agent")
    if (!drawerText.includes("lookup_order"))
      throw new Error("FAIL the drawer lists no registered tools")
    // Edit the input (the drawer's second textarea is the input), then
    // re-run: the demo model quotes the question, so the answer — and
    // the diff — carry the edit.
    const textareas = Array.from(
      dom.window.document
        .querySelector("weft-devtools")
        ?.shadowRoot?.querySelectorAll<HTMLTextAreaElement>(".weft-drawer textarea") ?? []
    )
    const input = textareas.at(-1)
    if (!input) throw new Error("FAIL the drawer has no input field")
    const edited = "where is order 4242?"
    input.value = edited
    input.dispatchEvent(
      new (dom.window as unknown as { Event: typeof Event }).Event("input", { bubbles: true })
    )
    await sleep(100)
    const runBtn = Array.from(
      dom.window.document
        .querySelector("weft-devtools")
        ?.shadowRoot?.querySelectorAll(".weft-drawer button") ?? []
    ).find((b) => b.textContent.includes("Run experiment"))
    if (!runBtn) throw new Error("FAIL no Run experiment button")
    ;(runBtn as HTMLElement).dispatchEvent(
      new (dom.window as unknown as { Event: typeof Event }).Event("click", { bubbles: true })
    )
    await waitFor(() => {
      const res = text(".weft-xres")
      return res.includes("·x1") && res.includes(edited) ? res : null
    }, "the experiment's result streams in place, labelled t·x1, answering the edited input")
    console.log("PASS the experiment result streams in place (labelled ·x1)")
    await waitFor(() => {
      const diff = text(".weft-diff-h")
      // The header reads "diff vs t1:  +1 −1"; an edited input that
      // changed nothing would read "… identical" — not a pass.
      return diff.includes("diff vs") && !diff.includes("identical") ? diff : null
    }, "the inline diff against the source turn renders")
    console.log(`PASS inline diff against the source turn: ${text(".weft-diff-h")}`)
    // The Studio hand-off: every parameter rides the fragment (never a
    // server's request line or logs), and the page it names is served.
    const compare = Array.from(
      dom.window.document.querySelector("weft-devtools")?.shadowRoot?.querySelectorAll(".weft-xres a") ?? []
    ).find((a) => a.textContent === "compare in Studio")
    const handoff = new URL(compare?.getAttribute("href") ?? "about:blank")
    const carried = new URLSearchParams(handoff.hash.slice(1))
    if (handoff.search !== "" || !carried.get("run") || carried.get("input") !== edited)
      throw new Error(`FAIL the hand-off does not ride the fragment: ${handoff.toString()}`)
    const shell = await fetch(handoff.origin + handoff.pathname)
    if (shell.status !== 200) throw new Error(`FAIL the hand-off page does not resolve: ${shell.status}`)
    console.log(`PASS the Studio hand-off rides the fragment: ${handoff.pathname}#${handoff.hash.slice(1, 40)}…`)
    // The original session's thread files are byte-identical.
    if (before !== null) {
      await sleep(500) // any writer that was going to touch them has
      const after = snapDir(args.threads)
      if (after.size !== before.size)
        throw new Error(`FAIL thread store changed: ${before.size} files before, ${after.size} after`)
      for (const [p, body] of before)
        if (after.get(p) !== body) throw new Error(`FAIL thread file changed: ${p}`)
      console.log(`PASS the original session's thread files are byte-identical (${after.size} files)`)
    }

    // 7. fork mode (§5.4): the same drawer, thread=fork — the command's
    // accepted row names no run, the finished one names the new
    // session's turn, and the pane moves to it. After the byte check:
    // a fork writes a session of its own.
    const shadow = () => dom.window.document.querySelector("weft-devtools")?.shadowRoot
    const threadSel = Array.from(shadow()?.querySelectorAll<HTMLSelectElement>(".weft-drawer select") ?? []).find(
      (s) => Array.from(s.options).some((o) => o.value === "fork")
    )
    if (!threadSel) throw new Error("FAIL the drawer offers no thread mode")
    threadSel.value = "fork"
    threadSel.dispatchEvent(new (dom.window as unknown as { Event: typeof Event }).Event("change", { bubbles: true }))
    await sleep(100)
    const forkInput = Array.from(shadow()?.querySelectorAll<HTMLTextAreaElement>(".weft-drawer textarea") ?? []).at(-1)
    if (!forkInput) throw new Error("FAIL the drawer has no input field for the fork")
    const forked = "and order 7777?"
    forkInput.value = forked
    forkInput.dispatchEvent(new (dom.window as unknown as { Event: typeof Event }).Event("input", { bubbles: true }))
    await sleep(100)
    const forkRun = Array.from(shadow()?.querySelectorAll(".weft-drawer button") ?? []).find((b) =>
      b.textContent.includes("Run experiment")
    )
    ;(forkRun as HTMLElement | undefined)?.dispatchEvent(
      new (dom.window as unknown as { Event: typeof Event }).Event("click", { bubbles: true })
    )
    const forkText = await waitFor(() => {
      const res = text(".weft-xres")
      return res.includes(forked) && res.includes("finished") ? res : null
    }, "fork mode: the result moves to the new session's turn and answers the fork's input", 30000)
    if (/error|failed|rejected/i.test(text(".weft-xres .weft-warn"))) throw new Error(`FAIL fork: ${forkText}`)
    const href = shadow()?.querySelector(".weft-xres a")?.getAttribute("href") ?? ""
    const forkRunId = new URLSearchParams(new URL(href).hash.slice(1)).get("run") ?? ""
    if (!/-t\d+$/.test(forkRunId)) throw new Error(`FAIL fork: the pane is not on a thread turn: ${forkRunId}`)
    console.log(`PASS fork mode: the pane follows the forked session's turn ${forkRunId}`)
  }

  if (!args.otlp && args.pageScript) await pageScriptGate(args, panelJs)

  console.log("PANEL GATE PASS")
  dom.window.close()
  process.exit(0)
}

/** pageScriptGate drives the app's own served page (examples/studio-local's
 * `/`) with its inline script running: the panel's one global, the
 * "debug this" button, the "report this run" link and the parked-call
 * count (plan C4's Done line). The inline script is evaluated before
 * panel.js, as the page orders them; DOMContentLoaded is dispatched
 * after both. */
async function pageScriptGate(args: Args, panelJs: string) {
  const res = await fetch(args.page)
  if (!res.ok) throw new Error(`FAIL the app's page: ${res.status}`)
  const dom = new JSDOM(await res.text(), { url: args.page, runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window as unknown as { fetch: typeof fetch; EventSource: typeof EventSource; eval: (code: string) => void }
  const doc = dom.window.document
  const Ev = (dom.window as unknown as { Event: typeof Event }).Event
  // The page's own fetch("/run") is relative to the page.
  w.fetch = (input: RequestInfo | URL, init?: RequestInit) =>
    fetch(typeof input === "string" ? new URL(input, args.page) : input, init)
  w.EventSource = FetchEventSource as unknown as typeof EventSource
  Object.defineProperty(w, "addEventListener", { configurable: true, value: () => {} })
  Object.defineProperty(w, "removeEventListener", { configurable: true, value: () => {} })
  const inline = Array.from(doc.querySelectorAll("script:not([src])")).map((n) => n.textContent)
  if (!inline.length) throw new Error("FAIL the app's page has no inline script")
  for (const code of inline) w.eval(`(function(){\n${code}\n})()`)
  w.eval(`(function(){\n${panelJs}\n})()`)
  doc.dispatchEvent(new Ev("DOMContentLoaded"))
  const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))
  const waitFor = async (what: () => string | null, label: string, ms = 20000) => {
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
  const shadow = () => doc.querySelector("weft-devtools")?.shadowRoot
  await waitFor(
    () => ((dom.window as unknown as { weft?: { devtools?: unknown } }).weft?.devtools ? "global" : null),
    "page script: window.weft.devtools is the panel's API"
  )
  // A user asks once the panel is up: Studio answered and the header
  // rung reads the page's fetches (the footer says so).
  await waitFor(
    () => (shadow()?.querySelector(".weft-detect")?.textContent.includes("headers") ? "rung" : null),
    "page script: the panel answered and reads the page's Weft-Scope headers"
  )
  const input = doc.querySelector<HTMLInputElement>("#ask input[name=text]")
  if (!input) throw new Error("FAIL the page has no ask form")
  input.value = "refund order 42 (page script)"
  doc.getElementById("ask")?.dispatchEvent(new Ev("submit", { bubbles: true, cancelable: true }))
  await waitFor(
    () => (doc.body.getAttribute("data-parked-count") === "1" ? "1" : null),
    'page script: on("parked") fired once for the refund turn (data-parked-count="1")'
  )
  await waitFor(
    () => (doc.querySelector("button.debug-this") ? "button" : null),
    'page script: the reply carries its "debug this" button'
  )
  const debug = doc.querySelector<HTMLButtonElement>("button.debug-this")
  const run = debug?.dataset.run ?? ""
  if (!run) throw new Error("FAIL the debug button names no run")
  debug?.dispatchEvent(new Ev("click", { bubbles: true }))
  await waitFor(
    () => (shadow()?.querySelector(".weft-sel .weft-id")?.textContent === run ? run : null),
    `page script: "debug this" scoped and selected ${run} in the panel`
  )
  const arrow = await waitFor(() => {
    const h = Array.from(shadow()?.querySelectorAll("a") ?? []).find((a) => a.textContent === "⤢")?.getAttribute("href") ?? ""
    return h.includes(`/runs/${encodeURIComponent(run)}?step=0&view=story`) ? h : null
  }, "page script: the ⤢ link carries the selected step (0)")
  const report = doc.querySelector<HTMLAnchorElement>("#report")?.getAttribute("href") ?? ""
  if (!report.includes(`/runs/${encodeURIComponent(run)}?step=`) || report.includes("token"))
    throw new Error(`FAIL the report link is not the panel's run link: ${report}`)
  console.log(`PASS page script: "report this run" is the panel's studioLink: ${report} (⤢ ${arrow})`)
  dom.window.close()
}

main().catch((e) => {
  console.error(String(e))
  process.exit(1)
})
