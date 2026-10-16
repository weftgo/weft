// U8.2-fix and U9.2-fix on the run page, over rw.deep-delegation's
// director run: one /spans read at load (the tree, once the
// capabilities are known); the t key steps the scale; the dim filters
// round-trip ?dim=; a step no span covers is drawn and badged; the
// whole-trace link always run-qualifies the run's own keys; a running
// child is never badged a loss, a finished one in the server's words;
// an expanded child's stream is read again while it runs, its gaps and
// its read cap badged; the call graph's nodes take a focus ring and
// keep axe clean, a child run's MCP calls said not shown.
import { cleanup, configure, fireEvent, waitFor } from "@testing-library/react"
import axe from "axe-core"
import { afterEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc, RunRow, RunsPage, TreeHole, WireEvent } from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { DIR, EVENTS, INV, OPS, SPANS } from "@/test/director-fixture"
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

const OPS_EVENTS = [
  { type: "run_start", id: OPS, agent: "ops-manager" },
  { type: "step_start", run_id: OPS, index: 0 },
  { type: "step_finish", run_id: OPS, index: 0, reason: "stop" },
  { type: "run_finish", run_id: OPS, steps: 1 },
] as unknown as WireEvent[]

let studio: FakeStudio

function serve(
  opts: {
    holes?: TreeHole[]
    spans?: typeof SPANS
    opsStatus?: RunRow["status"]
    opsEvents?: WireEvent[]
    opsGaps?: number[]
  } = {}
) {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  const ops: RunRow = {
    ...rOK,
    id: OPS,
    agent: "ops-manager",
    status: opts.opsStatus ?? rOK.status,
    parent_run_id: DIR,
    parent_call_id: "call_1",
  }
  const doc: RunDoc = {
    ...rOK,
    id: DIR,
    agent: "director",
    steps: 2,
    trace_id: "t1",
    children: [ops],
    holes: [],
  }
  const tree = (opts.spans ?? SPANS).map((s) => ({
    ...s,
    run_id: String(s.attrs["weft.run.id"] ?? DIR),
  }))
  const paged = (evs: WireEvent[], gaps?: number[]) =>
    pagedEvents(
      evs.map((event, pos) => ({ pos, time: rOK.started, event })),
      { done: true, gaps }
    )
  studio = new FakeStudio()
    .on("GET meta", {
      ...golden<Record<string, unknown>>("meta"),
      capabilities: ["ingest", "spans_tree"],
    })
    .on(`GET runs/${DIR}`, doc)
    .on(`GET runs/${DIR}/events`, paged(EVENTS))
    .on(`GET runs/${DIR}/transcript`, transcriptOf([]))
    .on(`GET runs/${DIR}/spans`, () => ({
      spans: tree,
      holes: opts.holes ?? [],
    }))
    .on(`GET runs/${OPS}`, { ...ops, children: [], holes: [] })
    .on(
      `GET runs/${OPS}/events`,
      paged(opts.opsEvents ?? OPS_EVENTS, opts.opsGaps)
    )
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
const toggle = (key: string) =>
  row(key).querySelector<HTMLButtonElement>(
    'button[aria-label="expand"], button[aria-label="collapse"]'
  )!
const press = (key: string) => fireEvent.keyDown(window, { key, bubbles: true })

describe("the spans read (U8.2-fix #6)", () => {
  it("reads the tree once at load, after the capabilities — never the plain spans first", async () => {
    serve()
    renderApp(`/runs/${DIR}`)
    await waitFor(() => expect(keys()).toContain(`r:${OPS}`))
    // Long enough for a stray first read to have been made.
    await new Promise((r) => setTimeout(r, 300))
    const reads = studio.calls(`GET runs/${DIR}/spans`)
    expect(reads.map((r) => r.query.get("tree"))).toEqual(["1"])
  })
})

describe("the scale key and the dim filters (U8.2-fix #7)", () => {
  it("t steps the scale: time → order → graph → time", async () => {
    serve()
    const { router } = renderApp(`/runs/${DIR}`)
    await waitFor(() => expect(keys()).toContain("s0"))
    press("t")
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ scale: "order" })
    )
    press("t")
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ scale: "graph" })
    )
    press("t")
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ scale: "time" })
    )
  })

  it("the chips write ?dim= and dim (never hide) what no filter keeps; a link restores them", async () => {
    serve()
    const { router } = renderApp(`/runs/${DIR}`)
    await waitFor(() => expect(keys()).toContain("c:0:call_1"))
    const all = keys()
    const chip = (k: string) =>
      document.querySelector<HTMLButtonElement>(`[data-dim-filter="${k}"]`)!
    expect(
      Array.from(document.querySelectorAll("[data-dim-filter]")).map(
        (b) => b.textContent
      )
    ).toEqual(["errors", "slow", "tools"])
    fireEvent.click(chip("tools"))
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ dim: "tools" })
    )
    await waitFor(() =>
      expect(row("s1").hasAttribute("data-dimmed")).toBe(true)
    )
    expect(keys()).toEqual(all)
    // The tool, the child run and the path to them stay lit.
    for (const k of ["c:0:call_1", `r:${OPS}`, "s0", `r:${DIR}`])
      expect(row(k).hasAttribute("data-dimmed")).toBe(false)
    expect(chip("tools").getAttribute("aria-pressed")).toBe("true")
    fireEvent.click(chip("tools"))
    await waitFor(() =>
      expect(router.state.location.search.dim).toBeUndefined()
    )
    await waitFor(() =>
      expect(row("s1").hasAttribute("data-dimmed")).toBe(false)
    )
  })

  it("?dim= reads back, unknown names dropped, written in the chips' order", async () => {
    serve()
    const { router } = renderApp(`/runs/${DIR}?dim=tools,nope,errors`)
    await waitFor(() => expect(keys()).toContain("s1"))
    expect(router.state.location.search).toMatchObject({ dim: "errors,tools" })
    await waitFor(() =>
      expect(row("s1").hasAttribute("data-dimmed")).toBe(true)
    )
  })
})

