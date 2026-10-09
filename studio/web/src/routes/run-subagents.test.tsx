// Subagents on the page (plan A10) against a fake Studio serving the
// subagent example's real records (TestStepRoute's recordStepsRun: the
// research Subagent at step 1): the step that called it shows the
// child as a nested row — agent, status, usage, a link to its own run
// page — and opening it shows the CHILD's prompt
// (requests-child.golden.json, read by the child's id), never the
// parent's; the trace view's call detail does the same; the runs list
// keeps children out by default and a toggle lists them, each with its
// parent link.
import {
  cleanup,
  configure,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc, RunRow, RunsPage, StepDoc } from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import {
  FakeStudio,
  golden,
  hiddenRefusal,
  pagedEvents,
  pagedRequests,
  transcriptOf,
} from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const RUN = "r_steps"
const steps = (["0", "1", "2"] as const).map((n) => golden<StepDoc>(`step-${n}`))
const kid = steps[1].children[0] // the researcher's run, as A7 assembled it
const CHILD = kid.id // r_steps/1/c_sub
const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!
const childRow: RunRow = {
  ...rOK,
  id: CHILD,
  parent_run_id: RUN,
  parent_call_id: kid.call_id,
  agent: kid.agent,
  status: kid.status,
  usage: kid.usage,
  steps: 1,
  trace_id: "",
}
const doc: RunDoc = {
  ...rOK,
  id: RUN,
  agent: "orders",
  steps: 3,
  trace_id: "",
  children: [childRow],
  holes: [],
}

/** The parent's stream: run_start, then the recorded steps' events. */
function parentEvents() {
  const evs: unknown[] = [
    { type: "run_start", id: RUN, model: { provider: "wefttest", name: "glm-a" }, agent: "orders" },
    ...steps.flatMap((s) => s.events.map((e) => e.event)),
  ]
  return evs.map((event, pos) => ({ pos, time: rOK.started, event }))
}

/** The parent's request record: each recorded step's attempt 1. */
const parentRequests = {
  requests: steps.flatMap((s) =>
    s.request && "index" in s.request ? [s.request] : []
  ),
}

function childEvents() {
  return [
    { type: "run_start", id: CHILD, model: { provider: "wefttest", name: "script" }, agent: "researcher" },
    { type: "step_start", run_id: CHILD, index: 0 },
    { type: "step_finish", run_id: CHILD, index: 0, reason: "stop", usage: kid.usage },
    { type: "run_finish", run_id: CHILD, usage: kid.usage, steps: 1 },
  ].map((event, pos) => ({ pos, time: rOK.started, event }))
}

const say = (role: string, text: string) => ({ role, content: [{ type: "text", text }] })

