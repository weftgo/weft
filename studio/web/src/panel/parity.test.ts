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
import { queryClient } from "../lib/query"
import { ROLE } from "../lib/palette"
import { AWAITING, STATUS, statusOf } from "../lib/status"
import { statusClass } from "../lib/status-class"
import { PANEL_CSS } from "./styles"
import { clip, NO_CONTENT } from "../lib/title"
import type { DecisionDoc, RunDoc, RunRow } from "../lib/api"
import type { AgentView } from "./client"
import { TARGET_WORDS } from "../lib/target"
import { UNRUN_CALL_REASON } from "../lib/events"
import { EXPIRY_UNREAD } from "../lib/decisions"
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
  isLive,
  PLAYGROUND_OFF,
  resultCapReason,
  RUNTIMES_UNREAD,
  SETTLING_HINT,
  SETTLING_WORD,
} from "../lib/honesty"
import { renderApp, stubBrowser } from "../test/app"
import { renderWithRouter } from "../test/render"
import { createElement } from "react"
import { StepDiffTable } from "../components/studio/step-diff"
import { SessionTurns } from "../components/studio/session-turns"
import { RunsTable } from "../components/studio/runs-table"
import { matchesFilter } from "../lib/live-rows"
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
import { panelRoutes, shape, studioFor } from "../test/cards-fixture"
import {
  apiError,
  FakeStudio,
  golden,
  hiddenRefusal,
  pagedEvents,
  pagedRequests,
  transcriptOf,
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
import { NO_RUN_FINISH } from "../lib/stopped-by"
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
import { drawerHandoff, ReplayDrawer } from "../components/studio/replay-drawer"
import * as RK from "./replaykit"
import { editResultAndReplay, replayFromStep, rerun } from "../lib/replay"
import { choose } from "../test/select"
import type { ReplayDraft } from "../lib/replay"
import { DRAWER_ORDER } from "../lib/drawer-spec"
import { RUNS_IN } from "../lib/runs-in"
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
  RESUMED as APP_RESUMED,
  RUNTIMES_500 as APP_RUNTIMES_500,
  APP_SESSION,
  parkedRow as appParkedRow,
  resumedRow as appResumedRow,
  runtimeRow as appRuntime,
  serveAppTurn,
} from "../test/appturn-fixture"
import type { AppTurnOptions } from "../test/appturn-fixture"
import {
  DIR,
  EVENTS as DIR_EVENTS,
  OPS,
  SPANS as DIR_SPANS,
} from "../test/director-fixture"
import { build as buildLoop, FLAKY as LOOP_FLAKY } from "../test/loop-fixtures"
import {
  UL,
  UL_BODIES,
  UL_EVENTS,
  UL_LOGS,
  UL_REQUESTS,
  UL_SPANS,
  ulDoc,
} from "../test/usage-limit-fixture"

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

// P8-pre-web #2: a run document's hidden children hole carries its
// count; both surfaces say it beside the badge, in the same words.
describe("a hidden children hole's count (P8-pre-web)", () => {
  it("the run header badges hidden with '2 child runs' on both surfaces", async () => {
    const fx = (): Fixture => ({
      doc: {
        holes: [
          {
            hole: "hidden",
            reason:
              "2 child runs carry another public id, or none: a panel token reads its own public id's runs, so they are left out",
            fix: "open the run with the server (dev) token",
            count: 2,
          },
        ] as RunDoc["holes"],
      },
      events: events(),
    })
    const panelSel = `${PANEL.header} [data-hole="hidden"]`
    const p = await inPanel(fx(), panelSel)
    expect(badgeLabel($(p.el, panelSel))).toBe(
      `${HOLES.hidden.label} · 2 child runs`
    )
    p.el.remove()
    await settle()
    cleanup()
    document.body.innerHTML = ""
    const studioSel = `${STUDIO.header} [data-hole="hidden"]`
    await inStudio(fx(), studioSel)
    const b = document.querySelector(studioSel)!
    expect(b.firstChild?.textContent).toBe(HOLES.hidden.label)
    expect(b.querySelector("[data-hole-note]")?.textContent).toBe(
      " · 2 child runs"
    )
  })
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
      // The words the server sends for it (lib/honesty.ts's CAUSES,
      // pinned by holes.golden.json), never a fixture's own.
      {
        hole: "truncated",
        cause: "response_cap",
        ...CAUSES.truncated!.response_cap,
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
    same: string[]
    openChanges: boolean
    system: string
    changed: string[]
    holes: string[]
    rows: string[][]
    warnings: string[]
    unchecked: string
  }
  const read = (root: ParentNode, p: string): Rows => ({
    // UX-D-36: runs of same rows folded as "N same", the changes open.
    same: [...root.querySelectorAll(`[data-${p}preview-same]`)].map(
      (n) => n.textContent
    ),
    openChanges: [...root.querySelectorAll(`[data-${p}preview-op]`)]
      .filter((n) => n.getAttribute(`data-${p}preview-op`) !== "same")
      .every((n) => !n.closest("details")),
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
      if (doc === PV) expect(studio.same).toEqual(["1 same", "1 same"])
      expect(studio.openChanges).toBe(true)
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
    click(
      all(el, ".weft-turn").find((b) =>
        (b.getAttribute("data-weft-run") ?? "").includes(DERIVED)
      )
    )
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
      // P8-fix-a #12, the documented difference: the panel draws no
      // Story steps for a derived turn (no events to fold), so no
      // answer — it hands the steps off to the run page, whose drawer
      // reads the step's answer from its spans (lib/run-tree.ts's
      // derivedStep, drawer.test.ts).
      answers: all(el, "[data-verdict]").length,
    }
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
    expect(panel.answers).toBe(0)
    expect(studio.steps).toBe(2)
    // The run tree's rows on both: "the run tree on both surfaces (U8.3)".
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
    expect(stats.textContent).toBe("1 run · — tok")
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
    /** The run's commands are read: wait for the lost mark (or for
     * the read, when none is expected). */
    marked?: boolean
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
      name: "a decision lost before it landed is marked on both after a reload (P10a-pre-web-fix)",
      o: { runtimes: [appRuntime({ connected: false })], lost: true },
      kind: "read-only",
      marked: true,
    },
    {
      name: "a call lost and then decided again is not marked on either: the latest decide decides (P10a-pre-web-fix)",
      o: { runtimes: [appRuntime({ connected: false })], lost: "redecided" },
      kind: "read-only",
      marked: false,
    },
    {
      name: "an opted-in app's runtime gone (U20.2): disconnected, the restart fix",
      o: { runtimes: [appRuntime({ connected: false })] },
      kind: "read-only",
    },
    {
      name: "an opted-in app gone beside a CLI of its service without Sessions (U20.2-fix): the restart fix, never set Sessions",
      o: {
        runtimes: [
          appRuntime({ id: "rt_cli", sessions: false, breakpoints_own: false }),
          appRuntime({ connected: false }),
        ],
      },
      kind: "read-only",
    },
    {
      name: "the app's own link dialled another Studio (U20.2): named, the fix",
      o: {
        runtimes: [],
        runtimeLink: { studio_url: "http://127.0.0.1:7342", in_process: false },
      },
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
          all(el, ".weft-turn").some((b) =>
            (b.getAttribute("data-weft-run") ?? "").includes(id)
          )
        ).toBe(true),
      { timeout: 5_000 }
    )
    click(
      all(el, ".weft-turn").find((b) =>
        (b.getAttribute("data-weft-run") ?? "").includes(id)
      )
    )
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
      click(button(el, "approve"))
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
            (button(el, "approve") as HTMLButtonElement | undefined)?.disabled
          ).toBe(false),
        { timeout: 5_000 }
      )
      await settle()
    } else if (c.click) {
      click(button(el, "approve"))
      const done = () =>
        $(el, `${callSel} [data-weft-app-verb="rejected"]`) ??
        $(el, '[data-weft-app-owned="read-only"]') ??
        $(el, '[data-weft-app-owned="resumed"]')
      await vi.waitFor(() => expect(done()).toBeTruthy(), { timeout: 5_000 })
      await settle()
    }
    if (c.marked !== undefined) {
      await vi.waitFor(
        () =>
          expect(
            fake.calls(`GET runs/${APP_PARKED}/commands`).length
          ).toBeGreaterThan(0),
        { timeout: 5_000 }
      )
      if (c.marked)
        await vi.waitFor(
          () => expect($(el, '[data-weft-app-verb="lost"]')).toBeTruthy(),
          { timeout: 5_000 }
        )
      await settle()
    }
    const owned = $(el, "[data-weft-app-owned]")
    const seen = {
      kind:
        owned?.getAttribute("data-weft-app-owned") ??
        (button(el, "approve") ? "decide" : null),
      line: owned?.textContent ?? "",
      refused: all(el, '[data-weft-refused="app-owned"]').length,
      after: $(el, `${callSel} [data-weft-app-verb]`)?.textContent ?? "",
      lost: !!$(el, '[data-weft-app-verb="lost"]'),
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
        .find((n) => n.textContent === "approve")!
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
            (n) => n.textContent === "approve"
          )?.disabled
        ).toBe(false)
      )
    } else if (c.click) {
      ;[...box()!.querySelectorAll("button")]
        .find((n) => n.textContent === "approve")!
        .click()
      await waitFor(() =>
        expect(
          box()!.querySelector('[data-app-verb="rejected"]') ??
            box()!.querySelector('[data-app-owned="read-only"]') ??
            box()!.querySelector('[data-app-owned="resumed"]')
        ).toBeTruthy()
      )
    }
    if (c.marked !== undefined) {
      await waitFor(() =>
        expect(
          fake.calls(`GET runs/${APP_PARKED}/commands`).length
        ).toBeGreaterThan(0)
      )
      if (c.marked)
        await waitFor(() =>
          expect(box()!.querySelector("[data-command-lost]")).toBeTruthy()
        )
      await new Promise((r) => setTimeout(r, 50))
    }
    const owned = box()!.querySelector("[data-app-owned]")
    const seen = {
      kind:
        owned?.getAttribute("data-app-owned") ??
        ([...box()!.querySelectorAll("button")].some(
          (n) => n.textContent === "approve"
        )
          ? "decide"
          : null),
      line: owned?.textContent ?? "",
      refused: box()!.querySelectorAll('[data-refused="app-owned"]').length,
      after: box()!.querySelector("[data-app-verb]")?.textContent ?? "",
      lost: !!box()!.querySelector("[data-command-lost]"),
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
          all(el, ".weft-turn").some((b) =>
            (b.getAttribute("data-weft-run") ?? "").includes(APP_PARKED)
          )
        ).toBe(true),
      { timeout: 5_000 }
    )
    click(
      all(el, ".weft-turn").find((b) =>
        (b.getAttribute("data-weft-run") ?? "").includes(APP_PARKED)
      )
    )
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
          all(el, ".weft-turn").some((b) =>
            (b.getAttribute("data-weft-run") ?? "").includes(APP_PARKED)
          )
        ).toBe(true),
      { timeout: 5_000 }
    )
    click(
      all(el, ".weft-turn").find((b) =>
        (b.getAttribute("data-weft-run") ?? "").includes(APP_PARKED)
      )
    )
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

