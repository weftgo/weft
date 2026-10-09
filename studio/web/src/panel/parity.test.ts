// Badge parity (D5's Done line): every hole of the A3 table
// (studio/testdata/holes.golden.json, obsdb.HoleNote's words) is shown
// by the panel and by the Studio run page for the same run. One run per
// hole (and per loop cut), served by one fake Studio
// (src/test/fake-studio.ts) to both surfaces in turn: the panel mounted
// on it, then the run page rendered on it. Each must show
// data-hole="<hole>" where both mark its source (the run header, a step
// card, a tool call, a request section) with the table's words; every
// badge the run page shows anywhere must be one the panel shows too;
// and a hole-free baseline shows none on either. And the pre-A1 run's
// Request tab says not_recorded on both surfaces.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { cleanup, configure, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "../lib/api"
import type { RunDoc } from "../lib/api"
import { UNRUN_CALL_REASON } from "../lib/events"
import { REQUEST_NO_INDEX_REASON, REQUEST_NO_RECORD_REASON, REQUEST_NOT_RECORDED_LABEL } from "../lib/requests"
import { badgeLabel } from "./badges"
import { CAUSES, HOLES, isHole, resultCapReason } from "../lib/honesty"
import { renderApp, stubBrowser } from "../test/app"
import { renderWithRouter } from "../test/render"
import { createElement } from "react"
import { StepDiffTable } from "../components/studio/step-diff"
import { DIFF_COLUMNS, cellHole, markerWords, nWayView, stepDiffView } from "../lib/stepdiff"
import type { DiffDoc } from "../lib/stepdiff"
import { stepDiffBlock } from "./compare"
import { FakeEventSource as StudioEventSource } from "../test/fake-event-source"
import { FakeStudio, golden, hiddenRefusal, pagedEvents, pagedRequests } from "../test/fake-studio"
import type { FakePosEvent } from "../test/fake-studio"
import { $, all, assistant, ATTRS, click, META, mount, runRow, SESSION, settle, setup, T0, teardown, transcript, user } from "./testkit"
import type { WeftDevtools } from "./element"
import { buildRunBody as studioBody } from "../lib/experiment-body"
import type { VariantFields } from "../lib/experiment-body"
import type { ReplayEdit } from "../lib/edits"
import { previewView } from "../lib/preview"
import type { PreviewDoc } from "../lib/preview"
import { PreviewPane } from "../components/studio/transcript-editor"
import { previewBlock } from "./editor"
import { buildRunBody as panelBody } from "./playground"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const RUN = "s_01-t1"
const table = JSON.parse(readFileSync(resolve(process.cwd(), "../testdata/holes.golden.json"), "utf8")) as {
  hole: string
  reason: string
  fix?: string
}[]

/** Where both surfaces mark a hole's source. */
type Scope = "header" | "step" | "call" | "request"
const PANEL: Record<Scope, string> = {
  header: "[data-weft-turn-holes]",
  step: "[data-weft-step]",
  call: ".weft-call-h",
  request: "[data-weft-request]",
}
const STUDIO: Record<Scope, string> = {
  header: "[data-run-holes]",
  step: "[data-step]",
  call: "[data-call]",
  request: "[data-request]",
}

interface Fixture {
  doc: Partial<RunDoc>
  events: FakePosEvent[]
  gaps?: number[]
  transcript?: unknown
  requests?: "ok" | "empty" | "not-recorded" | "hidden"
  /** The request rows served as given (E1.2's pane cases), and the
   * tools route's answer beside them. */
  rows?: unknown[]
  tools?: unknown
}

/** A one-step run as an A4 weft records it: a lookup and its result,
 * the step's timing; attrs on the result (or on every event), the
 * result's content and the step's reason as given. */
function events(
  opts: {
    attrs?: Record<string, unknown>
    all?: Record<string, unknown>
    finish?: boolean
    content?: string
    reason?: string
  } = {}
): FakePosEvent[] {
  const evs: unknown[] = [
    { type: "run_start", id: RUN, model: { provider: "wefttest", name: "script" }, agent: "acme-support" },
    { type: "step_start", run_id: RUN, index: 0 },
    { type: "tool_start", run_id: RUN, seq: 1, call_id: "c1", name: "lookup_order", args: { order_id: "42" } },
    {
      type: "tool_finish",
      run_id: RUN,
      seq: 2,
      call_id: "c1",
      name: "lookup_order",
      content: opts.content ?? "shipped",
      is_error: false,
    },
    {
      type: "step_finish",
      run_id: RUN,
      index: 0,
      reason: opts.reason ?? "stop",
      usage: { input_tokens: 5, output_tokens: 2 },
      latency_ms: 120,
      ttft_ms: 40,
    },
  ]
  if (opts.finish !== false) evs.push({ type: "run_finish", run_id: RUN, usage: { input_tokens: 5, output_tokens: 2 }, steps: 1 })
  return evs.map((event, pos) => {
    const attrs = opts.all ?? (pos === 3 ? opts.attrs : undefined)
    return { pos, time: T0, event, ...(attrs ? { attrs } : {}) }
  })
}