let studio: FakeStudio
function serve(opts: { childRequests?: "ok" | "hidden" } = {}) {
  studio = new FakeStudio()
    .on("GET meta", { ...golden<Record<string, unknown>>("meta"), capabilities: ["requests", "ingest"] })
    .on(`GET runs/${RUN}`, doc)
    .on(`GET runs/${RUN}/events`, pagedEvents(parentEvents(), { done: true }))
    .on(`GET runs/${RUN}/transcript`, transcriptOf([]))
    .on(`GET runs/${RUN}/spans`, { spans: [] })
    .on(`GET runs/${RUN}/requests`, pagedRequests(parentRequests))
    .on(`GET runs/${CHILD}`, { ...childRow, children: [], holes: [] })
    .on(`GET runs/${CHILD}/events`, pagedEvents(childEvents(), { done: true }))
    .on(
      `GET runs/${CHILD}/transcript`,
      transcriptOf([[say("user", "why is order 42 late?")], [say("assistant", "the carrier lost it")]])
    )
    .on(
      `GET runs/${CHILD}/requests`,
      opts.childRequests === "hidden"
        ? () => hiddenRefusal()
        : pagedRequests(golden("requests-child"))
    )
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

async function childRow1(): Promise<HTMLElement> {
  let row: HTMLElement | null = null
  await waitFor(() => {
    row = document.querySelector<HTMLElement>(`[data-step="1"] [data-child-row="${CHILD}"]`)
    expect(row).toBeTruthy()
  })
  return row!
}

/** Opens the block, then the child's step-0 request; returns it. */
async function openChildRequest(block: HTMLElement): Promise<HTMLElement> {
  fireEvent.click(within(block).getByRole("button", { name: /subagent researcher/ }))
  let sec: HTMLElement | null = null
  await waitFor(() => {
    sec = block.querySelector<HTMLElement>('[data-child-request="0"]')
    expect(sec && within(sec).queryByRole("button", { name: /request/ })).toBeTruthy()
  })
  fireEvent.click(within(sec!).getByRole("button", { name: /request/ }))
  return sec!
}

describe("subagents on the run page (A10)", () => {
  it("the step that called a subagent shows the child as a nested row: agent, status, usage, link", async () => {
    serve()
    renderApp(`/runs/${RUN}?view=story`)
    const row = await childRow1()
    expect(row.querySelector("[data-child-agent]")?.textContent).toBe("researcher")
    expect(row.querySelector("[data-child-status]")?.textContent).toBe("succeeded")
    expect(row.querySelector("[data-child-usage]")?.textContent).toBe("10 in / 5 out")
    const link = within(row).getByRole("link", { name: /open run/ })
    expect(decodeURIComponent(link.getAttribute("href") ?? "")).toContain(`/runs/${CHILD}`)
    // Only step 1 called it.
    expect(document.querySelector(`[data-step="0"] [data-child-row]`)).toBeNull()
    expect(document.querySelector(`[data-step="2"] [data-child-row]`)).toBeNull()
  })

  it("expanding the subagent call shows the CHILD's prompt, read by the child's id — not the parent's", async () => {
    serve()
    renderApp(`/runs/${RUN}?view=story`)
    const block = await childRow1()
    const sec = await openChildRequest(block)
    await waitFor(() => expect(sec.querySelector("[data-prompt]")?.textContent).toBe("You research orders."))
    expect(sec.textContent).not.toContain("You are a support agent.")
    expect(studio.calls(`GET runs/${CHILD}/requests`).length).toBeGreaterThan(0)
    // The parent's own step 1 still reads the parent's prompt.
    // (Opened, the section moves beside the story — plan H3's split —
    // so it is read again where it lands.)
    const parent = () => document.querySelector<HTMLElement>('[data-step="1"] [data-request="1"]')!
    fireEvent.click(within(parent()).getAllByRole("button", { name: /request/ })[0])
    await waitFor(() =>
      expect(parent().querySelector("[data-prompt]")?.textContent).toBe("You are a support agent.")
    )
    // The child's words, folded from its own stream.
    expect(await within(block).findByText("the carrier lost it")).toBeTruthy()
  })

  it("a token that may not read prompts sees the child's request as hidden", async () => {
    serve({ childRequests: "hidden" })
    renderApp(`/runs/${RUN}?view=story`)
    const block = await childRow1()
    fireEvent.click(within(block).getByRole("button", { name: /subagent researcher/ }))
    await waitFor(() => {
      const sec = block.querySelector<HTMLElement>('[data-child-request="0"]')
      expect(sec?.textContent).toContain("hidden by your token scope")
    })
    expect(block.querySelector("[data-prompt]")).toBeNull()
  })

  it("the trace view's subagent call opens the child, whose step shows the child's request", async () => {
    serve()
    renderApp(`/runs/${RUN}?sel=${encodeURIComponent("c:1:c_sub")}`)
    let block: HTMLElement | null = null
    await waitFor(() => {
      block = document.querySelector<HTMLElement>(`[data-child-row="${CHILD}"]`)
      expect(block).toBeTruthy()
    })
    const sec = await openChildRequest(block!)
    await waitFor(() => expect(sec.querySelector("[data-prompt]")?.textContent).toBe("You research orders."))
    expect(sec.textContent).not.toContain("You are a support agent.")
  })
})

describe("the trace view on a call id repeated across steps (A10)", () => {
  // Step 0 calls lookup_order as c1; step 1 calls the research
  // subagent, also as c1 (ids may repeat across steps, core/loop.go):
  // the child, <run>/1/c1, belongs to step 1's call alone. The trace
  // fold is linked by child id (linkView), so the call detail joins on
  // it. A call span's key names its step (c:<step>:<call id>), so
  // ?sel=c:1:c1 selects step 1's call, never step 0's.
  const R = "r_rep"
  const KID = `${R}/1/c1`
  const U = { input_tokens: 1, output_tokens: 1 }
  function serveRepeated() {
    const call = (index: number, name: string) => [
      { type: "step_start", run_id: R, index },
      { type: "tool_start", run_id: R, seq: index + 1, call_id: "c1", name, args: {} },
      { type: "tool_finish", run_id: R, seq: index + 1, call_id: "c1", name, content: "ok", is_error: false },
      { type: "step_finish", run_id: R, index, reason: "tool_calls", usage: U },
    ]
    const evs = [
      { type: "run_start", id: R, model: { provider: "wefttest", name: "glm-a" }, agent: "orders" },
      ...call(0, "lookup_order"),
      ...call(1, "research"),
      { type: "run_finish", run_id: R, usage: U, steps: 2 },
    ].map((event, pos) => ({ pos, time: rOK.started, event }))
    const kidRow: RunRow = { ...childRow, id: KID, parent_run_id: R, parent_call_id: "c1" }
    studio = new FakeStudio()
      .on("GET meta", { ...golden<Record<string, unknown>>("meta"), capabilities: ["ingest"] })
      .on(`GET runs/${R}`, { ...doc, id: R, steps: 2, children: [kidRow] })
      .on(`GET runs/${R}/events`, pagedEvents(evs, { done: true }))
      .on(`GET runs/${R}/transcript`, transcriptOf([]))
      .install()
  }

  it("step 0's c1 lookup shows no child; step 1's c1 subagent shows it", async () => {
    serveRepeated()
    renderApp(`/runs/${R}?sel=${encodeURIComponent("c:0:c1")}`)
    // Step 0's key lands on step 0's lookup: no child there.
    await waitFor(() =>
      expect(document.querySelector('[data-span="c:0:c1"][aria-selected="true"]')).toBeTruthy()
    )
    expect(document.querySelector('[data-span="c:1:c1"][aria-selected="true"]')).toBeNull()
    expect(document.querySelector("[data-call=\"c1\"]")?.textContent).toContain("lookup_order")
    expect(document.querySelector("[data-child-row]")).toBeNull()
    cleanup()

    // Step 1's key selects step 1's call: the research subagent, its child.
    serveRepeated()
    renderApp(`/runs/${R}?sel=${encodeURIComponent("c:1:c1")}`)
    await waitFor(() =>
      expect(document.querySelector('[data-span="c:1:c1"][aria-selected="true"]')).toBeTruthy()
    )
    expect(document.querySelector('[data-span="c:0:c1"][aria-selected="true"]')).toBeNull()
    await waitFor(() => {
      const block = document.querySelector<HTMLElement>(`[data-child-row="${KID}"]`)
      expect(block?.closest("[data-call]")?.textContent).toContain("research")
    })
    cleanup()

    serveRepeated()
    renderApp(`/runs/${R}?sel=s1`)
    let row: HTMLElement | null = null
    await waitFor(() => {
      row = document.querySelector<HTMLElement>(`[data-child-row="${KID}"]`)
      expect(row).toBeTruthy()
    })
    expect(row!.closest("[data-call]")?.textContent).toContain("research")
    expect(document.querySelectorAll("[data-child-row]").length).toBe(1)
  })
})

describe("the runs list keeps subagent children out by default (A10)", () => {
  it("lists top-level runs, and the toggle lists the children with their parent link", async () => {
    const parentRow: RunRow = { ...rOK, id: RUN, agent: "orders" }
    serve()
    studio.on("GET runs", (req) =>
      req.query.get("all") === "1"
        ? { total: 2, runs: [parentRow, childRow], next_before: null }
        : { total: 1, runs: [parentRow], next_before: null }
    )
    renderApp("/runs")
    expect(await screen.findByText(RUN)).toBeTruthy()
    expect(screen.queryByText(CHILD)).toBeNull()
    expect(studio.calls("GET runs")[0].query.get("all")).toBeNull()
    expect(studio.calls("GET runs")[0].query.get("parent")).toBeNull()

    fireEvent.click(screen.getByRole("button", { name: "top-level only" }))
    expect(await screen.findByText(CHILD)).toBeTruthy()
    expect(studio.calls("GET runs").at(-1)?.query.get("all")).toBe("1")
    const parentLink = document.querySelector<HTMLElement>("[data-parent-link] a")
    expect(parentLink?.textContent).toBe(RUN)
    expect(decodeURIComponent(parentLink?.getAttribute("href") ?? "")).toContain(`/runs/${RUN}`)
    expect(screen.getByRole("button", { name: "with subagents" })).toBeTruthy()
  })

  it("parent=<run id> in the URL lists that run's children", async () => {
    serve()
    studio.on("GET runs", (req) =>
      req.query.get("parent") === RUN
        ? { total: 1, runs: [childRow], next_before: null }
        : { total: 0, runs: [], next_before: null }
    )
    renderApp(`/runs?parent=${encodeURIComponent(RUN)}`)
    expect(await screen.findByText(CHILD)).toBeTruthy()
    expect(screen.getByText(`children of ${RUN}`)).toBeTruthy()
  })
})