// Decisions (U13.2): a decided park's outcome and the resume's
// "resumed from" card, from the run documents' decided / resumes blocks
// (studio/testdata/api/decisions.golden.json — thread_approvals' t1,
// agent-7's deny), read the same on both surfaces: lib/decisions.ts's
// words, the turn tag the link.
describe("a parked call's request expiry on both surfaces (UX-B-41)", () => {
  // The parked turn's approval.request record: Studio's card reads it
  // from the session's timeline, the panel from the run's own marks
  // (/entries?run=) — the same record, the same lib/decisions.ts words.
  const mark = (at: string) => ({
    at,
    record: "thread",
    kind: "approval.request",
    event: "weft.thread.approval.request",
    lane: "approvals",
    run_id: APP_PARKED,
    turn: 1,
    content_mark: "none",
    fields: { call_id: APP_CALL.id, expiry: at },
  })
  const fail = () => apiError(500, "internal", "session store unavailable")
  /** fails: both reads answer 500 (the panel's /entries?run=, Studio's
   * timeline). */
  const serveTurn = (at: string, studioMeta = false, fails = false) =>
    fails
      ? serveAppTurn(new FakeStudio(), { studioMeta })
          .on(`GET sessions/${APP_SESSION}/timeline`, fail)
          .on(`GET sessions/${APP_SESSION}/entries`, fail)
          .install()
      : serveOK(at, studioMeta)
  const serveOK = (at: string, studioMeta: boolean) =>
    serveAppTurn(new FakeStudio(), { studioMeta })
      .on(`GET sessions/${APP_SESSION}/timeline`, {
        session_id: APP_SESSION,
        agent: "approvals-example",
        status: "succeeded",
        turns: [],
        marks: [mark(at)],
        lanes: [],
        marks_total: 1,
        holes: [],
      })
      .on(`GET sessions/${APP_SESSION}/entries`, {
        session_id: APP_SESSION,
        run: APP_PARKED,
        entries: [mark(at)],
        total: 1,
        offset: 0,
        next_offset: null,
        holes: [],
      })
      .install()
  const expiryWords = (n: Element) => ({
    state: n.getAttribute("data-request-expiry"),
    // The countdown is each surface's own clock: its figure is not compared.
    text: n.textContent.replace(/\(in [^)]*\)/, "(in …)"),
    title: n.getAttribute("title"),
  })
  for (const [state, ms] of [
    ["lapsed", -5_000],
    ["open", 60_000],
  ] as const)
    it(`a ${state} request reads alike`, async () => {
      const at = new Date(Date.now() + ms).toISOString()
      serveTurn(at)
      const el = await mount({
        "data-endpoint": "http://studio.test/studio/",
        "data-open": "true",
        "data-public-id": APP_PUB,
      })
      await vi.waitFor(
        () =>
          expect(
            all(el, ".weft-turn").some((b) =>
              (b.getAttribute("data-weft-run") ?? "").includes(APP_PARKED)
            )
          ).toBe(true),
        { timeout: 5_000 }
      )
      click(
        all(el, ".weft-turn").find((b) =>
          (b.getAttribute("data-weft-run") ?? "").includes(APP_PARKED)
        )
      )
      const pn = await vi.waitFor(
        () => {
          const n = $(
            el,
            `.weft-call[data-key="${APP_CALL.id}"] [data-request-expiry]`
          )
          expect(n).toBeTruthy()
          return n!
        },
        { timeout: 5_000 }
      )
      const panel = expiryWords(pn)
      el.remove()
      await settle()
      cleanup()
      document.body.innerHTML = ""

      stubBrowser()
      vi.stubGlobal("scrollTo", vi.fn())
      StudioEventSource.reset()
      vi.stubGlobal("EventSource", StudioEventSource)
      setStudioToken("")
      serveTurn(at, true)
      renderApp(`/runs/${APP_PARKED}?view=story`)
      const sn = await waitFor(() => {
        const n = document.querySelector(
          `[data-pending-call="${APP_CALL.id}"] [data-request-expiry]`
        )
        expect(n).toBeTruthy()
        return n!
      })
      const studio = expiryWords(sn)
      cleanup()
      expect(panel).toEqual(studio)
      expect(panel.state).toBe(state)
    })

  it("a failed read draws the same not_recorded badge, no expiry (P9-pre-web-fix6)", async () => {
    const badgeOf = (n: Element) => ({
      hole: n.getAttribute("data-hole"),
      title: n.getAttribute("title"),
    })
    serveTurn("", false, true)
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-open": "true",
      "data-public-id": APP_PUB,
    })
    await vi.waitFor(
      () =>
        expect(
          all(el, ".weft-turn").some((b) =>
            (b.getAttribute("data-weft-run") ?? "").includes(APP_PARKED)
          )
        ).toBe(true),
      { timeout: 5_000 }
    )
    click(
      all(el, ".weft-turn").find((b) =>
        (b.getAttribute("data-weft-run") ?? "").includes(APP_PARKED)
      )
    )
    const pb = await vi.waitFor(
      () => {
        const n = $(el, `.weft-call[data-key="${APP_CALL.id}"]`)
          ?.closest(".weft-step")
          ?.querySelector('.weft-step-h [data-hole="not_recorded"]')
        expect(n).toBeTruthy()
        return n!
      },
      { timeout: 5_000 }
    )
    const panel = badgeOf(pb)
    expect($(el, "[data-request-expiry]")).toBeNull()
    el.remove()
    await settle()
    cleanup()
    document.body.innerHTML = ""

    stubBrowser()
    vi.stubGlobal("scrollTo", vi.fn())
    StudioEventSource.reset()
    vi.stubGlobal("EventSource", StudioEventSource)
    setStudioToken("")
    serveTurn("", true, true)
    renderApp(`/runs/${APP_PARKED}?view=story`)
    const sb = await waitFor(
      () => {
        const n = document.querySelector(
          '[data-parked] [data-hole="not_recorded"]'
        )
        expect(n).toBeTruthy()
        return n!
      },
      { timeout: 8_000 }
    )
    const studio = badgeOf(sb)
    expect(document.querySelector("[data-request-expiry]")).toBeNull()
    cleanup()
    expect(panel).toEqual(studio)
    expect(panel.title).toBe(
      `${EXPIRY_UNREAD.reason} — fix: ${EXPIRY_UNREAD.fix}`
    )
  })

  // P9-pre-web-fix7: the badge only when nothing is held — the panel's
  // rule (appturn.test.ts "a failed re-read keeps …").
  it("Studio: a failed refetch keeps the words it read, no badge", async () => {
    const at = new Date(Date.now() + 60_000).toISOString()
    let down = false
    stubBrowser()
    vi.stubGlobal("scrollTo", vi.fn())
    StudioEventSource.reset()
    vi.stubGlobal("EventSource", StudioEventSource)
    setStudioToken("")
    serveAppTurn(new FakeStudio(), { studioMeta: true })
      .on(`GET sessions/${APP_SESSION}/timeline`, () =>
        down
          ? fail()
          : {
              session_id: APP_SESSION,
              agent: "approvals-example",
              status: "succeeded",
              turns: [],
              marks: [mark(at)],
              lanes: [],
              marks_total: 1,
              holes: [],
            }
      )
      .install()
    renderApp(`/runs/${APP_PARKED}?view=story`)
    const sel = `[data-pending-call="${APP_CALL.id}"] [data-request-expiry]`
    await waitFor(() =>
      expect(
        document.querySelector(sel)?.getAttribute("data-request-expiry")
      ).toBe("open")
    )
    down = true
    const key = ["session-timeline", APP_SESSION]
    await queryClient.refetchQueries({ queryKey: key, exact: true })
    await waitFor(
      () =>
        expect(
          queryClient.getQueryState(key)?.status,
          "the refetch failed"
        ).toBe("error"),
      { timeout: 8_000 }
    )
    await settle()
    expect(
      document.querySelector(sel)?.getAttribute("data-request-expiry")
    ).toBe("open")
    expect(document.querySelector("[data-parked] [data-hole]")).toBeNull()
    cleanup()
  })
})

describe("decisions on both surfaces (U13.2)", () => {
  const DONE_LINE = "✋ denied · agent-7 · 'need a photo of the damage' → t2"
  // The parked call's row in the panel's approvals block (the resumed
  // card's rows carry no data-key).
  const PARKED_DECISION = `.weft-call[data-key="${APP_CALL.id}"] [data-weft-decision]`
  const blocks = {
    ...golden<Record<string, DecisionDoc>>("decisions"),
    ...golden<Record<string, DecisionDoc>>("decisions_support_desk"),
  }
  // The decision time is each surface's own clock (Studio's local
  // absoluteTime, the panel's UTC): compared without it.
  const untimed = (s: string | null | undefined) =>
    (s ?? "").split(" · ").slice(0, -1).join(" · ")
  /** The refund rw.support-desk's resume runs before step 0, its result. */
  const REFUND = "refund of 249.90 issued"
  const serveDecided = (
    studioMeta: boolean,
    read?: boolean,
    seed: "thread" | "support_desk" | "resolved" = "thread"
  ) => {
    const fake = serveAppTurn(new FakeStudio(), { resumed: true, studioMeta })
    // The fixture's parked call (call_deploy_1) stands for r1 (the
    // support desk's first call, its approved one) — or, seed resolved,
    // for the resumed fixture's resolved add_note (its second).
    const nth = seed === "resolved" ? 1 : 0
    const call = (d: DecisionDoc) => ({
      ...d,
      calls: d.calls
        .slice(nth, nth + 1)
        .map((c) => ({ ...c, call_id: APP_CALL.id })),
    })
    const t1 = call(
      blocks[
        seed === "support_desk"
          ? "support_desk_decided"
          : seed === "resolved"
            ? "resumed_decided"
            : read
              ? "read_token_t1_decided"
              : "thread_t1_decided"
      ]
    )
    const t2 = call(
      blocks[
        seed === "support_desk"
          ? "support_desk_resumes"
          : seed === "resolved"
            ? "resumed_resumes"
            : "thread_t2_resumes"
      ]
    )
    if (seed === "support_desk") {
      // The approved call ran before step 0's model call: its
      // tool_start / tool_finish come before any step_start.
      const at = (pos: number, event: object) => ({ pos, time: T0, event })
      fake.on(
        `GET runs/${APP_RESUMED}/events`,
        pagedEvents([
          at(0, {
            type: "run_start",
            id: APP_RESUMED,
            model: { provider: "demo", name: "ops" },
            agent: "approvals-example",
          }),
          at(1, {
            type: "tool_start",
            run_id: APP_RESUMED,
            seq: 1,
            call_id: APP_CALL.id,
            name: APP_CALL.name,
            args: APP_CALL.args,
          }),
          at(2, {
            type: "tool_finish",
            run_id: APP_RESUMED,
            seq: 1,
            call_id: APP_CALL.id,
            name: APP_CALL.name,
            content: REFUND,
            is_error: false,
          }),
          at(3, { type: "step_start", run_id: APP_RESUMED, index: 0 }),
          at(4, {
            type: "step_finish",
            run_id: APP_RESUMED,
            index: 0,
            reason: "stop",
            usage: { input_tokens: 10, output_tokens: 5 },
          }),
          at(5, {
            type: "run_finish",
            run_id: APP_RESUMED,
            usage: { input_tokens: 10, output_tokens: 5 },
            steps: 1,
          }),
        ])
      )
    }
    fake
      .on(`GET runs/${APP_PARKED}`, {
        ...appParkedRow(),
        resumed_by: APP_RESUMED,
        children: [],
        holes: [],
        decided: { ...t1, run: APP_RESUMED },
      })
      .on(`GET runs/${APP_RESUMED}`, {
        ...appResumedRow(),
        resumed_from: APP_PARKED,
        children: [],
        holes: [],
        resumes: { ...t2, run: APP_PARKED },
      })
    return fake.install()
  }
  async function panelSeen(run: string, sel: string) {
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-open": "true",
      "data-public-id": APP_PUB,
    })
    await vi.waitFor(
      () =>
        expect(
          all(el, ".weft-turn").some((b) =>
            (b.getAttribute("data-weft-run") ?? "").includes(run)
          )
        ).toBe(true),
      { timeout: 5_000 }
    )
    click(
      all(el, ".weft-turn").find((b) =>
        (b.getAttribute("data-weft-run") ?? "").includes(run)
      )
    )
    await vi.waitFor(() => expect($(el, sel)).toBeTruthy(), { timeout: 5_000 })
    return el
  }
  const reset = async (el?: HTMLElement) => {
    el?.remove()
    await settle()
    cleanup()
    document.body.innerHTML = ""
    stubBrowser()
    vi.stubGlobal("scrollTo", vi.fn())
    StudioEventSource.reset()
    vi.stubGlobal("EventSource", StudioEventSource)
    setStudioToken("")
  }

  it("the parked turn reads its outcome: ✋ denied · agent-7 · 'need a photo of the damage' → t2", async () => {
    serveDecided(false)
    const el = await panelSeen(APP_PARKED, PARKED_DECISION)
    const dec = $(el, PARKED_DECISION)!
    const panel = `${dec.firstChild?.textContent} → ${dec.querySelector("[data-weft-resumed]")?.textContent}`
    const panelDetail = dec.querySelector(".weft-reason")?.textContent
    // No verb is left on a decided park.
    expect(button(el, "approve")).toBeUndefined()
    await reset(el)
    serveDecided(true)
    renderApp(`/runs/${APP_PARKED}?view=story`)
    await waitFor(() =>
      expect(document.querySelector("[data-decision-line]")).toBeTruthy()
    )
    const row = document.querySelector("[data-decision]")!
    const studio = `${row.querySelector("[data-decision-line]")?.textContent} → ${row.querySelector("[data-resumed]")?.textContent}`
    const studioDetail = row.querySelector(
      "[data-decision-detail]"
    )?.textContent
    expect(
      [...document.querySelectorAll("[data-parked] button")].map(
        (b) => b.textContent
      )
    ).not.toContain("approve")
    cleanup()
    expect([panel, studio]).toEqual([DONE_LINE, DONE_LINE])
    expect(untimed(panelDetail)).toBe(untimed(studioDetail))
    expect(untimed(studioDetail)).toBe("via user")
    // Each surface's clock: the panel's UTC, Studio's local with its zone.
    expect(panelDetail).toMatch(/ · \d\d:\d\d:\d\d UTC$/)
    expect(studioDetail).toMatch(/ · \d{4}-\d\d-\d\d \d\d:\d\d:\d\d \S+$/)
  })

  // P9-pre-web-fix 2: the decided call's own row reads its outcome,
  // never "awaiting a decision" (lib/cards.ts parkWord), and the
  // answer's tools line leaves it out of the held count.
  it("the decided call's row reads ✋ denied → t2 on both surfaces, never awaiting", async () => {
    const WORD = "✋ denied → t2"
    // The parked call started (its tool_start) and never finished.
    const at = (pos: number, event: object) => ({ pos, time: T0, event })
    const parkedEvents = pagedEvents([
      at(0, {
        type: "run_start",
        id: APP_PARKED,
        model: { provider: "demo", name: "ops" },
        agent: "approvals-example",
      }),
      at(1, { type: "step_start", run_id: APP_PARKED, index: 0 }),
      at(2, {
        type: "tool_start",
        run_id: APP_PARKED,
        seq: 1,
        call_id: APP_CALL.id,
        name: APP_CALL.name,
        args: APP_CALL.args,
      }),
      at(3, {
        type: "step_finish",
        run_id: APP_PARKED,
        index: 0,
        reason: "tool_calls",
        usage: { input_tokens: 10, output_tokens: 5 },
      }),
      at(4, {
        type: "run_finish",
        run_id: APP_PARKED,
        usage: { input_tokens: 10, output_tokens: 5 },
        steps: 1,
        pending: [APP_CALL],
      }),
    ])
    serveDecided(false).on(`GET runs/${APP_PARKED}/events`, parkedEvents)
    const el = await panelSeen(APP_PARKED, PARKED_DECISION)
    await vi.waitFor(
      () =>
        expect(
          all(el, `.weft-call[data-key="${APP_CALL.id}"] .weft-res`).map(
            (r) => r.textContent
          )
        ).toContain(WORD),
      { timeout: 5_000 }
    )
    const panelTools = all(el, '[data-answer-line="tools"]').map(
      (d) => d.textContent
    )
    await reset(el)
    serveDecided(true).on(`GET runs/${APP_PARKED}/events`, parkedEvents)
    renderApp(`/runs/${APP_PARKED}?view=story`)
    await waitFor(() =>
      expect(
        document.querySelector(
          `[data-call="${APP_CALL.id}"] [data-parked-call]`
        )?.textContent
      ).toBe(WORD)
    )
    cleanup()
    expect(panelTools.join(" ")).not.toContain(AWAITING)
  })

  // U13.2-fix 6: rw.support-desk itself — a plain Generate the arena
  // resumes with Approve, no actor, no denial — and the resumed card's
  // result line (fix 2): the refund ran before step 0, which no longer
  // draws it, so the card says what it left, on both surfaces.
  it("rw.support-desk's shape: ✓ approved → t2, and t2's card carries the refund's result", async () => {
    const LINE = "✓ approved → t2"
    serveDecided(false, false, "support_desk")
    let el = await panelSeen(APP_PARKED, PARKED_DECISION)
    const dec = $(el, PARKED_DECISION)!
    const panelParked = `${dec.firstChild?.textContent} → ${dec.querySelector("[data-weft-resumed]")?.textContent}`
    await reset(el)
    serveDecided(false, false, "support_desk")
    el = await panelSeen(
      APP_RESUMED,
      "[data-weft-resumed-card] [data-weft-result]"
    )
    const card = $(el, "[data-weft-resumed-card]")!
    const panelCard = [
      card.querySelector("[data-weft-decision]")?.firstChild?.textContent,
      card.querySelector("[data-weft-result]")?.textContent,
    ]
    // Step 0 no longer draws the call the card lists.
    expect($(el, '.weft-step[data-key="s0"]')?.textContent ?? "").not.toContain(
      REFUND
    )
    await reset(el)
    serveDecided(true, false, "support_desk")
    renderApp(`/runs/${APP_PARKED}?view=story`)
    await waitFor(() =>
      expect(document.querySelector("[data-decision-line]")).toBeTruthy()
    )
    const row = document.querySelector("[data-decision]")!
    const studioParked = `${row.querySelector("[data-decision-line]")?.textContent} → ${row.querySelector("[data-resumed]")?.textContent}`
    cleanup()
    serveDecided(true, false, "support_desk")
    renderApp(`/runs/${APP_RESUMED}?view=story`)
    await waitFor(() =>
      expect(document.querySelector("[data-resumed-result]")).toBeTruthy()
    )
    const sc = document.querySelector("[data-resumed-card]")!
    const studioCard = [
      sc.querySelector("[data-decision-line]")?.textContent,
      sc.querySelector("[data-resumed-result]")?.textContent,
    ]
    cleanup()
    expect([panelParked, studioParked]).toEqual([LINE, LINE])
    expect(panelCard).toEqual(["✓ approved", `→ ${REFUND}`])
    expect(studioCard).toEqual(panelCard)
  })

  // U13.2-fix2 2: a resolve runs no handler — no tool event, nothing in
  // the fold — so the card's "→ …" is the server's result on the decided
  // call: the resumed fixture's add_note, "noted by hand", on both.
  it("the resumed fixture's resolved call: ↩ resolved · op_42 → noted by hand", async () => {
    serveDecided(false, false, "resolved")
    const el = await panelSeen(
      APP_RESUMED,
      "[data-weft-resumed-card] [data-weft-result]"
    )
    const card = $(el, "[data-weft-resumed-card]")!
    const panel = [
      card.querySelector("[data-weft-decision]")?.firstChild?.textContent,
      card.querySelector("[data-weft-result]")?.textContent,
    ]
    await reset(el)
    serveDecided(true, false, "resolved")
    renderApp(`/runs/${APP_RESUMED}?view=story`)
    await waitFor(() =>
      expect(document.querySelector("[data-resumed-result]")).toBeTruthy()
    )
    const sc = document.querySelector("[data-resumed-card]")!
    const studio = [
      sc.querySelector("[data-decision-line]")?.textContent,
      sc.querySelector("[data-resumed-result]")?.textContent,
    ]
    cleanup()
    expect(panel).toEqual(["↩ resolved · op_42", "→ noted by hand"])
    expect(studio).toEqual(panel)
  })

  it("the resume turn opens with the resumed-from card, before step 0, on both", async () => {
    serveDecided(false)
    const el = await panelSeen(APP_RESUMED, "[data-weft-resumed-card]")
    const card = $(el, "[data-weft-resumed-card]")!
    const panel = [
      card.querySelector(".weft-step-h")?.textContent,
      card.querySelector("[data-weft-decision]")?.firstChild?.textContent,
    ]
    // Before step 0: the card precedes the first step in the turn view.
    const step0 = $(el, '.weft-step[data-key="s0"]')
    expect(step0).toBeTruthy()
    expect(
      card.compareDocumentPosition(step0!) & Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy()
    await reset(el)
    serveDecided(true)
    renderApp(`/runs/${APP_RESUMED}?view=story`)
    await waitFor(() =>
      expect(document.querySelector("[data-resumed-card]")).toBeTruthy()
    )
    const sc = document.querySelector("[data-resumed-card]")!
    const studio = [
      sc.querySelector("[data-resumed-from]")?.textContent,
      sc.querySelector("[data-decision-line]")?.textContent,
    ]
    const first = document.querySelector('[data-step="0"]')
    expect(first).toBeTruthy()
    expect(
      sc.compareDocumentPosition(first!) & Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy()
    cleanup()
    expect(panel).toEqual([
      "resumed from t1 · 1 decided call",
      "✋ denied · agent-7 · 'need a photo of the damage'",
    ])
    expect(studio).toEqual(panel)
  })

  it("a read token: no who, no reason — the hidden badge on both", async () => {
    serveDecided(false, true)
    const el = await panelSeen(APP_PARKED, PARKED_DECISION)
    const panel = [
      $(el, PARKED_DECISION)!.firstChild?.textContent,
      $(el, `${PARKED_DECISION} [data-hole]`)?.getAttribute("data-hole"),
      all(el, `${PARKED_DECISION} [data-hole]`).length,
    ]
    await reset(el)
    serveDecided(true, true)
    renderApp(`/runs/${APP_PARKED}?view=story`)
    await waitFor(() =>
      expect(document.querySelector("[data-decision-line]")).toBeTruthy()
    )
    const studio = [
      document.querySelector("[data-decision-line]")?.textContent,
      document
        .querySelector("[data-decision] [data-hole]")
        ?.getAttribute("data-hole"),
      document.querySelectorAll("[data-decision] [data-hole]").length,
    ]
    cleanup()
    // The actor is metadata (weft.approval.actor), never content: it
    // names the decider to every token, so only the reason is hidden —
    // one badge, no who hole (U13.2-fix 3).
    expect(panel).toEqual(["✋ denied · agent-7", "hidden", 1])
    expect(studio).toEqual(panel)
  })
})