/** The request record of the baseline: step 0's one attempt (the
 * recorded run's first row). */
const OK_REQUESTS = () => {
  const g = golden<{ requests: { index: number; step: number }[] }>("requests-ok")
  return { requests: g.requests.filter((r) => r.step === 0).slice(0, 1) }
}

/** One run per case: what the API answers for a run exhibiting the
 * hole, the scope both surfaces mark it in, and the words its badge
 * carries (the table's fix, else its reason, unless a response's). */
interface Case {
  hole: string
  scope: Scope
  words: string
  fixture: () => Fixture
}
const words = (hole: string) => {
  const g = table.find((t) => t.hole === hole)!
  return g.fix ?? g.reason
}
const CASES: Record<string, Case> = {
  truncated: {
    hole: "truncated",
    scope: "call",
    words: words("truncated"),
    fixture: () => ({ doc: {}, events: events({ attrs: { "weft.content.truncated_bytes": 12595 } }) }),
  },
  "truncated (result_cap)": {
    hole: "truncated",
    scope: "call",
    words: resultCapReason(4096),
    fixture: () => ({ doc: {}, events: events({ content: "shipped\n…[truncated 4096 bytes]" }) }),
  },
  stripped: {
    hole: "stripped",
    scope: "header",
    words: words("stripped"),
    fixture: () => ({ doc: {}, events: events({ all: { "weft.content": "stripped" } }) }),
  },
  // weft's pipeline never emits weft.content = redacted (Go reserves
  // the mark); this is a non-weft writer's shape, which both surfaces
  // still badge.
  redacted: {
    hole: "redacted",
    scope: "call",
    words: words("redacted"),
    fixture: () => ({ doc: {}, events: events({ attrs: { "weft.content": "redacted" } }) }),
  },
  max_tokens: {
    hole: "max_tokens",
    scope: "header",
    words: words("max_tokens"),
    fixture: () => ({ doc: { stop_reason: "max_tokens" }, events: events() }),
  },
  "max_tokens (unrun call)": {
    hole: "max_tokens",
    scope: "call",
    words: UNRUN_CALL_REASON,
    fixture: () => ({
      doc: {},
      events: events({ content: "tool call lookup_order was not executed: the response hit the output token limit" }),
    }),
  },
  interrupted: {
    hole: "interrupted",
    scope: "header",
    words: words("interrupted"),
    fixture: () => ({ doc: { status: "interrupted", finished: null }, events: events({ finish: false }) }),
  },
  gap: {
    hole: "gap",
    scope: "header",
    words: words("gap"),
    // A lost event (position 2): the record's gap, the run over.
    fixture: () => ({ doc: {}, events: events().filter((e) => e.pos !== 2), gaps: [2] }),
  },
  // A finished step no request row names: the record's gap, on the
  // step's request section (REQUEST_NO_RECORD_REASON on both).
  "gap (a step no request row names)": {
    hole: "gap",
    scope: "request",
    words: REQUEST_NO_RECORD_REASON,
    fixture: () => ({ doc: {}, events: events(), requests: "empty" }),
  },
  not_recorded: {
    hole: "not_recorded",
    scope: "request",
    words: words("not_recorded"),
    fixture: () => ({
      doc: {
        requests_badge: "not_recorded",
        request_count: 0,
        holes: [{ hole: "not_recorded", reason: HOLES.not_recorded.reason, fix: HOLES.not_recorded.fix }],
      },
      events: events(),
      requests: "not-recorded",
    }),
  },
  derived: {
    hole: "derived",
    scope: "header",
    words: words("derived"),
    fixture: () => ({ doc: { holes: [{ hole: "derived", reason: HOLES.derived.reason }] }, events: events() }),
  },
  hidden: {
    hole: "hidden",
    scope: "request",
    words: words("hidden"),
    fixture: () => ({ doc: {}, events: events(), requests: "hidden" }),
  },
  compacted: {
    hole: "compacted",
    scope: "step",
    words: words("compacted"),
    fixture: () => ({
      doc: { compactions: golden<RunDoc>("run-compacted").compactions },
      events: golden<{ events: FakePosEvent[] }>("events-compacted").events,
      transcript: golden("transcript-compacted"),
    }),
  },
}

