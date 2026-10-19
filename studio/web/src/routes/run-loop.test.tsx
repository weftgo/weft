// U34's Done line (WEFT-LOOP-GRAPH.md §6) on the run page: the graph
// scale opens on the loop — rw.flaky-infra's failing tools, its insight
// strip and inventory_api's ‹ call 1 of 2 ›; rw.budget-guards' repeated
// poll_job and loop_detected end, its worker's max_steps; rw.support-
// desk's parked issue_refund (a dashed result edge) and its resume's
// has_tool_call:submit_output; rw.tool-storm's scrubber lighting the
// fan-out; rw.deep-delegation's subagent expanded in place with its
// breadcrumb, and level=agents' call graph. A copied URL restores the
// scale, level, step and selection; the keyboard reaches every node;
// axe finds nothing. U34-fix: the seed's shapes (call ids reused across
// steps, the resume's output_submitted, RETRY a retry), a child loop's
// own step rows and catalog hole, the agents level off without spans,
// the end, edges and never-called insight selectable, subagent costs,
// and the draw's budget.
import {
  cleanup,
  configure,
  fireEvent,
  render,
  waitFor,
} from "@testing-library/react"
import axe from "axe-core"
import { afterEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc, RunRow, RunsPage, Span } from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import {
  apiError,
  FakeStudio,
  golden,
  pagedEvents,
  transcriptOf,
} from "@/test/fake-studio"
import {
  build,
  DESK_PARKED,
  DESK_RESUME,
  DIRECTOR,
  FLAKY,
  longRun,
  POLLER,
  STORM,
  TEAM,
  WORKER,
} from "@/test/loop-fixtures"
import type { FxRun } from "@/test/loop-fixtures"
import { LoopGraphView } from "@/components/studio/loop-graph"
import { loopGraph, NO_SEL } from "@/lib/loopgraph"
import { ALL_RUNS } from "@/lib/run-tree"
import { treeRows } from "@/lib/waterfall-tree"
import * as waterfallTree from "@/lib/waterfall-tree"
import { BASELINE_MS, measure, withinBudget } from "@/test/budget"
import {
  COST_NOT_READ_HERE,
  COST_NOT_READ_YET,
  COST_READ_FAILED,
} from "@/lib/cost"

// The loop's rows builder, spied (U34-fix2): the page builds its every-
// child-expanded rows with treeRows({ expanded: ALL_RUNS }); a passthrough.
vi.mock("@/lib/waterfall-tree", async (orig) => {
  const m = await orig<typeof waterfallTree>()
  return { ...m, treeRows: vi.fn(m.treeRows) }
})
/** How many times the page built the loop's whole tree. */
const loopBuilds = () =>
  vi
    .mocked(waterfallTree.treeRows)
    .mock.calls.filter(([input]) => input.expanded === ALL_RUNS).length

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!

/** The child runs a fixture starts, depth first. */
function childrenOf(r: FxRun): FxRun[] {
  return r.steps.flatMap((s) =>
    s.calls.flatMap((c) => (c.child ? [c.child, ...childrenOf(c.child)] : []))
  )
}

function serve(
  r: FxRun,
  opts: {
    spans?: boolean
    caps?: string[]
    /** Per run id: fields over its row (a cost block). */
    rows?: Record<string, Partial<RunRow>>
    /** The run still running: its events up to (not including) this
     * index, the stream open for the rest. */
    live?: number
    /** A trace id even without spans (the spans read back empty). */
    trace?: boolean
    /** GET runs/{id}/spans answers a 500. */
    spansFail?: boolean
    /** The requests capability, its read answering a 500. */
    requestsFail?: boolean
    /** Per child run id: its document answers once this settles. */
    holdDocs?: Record<string, Promise<unknown>>
    /** Child run ids whose document answers a 500. */
    failDocs?: string[]
  } = {}
) {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  const b = build(r)
  const withSpans = opts.spans ?? true
  const kids = childrenOf(r)
  const parkedOf = (x: FxRun) =>
    x.steps.flatMap((st) => st.calls).filter((c) => c.pending).length
  const row = (x: FxRun, parent?: string): RunRow => ({
    ...rOK,
    id: x.id,
    agent: x.agent,
    steps: x.steps.length,
    status: x.failed ? "failed" : "succeeded",
    ...(parkedOf(x) ? { pending: parkedOf(x) } : {}),
    ...(x.stoppedBy ? { stopped_by: x.stoppedBy } : {}),
    ...(parent
      ? { parent_run_id: parent, parent_call_id: x.id.split("/").at(-1) }
      : {}),
    ...(!parent && opts.live !== undefined
      ? { status: "running", finished: null }
      : {}),
    ...opts.rows?.[x.id],
  })
  const parentOf = (id: string) => /^(.+)\/\d+\/[^/]+$/.exec(id)?.[1]
  const doc: RunDoc = {
    ...row(r),
    trace_id: (opts.trace ?? withSpans) ? "t1" : "",
    children: kids
      .filter((k) => parentOf(k.id) === r.id)
      .map((k) => row(k, r.id)),
    holes: [],
  }
  const paged = (evs: unknown[]) =>
    pagedEvents(
      evs.map((event, pos) => ({ pos, time: rOK.started, event })),
      { done: true }
    )
  const s = new FakeStudio()
    .on("GET meta", {
      ...golden<Record<string, unknown>>("meta"),
      capabilities: [
        "ingest",
        "spans_tree",
        ...(opts.live !== undefined ? ["live"] : []),
        ...(opts.requestsFail ? ["requests"] : []),
        ...(opts.caps ?? []),
      ],
    })
    .on(`GET runs/${r.id}`, doc)
    .on(
      `GET runs/${r.id}/events`,
      opts.live === undefined
        ? paged(b.events)
        : pagedEvents(
            b.events
              .slice(0, opts.live)
              .map((event, pos) => ({ pos, time: rOK.started, event })),
            { done: false }
          )
    )
    .on(`GET runs/${r.id}/transcript`, transcriptOf([]))
    .on(`GET runs/${r.id}/spans`, () =>
      opts.spansFail
        ? apiError(500, "internal", "database is locked")
        : {
            spans: withSpans ? b.spans : ([] as Span[]),
            holes: [],
          }
    )
  if (opts.requestsFail)
    s.on(`GET runs/${r.id}/requests`, () =>
      apiError(500, "internal", "database is locked")
    )
  for (const k of kids)
    s.on(`GET runs/${k.id}`, async () => {
      await opts.holdDocs?.[k.id]
      if (opts.failDocs?.includes(k.id))
        return apiError(500, "internal", "database is locked")
      return {
        ...row(k, parentOf(k.id)),
        children: kids
          .filter((x) => parentOf(x.id) === k.id)
          .map((x) => row(x, k.id)),
        holes: [],
      }
    })
      .on(`GET runs/${k.id}/events`, paged([]))
      .on(`GET runs/${k.id}/spans`, () => ({
        spans: b.spans.filter((x) => x.run_id?.startsWith(k.id)),
        holes: [],
      }))
  s.install()
  return b
}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