// Cost (plan A5.3): the panel's turn row and the run page's header read
// the same figure and the same reason for the same row — lib/cost.ts's
// words on both — own and delegated, two numbers on each.
describe("a turn's cost on both surfaces (A5.3)", () => {
  const WHY = "openai/kimi-k2.7-code has no price"
  const SCRIPTED = "a scripted model: nothing bills it, an exact $0"
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
        unpriced_models: ["openai/kimi-k2.7-code"],
        delegated_cost_usd: 0.0011,
        delegated_cost_known: true,
      },
    },
    {
      name: "a scripted model (U7): $0 · scripted, exact",
      doc: {
        cost_usd: 0,
        cost_known: true,
        cost_badge: "scripted",
        cost_reason: SCRIPTED,
        cache: "exact",
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
      // U7: the header's one unpriced line (the panel counts turns).
      const pLine = $(el, "[data-weft-unpriced]")
      const pUnpriced = pLine && {
        text: pLine.textContent,
        title: pLine.getAttribute("title"),
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
      if (c.doc.cost_badge === "scripted")
        expect(pOwn).toEqual({ text: "$0 · scripted", title: SCRIPTED })
      // The one unpriced line: the same models, the same title, on both
      // (the panel's says "turn" and adds where the link goes) — or neither.
      const sLine = document.querySelector("[data-unpriced-line] > span")
      const sUnpriced = sLine && {
        text: sLine.textContent.replace(/\brun\b/, "turn"),
        title: `${sLine.getAttribute("title")} · price in Studio`,
      }
      expect(pUnpriced).toEqual(sUnpriced)
      if (c.doc.cost_badge === "unpriced")
        expect(pUnpriced?.text).toBe(
          "1 turn unpriced: no row for openai/kimi-k2.7-code"
        )
    })
})