describe("a step no span covers (U8.2-fix #2)", () => {
  it("a finished run draws it in its place, badged not recorded, no duration", async () => {
    serve({ spans: SPANS.filter((s) => s.span_id !== "d_c1") })
    renderApp(`/runs/${DIR}`)
    await waitFor(() => expect(keys()).toContain("s1"))
    expect(row("s1").getAttribute("data-untimed")).toBe("missing")
    expect(
      row("s1").querySelector('[data-hole="not_recorded"]')!.textContent
    ).toContain("no span covers this row")
    expect(row("s1").textContent).toContain("—")
    expect(row("s0").hasAttribute("data-untimed")).toBe(false)
  })
})

describe("the whole-trace link (U9.2-fix W4)", () => {
  it("run-qualifies the run's own key even for a top-level run", async () => {
    serve()
    renderApp(`/runs/${DIR}?sel=s1`)
    await waitFor(() =>
      expect(row("s1").getAttribute("aria-selected")).toBe("true")
    )
    const a = document.querySelector<HTMLAnchorElement>("[data-trace-link]")!
    expect(new URL(a.href, "http://x").searchParams.get("sel")).toBe(
      `s:${DIR}:1`
    )
  })
})

describe("child rows (U9.2-fix W2, W3)", () => {
  const noSpans: TreeHole = {
    hole: "not_recorded",
    cause: "no_spans",
    reason: "1 descendant run has a row and no span",
    fix: "install a tracer (otel.Install records spans)",
    runs: [OPS],
  }
  const withoutOps = SPANS.filter(
    (s) => !String(s.attrs["weft.run.id"] ?? "").startsWith(OPS)
  )

  it("a finished child with no span is badged in the server's words", async () => {
    serve({ holes: [noSpans], spans: withoutOps })
    renderApp(`/runs/${DIR}`)
    await waitFor(() => expect(keys()).toContain(`r:${OPS}`))
    const badge = row(`r:${OPS}`).querySelector('[data-hole="not_recorded"]')!
    expect(badge.textContent).toContain(
      "1 descendant run has a row and no span"
    )
  })

  it("a running child has no span yet: no loss badge, it reads running", async () => {
    serve({ holes: [noSpans], spans: withoutOps, opsStatus: "running" })
    renderApp(`/runs/${DIR}`)
    await waitFor(() => expect(keys()).toContain(`r:${OPS}`))
    await new Promise((r) => setTimeout(r, 200))
    expect(
      row(`r:${OPS}`).querySelector('[data-hole="not_recorded"]')
    ).toBeNull()
  })

  it("an expanded running child's stream is read again, never frozen at the expand", async () => {
    serve({ opsStatus: "running" })
    renderApp(`/runs/${DIR}`)
    await waitFor(() => expect(keys()).toContain(`r:${OPS}`))
    fireEvent.click(toggle(`r:${OPS}`))
    await waitFor(() =>
      expect(studio.calls(`GET runs/${OPS}/events`).length).toBeGreaterThan(0)
    )
    const first = studio.calls(`GET runs/${OPS}/events`).length
    await waitFor(
      () =>
        expect(studio.calls(`GET runs/${OPS}/events`).length).toBeGreaterThan(
          first
        ),
      { timeout: 6_000 }
    )
  })

  it("an expanded child's gaps and its read cap are badges on its row", async () => {
    const many = [
      { type: "run_start", id: OPS, agent: "ops-manager" },
      ...Array.from({ length: 20_001 }, () => ({
        type: "heartbeat",
        run_id: OPS,
      })),
    ] as unknown as WireEvent[]
    serve({ opsEvents: many, opsGaps: [7, 9] })
    renderApp(`/runs/${DIR}`)
    await waitFor(() => expect(keys()).toContain(`r:${OPS}`))
    fireEvent.click(toggle(`r:${OPS}`))
    await waitFor(() =>
      expect(
        row(`r:${OPS}`).querySelector('[data-hole="truncated"]')
      ).not.toBeNull()
    )
    // Paged by next_after, four pages of 5,000, then the cap.
    expect(
      studio.calls(`GET runs/${OPS}/events`).map((r) => r.query.get("after"))
    ).toEqual(["-1", "5000", "10000", "15000"])
    expect(
      row(`r:${OPS}`).querySelector('[data-hole="truncated"]')!.textContent
    ).toContain("first 20,000 events")
    expect(
      row(`r:${OPS}`).querySelector('[data-hole="gap"]')!.textContent
    ).toContain("2 events missing")
  })
})