const node = (id: string) =>
  document.querySelector<SVGGElement>(`[data-loop-node="${CSS.escape(id)}"]`)!
const loopOf = () => document.querySelector<HTMLElement>("[data-loop-graph]")
const nodes = () =>
  Array.from(
    document.querySelectorAll<SVGGElement>("[data-loop-node^='t:']")
  ).map((n) => [
    n.getAttribute("data-loop-node"),
    n.getAttribute("data-calls"),
    n.getAttribute("data-fails"),
  ])

describe("rw.flaky-infra on ?scale=graph", () => {
  it("model ⇄ the four tools, the strip names the failing ones, inventory_api walks its two calls", async () => {
    serve(FLAKY)
    const { router } = renderApp(`/runs/${FLAKY.id}?scale=graph`)
    await waitFor(() => expect(loopOf()).not.toBeNull())
    // In call order (weft.tool.seq), though tax_service's span starts
    // first; inventory_api's RETRY is a retry (↻), not a failure.
    await waitFor(() =>
      expect(nodes()).toEqual([
        ["t:inventory_api", "2", "0"],
        ["t:shipping_quote", "1", "1"],
        ["t:legacy_billing", "1", "1"],
        ["t:tax_service", "1", "1"],
      ])
    )
    expect(
      Array.from(document.querySelectorAll("[data-insight]")).map(
        (n) => n.textContent
      )
    ).toEqual([
      "shipping_quote failed 1 of 1",
      "legacy_billing failed 1 of 1",
      "tax_service failed 1 of 1",
    ])
    expect(document.querySelector("[data-loop-end]")!.textContent).toContain(
      "the model answered"
    )
    expect(node("t:inventory_api").getAttribute("data-marks")).toBe("×2 ↻")
    // The level switch: loop by default.
    expect(
      document.querySelector("[data-level]")!.getAttribute("data-level")
    ).toBe("loop")
    fireEvent.click(node("t:inventory_api"))
    await waitFor(() =>
      expect(String(router.state.location.search.sel)).toMatch(/^c:0:/)
    )
    await waitFor(() =>
      expect(document.querySelector("[data-call-of]")!.textContent).toBe(
        "call 1 of 2"
      )
    )
    expect(node("t:inventory_api").getAttribute("aria-pressed")).toBe("true")
    fireEvent.click(document.querySelector('[aria-label="next call"]')!)
    await waitFor(() =>
      expect(String(router.state.location.search.sel)).toMatch(/^c:1:/)
    )
    await waitFor(() =>
      expect(document.querySelector("[data-call-of]")!.textContent).toBe(
        "call 2 of 2"
      )
    )
    // The same drawer: the selected row's detail is drawn beside it.
    expect(document.body.textContent).toContain("inventory_api")
    // An insight selects its node.
    fireEvent.click(document.querySelector("[data-insight]")!)
    await waitFor(() =>
      expect(node("t:shipping_quote").getAttribute("aria-pressed")).toBe("true")
    )
  })
})

describe("rw.budget-guards", () => {
  it("the poller: poll_job ×3 with the repeated-call insight, the end loop_detected", async () => {
    serve(POLLER)
    renderApp(`/runs/${POLLER.id}?scale=graph`)
    await waitFor(() => expect(nodes()).toEqual([["t:poll_job", "3", "0"]]))
    expect(
      document.querySelector("[data-loop-end]")!.getAttribute("data-loop-end")
    ).toBe("loop_detected")
    expect(
      Array.from(document.querySelectorAll("[data-insight]")).map(
        (n) => n.textContent
      )
    ).toEqual(["ended by loop detected", "poll_job ×3 same args"])
  })
  it("the worker ends max_steps; call_1 twice with {n:1} / {n:2} is no repeat", async () => {
    serve(WORKER)
    renderApp(`/runs/${WORKER.id}?scale=graph`)
    await waitFor(() =>
      expect(
        document.querySelector("[data-loop-end]")?.getAttribute("data-loop-end")
      ).toBe("max_steps")
    )
    expect(document.querySelector("[data-loop-end]")!.textContent).toContain(
      "max steps reached"
    )
    await waitFor(() => expect(nodes()).toEqual([["t:record_step", "2", "0"]]))
    expect(
      Array.from(document.querySelectorAll("[data-insight]")).map(
        (n) => n.textContent
      )
    ).toEqual(["ended by max steps reached"])
  })
})