/** The hole-free run every case departs from. */
const BASELINE = (): Fixture => ({ doc: {}, events: events() })

/** Holes neither surface can be shown from the API, with why. */
const SKIPPED: Record<string, string> = {}

function serve(fx: Fixture, studioMeta: boolean): FakeStudio {
  const row = runRow({ id: RUN, request_count: 1, ...(fx.doc as object) })
  const doc = { ...row, children: [], holes: [], ...fx.doc }
  const caps = ["ingest", "requests"]
  const fake = new FakeStudio()
    .on("GET meta", studioMeta ? { ...golden<Record<string, unknown>>("meta"), capabilities: caps } : { ...META, capabilities: caps })
    .on("GET sessions", { total: 1, sessions: [SESSION], next_before: null })
    .on("GET runs", { total: 1, runs: [row], next_before: null })
    .on(`GET runs/${RUN}`, doc)
    .on(`GET runs/${RUN}/events`, pagedEvents(fx.events, { gaps: fx.gaps ?? [], done: doc.status !== "running" }))
    .on(`GET runs/${RUN}/transcript`, fx.transcript ?? transcript([user("where is 42?")], [assistant("It shipped.")]))
    .on(`GET runs/${RUN}/spans`, { spans: [] })
  const variant = fx.requests ?? "ok"
  if (fx.rows) fake.on(`GET runs/${RUN}/requests`, pagedRequests({ requests: fx.rows as never })).on(`GET runs/${RUN}/tools`, fx.tools ?? golden<object>("tools-ok"))
  else if (variant === "hidden") fake.on(`GET runs/${RUN}/requests`, () => hiddenRefusal()).on(`GET runs/${RUN}/tools`, () => hiddenRefusal())
  else if (variant === "not-recorded")
    fake
      .on(`GET runs/${RUN}/requests`, pagedRequests(golden("requests-not-recorded")))
      .on(`GET runs/${RUN}/tools`, golden<object>("tools-not-recorded"))
  else if (variant === "empty") fake.on(`GET runs/${RUN}/requests`, { requests: [] }).on(`GET runs/${RUN}/tools`, golden<object>("tools-ok"))
  else fake.on(`GET runs/${RUN}/requests`, pagedRequests(OK_REQUESTS())).on(`GET runs/${RUN}/tools`, golden<object>("tools-ok"))
  return fake.install()
}

beforeEach(() => setup())
afterEach(() => {
  cleanup()
  teardown()
})

const holeSet = (nodes: Element[]) => [...new Set(nodes.map((n) => n.getAttribute("data-hole") ?? ""))].sort()

/** The panel on the fixture, at rest once its request line is drawn:
 * every hole it shows, and the badge in the case's scope. */
async function inPanel(fx: Fixture, sel?: string): Promise<{ holes: string[]; title: string | null; el: WeftDevtools }> {
  serve(fx, false)
  const el = await mount(ATTRS)
  await vi.waitFor(() => expect($(el, "[data-weft-request]"), "panel request line").toBeTruthy(), { timeout: 5_000 })
  if (sel) await vi.waitFor(() => expect($(el, sel), `panel ${sel}`).toBeTruthy(), { timeout: 5_000 })
  await settle()
  return { holes: holeSet(all(el, "[data-hole]")), title: sel ? $(el, sel)!.getAttribute("title") : null, el }
}

/** The run page on the same fixture, at rest once its request
 * sections are drawn. */
async function inStudio(fx: Fixture, sel?: string): Promise<{ holes: string[]; title: string | null }> {
  stubBrowser()
  // jsdom has no scrolling: the router's restoration would say so.
  vi.stubGlobal("scrollTo", vi.fn())
  StudioEventSource.reset()
  vi.stubGlobal("EventSource", StudioEventSource)
  setStudioToken("")
  const fake = serve(fx, true)
  renderApp(`/runs/${RUN}?view=story`)
  await waitFor(() => expect(document.querySelector("[data-request]"), "studio request section").toBeTruthy())
  await waitFor(() => expect(fake.calls(`GET runs/${RUN}/requests`).length).toBeGreaterThan(0))
  if (sel) await waitFor(() => expect(document.querySelector(sel), `studio ${sel}`).toBeTruthy())
  // The request record's answer lands in a later render.
  await new Promise((r) => setTimeout(r, 150))
  return {
    holes: holeSet(Array.from(document.querySelectorAll("[data-hole]"))),
    title: sel ? document.querySelector(sel)!.getAttribute("title") : null,
  }
}