// The result pane's figure (A5.3 fixes): the replayed run's whole bill,
// own + delegated folded, says so on both surfaces — the panel's result
// pane and the playground's result card read the same figure, the same
// title, the same wrapper words.
describe("a replayed run's cost on both surfaces (A5.3)", () => {
  const PG = "pg_x1"
  type Bill = Partial<
    Pick<
      RunDoc,
      "cost_usd" | "cost_badge" | "cost_reason" | "delegated_cost_usd"
    >
  >
  const row = (bill: Bill = {}) => ({
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
    ...bill,
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

  const bills: { name: string; bill: Bill; text: string }[] = [
    { name: "priced", bill: {}, text: "$0.0050" },
    {
      // U5U7-fix 1: a scripted parent whose subagents bill is not "$0 · scripted".
      name: "a scripted parent with paid subagents",
      bill: {
        cost_usd: 0,
        cost_badge: "scripted",
        cost_reason: "a scripted model: nothing bills it, an exact $0",
        delegated_cost_usd: 0.5,
      },
      text: "$0.50",
    },
  ]
  for (const { name, bill, text } of bills)
    it(`${name}: the panel's result pane is the playground's result card: figure, title, the whole-bill wrapper`, async () => {
      const routes = baseRoutes()
      routes["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
      routes["playground/commands/cmd_1"] = cmd("finished")
      routes[`runs/${PG}`] = { ...row(bill), children: [] }
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
        .on(`GET runs/${PG}`, { ...row(bill), children: [] })
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
      const go = await screen.findByRole("button", { name: /^Run A / })
      await waitFor(() => expect(go).toHaveProperty("disabled", false))
      fireEvent.click(go)
      await waitFor(
        () => expect(document.querySelector("[data-result-cost]")).toBeTruthy(),
        { timeout: 10_000 }
      )
      const studio = read(document.querySelector("[data-result-cost]"))
      expect(panel.text).toBe(text)
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
        error_type: "cancelled",
        stopped_by: "canceled",
        cancel_cause: "studio.cancel",
        stop_reason: "tool_calls",
        err: "weft: run failed at step 0: context canceled",
        steps: 1,
      },
      // Beside the status word "cancelled": the cause alone (W7-prep).
      chip: "· studio.cancel",
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

// A settling run (U2-fix2): its root span stored — the status reads
// succeeded — its run_finish not yet. Both surfaces keep the status
// word and draw the same settling note, lib/honesty.ts's words, and
// both keep following it (isLive), never "ended without run_finish".
describe("a settling run on both surfaces (U2-fix2)", () => {
  it("the panel's settling chip is the run page's header note", async () => {
    const fx: Fixture = {
      doc: { status: "succeeded", settling: true, stop_reason: "stop" },
      events: events({ finish: false }),
    }
    serve(fx, false)
    const el = await mount(ATTRS)
    await vi.waitFor(
      () =>
        expect($(el, '.weft-turn [data-weft-chip="settling"]')).toBeTruthy(),
      { timeout: 5_000 }
    )
    const p = $(el, '.weft-turn [data-weft-chip="settling"]')!
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
      expect(document.querySelector("[data-settling]")).toBeTruthy()
    )
    const s = document.querySelector("[data-settling]")!
    expect(panel).toEqual({ text: SETTLING_WORD, title: SETTLING_HINT })
    expect({ text: s.textContent, title: s.getAttribute("title") }).toEqual(
      panel
    )
    expect(isLive(fx.doc)).toBe(true)
    expect(isLive({ status: "succeeded" })).toBe(false)
  })

  // U5's carried c2/c3: while it settles the story's foot reads "stream
  // in progress…", never "ended without run_finish"; once it settles
  // (the root's window passed, the poll answers settling absent) the
  // note goes on both surfaces.
  it("reads in progress while settling, and drops the note once settled", async () => {
    const fx: Fixture = {
      doc: { status: "succeeded", settling: true, stop_reason: "stop" },
      events: events({ finish: false }),
    }
    const settled = {
      ...runRow({ id: RUN, request_count: 1, status: "succeeded" }),
      children: [],
      holes: [],
      stop_reason: "stop",
    }
    const fake = serve(fx, true)
    stubBrowser()
    vi.stubGlobal("scrollTo", vi.fn())
    renderApp(`/runs/${RUN}?view=story`)
    await waitFor(() =>
      expect(document.querySelector("[data-settling]")).toBeTruthy()
    )
    await waitFor(() =>
      expect(document.body.textContent).toContain("stream in progress…")
    )
    expect(document.body.textContent).not.toContain(NO_RUN_FINISH)
    fake.on(`GET runs/${RUN}`, settled)
    await waitFor(
      () => expect(document.querySelector("[data-settling]")).toBeNull(),
      { timeout: 5_000 }
    )
    cleanup()
    document.body.innerHTML = ""

    // The panel reads its list again once a live row's window would
    // have passed (meta.interrupted_after_ms): shortened here.
    const pfake = serve(fx, false).on("GET meta", {
      ...META,
      capabilities: ["ingest", "requests"],
      interrupted_after_ms: 200,
    })
    const el = await mount(ATTRS)
    await vi.waitFor(
      () =>
        expect($(el, '.weft-turn [data-weft-chip="settling"]')).toBeTruthy(),
      { timeout: 5_000 }
    )
    pfake.on(`GET runs/${RUN}`, settled)
    pfake.on("GET runs", {
      total: 1,
      runs: [runRow({ id: RUN, request_count: 1, status: "succeeded" })],
      next_before: null,
    })
    await vi.waitFor(
      () => expect($(el, '.weft-turn [data-weft-chip="settling"]')).toBeNull(),
      { timeout: 8_000 }
    )
    el.remove()
  }, 20_000)
})

// Titles, not ids (U6): one row, both surfaces, one helper
// (lib/title.ts). The panel's narrow picker reads "● parked t3 How many
// units… · 1 step · 811ms", its turn row "t3 How many units…"; the run page's
// heading is "agent · the prompt" and the tab "agent · How many units…
// · succeeded · weft studio"; the id rides in a title / a mono line,
// never as the label. A content-off row reads "(content not captured)"
// on both.
const AT = (ms: number) => new Date(Date.parse(T0) + ms).toISOString()

describe("titles, not ids, on both surfaces (U6)", () => {
  const PROMPT = "How many units of SKU A-17 are in stock right now, please?"
  const CUT = clip(PROMPT)
  const shapes: {
    name: string
    doc: Partial<RunDoc>
    option: string
    row: string
    heading: string
    tab: string
    full: string
  }[] = [
    {
      name: "a parked turn with a prompt",
      doc: { prompt_preview: PROMPT, pending: 1, turn: 3, finished: AT(811) },
      option: `● parked t3 ${CUT} · 1 step · 811ms`,
      row: `t3 ${CUT}`,
      heading: `acme-support·${PROMPT}`,
      tab: `acme-support · ${CUT} · succeeded · weft studio`,
      full: PROMPT,
    },
    {
      name: "a content-off turn of an unnamed agent",
      doc: { agent: "", message_count: 0, event_count: 6, turn: 3 },
      option: `● succeeded t3 ${NO_CONTENT} · 1 step · 0ms`,
      row: `t3 ${NO_CONTENT}`,
      heading: `unnamed agent·${NO_CONTENT}`,
      tab: `${NO_CONTENT} · succeeded · weft studio`,
      full: NO_CONTENT,
    },
    // U6-fix: kind agent already leads with the agent — the heading
    // says it once, as the row, the picker and the tab do.
    {
      name: "a content-off turn of a named agent",
      doc: { agent: "director", message_count: 0, event_count: 6, turn: 3 },
      option: `● succeeded t3 director · ${NO_CONTENT} · 1 step · 0ms`,
      row: `t3 director · ${NO_CONTENT}`,
      heading: `director · ${NO_CONTENT}`,
      tab: `director · ${NO_CONTENT} · succeeded · weft studio`,
      full: `director · ${NO_CONTENT}`,
    },
  ]
  for (const c of shapes)
    it(`${c.name}: the panel's picker and row, the run page's heading and tab`, async () => {
      expect(CUT).toBe("How many units of SKU A-17 are in stock…")
      const fx: Fixture = { doc: c.doc, events: events() }
      serve(fx, false)
      const el = await mount(ATTRS) // the 520 px float: the picker
      await vi.waitFor(
        () => expect($(el, ".weft-turn-pick option")).toBeTruthy(),
        { timeout: 5_000 }
      )
      const opt = $(el, ".weft-turn-pick option")!
      expect(opt.textContent).toBe(c.option)
      expect(opt.getAttribute("title")).toBe(`${RUN}\n${c.full}`)
      const row = $(el, ".weft-turn .weft-ttl")!
      expect(row.textContent).toBe(c.row)
      expect(row.getAttribute("title")).toBe(`${RUN}\n${c.full}`)
      expect($(el, ".weft-turn")!.getAttribute("data-weft-run")).toBe(RUN)
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
        expect(document.querySelector("[data-run-title]")).toBeTruthy()
      )
      const h1 = document.querySelector("[data-run-title]")!
      expect(h1.textContent).toBe(c.heading)
      expect(h1.querySelector("[data-title-kind]")!.getAttribute("title")).toBe(
        c.full
      )
      expect(document.querySelector("[data-run-id]")!.textContent).toBe(RUN)
      await waitFor(() => expect(document.title).toBe(c.tab))
    })
})

// A thread resume on both surfaces (U6-fix): t3 parked, t4 carries no
// prompt of its own — turnTitle's "resume of t3 · 1 decided call" on the
// panel's row and picker and on Studio's session page alike; an
// experiment of t3 in the picker in its card's order, the engine word
// and the duration after the steps.
describe("a resume and an experiment on both surfaces (U6-fix)", () => {
  const PROMPT = "Book the 9:40 to Lyon"
  const t3 = runRow({
    id: "s_01-t3",
    turn: 3,
    pending: 1,
    prompt_preview: PROMPT,
    started: AT(-60_000),
    finished: AT(-59_000),
    last_seen: AT(-59_000),
  })
  const t4 = runRow({
    id: "s_01-t4",
    turn: 4,
    prompt_preview: PROMPT,
    started: T0,
    finished: AT(811),
    last_seen: AT(811),
  })
  const x1 = runRow({
    id: "pg_x1",
    playground: true,
    session_id: "",
    turn: 0,
    forked_from: "s_01-t3#0",
    status: "failed",
    model: { provider: "weft/runtime", name: "scripted" },
    usage: { input_tokens: 0, output_tokens: 0 },
    started: AT(60_000),
    finished: AT(62_500),
    last_seen: AT(62_500),
  })
  const RESUME = "resume of t3 · 1 decided call"

  it("the panel's row and picker say what Studio's session page says", async () => {
    const fake = new FakeStudio()
      .on("GET meta", { ...META, capabilities: ["ingest", "playground"] })
      .on("GET sessions", { total: 1, sessions: [SESSION], next_before: null })
      .on("GET runs", { total: 3, runs: [x1, t4, t3], next_before: null })
    for (const r of [t3, t4, x1])
      fake
        .on(`GET runs/${r.id}`, { ...r, children: [], holes: [] })
        .on(`GET runs/${r.id}/events`, pagedEvents(events(), { done: true }))
        .on(
          `GET runs/${r.id}/transcript`,
          transcript([user("where is 42?")], [assistant("It shipped.")])
        )
        .on(`GET runs/${r.id}/spans`, { spans: [] })
        .on(`GET runs/${r.id}/requests`, pagedRequests(OK_REQUESTS()))
        .on(`GET runs/${r.id}/tools`, golden<object>("tools-ok"))
    fake.install()
    const el = await mount(ATTRS) // the 520 px float: the picker
    await vi.waitFor(
      () => expect(all(el, ".weft-turn-pick option")).toHaveLength(3),
      { timeout: 5_000 }
    )
    const opts = Object.fromEntries(
      all(el, ".weft-turn-pick option").map((o) => [
        o.getAttribute("value"),
        o.textContent,
      ])
    )
    expect(opts).toEqual({
      "s_01-t4": `● succeeded t4 ${RESUME} · 1 step · 811ms`,
      // t4 resumed t3 (U1): its word is the decided park's.
      "s_01-t3": `● parked → t4 t3 ${PROMPT} · 1 step · 1.0s`,
      pg_x1: "↳ replay of t3 · scripted · ● failed · 1 step · 2.5s",
    })
    const rows = Object.fromEntries(
      all(el, ".weft-turn").map((r) => [
        r.getAttribute("data-weft-run"),
        r.querySelector(".weft-ttl")!.textContent,
      ])
    )
    expect(rows["s_01-t4"]).toBe(`t4 ${RESUME}`)
    expect(rows["s_01-t3"]).toBe(`t3 ${PROMPT}`)
    el.remove()
    await settle()
    cleanup()
    // Studio's session page: the turns oldest first, turnTitle over the
    // turn above.
    const { container } = await renderWithRouter(
      createElement(SessionTurns, {
        turns: [t3, t4],
        experiments: new Map([[t3.id, [x1]]]),
      })
    )
    const heads = Object.fromEntries(
      [...container.querySelectorAll("[data-turn]")].map((tr) => [
        tr.getAttribute("data-turn"),
        tr.querySelector("[data-run-link]")!.textContent,
      ])
    )
    expect(heads["s_01-t4"]).toBe(`t4 ${RESUME}`)
    expect(heads["s_01-t3"]).toBe(`t3 ${PROMPT}`)
  })

  // U7 (c1): turn numbers can have gaps — a queued turn's number is
  // taken at Send and dropped by ClearQueue — so t5 resumes t3 with no
  // t4 loaded: the panel takes the highest loaded turn below, as
  // Studio's session page takes the turn above.
  it("a gap in the turn numbers: t5 resumes t3 on both surfaces", async () => {
    const t5 = { ...t4, id: "s_01-t5", turn: 5 }
    const fake = new FakeStudio()
      .on("GET meta", { ...META, capabilities: ["ingest", "playground"] })
      .on("GET sessions", { total: 1, sessions: [SESSION], next_before: null })
      .on("GET runs", { total: 2, runs: [t5, t3], next_before: null })
    for (const r of [t3, t5])
      fake
        .on(`GET runs/${r.id}`, { ...r, children: [], holes: [] })
        .on(`GET runs/${r.id}/events`, pagedEvents(events(), { done: true }))
        .on(
          `GET runs/${r.id}/transcript`,
          transcript([user("where is 42?")], [assistant("It shipped.")])
        )
        .on(`GET runs/${r.id}/spans`, { spans: [] })
        .on(`GET runs/${r.id}/requests`, pagedRequests(OK_REQUESTS()))
        .on(`GET runs/${r.id}/tools`, golden<object>("tools-ok"))
    fake.install()
    const el = await mount(ATTRS)
    await vi.waitFor(() => expect(all(el, ".weft-turn")).toHaveLength(2), {
      timeout: 5_000,
    })
    const rows = Object.fromEntries(
      all(el, ".weft-turn").map((r) => [
        r.getAttribute("data-weft-run"),
        r.querySelector(".weft-ttl")!.textContent,
      ])
    )
    expect(rows["s_01-t5"]).toBe(`t5 ${RESUME}`)
    el.remove()
    await settle()
    cleanup()
    const { container } = await renderWithRouter(
      createElement(SessionTurns, { turns: [t3, t5], experiments: new Map() })
    )
    const head = container.querySelector(
      '[data-turn="s_01-t5"] [data-run-link]'
    )
    expect(head!.textContent).toBe(`t5 ${RESUME}`)
  })
})

// A failed run's kind and its problems (U4): the Done line's seeded
// shapes show the same words on both surfaces — the panel's turn chips
// and the run page's header kind chip and stats strip, lib/error-kind.ts's
// table — and agentic.chaos's strip names its four failure shapes.
describe("error kinds and problems on both surfaces (U4)", () => {
  const shapes: {
    name: string
    doc: Partial<RunDoc>
    kind?: string
    glyphs: string
  }[] = [
    { name: "mw.retry", doc: { attempts_retried: 2 }, glyphs: "↻2" },
    {
      name: "rw.flaky-infra",
      doc: { attempts_retried: 1, fallbacks: 1 },
      glyphs: "↻1 ⇄",
    },
    { name: "appr.deny", doc: { denied: 1 }, glyphs: "✋" },
    {
      name: "opt.max-steps",
      doc: {
        status: "failed",
        error_type: "max_steps",
        stopped_by: "max_steps",
        err: "weft: run failed at step 1: weft: max steps reached",
      },
      kind: "[max_steps]",
      glyphs: "",
    },
    {
      name: "agentic.chaos",
      doc: { attempts_retried: 2, fallbacks: 1, tool_errors: 2, denied: 1 },
      glyphs: "↻2 ⇄ ✗2 ✋",
    },
  ]
  for (const c of shapes)
    it(`${c.name}: the panel's chips are the run page's chip and strip`, async () => {
      const fx: Fixture = {
        doc: c.doc,
        events: events({ finish: c.doc.status !== "failed" }),
      }
      serve(fx, false)
      const el = await mount(ATTRS)
      await vi.waitFor(() => expect($(el, ".weft-turn")).toBeTruthy(), {
        timeout: 5_000,
      })
      await vi.waitFor(
        () =>
          expect(
            $(el, '.weft-turn [data-weft-chip="problems"]') ??
              $(el, '.weft-turn [data-weft-chip="error-type"]')
          ).toBeTruthy(),
        { timeout: 5_000 }
      )
      const probs = $(el, '.weft-turn [data-weft-chip="problems"]')
      const kind = $(el, '.weft-turn [data-weft-chip="error-type"]')
      const panel = {
        glyphs: probs?.textContent ?? "",
        labels: probs?.getAttribute("title") ?? "",
        kind: kind?.textContent ?? undefined,
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
      serve(fx, true)
      renderApp(`/runs/${RUN}?view=story`)
      await waitFor(() =>
        expect(document.querySelector("[data-header-usage]")).toBeTruthy()
      )
      const strip = [...document.querySelectorAll("[data-strip]")]
      const studio = {
        glyphs: strip.map((s) => s.textContent.split(" ")[0]).join(" "),
        labels: strip.map((s) => s.getAttribute("title")).join("; "),
        kind:
          document.querySelector("[data-error-type]")?.textContent ?? undefined,
      }
      expect(panel.glyphs).toBe(c.glyphs)
      expect(panel.kind).toBe(c.kind)
      expect(studio).toEqual(panel)
      if (c.name === "agentic.chaos")
        expect(strip.map((s) => s.getAttribute("data-strip"))).toEqual([
          "retried",
          "fallback",
          "tool_errors",
          "denied",
        ])
    })
})

// One status vocabulary (U1; UX-A-06, UX-B-04, UX-D-16, UX-F-07,
// UX-S3-03): a parked t1 its t2 resumed, a cancelled t3 and an open
// park t4 read the same words, in the same colour role, on the panel's
// turn list and picker and on Studio's session page; the panel's
// "parked" filter returns the open park only.
describe("one status vocabulary on both surfaces (U1)", () => {
  const turn = (n: number, o: Partial<RunRow>) =>
    runRow({
      id: `s_01-t${n}`,
      turn: n,
      started: AT(n * 10_000),
      finished: AT(n * 10_000 + 1_000),
      last_seen: AT(n * 10_000 + 1_000),
      ...o,
    })
  const t1 = turn(1, { pending: 1 })
  const t2 = turn(2, {})
  const t3 = turn(3, {
    status: "failed",
    error_type: "cancelled",
    cancel_cause: "studio.cancel",
    stopped_by: "canceled",
    err: "weft: run failed at step 0: context canceled",
  })
  const t4 = turn(4, { pending: 2 })
  const WORDS = {
    "s_01-t1": "parked → t2",
    "s_01-t2": "succeeded",
    "s_01-t3": "cancelled",
    "s_01-t4": "parked",
  }
  const KEYS = {
    "s_01-t1": "resumed",
    "s_01-t2": "succeeded",
    "s_01-t3": "cancelled",
    "s_01-t4": "parked",
  } as const

  it("the six words and their roles are pinned", () => {
    expect(
      Object.fromEntries(
        Object.entries(STATUS).map(([k, w]) => [k, [w.word, w.role]])
      )
    ).toEqual({
      running: ["running", "running"],
      succeeded: ["succeeded", "succeeded"],
      failed: ["failed", "failed"],
      interrupted: ["interrupted", "interrupted"],
      parked: ["parked", "parked"],
      cancelled: ["cancelled", "cancelled"],
      resumed: ["parked →", "resumed"],
    })
    // The transition: t1 reads parked until t2 exists.
    expect(statusOf(t1, [t1]).word).toBe("parked")
    // "awaiting a decision" only against the session's whole turn list.
    expect(statusOf(t1, [t1], true).detail).toBe("1 call awaiting a decision")
    expect(statusOf(t1, [t1]).detail).toBe(
      "1 call pending · no resume recorded here"
    )
    expect(statusOf(t1, [t1, t2])).toMatchObject({
      key: "resumed",
      word: "parked → t2",
      resumedBy: "s_01-t2",
      detail: "1 call · next turn t2 (decided or dropped)",
    })
    // "decided · resumed" only on the stored link (W7-prep).
    expect(statusOf({ ...t1, resumed_by: "s_01-t2" }, [t1, t2]).detail).toBe(
      "1 call decided · resumed in t2"
    )
    expect(statusOf(t3).hint).toContain("cancelled · studio.cancel")
    // A stored resume link with no turn to name (U13.1-fix): the
    // resumer's id cut to 8 characters, whole in the hint — one word
    // for the panel's turn chip and Studio's runs list alike.
    const long = "3f9a1c2b7d4e5f60718293a4b5c6d7e8"
    const gen = statusOf(runRow({ id: "r_gen", pending: 1, resumed_by: long }))
    expect(gen.word).toBe("parked → 3f9a1c2b…")
    expect(gen.hint).toContain(`(${long})`)
    // The stored status is the contract's: the row still says failed.
    expect(t3.status).toBe("failed")
  })

  it("the panel's turn list, picker and filter say what Studio's session page says", async () => {
    const fake = new FakeStudio()
      .on("GET meta", { ...META, capabilities: ["ingest"] })
      .on("GET sessions", { total: 1, sessions: [SESSION], next_before: null })
      .on("GET runs", { total: 4, runs: [t4, t3, t2, t1], next_before: null })
    for (const r of [t1, t2, t3, t4])
      fake
        .on(`GET runs/${r.id}`, { ...r, children: [], holes: [] })
        .on(`GET runs/${r.id}/events`, pagedEvents(events(), { done: true }))
        .on(
          `GET runs/${r.id}/transcript`,
          transcript([user("where is 42?")], [assistant("It shipped.")])
        )
        .on(`GET runs/${r.id}/spans`, { spans: [] })
        .on(`GET runs/${r.id}/requests`, pagedRequests(OK_REQUESTS()))
        .on(`GET runs/${r.id}/tools`, golden<object>("tools-ok"))
    fake.install()
    const el = await mount(ATTRS)
    await vi.waitFor(() => expect(all(el, ".weft-turn")).toHaveLength(4), {
      timeout: 5_000,
    })
    const chips = Object.fromEntries(
      all(el, ".weft-turn").map((r) => [
        r.getAttribute("data-weft-run"),
        r.querySelector(".weft-row1 > .weft-chip")!,
      ])
    )
    for (const [id, word] of Object.entries(WORDS)) {
      const c = chips[id]
      expect(c.textContent, id).toBe(word)
      const key = KEYS[id as keyof typeof KEYS]
      expect(c.classList.contains(`weft-${key}`), id).toBe(true)
      // The chip's colour is its role's panel token (UX-F-07).
      expect(PANEL_CSS).toContain(
        `.weft-chip.weft-${key} { color: var(--weft-${ROLE[STATUS[key].role]}); border: 1px solid; }`
      )
    }
    // The panel's list is session-scoped: an open park's resume was
    // looked for, so its hint may say awaiting (U1-fix) — Studio's
    // session page says the same below.
    expect(chips["s_01-t4"].getAttribute("title")).toBe(
      "2 calls awaiting a decision"
    )
    // The status word says cancelled: no [cancelled] kind chip beside it.
    expect(
      $(el, '[data-weft-run="s_01-t3"] [data-weft-chip="error-type"]')
    ).toBeNull()
    // The picker leads with the same word.
    for (const o of all(el, ".weft-turn-pick option"))
      expect(o.textContent).toContain(
        `● ${WORDS[o.getAttribute("value") as keyof typeof WORDS]} `
      )
    // The "parked" filter: open parks only — t1 was decided.
    const st = $(el, ".weft-tq-status") as HTMLSelectElement
    st.value = "parked"
    st.dispatchEvent(new Event("change", { bubbles: true }))
    await settle()
    expect(
      all(el, ".weft-turn").map((r) => r.getAttribute("data-weft-run"))
    ).toEqual(["s_01-t4"])
    st.value = "cancelled"
    st.dispatchEvent(new Event("change", { bubbles: true }))
    await settle()
    expect(
      all(el, ".weft-turn").map((r) => r.getAttribute("data-weft-run"))
    ).toEqual(["s_01-t3"])
    // The decided park has its own pick (U1-fix).
    const pick = all(el, ".weft-tq-status option").find(
      (o) => (o as HTMLOptionElement).value === "resumed"
    )
    expect(pick?.textContent).toBe("parked → (decided)")
    st.value = "resumed"
    st.dispatchEvent(new Event("change", { bubbles: true }))
    await settle()
    expect(
      all(el, ".weft-turn").map((r) => r.getAttribute("data-weft-run"))
    ).toEqual(["s_01-t1"])
    el.remove()
    await settle()
    cleanup()
    // Studio's session page: the same words, the same role, t1's word a
    // link to t2.
    const { container } = await renderWithRouter(
      createElement(SessionTurns, {
        turns: [t1, t2, t3, t4],
        experiments: new Map(),
      })
    )
    expect(
      container
        .querySelector('[data-turn="s_01-t4"] [data-status]')!
        .getAttribute("title")
    ).toBe("2 calls awaiting a decision")
    for (const [id, word] of Object.entries(WORDS)) {
      const c = container.querySelector(`[data-turn="${id}"] [data-status]`)!
      expect(c.textContent, id).toBe(word)
      const key = KEYS[id as keyof typeof KEYS]
      expect(c.getAttribute("data-status"), id).toBe(key)
      expect(c.className, id).toContain(statusClass(STATUS[key].role).text)
    }
    expect(
      container
        .querySelector('[data-turn="s_01-t1"] [data-resumed-by]')!
        .getAttribute("data-resumed-by")
    ).toBe("s_01-t2")
  })

  it("the runs list and the live filter never claim awaiting: the resume may be off the page (U1-fix)", async () => {
    // A runs page that holds t4 alone (a filter, a sort): no claim.
    const { container } = await renderWithRouter(
      createElement(RunsTable, { runs: [t4] })
    )
    const c = container.querySelector("[data-status]")!
    expect(c.textContent).toBe("parked")
    expect(c.getAttribute("title")).toBe(
      "2 calls pending · no resume recorded here"
    )
    cleanup()
    // A page that holds t1 and t2: t1's resume is among them.
    const two = await renderWithRouter(
      createElement(RunsTable, { runs: [t2, t1] })
    )
    expect(
      two.container
        .querySelector('[data-status="resumed"] [data-resumed-by]')!
        .getAttribute("data-resumed-by")
    ).toBe("s_01-t2")
    // The live filter matches on the key: "parked" is not t1 once t2 is
    // held, "resumed" is.
    expect(matchesFilter(t1, "parked", [t1, t2])).toBe(false)
    expect(matchesFilter(t1, "resumed", [t1, t2])).toBe(true)
    expect(matchesFilter(t1, "parked", [t1])).toBe(true)
    expect(matchesFilter(t3, "failed")).toBe(false)
    expect(matchesFilter(t3, "cancelled")).toBe(true)
  })

  it("the panel hands a decided park off to its resume turn (U1-fix)", async () => {
    const fake = new FakeStudio()
      .on("GET meta", { ...META, capabilities: ["ingest"] })
      .on("GET sessions", { total: 1, sessions: [SESSION], next_before: null })
      .on("GET runs", { total: 2, runs: [t2, t1], next_before: null })
    for (const r of [t1, t2])
      fake
        .on(`GET runs/${r.id}`, { ...r, children: [], holes: [] })
        .on(`GET runs/${r.id}/events`, pagedEvents(events(), { done: true }))
        .on(
          `GET runs/${r.id}/transcript`,
          transcript([user("where is 42?")], [assistant("It shipped.")])
        )
        .on(`GET runs/${r.id}/spans`, { spans: [] })
        .on(`GET runs/${r.id}/requests`, pagedRequests(OK_REQUESTS()))
        .on(`GET runs/${r.id}/tools`, golden<object>("tools-ok"))
    fake.install()
    const el = await mount(ATTRS)
    // The decided park's word is the hand-off (Studio links it).
    const go = await vi.waitFor(
      () => {
        const b = $(el, '[data-weft-run="s_01-t1"] [data-weft-resume]')
        expect(b).toBeTruthy()
        return b!
      },
      { timeout: 5_000 }
    )
    expect(go.getAttribute("data-weft-resume")).toBe("s_01-t2")
    expect(go.textContent).toBe("parked → t2")
    // An open park's word is no hand-off.
    expect($(el, '[data-weft-run="s_01-t2"] [data-weft-resume]')).toBeNull()
    click(go)
    await vi.waitFor(() =>
      expect(
        $(el, '.weft-turn[aria-current="true"]')?.getAttribute("data-weft-run")
      ).toBe("s_01-t2")
    )
    el.remove()
    await settle()
  })

  describe("the run header's detail and link (U1-fix)", () => {
    const header = async (doc: Partial<RunRow>, session: object | null) => {
      cleanup()
      document.body.innerHTML = ""
      stubBrowser()
      vi.stubGlobal("scrollTo", vi.fn())
      StudioEventSource.reset()
      vi.stubGlobal("EventSource", StudioEventSource)
      setStudioToken("")
      const fake = serve({ doc, events: events() }, true)
      if (session) fake.on("GET sessions/s_01", session)
      renderApp(`/runs/${RUN}?view=story`)
      await waitFor(() =>
        expect(document.querySelector("[data-header-usage]")).toBeTruthy()
      )
      return () =>
        document.querySelector(
          '[data-status="parked"], [data-status="resumed"]'
        )!
    }

    it("with the session loaded and no later turn: awaiting a decision", async () => {
      const chip = await header(
        { pending: 1 },
        { ...SESSION, runs: [runRow({ pending: 1 })] }
      )
      await waitFor(() =>
        expect(chip().textContent).toBe("parked· 1 call awaiting a decision")
      )
    })

    it("with a later turn loaded: parked → t2, a link to it", async () => {
      const chip = await header(
        { pending: 1 },
        {
          ...SESSION,
          runs: [runRow({ pending: 1 }), runRow({ id: "s_01-t2", turn: 2 })],
        }
      )
      await waitFor(() =>
        expect(
          chip()
            .querySelector("[data-resumed-by]")
            ?.getAttribute("data-resumed-by")
        ).toBe("s_01-t2")
      )
      expect(chip().textContent).toBe(
        "parked → t2· 1 call · next turn t2 (decided or dropped)"
      )
    })

    it("the session refused: pending, no claim", async () => {
      const chip = await header({ pending: 1 }, () =>
        apiError(500, "internal", "boom")
      )
      await settle()
      expect(chip().textContent).toBe(
        "parked· 1 call pending · no resume recorded here"
      )
    })

    it("a plain Generate (rw.support-desk's shape): never awaiting", async () => {
      const chip = await header(
        { pending: 1, session_id: "", public_id: "", turn: 0 },
        null
      )
      await settle()
      expect(chip().textContent).toBe(
        "parked· 1 call pending · a resume, if any, is not linked yet"
      )
    })
  })
})

// U8.3 (UX-F-18, UX-D-34): one row model, two renderers. The director
// run (rw.deep-delegation, test/director-fixture.ts) on Studio's run
// page and in the panel's Timeline: the same rows — key, label (with
// its gist: the label cell's "label — sub" title), kind and tone — the
// child run one collapsed row on both, the panel never deeper.
describe("the run tree on both surfaces (U8.3)", () => {
  const rOK = golden<{ runs: RunRow[] }>("runs").runs.find(
    (r) => r.id === "r_ok"
  )!
  const child: RunRow = {
    ...rOK,
    id: OPS,
    agent: "ops-manager",
    parent_run_id: DIR,
    parent_call_id: "call_1",
  }
  const row: RunRow = { ...rOK, id: DIR, agent: "director", steps: 2 }
  const doc = {
    ...row,
    trace_id: "t1",
    children: [child],
    holes: [],
  } as unknown as RunDoc
  // The run's own spans: the panel reads /spans without ?tree=1.
  const own = DIR_SPANS.filter((s) => s.span_id.startsWith("d_")).map((s) => ({
    ...s,
    run_id: DIR,
  }))
  const serveDir = (studioMeta: boolean, spans = own) =>
    new FakeStudio()
      .on(
        "GET meta",
        studioMeta ? golden<Record<string, unknown>>("meta") : META
      )
      .on("GET runs", { total: 1, runs: [row], next_before: null })
      .on(`GET runs/${DIR}`, doc)
      .on(
        `GET runs/${DIR}/events`,
        pagedEvents(
          DIR_EVENTS.map((event, pos) => ({ pos, time: rOK.started, event })),
          { done: true }
        )
      )
      .on(`GET runs/${DIR}/transcript`, { messages: [] })
      .on(`GET runs/${DIR}/spans`, { spans })
      .install()
  const kinds = (n: HTMLElement) => [n.dataset.kind, n.dataset.tone]

  it("the director run: the same row keys, labels and kind classes (one level deep on the panel)", async () => {
    serveDir(false)
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-open": "true",
    })
    await vi.waitFor(() => expect(all(el, ".weft-turn").length).toBe(1), {
      timeout: 5_000,
    })
    click(all(el, ".weft-turn")[0])
    await vi.waitFor(() => expect($(el, "#weft-tab-timeline")).toBeTruthy(), {
      timeout: 5_000,
    })
    click($(el, "#weft-tab-timeline"))
    await vi.waitFor(
      () =>
        expect(
          $(el, `#weft-tp-timeline [data-key="${CSS.escape(`tl:r:${OPS}`)}"]`)
        ).toBeTruthy(),
      { timeout: 5_000 }
    )
    const panel = (all(el, "#weft-tp-timeline .weft-tl") as HTMLElement[]).map(
      (n) => [
        n.dataset.key!.slice(3), // tl:<row key>
        n.querySelector(".weft-wf-name")!.getAttribute("title"),
        ...kinds(n),
      ]
    )
    const handoff = $(el, `#weft-tp-timeline [data-weft-handoff]`)!
    el.remove()
    await settle()
    cleanup()
    document.body.innerHTML = ""

    stubBrowser()
    vi.stubGlobal("scrollTo", vi.fn())
    StudioEventSource.reset()
    vi.stubGlobal("EventSource", StudioEventSource)
    setStudioToken("")
    serveDir(true)
    renderApp(`/runs/${DIR}`)
    await waitFor(() =>
      expect(
        document.querySelector(`[data-span="${CSS.escape(`r:${OPS}`)}"]`)
      ).toBeTruthy()
    )
    const studio = Array.from(
      document.querySelectorAll<HTMLElement>("[data-span]")
    ).map((n) => [
      n.dataset.span,
      n.querySelector("div[title]")!.getAttribute("title"),
      ...kinds(n),
    ])

    expect(panel).toEqual(studio)
    expect(panel.map((r) => r[0])).toEqual([
      `r:${DIR}`,
      "s0",
      "s0:m",
      "c:0:call_1",
      `r:${OPS}`,
      "t:d_http",
      "s1",
      "s1:m",
    ])
    // The label rule, the kinds (a child run a subagent row), the tones.
    expect(panel.map((r) => r[1]!.split(" — ")[0])).toEqual([
      "director",
      "step 0",
      "model · script",
      "ops_manager",
      "ops-manager", // no child span or fold read: its agent, from the doc
      "http.get",
      "step 1",
      "model · script",
    ])
    expect(panel.map((r) => r[2])).toEqual([
      "run",
      "step",
      "model",
      "tool",
      "subagent",
      "span",
      "step",
      "model",
    ])
    // One level deep: the child is one row, its ⤢ hands off to Studio's
    // run page at that row.
    expect(panel.some((r) => r[0]!.includes(`${OPS}:`))).toBe(false)
    expect(handoff.getAttribute("data-weft-handoff")).toBe(OPS)
    expect(new URL(handoff.getAttribute("href")!).searchParams.get("sel")).toBe(
      `r:${OPS}`
    )
  })
})

// U8.3-fix (P6): one label rule for a child row. The panel reads its
// own spans (no ?tree=1): the child row is the fold's, named by the run
// document's agent. Studio, fed the ?tree=1 shape, names it from the
// child's invoke_agent span. The same key and the same label on both.
describe("the child row's label on both surfaces, whatever spans each read (U8.3-fix)", () => {
  const rOK = golden<{ runs: RunRow[] }>("runs").runs.find(
    (r) => r.id === "r_ok"
  )!
  const child: RunRow = {
    ...rOK,
    id: OPS,
    agent: "ops-manager",
    parent_run_id: DIR,
    parent_call_id: "call_1",
  }
  const row: RunRow = { ...rOK, id: DIR, agent: "director", steps: 2 }
  const doc = {
    ...row,
    trace_id: "t1",
    children: [child],
    holes: [],
  } as unknown as RunDoc
  const withRun = (sp: typeof DIR_SPANS) =>
    sp.map((s) => ({ ...s, run_id: String(s.attrs["weft.run.id"] ?? DIR) }))
  const serveTree = (meta: Record<string, unknown>, spans: typeof DIR_SPANS) =>
    new FakeStudio()
      .on("GET meta", meta)
      .on("GET runs", { total: 1, runs: [row], next_before: null })
      .on(`GET runs/${DIR}`, doc)
      .on(
        `GET runs/${DIR}/events`,
        pagedEvents(
          DIR_EVENTS.map((event, pos) => ({ pos, time: rOK.started, event })),
          { done: true }
        )
      )
      .on(`GET runs/${DIR}/transcript`, { messages: [] })
      .on(`GET runs/${DIR}/spans`, { spans })
      .install()

  it("the panel's fold fallback and Studio's ?tree=1 spans: one key, one label", async () => {
    serveTree(
      META,
      withRun(DIR_SPANS.filter((s) => s.span_id.startsWith("d_")))
    )
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-open": "true",
    })
    await vi.waitFor(() => expect(all(el, ".weft-turn").length).toBe(1), {
      timeout: 5_000,
    })
    click(all(el, ".weft-turn")[0])
    await vi.waitFor(() => expect($(el, "#weft-tab-timeline")).toBeTruthy(), {
      timeout: 5_000,
    })
    click($(el, "#weft-tab-timeline"))
    const at = `#weft-tp-timeline [data-key="${CSS.escape(`tl:r:${OPS}`)}"]`
    await vi.waitFor(() => expect($(el, at)).toBeTruthy(), { timeout: 5_000 })
    const panel = $(el, `${at} .weft-tl-l`)!.textContent
    el.remove()
    await settle()
    cleanup()
    document.body.innerHTML = ""

    stubBrowser()
    vi.stubGlobal("scrollTo", vi.fn())
    StudioEventSource.reset()
    vi.stubGlobal("EventSource", StudioEventSource)
    setStudioToken("")
    serveTree(
      {
        ...golden<Record<string, unknown>>("meta"),
        capabilities: [
          ...golden<{ capabilities: string[] }>("meta").capabilities,
          "spans_tree",
        ],
      },
      withRun(DIR_SPANS)
    )
    renderApp(`/runs/${DIR}`)
    const sel = `[data-span="${CSS.escape(`r:${OPS}`)}"]`
    await waitFor(() => expect(document.querySelector(sel)).toBeTruthy())
    const studio = document
      .querySelector(`${sel} div[title]`)!
      .getAttribute("title")!
      .split(" — ")[0]
    expect(panel).toBe("ops-manager")
    expect(studio).toBe(panel)
  })
})

// U10.2 (UX-D-12, audit §6.3): the panel's Story step is Studio's
// answer-first drawer at panel width — lib/summarize.ts's verdict and
// its sent / replied / tools ran / then lines, the same words. On
// opt.usage-limit (test/usage-limit-fixture.ts) both land on step 2,
// the step the run failed at.
describe("the step's answer on both surfaces (U10.2)", () => {
  const doc = ulDoc()
  const row = (({ children: _c, holes: _h, compactions: _x, ...r }) => r)(
    doc
  ) as unknown as RunRow
  const serveUL = (meta: Record<string, unknown>) =>
    new FakeStudio()
      .on("GET meta", meta)
      .on("GET runs", { total: 1, runs: [row], next_before: null })
      .on(`GET runs/${UL}`, doc)
      .on(
        `GET runs/${UL}/events`,
        pagedEvents(
          UL_EVENTS.map((event, pos) => ({ pos, time: doc.started, event })),
          { done: true }
        )
      )
      .on(`GET runs/${UL}/transcript`, transcriptOf(UL_BODIES))
      .on(`GET runs/${UL}/spans`, { spans: UL_SPANS })
      .on(`GET runs/${UL}/requests`, pagedRequests(UL_REQUESTS))
      .on(`GET runs/${UL}/logs`, UL_LOGS)
      .on("GET runtimes", { runtimes: [] })
      .install()
  const lines = (root: ParentNode) =>
    Array.from(root.querySelectorAll<HTMLElement>("[data-answer-line]")).map(
      (n) => [n.dataset.answerLine, n.firstChild?.textContent]
    )

  it("opt.usage-limit's step 2: the panel's verdict and lines are Studio's drawer's", async () => {
    serveUL({
      ...META,
      capabilities: [...META.capabilities, "requests", "logs"],
    })
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-open": "true",
    })
    await vi.waitFor(() => expect(all(el, ".weft-turn").length).toBe(1), {
      timeout: 5_000,
    })
    click(all(el, ".weft-turn")[0])
    // No step picked: the answer sits on the step the run failed at.
    await vi.waitFor(
      () =>
        expect(
          $(el, '[data-weft-step="2"] [data-answer-line="sent"]')?.textContent
        ).toContain("messages"),
      { timeout: 5_000 }
    )
    expect(all(el, "[data-verdict]")).toHaveLength(1)
    const step = $(el, '.weft-step[data-weft-step="2"]')!
    const verdict = step.querySelector("[data-verdict]")!
    const panel = {
      verdict: verdict.textContent,
      bad: verdict.hasAttribute("data-bad"),
      lines: lines(step),
    }
    // Logs (the step head's 📄 ⤢, U11.2's logsLink) and Raw OTel (the answer's)
    // hand off to the run page at the step's row — no token in the URL.
    const go = Array.from(step.querySelectorAll<HTMLAnchorElement>("a")).filter(
      (a) => a.textContent.endsWith("⤢") && a.href.includes("tab=")
    )
    expect(go.map((a) => a.textContent)).toEqual(["📄 ⤢", "Raw OTel ⤢"])
    for (const [a, tab] of [
      [go[0], "logs"],
      [go[1], "raw"],
    ] as const) {
      const u = new URL(a.href)
      expect(u.pathname).toBe(`/studio/runs/${UL}`)
      expect(u.searchParams.get("sel")).toBe("s2")
      expect(u.searchParams.get("tab")).toBe(tab)
      expect(a.href).not.toContain("token")
    }
    // Picking another step moves the answer there.
    click($(el, '.weft-step[data-weft-step="0"] .weft-step-h'))
    await vi.waitFor(
      () =>
        expect(
          $(el, '.weft-step[data-weft-step="0"] [data-verdict]')
        ).toBeTruthy(),
      { timeout: 5_000 }
    )
    expect(all(el, "[data-verdict]")).toHaveLength(1)
    el.remove()
    await settle()
    cleanup()
    document.body.innerHTML = ""

    stubBrowser()
    vi.stubGlobal("scrollTo", vi.fn())
    StudioEventSource.reset()
    vi.stubGlobal("EventSource", StudioEventSource)
    setStudioToken("")
    serveUL({
      ...golden<Record<string, unknown>>("meta"),
      capabilities: ["ingest", "requests", "playground", "diff", "logs"],
    })
    renderApp(`/runs/${UL}`)
    await waitFor(() =>
      expect(
        document.querySelector('[data-answer-line="sent"]')?.textContent
      ).toContain("messages")
    )
    const v = document.querySelector<HTMLElement>("[data-verdict]")!
    const studio = {
      verdict: v.textContent,
      bad: v.hasAttribute("data-bad"),
      lines: lines(document),
    }
    expect(panel.verdict).toContain("✗ usage limit reached")
    expect(panel).toEqual(studio)
  })
})

