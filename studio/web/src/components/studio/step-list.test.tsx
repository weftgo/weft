// The step story against the goldens (plan §7): the run page renders
// steps, the subagent block inline under its call, and the truncation
// badge on capped results.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"

import type { EventsPage, RunDoc, WireEvent } from "@/lib/api"
import { fold } from "@/lib/events"
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
    expect(screen.getByText("cut 130,000 bytes")).toBeTruthy()
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