async function both(fx: Fixture, scope?: Scope, hole?: string) {
  const p = await inPanel(fx, scope && `${PANEL[scope]} [data-hole="${hole}"]`)
  p.el.remove()
  await settle()
  cleanup()
  document.body.innerHTML = ""
  const s = await inStudio(fx, scope && `${STUDIO[scope]} [data-hole="${hole}"]`)
  return { panel: p, studio: s }
}

describe("badge parity over the A3 table (D5)", () => {
  it("the cases cover the table: one run per hole, or a reason it has none", () => {
    const covered = new Set(Object.values(CASES).map((c) => c.hole))
    for (const g of table) {
      expect(isHole(g.hole)).toBe(true)
      expect(covered.has(g.hole) || g.hole in SKIPPED, g.hole).toBe(true)
    }
  })

  it("the baseline run shows no badge on either surface", async () => {
    const { panel, studio } = await both(BASELINE())
    expect(panel.holes).toEqual([])
    expect(studio.holes).toEqual([])
  })

  for (const [name, c] of Object.entries(CASES))
    it(`${name}: both surfaces badge it where they mark its source, with the table's words; the run page's badges are the panel's`, async () => {
      const { panel, studio } = await both(c.fixture(), c.scope, c.hole)
      expect(panel.title, "panel").toContain(c.words)
      expect(studio.title, "run page").toContain(c.words)
      expect(panel.holes).toContain(c.hole)
      // Every badge the run page shows for this run, the panel shows.
      expect(studio.holes.filter((h) => !panel.holes.includes(h)), `run page ${studio.holes} vs panel ${panel.holes}`).toEqual([])
    })

  for (const [hole, why] of Object.entries(SKIPPED)) it.skip(`${hole}: ${why}`, () => {})
})

describe("the loop's cut words are shared", () => {
  it("result_cap and the unrun call read the same words the table and lib/events give", () => {
    expect(resultCapReason(4096)).toBe(`${CAUSES.truncated!.result_cap.reason} (4.0 KiB cut)`)
    expect(UNRUN_CALL_REASON).toMatch(/not executed: the response hit the output token limit/)
  })
})

describe("a pre-A1 run's Request tab (D5's second Done clause)", () => {
  it("the run page's request section says not_recorded", async () => {
    const s = await inStudio(CASES.not_recorded.fixture(), `[data-request] [data-hole="not_recorded"]`)
    expect(s.title).toContain(HOLES.not_recorded.fix!)
  })

  // The panel's Request tab (E1.2) draws the record's hole.
  it("the panel's Request tab says not_recorded", async () => {
    serve(CASES.not_recorded.fixture(), false)
    const el = await mount(ATTRS)
    await vi.waitFor(() => expect($(el, "#weft-tab-request")).toBeTruthy())
    click($(el, "#weft-tab-request"))
    await settle()
    const tab = $(el, '[data-key="tp:request"]')!
    expect(tab).toBeTruthy()
    expect(tab.textContent).not.toMatch(/lands with E1\.2/)
    await vi.waitFor(() => expect(tab.querySelector('[data-hole="not_recorded"]')).toBeTruthy())
    expect(tab.querySelector('[data-hole="not_recorded"]')!.getAttribute("title")).toContain(HOLES.not_recorded.fix!)
  })
})

