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
import {
  cleanup,
  configure,
  fireEvent,
  screen,
  waitFor,
} from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "../lib/api"
import type { RunDoc, RunRow } from "../lib/api"
import { UNRUN_CALL_REASON } from "../lib/events"
import {
  REQUEST_NO_INDEX_REASON,
  REQUEST_NO_RECORD_REASON,
  REQUEST_NOT_RECORDED_LABEL,
} from "../lib/requests"
import { badgeLabel } from "./badges"
import {
  CAUSES,
  HOLES,
  isHole,
  PLAYGROUND_OFF,
  resultCapReason,
  RUNTIMES_UNREAD,
} from "../lib/honesty"
import { renderApp, stubBrowser } from "../test/app"
import { renderWithRouter } from "../test/render"
import { createElement } from "react"
import { StepDiffTable } from "../components/studio/step-diff"
import {
  DIFF_COLUMNS,
  cellHole,
  markerWords,
  nWayView,
  stepDiffView,
} from "../lib/stepdiff"
import type { DiffDoc } from "../lib/stepdiff"
import { stepDiffBlock } from "./compare"
import { FakeEventSource as StudioEventSource } from "../test/fake-event-source"
import {
  FakeStudio,
  golden,
  hiddenRefusal,
  pagedEvents,
  pagedRequests,
} from "../test/fake-studio"
import type { FakePosEvent } from "../test/fake-studio"
import {
  $,
  all,
  assistant,
  ATTRS,
  baseRoutes,
  button,
  click,
  fakeStudio as panelStudio,
  META,
  mount,
  runExperiment,
  runRow,
  RUNTIMES,
  SESSION,
  settle,
  setup,
  T0,
  teardown,
  transcript,
  user,
} from "./testkit"
import {
  keepEditedTitle,
  keepPromptWords,
  PROPOSAL_NEEDS_DEV,
  SAVE_TEST_LABEL,
  SAVE_TEST_TITLE,
} from "../lib/saves"
import { resetNotices } from "../lib/notify"
import type { WeftDevtools } from "./element"
import { buildRunBody as studioBody, emptyLab } from "../lib/experiment-body"
import type { VariantFields } from "../lib/experiment-body"
import type { ReplayEdit } from "../lib/edits"
import { previewView } from "../lib/preview"
import type { PreviewDoc } from "../lib/preview"
import { PreviewPane } from "../components/studio/transcript-editor"
import { previewBlock } from "./editor"
import { buildRunBody as panelBody } from "./playground"
import type { ExperimentDraft } from "./playground"
import { studioPlaygroundLink } from "./element"
import { drawerHandoff } from "../components/studio/replay-drawer"
import { playgroundLink } from "../lib/links"
import {
  DERIVED,
  STOCK as DERIVED_STOCK,
  derivedBodies,
  serveDerived,
} from "../test/derived-fixture"
import {
  APP_PUB,
  CALL as APP_CALL,
  NOPUB as APP_NOPUB,
  PARKED as APP_PARKED,
  PLAIN as APP_PLAIN,
  RUNTIMES_500 as APP_RUNTIMES_500,
  runtimeRow as appRuntime,
  serveAppTurn,
} from "../test/appturn-fixture"
import type { AppTurnOptions } from "../test/appturn-fixture"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const RUN = "s_01-t1"
const table = JSON.parse(
  readFileSync(resolve(process.cwd(), "../testdata/holes.golden.json"), "utf8")
) as {
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
    {
      type: "run_start",
      id: RUN,
      model: { provider: "wefttest", name: "script" },
      agent: "acme-support",
    },
    { type: "step_start", run_id: RUN, index: 0 },
    {
      type: "tool_start",
      run_id: RUN,
      seq: 1,
      call_id: "c1",
      name: "lookup_order",
      args: { order_id: "42" },
    },
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
  if (opts.finish !== false)
    evs.push({
      type: "run_finish",
      run_id: RUN,
      usage: { input_tokens: 5, output_tokens: 2 },
      steps: 1,
    })
  return evs.map((event, pos) => {
    const attrs = opts.all ?? (pos === 3 ? opts.attrs : undefined)
    return { pos, time: T0, event, ...(attrs ? { attrs } : {}) }
  })
}

/** The request record of the baseline: step 0's one attempt (the
 * recorded run's first row). */
const OK_REQUESTS = () => {
  const g = golden<{ requests: { index: number; step: number }[] }>(
    "requests-ok"
  )
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
    fixture: () => ({
      doc: {},
      events: events({ attrs: { "weft.content.truncated_bytes": 12595 } }),
    }),
  },
  "truncated (result_cap)": {
    hole: "truncated",
    scope: "call",
    words: resultCapReason(4096),
    fixture: () => ({
      doc: {},
      events: events({ content: "shipped\n…[truncated 4096 bytes]" }),
    }),
  },
  stripped: {
    hole: "stripped",
    scope: "header",
    words: words("stripped"),
    fixture: () => ({
      doc: {},
      events: events({ all: { "weft.content": "stripped" } }),
    }),
  },
  // weft's pipeline never emits weft.content = redacted (Go reserves
  // the mark); this is a non-weft writer's shape, which both surfaces
  // still badge.
  redacted: {
    hole: "redacted",
    scope: "call",
    words: words("redacted"),
    fixture: () => ({
      doc: {},
      events: events({ attrs: { "weft.content": "redacted" } }),
    }),
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
      events: events({
        content:
          "tool call lookup_order was not executed: the response hit the output token limit",
      }),
    }),
  },
  interrupted: {
    hole: "interrupted",
    scope: "header",
    words: words("interrupted"),
    fixture: () => ({
      doc: { status: "interrupted", finished: null },
      events: events({ finish: false }),
    }),
  },
  gap: {
    hole: "gap",
    scope: "header",
    words: words("gap"),
    // A lost event (position 2): the record's gap, the run over.
    fixture: () => ({
      doc: {},
      events: events().filter((e) => e.pos !== 2),
      gaps: [2],
    }),
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
        holes: [
          {
            hole: "not_recorded",
            reason: HOLES.not_recorded.reason,
            fix: HOLES.not_recorded.fix,
          },
        ],
      },
      events: events(),
      requests: "not-recorded",
    }),
  },
  derived: {
    hole: "derived",
    scope: "header",
    words: words("derived"),
    fixture: () => ({
      doc: { holes: [{ hole: "derived", reason: HOLES.derived.reason }] },
      events: events(),
    }),
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
    .on(
      "GET meta",
      studioMeta
        ? { ...golden<Record<string, unknown>>("meta"), capabilities: caps }
        : { ...META, capabilities: caps }
    )
    .on("GET sessions", { total: 1, sessions: [SESSION], next_before: null })
    .on("GET runs", { total: 1, runs: [row], next_before: null })
    .on(`GET runs/${RUN}`, doc)
    .on(
      `GET runs/${RUN}/events`,
      pagedEvents(fx.events, {
        gaps: fx.gaps ?? [],
        done: doc.status !== "running",
      })
    )
    .on(
      `GET runs/${RUN}/transcript`,
      fx.transcript ??
        transcript([user("where is 42?")], [assistant("It shipped.")])
    )
    .on(`GET runs/${RUN}/spans`, { spans: [] })
  const variant = fx.requests ?? "ok"
  if (fx.rows)
    fake
      .on(
        `GET runs/${RUN}/requests`,
        pagedRequests({ requests: fx.rows as never })
      )
      .on(`GET runs/${RUN}/tools`, fx.tools ?? golden<object>("tools-ok"))
  else if (variant === "hidden")
    fake
      .on(`GET runs/${RUN}/requests`, () => hiddenRefusal())
      .on(`GET runs/${RUN}/tools`, () => hiddenRefusal())
  else if (variant === "not-recorded")
    fake
      .on(
        `GET runs/${RUN}/requests`,
        pagedRequests(golden("requests-not-recorded"))
      )
      .on(`GET runs/${RUN}/tools`, golden<object>("tools-not-recorded"))
  else if (variant === "empty")
    fake
      .on(`GET runs/${RUN}/requests`, { requests: [] })
      .on(`GET runs/${RUN}/tools`, golden<object>("tools-ok"))
  else
    fake
      .on(`GET runs/${RUN}/requests`, pagedRequests(OK_REQUESTS()))
      .on(`GET runs/${RUN}/tools`, golden<object>("tools-ok"))
  return fake.install()
}

beforeEach(() => setup())
afterEach(() => {
  cleanup()
  teardown()
})

const holeSet = (nodes: Element[]) =>
  [...new Set(nodes.map((n) => n.getAttribute("data-hole") ?? ""))].sort()

/** The panel on the fixture, at rest once its request line is drawn:
 * every hole it shows, and the badge in the case's scope. */
