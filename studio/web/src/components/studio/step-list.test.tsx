// The step story against the goldens (plan §7): the run page renders
// steps, the subagent block inline under its call, and the truncation
// badge on capped results.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { screen } from "@testing-library/react"
import { QueryClient } from "@tanstack/react-query"
import { describe, expect, it, vi } from "vitest"

import { stepQuery } from "@/lib/api"
import type { EventsPage, RunDoc, StepDoc, WireEvent } from "@/lib/api"
import { fold } from "@/lib/events"
import { resultCapReason } from "@/lib/honesty"
import { StepList } from "@/components/studio/step-list"
import { renderWithRouter } from "@/test/render"

function load(name: string): string {
  return readFileSync(resolve(process.cwd(), `../testdata/api/${name}`), "utf8")
}

const subDoc = JSON.parse(load("run-sub.golden.json")) as RunDoc
const subEvents = (
  JSON.parse(load("events-sub.golden.json")) as EventsPage
).events.map((pe) => pe.event)

describe("StepList", () => {
  it("renders steps with the subagent block under its call (B1, B7, S4.3)", async () => {
    await renderWithRouter(
      <StepList events={subEvents} folded={fold(subEvents)} doc={subDoc} />
    )

    // The parent's step: the delegation call (no stored text — the
    // transcript owns the finished words).
    expect(screen.getAllByText(/research/).length).toBeGreaterThan(0)

    // The subagent block is collapsed by default (the child's events
    // are fetched on expand, never inline); the row shows the child's
    // agent, its own usage, and the link to its own run page.
    expect(screen.getByText(/subagent/)).toBeTruthy()
    expect(screen.getByText("researcher")).toBeTruthy()
    expect(screen.getAllByText(/9 in \/ 5 out/).length).toBeGreaterThan(0)
    // The call row: name, an args summary, the ok pill and the size.
    expect(screen.getByText('{"prompt":"status of order 42"}')).toBeTruthy()
    expect(screen.getByText("ok")).toBeTruthy()
    expect(screen.getAllByText("29 B").length).toBeGreaterThan(0)

    // The child link.
    expect(screen.getByText("open run")).toBeTruthy()
    // Collapsed: the child's inner text is NOT in the DOM.
    expect(screen.queryByText("no events from the child yet")).toBeNull()
  })

  it("badges a truncated result (B9)", async () => {
    const events: WireEvent[] = [
      { type: "run_start", id: "r_t", model: { provider: "p", name: "m" } },
      { type: "step_start", run_id: "r_t", index: 0 },
      {
        type: "tool_start",
        run_id: "r_t",
        seq: 1,
        call_id: "c1",
        name: "read_logs",
        args: {},
      },
      {
        type: "tool_finish",
        run_id: "r_t",
        seq: 2,
        call_id: "c1",
        name: "read_logs",
        content: "…first bytes…\n…[truncated 130000 bytes]",
        is_error: false,
      },
      {
        type: "step_finish",
        run_id: "r_t",
        index: 0,
        reason: "stop",
        usage: { input_tokens: 1, output_tokens: 1 },
      },
      {
        type: "run_finish",
        run_id: "r_t",
        usage: { input_tokens: 1, output_tokens: 1 },
        steps: 1,
      },
    ]
    const doc = {
      ...subDoc,
      id: "r_t",
      children: [],
      status: "succeeded",
    } as RunDoc
    await renderWithRouter(
      <StepList events={events} folded={fold(events)} doc={doc} />
    )
    // The table's badge (D5): truncated, cause result_cap, the bytes cut.
    const cut = document.querySelector<HTMLElement>('[data-hole="truncated"]')!
    expect(cut.getAttribute("title")).toContain(resultCapReason(130000))
    expect(cut.getAttribute("title")).toContain("raise the tool's weft.MaxResultBytes")
  })

  it("says a failed run's open step failed, not that it is in flight", async () => {
    const events: WireEvent[] = [
      { type: "run_start", id: "r_x", model: { provider: "p", name: "m" } },
      { type: "step_start", run_id: "r_x", index: 0 },
      { type: "text_delta", run_id: "r_x", text: "partial" },
    ]
    const doc = {
      ...subDoc,
      id: "r_x",
      children: [],
      status: "failed",
      err: "boom",
    } as RunDoc
    await renderWithRouter(
      <StepList events={events} folded={fold(events)} doc={doc} />
    )
    expect(screen.getByText("failed during this step")).toBeTruthy()
    expect(screen.queryByText("in flight…")).toBeNull()
  })

  it("renders exactly the folded prefix at a playhead (B2's shape)", async () => {
    const first = await renderWithRouter(
      <StepList
        events={subEvents}
        folded={fold(subEvents)}
        doc={subDoc}
        upTo={3}
      />
    )
    // Before the delegation's tool_finish: the call reads running and
    // no result is revealed.
    expect(screen.getByText("running…")).toBeTruthy()
    first.unmount()
    await renderWithRouter(
      <StepList
        events={subEvents}
        folded={fold(subEvents)}
        doc={subDoc}
        upTo={subEvents.length}
      />
    )
    // The full stream: the call closed with the child's answer as its
    // tool result (the child's own steps stay in its collapsed block).
    expect(
      screen.getAllByText("order 42 shipped this morning").length
    ).toBeGreaterThan(0)
  })
})

  it("renders a steered user turn between the steps (ADR 0019)", async () => {
    const events: WireEvent[] = [
      { type: "run_start", id: "r_st", model: { provider: "p", name: "m" } },
      { type: "step_start", run_id: "r_st", index: 0 },
      { type: "text_delta", run_id: "r_st", text: "on it" },
      {
        type: "step_finish",
        run_id: "r_st",
        index: 0,
        reason: "stop",
        usage: { input_tokens: 1, output_tokens: 1 },
      },
      {
        type: "steered",
        run_id: "r_st",
        seq: 1,
        step: 0,
        messages: [
          { role: "user", content: [{ type: "text", text: "metric units" }] },
        ],
      },
      { type: "step_start", run_id: "r_st", index: 1 },
      { type: "text_delta", run_id: "r_st", text: "done in metres" },
      {
        type: "step_finish",
        run_id: "r_st",
        index: 1,
        reason: "stop",
        usage: { input_tokens: 1, output_tokens: 1 },
      },
      {
        type: "run_finish",
        run_id: "r_st",
        usage: { input_tokens: 2, output_tokens: 2 },
        steps: 2,
      },
    ]
    const doc = {
      ...subDoc,
      id: "r_st",
      children: [],
      status: "succeeded",
    } as RunDoc
    await renderWithRouter(
      <StepList events={events} folded={fold(events)} doc={doc} />
    )
    // The delivered words, labelled as the user's steer.
    expect(screen.getByText("metric units")).toBeTruthy()
    expect(screen.getByText("steered · user")).toBeTruthy()
    // The DOM order: step 0's card, the steer, step 1's card.
    const order = [
      document.querySelector("[data-step='0']"),
      document.querySelector("[data-steer]"),
      document.querySelector("[data-step='1']"),
    ]
    expect(order.every(Boolean)).toBe(true)
    expect(
      order[0]!.compareDocumentPosition(order[1]!) & Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy()
    expect(
      order[1]!.compareDocumentPosition(order[2]!) & Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy()
  
})

describe("StepList's subagent child rows (A10)", () => {
  // A child's usage is the record's only once the run ended — a
  // running child's zeros read "usage at finish", an interrupted one's
  // "—" beside its interrupted badge; never "0 in / 0 out".
  it("a running child's row says its usage comes at the finish; an interrupted one's is a dash and a badge", async () => {
    const zero = { input_tokens: 0, output_tokens: 0 }
    for (const [status, want] of [
      ["running", "usage at finish"],
      ["interrupted", "—"],
    ] as const) {
      const doc: RunDoc = {
        ...subDoc,
        children: subDoc.children.map((c) => ({ ...c, status, usage: zero, finished: null })),
      }
      const { unmount } = await renderWithRouter(
        <StepList events={subEvents} folded={fold(subEvents)} doc={doc} />
      )
      const row = document.querySelector(`[data-child-row="${subDoc.children[0].id}"]`)!
      expect(row.querySelector("[data-child-usage]")?.textContent).toBe(want)
      expect(row.textContent).not.toContain("0 in / 0 out")
      expect(Boolean(row.querySelector('[data-hole="interrupted"]'))).toBe(status === "interrupted")
      unmount()
    }
  })

  // A10: a live child the run document does not list yet still gets
  // its row from the step route's children[] when that is cached.
  it("falls back to the cached step route's children[] for a child the run document does not list", async () => {
    const kid = subDoc.children[0]
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    client.setQueryData(stepQuery(subDoc.id, 0).queryKey, {
      children: [{ id: kid.id, call_id: kid.parent_call_id, agent: "researcher", status: "succeeded", usage: kid.usage }],
    } as unknown as StepDoc)
    const doc: RunDoc = { ...subDoc, children: [] }
    await renderWithRouter(<StepList events={subEvents} folded={fold(subEvents)} doc={doc} />, client)
    const row = document.querySelector(`[data-step="0"] [data-child-row="${kid.id}"]`)
    expect(row?.querySelector("[data-child-agent]")?.textContent).toBe("researcher")
    expect(row?.querySelector("[data-child-usage]")?.textContent).toBe("9 in / 5 out")
  })

  // A10: call ids may repeat across steps (core/loop.go); the child's
  // id names its step (<parent>/<step>/<call id>), and that is the
  // call it joins — never the first call with the same id.
  it("joins a child to the call of the step its id names when a call id repeats across steps", async () => {
    const R = "r_rep"
    const U = { input_tokens: 1, output_tokens: 1 }
    const call = (index: number, name: string): WireEvent[] => [
      { type: "step_start", run_id: R, index },
      { type: "tool_start", run_id: R, seq: index + 1, call_id: "c1", name, args: {} },
      { type: "tool_finish", run_id: R, seq: index + 1, call_id: "c1", name, content: "ok", is_error: false },
      { type: "step_finish", run_id: R, index, reason: "tool_calls", usage: U },
    ]
    const events: WireEvent[] = [
      { type: "run_start", id: R, model: { provider: "p", name: "m" } },
      ...call(0, "lookup_order"),
      ...call(1, "research"),
      { type: "run_finish", run_id: R, usage: U, steps: 2 },
    ]
    const kid = { ...subDoc.children[0], id: `${R}/1/c1`, parent_run_id: R, parent_call_id: "c1" }
    const doc: RunDoc = { ...subDoc, id: R, children: [kid] }
    await renderWithRouter(<StepList events={events} folded={fold(events)} doc={doc} />)
    expect(document.querySelector(`[data-step="1"] [data-child-row="${kid.id}"]`)).toBeTruthy()
    expect(document.querySelector(`[data-step="0"] [data-child-row]`)).toBeNull()
  })

  // A10: a resumed run's step 0 holds two calls with one id (the
  // resumed call and the model's own). Rows are keyed by stream
  // position — no duplicate-key warning — and the step route's
  // children, which name that id twice, never join on it.
  it("renders a resumed step 0 with a repeated call id: no duplicate keys, each call its own child", async () => {
    const R = "r_res"
    const U = { input_tokens: 1, output_tokens: 1 }
    const events: WireEvent[] = [
      { type: "run_start", id: R, model: { provider: "p", name: "m" } },
      { type: "tool_start", run_id: R, seq: 1, call_id: "c1", name: "research", args: {} },
      { type: "tool_finish", run_id: R, seq: 1, call_id: "c1", name: "research", content: "resumed", is_error: false },
      { type: "step_start", run_id: R, index: 0 },
      { type: "tool_start", run_id: R, seq: 2, call_id: "c1", name: "research", args: {} },
      { type: "tool_finish", run_id: R, seq: 2, call_id: "c1", name: "research", content: "own", is_error: false },
      { type: "step_finish", run_id: R, index: 0, reason: "tool_calls", usage: U },
      { type: "run_finish", run_id: R, usage: U, steps: 1 },
    ]
    const base = subDoc.children[0]
    const resumed = { ...base, id: `${R}/resume/c1`, parent_run_id: R, parent_call_id: "c1" }
    const own = { ...base, id: `${R}/0/c1`, parent_run_id: R, parent_call_id: "c1" }
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    client.setQueryData(stepQuery(R, 0).queryKey, {
      children: [resumed, own].map((k) => ({ id: k.id, call_id: "c1", agent: "researcher", status: "succeeded", usage: k.usage })),
    } as unknown as StepDoc)
    const errors = vi.spyOn(console, "error").mockImplementation(() => {})
    try {
      // The run document lists neither: the step route's two c1
      // children are ambiguous, so no call draws one.
      const { unmount } = await renderWithRouter(
        <StepList events={events} folded={fold(events)} doc={{ ...subDoc, id: R, children: [] }} />,
        client
      )
      expect(document.querySelectorAll(`[data-step="0"] [data-child-row]`).length).toBe(0)
      unmount()
      // The run document lists both: each call its own.
      await renderWithRouter(
        <StepList events={events} folded={fold(events)} doc={{ ...subDoc, id: R, children: [resumed, own] }} />,
        client
      )
      const rows = [...document.querySelectorAll(`[data-step="0"] [data-child-row]`)].map((r) =>
        r.getAttribute("data-child-row")
      )
      expect(rows).toEqual([resumed.id, own.id])
      const dup = errors.mock.calls.filter((c) => String(c[0]).includes("same key"))
      expect(dup).toEqual([])
    } finally {
      errors.mockRestore()
    }
  })
})
