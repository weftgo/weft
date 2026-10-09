// Badge parity (D5's Done line): every hole of the A3 table
// (studio/testdata/holes.golden.json, obsdb.HoleNote's words) is shown
// by the panel and by the Studio run page for the same run. One run per
// hole, served by one fake Studio (src/test/fake-studio.ts) to both
// surfaces in turn: the panel mounted on it, then the run page rendered
// on it; each must show data-hole="<hole>" with the table's words (the
// fix, or the reason where the table has none) in its title. And the
// pre-A1 run's Request tab says not_recorded on both surfaces.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { cleanup, configure, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "../lib/api"
import type { RunDoc } from "../lib/api"
import { HOLES, isHole } from "../lib/honesty"
import { renderApp, stubBrowser } from "../test/app"
import { FakeEventSource as StudioEventSource } from "../test/fake-event-source"
import { FakeStudio, golden, hiddenRefusal, pagedEvents, pagedRequests } from "../test/fake-studio"
import type { FakePosEvent } from "../test/fake-studio"
import { $, ATTRS, click, META, mount, runRow, SESSION, settle, setup, T0, teardown, transcript, user, assistant } from "./testkit"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const RUN = "s_01-t1"
const table = JSON.parse(readFileSync(resolve(process.cwd(), "../testdata/holes.golden.json"), "utf8")) as {
  hole: string
  reason: string
  fix?: string
}[]

interface Fixture {
  doc: Partial<RunDoc>
  events: FakePosEvent[]
  gaps?: number[]
  transcript?: unknown
  requests?: "not-recorded" | "hidden"
}

/** A one-step run: a lookup and its result; attrs on the result (or on
 * every event) as the record carries them. */
function events(opts: { attrs?: Record<string, unknown>; all?: Record<string, unknown>; finish?: boolean } = {}): FakePosEvent[] {
  const evs: unknown[] = [
    { type: "run_start", id: RUN, model: { provider: "wefttest", name: "script" }, agent: "acme-support" },
    { type: "step_start", run_id: RUN, index: 0 },
    { type: "tool_start", run_id: RUN, seq: 1, call_id: "c1", name: "lookup_order", args: { order_id: "42" } },
    { type: "tool_finish", run_id: RUN, seq: 2, call_id: "c1", name: "lookup_order", content: "shipped", is_error: false },
    { type: "step_finish", run_id: RUN, index: 0, reason: "stop", usage: { input_tokens: 5, output_tokens: 2 } },
  ]
  if (opts.finish !== false) evs.push({ type: "run_finish", run_id: RUN, usage: { input_tokens: 5, output_tokens: 2 }, steps: 1 })
  return evs.map((event, pos) => {
    const attrs = opts.all ?? (pos === 3 ? opts.attrs : undefined)
    return { pos, time: T0, event, ...(attrs ? { attrs } : {}) }
  })
}

/** The run per hole: what the API answers for a run exhibiting it. */
const FIXTURES: Partial<Record<string, () => Fixture>> = {
  truncated: () => ({ doc: {}, events: events({ attrs: { "weft.content.truncated_bytes": 12595 } }) }),
  stripped: () => ({ doc: {}, events: events({ all: { "weft.content": "stripped" } }) }),
  redacted: () => ({ doc: {}, events: events({ attrs: { "weft.content": "redacted" } }) }),
  max_tokens: () => ({ doc: { stop_reason: "max_tokens" }, events: events() }),
  interrupted: () => ({ doc: { status: "interrupted", finished: null }, events: events({ finish: false }) }),
  gap: () => ({ doc: {}, events: events().filter((e) => e.pos !== 2), gaps: [2] }),
  not_recorded: () => ({
    doc: {
      requests_badge: "not_recorded",
      request_count: 0,
      holes: [{ hole: "not_recorded", reason: HOLES.not_recorded.reason, fix: HOLES.not_recorded.fix }],
    },
    events: events(),
    requests: "not-recorded",
  }),
  derived: () => ({ doc: { holes: [{ hole: "derived", reason: HOLES.derived.reason }] }, events: events() }),
  hidden: () => ({ doc: {}, events: events(), requests: "hidden" }),
  compacted: () => ({
    doc: { compactions: golden<RunDoc>("run-compacted").compactions },
    events: golden<{ events: FakePosEvent[] }>("events-compacted").events,
    transcript: golden("transcript-compacted"),
  }),
}