// P8-fix-a #1, #11, #12: with no step picked both surfaces land on the
// same step — where the run failed (core's real shapes, core/loop.go:
// a model error or a cancel during step N names step N with steps N;
// a cancel before step N began names N with no row for it, so the last
// drawn), else step 0 — and draw the same answer there. Each case is
// opt.usage-limit's bodies cut to what core writes for it.
describe("the landing step's answer on both surfaces (P8-fix-a)", () => {
  const P = "weft: run failed at step"
  const inv = (attrs: Record<string, unknown>, status: "ok" | "error") =>
    UL_SPANS.map((s) =>
      s.span_id === "u_inv"
        ? {
            ...s,
            status,
            attrs: (({
              "weft.run.stopped_by": _b,
              "error.type": _e,
              ...a
            }) => ({ ...a, ...attrs }))(s.attrs),
          }
        : s
    )
  // Steps 0–1 finished, step 2 begun: its chat span errored, no tool.
  const openTwo = (attrs: Record<string, unknown>) =>
    inv({ "weft.run.steps": 2, ...attrs }, "error")
      .filter((s) => s.span_id !== "u_t2")
      .map((s) =>
        s.span_id === "u_c2" ? { ...s, status: "error" as const } : s
      )
  const cases: {
    name: string
    doc: Partial<RunDoc>
    events: typeof UL_EVENTS
    spans: typeof UL_SPANS
    requests?: typeof UL_REQUESTS
    land: number
    bad: boolean
  }[] = [
    {
      name: "succeeded: step 0",
      doc: {
        status: "succeeded",
        err: "",
        stopped_by: "final_answer",
        error_type: "",
      },
      events: UL_EVENTS,
      spans: inv({ "weft.run.stopped_by": "final_answer" }, "ok"),
      land: 0,
      bad: false,
    },
    {
      name: "settling: step 0, the record's words",
      doc: {
        status: "succeeded",
        settling: true,
        err: "",
        stopped_by: "final_answer",
        error_type: "",
      },
      events: UL_EVENTS,
      spans: inv({ "weft.run.stopped_by": "final_answer" }, "ok"),
      land: 0,
      bad: false,
    },
    {
      name: "max_steps: its last step",
      doc: {
        err: `${P} 3: weft: run exceeded the maximum number of steps`,
        stopped_by: "max_steps",
        error_type: "max_steps",
      },
      events: UL_EVENTS,
      spans: inv(
        { "weft.run.stopped_by": "max_steps", "error.type": "max_steps" },
        "error"
      ),
      land: 2,
      bad: true,
    },
    {
      name: "a model error during step 2: step 2, never step 1",
      doc: {
        steps: 2,
        err: `${P} 2: model stream: boom`,
        stopped_by: undefined,
        error_type: "model",
      },
      events: UL_EVENTS.slice(0, 10),
      spans: openTwo({}),
      land: 2,
      bad: true,
    },
    {
      name: "a cancel during step 2's model call: step 2",
      doc: {
        steps: 2,
        err: `${P} 2: context canceled`,
        stopped_by: "canceled",
        error_type: "cancelled",
      },
      events: UL_EVENTS.slice(0, 10),
      spans: openTwo({
        "weft.run.stopped_by": "canceled",
        "error.type": "cancelled",
      }),
      land: 2,
      bad: true,
    },
    {
      name: "a cancel before step 2 began: the last drawn, step 1",
      doc: {
        steps: 2,
        err: `${P} 2: context canceled`,
        stopped_by: "canceled",
        error_type: "cancelled",
      },
      events: UL_EVENTS.slice(0, 9),
      spans: inv(
        {
          "weft.run.steps": 2,
          "weft.run.stopped_by": "canceled",
          "error.type": "cancelled",
        },
        "error"
      ).filter((s) => s.span_id !== "u_c2" && s.span_id !== "u_t2"),
      requests: { requests: UL_REQUESTS.requests.slice(0, 2) },
      land: 1,
      bad: true,
    },
  ]
  const lines = (root: ParentNode) =>
    Array.from(root.querySelectorAll<HTMLElement>("[data-answer-line]")).map(
      (n) => [n.dataset.answerLine, n.firstChild?.textContent]
    )
  for (const c of cases)
    it(c.name, async () => {
      const doc = ulDoc(c.doc)
      const row = (({ children: _c, holes: _h, compactions: _x, ...r }) => r)(
        doc
      ) as unknown as RunRow
      const serveCase = (meta: Record<string, unknown>) =>
        new FakeStudio()
          .on("GET meta", meta)
          .on("GET runs", { total: 1, runs: [row], next_before: null })
          .on(`GET runs/${UL}`, doc)
          .on(
            `GET runs/${UL}/events`,
            pagedEvents(
              c.events.map((event, pos) => ({ pos, time: doc.started, event })),
              { done: true }
            )
          )
          .on(`GET runs/${UL}/transcript`, transcriptOf(UL_BODIES))
          .on(`GET runs/${UL}/spans`, { spans: c.spans })
          .on(
            `GET runs/${UL}/requests`,
            pagedRequests(c.requests ?? UL_REQUESTS)
          )
          .on(`GET runs/${UL}/logs`, UL_LOGS)
          .on("GET runtimes", { runtimes: [] })
          .install()
      serveCase({
        ...META,
        capabilities: [...META.capabilities, "requests", "logs"],
      })
      const el = await mount({
        "data-endpoint": "http://studio.test/studio/",
        "data-open": "true",
      })
      await vi.waitFor(() => expect(all(el, ".weft-turn").length).toBe(1), {
        timeout: 5_000,
      })
      click(all(el, ".weft-turn")[0])
      await vi.waitFor(
        () =>
          expect($(el, '[data-answer-line="sent"]')?.textContent).toContain(
            "message"
          ),
        { timeout: 5_000 }
      )
      expect(all(el, "[data-verdict]")).toHaveLength(1)
      const step = $(el, "[data-verdict]")!.closest<HTMLElement>(".weft-step")!
      expect(step.getAttribute("data-weft-step")).toBe(String(c.land))
      const verdict = step.querySelector("[data-verdict]")!
      const panel = {
        verdict: verdict.textContent,
        bad: verdict.hasAttribute("data-bad"),
        lines: lines(step),
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
      serveCase({
        ...golden<Record<string, unknown>>("meta"),
        capabilities: ["ingest", "requests", "playground", "diff", "logs"],
      })
      renderApp(`/runs/${UL}`)
      await waitFor(() =>
        expect(
          document.querySelector('[data-answer-line="sent"]')?.textContent
        ).toContain("message")
      )
      const v = document.querySelector<HTMLElement>("[data-verdict]")!
      const studio = {
        verdict: v.textContent,
        bad: v.hasAttribute("data-bad"),
        lines: lines(document),
      }
      expect(panel.bad).toBe(c.bad)
      expect(panel).toEqual(studio)
      if (c.doc.stopped_by !== "canceled") return
      // P8-fix-b #5: the header's chip, its link and the story's end
      // card name the landing step too — a cancel during step 2 reads
      // step 2, never step 1.
      const chip = document.querySelector<HTMLAnchorElement>(
        'a[data-stopped-by="canceled"]'
      )!
      expect(chip.textContent).toBe(`· step ${c.land}`)
      expect(
        new URL(chip.getAttribute("href")!, "http://x").searchParams.get("step")
      ).toBe(String(c.land))
      cleanup()
      renderApp(`/runs/${UL}?view=story`)
      await waitFor(() =>
        expect(document.querySelector("[data-story-end]")?.textContent).toBe(
          `■ cancelled at step ${c.land}`
        )
      )
    })

  // P8-fix-a #5: the logs route refuses every panel token, so the step
  // head's 📄 ⤢ is drawn for no token or the dev token only.
  it("a panel token's Story draws no logs hand-off", async () => {
    const doc = ulDoc()
    const row = (({ children: _c, holes: _h, compactions: _x, ...r }) => r)(
      doc
    ) as unknown as RunRow
    new FakeStudio()
      .on("GET meta", {
        ...META,
        capabilities: [...META.capabilities, "requests", "logs"],
      })
      .on("GET runs", { total: 1, runs: [row], next_before: null })
      .on(`GET runs/${UL}`, doc)
      .on(
        `GET runs/${UL}/events`,
        pagedEvents(
          UL_EVENTS.map((event, pos) => ({ pos, time: doc.started, event })),
          { done: true }
        )
      )
      .on(`GET runs/${UL}/transcript`, transcriptOf(UL_BODIES))
      .on(`GET runs/${UL}/spans`, { spans: UL_SPANS })
      .on(`GET runs/${UL}/requests`, pagedRequests(UL_REQUESTS))
      .on("GET runtimes", { runtimes: [] })
      .install()
    const read = `weft_pt.${btoa(JSON.stringify({ public_id: "pub_x", scope: "read", exp: "2099-01-01T00:00:00Z" })).replace(/=+$/, "")}.sig`
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-open": "true",
      "data-token": read,
    })
    await vi.waitFor(() => expect(all(el, ".weft-turn").length).toBe(1), {
      timeout: 5_000,
    })
    click(all(el, ".weft-turn")[0])
    await vi.waitFor(() => expect($(el, "[data-verdict]")).toBeTruthy(), {
      timeout: 5_000,
    })
    expect(all(el, "[data-weft-logs]")).toHaveLength(0)
  })
})