describe("rw.support-desk", () => {
  it("lookup_order above issue_refund ⏸ with a dashed result edge; the run parked", async () => {
    serve(DESK_PARKED)
    renderApp(`/runs/${DESK_PARKED.id}?scale=graph`)
    await waitFor(() =>
      expect(nodes().map((n) => n[0])).toEqual([
        "t:lookup_order",
        "t:issue_refund",
      ])
    )
    expect(node("t:issue_refund").getAttribute("data-parked")).toBe("1")
    expect(node("t:issue_refund").textContent).toContain("⏸")
    const back = document.querySelector(
      '[data-loop-edge="model>t:issue_refund"] [data-loop-result]'
    )!
    expect(back.getAttribute("data-loop-result")).toBe("parked")
    expect(back.getAttribute("stroke-dasharray")).toBe("4 3")
    expect(
      document.querySelector("[data-loop-end]")!.getAttribute("data-loop-end")
    ).toBe("parked")
  })
  it("the resume ends output_submitted", async () => {
    serve(DESK_RESUME)
    renderApp(`/runs/${DESK_RESUME.id}?scale=graph`)
    await waitFor(() =>
      expect(
        document.querySelector("[data-loop-end]")?.getAttribute("data-loop-end")
      ).toBe("output_submitted")
    )
    expect(
      document.querySelector("[data-loop-end]")!.getAttribute("data-word")
    ).toBe("typed output submitted")
    expect(nodes().map((n) => n[0])).toEqual([
      "t:issue_refund",
      "t:submit_output",
    ])
  })
})

describe("rw.tool-storm", () => {
  it("the scrubber on the fan-out step lights its calls and dims every other step", async () => {
    serve(STORM)
    const { router } = renderApp(`/runs/${STORM.id}?scale=graph`)
    await waitFor(() => expect(nodes()[0]).toEqual(["t:get_weather", "6", "0"]))
    fireEvent.click(document.querySelector('[data-loop-step="0"]')!)
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ step: 0 })
    )
    await waitFor(() =>
      expect(
        Array.from(document.querySelectorAll("[data-loop-edge][data-lit]")).map(
          (e) => e.getAttribute("data-loop-edge")
        )
      ).toEqual([
        "model>t:get_weather",
        "model>t:ledger_append",
        "model>t:forecast_report",
      ])
    )
    // The six calls ride one edge, ×6 shown while it is lit.
    expect(
      document.querySelector(
        '[data-loop-edge="model>t:get_weather"] [data-edge-label]'
      )!.textContent
    ).toBe("×6")
    fireEvent.click(document.querySelector('[data-loop-step="1"]')!)
    await waitFor(() =>
      expect(
        document.querySelectorAll("[data-loop-edge][data-lit]")
      ).toHaveLength(0)
    )
    expect(
      document
        .querySelector('[data-loop-edge="model>t:get_weather"]')!
        .getAttribute("opacity")
    ).toBe("0.3")
    // J / K walk the scrubber on the graph.
    fireEvent.keyDown(window, { key: "k", bubbles: true })
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ step: 0 })
    )
  })
})

describe("rw.deep-delegation", () => {
  it("ops-manager expands in place with its breadcrumb; level=agents is the call graph", async () => {
    serve(DIRECTOR)
    const { router } = renderApp(`/runs/${DIRECTOR.id}?scale=graph`)
    await waitFor(() => expect(nodes()).toEqual([["t:ops_manager", "1", "0"]]))
    expect(node("t:ops_manager").getAttribute("data-agent")).toBe("ops-manager")
    const ops = "r_dir/0/call_1"
    fireEvent.click(
      document.querySelector(`[data-loop-expand="${CSS.escape(ops)}"]`)!
    )
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ sel: `r:${ops}` })
    )
    await waitFor(() =>
      expect(loopOf()!.getAttribute("data-loop-graph")).toBe(ops)
    )
    expect(document.querySelector("[data-loop-crumbs]")!.textContent).toBe(
      "director›ops-manager"
    )
    expect(nodes()).toEqual([["t:inventory_specialist", "1", "0"]])
    // The prompt node is the child's own run row: selected.
    expect(node("prompt").getAttribute("aria-pressed")).toBe("true")
    // The breadcrumb steps back up.
    fireEvent.click(document.querySelector("[data-loop-crumb]")!)
    await waitFor(() =>
      expect(loopOf()!.getAttribute("data-loop-graph")).toBe(DIRECTOR.id)
    )
    // The agents level: today's call graph.
    fireEvent.click(
      Array.from(
        document.querySelectorAll<HTMLElement>("[data-level] button")
      ).find((b) => b.textContent === "agents")!
    )
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ level: "agents" })
    )
    await waitFor(() =>
      expect(document.querySelector("[data-call-graph]")).not.toBeNull()
    )
    expect(loopOf()).toBeNull()
  })
})

describe("a copied URL, the keyboard, axe", () => {
  it("restores scale, level, step and selection", async () => {
    const b = serve(FLAKY)
    const second = b.fold.steps[1].toolCalls[0].callId
    const { router } = renderApp(
      `/runs/${FLAKY.id}?scale=graph&step=1&sel=${encodeURIComponent(`c:1:${second}`)}`
    )
    await waitFor(() =>
      expect(document.querySelector("[data-call-of]")?.textContent).toBe(
        "call 2 of 2"
      )
    )
    expect(node("t:inventory_api").getAttribute("aria-pressed")).toBe("true")
    expect(
      document
        .querySelector('[data-loop-step="1"]')!
        .getAttribute("aria-pressed")
    ).toBe("true")
    expect(router.state.location.search).toMatchObject({
      scale: "graph",
      step: 1,
    })
    cleanup()
    serve(DIRECTOR)
    renderApp(`/runs/${DIRECTOR.id}?scale=graph&level=agents`)
    await waitFor(() =>
      expect(document.querySelector("[data-call-graph]")).not.toBeNull()
    )
  })

  it("one tab stop; the arrows reach every node; Enter selects, Esc clears; axe finds nothing", async () => {
    serve(FLAKY)
    const { router } = renderApp(`/runs/${FLAKY.id}?scale=graph`)
    await waitFor(() => expect(nodes()).toHaveLength(4))
    const all = Array.from(
      document.querySelectorAll<SVGGElement>("[data-loop-node]")
    )
    expect(all.filter((n) => n.getAttribute("tabindex") === "0")).toHaveLength(
      1
    )
    const seen = new Set<string>()
    let at = node("prompt")
    at.focus()
    const key = (k: string) => {
      fireEvent.keyDown(document.activeElement!, { key: k })
      at = document.activeElement as SVGGElement
      seen.add(at.getAttribute("data-loop-node")!)
    }
    seen.add("prompt")
    key("ArrowRight") // model
    key("ArrowRight") // the first tool
    for (let i = 0; i < 4; i++) key("ArrowDown")
    key("End")
    expect([...seen].sort()).toEqual(
      all.map((n) => n.getAttribute("data-loop-node")!).sort()
    )
    // ← from a tool returns to the model.
    node("t:tax_service").focus()
    key("ArrowLeft")
    expect(at.getAttribute("data-loop-node")).toBe("model")
    key("Enter")
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ sel: "s0" })
    )
    key("Escape")
    await waitFor(() => expect(router.state.location.search.sel).toBe(NO_SEL))
    const res = await axe.run(loopOf()!)
    expect(
      res.violations.map(
        (v) =>
          `${v.id}: ${v.nodes.map((n) => n.html.slice(0, 120)).join(" | ")}`
      )
    ).toEqual([])
  })

  it("a run without spans: the loop from its fold", async () => {
    serve(DESK_PARKED, { spans: false })
    renderApp(`/runs/${DESK_PARKED.id}?scale=graph`)
    await waitFor(() =>
      expect(nodes().map((n) => n[0])).toEqual([
        "t:lookup_order",
        "t:issue_refund",
      ])
    )
    expect(node("t:issue_refund").getAttribute("data-parked")).toBe("1")
  })
})

