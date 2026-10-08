// G1's Done line: ⤢ from a panel step lands on that step's ordinal in
// Studio, for a run with a steer batch — where the step's event index
// (its step_start's stream position) and its transcript batch index
// are not its ordinal. The panel half reads the link the panel draws;
// the Studio half renders the real app at that link and finds the
// step it names, in the story and in the trace.
import { cleanup, configure, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc, RunsPage } from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource as StudioEventSource } from "@/test/fake-event-source"
import {
  FakeStudio,
  golden,
  pagedEvents,
  transcriptOf,
} from "@/test/fake-studio"
import {
  $,
  all,
  assistant,
  baseRoutes,
  click,
  fakeStudio,
  idle,
  mount,
  page,
  runRow,
  setup,
  teardown,
  transcript,
  user,
} from "@/panel/testkit"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const RUN = "s_01-t1"
const BASE = "http://studio.test/studio/"
const TOKEN = "sekrit-panel-token"

/** A steered run: step 0 calls a tool and finishes, a steer delivers
 * a user turn after it, step 1 answers. Step 1's step_start sits at
 * stream position 6; its assistant batch is transcript batch 4. */
function steeredEvents(opts: { finished?: boolean } = {}): unknown[] {
  const usage = { input_tokens: 10, output_tokens: 4 }
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
      args: { order_id: "4411" },
    },
    {
      type: "tool_finish",
      run_id: RUN,
      seq: 2,
      call_id: "c1",
      name: "lookup_order",
      content: "shipped",
      is_error: false,
    },
    { type: "step_finish", run_id: RUN, index: 0, reason: "tool_calls", usage },
    {
      type: "steered",
      run_id: RUN,
      seq: 3,
      step: 0,
      messages: [user("and the refund?")],
    },
    { type: "step_start", run_id: RUN, index: 1 },
  ]
  if (opts.finished !== false)
    evs.push(
      { type: "step_finish", run_id: RUN, index: 1, reason: "stop", usage },
      { type: "run_finish", run_id: RUN, usage, steps: 2 }
    )
  return evs
}

const STEP1_POS = 6

const batches = [
  [user("where is my order #4411?")],
  [
    assistant("", [
      { id: "c1", name: "lookup_order", args: { order_id: "4411" } },
    ]),
  ],
  [
    {
      role: "tool",
      content: [{ type: "tool_result", call_id: "c1", content: "shipped" }],
    },
  ],
  [user("and the refund?")], // the steer batch (step 0's drain)
  [assistant("Shipped; the refund is on its way.")],
]

describe("the fixture is a run where index ≠ ordinal", () => {
  it("step 1's step_start is at stream position 6, its words in batch 4", () => {
    const evs = steeredEvents()
    const at = evs.findIndex(
      (e) =>
        (e as { type: string; index?: number }).type === "step_start" &&
        (e as { index: number }).index === 1
    )
    expect(at).toBe(STEP1_POS)
    expect(at).not.toBe(1)
    const tr = transcriptOf(batches)
    const words = tr.batches.findIndex(
      (b) =>
        (b.messages as { role: string }[]).some(
          (m) => m.role === "assistant"
        ) && b.step === 1
    )
    expect(words).toBe(4)
  })
})

function routes(opts: { finished?: boolean } = {}) {
  const r = baseRoutes()
  const status = opts.finished === false ? "running" : "succeeded"
  const row = runRow({
    status,
    steps: 2,
    finished: opts.finished === false ? null : runRow({}).finished,
  })
  r["runs?public_id=pub_orders&limit=50"] = {
    total: 1,
    runs: [row],
    next_before: null,
  }
  r[`runs/${RUN}`] = { ...row, children: [] }
  r[`runs/${RUN}/events?after=0&limit=500`] = page(steeredEvents(opts))
  r[`runs/${RUN}/transcript`] = transcript(...batches)
  return r
}

async function panel(opts: { finished?: boolean } = {}) {
  fakeStudio(routes(opts))
  return mount({
    "data-endpoint": BASE,
    "data-public-id": "pub_orders",
    "data-open": "true",
    "data-token": TOKEN,
  })
}

const expand = (el: Element) =>
  ($(el as never, ".weft-head a") as HTMLAnchorElement).getAttribute("href")