// U25: the run cards' facts are one set of words on both surfaces — a
// recorded shape (studio/cards_test.go's golden) drawn by the panel's
// Story, then by the run page's story: the tool card's execution facts,
// a breakpoint park's word, a Subagent call's typed outcome, an
// attempt's cause.
describe("the run cards' facts on both surfaces (U25)", () => {
  const CAPS = {
    ...META,
    capabilities: [...META.capabilities, "requests", "steps"],
  }
  async function panelText(name: string, sel: string): Promise<string[]> {
    panelStudio(panelRoutes(name), CAPS)
    const el = await mount()
    await settle()
    const out = all(el, sel).map((n) => n.textContent)
    el.remove()
    await settle()
    teardown()
    setup()
    return out
  }
  async function studioText(name: string, sel: string, query = "view=story") {
    stubBrowser()
    StudioEventSource.reset()
    vi.stubGlobal("EventSource", StudioEventSource)
    setStudioToken("")
    studioFor(name).install()
    renderApp(`/runs/${shape(name).run.id}?${query}`)
    await waitFor(() => expect(document.querySelector(sel)).toBeTruthy())
    return Array.from(document.querySelectorAll(sel), (n) => n.textContent)
  }

  it("tools.timeout's execution facts", async () => {
    const panel = await panelText(
      "tools.timeout",
      '.weft-call[data-key="s1"] .weft-badge.weft-info'
    )
    const studio = await studioText(
      "tools.timeout",
      '[data-call="s1"] [data-exec-facts]'
    )
    expect(studio).toEqual(["0 ms / 50 ms limit"])
    expect(panel).toContain(studio[0])
  })

  it("appr.breakpoint-own's park word", async () => {
    const panel = await panelText(
      "appr.breakpoint-own",
      '.weft-call[data-key="b1"] .weft-warn'
    )
    const studio = await studioText(
      "appr.breakpoint-own",
      '[data-call="b1"] [data-parked-call]'
    )
    expect(panel).toEqual(studio)
  })

  it("sub.failed's typed outcome", async () => {
    const panel = await panelText(
      "sub.failed",
      '.weft-call[data-key="w1"] .weft-err'
    )
    const studio = await studioText(
      "sub.failed",
      '[data-call="w1"] [data-tool-pill]'
    )
    expect(studio).toEqual(["child failed"])
    expect(panel).toEqual(studio)
  })
})

