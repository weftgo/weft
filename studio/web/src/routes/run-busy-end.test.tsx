// U15.2-fix on the run page, on runs shaped from the seed's sessions:
// incident-analyst's t6 (interrupted by t7) and t8 (rolled back by t9)
// read neutral everywhere — header, waterfall rows, story step, the
// call it cut off, the drawer's verdict, the story's end line names the
// turn that ended it (UX-B-09); research-notebook's t5 opens on every
// compaction since t4 and its header says so, a held marker opens its
// own run instead of closing it (UX-B-12); an approved call names what
// let it run (UX-B-14); a fork's turn names its origin and the fork
// point (UX-B-07).
import { cleanup, configure, waitFor } from "@testing-library/react"
import axe from "axe-core"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc, SessionMark, TimelineDoc, TimelineTurn } from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden, pagedEvents } from "@/test/fake-studio"
import type { FakePosEvent } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const T0 = Date.parse("2026-10-19T14:31:20.000Z")
const at = (s: number) => new Date(T0 + s * 1000).toISOString()
const base = golden<RunDoc>("run-fail")

const turnOf = (sid: string, n: number, over: Partial<TimelineTurn> = {}) =>
  ({
    run_id: `${sid}-t${n}`,
    turn: n,
    agent: "incident-analyst",
    status: "succeeded",
    started: at(n),
    finished: at(n + 0.5),
    last_seen: at(n + 0.5),
    steps: 1,
    usage: { input_tokens: 10, output_tokens: 5 },
    marks: 0,
    prompt_preview: `prompt ${n}`,
    ...over,
  }) as TimelineTurn

const markOf = (
  sid: string,
  n: number,
  kind: string,
  lane: string,
  fields: Record<string, unknown>,
  over: Partial<SessionMark> = {}
): SessionMark => ({
  at: at(n + 0.4),
  record: kind === "compaction" ? "compaction" : "thread",
  kind,
  event: kind === "compaction" ? "weft.compaction" : `weft.thread.${kind}`,
  lane,
  run_id: `${sid}-t${n}`,
  turn: n,
  content_mark: "none",
  fields,
  ...over,
})

const timeline = (
  sid: string,
  turns: TimelineTurn[],
  marks: SessionMark[]
): TimelineDoc => ({
  session_id: sid,
  public_id: "pub",
  agent: turns[0].agent,
  status: "succeeded",
  turns,
  marks,
  lanes: [],
  marks_total: marks.length,
  holes: [],
})

const runDoc = (
  sid: string,
  n: number,
  over: Partial<RunDoc> = {}
): RunDoc => ({
  ...base,
  id: `${sid}-t${n}`,
  session_id: sid,
  turn: n,
  started: at(n),
  finished: at(n + 0.5),
  last_seen: at(n + 0.5),
  status: "succeeded",
  err: "",
  steps: 1,
  meta: {},
  compactions: [],
  ...over,
})

const ev = (
  _run: string,
  pos: number,
  event: Record<string, unknown>
): FakePosEvent => ({ pos, time: at(6 + pos * 0.1), event })

// t6: step 0 called run_analysis; the interrupt cancelled it mid-call.
const cutEvents = (run: string): FakePosEvent[] => [
  ev(run, 0, {
    type: "run_start",
    id: run,
    model: { provider: "wefttest", name: "script" },
    agent: "incident-analyst",
  }),
  ev(run, 1, { type: "step_start", run_id: run, index: 0 }),
  ev(run, 2, {
    type: "tool_start",
    run_id: run,
    seq: 1,
    call_id: "call_1",
    name: "run_analysis",
    args: { dataset: "checkout-latency" },
  }),
]

const WAR = "s_war"
const canceled = "weft: run failed at step 1: context canceled"
const warTL = timeline(
  WAR,
  [6, 7, 8, 9].map((n) =>
    turnOf(
      WAR,
      n,
      n === 6 || n === 8 ? { status: "failed", error_type: "cancelled" } : {}
    )
  ),
  [
    markOf(WAR, 6, "interrupt", "steer_queue", {
      by_run: `${WAR}-t7`,
      by_turn: 7,
      policy: "interrupt",
      target: "run",
    }),
    markOf(WAR, 8, "interrupt", "steer_queue", {
      by_run: `${WAR}-t9`,
      by_turn: 9,
      policy: "rollback",
      target: "run",
    }),
  ]
)

