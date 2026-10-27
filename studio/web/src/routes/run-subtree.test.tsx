// U9.2's Done line: on rw.deep-delegation's director run the child
// subtree draws in place — ops-manager expands, then its worker
// (inventory-specialist), each still one row until expanded, "open run
// ↗" kept; ?sel= naming a row inside them restores the expansion; the
// graph scale draws the three agents with their edge counts, sharing
// the tree's selection; the run page links the whole trace; the tree's
// holes (U9.1-fix) are badges (UX-A-01, UX-D-13, UX-A-17, UX-S3-14).
import { cleanup, configure, fireEvent, waitFor } from "@testing-library/react"
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

// ops-manager's own stream: one call to its worker, then its answer.
const OPS_EVENTS = [
  { type: "run_start", id: OPS, agent: "ops-manager" },
  { type: "step_start", run_id: OPS, index: 0 },
  {
    type: "tool_start",
    run_id: OPS,
    seq: 1,
    call_id: "call_1",
    name: "inventory_specialist",
    args: {},
  },
  {
    type: "tool_finish",
    run_id: OPS,
    seq: 1,
    call_id: "call_1",
    name: "inventory_specialist",
    content: "42",
    is_error: false,
  },
  { type: "step_finish", run_id: OPS, index: 0, reason: "tool_calls" },
  { type: "step_start", run_id: OPS, index: 1 },
  { type: "step_finish", run_id: OPS, index: 1, reason: "stop" },
  { type: "run_finish", run_id: OPS, steps: 2 },
] as unknown as WireEvent[]

let studio: FakeStudio