describe("the panel's ⤢ carries the step ordinal (G1)", () => {
  beforeEach(setup)
  afterEach(teardown)

  it("a click on step 1's card links runs/<id>?step=1 — the ordinal, not the event index", async () => {
    const el = await panel()
    expect(
      all(el, "[data-weft-step]").map((n) => n.getAttribute("data-weft-step"))
    ).toEqual(["0", "1"])
    click($(el, '[data-weft-step="1"]'))
    await idle()
    expect(expand(el)).toBe(`${BASE}runs/${RUN}?step=1&view=story`)
    click($(el, '[data-weft-step="0"]'))
    await idle()
    expect(expand(el)).toBe(`${BASE}runs/${RUN}?step=0&view=story`)
  })

  it("with no step selected, a running turn's ⤢ carries the running step", async () => {
    const el = await panel({ finished: false })
    expect(expand(el)).toBe(`${BASE}runs/${RUN}?step=1&view=story`)
  })

  it("the tool call, the session and the trace are links too — and no link carries the token", async () => {
    const el = await panel()
    const call = $(el, '[data-weft-call-link="c1"]') as HTMLAnchorElement
    expect(call.getAttribute("href")).toBe(
      `${BASE}runs/${RUN}?step=0&sel=c%3A0%3Ac1`
    )
    expect(
      (
        $(el, '[data-weft-session-link="s_01"]') as HTMLAnchorElement
      ).getAttribute("href")
    ).toBe(`${BASE}sessions/s_01`)
    expect(
      (
        $(el, '[data-weft-trace-link="0102"]') as HTMLAnchorElement
      ).getAttribute("href")
    ).toBe(`${BASE}traces/0102`)
    const hrefs = all(el, "a[href]").map((a) => a.getAttribute("href") ?? "")
    expect(hrefs.length).toBeGreaterThan(3)
    for (const h of hrefs) {
      expect(h).not.toContain(TOKEN)
      expect(h).not.toMatch(/token/i)
    }
  })
})

describe("Studio lands on the step the link names (G1)", () => {
  const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!
  const doc: RunDoc = {
    ...rOK,
    id: RUN,
    agent: "acme-support",
    steps: 2,
    trace_id: "",
    children: [],
    holes: [],
  }

  function serveStudio() {
    stubBrowser()
    StudioEventSource.reset()
    vi.stubGlobal("EventSource", StudioEventSource)
    setStudioToken("")
    new FakeStudio()
      .on("GET meta", {
        ...golden<Record<string, unknown>>("meta"),
        capabilities: ["ingest"],
      })
      .on(`GET runs/${RUN}`, doc)
      .on(
        `GET runs/${RUN}/events`,
        pagedEvents(
          steeredEvents().map((event, pos) => ({
            pos,
            time: rOK.started,
            event,
          })),
          { done: true }
        )
      )
      .on(`GET runs/${RUN}/transcript`, transcriptOf(batches))
      .on(`GET runs/${RUN}/spans`, { spans: [] })
      .install()
  }
  beforeEach(serveStudio)
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
  })

  /** The panel's link, as Studio's router sees it (under its mount). */
  const route = (href: string) => {
    const u = new URL(href)
    return u.pathname.replace(/^\/studio/, "") + u.search
  }

  it("the href the panel drew, opened in Studio, highlights step 1's card — not the step at event 1 or batch 1", async () => {
    // The panel half: mount it, click step 1, read ⤢ off the DOM.
    vi.unstubAllGlobals()
    setup()
    const el = await panel()
    click($(el, '[data-weft-step="1"]'))
    await idle()
    const drawn = expand(el)
    expect(drawn).toBe(`${BASE}runs/${RUN}?step=1&view=story`)
    teardown()
    // The Studio half: that very href, under Studio's mount.
    serveStudio()
    renderApp(route(drawn!))
    await waitFor(() =>
      expect(document.querySelector('[data-step="1"]')).toBeTruthy()
    )
    expect(
      document
        .querySelector('[data-step="1"]')!
        .hasAttribute("data-highlighted")
    ).toBe(true)
    expect(document.querySelectorAll("[data-highlighted]").length).toBe(1)
    // The steer sits between the two cards, as the run delivered it.
    expect(document.querySelector("[data-steer]")?.textContent).toContain(
      "and the refund?"
    )
  })

  it("the trace selects step 1's span when no span is named", async () => {
    renderApp(`/runs/${RUN}?step=1`)
    await waitFor(() =>
      expect(document.querySelector('[data-span="s1"]')).toBeTruthy()
    )
    expect(
      document.querySelector('[data-span="s1"]')!.getAttribute("aria-selected")
    ).toBe("true")
    expect(
      document.querySelector('[data-span="s0"]')!.getAttribute("aria-selected")
    ).toBe("false")
  })

  it("a named call wins over the step: ?step=0&sel=c:0:c1 selects the call", async () => {
    renderApp(route(`${BASE}runs/${RUN}?step=0&sel=c%3A0%3Ac1`))
    await waitFor(() =>
      expect(document.querySelector('[data-span="c:0:c1"]')).toBeTruthy()
    )
    expect(
      document
        .querySelector('[data-span="c:0:c1"]')!
        .getAttribute("aria-selected")
    ).toBe("true")
  })
})