let studio: FakeStudio
const serve = (
  doc: RunDoc,
  events: FakePosEvent[],
  tl?: TimelineDoc,
  spans: unknown[] = []
) => {
  studio = new FakeStudio()
    .on("GET meta", {
      ...golden<Record<string, unknown>>("meta"),
      capabilities: ["steps", "ingest", "timeline"],
    })
    .on(`GET runs/${doc.id}`, doc)
    .on(`GET runs/${doc.id}/events`, pagedEvents(events))
    .on(`GET runs/${doc.id}/transcript`, { batches: [] })
    .on(`GET runs/${doc.id}/spans`, { spans })
    .on("GET runs", { total: 0, runs: [], next_before: null })
  if (tl) studio.on(`GET sessions/${tl.session_id}/timeline`, tl)
  studio.install()
}

beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

const noViolations = async (el: Element | Document) => {
  const r = await axe.run(el, {
    rules: { "color-contrast": { enabled: true } },
  })
  expect(r.violations.map((v) => v.id)).toEqual([])
}

const ended = (n: number, over: Partial<RunDoc> = {}) =>
  runDoc(WAR, n, {
    status: "failed",
    err: canceled,
    error_type: "cancelled",
    cancel_cause: "thread.interrupted",
    stopped_by: "canceled",
    ...over,
  })

describe("an interrupted turn's run page is neutral and names its end (UX-B-09)", () => {
  it("t6: the header, the story's step, the cut call and the end line say interrupted by t7 — no red", async () => {
    const doc = ended(6)
    serve(doc, cutEvents(doc.id), warTL)
    const { container } = renderApp(`/runs/${doc.id}?view=story`)
    const head = await waitFor(() => {
      const h = container.querySelector("[data-run-ended]")
      expect(h).toBeTruthy()
      expect(h?.textContent).toContain("interrupted by t7")
      return h as HTMLElement
    })
    expect(head.getAttribute("data-run-ended")).toBe("interrupted")
    expect(head.textContent).toContain(canceled)
    await waitFor(() =>
      expect(container.querySelector("[data-story-ended]")?.textContent).toBe(
        "■ interrupted by t7 at step 0"
      )
    )
    expect(container.querySelector("[data-turn-end]")?.textContent).toBe(
      "✗ interrupted by t7"
    )
    expect(container.querySelector("[data-step-ended]")?.textContent).toBe(
      "interrupted by t7 during this step"
    )
    expect(container.querySelector("[data-call-ended]")?.textContent).toBe(
      "cut off"
    )
    // Red stays for real failures: nothing of this run reads it.
    const main = container.querySelector("main") ?? container
    expect(
      [...main.querySelectorAll('[class*="status-bad"]')].map(
        (e) => e.textContent
      )
    ).toEqual([])
    expect(main.textContent).not.toMatch(
      /never completed|failed during this step/i
    )
    await noViolations(container)
  })

  it("t8: rolled back by t9, off path — the waterfall's rows and the drawer's verdict neutral", async () => {
    const doc = ended(8)
    serve(doc, cutEvents(doc.id), warTL)
    const { container } = renderApp(`/runs/${doc.id}?sel=s0`)
    await waitFor(() =>
      expect(container.querySelector("[data-verdict]")?.textContent).toBe(
        "rolled back by t9 · off path"
      )
    )
    expect(
      container.querySelector("[data-verdict]")?.hasAttribute("data-bad")
    ).toBe(false)
    expect(
      container.querySelector('[data-answer-line="then"]')?.textContent
    ).toBe("■ rolled back by t9 · off path at step 0")
    expect(
      container
        .querySelector("[data-run-ended]")
        ?.getAttribute("data-run-ended")
    ).toBe("rolled_back")
    const main = container.querySelector("main") ?? container
    expect(
      [
        ...main.querySelectorAll(
          '[class*="status-bad"], [class*="text-ev-error"]'
        ),
      ].map((e) => e.textContent)
    ).toEqual([])
    await noViolations(container)
  })

  it("a cancel with another cause stays a failure's red", async () => {
    const doc = ended(6, { cancel_cause: "app.shutdown" })
    serve(doc, cutEvents(doc.id), warTL)
    const { container } = renderApp(`/runs/${doc.id}?view=story`)
    await waitFor(() =>
      expect(container.querySelector('[class*="status-bad"]')).toBeTruthy()
    )
    expect(container.querySelector("[data-run-ended]")).toBeNull()
    expect(container.querySelector("[data-story-ended]")).toBeNull()
  })
})