async function inPanel(
  fx: Fixture,
  sel?: string
): Promise<{ holes: string[]; title: string | null; el: WeftDevtools }> {
  serve(fx, false)
  const el = await mount(ATTRS)
  await vi.waitFor(
    () =>
      expect($(el, "[data-weft-request]"), "panel request line").toBeTruthy(),
    { timeout: 5_000 }
  )
  if (sel)
    await vi.waitFor(() => expect($(el, sel), `panel ${sel}`).toBeTruthy(), {
      timeout: 5_000,
    })
  await settle()
  return {
    holes: holeSet(all(el, "[data-hole]")),
    title: sel ? $(el, sel)!.getAttribute("title") : null,
    el,
  }
}

/** The run page on the same fixture, at rest once its request
 * sections are drawn. */
async function inStudio(
  fx: Fixture,
  sel?: string
): Promise<{ holes: string[]; title: string | null }> {
  stubBrowser()
  // jsdom has no scrolling: the router's restoration would say so.
  vi.stubGlobal("scrollTo", vi.fn())
  StudioEventSource.reset()
  vi.stubGlobal("EventSource", StudioEventSource)
  setStudioToken("")
  const fake = serve(fx, true)
  renderApp(`/runs/${RUN}?view=story`)
  await waitFor(() =>
    expect(
      document.querySelector("[data-request]"),
      "studio request section"
    ).toBeTruthy()
  )
  await waitFor(() =>
    expect(fake.calls(`GET runs/${RUN}/requests`).length).toBeGreaterThan(0)
  )
  if (sel)
    await waitFor(() =>
      expect(document.querySelector(sel), `studio ${sel}`).toBeTruthy()
    )
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
  const s = await inStudio(
    fx,
    scope && `${STUDIO[scope]} [data-hole="${hole}"]`
  )
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
      expect(
        studio.holes.filter((h) => !panel.holes.includes(h)),
        `run page ${studio.holes} vs panel ${panel.holes}`
      ).toEqual([])
    })

  for (const [hole, why] of Object.entries(SKIPPED))
    it.skip(`${hole}: ${why}`, () => {})
})

describe("the loop's cut words are shared", () => {
  it("result_cap and the unrun call read the same words the table and lib/events give", () => {
    expect(resultCapReason(4096)).toBe(
      `${CAUSES.truncated!.result_cap.reason} (4.0 KiB cut)`
    )
    expect(UNRUN_CALL_REASON).toMatch(
      /not executed: the response hit the output token limit/
    )
  })
})