// Review fixes 3 and 4: a ?sel= that names no row never wins (an old
// two-part call key maps to the call it meant), and ?step= lands on
// the time axis too. The run calls c1 in both steps — call ids repeat
// across steps (core/loop.go).
describe("?sel= and ?step= resolve to a row that exists (G1 review)", () => {
  const REP = "r_rep"
  const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!
  const usage = { input_tokens: 10, output_tokens: 4 }
  const call = (index: number) => [
    { type: "step_start", run_id: REP, index },
    {
      type: "tool_start",
      run_id: REP,
      call_id: "c1",
      name: "lookup_order",
      args: {},
    },
    {
      type: "tool_finish",
      run_id: REP,
      call_id: "c1",
      name: "lookup_order",
      content: "ok",
      is_error: false,
    },
    { type: "step_finish", run_id: REP, index, reason: "tool_calls", usage },
  ]
  const events = [
    {
      type: "run_start",
      id: REP,
      model: { provider: "wefttest", name: "script" },
      agent: "acme-support",
    },
    ...call(0),
    ...call(1),
    { type: "step_start", run_id: REP, index: 2 },
    { type: "step_finish", run_id: REP, index: 2, reason: "stop", usage },
    { type: "run_finish", run_id: REP, usage, steps: 3 },
  ].map((event, pos) => ({ pos, time: rOK.started, event }))

  const T = "2026-10-01T09:00:0"
  const span = (
    span_id: string,
    name: string,
    parent: string,
    sec: number,
    attrs: Record<string, unknown>
  ) => ({
    trace_id: "0102",
    span_id,
    parent_span_id: parent,
    name,
    kind: "internal",
    start: `${T}${sec}.000Z`,
    end: `${T}${sec}.500Z`,
    status: "ok",
    status_message: "",
    service: "app",
    attrs: { "weft.run.id": REP, ...attrs },
    events: [],
  })
  const spans = [
    {
      ...span("aa00", "invoke_agent acme-support", "", 0, {
        "gen_ai.operation.name": "invoke_agent",
      }),
      end: `${T}4.000Z`,
    },
    span("aa01", "chat script", "aa00", 1, {
      "gen_ai.operation.name": "chat",
      "weft.step.index": 0,
    }),
    span("aa02", "execute_tool lookup_order", "aa00", 1, {
      "gen_ai.operation.name": "execute_tool",
      "weft.step.index": 0,
    }),
    span("aa03", "chat script", "aa00", 2, {
      "gen_ai.operation.name": "chat",
      "weft.step.index": 1,
    }),
    span("aa04", "execute_tool lookup_order", "aa00", 2, {
      "gen_ai.operation.name": "execute_tool",
      "weft.step.index": 1,
    }),
    span("aa05", "chat script", "aa00", 3, {
      "gen_ai.operation.name": "chat",
      "weft.step.index": 2,
    }),
  ]

  function serve(opts: { traced: boolean }) {
    stubBrowser()
    StudioEventSource.reset()
    vi.stubGlobal("EventSource", StudioEventSource)
    setStudioToken("")
    const doc: RunDoc = {
      ...rOK,
      id: REP,
      agent: "acme-support",
      steps: 3,
      trace_id: opts.traced ? "0102" : "",
      children: [],
      holes: [],
    }
    new FakeStudio()
      .on("GET meta", {
        ...golden<Record<string, unknown>>("meta"),
        capabilities: ["ingest"],
      })
      .on(`GET runs/${REP}`, doc)
      .on(`GET runs/${REP}/events`, pagedEvents(events, { done: true }))
      .on(`GET runs/${REP}/transcript`, transcriptOf([]))
      .on(`GET runs/${REP}/spans`, { spans: opts.traced ? spans : [] })
      .install()
  }
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
  })

  const selected = () =>
    Array.from(
      document.querySelectorAll('[data-span][aria-selected="true"]')
    ).map((n) => n.getAttribute("data-span"))

  it("?step=1&sel=c:c1 (a pre-G1 call key) selects step 1's call", async () => {
    serve({ traced: false })
    renderApp(`/runs/${REP}?step=1&sel=${encodeURIComponent("c:c1")}`)
    await waitFor(() => expect(selected()).toEqual(["c:1:c1"]))
    expect(document.body.textContent).not.toContain("past the playhead")
  })

  it("?sel=c:c1 without a step selects the first call of that id", async () => {
    serve({ traced: false })
    renderApp(`/runs/${REP}?sel=${encodeURIComponent("c:c1")}`)
    await waitFor(() => expect(selected()).toEqual(["c:0:c1"]))
  })

  it("?step=1&sel=c:gone falls to step 1, with no false playhead note", async () => {
    serve({ traced: false })
    renderApp(`/runs/${REP}?step=1&sel=${encodeURIComponent("c:gone")}`)
    await waitFor(() => expect(selected()).toEqual(["s1"]))
    expect(document.body.textContent).not.toContain("past the playhead")
  })

  it("?step=1&axis=time selects step 1's chat span on the time axis", async () => {
    serve({ traced: true })
    renderApp(`/runs/${REP}?step=1&axis=time`)
    await waitFor(() => expect(selected()).toEqual(["t:aa03"]))
  })
})