const RES = "s_res"
const comp = (
  n: number,
  dt: number,
  reason: string,
  hash: string,
  replaced: number,
  entries: number,
  before: number,
  after: number
) =>
  markOf(
    RES,
    n,
    "compaction",
    "compaction",
    {
      scope: "session",
      hash,
      reason,
      replaced,
      entries,
      tokens_before: before,
      tokens_after: after,
    },
    { at: at(n + dt) }
  )
const resTL = timeline(
  RES,
  [3, 4, 5].map((n) => turnOf(RES, n, { agent: "research-notebook" })),
  [
    comp(3, 0.7, "trim", "h_c1", 1, 1, 11070, 6749),
    comp(4, 0.7, "trim", "h_c2", 1, 1, 9378, 6054),
    comp(4, 0.8, "manual", "h_c3", 11, 2, 6054, 2700),
  ]
)
const resEvents = (run: string): FakePosEvent[] => [
  ev(run, 0, {
    type: "run_start",
    id: run,
    model: { provider: "wefttest", name: "script" },
    agent: "research-notebook",
  }),
  ev(run, 1, { type: "step_start", run_id: run, index: 0 }),
  ev(run, 2, {
    type: "step_finish",
    run_id: run,
    index: 0,
    reason: "stop",
    usage: { input_tokens: 10, output_tokens: 5 },
  }),
  ev(run, 3, {
    type: "run_finish",
    run_id: run,
    usage: { input_tokens: 10, output_tokens: 5 },
    steps: 1,
  }),
]
const session = (
  hash: string,
  reason: string,
  replaced: number,
  entries: number
) => ({
  scope: "session" as const,
  hash,
  reason,
  replaced,
  entries,
})

describe("the compaction at its boundary on the run page (UX-B-12, UX-S5b-05)", () => {
  it("t5 opens on every compaction since t4 — both — and its header says it started on a compacted context", async () => {
    const doc = runDoc(RES, 5, { agent: "research-notebook" })
    serve(doc, resEvents(doc.id), resTL)
    const { container } = renderApp(`/runs/${doc.id}?view=story`)
    const card = await waitFor(() => {
      const c = container.querySelector(`[data-started-compacted="${doc.id}"]`)
      expect(c).toBeTruthy()
      return c as HTMLElement
    })
    expect(
      [...card.querySelectorAll("[data-started-line]")].map(
        (l) => l.firstChild?.textContent
      )
    ).toEqual([
      "compacted after t4 · 1 message compacted into 1 · 9.4k → 6.1k tokens · trim",
      "compacted after t4 · 11 messages compacted into 2 · 6.1k → 2.7k tokens · manual",
    ])
    expect(
      card.querySelector('[data-hole="compacted"]')?.getAttribute("title")
    ).toContain("fix: open the marker (t4) on this card")
    expect(
      container.querySelector('[data-compaction-chip="started"]')?.textContent
    ).toBe("⌗ started on a compacted context (2 compactions)")
    await noViolations(container)
  })

  it("t4's story closes on its two markers, after the run; t3's trim is t4's start", async () => {
    const doc = runDoc(RES, 4, {
      agent: "research-notebook",
      compactions: [
        session("h_c2", "trim", 1, 1),
        session("h_c3", "manual", 11, 2),
      ],
    })
    serve(doc, resEvents(doc.id), resTL)
    const { container } = renderApp(`/runs/${doc.id}?view=story`)
    await waitFor(() =>
      expect(
        container.querySelectorAll('[data-compaction="session"]')
      ).toHaveLength(2)
    )
    expect(
      container.querySelector(
        `[data-started-compacted="${doc.id}"] [data-started-line]`
      )?.firstChild?.textContent
    ).toBe(
      "compacted after t3 · 1 message compacted into 1 · 11.1k → 6.7k tokens · trim"
    )
    expect(
      [...container.querySelectorAll("[data-compaction-chip]")].map((c) =>
        c.getAttribute("data-compaction-chip")
      )
    ).toEqual(["started", "session", "session"])
  })

  it("a held marker (filed under the run that started on it) opens its story, never closes it", async () => {
    // No run produced the compacted context (a fork's copied path):
    // thread held the marker and emitted it as t1 reported.
    const FORK = "s_fork"
    const tl = timeline(
      FORK,
      [turnOf(FORK, 1, { agent: "research-notebook" })],
      [
        markOf(
          FORK,
          1,
          "compaction",
          "compaction",
          {
            scope: "session",
            hash: "h_held",
            reason: "manual",
            replaced: 4,
            entries: 1,
          },
          { at: at(1.1) }
        ),
      ]
    )
    const doc = runDoc(FORK, 1, {
      agent: "research-notebook",
      compactions: [session("h_held", "manual", 4, 1)],
    })
    serve(doc, resEvents(doc.id), tl)
    const { container } = renderApp(`/runs/${doc.id}?view=story`)
    const line = await waitFor(() => {
      const l = container.querySelector('[data-started-line="held"]')
      expect(l).toBeTruthy()
      return l as HTMLElement
    })
    expect(line.textContent).toBe(
      "compacted before this turn · 4 messages compacted into 1 · manual"
    )
    expect(container.querySelector('[data-compaction="session"]')).toBeNull()
    expect(document.body.textContent).not.toContain(
      "after this run · session compacted"
    )
    expect(
      [...container.querySelectorAll("[data-compaction-chip]")].map((c) =>
        c.getAttribute("data-compaction-chip")
      )
    ).toEqual(["started"])
  })
})