describe("U34-fix", () => {
  const ops = "r_dir/0/call_1"

  it("a child loop: its scrubber selects its own step rows (?step= stays the root's); its catalog is a not_recorded badge", async () => {
    serve(DIRECTOR)
    const { router } = renderApp(
      `/runs/${DIRECTOR.id}?scale=graph&step=1&sel=${encodeURIComponent(`r:${ops}`)}`
    )
    await waitFor(() =>
      expect(loopOf()?.getAttribute("data-loop-graph")).toBe(ops)
    )
    // The root's ?step=1 is not the child's step.
    expect(
      document.querySelectorAll("[data-loop-step][aria-pressed='true']")
    ).toHaveLength(0)
    fireEvent.click(document.querySelector('[data-loop-step="1"]')!)
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({
        sel: `s:${ops}:1`,
        step: 1,
      })
    )
    await waitFor(() =>
      expect(
        document
          .querySelector('[data-loop-step="1"]')!
          .getAttribute("aria-pressed")
      ).toBe("true")
    )
    expect(loopOf()!.getAttribute("data-loop-graph")).toBe(ops)
    // K walks the child's steps, through its rows.
    fireEvent.keyDown(window, { key: "k", bubbles: true })
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({
        sel: `s:${ops}:0`,
      })
    )
    // Clearing the step selects the child's own run row.
    fireEvent.click(document.querySelector('[data-loop-step="0"]')!)
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ sel: `r:${ops}` })
    )
    const hole = document.querySelector(
      "[data-loop-offered-hole] [data-hole='not_recorded']"
    )!
    expect(hole.textContent).toContain("not recorded")
    expect(hole.textContent).toContain("subagent's request catalog")
  })

  it("without spans the agents level is off, with a not_recorded badge; ?level=agents reads the loop", async () => {
    serve(DIRECTOR, { spans: false })
    renderApp(`/runs/${DIRECTOR.id}?scale=graph&level=agents`)
    await waitFor(() => expect(nodes()).toEqual([["t:ops_manager", "1", "0"]]))
    expect(document.querySelector("[data-call-graph]")).toBeNull()
    expect(document.body.textContent).not.toContain("one agent, no subagents")
    const agents = Array.from(
      document.querySelectorAll<HTMLButtonElement>("[data-level] button")
    ).find((b) => b.textContent === "agents")!
    expect(agents.disabled).toBe(true)
    await waitFor(() =>
      expect(
        document.querySelector("[data-level-hole] [data-hole='not_recorded']")
      ).not.toBeNull()
    )
  })

  it("the end node, an edge and the never-called insight select", async () => {
    serve(FLAKY)
    const { router } = renderApp(`/runs/${FLAKY.id}?scale=graph`)
    await waitFor(() => expect(nodes()).toHaveLength(4))
    fireEvent.click(node("end"))
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ sel: "s2:m" })
    )
    await waitFor(() =>
      expect(node("end").getAttribute("aria-pressed")).toBe("true")
    )
    expect(node("model").getAttribute("aria-pressed")).toBe("false")
    // An edge selects the first call along it.
    fireEvent.click(
      document.querySelector('[data-loop-edge="model>t:tax_service"]')!
    )
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ sel: "c:0:call_4" })
    )
    await waitFor(() =>
      expect(node("t:tax_service").getAttribute("aria-pressed")).toBe("true")
    )
  })

  it("the never-called insight focuses and marks its node (no row to select)", () => {
    const b = build(DESK_PARKED)
    const rows = treeRows(
      { runId: DESK_PARKED.id, spans: b.spans, fold: b.fold },
      "time"
    ).rows
    const g = loopGraph(rows, DESK_PARKED.id, {
      stoppedBy: "parked",
      pending: b.pending,
      catalog: [
        { name: "lookup_order" },
        { name: "issue_refund" },
        { name: "search_docs" },
      ],
    })
    const onSelect = vi.fn()
    render(
      <LoopGraphView
        graph={g}
        chain={[]}
        depth={0}
        onSelect={onSelect}
        onClear={() => {}}
        onStep={() => {}}
        onFocusRun={() => {}}
        openRun={(r) => `/runs/${r}`}
      />
    )
    const ins = document.querySelector<HTMLElement>("[data-insight='never']")!
    expect(ins.textContent).toBe("search_docs never called")
    fireEvent.click(ins)
    const o = node("o:search_docs")
    expect(document.activeElement).toBe(o)
    expect(o.getAttribute("aria-pressed")).toBe("true")
    expect(onSelect).not.toHaveBeenCalled()
  })

  it("subagent nodes carry the child run's own cost: known, or — with the reason; the agents level too", async () => {
    let release = () => {}
    const held = new Promise<void>((r) => (release = r))
    serve(DIRECTOR, {
      caps: ["pricing"],
      holdDocs: { [ops]: held },
      rows: {
        [ops]: { cost_usd: 0.0125, cost_known: true, cache: "exact" },
        [`${ops}/0/call_1`]: {
          cost_usd: null,
          cost_known: false,
          cost_badge: "unpriced",
          cost_reason: "no price for acme/m1",
        },
      },
    })
    const { router } = renderApp(`/runs/${DIRECTOR.id}?scale=graph`)
    await waitFor(() =>
      expect(
        document.querySelector(
          "[data-loop-node='t:ops_manager'] [data-loop-cost]"
        )
      ).not.toBeNull()
    )
    const known = document.querySelector(
      "[data-loop-node='t:ops_manager'] [data-loop-cost]"
    )!
    expect(known.getAttribute("data-cost-known")).toBe("true")
    expect(known.textContent).toContain("$0.0125")
    // Into ops-manager's loop: its specialist is unpriced.
    fireEvent.click(
      document.querySelector(`[data-loop-expand="${CSS.escape(ops)}"]`)!
    )
    // "—" not read yet while ops-manager's document is in flight (the
    // read that carries its children's rows, U34-fix3)…
    await waitFor(() =>
      expect(
        document.querySelector(
          "[data-loop-node='t:inventory_specialist'] [data-loop-cost] title"
        )?.textContent
      ).toBe(COST_NOT_READ_YET)
    )
    // …until it answers and names its row.
    release()
    await waitFor(() =>
      expect(
        document.querySelector(
          "[data-loop-node='t:inventory_specialist'] [data-loop-cost] title"
        )?.textContent
      ).toBe("no price for acme/m1")
    )
    const unknown = document.querySelector(
      "[data-loop-node='t:inventory_specialist'] [data-loop-cost]"
    )!
    expect(unknown.getAttribute("data-cost-known")).toBe("false")
    expect(unknown.textContent).toContain("—")
    expect(unknown.querySelector("title")!.textContent).toBe(
      "no price for acme/m1"
    )
    // The agents level: each agent's own.
    void router.navigate({
      to: "/runs/$id",
      params: { id: DIRECTOR.id },
      search: { scale: "graph", level: "agents" },
    })
    await waitFor(() =>
      expect(document.querySelectorAll("[data-graph-cost]").length).toBe(3)
    )
    expect(
      document.querySelector("[data-node='a:ops-manager'] [data-graph-cost]")!
        .textContent
    ).toContain("$0.0125")
    // The grandchild's row is not read with nothing selected under it,
    // and no read in flight would carry it: "—" not read on this page
    // (U34-fix3), never "yet", never nothing, never a guess.
    const unread = document.querySelector(
      "[data-node='a:inventory-specialist'] [data-graph-cost]"
    )!
    expect(unread.textContent).toContain("—")
    expect(unread.querySelector("title")!.textContent).toBe(COST_NOT_READ_HERE)
  })

  it("the agents level: each agent's steps and status word in its role — a failed and a parked subagent read so (U34-fix4, UX-S3-14)", async () => {
    serve(TEAM)
    renderApp(`/runs/${TEAM.id}?scale=graph&level=agents`)
    await waitFor(() =>
      expect(
        document.querySelectorAll("[data-call-graph] [data-node]").length
      ).toBe(3)
    )
    const at = (id: string) =>
      document.querySelector<SVGGElement>(`[data-node="${id}"]`)!
    const word = (id: string) => at(id).querySelector("[data-node-word]")!
    // The director succeeded: its word in the succeeded role, no status frame.
    expect(at("a:director").getAttribute("data-node-status")).toBe("succeeded")
    expect(
      at("a:director").querySelector("[data-node-line]")!.textContent
    ).toBe("succeeded · 2 steps ")
    expect(word("a:director").getAttribute("class")).toBe("fill-status-ok")
    expect(
      at("a:director").querySelector("[data-node-frame]")!.getAttribute("class")
    ).not.toContain("stroke-status")
    // The researcher failed: the failed word, its name and frame red.
    expect(at("a:researcher").getAttribute("data-node-status")).toBe("failed")
    expect(
      at("a:researcher").querySelector("[data-node-line]")!.textContent
    ).toBe("failed · 3 steps ")
    expect(word("a:researcher").getAttribute("class")).toBe("fill-status-bad")
    expect(
      at("a:researcher")
        .querySelector("[data-node-frame]")!
        .getAttribute("class")
    ).toContain("stroke-status-bad")
    // The approver parked a call: the parked word, in the parked role.
    expect(at("a:approver").getAttribute("data-node-status")).toBe("parked")
    expect(
      at("a:approver").querySelector("[data-node-line]")!.textContent
    ).toBe("parked · 1 step ")
    expect(word("a:approver").getAttribute("class")).toBe("fill-status-parked")
    expect(
      at("a:approver").querySelector("[data-node-frame]")!.getAttribute("class")
    ).toContain("stroke-status-parked")
    // The name says it too, in the visible words' order.
    expect(at("a:researcher").getAttribute("aria-label")).toContain(
      "failed · 3 steps"
    )
  })

  it('a child document whose read fails is not in flight: its rows say the failure, never "not read yet" (U34-fix4)', async () => {
    serve(DIRECTOR, { caps: ["pricing"], failDocs: [ops] })
    renderApp(`/runs/${DIRECTOR.id}?scale=graph`)
    await waitFor(() =>
      expect(
        document.querySelector(`[data-loop-expand="${CSS.escape(ops)}"]`)
      ).not.toBeNull()
    )
    fireEvent.click(
      document.querySelector(`[data-loop-expand="${CSS.escape(ops)}"]`)!
    )
    // ops-manager's document answers a 500: the specialist's row it
    // would carry is the failure's words, and stays so.
    await waitFor(() =>
      expect(
        document.querySelector(
          "[data-loop-node='t:inventory_specialist'] [data-loop-cost] title"
        )?.textContent
      ).toBe(COST_READ_FAILED)
    )
    await new Promise((r) => setTimeout(r, 50))
    expect(
      document.querySelector(
        "[data-loop-node='t:inventory_specialist'] [data-loop-cost] title"
      )?.textContent
    ).toBe(COST_READ_FAILED)
    expect(COST_READ_FAILED).not.toContain("yet")
  })

  it("the draw's budget: a 1,000-step run's model and render, with CI margin", () => {
    const r = longRun(1000)
    const b = build(r)
    const input = {
      runId: r.id,
      status: "failed",
      spans: b.spans,
      fold: b.fold,
      expanded: ALL_RUNS,
    }
    const make = () =>
      loopGraph(treeRows(input, "time").rows, r.id, { stoppedBy: "max_steps" })
    // The model (lib/loopgraph.test.ts's budget): calibrated against a
    // fixed baseline timed beside it (src/test/budget.ts) — the ratio
    // the spec's 50 ms implies, plus a 250 ms ceiling — so a loaded
    // machine slows both sides, never the verdict.
    let g = make()
    const budget = measure(() => (g = make()), 50)
    const t1 = performance.now()
    render(
      <LoopGraphView
        graph={g}
        chain={[]}
        depth={0}
        step={500}
        onSelect={() => {}}
        onClear={() => {}}
        onStep={() => {}}
        onFocusRun={() => {}}
        openRun={(x) => `/runs/${x}`}
      />
    )
    const draw = performance.now() - t1
    expect(document.querySelectorAll("[data-loop-step]")).toHaveLength(1000)
    const v = withinBudget(budget)
    expect(v.ok, v.why).toBe(true)
    // jsdom draws far slower than a browser: a generous bound, scaled
    // by the load the baseline measured (never below 1,500 ms).
    expect(draw).toBeLessThan(1500 * Math.max(1, budget.base / BASELINE_MS))
    // The test's own timeout is not the budget (as loopgraph.test.ts).
  }, 60_000)
})

