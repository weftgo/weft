// Plan H3 on the pages: the trace page's tree/detail split, the run
// page's story/request split and the playground's columns resize from
// the keyboard and come back at their last size; at phone width they
// stack (the playground's columns in order, no native select anywhere;
// its cards and tables scroll in their own box, pinned in
// pages.test.tsx); the density toggle and
// the layout reset are in the ⌘K palette and the sidebar.
import { act, cleanup, configure, fireEvent, screen, waitFor, within } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc, RunsPage, Span } from "@/lib/api"
import { DENSITY_KEY } from "@/lib/density"
import { paneKey, readPaneSizes } from "@/lib/pane-sizes"
import { renderApp } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden, pagedEvents, transcriptOf } from "@/test/fake-studio"
import type { FakePosEvent } from "@/test/fake-studio"
import { stubLayout, stubViewport } from "@/test/layout"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const TRACE = "0af7651916cd43dd8448eb211c80319c"
const span = (id: string, name: string, parent = ""): Span => ({
  trace_id: TRACE,
  span_id: id,
  parent_span_id: parent,
  name,
  kind: "internal",
  start: "2026-10-01T09:00:00.000Z",
  end: "2026-10-01T09:00:01.000Z",
  status: "ok",
  status_message: "",
  service: "acme-api",
  attrs: { "probe.name": name },
  events: [],
})

const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!
const RUN = "r_panes"
function runEvents(): FakePosEvent[] {
  const evs: unknown[] = [{ type: "run_start", id: RUN, model: { provider: "wefttest", name: "script" } }]
  for (let i = 0; i < 3; i++) {
    evs.push({ type: "step_start", run_id: RUN, index: i })
    evs.push({ type: "step_finish", run_id: RUN, index: i, reason: i < 2 ? "tool_calls" : "stop", usage: { input_tokens: 5, output_tokens: 2 } })
  }
  evs.push({ type: "run_finish", run_id: RUN, usage: { input_tokens: 15, output_tokens: 6 }, steps: 3 })
  return evs.map((event, pos) => ({ pos, time: rOK.started, event }))
}

const tool = (name: string) => ({ name, side_effects: "never", allow: false })
const runtimes = {
  runtimes: [
    {
      id: "rt_1",
      host: "laptop",
      pid: 1,
      service: "acme-api",
      env: "dev",
      connected_since: rOK.started,
      last_seen: rOK.started,
      agents: [{ name: "orders", models: [], tools: [tool("lookup_order")], instructions: "You are support." }],
    },
  ],
}

let restore = () => {}
beforeEach(() => {
  localStorage.clear()
  document.documentElement.removeAttribute("data-density")
  Element.prototype.scrollIntoView = vi.fn()
  stubViewport(1280)
  restore = stubLayout()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  new FakeStudio()
    .on("GET meta", {
      ...golden<Record<string, unknown>>("meta"),
      capabilities: ["live", "playground", "runtimes", "requests", "ingest"],
    })
    .on(`GET traces/${TRACE}`, { spans: [span("aa01", "invoke_agent support"), span("bb02", "chat glm", "aa01")] })
    .on("GET runtimes", runtimes)
    .on("GET experiments", { experiments: [] })
    .on(`GET runs/${RUN}`, { ...rOK, id: RUN, steps: 3, children: [] } satisfies RunDoc)
    .on(`GET runs/${RUN}/events`, pagedEvents(runEvents()))
    .on(`GET runs/${RUN}/transcript`, transcriptOf([]))
    .on(`GET runs/${RUN}/spans`, { spans: [] })
    .withRequests(RUN, "ok")
    .install()
})
afterEach(() => {
  cleanup()
  restore()
  vi.unstubAllGlobals()
  setStudioToken("")
})

const now = (sep: HTMLElement) => Number(sep.getAttribute("aria-valuenow"))
async function press(el: HTMLElement, key: string) {
  el.focus()
  await act(async () => {
    fireEvent.keyDown(el, { key })
  })
}

describe("the trace page's tree/detail split", () => {
  const sep = () => screen.findByRole("separator", { name: "resize the span tree and the span detail" })

  it("resizes from the keyboard and reopens at that size", async () => {
    renderApp(`/traces/${TRACE}?span=aa01`)
    const h = await sep()
    expect(now(h)).toBe(60)
    await press(h, "ArrowLeft")
    const moved = now(h)
    expect(moved).toBeLessThan(60)
    expect(readPaneSizes("trace-detail", 2)?.[0]).toBe(moved)
    // A reload: the same page, mounted afresh.
    renderApp(`/traces/${TRACE}?span=aa01`)
    expect(now(await sep())).toBe(moved)
    // The detail sits in its own pane, beside the tree.
    const detail = document.querySelector('[data-split="trace-detail"] [data-pane="detail"]')!
    expect(within(detail as HTMLElement).getByText(/invoke_agent support · acme-api/)).toBeTruthy()
  })

  it("stacks the detail under the tree at phone width", async () => {
    stubViewport(390)
    renderApp(`/traces/${TRACE}?span=aa01`)
    await waitFor(() => expect(document.querySelector('[data-split="trace-detail"][data-stacked]')).toBeTruthy())
    expect(screen.queryByRole("separator")).toBeNull()
  })
})