const NOT_RECORDED = FIXTURES.not_recorded!

/** Holes neither surface can be shown from the API, with why. */
const SKIPPED: Record<string, string> = {}

function serve(fx: Fixture, studioMeta: boolean): FakeStudio {
  const row = runRow({ id: RUN, ...(fx.doc as object) })
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
  if (fx.requests === "hidden") fake.on(`GET runs/${RUN}/requests`, () => hiddenRefusal()).on(`GET runs/${RUN}/tools`, () => hiddenRefusal())
  else if (fx.requests === "not-recorded")
    fake
      .on(`GET runs/${RUN}/requests`, pagedRequests(golden("requests-not-recorded")))
      .on(`GET runs/${RUN}/tools`, golden<object>("tools-not-recorded"))
  else fake.on(`GET runs/${RUN}/requests`, { requests: [] })
  return fake.install()
}

/** The words a badge must carry: the table's fix, else its reason. */
const words = (hole: string) => {
  const g = table.find((t) => t.hole === hole)!
  return g.fix ?? g.reason
}

beforeEach(() => setup())
afterEach(() => {
  cleanup()
  teardown()
})

/** The panel on the fixture: the badge's title. */
async function panelShows(hole: string, fx: Fixture, sel = `[data-hole="${hole}"]`): Promise<string | null> {
  serve(fx, false)
  const el = await mount(ATTRS)
  await vi.waitFor(() => expect($(el, sel), `panel ${hole}`).toBeTruthy(), { timeout: 5_000 })
  const title = $(el, sel)!.getAttribute("title")
  el.remove()
  await settle()
  return title
}

/** The Studio run page on the same fixture. */
async function studioShows(hole: string, fx: Fixture, sel = `[data-hole="${hole}"]`): Promise<string | null> {
  stubBrowser()
  // jsdom has no scrolling: the router's restoration would say so.
  vi.stubGlobal("scrollTo", vi.fn())
  StudioEventSource.reset()
  vi.stubGlobal("EventSource", StudioEventSource)
  setStudioToken("")
  serve(fx, true)
  renderApp(`/runs/${RUN}?view=story`)
  await waitFor(() => expect(document.querySelector(sel), `studio ${hole}`).toBeTruthy())
  return document.querySelector(sel)!.getAttribute("title")
}

describe("badge parity over the A3 table (D5)", () => {
  it("the fixtures cover the table: one run per hole, or a reason it has none", () => {
    for (const g of table) {
      expect(isHole(g.hole)).toBe(true)
      expect(g.hole in FIXTURES || g.hole in SKIPPED, g.hole).toBe(true)
    }
  })

  for (const g of table) {
    const fixture = FIXTURES[g.hole]
    if (!fixture) {
      it.skip(`${g.hole}: ${SKIPPED[g.hole]}`, () => {})
      continue
    }
    it(`${g.hole}: the panel and the run page both badge it, with the table's words`, async () => {
      const inPanel = await panelShows(g.hole, fixture())
      expect(inPanel).toContain(words(g.hole))
      cleanup()
      document.body.innerHTML = ""
      const inStudio = await studioShows(g.hole, fixture())
      expect(inStudio).toContain(words(g.hole))
    })
  }
})

describe("a pre-A1 run's Request tab (D5's second Done clause)", () => {
  it("the run page's request section says not_recorded", async () => {
    const title = await studioShows("not_recorded", NOT_RECORDED(), `[data-request] [data-hole="not_recorded"]`)
    expect(title).toContain(HOLES.not_recorded.fix!)
  })

  // The panel's Request tab (E1.2) draws the record's hole.
  it("the panel's Request tab says not_recorded", async () => {
    serve(NOT_RECORDED(), false)
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