describe("U34-fix2", () => {
  const ops = "r_dir/0/call_1"
  const pressedNodes = () =>
    Array.from(
      document.querySelectorAll("[data-loop-node][aria-pressed='true']")
    ).map((n) => n.getAttribute("data-loop-node"))

  it("a picked never-called node clears on every other selection: one node pressed, never two", () => {
    const b = build(DESK_PARKED)
    const rows = treeRows(
      { runId: DESK_PARKED.id, spans: b.spans, fold: b.fold },
      "time"
    ).rows
    const g = loopGraph(rows, DESK_PARKED.id, {
      stoppedBy: "parked",
      pending: b.pending,
      catalog: [
        { name: "lookup_order" },
        { name: "issue_refund" },
        { name: "search_docs" },
      ],
    })
    const onSelect = vi.fn()
    const onClear = vi.fn()
    const view = (selectedNode?: string) => (
      <LoopGraphView
        graph={g}
        chain={[]}
        depth={0}
        selectedNode={selectedNode}
        onSelect={onSelect}
        onClear={onClear}
        onStep={() => {}}
        onFocusRun={() => {}}
        openRun={(r) => `/runs/${r}`}
      />
    )
    // The page's landing selection lights the model.
    const { rerender } = render(view("model"))
    expect(pressedNodes()).toEqual(["model"])
    // The insight picks the never-called node: it alone is pressed.
    fireEvent.click(document.querySelector("[data-insight='never']")!)
    expect(pressedNodes()).toEqual(["o:search_docs"])
    // A tool's click selects its row and clears the pick at once — even
    // before the page's ?sel= moves.
    fireEvent.click(node("t:lookup_order"))
    expect(onSelect).toHaveBeenCalledTimes(1)
    expect(pressedNodes()).toEqual(["model"])
    rerender(view("t:lookup_order"))
    expect(pressedNodes()).toEqual(["t:lookup_order"])
    // Picked again, then the page's selection moves (J / K, a link):
    // the pick clears.
    fireEvent.click(node("o:search_docs"))
    expect(pressedNodes()).toEqual(["o:search_docs"])
    rerender(view("end"))
    expect(pressedNodes()).toEqual(["end"])
    // Picked again, then the end, an edge and Esc each clear it.
    fireEvent.click(node("o:search_docs"))
    fireEvent.click(node("end"))
    expect(pressedNodes()).toEqual(["end"])
    fireEvent.click(node("o:search_docs"))
    fireEvent.click(
      document.querySelector('[data-loop-edge="model>t:issue_refund"]')!
    )
    expect(pressedNodes()).toEqual(["end"])
    fireEvent.click(node("o:search_docs"))
    fireEvent.keyDown(node("o:search_docs"), { key: "Escape" })
    expect(onClear).toHaveBeenCalledTimes(1)
    expect(pressedNodes()).toEqual(["end"])
  })

  it("a failed requests read is said as such, never 'no request record carried a tool catalog'", async () => {
    serve(FLAKY, { requestsFail: true })
    renderApp(`/runs/${FLAKY.id}?scale=graph`)
    await waitFor(() => expect(nodes()).toHaveLength(4))
    // Words in text-status-bad, as the attempts pane says a failed read
    // — no badge: the A3 table's gap is a dropped record (U34-fix3).
    const said = await waitFor(() => {
      const h = document.querySelector("[data-loop-offered-hole]")
      expect(h).not.toBeNull()
      return h!
    })
    expect(said.hasAttribute("data-read-failed")).toBe(true)
    expect(said.className).toContain("text-status-bad")
    expect(said.querySelector("[data-hole]")).toBeNull()
    const words = said.textContent
    expect(words).toContain("the request record could not be read: ")
    expect(words).toContain("database is locked")
    expect(words).not.toContain("no request record of this run carried")
  })

  it("the agents level: a failed spans read is a read failure, never 'this run recorded none'", async () => {
    serve(DIRECTOR, { spansFail: true })
    renderApp(`/runs/${DIRECTOR.id}?scale=graph&level=agents`)
    const said = await waitFor(() => {
      const h = document.querySelector("[data-level-hole]")
      expect(h).not.toBeNull()
      return h!
    })
    expect(said.hasAttribute("data-read-failed")).toBe(true)
    expect(said.className).toContain("text-status-bad")
    expect(said.querySelector("[data-hole]")).toBeNull()
    const words = said.textContent
    expect(words).toContain("the run's spans could not be read: ")
    expect(words).toContain("database is locked")
    expect(words).not.toContain("recorded none")
    expect(document.querySelector("[data-call-graph]")).toBeNull()
  })

  it("the agents level: 'recorded none' on a successful empty read", async () => {
    // A trace id, and its spans read back empty: the read succeeded.
    serve(DIRECTOR, { spans: false, trace: true })
    renderApp(`/runs/${DIRECTOR.id}?scale=graph&level=agents`)
    await waitFor(() =>
      expect(
        document.querySelector("[data-level-hole]")?.textContent
      ).toContain("recorded none")
    )
  })

  it("the agents level: no 'recorded none' while the page still polls for late spans; said once the 30 s pass", async () => {
    // A cold open of a run that ended a second ago (the runs list →
    // click flow): its spans may still be batching (OTLP), so the page
    // polls them for 30 s after the end — an empty answer inside that
    // window is not "recorded none" (U34-fix3).
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      serve(DIRECTOR, {
        spans: false,
        trace: true,
        rows: {
          [DIRECTOR.id]: {
            finished: new Date(Date.now() - 1000).toISOString(),
          },
        },
      })
      renderApp(`/runs/${DIRECTOR.id}?scale=graph&level=agents`)
      await waitFor(() => expect(nodes().length).toBeGreaterThan(0))
      // Spans read (empty) at least once, still inside the window.
      await vi.advanceTimersByTimeAsync(5000)
      expect(document.querySelector("[data-level-hole]")).toBeNull()
      // The window closes: the poll stops and the badge says it.
      await vi.advanceTimersByTimeAsync(30_000)
      await waitFor(() =>
        expect(
          document.querySelector("[data-level-hole]")?.textContent
        ).toContain("recorded none")
      )
    } finally {
      vi.useRealTimers()
    }
  })

  it("subagent nodes not read yet draw — with the reason, never nothing", () => {
    const b = build(DIRECTOR)
    const rows = treeRows(
      {
        runId: DIRECTOR.id,
        spans: b.spans,
        fold: b.fold,
        expanded: ALL_RUNS,
      },
      "time"
    ).rows
    const g = loopGraph(rows, DIRECTOR.id, { stoppedBy: "final_answer" })
    render(
      <LoopGraphView
        graph={g}
        chain={[]}
        depth={0}
        // Pricing on, the child's row not read yet.
        costOf={() => undefined}
        onSelect={() => {}}
        onClear={() => {}}
        onStep={() => {}}
        onFocusRun={() => {}}
        openRun={(r) => `/runs/${r}`}
      />
    )
    const cost = document.querySelector(
      "[data-loop-node='t:ops_manager'] [data-loop-cost]"
    )!
    expect(cost.getAttribute("data-cost-known")).toBe("false")
    expect(cost.textContent).toContain("—")
    expect(cost.querySelector("title")!.textContent).toBe(
      "a run's cost is not read yet"
    )
    expect(node("t:ops_manager").getAttribute("aria-label")).toContain("—")
  })

  it("axe finds nothing on the parked support-desk run and the director's subagent loop", async () => {
    const check = async () => {
      const res = await axe.run(loopOf()!.parentElement!.parentElement!, {
        rules: { "label-content-name-mismatch": { enabled: true } },
      })
      expect(
        res.violations.map(
          (v) =>
            `${v.id}: ${v.nodes.map((n) => n.html.slice(0, 120)).join(" | ")}`
        )
      ).toEqual([])
    }
    serve(DESK_PARKED)
    renderApp(`/runs/${DESK_PARKED.id}?scale=graph`)
    await waitFor(() => expect(nodes()).toHaveLength(2))
    await check()
    // Each tool node's name starts with its visible words, in order.
    for (const n of document.querySelectorAll<SVGGElement>(
      "[data-loop-node^='t:']"
    )) {
      const seen = Array.from(n.querySelectorAll("text"))
        .map((t) =>
          Array.from(t.childNodes)
            .filter(
              (c) =>
                c.nodeType === Node.TEXT_NODE ||
                (c instanceof Element && !c.hasAttribute("aria-hidden"))
            )
            .map((c) => (c.nodeName === "title" ? "" : c.textContent))
            .join("")
        )
        .join("")
        .replace(/\s+/g, " ")
        .trim()
      expect(n.getAttribute("aria-label")!.replace(/\s+/g, " ")).toBe(seen)
    }
    cleanup()
    serve(DIRECTOR, { caps: ["pricing"] })
    renderApp(`/runs/${DIRECTOR.id}?scale=graph`)
    await waitFor(() => expect(nodes()).toHaveLength(1))
    await check()
    fireEvent.click(
      document.querySelector(`[data-loop-expand="${CSS.escape(ops)}"]`)!
    )
    await waitFor(() =>
      expect(loopOf()?.getAttribute("data-loop-graph")).toBe(ops)
    )
    await check()
  })

  it("a text-delta batch on a live run does not rebuild the loop's rows; a tool call does", async () => {
    const b = build(FLAKY)
    // Live up to the second step's first tool_start.
    const cut = b.events.findIndex(
      (e, i) =>
        e.type === "tool_start" &&
        b.events.slice(0, i).some((x) => x.type === "step_finish")
    )
    expect(cut).toBeGreaterThan(0)
    serve(FLAKY, { live: cut })
    renderApp(`/runs/${FLAKY.id}?scale=graph`)
    await waitFor(() => expect(nodes().length).toBeGreaterThan(0))
    await waitFor(() => expect(FakeEventSource.open()).toHaveLength(1))
    const es = FakeEventSource.open()[0]
    es.connect()
    // Let the open's catch-up read settle.
    await new Promise((r) => setTimeout(r, 50))
    const before = loopBuilds()
    expect(before).toBeGreaterThan(0)
    const rec = (kind: string, pos: number, event: unknown) =>
      es.emit(
        "record",
        {
          run_id: FLAKY.id,
          session_id: "",
          public_id: "",
          kind,
          pos,
          time: rOK.started,
          event,
        },
        kind === "event" ? String(pos) : ""
      )
    for (let d = 0; d < 40; d++)
      rec("delta", d, { type: "text_delta", run_id: FLAKY.id, text: "w " })
    await new Promise((r) => setTimeout(r, 100))
    expect(loopBuilds()).toBe(before)
    // The control: the next tool_start moves the loop — rebuilt.
    rec("event", cut, b.events[cut])
    await waitFor(() => expect(loopBuilds()).toBeGreaterThan(before))
  })
})