describe("a pre-A1 run's Request tab (D5's second Done clause)", () => {
  it("the run page's request section says not_recorded", async () => {
    const s = await inStudio(
      CASES.not_recorded.fixture(),
      `[data-request] [data-hole="not_recorded"]`
    )
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
    await vi.waitFor(() =>
      expect(tab.querySelector('[data-hole="not_recorded"]')).toBeTruthy()
    )
    expect(
      tab.querySelector('[data-hole="not_recorded"]')!.getAttribute("title")
    ).toContain(HOLES.not_recorded.fix!)
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
    nodes.map((n) => ({
      hole: n.getAttribute("data-hole") ?? "",
      label: badgeLabel(n),
      title: n.getAttribute("title") ?? "",
    }))
  const key = (x: Seen) => `${x.hole} | ${x.label} | ${x.title}`
  const row0 = () =>
    structuredClone(
      golden<{ requests: Record<string, unknown>[] }>("requests-ok").requests[0]
    ) as Record<string, any>

  async function panes(
    fx: Fixture
  ): Promise<{ panel: Seen[]; studio: Seen[] }> {
    serve(fx, false)
    const el = await mount(ATTRS)
    await vi.waitFor(() => expect($(el, "#weft-tab-request")).toBeTruthy())
    click($(el, "#weft-tab-request"))
    await vi.waitFor(
      () => expect($(el, "#weft-tp-request [data-weft-rq-pane]")).toBeTruthy(),
      { timeout: 5_000 }
    )
    await settle()
    const panel = seen(
      all(el, "#weft-tp-request [data-weft-rq-pane] [data-hole]")
    )
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
    await waitFor(() =>
      expect(pane()?.querySelector("button[aria-expanded]")).toBeTruthy()
    )
    pane()!.querySelector<HTMLElement>("button[aria-expanded]")!.click()
    await new Promise((r) => setTimeout(r, 150))
    return {
      panel,
      studio: seen(Array.from(pane()!.querySelectorAll("[data-hole]"))),
    }
  }
  const expectBoth = (
    r: { panel: Seen[]; studio: Seen[] },
    want: Partial<Seen>[]
  ) => {
    for (const w of want)
      for (const [side, list] of Object.entries(r))
        expect(
          list.some((x) =>
            Object.entries(w).every(([k, v]) => x[k as "hole"].includes(v))
          ),
          `${side} lacks ${JSON.stringify(w)}: ${list.map(key).join("; ")}`
        ).toBe(true)
    // What the run page's pane says of the record, the panel's says.
    expect(
      r.studio.map(key).filter((k) => !r.panel.map(key).includes(k))
    ).toEqual([])
  }

  it("a derived prompt and catalog: the derived badge on both blocks of both panes, no prompt box", async () => {
    const r = row0()
    r.prompt = {
      hash: r.system_hash,
      text: "",
      content: "derived",
      truncated_bytes: 0,
    }
    r.tools = {
      hash: r.catalog_hash,
      tools: [],
      content: "derived",
      truncated_bytes: 0,
    }
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
    const fx: Fixture = {
      doc: { status: "running", finished: null },
      events: events({ finish: false }),
      rows: [r],
    }
    const got = await panes(fx)
    expect(
      got.panel.filter((x) => x.hole === "gap"),
      "panel"
    ).toEqual([])
    expect(
      got.studio.filter((x) => x.hole === "gap"),
      "run page"
    ).toEqual([])
    expect(document.querySelector('[data-request="0"]')!.textContent).toContain(
      "bytes when the transcript is read"
    )
    serve(fx, false)
    const el = await mount(ATTRS)
    await vi.waitFor(() => expect($(el, "#weft-tab-request")).toBeTruthy())
    click($(el, "#weft-tab-request"))
    await vi.waitFor(
      () =>
        expect(
          $(el, "#weft-tp-request [data-weft-messages-line]")
        ).toBeTruthy(),
      { timeout: 5_000 }
    )
    await settle()
    expect(
      $(el, "#weft-tp-request [data-weft-rq-pane]")!.textContent
    ).toContain("bytes when the transcript is read")
    expect(all(el, '#weft-tp-request [data-hole="gap"]')).toEqual([])
    el.remove()
  })

  it("a stripped HoleRef carries the tools route's reason and fix on both", async () => {
    const r = row0()
    r.content = "stripped"
    r.prompt = { hash: r.system_hash, badge: "stripped" }
    r.tools = { hash: r.catalog_hash, badge: "stripped" }
    const tools = {
      catalogs: [],
      badge: "stripped",
      reason: "the destination stripped it (otel.NoContent)",
      fix: "drop otel.NoContent()",
    }
    expectBoth(await panes({ doc: {}, events: events(), rows: [r], tools }), [
      {
        hole: "stripped",
        title:
          "the destination stripped it (otel.NoContent) — fix: drop otel.NoContent()",
      },
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
  const blank = (): Drawn => ({
    markers: [],
    cells: {},
    holes: {},
    chips: {},
    cellHoles: {},
    top: [],
  })
  const attr = (ns: Iterable<Element>, a: string) =>
    [...ns].map((n) => n.getAttribute(a)!)

  function panelDrawn(doc: DiffDoc): Drawn {
    const box = stepDiffBlock(doc, "http://studio.test/studio/")
    const out = blank()
    out.top = attr(
      box.querySelectorAll(":scope > .weft-note [data-hole]"),
      "data-hole"
    )
    for (const m of box.querySelectorAll("[data-weft-diff-marker]"))
      out.markers.push({
        step: m.getAttribute("data-weft-diff-marker")!,
        text: m.textContent,
      })
    for (const tr of box.querySelectorAll("tbody tr")) {
      const n = tr.getAttribute("data-weft-diff-step")!
      out.cells[n] = {}
      for (const td of tr.querySelectorAll("[data-weft-diff-cell]"))
        out.cells[n][td.getAttribute("data-weft-diff-cell")!] =
          td.getAttribute("data-state")!
      out.cellHoles[n] = attr(
        tr.querySelectorAll("[data-weft-diff-cell] [data-hole]"),
        "data-hole"
      )
      for (const side of ["a", "b"]) {
        out.holes[`${n}${side}`] = attr(
          tr.querySelectorAll(`[data-weft-diff-side="${side}"] [data-hole]`),
          "data-hole"
        )
        out.chips[`${n}${side}`] = attr(
          tr.querySelectorAll(
            `[data-weft-diff-side="${side}"] [data-weft-diff-mark]`
          ),
          "data-weft-diff-mark"
        )
      }
    }
    return out
  }

  async function studioDrawn(doc: DiffDoc): Promise<Drawn> {
    const { container } = await renderWithRouter(
      createElement(StepDiffTable, { view: nWayView([doc]) })
    )
    await waitFor(() =>
      expect(container.querySelector("[data-step-diff]")).toBeTruthy()
    )
    const out = blank()
    out.top = attr(
      container.querySelectorAll("[data-diff-holes] [data-hole]"),
      "data-hole"
    )
    for (const m of container.querySelectorAll("[data-diff-marker]"))
      out.markers.push({
        step: m.getAttribute("data-diff-marker")!,
        text: m.querySelector("a")!.textContent,
      })
    for (const tr of container.querySelectorAll("tbody tr")) {
      const n = tr.getAttribute("data-diff-step")!
      out.cells[n] = {}
      const mine = `[data-diff-run="${doc.b.run_id}"][data-diff-state]`
      for (const td of tr.querySelectorAll(mine))
        out.cells[n][td.getAttribute("data-diff-cell")!] =
          td.getAttribute("data-diff-state")!
      out.cellHoles[n] = attr(
        tr.querySelectorAll(`${mine} [data-hole]`),
        "data-hole"
      )
      for (const [side, run] of [
        ["a", doc.a.run_id],
        ["b", doc.b.run_id],
      ]) {
        out.holes[`${n}${side}`] = attr(
          tr.querySelectorAll(
            `[data-diff-run="${run}"][data-diff-marks] [data-hole]`
          ),
          "data-hole"
        )
        out.chips[`${n}${side}`] = attr(
          tr.querySelectorAll(
            `[data-diff-run="${run}"][data-diff-marks] [data-diff-mark]`
          ),
          "data-diff-mark"
        )
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
      out.cellHoles[n] = DIFF_COLUMNS.flatMap(
        (c) => cellHole(c, r.a.side, r.b.side) ?? []
      )
      for (const [side, sv] of [
        ["a", r.a],
        ["b", r.b],
      ] as const) {
        out.holes[`${n}${side}`] = [
          ...sv.marks.flatMap((m) => m.hole ?? []),
          ...sv.holes.map((h) => h.hole),
        ]
        out.chips[`${n}${side}`] = sv.marks
          .filter((m) => !m.hole)
          .map((m) => m.mark)
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
    s1.b = {
      ...s1.b!,
      marks: ["max_tokens"],
      holes: [
        {
          hole: "max_tokens",
          reason: "the step finished on the output token limit",
        },
      ],
    }
    doc.holes = [
      {
        hole: "truncated",
        reason: "this response reads a bounded number of steps",
        fix: "open the later steps",
      },
    ]
    return doc
  }

  const cases: [string, () => DiffDoc][] = [
    ["diff", () => golden<DiffDoc>("diff")],
    ["diff-hidden", () => golden<DiffDoc>("diff-hidden")],
    ["diff-not-recorded", () => golden<DiffDoc>("diff-not-recorded")],
    ["diff with marks and a truncated response", marked],
  ]
  it.each(cases)(
    "%s: the same rows, cells, markers, badges and chips on both surfaces",
    async (_, make) => {
      const doc = make()
      expect(panelDrawn(doc), "panel").toEqual(want(doc))
      expect(await studioDrawn(doc), "studio").toEqual(want(doc))
    }
  )

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
      expect(got.markers).toEqual([
        { step: "3", text: "changed at step 3 · tool results" },
      ])
      for (const [n, row] of Object.entries(got.cells))
        expect(Object.values(row).filter((s) => s !== "same")).toEqual(
          n === "3" ? ["changed"] : []
        )
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
    {
      kind: "tool_result",
      step: 1,
      callID: "c2",
      toolResult: "policy: no refunds",
    },
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
    const agent = {
      name: "acme-support",
      models: [],
      tools: [{ name: "lookup_order", side_effects: "never", allow: false }],
    }
    const studio = studioBody({
      runtime: "rt_1",
      agent,
      variant,
      sourceRunID: RUN,
      fromStep: 3,
      input: "",
      edits,
    })
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
    expect(JSON.stringify(panel.transcript_edits)).toBe(
      JSON.stringify(studio.transcript_edits)
    )
    expect(JSON.stringify(panel)).toBe(JSON.stringify(studio))
    expect(
      (studio.transcript_edits as { kind: string }[]).map((e) => e.kind)
    ).toEqual(["user", "tool_args", "tool_result", "reply", "insert"])
  })

  type Rows = {
    system: string
    changed: string[]
    holes: string[]
    rows: string[][]
    warnings: string[]
    unchecked: string
  }
  const read = (root: ParentNode, p: string): Rows => ({
    system: root
      .querySelector(`[data-${p}preview-system]`)!
      .getAttribute(`data-${p}preview-system`)!,
    changed: [...root.querySelectorAll(`[data-${p}preview-changed]`)].map((n) =>
      n.getAttribute(`data-${p}preview-changed`)!
    ),
    holes: [...root.querySelectorAll("[data-hole]")].map((n) =>
      n.getAttribute("data-hole")!
    ),
    rows: [...root.querySelectorAll(`[data-${p}preview-op]`)].map((r) => [
      r.getAttribute(`data-${p}preview-op`)!,
      r.querySelector(`[data-${p}preview-was]`)?.textContent ?? "",
      r.querySelector(`[data-${p}preview-will]`)?.textContent ?? "",
    ]),
    warnings: [...root.querySelectorAll(`[data-${p}preview-warning]`)].map(
      (n) => `${n.getAttribute(`data-${p}preview-warning`)}: ${n.textContent}`
    ),
    unchecked:
      root.querySelector(`[data-${p}preview-unchecked]`)?.textContent ?? "",
  })
  const PV = golden<PreviewDoc>("playground-preview")
  const hidden: PreviewDoc = {
    ...PV,
    will_send: {
      ...PV.will_send,
      system: null,
      system_badge: "hidden",
      tools: null,
      tools_badge: "hidden",
    },
    was_sent: {
      ...PV.was_sent,
      system: null,
      system_badge: "hidden",
      tools: null,
      tools_badge: "hidden",
      badge: "derived",
    },
    diff: { ...PV.diff, system: "hidden", tools: null },
    unchecked: ["model"],
  }

  for (const [name, doc] of [
    ["the golden", PV],
    ["hidden to a read token, unchecked", hidden],
  ] as const)
    it(`both surfaces draw the same preview rows: ${name}`, async () => {
      const panel = read(previewBlock({ doc }, 2), "weft-")
      const { container } = await renderWithRouter(
        createElement(PreviewPane, { state: { doc }, fromStep: 2 })
      )
      await waitFor(() =>
        expect(container.querySelector("[data-preview-system]")).toBeTruthy()
      )
      const studio = read(container, "")
      cleanup()
      expect(panel).toEqual(studio)
      const v = previewView(doc)
      expect(studio.rows).toEqual(v.rows!.map((r) => [r.op, r.was, r.will]))
      expect(studio.holes).toEqual(v.holes.map((h) => h.hole))
    })
})

describe("the hand-off to Studio's playground is one fragment (final web review)", () => {
  // The same draft on both surfaces: the panel's "compare in Studio"
  // and the run page drawer's "open in the playground" write the same
  // keys, in the same order, by the same omission rule.
  const tools = ["lookup_order", "search_kb", "refund"]
  const cases: {
    name: string
    step: number
    v: Omit<VariantFields, "toolsOff"> & { off: string[] }
    edits: ReplayEdit[]
  }[] = [
    {
      name: "a later step with edits, a lab and every override (the input stays behind)",
      step: 2,
      v: {
        off: ["refund"],
        instructions: "be terse",
        model: "glm",
        thinking: "high",
        input: "stale",
        engine: "live",
        sideEffects: "park",
        thread: "ephemeral",
        lab: { ...emptyLab(), max_steps: "3", park_on: ["refund"] },
      },
      edits: [
        { kind: "tool_result", step: 1, callID: "c2", toolResult: "none" },
      ],
    },
    {
      name: "step 0, a fork with its input, scripted",
      step: 0,
      v: {
        off: [],
        instructions: "you help",
        model: "",
        thinking: "",
        input: "and order 9?",
        engine: "scripted",
        sideEffects: "substitute",
        thread: "fork",
      },
      edits: [],
    },
    {
      name: "every default: the run and its target only",
      step: 0,
      v: {
        off: [],
        instructions: "you help",
        model: "",
        thinking: "",
        input: "",
        engine: "live",
        sideEffects: "substitute",
        thread: "ephemeral",
      },
      edits: [],
    },
  ]
  for (const c of cases)
    it(c.name, () => {
      const { off, ...v } = c.v
      const studio = playgroundLink(
        drawerHandoff({
          runID: RUN,
          fromStep: c.step,
          agent: "acme-support",
          runtime: "rt_1",
          tools,
          registered: "you help",
          variant: { ...v, toolsOff: new Set(off) },
          edits: c.edits,
        })
      ).hash
      const draft: ExperimentDraft = {
        runId: RUN,
        agent: "acme-support",
        runtimeId: "rt_1",
        step: c.step,
        instructions: v.instructions,
        registeredInstructions: "you help",
        tools: Object.fromEntries(tools.map((t) => [t, !off.includes(t)])),
        model: v.model,
        thinking: v.thinking,
        input: v.input,
        engine: v.engine,
        sideEffects: v.sideEffects,
        thread: v.thread,
        edits: c.edits,
        ...(v.lab ? { lab: v.lab } : {}),
      }
      const panel = new URL(
        studioPlaygroundLink("http://studio.test/studio/", draft, c.step)
      ).hash.slice(1)
      expect(panel).toBe(studio)
      expect(panel).toContain(`run=${RUN}`)
    })
})

// A6.2: the wire pair. The panel draws no body — its Request tab links
// to the run page's Wire tab exactly where that tab draws an exchange,
// with the deep link that opens it; where the row carries none, no link,
// and the run page says the row's not_recorded with the raw_off words.
describe("the wire pair: the panel links where the run page draws it (A6.2)", () => {
  const row0 = () =>
    structuredClone(
      golden<{ requests: Record<string, unknown>[] }>("requests-ok").requests[0]
    ) as Record<string, any>
  const RAW = {
    step: 0,
    attempt: 1,
    request: {
      method: "POST",
      url: "https://api.example/v1/messages",
      headers: {},
      media_type: "application/json",
      body: "{}",
      bytes: 2,
    },
    response: {
      status: 200,
      headers: {},
      media_type: "text/event-stream",
      body: "data: ok\n\n",
      bytes: 10,
    },
    redacted_headers: ["x-api-key"],
  }
  async function wirePair(
    r: Record<string, any>
  ): Promise<{ link: string | null; exchange: boolean; hole: Element | null }> {
    const fx: Fixture = { doc: {}, events: events(), rows: [r] }
    serve(fx, false)
    const el = await mount(ATTRS)
    await vi.waitFor(() => expect($(el, "#weft-tab-request")).toBeTruthy())
    click($(el, "#weft-tab-request"))
    await vi.waitFor(
      () => expect($(el, "#weft-tp-request [data-weft-rq-pane]")).toBeTruthy(),
      { timeout: 5_000 }
    )
    await settle()
    const link =
      $(el, "#weft-tp-request [data-weft-wire-link]")?.getAttribute("href") ??
      null
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
    renderApp(`/runs/${RUN}?step=0&view=story&pane=wire`)
    const wire = () =>
      document.querySelector<HTMLElement>('[data-step="0"] [data-wire]')
    await waitFor(() =>
      expect(wire()?.querySelector("[data-wire-attempt]")).toBeTruthy()
    )
    return {
      link,
      exchange: Boolean(wire()!.querySelector("[data-wire-side]")),
      hole: wire()!.querySelector("[data-hole]"),
    }
  }

  it("a row with raw: the panel's link is the run page's own pane=wire URL, and that page draws the exchange", async () => {
    const r = row0()
    r.raw = RAW
    delete r.raw_badge
    delete r.raw_reason
    delete r.raw_fix
    const got = await wirePair(r)
    expect(got.link).toBe(
      `${ATTRS["data-endpoint"]}runs/${RUN}?step=0&view=story&pane=wire`
    )
    expect(got.exchange).toBe(true)
    expect(got.hole).toBeNull()
  })

  it("a row with raw null: no link in the panel; the run page's Wire tab says not_recorded in the raw_off words", async () => {
    const got = await wirePair(row0())
    expect(got.link).toBeNull()
    expect(got.exchange).toBe(false)
    expect(got.hole?.getAttribute("data-hole")).toBe("not_recorded")
    expect(got.hole?.getAttribute("title")).toBe(
      `${CAUSES.not_recorded!.raw_off.reason} — fix: ${CAUSES.not_recorded!.raw_off.fix}`
    )
  })
})

describe("the saves say the same words on both surfaces (F6.2)", () => {
  const PLAY = `weft_pt.${btoa(JSON.stringify({ pid: "pub_orders", scope: "playground" })).replace(/=+$/, "")}.sig`
  const CAPS = ["export", "proposals"]
  const studioToasts = () =>
    [...document.querySelectorAll<HTMLElement>("[data-sonner-toast]")].map(
      (t) => t.querySelector("[data-title]")?.textContent ?? ""
    )

  it("save as test's label and title; keep as prompt's copy-only line under a panel token", async () => {
    vi.stubGlobal("navigator", {
      clipboard: { writeText: () => Promise.resolve() },
    })
    // The panel: the turn's link, then the result pane's keep as prompt.
    panelStudio(baseRoutes(), {
      ...META,
      capabilities: [...META.capabilities, ...CAPS],
    })
    const el = await mount({ ...ATTRS, "data-token": PLAY })
    const link = $(el, "a[data-weft-save-test]")
    expect(link?.textContent).toBe(SAVE_TEST_LABEL)
    expect(link?.getAttribute("title")).toBe(SAVE_TEST_TITLE)
    await runExperiment(el)
    // Its title is the playground card's (lib/saves.ts's keepEditedTitle).
    expect(button(el, "keep as prompt ⤴")?.getAttribute("title")).toBe(
      keepEditedTitle("acme-support", [...META.capabilities, ...CAPS], PLAY)
    )
    expect(button(el, "keep as prompt ⤴")?.getAttribute("title")).toBe(
      "copy the edited prompt — a proposal needs the dev token"
    )
    click(button(el, "keep as prompt ⤴"))
    await settle(20)
    const keepLine = keepPromptWords({
      kind: "keep-prompt",
      agent: "acme-support",
      copied: true,
      proposal: "copy-only",
    }).title
    expect($(el, ".weft-api-note")?.textContent).toBe(keepLine)
    el.remove()
    await settle()
    vi.unstubAllGlobals()
    // Studio: the run header's button, then the Request pane's keep as prompt.
    cleanup()
    document.body.innerHTML = ""
    stubBrowser()
    StudioEventSource.reset()
    vi.stubGlobal("EventSource", StudioEventSource)
    Object.defineProperty(window.navigator, "clipboard", {
      configurable: true,
      value: { writeText: () => Promise.resolve() },
    })
    resetNotices()
    setStudioToken(PLAY)
    const evs: unknown[] = [
      {
        type: "run_start",
        id: RUN,
        model: { provider: "wefttest", name: "script" },
      },
    ]
    for (let i = 0; i < 3; i++)
      evs.push(
        { type: "step_start", run_id: RUN, index: i },
        {
          type: "step_finish",
          run_id: RUN,
          index: i,
          reason: "stop",
          usage: { input_tokens: 1, output_tokens: 1 },
        }
      )
    evs.push({
      type: "run_finish",
      run_id: RUN,
      usage: { input_tokens: 3, output_tokens: 3 },
      steps: 3,
    })
    new FakeStudio()
      .on("GET meta", {
        ...golden<Record<string, unknown>>("meta"),
        capabilities: ["requests", ...CAPS],
      })
      .on(`GET runs/${RUN}`, { ...runRow({ id: RUN }), steps: 3, children: [] })
      .on(
        `GET runs/${RUN}/events`,
        pagedEvents(evs.map((event, pos) => ({ pos, time: T0, event })))
      )
      .on(`GET runs/${RUN}/spans`, { spans: [] })
      .on(`GET runs/${RUN}/transcript`, { batches: [] })
      .withRequests(RUN, "ok")
      .install()
    renderApp(`/runs/${RUN}?view=story`)
    const btn = await waitFor(() => {
      const b = [...document.querySelectorAll("button")].find(
        (x) => x.textContent === SAVE_TEST_LABEL
      )
      expect(b).toBeTruthy()
      return b!
    })
    expect(btn.getAttribute("title")).toBe(SAVE_TEST_TITLE)
    await waitFor(() =>
      expect(
        document.querySelector(
          '[data-step="0"] [data-request="0"] button[aria-expanded]'
        )
      ).toBeTruthy()
    )
    ;(
      document.querySelector(
        '[data-step="0"] [data-request="0"] button[aria-expanded]'
      ) as HTMLElement
    ).click()
    const keep = await waitFor(() => {
      const b = document.querySelector<HTMLElement>("[data-keep-prompt]")
      expect(b).toBeTruthy()
      return b!
    })
    keep.click()
    // Both surfaces say the same copy-only line for the run's agent
    // (the dev token named, never the agent).
    await waitFor(() => expect(studioToasts()).toContain(keepLine))
    expect(keepLine).toBe(`copied · ${PROPOSAL_NEEDS_DEV}`)
    setStudioToken("")
  })
})

// I1.3: a run derived from stock OTel GenAI spans reads the same on
// both surfaces — the same holes (derived alone, in the stock_spans
// words), on the run header / turn notes, the row chip / turn chip and
// the request section / Request tab; neither offers a replay verb or a
// save-as-test link; both draw its spans. One fake (the server's own
// bodies, src/test/derived-fixture.ts) serves the panel, then the page.
describe("a derived run on both surfaces (I1.3)", () => {
  const stock = (s: string | null | undefined) => {
    expect(s).toContain(DERIVED_STOCK.reason)
    expect(s).toContain(DERIVED_STOCK.fix)
  }
  const caps = [
    ...golden<{ capabilities: string[] }>("meta").capabilities,
    "playground",
  ]
  const fake = (studioMeta: boolean) =>
    serveDerived(
      new FakeStudio().on(
        "GET meta",
        studioMeta
          ? { ...golden<Record<string, unknown>>("meta"), capabilities: caps }
          : { ...META, capabilities: caps }
      )
    ).install()

  it("the same holes, chips and refusals, the same words", async () => {
    // The panel: dev mode (a derived run has no public id), the turn opened.
    fake(false)
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-open": "true",
    })
    await vi.waitFor(() => expect(all(el, ".weft-turn").length).toBe(2), {
      timeout: 5_000,
    })
    click(all(el, ".weft-turn").find((b) => b.textContent.includes(DERIVED)))
    await vi.waitFor(
      () =>
        expect(
          $(el, '[data-weft-turn-holes] [data-hole="derived"]')
        ).toBeTruthy(),
      { timeout: 5_000 }
    )
    click($(el, "#weft-tab-request"))
    await vi.waitFor(
      () =>
        expect($(el, '#weft-tp-request [data-hole="derived"]')).toBeTruthy(),
      { timeout: 5_000 }
    )
    const panel = {
      holes: holeSet(all(el, "[data-hole]")),
      header: $(el, '[data-weft-turn-holes] [data-hole="derived"]')!
        .parentElement!.textContent,
      chip: $(el, `.weft-turn [data-weft-chip="derived"]`)!.getAttribute(
        "title"
      ),
      request: $(el, "#weft-tp-request")!.textContent,
      verbs: all(el, "[data-weft-verb]").length,
      saveLink: all(el, "a[data-weft-save-test]").length,
      refused: all(el, '[data-weft-refused="derived"]').length,
      handoff: $(el, "[data-weft-derived-handoff]")!.textContent,
      timeline: [] as (string | null)[],
    }
    click($(el, "#weft-tab-timeline"))
    await vi.waitFor(
      () =>
        expect($(el, "#weft-tp-timeline .weft-wf .weft-wf-name")).toBeTruthy(),
      { timeout: 5_000 }
    )
    panel.timeline = all(el, "#weft-tp-timeline .weft-wf .weft-wf-name").map(
      (n) => n.textContent
    )
    el.remove()
    await settle()
    cleanup()
    document.body.innerHTML = ""

    // The run page on the same bodies: its story, then its row.
    stubBrowser()
    vi.stubGlobal("scrollTo", vi.fn())
    StudioEventSource.reset()
    vi.stubGlobal("EventSource", StudioEventSource)
    setStudioToken("")
    fake(true)
    renderApp(`/runs/${DERIVED}?view=story`)
    await waitFor(() =>
      expect(
        document.querySelectorAll(
          "[data-step] [data-request] [data-hole='derived']"
        ).length
      ).toBe(2)
    )
    const studio = {
      holes: holeSet(Array.from(document.querySelectorAll("[data-hole]"))),
      header: document.querySelector("[data-run-holes]")!.textContent,
      request: document.querySelector("[data-request]")!.textContent,
      verbs: document.querySelectorAll("[data-replay-verb]").length,
      steps: document.querySelectorAll("[data-derived-story] [data-step]")
        .length,
      refused: document.querySelectorAll(
        "[data-replay-refused='derived'], [data-save-test='derived']"
      ).length,
    }
    renderApp("/runs")
    await waitFor(() =>
      expect(
        document.querySelector("[data-derived-chip] [data-hole='derived']")
      ).toBeTruthy()
    )
    const studioChip = document
      .querySelector("[data-derived-chip] [data-hole='derived']")!
      .getAttribute("title")
    renderApp(`/runs/${DERIVED}`)
    await waitFor(() =>
      expect(document.querySelectorAll("[data-span]").length).toBe(4)
    )
    const waterfall = [
      ...document.querySelectorAll("[data-span] > div[title]"),
    ].map((n) => n.getAttribute("title")!.split(" — ")[0])

    // The same rows: one hole, derived, on each surface.
    expect(panel.holes).toEqual(["derived"])
    expect(studio.holes).toEqual(panel.holes)
    for (const s of [
      panel.header,
      panel.chip,
      panel.request,
      studio.header,
      studioChip,
      studio.request,
    ])
      stock(s)
    expect(panel.chip).toBe(studioChip)
    // Neither offers what the server would refuse; both say why.
    expect(panel.verbs).toBe(0)
    expect(studio.verbs).toBe(0)
    expect(panel.saveLink).toBe(0)
    expect(panel.refused).toBe(3) // ✎ Experiment, ↻ Re-run, save as test
    expect(studio.refused).toBe(2) // replay, save as test
    // The steps: the panel hands off with the count the page draws cards for.
    expect(panel.handoff).toBe(`${studio.steps} steps · open in Studio ⤢`)
    expect(studio.steps).toBe(2)
    // The spans: the Timeline lists what the waterfall lists.
    expect(panel.timeline).toEqual([
      "invoke_agent support",
      "chat gpt-5",
      "execute_tool lookup_order",
      "chat gpt-5",
    ])
    expect(waterfall).toEqual(panel.timeline)
  })

  it("no usage reported: both surfaces read the row's tokens as unknown, in the API's reason", async () => {
    const row = derivedBodies.runNoUsage as RunRow
    const reason = row.cost_reason!
    fake(false).on("GET runs", { total: 1, runs: [row], next_before: null })
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-open": "true",
    })
    await vi.waitFor(
      () =>
        expect($(el, '.weft-turn [data-weft-usage="unknown"]')).toBeTruthy(),
      { timeout: 5_000 }
    )
    const p = $(el, '.weft-turn [data-weft-usage="unknown"]')!
    // The header's total leaves the unknown turn out (Studio's session
    // and list totals read the row the same way: "—", the API's reason).
    const stats = $(el, "[data-weft-stats]")!
    expect(stats.textContent).toBe("1 turns · — tok")
    expect(stats.getAttribute("title")).toContain(reason)
    el.remove()
    await settle()
    cleanup()
    document.body.innerHTML = ""
    stubBrowser()
    vi.stubGlobal("scrollTo", vi.fn())
    StudioEventSource.reset()
    vi.stubGlobal("EventSource", StudioEventSource)
    setStudioToken("")
    fake(true).on("GET runs", { total: 1, runs: [row], next_before: null })
    renderApp("/runs")
    await waitFor(() =>
      expect(
        document.querySelector("tbody tr [data-usage='unknown']")
      ).toBeTruthy()
    )
    const s = document.querySelector("tbody tr [data-usage='unknown']")!
    expect(p.textContent).toBe("—")
    expect(s.firstChild?.textContent).toBe("—")
    expect(p.getAttribute("title")).toBe(reason)
    expect(s.getAttribute("title")).toBe(reason)
  })
})

// The app's own turns (plan F4.3): the same gate, the same verbs and
// the same words on both surfaces. One fake (src/test/appturn-fixture.ts,
// studio/appturn.go's shapes) per case serves the panel, then the run
// page: what kind of block each draws (decide, read-only, resumed,
// plain), its line, its refused verbs, and — after continue — the
// verb's line. The gate is a Sessions runtime of the run's own service
// (GET /api/runtimes), never meta's global debug_scope: breakpoints_own
// alone, or another service's Sessions runtime, reads read-only.
describe("an app turn's verbs and words on both surfaces (F4.3)", () => {
  const cases: {
    name: string
    o: AppTurnOptions
    run?: string
    click?: boolean
    regated?: boolean
    kind: string
  }[] = [
    {
      name: "a Sessions runtime of the service: continue decides, the parked turn reads resumed",
      o: {},
      click: true,
      kind: "resumed",
    },
    {
      name: "the runtime's rejection verbatim",
      o: { outcome: "rejected" },
      click: true,
      kind: "decide",
    },
    {
      name: "a 403 becomes the read-only line with the server's fix",
      o: { outcome: "refused" },
      click: true,
      kind: "read-only",
    },
    // The panel's rule on both: a 403 is live until a runtimes read
    // started after it has applied and says decide — then the verbs are back.
    {
      name: "a 403 a later runtimes read re-gates to decide gives the verbs back",
      o: { outcome: "refused", reconnects: true },
      click: true,
      regated: true,
      kind: "decide",
    },
    {
      name: "connected without Sessions: read-only",
      o: {
        runtimes: [appRuntime({ sessions: false, breakpoints_own: false })],
      },
      kind: "read-only",
    },
    {
      name: "breakpoints_own alone (debug_scope own+runtime): read-only",
      o: {
        runtimes: [appRuntime({ sessions: false, breakpoints_own: true })],
        debugScope: "own+runtime",
      },
      kind: "read-only",
    },
    {
      name: "another service's Sessions runtime: read-only for this service's turn",
      o: {
        runtimes: [appRuntime({ service: "billing" })],
        debugScope: "own+runtime",
      },
      kind: "read-only",
    },
    {
      name: "no runtime of the service connected: the link's fix",
      o: { runtimes: [] },
      kind: "read-only",
    },
    {
      name: "a turn already resumed: decided — resumed as the next run",
      o: { resumed: true },
      kind: "resumed",
    },
    { name: "a plain Generate's park", o: {}, run: APP_PLAIN, kind: "plain" },
    {
      name: "a session turn without a public id: appturn.go's words, the thread.PublicID fix",
      o: {},
      run: APP_NOPUB,
      kind: "no-public-id",
    },
    {
      name: "a failed runtimes read (500): its error, no verdict, no verbs",
      o: { runtimesError: true },
      kind: "unread",
    },
  ]
  interface Seen {
    kind: string | null
    line: string
    refused: number
    after: string
  }
  async function panelSeen(c: (typeof cases)[number]): Promise<Seen> {
    const fake = serveAppTurn(new FakeStudio(), c.o).install()
    // A run outside the public id's conversation: the panel's fallback list.
    const plain = c.run === APP_PLAIN || c.run === APP_NOPUB
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-open": "true",
      ...(plain ? {} : { "data-public-id": APP_PUB }),
    })
    const id = c.run ?? APP_PARKED
    await vi.waitFor(
      () =>
        expect(
          all(el, ".weft-turn").some((b) => b.textContent.includes(id))
        ).toBe(true),
      { timeout: 5_000 }
    )
    click(all(el, ".weft-turn").find((b) => b.textContent.includes(id)))
    const callSel = `.weft-call[data-key="${APP_CALL.id}"]`
    await vi.waitFor(() => expect($(el, callSel)).toBeTruthy(), {
      timeout: 5_000,
    })
    await vi.waitFor(
      () =>
        expect($(el, ".weft-main")?.textContent ?? "").not.toContain(
          "checking for a runtime"
        ),
      { timeout: 5_000 }
    )
    await settle()
    if (c.click && c.regated) {
      const reads = fake.calls("GET runtimes").length
      click(button(el, "continue"))
      await vi.waitFor(
        () =>
          expect(fake.calls(`POST runs/${APP_PARKED}/approvals`)).toHaveLength(
            1
          ),
        { timeout: 5_000 }
      )
      await vi.waitFor(
        () => expect(fake.calls("GET runtimes").length).toBeGreaterThan(reads),
        { timeout: 5_000 }
      )
      await vi.waitFor(
        () =>
          expect(
            (button(el, "continue") as HTMLButtonElement | undefined)?.disabled
          ).toBe(false),
        { timeout: 5_000 }
      )
      await settle()
    } else if (c.click) {
      click(button(el, "continue"))
      const done = () =>
        $(el, `${callSel} [data-weft-app-verb="rejected"]`) ??
        $(el, '[data-weft-app-owned="read-only"]') ??
        $(el, '[data-weft-app-owned="resumed"]')
      await vi.waitFor(() => expect(done()).toBeTruthy(), { timeout: 5_000 })
      await settle()
    }
    const owned = $(el, "[data-weft-app-owned]")
    const seen = {
      kind:
        owned?.getAttribute("data-weft-app-owned") ??
        (button(el, "continue") ? "decide" : null),
      line: owned?.textContent ?? "",
      refused: all(el, '[data-weft-refused="app-owned"]').length,
      after: $(el, `${callSel} [data-weft-app-verb]`)?.textContent ?? "",
    }
    el.remove()
    await settle()
    cleanup()
    document.body.innerHTML = ""
    return seen
  }
  async function studioSeen(c: (typeof cases)[number]): Promise<Seen> {
    stubBrowser()
    vi.stubGlobal("scrollTo", vi.fn())
    StudioEventSource.reset()
    vi.stubGlobal("EventSource", StudioEventSource)
    setStudioToken("")
    const fake = serveAppTurn(new FakeStudio(), {
      studioMeta: true,
      ...c.o,
    }).install()
    renderApp(`/runs/${c.run ?? APP_PARKED}?view=story`)
    const box = () => document.querySelector<HTMLElement>("[data-parked]")
    const first = c.click ? "decide" : c.kind
    await waitFor(() => expect(box()?.getAttribute("data-parked")).toBe(first))
    if (c.click && c.regated) {
      const reads = fake.calls("GET runtimes").length
      ;[...box()!.querySelectorAll("button")]
        .find((n) => n.textContent === "continue")!
        .click()
      await waitFor(() =>
        expect(fake.calls(`POST runs/${APP_PARKED}/approvals`)).toHaveLength(1)
      )
      await waitFor(() =>
        expect(fake.calls("GET runtimes").length).toBeGreaterThan(reads)
      )
      await waitFor(() =>
        expect(
          [...box()!.querySelectorAll("button")].find(
            (n) => n.textContent === "continue"
          )?.disabled
        ).toBe(false)
      )
    } else if (c.click) {
      ;[...box()!.querySelectorAll("button")]
        .find((n) => n.textContent === "continue")!
        .click()
      await waitFor(() =>
        expect(
          box()!.querySelector('[data-app-verb="rejected"]') ??
            box()!.querySelector('[data-app-owned="read-only"]') ??
            box()!.querySelector('[data-app-owned="resumed"]')
        ).toBeTruthy()
      )
    }
    const owned = box()!.querySelector("[data-app-owned]")
    const seen = {
      kind:
        owned?.getAttribute("data-app-owned") ??
        ([...box()!.querySelectorAll("button")].some(
          (n) => n.textContent === "continue"
        )
          ? "decide"
          : null),
      line: owned?.textContent ?? "",
      refused: box()!.querySelectorAll('[data-refused="app-owned"]').length,
      after: box()!.querySelector("[data-app-verb]")?.textContent ?? "",
    }
    cleanup()
    return seen
  }
  for (const c of cases)
    it(c.name, async () => {
      const panel = await panelSeen(c)
      const studio = await studioSeen(c)
      expect(studio).toEqual(panel)
      expect(panel.kind).toBe(c.kind)
      // A failed read is said as such: never the closed-link verdict.
      if (c.kind === "unread")
        expect([panel.line, panel.refused]).toEqual([
          `${RUNTIMES_UNREAD}: ${APP_RUNTIMES_500}`,
          0,
        ])
      if (c.kind === "no-public-id")
        expect(panel.line).toContain(
          `run ${APP_NOPUB} is not a turn of a thread session with a public id`
        )
    })

  // A finished steer reads "steered" on both (lib/honesty.ts's
  // appVerbWords: the panel's steer carries no decision).
  it("a finished steer reads steered on both surfaces, never decided", async () => {
    serveAppTurn(new FakeStudio(), { running: true }).install()
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-open": "true",
      "data-public-id": APP_PUB,
    })
    await vi.waitFor(
      () =>
        expect(
          all(el, ".weft-turn").some((b) => b.textContent.includes(APP_PARKED))
        ).toBe(true),
      { timeout: 5_000 }
    )
    click(all(el, ".weft-turn").find((b) => b.textContent.includes(APP_PARKED)))
    await vi.waitFor(
      () => expect($(el, "[data-weft-app-steer] input")).toBeTruthy(),
      { timeout: 5_000 }
    )
    ;($(el, "[data-weft-app-steer] input") as HTMLInputElement).value =
      "use staging instead"
    click(button(el, "steer"))
    await vi.waitFor(
      () => expect($(el, '[data-weft-app-verb="finished"]')).toBeTruthy(),
      { timeout: 5_000 }
    )
    const panel = $(el, '[data-weft-app-verb="finished"]')!.textContent
    el.remove()
    await settle()
    cleanup()
    document.body.innerHTML = ""
    stubBrowser()
    vi.stubGlobal("scrollTo", vi.fn())
    StudioEventSource.reset()
    vi.stubGlobal("EventSource", StudioEventSource)
    setStudioToken("")
    serveAppTurn(new FakeStudio(), {
      studioMeta: true,
      running: true,
    }).install()
    renderApp(`/runs/${APP_PARKED}?view=story`)
    await waitFor(() =>
      expect(document.querySelector("[data-app-steer]")).toBeTruthy()
    )
    const form = document.querySelector<HTMLFormElement>("[data-app-steer]")!
    fireEvent.change(form.querySelector("input")!, {
      target: { value: "use staging instead" },
    })
    fireEvent.submit(form)
    await waitFor(() =>
      expect(form.querySelector('[data-app-verb="finished"]')).toBeTruthy()
    )
    const studio = form.querySelector('[data-app-verb="finished"]')!.textContent
    cleanup()
    expect([panel, studio]).toEqual(["steered", "steered"])
  })

  // A Studio without Playground(true): both say so in
  // meta.capabilities_off's words through lib/honesty.ts's refusalWords
  // (its AppGateFacts argument, the meta each surface read).
  it("a Studio without the playground: the same line on both", async () => {
    serveAppTurn(new FakeStudio(), { noPlayground: true }).install()
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-open": "true",
      "data-public-id": APP_PUB,
    })
    await vi.waitFor(
      () =>
        expect(
          all(el, ".weft-turn").some((b) => b.textContent.includes(APP_PARKED))
        ).toBe(true),
      { timeout: 5_000 }
    )
    click(all(el, ".weft-turn").find((b) => b.textContent.includes(APP_PARKED)))
    await vi.waitFor(
      () => expect($(el, "[data-weft-app-owned]")).toBeTruthy(),
      { timeout: 5_000 }
    )
    const panel = $(el, "[data-weft-app-owned]")!.textContent
    el.remove()
    await settle()
    cleanup()
    document.body.innerHTML = ""
    stubBrowser()
    vi.stubGlobal("scrollTo", vi.fn())
    StudioEventSource.reset()
    vi.stubGlobal("EventSource", StudioEventSource)
    setStudioToken("")
    serveAppTurn(new FakeStudio(), {
      studioMeta: true,
      noPlayground: true,
    }).install()
    renderApp(`/runs/${APP_PARKED}?view=story`)
    await waitFor(() =>
      expect(
        document.querySelector("[data-parked] [data-app-owned]")
      ).toBeTruthy()
    )
    const studio = document.querySelector(
      "[data-parked] [data-app-owned]"
    )!.textContent
    cleanup()
    expect(studio).toContain(PLAYGROUND_OFF)
    expect(panel).toBe(studio)
  })
})