describe("the call graph (U9.2-fix W5, W6)", () => {
  it("nodes take a focus ring, axe finds nothing; a child run's MCP calls are said not shown", async () => {
    serve()
    renderApp(`/runs/${DIR}?scale=graph`)
    await waitFor(() =>
      expect(document.querySelector("[data-call-graph]")).not.toBeNull()
    )
    const g = document.querySelector<HTMLElement>("[data-call-graph]")!
    for (const n of g.querySelectorAll("[data-node]"))
      expect(n.querySelector("[data-focus-ring]")).not.toBeNull()
    // The edge's hint is the edge group's title, not its label's text.
    const edge = g.querySelector('[data-edge="a:director>a:ops-manager"]')!
    expect(edge.querySelector(":scope > title")?.textContent).toMatch(
      /calls along this edge/
    )
    expect(edge.querySelector("[data-edge-label]")!.textContent).toBe(
      "1× · 9.3ms"
    )
    expect(g.querySelector("[data-graph-unsourced]")!.textContent).toMatch(
      /^MCP calls of 2 child runs not shown/
    )
    const res = await axe.run(g)
    expect(
      res.violations.map(
        (v) =>
          `${v.id}: ${v.nodes.map((n) => n.html.slice(0, 120)).join(" | ")}`
      )
    ).toEqual([])
  })
})

void INV