// P9-pre-web-fix 16: the graph's keyboard and pointer, end to end.
describe("the loop graph's selection, keyboard and targets", () => {
  const pressed = () =>
    Array.from(document.querySelectorAll("[data-loop-node]"))
      .filter((n) => n.getAttribute("aria-pressed") === "true")
      .map((n) => n.getAttribute("data-loop-node"))

  it("J / K move the selection with the scrubber: no pressed node stays dimmed on the step before", async () => {
    serve(STORM)
    const { router } = renderApp(`/runs/${STORM.id}?scale=graph`)
    await waitFor(() => expect(nodes()[0]).toEqual(["t:get_weather", "6", "0"]))
    // A tool of step 0 pressed, then J to step 0, J again to step 1
    // (which calls none of its tools).
    fireEvent.click(node("t:get_weather"))
    await waitFor(() => expect(pressed()).toEqual(["t:get_weather"]))
    fireEvent.keyDown(window, { key: "j", bubbles: true })
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({
        step: 0,
        sel: "s0",
      })
    )
    fireEvent.keyDown(window, { key: "j", bubbles: true })
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({
        step: 1,
        sel: "s1",
      })
    )
    // The step's own row: the model pressed, never a dimmed tool.
    await waitFor(() => expect(pressed()).toEqual(["model"]))
    expect(node("t:get_weather").getAttribute("opacity")).toBe("0.3")
    fireEvent.keyDown(window, { key: "k", bubbles: true })
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({
        step: 0,
        sel: "s0",
      })
    )
  })

  it("Esc means no selection: the landing rule never picks a row again until the next pick", async () => {
    serve(FLAKY)
    const { router } = renderApp(`/runs/${FLAKY.id}?scale=graph`)
    await waitFor(() => expect(nodes()).toHaveLength(4))
    // The landing rule pressed a node.
    await waitFor(() => expect(pressed()).toHaveLength(1))
    node("model").focus()
    fireEvent.keyDown(node("model"), { key: "Escape" })
    await waitFor(() => expect(router.state.location.search.sel).toBe(NO_SEL))
    await new Promise((r) => setTimeout(r, 50))
    expect(pressed()).toEqual([])
    // The next pick selects again.
    fireEvent.click(node("t:tax_service"))
    await waitFor(() => expect(pressed()).toEqual(["t:tax_service"]))
  })

  it("an edge is a 24 px click target selecting its first call; the tab stop starts on the selected node", async () => {
    const b = serve(FLAKY)
    const second = b.fold.steps[1].toolCalls[0].callId
    const { router } = renderApp(
      `/runs/${FLAKY.id}?scale=graph&sel=${encodeURIComponent(`c:1:${second}`)}`
    )
    await waitFor(() => expect(pressed()).toEqual(["t:inventory_api"]))
    // The roving stop is the selected node, not the prompt.
    const stops = Array.from(
      document.querySelectorAll("[data-loop-node][tabindex='0']")
    ).map((n) => n.getAttribute("data-loop-node"))
    expect(stops).toEqual(["t:inventory_api"])
    const hit = document.querySelector<SVGPathElement>(
      '[data-loop-edge="model>t:shipping_quote"] [data-loop-edge-hit]'
    )!
    expect(Number(hit.getAttribute("stroke-width"))).toBeGreaterThanOrEqual(24)
    expect(hit.getAttribute("pointer-events")).toBe("stroke")
    fireEvent.click(hit)
    await waitFor(() => expect(pressed()).toEqual(["t:shipping_quote"]))
    expect(router.state.location.search.sel).toMatch(/^c:/)
    // The model → end edge has one too.
    expect(
      document.querySelector(
        '[data-loop-edge="model>end"] [data-loop-edge-hit]'
      )
    ).not.toBeNull()
    // Fitted to its pane: never wider than the drawer leaves it.
    expect(
      document.querySelector("[data-loop-svg]")!.getAttribute("width")
    ).toBe("100%")
  })

  it("the agents level is one tab stop, the arrows walk it", async () => {
    serve(DIRECTOR)
    renderApp(`/runs/${DIRECTOR.id}?scale=graph&level=agents`)
    await waitFor(() =>
      expect(
        document.querySelectorAll("[data-call-graph] [data-node]").length
      ).toBeGreaterThan(1)
    )
    const all = Array.from(
      document.querySelectorAll<SVGGElement>("[data-call-graph] [data-node]")
    )
    expect(all.filter((n) => n.getAttribute("tabindex") === "0")).toHaveLength(
      1
    )
    const first = all.find((n) => n.getAttribute("tabindex") === "0")!
    first.focus()
    fireEvent.keyDown(first, { key: "End" })
    expect(document.activeElement).not.toBe(first)
    fireEvent.keyDown(document.activeElement!, { key: "Home" })
    expect(document.activeElement).toBe(first)
    expect(all.filter((n) => n.getAttribute("tabindex") === "0")).toHaveLength(
      1
    )
    expect(
      document.querySelector("[data-call-graph] svg")!.getAttribute("width")
    ).toBe("100%")
  })
})