// U34 (WEFT-LOOP-GRAPH.md §4, §6): the panel's loop line and Studio's
// loop graph read one model (lib/loopgraph.ts's core) off the same run
// tree rows: on rw.flaky-infra the same tools in the same order, the
// same counts and failure marks, the same ending; a chip selects its
// step, and graph ⤢ lands Studio's graph on that step.
describe("the loop line on both surfaces (U34)", () => {
  const rOK = golden<{ runs: RunRow[] }>("runs").runs.find(
    (r) => r.id === "r_ok"
  )!
  const b = buildLoop(LOOP_FLAKY)
  const row: RunRow = {
    ...rOK,
    id: LOOP_FLAKY.id,
    agent: LOOP_FLAKY.agent,
    steps: 3,
    stopped_by: "final_answer",
  }
  const doc = {
    ...row,
    trace_id: "t1",
    children: [],
    holes: [],
  } as unknown as RunDoc
  const serveLoop = (meta: Record<string, unknown>) =>
    new FakeStudio()
      .on("GET meta", meta)
      .on("GET runs", { total: 1, runs: [row], next_before: null })
      .on(`GET runs/${row.id}`, doc)
      .on(
        `GET runs/${row.id}/events`,
        pagedEvents(
          b.events.map((event, pos) => ({ pos, time: rOK.started, event })),
          { done: true }
        )
      )
      .on(`GET runs/${row.id}/transcript`, { messages: [] })
      .on(`GET runs/${row.id}/spans`, { spans: b.spans })
      .install()

  it("rw.flaky-infra: the same tools, counts, marks and end; graph ⤢ carries the chip's step", async () => {
    serveLoop(META)
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-open": "true",
    })
    await vi.waitFor(() => expect(all(el, ".weft-turn").length).toBe(1), {
      timeout: 5_000,
    })
    click(all(el, ".weft-turn")[0])
    await vi.waitFor(() => expect($(el, ".weft-loop")).toBeTruthy(), {
      timeout: 5_000,
    })
    const chips = () => all(el, ".weft-loop button") as HTMLElement[]
    const panel = chips().map((c) => c.textContent)
    const tones = chips().map((c) => c.getAttribute("data-tone"))
    // A chip selects its step; the hand-off follows the selection.
    click(chips().at(-1))
    const link = () => new URL($(el, ".weft-loop a")!.getAttribute("href")!)
    await vi.waitFor(() => expect(link().searchParams.get("step")).toBe("2"), {
      timeout: 5_000,
    })
    const to = link()
    expect(to.searchParams.get("scale")).toBe("graph")
    expect(to.hash).toBe("")
    el.remove()
    await settle()
    cleanup()
    document.body.innerHTML = ""

    stubBrowser()
    vi.stubGlobal("scrollTo", vi.fn())
    StudioEventSource.reset()
    vi.stubGlobal("EventSource", StudioEventSource)
    setStudioToken("")
    serveLoop(golden<Record<string, unknown>>("meta"))
    renderApp(`/runs/${row.id}${to.search}`)
    await waitFor(() =>
      expect(document.querySelectorAll("[data-loop-node^='t:']").length).toBe(4)
    )
    const studio = [
      ...Array.from(
        document.querySelectorAll<SVGElement>("[data-loop-node^='t:']")
      ).map(
        (n) =>
          `${n.getAttribute("data-loop-node")!.slice(2)} ${n.getAttribute("data-marks")}`
      ),
      `→ ${document.querySelector("[data-loop-end]")!.getAttribute("data-word")}`,
    ]
    expect(panel).toEqual([
      "inventory_api ×2 ↻",
      "shipping_quote ×1 ✗",
      "legacy_billing ×1 ✗",
      "tax_service ×1 ✗",
      "→ the model answered",
    ])
    expect(studio).toEqual(panel)
    expect(tones).toEqual(["", "error", "error", "error", "ok"])
    // The same selection: step 2 is the scrubber's, its row selected.
    expect(
      document
        .querySelector('[data-loop-step="2"]')!
        .getAttribute("aria-pressed")
    ).toBe("true")
    expect(
      document
        .querySelector('[data-loop-node="model"]')!
        .getAttribute("aria-pressed")
    ).toBe("true")
  })
})