describe("the run page's story/request split", () => {
  const card = (n: number) => document.querySelector<HTMLElement>(`[data-step="${n}"]`)!
  async function openRequest(n: number) {
    renderApp(`/runs/${RUN}?view=story`)
    await waitFor(() => expect(card(n).querySelector("[data-request] button[aria-expanded]")).toBeTruthy())
    fireEvent.click(within(card(n)).getByRole("button", { name: /^request$/ }))
  }

  it("an open Request pane sits beside the story, resizable, one size for every card", async () => {
    await openRequest(1)
    const split = await waitFor(() => {
      const el = card(1).querySelector<HTMLElement>('[data-split="story-request"]')
      expect(el).toBeTruthy()
      return el!
    })
    expect(split.querySelector('[data-pane="request"] [data-request="1"]')).toBeTruthy()
    expect(split.querySelector('[data-pane="story"]')).toBeTruthy()
    // The keyboard stays on the toggle the card moved.
    expect(document.activeElement?.getAttribute("aria-expanded")).toBe("true")
    const h = within(split).getByRole("separator", { name: "resize the step's story and the step's request" })
    expect(now(h)).toBe(55)
    await press(h, "ArrowRight")
    expect(readPaneSizes("story-request", 2)?.[0]).toBe(now(h))
    // Closed, the pane goes back inline.
    fireEvent.click(within(card(1)).getByRole("button", { name: /^request$/ }))
    await waitFor(() => expect(card(1).querySelector('[data-split="story-request"]')).toBeNull())
  })

  it("narrower than lg the open pane stays inline, above the story", async () => {
    stubViewport(900)
    await openRequest(0)
    await waitFor(() => expect(card(0).querySelector('[data-request] button[aria-expanded="true"]')).toBeTruthy())
    expect(card(0).querySelector('[data-split="story-request"]')).toBeNull()
    expect(within(card(0)).queryByRole("separator")).toBeNull()
  })
})

describe("the playground's columns", () => {
  it("resize from the keyboard and reopen at that size", async () => {
    renderApp("/playground")
    const sep = () => screen.findByRole("separator", { name: "resize the config column and the runs column" })
    const h = await sep()
    expect(now(h)).toBe(30)
    await press(h, "ArrowRight")
    const moved = now(h)
    expect(moved).toBeGreaterThan(30)
    renderApp("/playground")
    expect(now(await sep())).toBe(moved)
  })

  it("stack at phone width: config above runs, the header wrapping, no native select", async () => {
    stubViewport(390)
    renderApp("/playground")
    const split = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-split="playground"]')
      expect(el?.hasAttribute("data-stacked")).toBe(true)
      return el!
    })
    // The stacked column scrolls as one; nothing beside it.
    expect(split.className).toContain("overflow-y-auto")
    expect(Array.from(split.querySelectorAll("[data-column]")).map((c) => c.getAttribute("data-column"))).toEqual([
      "config",
      "runs",
    ])
    expect(screen.queryByRole("separator")).toBeNull()
    await waitFor(() => expect(screen.getByLabelText("agent").getAttribute("data-value")).toBe("orders"))
    expect(screen.getByText("Playground ·").closest("header")!.className).toContain("flex-wrap")
    expect(document.querySelector("select, option")).toBeNull()
    expect(screen.getByLabelText("agent").getAttribute("role")).toBe("combobox")
    // The result cards and the matrix's table scroll inside their own
    // box: pinned where they are drawn (pages.test.tsx's playground).
  })
})

describe("the palette's density and layout entries", () => {
  async function palette(item: RegExp | string) {
    fireEvent.keyDown(window, { key: "k", ctrlKey: true })
    fireEvent.click(await screen.findByText(item))
  }

  it("toggles the density app-wide and persists it; the sidebar toggle shows it", async () => {
    renderApp(`/traces/${TRACE}`)
    await waitFor(() => expect(document.documentElement.getAttribute("data-density")).toBe("comfortable"))
    await palette("Compact density")
    expect(document.documentElement.getAttribute("data-density")).toBe("compact")
    expect(localStorage.getItem(DENSITY_KEY)).toBe("compact")
    const toggle = await screen.findByRole("button", { name: "density: compact" })
    expect(toggle.getAttribute("aria-pressed")).toBe("true")
    // The palette names the way back; the sidebar toggle takes it too.
    fireEvent.click(toggle)
    expect(document.documentElement.getAttribute("data-density")).toBe("comfortable")
    expect(localStorage.getItem(DENSITY_KEY)).toBeNull()
    await palette("Compact density")
    expect(document.documentElement.getAttribute("data-density")).toBe("compact")
  })

  it("a stored compact is on <html> after a reload", async () => {
    localStorage.setItem(DENSITY_KEY, "compact")
    renderApp(`/traces/${TRACE}`)
    await waitFor(() => expect(document.documentElement.getAttribute("data-density")).toBe("compact"))
  })

  it("reset pane layout puts the open split back at its default and forgets the saved sizes", async () => {
    localStorage.setItem(paneKey("trace-detail"), "[35,65]")
    localStorage.setItem(paneKey("playground"), "[50,50]")
    renderApp(`/traces/${TRACE}`)
    const h = await screen.findByRole("separator", { name: "resize the span tree and the span detail" })
    expect(now(h)).toBe(35)
    await palette("Reset pane layout")
    await waitFor(() => expect(now(h)).toBe(60))
    expect(localStorage.getItem(paneKey("trace-detail"))).toBeNull()
    expect(localStorage.getItem(paneKey("playground"))).toBeNull()
  })
})
