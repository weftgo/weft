// U8.2's Done line: the run page's default view is the run tree
// (lib/waterfall.ts's waterfallRows) — on rw.deep-delegation's director
// run it shows two steps, one tool lane, one collapsed child row and the
// critical path, model calls labelled "model · script", durations under
// "ms"; ?scale=order works on a run without spans (UX-A-02: the order
// scale round-trips, the dead "events" button is gone).
import { cleanup, configure, fireEvent, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc, RunRow, RunsPage } from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { DIR, EVENTS, OPS, REPORT, SPANS } from "@/test/director-fixture"
import { FakeEventSource } from "@/test/fake-event-source"
import {
  FakeStudio,
  golden,
  pagedEvents,
  transcriptOf,
} from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!

let studio: FakeStudio

function serve(opts: { spans: boolean; tree?: boolean }) {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  const child: RunRow = {
    ...rOK,
    id: OPS,
    agent: "ops-manager",
    parent_run_id: DIR,
    parent_call_id: "call_1",
  }
  const doc: RunDoc = {
    ...rOK,
    id: DIR,
    agent: "director",
    steps: 2,
    trace_id: opts.spans ? "t1" : "",
    children: [child],
    holes: [],
  }
  // The ?tree=1 shape: each span names its run (run_id) beside the
  // weft.run.id attribute; without the tree, the run's own spans only.
  const tree = SPANS.map((s) => ({
    ...s,
    run_id: String(s.attrs["weft.run.id"] ?? DIR),
  }))
  studio = new FakeStudio()
    .on("GET meta", {
      ...golden<Record<string, unknown>>("meta"),
      capabilities: opts.tree ? ["ingest", "spans_tree"] : ["ingest"],
    })
    .on(`GET runs/${DIR}`, doc)
    .on(
      `GET runs/${DIR}/events`,
      pagedEvents(
        EVENTS.map((event, pos) => ({ pos, time: rOK.started, event })),
        { done: true }
      )
    )
    .on(`GET runs/${DIR}/transcript`, transcriptOf([]))
    .on(`GET runs/${DIR}/spans`, (req) => ({
      spans: !opts.spans
        ? []
        : req.query.get("tree") === "1"
          ? tree
          : tree.filter((s) => s.run_id === DIR),
    }))
    .install()
}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

const rows = () =>
  Array.from(document.querySelectorAll<HTMLElement>("[data-span]"))
const keys = () => rows().map((n) => n.getAttribute("data-span"))
const row = (key: string) =>
  document.querySelector<HTMLElement>(`[data-span="${CSS.escape(key)}"]`)!
const selected = () =>
  rows()
    .filter((n) => n.getAttribute("aria-selected") === "true")
    .map((n) => n.getAttribute("data-span"))