// E1.2 review: the Request panes word a record's holes alike — the
// panel's Request tab and the run page's open Request pane, on the same
// row: a derived prompt and catalog (bodies that did not parse), a
// not_recorded HoleRef (the version's label), a stripped one (the tools
// route's reason and fix) and a request no messages record names.
describe("the Request panes word a record's holes alike (E1.2)", () => {
  type Seen = { hole: string; label: string; title: string }
  const seen = (nodes: Element[]): Seen[] =>
    nodes.map((n) => ({ hole: n.getAttribute("data-hole") ?? "", label: badgeLabel(n), title: n.getAttribute("title") ?? "" }))
  const key = (x: Seen) => `${x.hole} | ${x.label} | ${x.title}`
  const row0 = () => structuredClone(golden<{ requests: Record<string, unknown>[] }>("requests-ok").requests[0]) as Record<string, any>

  async function panes(fx: Fixture): Promise<{ panel: Seen[]; studio: Seen[] }> {
    serve(fx, false)
    const el = await mount(ATTRS)
    await vi.waitFor(() => expect($(el, "#weft-tab-request")).toBeTruthy())
    click($(el, "#weft-tab-request"))
    await vi.waitFor(() => expect($(el, "#weft-tp-request [data-weft-rq-pane]")).toBeTruthy(), { timeout: 5_000 })
    await settle()
    const panel = seen(all(el, "#weft-tp-request [data-weft-rq-pane] [data-hole]"))
    el.remove()
    await settle()
    cleanup()
    document.body.innerHTML = ""
    stubBrowser()
    vi.stubGlobal("scrollTo", vi.fn())
    StudioEventSource.reset()
    vi.stubGlobal("EventSource", StudioEventSource)
    setStudioToken("")
    serve(fx, true)
    renderApp(`/runs/${RUN}?view=story`)
    const pane = () => document.querySelector<HTMLElement>('[data-request="0"]')
    await waitFor(() => expect(pane()?.querySelector("button[aria-expanded]")).toBeTruthy())
    pane()!.querySelector<HTMLElement>("button[aria-expanded]")!.click()
    await new Promise((r) => setTimeout(r, 150))
    return { panel, studio: seen(Array.from(pane()!.querySelectorAll("[data-hole]"))) }
  }
  const expectBoth = (r: { panel: Seen[]; studio: Seen[] }, want: Partial<Seen>[]) => {
    for (const w of want)
      for (const [side, list] of Object.entries(r))
        expect(
          list.some((x) => Object.entries(w).every(([k, v]) => x[k as "hole"].includes(v))),
          `${side} lacks ${JSON.stringify(w)}: ${list.map(key).join("; ")}`
        ).toBe(true)
    // What the run page's pane says of the record, the panel's says.
    expect(r.studio.map(key).filter((k) => !r.panel.map(key).includes(k))).toEqual([])
  }

  it("a derived prompt and catalog: the derived badge on both blocks of both panes, no prompt box", async () => {
    const r = row0()
    r.prompt = { hash: r.system_hash, text: "", content: "derived", truncated_bytes: 0 }
    r.tools = { hash: r.catalog_hash, tools: [], content: "derived", truncated_bytes: 0 }
    const got = await panes({ doc: {}, events: events(), rows: [r] })
    expect(got.panel.filter((x) => x.hole === "derived").length).toBe(2)
    expect(got.studio.filter((x) => x.hole === "derived").length).toBe(2)
    expectBoth(got, [{ hole: "derived", title: HOLES.derived.reason }])
  })

  it("a not_recorded prompt HoleRef names the version; a request no messages record names says why — the same words", async () => {
    const r = row0()
    r.prompt = { hash: r.system_hash, badge: "not_recorded" }
    r.body.messages_ref = { count: 1 }
    expectBoth(await panes({ doc: {}, events: events(), rows: [r] }), [
      { hole: "not_recorded", label: REQUEST_NOT_RECORDED_LABEL },
      { hole: "gap", title: REQUEST_NO_INDEX_REASON },
    ])
  })

  // A running run's request row can land before the transcript batch
  // it names: the run page reads that as the neutral no_transcript, and
  // so must the panel's Request tab (messagesSent's running argument).
  it("a running run whose transcript batch has not landed: neutral on both panes, never the red gap", async () => {
    const r = row0()
    r.body.messages_ref = { count: 3, index: 99 }
    const fx: Fixture = { doc: { status: "running", finished: null }, events: events({ finish: false }), rows: [r] }
    const got = await panes(fx)
    expect(got.panel.filter((x) => x.hole === "gap"), "panel").toEqual([])
    expect(got.studio.filter((x) => x.hole === "gap"), "run page").toEqual([])
    expect(document.querySelector('[data-request="0"]')!.textContent).toContain("bytes when the transcript is read")
    serve(fx, false)
    const el = await mount(ATTRS)
    await vi.waitFor(() => expect($(el, "#weft-tab-request")).toBeTruthy())
    click($(el, "#weft-tab-request"))
    await vi.waitFor(() => expect($(el, "#weft-tp-request [data-weft-messages-line]")).toBeTruthy(), { timeout: 5_000 })
    await settle()
    expect($(el, "#weft-tp-request [data-weft-rq-pane]")!.textContent).toContain("bytes when the transcript is read")
    expect(all(el, '#weft-tp-request [data-hole="gap"]')).toEqual([])
    el.remove()
  })

  it("a stripped HoleRef carries the tools route's reason and fix on both", async () => {
    const r = row0()
    r.content = "stripped"
    r.prompt = { hash: r.system_hash, badge: "stripped" }
    r.tools = { hash: r.catalog_hash, badge: "stripped" }
    const tools = { catalogs: [], badge: "stripped", reason: "the destination stripped it (otel.NoContent)", fix: "drop otel.NoContent()" }
    expectBoth(await panes({ doc: {}, events: events(), rows: [r], tools }), [
      { hole: "stripped", title: "the destination stripped it (otel.NoContent) — fix: drop otel.NoContent()" },
    ])
  })
})