describe("an approved call names what let it run (UX-B-14)", () => {
  it("the execute_tool span's approved_by and grant id are the call's chip", async () => {
    const SRE = "s_sre"
    const doc = runDoc(SRE, 2, {
      agent: "oncall-sre",
      trace_id: "0102030405060708090a0b0c0d0e0f10",
    })
    const run = doc.id
    const events: FakePosEvent[] = [
      ev(run, 0, {
        type: "run_start",
        id: run,
        model: { provider: "wefttest", name: "script" },
        agent: "oncall-sre",
      }),
      ev(run, 1, { type: "step_start", run_id: run, index: 0 }),
      ev(run, 2, {
        type: "tool_start",
        run_id: run,
        seq: 1,
        call_id: "c1",
        name: "run_command",
        args: { cmd: "kubectl get pods" },
      }),
      ev(run, 3, {
        type: "tool_finish",
        run_id: run,
        seq: 1,
        call_id: "c1",
        name: "run_command",
        content: "3 pods",
        is_error: false,
      }),
      ev(run, 4, {
        type: "step_finish",
        run_id: run,
        index: 0,
        reason: "stop",
        usage: { input_tokens: 10, output_tokens: 5 },
      }),
      ev(run, 5, {
        type: "run_finish",
        run_id: run,
        usage: { input_tokens: 10, output_tokens: 5 },
        steps: 1,
      }),
    ]
    const span = {
      trace_id: doc.trace_id,
      span_id: "00000000000000c1",
      parent_span_id: "",
      name: "execute_tool run_command",
      start: at(2.2),
      end: at(2.3),
      status: "ok",
      run_id: run,
      attrs: {
        "gen_ai.operation.name": "execute_tool",
        "weft.run.id": run,
        "weft.step.index": 0,
        "gen_ai.tool.call.id": "c1",
        "weft.tool.approved_by": "grant",
        "weft.grant.id": "g_get",
      },
    }
    serve(doc, events, undefined, [span])
    const { container } = renderApp(`/runs/${run}?sel=c%3Ac1`)
    await waitFor(() =>
      expect(
        container.querySelector('[data-call-approval="grant"]')?.textContent
      ).toBe("✓ approved · by grant g_get")
    )
  })
})

describe("a fork's turn names its origin (UX-B-07)", () => {
  it("⑃ fork of <origin> at entry e11 with the fork point's prompt", async () => {
    const TRIP = "s_trip"
    const origin = timeline(
      TRIP,
      [1, 3].map((n) =>
        turnOf(TRIP, n, {
          agent: "trip-planner",
          prompt_preview:
            n === 3
              ? "What about Porto instead of Lisbon, same dates?"
              : "Plan a trip",
        })
      ),
      [
        markOf(
          TRIP,
          3,
          "fork",
          "branch_fork",
          { new_session: "s_fork_b" },
          { entry: "e11" }
        ),
      ]
    )
    const doc = runDoc("s_fork_b", 1, {
      agent: "trip-planner",
      meta: { "weft.session.forked_from": `${TRIP}#e11` },
    })
    serve(doc, resEvents(doc.id), origin)
    const { container } = renderApp(`/runs/${doc.id}`)
    await waitFor(() =>
      expect(
        container
          .querySelector("[data-fork-point]")
          ?.getAttribute("data-fork-point")
      ).toBe(`${TRIP}-t3`)
    )
    expect(container.querySelector("[data-fork-of]")?.textContent).toBe(
      `⑃ fork of ${TRIP} at entry e11 (“What about Porto instead of Lisbon, same dates?”)`
    )
    // The raw pair is no metadata chip.
    expect(document.body.textContent).not.toContain("weft.session.forked_from=")
  })
})