// Cost (plan A5.3): the panel's turn row and the run page's header read
// the same figure and the same reason for the same row — lib/cost.ts's
// words on both — own and delegated, two numbers on each.
describe("a turn's cost on both surfaces (A5.3)", () => {
  const WHY = "no row of the default pricing table matches wefttest/script"
  const cases: { name: string; doc: Partial<RunDoc> }[] = [
    {
      name: "priced, own + delegated",
      doc: {
        cost_usd: 0.0029,
        cost_known: true,
        cache: "exact",
        delegated_cost_usd: 0.0011,
        delegated_cost_known: true,
      },
    },
    {
      name: "an unpriced model: — with its reason",
      doc: {
        cost_usd: null,
        cost_known: false,
        cost_badge: "unpriced",
        cost_reason: WHY,
        delegated_cost_usd: 0.0011,
        delegated_cost_known: true,
      },
    },
    {
      name: "cache not reported: the visible mark and the hint in the title",
      doc: {
        cost_usd: 0.5,
        cost_known: true,
        cache: "not_reported",
        delegated_cost_usd: 0.25,
        delegated_cost_known: true,
      },
    },
  ]
  const priced = (fx: Fixture, studioMeta: boolean) => {
    const meta = studioMeta ? golden<Record<string, unknown>>("meta") : META
    return serve(fx, studioMeta).on("GET meta", {
      ...meta,
      capabilities: ["ingest", "requests", "pricing"],
    })
  }
  const read = (n: Element | null | undefined) => ({
    text: n?.firstChild?.textContent,
    title: n?.getAttribute("title"),
  })

  for (const c of cases)
    it(`${c.name}: the panel's turn cost is the run page's header cost`, async () => {
      const fx: Fixture = { doc: c.doc, events: events() }
      priced(fx, false)
      const el = await mount(ATTRS)
      await vi.waitFor(
        () => expect($(el, ".weft-turn [data-weft-turn-cost]")).toBeTruthy(),
        { timeout: 5_000 }
      )
      const [pOwn, pDel] = all(
        el,
        ".weft-turn [data-weft-turn-cost] [data-weft-cost]"
      ).map(read)
      const pHint = $(el, ".weft-turn [data-weft-cache-hint]")
      const pCache = pHint && {
        text: pHint.textContent,
        title: pHint.getAttribute("title"),
      }
      el.remove()
      await settle()
      cleanup()
      document.body.innerHTML = ""
      stubBrowser()
      vi.stubGlobal("scrollTo", vi.fn())
      StudioEventSource.reset()
      vi.stubGlobal("EventSource", StudioEventSource)
      setStudioToken("")
      priced(fx, true)
      renderApp(`/runs/${RUN}`)
      await waitFor(() =>
        expect(
          document.querySelector(
            "[data-run-cost] [data-cost-part='own'] [data-cost]"
          )
        ).toBeTruthy()
      )
      const sOwn = read(
        document.querySelector(
          "[data-run-cost] [data-cost-part='own'] [data-cost]"
        )
      )
      const sDel = read(
        document.querySelector(
          "[data-run-cost] [data-cost-part='delegated'] [data-cost]"
        )
      )
      expect(pOwn.text).toBeTruthy()
      expect(pDel.text).toMatch(/^\$/)
      expect(pOwn).toEqual(sOwn)
      expect(pDel).toEqual(sDel)
      // Studio's visible "cache not reported" mark (CacheHint), the same words and title — or neither.
      const sHint = document.querySelector("[data-run-cost] [data-cache-hint]")
      expect(pCache).toEqual(
        sHint && { text: sHint.textContent, title: sHint.getAttribute("title") }
      )
      if (c.doc.cache === "not_reported")
        expect(pCache?.text).toBe("cache not reported")
      if (c.doc.cost_known === false)
        expect(pOwn).toEqual({ text: "—", title: WHY })
    })
})