// E3.2's parity: the step compare. The same GET /api/diff response
// (the E3.1 goldens, some patched with marks or a truncated hole) drawn
// by the panel's 2-way block (panel/compare.ts) and by Studio's table
// (components/studio/step-diff.tsx) gives the same rows, the same cell
// states, the same "changed at step N" markers, the same badges per
// side (a hole-mark one badge, never twice) and in the cells, the same
// chips — both read lib/stepdiff.ts.
describe("step compare parity (E3.2)", () => {
  interface Drawn {
    markers: { step: string; text: string }[]
    cells: Record<string, Record<string, string>>
    /** Per row and side: the badges, then the chips. */
    holes: Record<string, string[]>
    chips: Record<string, string[]>
    /** Per row: the badges inside the compared cells. */
    cellHoles: Record<string, string[]>
    /** The response's own badges. */
    top: string[]
  }
  const blank = (): Drawn => ({ markers: [], cells: {}, holes: {}, chips: {}, cellHoles: {}, top: [] })
  const attr = (ns: Iterable<Element>, a: string) => [...ns].map((n) => n.getAttribute(a)!)

  function panelDrawn(doc: DiffDoc): Drawn {
    const box = stepDiffBlock(doc, "http://studio.test/studio/")
    const out = blank()
    out.top = attr(box.querySelectorAll(":scope > .weft-note [data-hole]"), "data-hole")
    for (const m of box.querySelectorAll("[data-weft-diff-marker]"))
      out.markers.push({ step: m.getAttribute("data-weft-diff-marker")!, text: m.textContent })
    for (const tr of box.querySelectorAll("tbody tr")) {
      const n = tr.getAttribute("data-weft-diff-step")!
      out.cells[n] = {}
      for (const td of tr.querySelectorAll("[data-weft-diff-cell]"))
        out.cells[n][td.getAttribute("data-weft-diff-cell")!] = td.getAttribute("data-state")!
      out.cellHoles[n] = attr(tr.querySelectorAll("[data-weft-diff-cell] [data-hole]"), "data-hole")
      for (const side of ["a", "b"]) {
        out.holes[`${n}${side}`] = attr(tr.querySelectorAll(`[data-weft-diff-side="${side}"] [data-hole]`), "data-hole")
        out.chips[`${n}${side}`] = attr(tr.querySelectorAll(`[data-weft-diff-side="${side}"] [data-weft-diff-mark]`), "data-weft-diff-mark")
      }
    }
    return out
  }

  async function studioDrawn(doc: DiffDoc): Promise<Drawn> {
    const { container } = await renderWithRouter(createElement(StepDiffTable, { view: nWayView([doc]) }))
    await waitFor(() => expect(container.querySelector("[data-step-diff]")).toBeTruthy())
    const out = blank()
    out.top = attr(container.querySelectorAll("[data-diff-holes] [data-hole]"), "data-hole")
    for (const m of container.querySelectorAll("[data-diff-marker]"))
      out.markers.push({ step: m.getAttribute("data-diff-marker")!, text: m.querySelector("a")!.textContent })
    for (const tr of container.querySelectorAll("tbody tr")) {
      const n = tr.getAttribute("data-diff-step")!
      out.cells[n] = {}
      const mine = `[data-diff-run="${doc.b.run_id}"][data-diff-state]`
      for (const td of tr.querySelectorAll(mine)) out.cells[n][td.getAttribute("data-diff-cell")!] = td.getAttribute("data-diff-state")!
      out.cellHoles[n] = attr(tr.querySelectorAll(`${mine} [data-hole]`), "data-hole")
      for (const [side, run] of [["a", doc.a.run_id], ["b", doc.b.run_id]]) {
        out.holes[`${n}${side}`] = attr(tr.querySelectorAll(`[data-diff-run="${run}"][data-diff-marks] [data-hole]`), "data-hole")
        out.chips[`${n}${side}`] = attr(tr.querySelectorAll(`[data-diff-run="${run}"][data-diff-marks] [data-diff-mark]`), "data-diff-mark")
      }
    }
    cleanup()
    return out
  }

  /** want is what lib/stepdiff.ts says either surface must draw. */
  function want(doc: DiffDoc): Drawn {
    const v = stepDiffView(doc)
    const out = blank()
    out.top = v.holes.map((h) => h.hole)
    for (const r of v.rows) {
      const n = String(r.step)
      if (r.changed) out.markers.push({ step: n, text: markerWords(r) })
      out.cells[n] = { ...r.cells }
      out.cellHoles[n] = DIFF_COLUMNS.flatMap((c) => cellHole(c, r.a.side, r.b.side) ?? [])
      for (const [side, sv] of [["a", r.a], ["b", r.b]] as const) {
        out.holes[`${n}${side}`] = [...sv.marks.flatMap((m) => m.hole ?? []), ...sv.holes.map((h) => h.hole)]
        out.chips[`${n}${side}`] = sv.marks.filter((m) => !m.hole).map((m) => m.mark)
      }
    }
    return out
  }

  /** marked is diff.golden.json with marks on step 1's sides:
   * a compaction and a subagent call on a, max_tokens on b — listed in
   * b's holes too, as the server does — and its response truncated. */
  function marked(): DiffDoc {
    const doc = golden<DiffDoc>("diff")
    const s1 = doc.steps[1]
    s1.a = { ...s1.a!, marks: ["compacted", "subagent"] }
    s1.b = { ...s1.b!, marks: ["max_tokens"], holes: [{ hole: "max_tokens", reason: "the step finished on the output token limit" }] }
    doc.holes = [{ hole: "truncated", reason: "this response reads a bounded number of steps", fix: "open the later steps" }]
    return doc
  }

  const cases: [string, () => DiffDoc][] = [
    ["diff", () => golden<DiffDoc>("diff")],
    ["diff-hidden", () => golden<DiffDoc>("diff-hidden")],
    ["diff-not-recorded", () => golden<DiffDoc>("diff-not-recorded")],
    ["diff with marks and a truncated response", marked],
  ]
  it.each(cases)("%s: the same rows, cells, markers, badges and chips on both surfaces", async (_, make) => {
    const doc = make()
    expect(panelDrawn(doc), "panel").toEqual(want(doc))
    expect(await studioDrawn(doc), "studio").toEqual(want(doc))
  })

  it("marks: one badge per hole per side — max_tokens in both marks and holes is drawn once — subagent a chip", async () => {
    const doc = marked()
    for (const got of [panelDrawn(doc), await studioDrawn(doc)]) {
      expect(got.holes["1a"]).toEqual(["compacted"])
      expect(got.chips["1a"]).toEqual(["subagent"])
      expect(got.holes["1b"]).toEqual(["max_tokens"])
      expect(got.top).toEqual(["truncated"])
    }
  })

  it("a read token: the system cells carry the hidden badge on both, their state the server's", async () => {
    const doc = golden<DiffDoc>("diff-hidden")
    for (const got of [panelDrawn(doc), await studioDrawn(doc)])
      for (const n of ["0", "1", "2", "3", "4"]) {
        expect(got.cellHoles[n]).toEqual(["hidden"])
        expect(got.cells[n].system).toBe("same")
      }
  })

  it("the Done line: a tool result that differs at step 3 is one marker on both, every other row the same", async () => {
    const doc = golden<DiffDoc>("diff")
    for (const got of [panelDrawn(doc), await studioDrawn(doc)]) {
      expect(got.markers).toEqual([{ step: "3", text: "changed at step 3 · tool results" }])
      for (const [n, row] of Object.entries(got.cells))
        expect(Object.values(row).filter((s) => s !== "same")).toEqual(n === "3" ? ["changed"] : [])
    }
  })
})