describe("the run tree on the director run (the Done line)", () => {
  it("time scale: two steps, one tool lane, one collapsed child row, the critical path", async () => {
    serve({ spans: true, tree: true })
    renderApp(`/runs/${DIR}`)
    const child = `r:${OPS}`
    await waitFor(() => expect(keys()).toContain(child))
    // The tree came from ?tree=1 (capability spans_tree).
    expect(
      studio.calls(`GET runs/${DIR}/spans`).map((r) => r.query.get("tree"))
    ).toContain("1")
    expect(keys()).toEqual([
      `r:${DIR}`,
      "s0",
      "s0:m",
      "c:0:call_1",
      "t:d_http",
      child,
      "s1",
      "s1:m",
    ])
    // Two steps, one tool lane.
    expect(rows().filter((n) => /^s\d+$/.test(n.dataset.span!))).toHaveLength(2)
    expect(row("s0").dataset.lanes).toBe("1")
    expect(row("c:0:call_1").dataset.lane).toBe("1")
    // One child row, collapsed: none of ops-manager's own rows drawn, a
    // link to its own page instead.
    const kids = rows().filter((n) => n.dataset.childRun)
    expect(kids.map((n) => n.dataset.span)).toEqual([child])
    expect(keys().some((k) => k!.includes(`${OPS}:`))).toBe(false)
    const open = row(child).querySelector("[data-open-run]")!
    expect(open.textContent).toBe("open run ↗")
    expect(open.getAttribute("href")).toContain(
      `/runs/${encodeURIComponent(OPS)}`
    )
    // The critical path: a serial run marks its whole chain — both
    // steps, the model call, the tool after it, the child inside it;
    // the legend says so.
    const critical = rows()
      .filter((n) => n.hasAttribute("data-critical"))
      .map((n) => n.dataset.span)
    for (const k of [`r:${DIR}`, "s0", "s0:m", "c:0:call_1", child, "s1"])
      expect(critical).toContain(k)
    expect(
      document.querySelector("[data-critical-legend]")!.textContent
    ).toContain("critical path")
    // The label rule (UX-A-04): model calls by their model, tools by
    // name, no OTel span names, no service per row; gists; "ms".
    const tree = document.querySelector('[role="tree"]')!
    expect(tree.getAttribute("aria-label")).toBe("waterfall")
    expect(row("s0:m").textContent).toContain("model · script")
    expect(row("s0:m").textContent).toContain("→ 1 call")
    expect(row("s0").textContent).toContain("1 tool: ops_manager")
    expect(row("c:0:call_1").textContent).toContain("ops_manager")
    expect(tree.textContent).not.toContain("chat script")
    expect(tree.textContent).not.toContain("invoke_agent")
    expect(tree.textContent).not.toContain("weft-arena-dev")
    expect(tree.textContent).toContain("ms")
    expect(tree.textContent).not.toMatch(/\bMSS?\b/)
    // Durations, not ranges: the run's 14 ms.
    expect(row(`r:${DIR}`).textContent).toContain("14.0 ms")
  })

  it("without spans_tree, the run's own spans: the child is still one collapsed row", async () => {
    serve({ spans: true, tree: false })
    renderApp(`/runs/${DIR}`)
    await waitFor(() => expect(keys()).toContain(`r:${OPS}`))
    expect(
      studio.calls(`GET runs/${DIR}/spans`).map((r) => r.query.get("tree"))
    ).not.toContain("1")
    expect(rows().filter((n) => n.dataset.childRun)).toHaveLength(1)
  })

  it("the scale switch writes both values (UX-A-02): order round-trips, then time", async () => {
    serve({ spans: true, tree: true })
    const { router } = renderApp(`/runs/${DIR}`)
    await waitFor(() => expect(keys()).toContain("s1:m"))
    const scale = () => document.querySelector<HTMLElement>("[data-scale]")!
    expect(scale().dataset.scale).toBe("time")
    // The switch is time | order | graph: the dead "events" axis is gone.
    expect(
      Array.from(scale().querySelectorAll("button")).map((b) => b.textContent)
    ).toEqual(["time", "order", "graph"])
    fireEvent.click(
      Array.from(scale().querySelectorAll("button")).find(
        (b) => b.textContent === "order"
      )!
    )
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ scale: "order" })
    )
    await waitFor(() => expect(scale().dataset.scale).toBe("order"))
    // The order scale's rows: positions, the same tree.
    expect(keys()).toEqual([
      `r:${DIR}`,
      "s0",
      "s0:m",
      "c:0:call_1",
      `r:${OPS}`,
      "s1",
      "s1:m",
    ])
    expect(document.querySelector(".waterfall")!.textContent).toContain(
      "events"
    )
    fireEvent.click(
      Array.from(scale().querySelectorAll("button")).find(
        (b) => b.textContent === "time"
      )!
    )
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ scale: "time" })
    )
    await waitFor(() => expect(scale().dataset.scale).toBe("time"))
  })

  it("an old ?axis=events link opens the order scale", async () => {
    serve({ spans: true, tree: true })
    renderApp(`/runs/${DIR}?axis=events`)
    await waitFor(() =>
      expect(
        document.querySelector<HTMLElement>("[data-scale]")?.dataset.scale
      ).toBe("order")
    )
  })

  it("the child row's detail is its tool call's: the result the parent read", async () => {
    serve({ spans: true, tree: true })
    renderApp(`/runs/${DIR}?sel=${encodeURIComponent(`r:${OPS}`)}`)
    await waitFor(() => expect(selected()).toEqual([`r:${OPS}`]))
    await waitFor(() => expect(document.body.textContent).toContain(REPORT))
  })
})

describe("?scale=order on a run without spans", () => {
  it("draws the run tree by stream position: two steps, the tool, the collapsed child, the critical path", async () => {
    serve({ spans: false })
    renderApp(`/runs/${DIR}?scale=order`)
    await waitFor(() => expect(keys()).toContain("s1:m"))
    expect(keys()).toEqual([
      `r:${DIR}`,
      "s0",
      "s0:m",
      "c:0:call_1",
      `r:${OPS}`,
      "s1",
      "s1:m",
    ])
    expect(row("s0").dataset.lanes).toBe("1")
    expect(rows().filter((n) => n.dataset.childRun)).toHaveLength(1)
    expect(
      rows().filter((n) => n.hasAttribute("data-critical")).length
    ).toBeGreaterThan(0)
    // No spans: the switch offers order and the loop graph (U34: the
    // graph reads the fold), and the replay bar (the order scale's
    // playhead) is there.
    expect(
      Array.from(
        document.querySelectorAll<HTMLElement>("[data-scale] button")
      ).map((b) => b.textContent)
    ).toEqual(["order", "graph"])
    expect(document.body.textContent).not.toContain("past the playhead")
    // A ?scale=time without spans reads the order scale too.
    renderApp(`/runs/${DIR}?scale=time`)
    await waitFor(() => expect(keys()).toContain("s1:m"))
    expect(keys()).toContain(`r:${OPS}`)
  })
})