function serve(opts: { holes?: TreeHole[]; spans?: boolean } = {}) {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  const withSpans = opts.spans ?? true
  const ops: RunRow = {
    ...rOK,
    id: OPS,
    agent: "ops-manager",
    parent_run_id: DIR,
    parent_call_id: "call_1",
  }
  const inv: RunRow = {
    ...rOK,
    id: INV,
    agent: "inventory-specialist",
    parent_run_id: OPS,
    parent_call_id: "call_1",
  }
  const doc: RunDoc = {
    ...rOK,
    id: DIR,
    agent: "director",
    steps: 2,
    trace_id: withSpans ? "t1" : "",
    children: [ops],
    holes: [],
  }
  const tree = SPANS.map((s) => ({
    ...s,
    run_id: String(s.attrs["weft.run.id"] ?? DIR),
  }))
  const paged = (evs: WireEvent[]) =>
    pagedEvents(
      evs.map((event, pos) => ({ pos, time: rOK.started, event })),
      { done: true }
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
      spans: withSpans ? tree : [],
      holes: opts.holes ?? [],
    }))
    .on(`GET runs/${OPS}`, { ...ops, children: [inv], holes: [] })
    .on(`GET runs/${OPS}/events`, paged(OPS_EVENTS))
    .on(`GET runs/${OPS}/spans`, () => ({
      spans: tree.filter((s) => s.run_id.startsWith(OPS)),
      holes: [],
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
const toggle = (key: string) =>
  row(key).querySelector<HTMLButtonElement>(
    'button[aria-label="expand"], button[aria-label="collapse"]'
  )!

const OPS_ROWS = [
  `r:${OPS}`,
  `s:${OPS}:0`,
  `s:${OPS}:0:m`,
  `c:${OPS}:0:call_1`,
  `r:${INV}`,
  `s:${OPS}:1`,
  `s:${OPS}:1:m`,
]
const INV_ROWS = [
  `r:${INV}`,
  `s:${INV}:0`,
  `s:${INV}:0:m`,
  `c:${INV}:0:call_1`,
  // A span weft does not name keeps its bare t:<span id> (no run of
  // its own: the trace page keys it so too, lib/trace-sel.ts).
  "t:i_db",
  `s:${INV}:1`,
  `s:${INV}:1:m`,
]

describe("the child subtree in place (the Done line)", () => {
  it("expands ops-manager, then its worker, in place; each child one row until expanded, open run kept", async () => {
    serve()
    renderApp(`/runs/${DIR}`)
    await waitFor(() => expect(keys()).toContain(`r:${OPS}`))
    // Collapsed by default: one row, its toggle says expand.
    expect(keys().some((k) => k!.startsWith(`s:${OPS}`))).toBe(false)
    expect(row(`r:${OPS}`).getAttribute("aria-expanded")).toBe("false")
    fireEvent.click(toggle(`r:${OPS}`))
    await waitFor(() => expect(keys()).toContain(`s:${OPS}:0`))
    const k = keys()
    const at = k.indexOf(`r:${OPS}`)
    expect(k.slice(at, at + OPS_ROWS.length)).toEqual(OPS_ROWS)
    expect(row(`r:${OPS}`).getAttribute("aria-expanded")).toBe("true")
    // The grandchild is lazy: one row, its own rows not drawn yet.
    expect(keys().some((x) => x!.startsWith(`s:${INV}`))).toBe(false)
    expect(row(`r:${INV}`).getAttribute("aria-expanded")).toBe("false")
    fireEvent.click(toggle(`r:${INV}`))
    await waitFor(() => expect(keys()).toContain(`s:${INV}:0`))
    const k2 = keys()
    const at2 = k2.indexOf(`r:${INV}`)
    expect(k2.slice(at2, at2 + INV_ROWS.length)).toEqual(INV_ROWS)
    // Labels and gists read as the root's do.
    expect(row(`c:${INV}:0:call_1`).textContent).toContain("inventory_count")
    // "open run ↗" stays on both child rows.
    for (const r of [OPS, INV])
      expect(
        row(`r:${r}`).querySelector("[data-open-run]")!.getAttribute("href")
      ).toContain(`/runs/${encodeURIComponent(r)}`)
    // Collapsing ops-manager folds its whole subtree back into one row.
    fireEvent.click(toggle(`r:${OPS}`))
    await waitFor(() => expect(keys()).not.toContain(`s:${OPS}:0`))
    expect(keys()).not.toContain(`r:${INV}`)
  })

  it("?sel= naming a row in the worker restores both levels and selects it", async () => {
    serve()
    const sel = `c:${INV}:0:call_1`
    renderApp(`/runs/${DIR}?sel=${encodeURIComponent(sel)}`)
    await waitFor(() => expect(selected()).toEqual([sel]))
    expect(keys()).toEqual(expect.arrayContaining([...OPS_ROWS, ...INV_ROWS]))
    // The detail pane reads the selected span.
    await waitFor(() =>
      expect(document.body.textContent).toContain("inventory_count")
    )
  })

  it("?sel=<child run>:<span id> resolves to the row that span is behind", async () => {
    serve()
    renderApp(`/runs/${DIR}?sel=${encodeURIComponent(`${INV}:i_t`)}`)
    await waitFor(() => expect(selected()).toEqual([`c:${INV}:0:call_1`]))
  })

  it("selecting inside a child, then collapsing it, moves the selection to the child's row", async () => {
    serve()
    const { router } = renderApp(
      `/runs/${DIR}?sel=${encodeURIComponent(`s:${INV}:1`)}`
    )
    await waitFor(() => expect(selected()).toEqual([`s:${INV}:1`]))
    fireEvent.click(toggle(`r:${INV}`))
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ sel: `r:${INV}` })
    )
    await waitFor(() => expect(selected()).toEqual([`r:${INV}`]))
    expect(keys()).not.toContain(`s:${INV}:1`)
    // Its parent stays as the selection left it: expanded.
    expect(keys()).toContain(`s:${OPS}:0`)
  })

  it("on the order scale the child expands from its own events", async () => {
    serve({ spans: false })
    renderApp(`/runs/${DIR}?scale=order`)
    await waitFor(() => expect(keys()).toContain(`r:${OPS}`))
    fireEvent.click(toggle(`r:${OPS}`))
    await waitFor(() => expect(keys()).toContain(`s:${OPS}:0`))
    // Its worker, linked from its run document, is a collapsed row.
    expect(keys()).toContain(`r:${INV}`)
    expect(studio.calls(`GET runs/${OPS}/events`).length).toBeGreaterThan(0)
  })
})

