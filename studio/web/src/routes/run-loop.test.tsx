// U34's Done line (WEFT-LOOP-GRAPH.md §6) on the run page: the graph
// scale opens on the loop — rw.flaky-infra's failing tools, its insight
// strip and inventory_api's ‹ call 1 of 2 ›; rw.budget-guards' repeated
// poll_job and loop_detected end, its worker's max_steps; rw.support-
// desk's parked issue_refund (a dashed result edge) and its resume's
// has_tool_call:submit_output; rw.tool-storm's scrubber lighting the
// fan-out; rw.deep-delegation's subagent expanded in place with its
// breadcrumb, and level=agents' call graph. A copied URL restores the
// scale, level, step and selection; the keyboard reaches every node;
// axe finds nothing.
import { cleanup, configure, fireEvent, waitFor } from "@testing-library/react"
import axe from "axe-core"
import { afterEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc, RunRow, RunsPage, Span } from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import {
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
  POLLER,
  STORM,
  WORKER,
} from "@/test/loop-fixtures"
import type { FxRun } from "@/test/loop-fixtures"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!

/** The child runs a fixture starts, depth first. */
function childrenOf(r: FxRun): FxRun[] {
  return r.steps.flatMap((s) =>
    s.calls.flatMap((c) => (c.child ? [c.child, ...childrenOf(c.child)] : []))
  )
}

function serve(r: FxRun, opts: { spans?: boolean } = {}) {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  const b = build(r)
  const withSpans = opts.spans ?? true
  const kids = childrenOf(r)
  const row = (x: FxRun, parent?: string): RunRow => ({
    ...rOK,
    id: x.id,
    agent: x.agent,
    steps: x.steps.length,
    status: x.failed ? "failed" : "succeeded",
    ...(x.stoppedBy ? { stopped_by: x.stoppedBy } : {}),
    ...(parent
      ? { parent_run_id: parent, parent_call_id: x.id.split("/").at(-1) }
      : {}),
  })
  const parentOf = (id: string) => /^(.+)\/\d+\/[^/]+$/.exec(id)?.[1]
  const doc: RunDoc = {
    ...row(r),
    trace_id: withSpans ? "t1" : "",
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
      capabilities: ["ingest", "spans_tree"],
    })
    .on(`GET runs/${r.id}`, doc)
    .on(`GET runs/${r.id}/events`, paged(b.events))
    .on(`GET runs/${r.id}/transcript`, transcriptOf([]))
    .on(`GET runs/${r.id}/spans`, () => ({
      spans: withSpans ? b.spans : ([] as Span[]),
      holes: [],
    }))
  for (const k of kids)
    s.on(`GET runs/${k.id}`, {
      ...row(k, parentOf(k.id)),
      children: kids
        .filter((x) => parentOf(x.id) === k.id)
        .map((x) => row(x, k.id)),
      holes: [],
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
    await waitFor(() =>
      expect(nodes()).toEqual([
        ["t:inventory_api", "2", "1"],
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
  it("the worker ends max_steps", async () => {
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
  it("the resume ends has_tool_call:submit_output", async () => {
    serve(DESK_RESUME)
    renderApp(`/runs/${DESK_RESUME.id}?scale=graph`)
    await waitFor(() =>
      expect(
        document.querySelector("[data-loop-end]")?.getAttribute("data-loop-end")
      ).toBe("has_tool_call:submit_output")
    )
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
    await waitFor(() =>
      expect(router.state.location.search.sel).toBeUndefined()
    )
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
