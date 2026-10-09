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
import { REQUEST_NO_RECORD_REASON } from "../lib/requests"
import { CAUSES, HOLES, isHole, resultCapReason } from "../lib/honesty"
import { renderApp, stubBrowser } from "../test/app"
import { FakeEventSource as StudioEventSource } from "../test/fake-event-source"
import { FakeStudio, golden, hiddenRefusal, pagedEvents, pagedRequests } from "../test/fake-studio"
import type { FakePosEvent } from "../test/fake-studio"
import { $, all, assistant, ATTRS, click, META, mount, runRow, SESSION, settle, setup, T0, teardown, transcript, user } from "./testkit"
import type { WeftDevtools } from "./element"

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
  if (variant === "hidden") fake.on(`GET runs/${RUN}/requests`, () => hiddenRefusal()).on(`GET runs/${RUN}/tools`, () => hiddenRefusal())
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