describe("the graph scale (§6.2)", () => {
  it("draws the three agents with their edge counts, the selection shared with the tree", async () => {
    serve()
    const { router } = renderApp(`/runs/${DIR}?scale=graph&level=agents`)
    await waitFor(() =>
      expect(document.querySelector("[data-call-graph]")).not.toBeNull()
    )
    const nodes = Array.from(
      document.querySelectorAll<SVGElement>("[data-node]")
    ).map((n) => n.getAttribute("data-node"))
    expect(nodes).toEqual([
      "a:director",
      "a:ops-manager",
      "a:inventory-specialist",
    ])
    const edge = (k: string) =>
      document
        .querySelector(`[data-edge="${k}"] [data-edge-label]`)
        ?.textContent.replace(EDGE_HINT_RE, "")
    expect(edge("a:director>a:ops-manager")).toBe("1× · 9.3 ms")
    expect(edge("a:ops-manager>a:inventory-specialist")).toBe("1× · 7.3 ms")
    expect(document.querySelectorAll("[data-edge]")).toHaveLength(2)
    // The switch offers the graph beside time and order.
    expect(
      Array.from(
        document.querySelectorAll<HTMLElement>("[data-scale] button")
      ).map((b) => b.textContent)
    ).toEqual(["time", "order", "graph"])
    // A node selects its run's row; switching back keeps the selection.
    fireEvent.click(
      document.querySelector('[data-node="a:inventory-specialist"]')!
    )
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ sel: `r:${INV}` })
    )
    await waitFor(() =>
      expect(
        document
          .querySelector('[data-node="a:inventory-specialist"]')!
          .getAttribute("aria-pressed")
      ).toBe("true")
    )
    fireEvent.click(
      Array.from(
        document.querySelectorAll<HTMLElement>("[data-scale] button")
      ).find((b) => b.textContent === "time")!
    )
    await waitFor(() => expect(selected()).toEqual([`r:${INV}`]))
    // The worker's row is drawn: its parent expanded for it.
    expect(keys()).toContain(`s:${OPS}:0`)
  })
})

const EDGE_HINT_RE = /calls along this edge · the slowest call/

describe("the run page and its trace (UX-A-17), the tree's holes", () => {
  it("links the whole trace with the selection kept", async () => {
    serve()
    renderApp(`/runs/${DIR}?sel=${encodeURIComponent(`s:${OPS}:0`)}`)
    await waitFor(() => expect(selected()).toEqual([`s:${OPS}:0`]))
    const a = document.querySelector<HTMLAnchorElement>("[data-trace-link]")!
    expect(a.textContent).toBe("whole trace")
    const href = new URL(a.href, "http://x")
    expect(href.pathname).toMatch(/\/traces\/t1$/)
    expect(href.searchParams.get("sel")).toBe(`s:${OPS}:0`)
    // U30.2: one switch, "this run" the current scope; the trace side
    // remembers the run it came from.
    expect(href.searchParams.get("run")).toBe(DIR)
    expect(
      document
        .querySelector('[data-scope-switch="run"] [data-scope-side="run"]')
        ?.getAttribute("aria-current")
    ).toBe("page")
  })

  it("draws the tree's holes as badges; a child with no spans says so on its row", async () => {
    serve({
      holes: [
        {
          hole: "not_recorded",
          cause: "no_spans",
          reason: "1 descendant run has a row and no span",
          fix: "install a tracer (otel.Install records spans)",
          runs: [OPS],
        },
      ],
    })
    renderApp(`/runs/${DIR}`)
    await waitFor(() =>
      expect(
        document.querySelector('[data-tree-holes] [data-hole="not_recorded"]')
      ).not.toBeNull()
    )
    expect(document.querySelector("[data-tree-holes]")!.textContent).toContain(
      "1 descendant run has a row and no span"
    )
    await waitFor(() =>
      expect(
        row(`r:${OPS}`).querySelector('[data-hole="not_recorded"]')
      ).not.toBeNull()
    )
  })

  it("a cut tree (truncated) reads an expanded child's own ?tree=1", async () => {
    serve({
      holes: [
        {
          hole: "truncated",
          cause: "response_cap",
          reason: "this response reads at most 10000 spans of the run tree",
        },
      ],
    })
    renderApp(`/runs/${DIR}`)
    await waitFor(() =>
      expect(document.querySelector('[data-hole="truncated"]')).not.toBeNull()
    )
    expect(studio.calls(`GET runs/${OPS}/spans`)).toHaveLength(0)
    fireEvent.click(toggle(`r:${OPS}`))
    await waitFor(() =>
      expect(
        studio.calls(`GET runs/${OPS}/spans`).map((r) => r.query.get("tree"))
      ).toContain("1")
    )
  })
})