// Plan F2: one transcript_edits and one preview on both surfaces. The
// same edits (the Done line's, all five kinds) build byte-identical
// command bodies through Studio's buildRunBody and the panel's; the
// same preview answer (the playground-preview golden, and its hidden
// variant) draws the same rows — op, was, will — the same system
// state, changed knobs, holes, warnings and unchecked line.
describe("the transcript editor's command and preview (F2 parity)", () => {
  const edits: ReplayEdit[] = [
    { kind: "user", step: 0, content: "refund order 7" },
    { kind: "tool_args", step: 0, callID: "c1", args: { q: "c9" } },
    { kind: "tool_result", step: 1, callID: "c2", toolResult: "policy: no refunds" },
    { kind: "reply", step: 1, content: "rewritten" },
    { kind: "insert", step: 2, content: "and check 43" },
  ]

  it("both surfaces post byte-identical transcript_edits for the same edits", () => {
    const variant: VariantFields = {
      instructions: "",
      toolsOff: new Set(),
      model: "",
      thinking: "",
      input: "",
      engine: "live",
      sideEffects: "substitute",
      thread: "ephemeral",
    }
    const agent = { name: "acme-support", models: [], tools: [{ name: "lookup_order", side_effects: "never", allow: false }] }
    const studio = studioBody({ runtime: "rt_1", agent, variant, sourceRunID: RUN, fromStep: 3, input: "", edits })
    const panel = panelBody(
      {
        edits,
        runId: RUN,
        agent: "acme-support",
        step: 3,
        instructions: "",
        registeredInstructions: "",
        tools: { lookup_order: true },
        model: "",
        thinking: "",
        input: "",
        engine: "live",
        sideEffects: "substitute",
        thread: "ephemeral",
        runtimeId: "rt_1",
      },
      ""
    )
    expect(JSON.stringify(panel.transcript_edits)).toBe(JSON.stringify(studio.transcript_edits))
    expect(JSON.stringify(panel)).toBe(JSON.stringify(studio))
    expect((studio.transcript_edits as { kind: string }[]).map((e) => e.kind)).toEqual(["user", "tool_args", "tool_result", "reply", "insert"])
  })

  type Rows = { system: string; changed: string[]; holes: string[]; rows: string[][]; warnings: string[]; unchecked: string }
  const read = (root: ParentNode, p: string): Rows => ({
    system: root.querySelector(`[data-${p}preview-system]`)!.getAttribute(`data-${p}preview-system`)!,
    changed: [...root.querySelectorAll(`[data-${p}preview-changed]`)].map((n) => n.getAttribute(`data-${p}preview-changed`)!),
    holes: [...root.querySelectorAll("[data-hole]")].map((n) => n.getAttribute("data-hole")!),
    rows: [...root.querySelectorAll(`[data-${p}preview-op]`)].map((r) => [
      r.getAttribute(`data-${p}preview-op`)!,
      r.querySelector(`[data-${p}preview-was]`)?.textContent ?? "",
      r.querySelector(`[data-${p}preview-will]`)?.textContent ?? "",
    ]),
    warnings: [...root.querySelectorAll(`[data-${p}preview-warning]`)].map((n) => `${n.getAttribute(`data-${p}preview-warning`)}: ${n.textContent}`),
    unchecked: root.querySelector(`[data-${p}preview-unchecked]`)?.textContent ?? "",
  })
  const PV = golden<PreviewDoc>("playground-preview")
  const hidden: PreviewDoc = {
    ...PV,
    will_send: { ...PV.will_send, system: null, system_badge: "hidden", tools: null, tools_badge: "hidden" },
    was_sent: { ...PV.was_sent, system: null, system_badge: "hidden", tools: null, tools_badge: "hidden", badge: "derived" },
    diff: { ...PV.diff, system: "hidden", tools: null },
    unchecked: ["model"],
  }

  for (const [name, doc] of [["the golden", PV], ["hidden to a read token, unchecked", hidden]] as const)
    it(`both surfaces draw the same preview rows: ${name}`, async () => {
      const panel = read(previewBlock({ doc }, 2), "weft-")
      const { container } = await renderWithRouter(createElement(PreviewPane, { state: { doc }, fromStep: 2 }))
      await waitFor(() => expect(container.querySelector("[data-preview-system]")).toBeTruthy())
      const studio = read(container, "")
      cleanup()
      expect(panel).toEqual(studio)
      const v = previewView(doc)
      expect(studio.rows).toEqual(v.rows!.map((r) => [r.op, r.was, r.will]))
      expect(studio.holes).toEqual(v.holes.map((h) => h.hole))
    })
})