// ── U17: the engine per command, on both drawers ───────────────────
// One fixture (panel/replaykit.ts's, the run page's replay shape), the
// same draft and the same change on Studio's replay drawer and the
// panel's: the same engine, the same option lines and reasons, the
// same target line, the same Run label, the same section order
// (lib/drawer-spec.ts) — the facts read by lib/engine.ts's factsOf on
// both (a knob equal to the agent's own is no change).
describe("the engine per command on both drawers (U17)", () => {
  const cost = (step: number) => ({
    run_id: RK.RUN,
    step,
    model: { provider: "wefttest", requested: "script" },
    usage: { input_tokens: 600, output_tokens: 150 },
    cost_usd: 0.0002,
    cost_known: true,
    cache: "exact",
  })
  const agent = {
    ...RK.agentView,
    resolver: true,
    defaults: {
      max_steps: 10,
      parallelism: 4,
      thinking: "",
      tool_choice: { mode: "auto" as const },
    },
  }
  type Read = {
    engine: string | null
    scripted: string
    live: string
    target: string
    run: string
    order: string[]
    /** U18.2: the build line, and whether Run is held. */
    build: string | null
    held: boolean
  }
  const read = (root: ParentNode, run: string): Read => ({
    build: root.querySelector("[data-target-build]")?.textContent ?? null,
    held: root.querySelector(run)!.hasAttribute("disabled"),
    engine: root
      .querySelector("[data-drawer-section=engine]")!
      .getAttribute("data-engine"),
    scripted: root.querySelector('[data-engine-option="scripted"]')!
      .textContent,
    live: root.querySelector('[data-engine-option="live"]')!.textContent,
    target: root.querySelector("[data-drawer-section=target]")!.textContent,
    run: root.querySelector(run)!.textContent,
    order: [...root.querySelectorAll("[data-drawer-section]")]
      .map((n) => n.getAttribute("data-drawer-section")!)
      .filter((k) => k !== "send"),
  })
  /** A change made on either surface: the field and its new text. */
  type Change =
    | { edit: string }
    | { input: string }
    | { model: string }
    | { toolChoice: string }

  /** U18.2's fixture: the source row's fields, the registered agent,
   * and whether the reader takes the target check's escape. */
  type Fx = {
    row?: Partial<RunRow>
    agent?: AgentView
    escape?: boolean
    /** The one registration is disconnected (U20.1-fix). */
    gone?: boolean
  }
  const runtimesOf = (fx: Fx) => ({
    runtimes: [
      {
        ...RK.runtimeOf([fx.agent ?? agent]),
        ...(fx.gone ? { connected: false } : {}),
      },
    ],
  })

  async function studioSide(
    draft: ReplayDraft,
    c?: Change,
    pv?: PreviewDoc,
    fx: Fx = {}
  ): Promise<Read> {
    stubBrowser()
    const fake = new FakeStudio()
    if (pv) fake.on("POST playground/preview", pv)
    fake
      .on("GET meta", {
        ...golden<Record<string, unknown>>("meta"),
        capabilities: ["playground", "steps", ...(pv ? ["preview"] : [])],
      })
      .on(`GET runs/${RK.RUN}`, {
        ...RK.runRowOf(fx.row),
        children: [],
        holes: [],
        compactions: [],
      })
      .on(`GET runs/${RK.RUN}/transcript`, transcript(...RK.bodies))
      .on(`GET runs/${RK.RUN}/steps/0`, cost(0))
      .on(`GET runs/${RK.RUN}/steps/1`, cost(1))
      .on(`GET runs/${RK.RUN}/steps/2`, cost(2))
      .on(`GET runs/${RK.RUN}/steps/3`, cost(3))
      .on("GET runtimes", runtimesOf(fx))
      .install()
    const { container } = await renderWithRouter(
      createElement(ReplayDrawer, {
        request: { runID: RK.RUN, draft },
        requestKey: 1,
        onClose: () => {},
      })
    )
    await waitFor(() =>
      expect(
        container.querySelector("[data-engine-live]")?.textContent
      ).toContain("≈")
    )
    const field = (sel: string) =>
      waitFor(() => {
        const n = container.querySelector<HTMLElement>(sel)
        expect(n).toBeTruthy()
        return n!
      })
    if (c && "edit" in c)
      fireEvent.change(
        await field('[data-edit-step="1"][data-edit-call="c2"]'),
        { target: { value: c.edit } }
      )
    if (c && "input" in c)
      fireEvent.change(await field('textarea[aria-label="input"]'), {
        target: { value: c.input },
      })
    if (c && "model" in c)
      fireEvent.change(
        await field('[aria-label="model name the app resolves"]'),
        { target: { value: c.model } }
      )
    if (c && "toolChoice" in c)
      await choose(await field('[aria-label="tool choice"]'), c.toolChoice)
    if (pv) await field("[data-preview-system]")
    if (fx.escape) {
      fireEvent.click(await field("[data-target-escape]"))
      await waitFor(() =>
        expect(container.querySelector("[data-target-escape]")).toBeNull()
      )
    }
    const out = read(container, "[data-run-engine]")
    cleanup()
    return out
  }

  async function panelSide(
    draft: ReplayDraft,
    c?: Change,
    pv?: PreviewDoc,
    fx: Fx = {}
  ): Promise<Read> {
    const routes: Record<string, unknown> = {
      ...RK.replayRoutes({ row: fx.row }),
    }
    if (pv) routes["POST playground/preview"] = pv
    for (const n of [0, 1, 2, 3]) routes[`runs/${RK.RUN}/steps/${n}`] = cost(n)
    routes.runtimes = runtimesOf(fx)
    panelStudio(routes, {
      ...RK.REPLAY_META,
      capabilities: [
        ...RK.REPLAY_META.capabilities,
        "steps",
        ...(pv ? ["preview"] : []),
      ],
    })
    const el = await mount()
    const model = (
      el as unknown as {
        model: { openExperiment: (id: string, d: ReplayDraft) => Promise<void> }
      }
    ).model
    await model.openExperiment(RK.RUN, draft)
    await settle(pv ? 400 : 60)
    const type = async (sel: string, v: string, ev = "input") => {
      const f = $(el, sel) as HTMLInputElement
      f.value = v
      f.dispatchEvent(new Event(ev, { bubbles: true }))
      await settle()
    }
    if (c && "edit" in c) await type('[data-weft-k="edit:1:c2"]', c.edit)
    if (c && "input" in c) await type('[data-weft-k="input"]', c.input)
    if (c && "model" in c)
      await type('[aria-label="model name the app resolves"]', c.model)
    if (c && "toolChoice" in c)
      await type('select[aria-label="tool choice"]', c.toolChoice, "change")
    if (fx.escape) {
      ;($(el, "[data-target-escape]") as HTMLElement).click()
      // The draft's preview is asked again (Run waits on it).
      await settle(pv ? 400 : 60)
    }
    const out = read(el.shadowRoot!, ".weft-drawer [data-run-engine]")
    el.remove()
    return out
  }

  const bothDrawers = async (
    draft: () => ReplayDraft,
    c?: Change,
    pv?: PreviewDoc,
    fx?: Fx
  ) => {
    const studio = await studioSide(draft(), c, pv, fx)
    const panel = await panelSide(draft(), c, pv, fx)
    expect(panel).toEqual(studio)
    return studio
  }
  const T = ": the recording answered the original"

  it("Runs in on a disconnected registration: the refusal on both drawers, never its id (U20.1-fix)", async () => {
    const fx: Fx = { gone: true }
    const head = `target${RUNS_IN.head}: ${RUNS_IN.none("acme-support")} · ${RUNS_IN.manage}`
    for (const r of [
      await studioSide(replayFromStep(2), undefined, undefined, fx),
      await panelSide(replayFromStep(2), undefined, undefined, fx),
    ]) {
      expect(r.target.startsWith(head)).toBe(true)
      expect(r.target).toContain(RUNS_IN.connected([]))
      expect(r.target).toContain(RUNS_IN.fix("acme-support"))
      // And Run waits (P10a-pre-web-fix): no command for a runtime
      // that is not connected, never a 503 afterwards.
      expect(r.held).toBe(true)
    }
  })

  it("a plain replay: scripted on both, the same lines and target, every later step priced, Run 'Run on scripted · 0 tokens'", async () => {
    const r = await bothDrawers(() => replayFromStep(2))
    expect(r.engine).toBe("scripted")
    expect(r.run).toBe("Run on scripted · 0 tokens")
    // Runs in (U20.1, lib/runs-in.ts): the same line on both drawers.
    expect(r.target).toMatch(
      new RegExp(
        `^target${RUNS_IN.head}: rt_1 app \\(pid \\d+, seen [^)]+\\) · ${RUNS_IN.manage}$`
      )
    )
    expect(r.live).toBe(
      "live · calls wefttest/script · ~1.5k tok ≈ $0.0004 for steps 2–3"
    )
    expect(r.order).toEqual(DRAWER_ORDER.filter((k) => r.order.includes(k)))
  })

  it("edit this result: an edit — live on both, scripted disabled with the same reason, Run names the model and the sum", async () => {
    const r = await bothDrawers(() => editResultAndReplay(1, "c2"), {
      edit: "policy: no refunds",
    })
    expect(r.engine).toBe("live")
    expect(r.scripted).toContain(`your edit changes step 2's request${T}`)
    expect(r.run).toBe("Run live on wefttest/script · ≈ $0.0004")
  })

  it("the preview's key check misses: scripted disabled on both, in the server's own sentence (U18.1)", async () => {
    const message =
      "scripted has no recorded answer for step 2: the tool list differs from the recording — run on live"
    const r = await bothDrawers(() => replayFromStep(2), undefined, {
      ...golden<PreviewDoc>("playground-preview"),
      scripted_key_match: { ok: false, because: ["tools"], step: 2, message },
    })
    expect(r.engine).toBe("live")
    expect(r.scripted).toContain(message)
  })

  for (const [name, draft, same, other, why] of [
    [
      "an input",
      rerun,
      { input: "refund order 4411" },
      { input: "refund order 9" },
      `a new input${T}`,
    ],
    [
      "a tool choice",
      () => replayFromStep(2),
      { toolChoice: "auto" },
      { toolChoice: "any" },
      `tool choice changed${T}`,
    ],
    [
      "a model",
      () => replayFromStep(2),
      { model: "script" },
      { model: "glm" },
      `a model override${T}`,
    ],
  ] as const)
    it(`${name} equal to the source's or the agent's own is no change on both; another is live on both`, async () => {
      expect((await bothDrawers(draft, same)).engine).toBe("scripted")
      const r = await bothDrawers(draft, other)
      expect(r.engine).toBe("live")
      expect(r.scripted).toContain(why)
    })

  // U18.2 (UX-C-06, UX-D-03): the seeded researcher's shape — a run
  // recorded by a build whose tools held search_docs, replayed against
  // today's registration of the same agent, whose tools hold
  // web_search. Both drawers say the build, the tools and the scripted
  // miss before Run, and hold Run until the reader takes today's build.
  it("another build of the same agent: the build line, the tools and the miss on both, Run held until today's build is taken (U18.2)", async () => {
    const message =
      "scripted has no recorded answer for step 2: the tool list differs from the recording — run on live"
    const base = golden<PreviewDoc>("playground-preview")
    const pv: PreviewDoc = {
      ...base,
      was_sent: {
        ...base.was_sent,
        tools: [{ name: "lookup_order" }, { name: "search_docs" }],
      },
      scripted_key_match: { ok: false, because: ["tools"], step: 2, message },
    }
    const fx: Fx = {
      row: { manifest_hash: "9b5997a3".padEnd(64, "0") },
      agent: {
        ...agent,
        manifest_hash: "a59ce89e".padEnd(64, "1"),
        tools: [
          { name: "lookup_order", side_effects: "never", allow: false },
          { name: "web_search", side_effects: "safe", allow: false },
        ],
      },
    }
    const r = await bothDrawers(() => replayFromStep(2), undefined, pv, fx)
    expect(r.build).toBe(
      `! ${TARGET_WORDS.cross("acme-support", "acme-support", "9b5997a3", "a59ce89e")} · tools differ: search_docs → web_search · scripted will miss at step 2`
    )
    expect(r.held).toBe(true)
    expect(r.engine).toBe("live")
    const taken = await bothDrawers(() => replayFromStep(2), undefined, pv, {
      ...fx,
      escape: true,
    })
    expect(taken.held).toBe(false)
    expect(taken.build).toBe(r.build)
  })

  it("the same build: no build line, Run free on both (U18.2)", async () => {
    const h = "a59ce89e".padEnd(64, "1")
    const r = await bothDrawers(() => replayFromStep(2), undefined, undefined, {
      row: { manifest_hash: h },
      agent: { ...agent, manifest_hash: h },
    })
    expect(r.build).toBeNull()
    expect(r.held).toBe(false)
  })
})