// The result pane's figure (A5.3 fixes): the replayed run's whole bill,
// own + delegated folded, says so on both surfaces — the panel's result
// pane and the playground's result card read the same figure, the same
// title, the same wrapper words.
describe("a replayed run's cost on both surfaces (A5.3)", () => {
  const PG = "pg_x1"
  const row = () => ({
    ...runRow({
      id: PG,
      playground: true,
      session_id: "",
      forked_from: `${RUN}#0`,
    }),
    cost_usd: 0.004,
    cost_known: true,
    cache: "exact" as const,
    delegated_cost_usd: 0.001,
    delegated_cost_known: true,
  })
  const cmd = (state: string) => ({
    command_id: "cmd_1",
    state,
    run_id: state === "finished" ? PG : "",
    status: "succeeded",
    error: null,
    created: T0,
    updated: T0,
  })
  const caps = ["live", "playground", "runtimes", "pricing"]
  const read = (n: Element | null | undefined) => {
    const f = n?.querySelector("[data-weft-cost], [data-cost]")
    return {
      wrapper: n?.getAttribute("title"),
      text: f?.firstChild?.textContent,
      title: f?.getAttribute("title"),
    }
  }

  it("the panel's result pane is the playground's result card: figure, title, the whole-bill wrapper", async () => {
    const routes = baseRoutes()
    routes["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
    routes["playground/commands/cmd_1"] = cmd("finished")
    routes[`runs/${PG}`] = { ...row(), children: [] }
    routes[`runs/${PG}/events?after=0&limit=500`] = {
      events: [],
      next_after: null,
      done: true,
      gaps: [],
    }
    routes[`runs/${PG}/transcript`] = transcript(
      [user("where is my order #4411?")],
      [assistant("It is delayed.")]
    )
    panelStudio(routes, { ...META, capabilities: caps })
    const el = await mount(ATTRS)
    await runExperiment(el)
    await vi.waitFor(
      () => expect($(el, ".weft-xres [data-weft-result-cost]")).toBeTruthy(),
      { timeout: 5_000 }
    )
    const panel = read($(el, ".weft-xres [data-weft-result-cost]"))
    el.remove()
    await settle()
    cleanup()
    document.body.innerHTML = ""
    stubBrowser()
    vi.stubGlobal("scrollTo", vi.fn())
    StudioEventSource.reset()
    vi.stubGlobal("EventSource", StudioEventSource)
    setStudioToken("")
    new FakeStudio()
      .on("GET meta", {
        ...golden<Record<string, unknown>>("meta"),
        capabilities: caps,
      })
      .on("GET runtimes", RUNTIMES)
      .on("GET experiments", { experiments: [] })
      .on(`GET runs/${RUN}`, { ...runRow({ id: RUN }), children: [] })
      .on(
        `GET runs/${RUN}/transcript`,
        transcript(
          [user("where is my order #4411?")],
          [assistant("It shipped.")]
        )
      )
      .on("POST playground/runs", cmd("queued"))
      .on("GET playground/commands/cmd_1", cmd("finished"))
      .on(`GET runs/${PG}`, { ...row(), children: [] })
      .on(`GET runs/${PG}/events`, pagedEvents([]))
      .on(
        `GET runs/${PG}/transcript`,
        transcript(
          [user("where is my order #4411?")],
          [assistant("It is delayed.")]
        )
      )
      .install()
    renderApp(`/playground?run=${RUN}`)
    const go = await screen.findByRole("button", { name: "Run A" })
    await waitFor(() => expect(go).toHaveProperty("disabled", false))
    fireEvent.click(go)
    await waitFor(
      () => expect(document.querySelector("[data-result-cost]")).toBeTruthy(),
      { timeout: 10_000 }
    )
    const studio = read(document.querySelector("[data-result-cost]"))
    expect(panel.text).toBe("$0.0050")
    expect(panel).toEqual(studio)
    expect(panel.wrapper).toBe(
      "the run's whole bill: its own model calls + its subagents'"
    )
  })
})

// What ended a run (U3.2): the five seeded shapes show the same chip on
// both surfaces — the panel's turn chip and the run page's header chip,
// lib/stopped-by.ts's words — and the run page's story ends on the same
// words, never "ended without run_finish" when the span named a stop.
describe("stopped by on both surfaces (U3.2)", () => {
  const USAGE =
    "weft: run failed at step 2: weft: run exceeded its usage limit (input 300/250, output 150/250)"
  const shapes: {
    name: string
    doc: Partial<RunDoc>
    chip: string
    end: string
  }[] = [
    {
      name: "opt.stop-when",
      doc: {
        stopped_by: "has_tool_call:submit",
        stop_reason: "tool_calls",
        steps: 1,
      },
      chip: "stopped by the tool call submit",
      end: "■ stopped by the tool call submit at step 0",
    },
    {
      name: "opt.max-steps",
      doc: {
        status: "failed",
        stopped_by: "max_steps",
        stop_reason: "tool_calls",
        err: "weft: run failed at step 1: weft: run exceeded the maximum number of steps",
        steps: 1,
      },
      chip: "max steps reached",
      end: "■ max steps reached at step 0",
    },
    {
      name: "opt.usage-limit",
      doc: {
        status: "failed",
        stopped_by: "usage_limit",
        stop_reason: "tool_calls",
        err: USAGE,
        steps: 1,
      },
      chip: "usage limit reached (input 300/250, output 150/250)",
      end: "■ usage limit reached at step 0 (input 300/250, output 150/250)",
    },
    {
      name: "opt.detect-loops",
      doc: {
        status: "failed",
        stopped_by: "loop_detected",
        stop_reason: "tool_calls",
        err: "weft: run failed at step 1: weft: the model repeated the same tool calls too many times: 3 identical steps in a row",
        steps: 1,
      },
      chip: "loop detected (3 identical steps in a row)",
      end: "■ loop detected at step 0 (3 identical steps in a row)",
    },
    {
      name: "a cancelled run",
      doc: {
        status: "failed",
        stopped_by: "canceled",
        cancel_cause: "studio.cancel",
        stop_reason: "tool_calls",
        err: "weft: run failed at step 0: context canceled",
        steps: 1,
      },
      chip: "cancelled · studio.cancel",
      end: "■ cancelled · studio.cancel at step 0",
    },
  ]
  for (const c of shapes)
    it(`${c.name}: the panel's turn chip is the run page's header chip`, async () => {
      const fx: Fixture = {
        doc: c.doc,
        events: events({
          finish: c.doc.status !== "failed",
          reason: c.doc.stop_reason,
        }),
      }
      serve(fx, false)
      const el = await mount(ATTRS)
      await vi.waitFor(
        () =>
          expect(
            $(el, '.weft-turn [data-weft-chip="stopped-by"]')
          ).toBeTruthy(),
        { timeout: 5_000 }
      )
      const p = $(el, '.weft-turn [data-weft-chip="stopped-by"]')!
      const panel = { text: p.textContent, title: p.getAttribute("title") }
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
      await waitFor(() =>
        expect(document.querySelector("[data-story-end]")).toBeTruthy()
      )
      const s = document.querySelector("a[data-stopped-by]")!
      expect(panel.text).toBe(c.chip)
      expect({ text: s.textContent, title: s.getAttribute("title") }).toEqual(
        panel
      )
      // The chip links to the step where the stop fired.
      expect(s.getAttribute("href")).toContain("step=0")
      expect(document.querySelector("[data-story-end]")!.textContent).toBe(
        c.end
      )
      expect(document.body.textContent).not.toContain(
        "the stream ended without run_finish"
      )
      // tool_calls contradicts every one of these stops: relabelled.
      expect(document.querySelector("[data-stop-reason]")!.textContent).toBe(
        "last step stopped on: tool_calls"
      )
    })

  it("a succeeded final answer: the chip on both, no relabel, the end card; a run with none says without run_finish", async () => {
    const fx: Fixture = {
      doc: { stopped_by: "final_answer", stop_reason: "stop" },
      events: events(),
    }
    serve(fx, false)
    const el = await mount(ATTRS)
    await vi.waitFor(
      () =>
        expect($(el, '.weft-turn [data-weft-chip="stopped-by"]')).toBeTruthy(),
      { timeout: 5_000 }
    )
    expect($(el, '.weft-turn [data-weft-chip="stopped-by"]')!.textContent).toBe(
      "the model answered"
    )
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
    await waitFor(() =>
      expect(document.querySelector("[data-story-end]")).toBeTruthy()
    )
    expect(document.querySelector("a[data-stopped-by]")!.textContent).toBe(
      "the model answered"
    )
    expect(document.querySelector("[data-story-end]")!.textContent).toBe(
      "■ the model answered at step 0"
    )
    // stop agrees with final_answer, and the run succeeded: no stop line.
    expect(document.querySelector("[data-stop-reason]")).toBeNull()
  })

  it("a failed run with no stopped_by and no run_finish ends on its error, the old label", async () => {
    serve(
      {
        doc: {
          status: "failed",
          err: "upstream down",
          stop_reason: "tool_calls",
        },
        events: events({ finish: false, reason: "tool_calls" }),
      },
      true
    )
    stubBrowser()
    vi.stubGlobal("scrollTo", vi.fn())
    renderApp(`/runs/${RUN}?view=story`)
    await waitFor(() =>
      expect(document.querySelector("[data-story-end]")).toBeTruthy()
    )
    // Its Error root span is the terminal record: no "without run_finish".
    expect(document.querySelector("[data-story-end]")!.textContent).toBe(
      "■ failed · upstream down"
    )
    expect(document.body.textContent).not.toContain(
      "the stream ended without run_finish"
    )
    expect(document.querySelector("a[data-stopped-by]")).toBeNull()
    expect(document.querySelector("[data-stop-reason]")!.textContent).toBe(
      "stop reason: tool_calls"
    )
  })

  it("an interrupted run with no stopped_by and no run_finish: the old note", async () => {
    serve(
      {
        doc: { status: "interrupted", stop_reason: "tool_calls" },
        events: events({ finish: false, reason: "tool_calls" }),
      },
      true
    )
    stubBrowser()
    vi.stubGlobal("scrollTo", vi.fn())
    renderApp(`/runs/${RUN}?view=story`)
    await waitFor(() =>
      expect(document.body.textContent).toContain(
        "the stream ended without run_finish"
      )
    )
    expect(document.querySelector("[data-story-end]")).toBeNull()
  })
})
