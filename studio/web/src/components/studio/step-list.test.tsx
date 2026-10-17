// The step story against the goldens (plan §7): the run page renders
// steps, the subagent block inline under its call, and the truncation
// badge on capped results.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { fireEvent, screen } from "@testing-library/react"
import { QueryClient } from "@tanstack/react-query"
import { describe, expect, it, vi } from "vitest"

import { stepQuery } from "@/lib/api"
import type { EventsPage, RunDoc, StepDoc, WireEvent } from "@/lib/api"
import { fold } from "@/lib/events"
import { toolFactsOf } from "@/lib/error-kind"
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
    expect(cut.getAttribute("title")).toContain(
      "raise the tool's weft.MaxResultBytes"
    )
  })

  it("the tool pill reads the call's span facts: retry n/budget, strict input (UX-S2-10, UX-E-48)", async () => {
    const call = (
      seq: number,
      id: string,
      name: string,
      content: string
    ): WireEvent[] => [
      { type: "tool_start", run_id: "r_f", seq, call_id: id, name, args: {} },
      {
        type: "tool_finish",
        run_id: "r_f",
        seq: seq + 1,
        call_id: id,
        name,
        content,
        is_error: true,
      },
    ]
    const events: WireEvent[] = [
      { type: "run_start", id: "r_f", model: { provider: "p", name: "m" } },
      { type: "step_start", run_id: "r_f", index: 0 },
      ...call(1, "c1", "flaky", "RETRY: try again"),
      ...call(3, "c2", "flaky", "RETRY: try again"),
      ...call(
        5,
        "c3",
        "lookup",
        'INVALID_INPUT: tool "lookup": unknown field "extra": not in the schema'
      ),
      ...call(7, "c4", "other", "RETRY: no span facts"),
      {
        type: "step_finish",
        run_id: "r_f",
        index: 0,
        reason: "tool_calls",
        usage: { input_tokens: 1, output_tokens: 1 },
      },
    ]
    const doc = {
      ...subDoc,
      id: "r_f",
      children: [],
      status: "failed",
    } as RunDoc
    const facts = toolFactsOf(
      [
        {
          attrs: {
            "gen_ai.operation.name": "execute_tool",
            "weft.run.id": "r_f",
            "gen_ai.tool.call.id": "c1",
            "error.type": "RETRY",
            "weft.tool.retry": 1,
            "weft.tool.retry_budget": 1,
          },
        },
        {
          attrs: {
            "gen_ai.operation.name": "execute_tool",
            "weft.run.id": "r_f",
            "gen_ai.tool.call.id": "c2",
            "error.type": "RETRY",
            "weft.tool.retry": 2,
            "weft.tool.retry_budget": 1,
          },
        },
        {
          attrs: {
            "gen_ai.operation.name": "execute_tool",
            "weft.run.id": "r_f",
            "gen_ai.tool.call.id": "c3",
            "error.type": "INVALID_INPUT",
            "weft.tool.input_error": "strict",
          },
        },
      ],
      "r_f"
    )
    await renderWithRouter(
      <StepList
        events={events}
        folded={fold(events)}
        doc={doc}
        toolFacts={facts}
      />
    )
    const pill = (id: string) =>
      document
        .querySelector<HTMLElement>(`[data-call="${id}"]`)!
        .querySelector<HTMLElement>("[data-tool-pill]")
    expect(pill("c1")!.textContent).toBe("retry 1/1")
    expect(pill("c2")!.textContent).toBe("retry 2/1 — budget spent")
    expect(pill("c2")!.title).toBe(
      "past MaxModelRetries(1): the run fails at its next model call"
    )
    expect(pill("c3")!.textContent).toBe("strict input")
    // No span facts: the result's own code, as before.
    expect(pill("c4")).toBeNull()
    expect(document.querySelector('[data-call="c4"]')!.textContent).toContain(
      "RETRY"
    )
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
    order[0]!.compareDocumentPosition(order[1]!) &
      Node.DOCUMENT_POSITION_FOLLOWING
  ).toBeTruthy()
  expect(
    order[1]!.compareDocumentPosition(order[2]!) &
      Node.DOCUMENT_POSITION_FOLLOWING
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
        children: subDoc.children.map((c) => ({
          ...c,
          status,
          usage: zero,
          finished: null,
        })),
      }
      const { unmount } = await renderWithRouter(
        <StepList events={subEvents} folded={fold(subEvents)} doc={doc} />
      )
      const row = document.querySelector(
        `[data-child-row="${subDoc.children[0].id}"]`
      )!
      expect(row.querySelector("[data-child-usage]")?.textContent).toBe(want)
      expect(row.textContent).not.toContain("0 in / 0 out")
      expect(Boolean(row.querySelector('[data-hole="interrupted"]'))).toBe(
        status === "interrupted"
      )
      unmount()
    }
  })

  // A10: a live child the run document does not list yet still gets
  // its row from the step route's children[] when that is cached.
  it("falls back to the cached step route's children[] for a child the run document does not list", async () => {
    const kid = subDoc.children[0]
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    client.setQueryData(stepQuery(subDoc.id, 0).queryKey, {
      children: [
        {
          id: kid.id,
          call_id: kid.parent_call_id,
          agent: "researcher",
          status: "succeeded",
          usage: kid.usage,
        },
      ],
    } as unknown as StepDoc)
    const doc: RunDoc = { ...subDoc, children: [] }
    await renderWithRouter(
      <StepList events={subEvents} folded={fold(subEvents)} doc={doc} />,
      client
    )
    const row = document.querySelector(
      `[data-step="0"] [data-child-row="${kid.id}"]`
    )
    expect(row?.querySelector("[data-child-agent]")?.textContent).toBe(
      "researcher"
    )
    expect(row?.querySelector("[data-child-usage]")?.textContent).toBe(
      "9 in / 5 out"
    )
  })

  // A10: call ids may repeat across steps (core/loop.go); the child's
  // id names its step (<parent>/<step>/<call id>), and that is the
  // call it joins — never the first call with the same id.
  it("joins a child to the call of the step its id names when a call id repeats across steps", async () => {
    const R = "r_rep"
    const U = { input_tokens: 1, output_tokens: 1 }
    const call = (index: number, name: string): WireEvent[] => [
      { type: "step_start", run_id: R, index },
      {
        type: "tool_start",
        run_id: R,
        seq: index + 1,
        call_id: "c1",
        name,
        args: {},
      },
      {
        type: "tool_finish",
        run_id: R,
        seq: index + 1,
        call_id: "c1",
        name,
        content: "ok",
        is_error: false,
      },
      { type: "step_finish", run_id: R, index, reason: "tool_calls", usage: U },
    ]
    const events: WireEvent[] = [
      { type: "run_start", id: R, model: { provider: "p", name: "m" } },
      ...call(0, "lookup_order"),
      ...call(1, "research"),
      { type: "run_finish", run_id: R, usage: U, steps: 2 },
    ]
    const kid = {
      ...subDoc.children[0],
      id: `${R}/1/c1`,
      parent_run_id: R,
      parent_call_id: "c1",
    }
    const doc: RunDoc = { ...subDoc, id: R, children: [kid] }
    await renderWithRouter(
      <StepList events={events} folded={fold(events)} doc={doc} />
    )
    expect(
      document.querySelector(`[data-step="1"] [data-child-row="${kid.id}"]`)
    ).toBeTruthy()
    expect(
      document.querySelector(`[data-step="0"] [data-child-row]`)
    ).toBeNull()
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
      {
        type: "tool_start",
        run_id: R,
        seq: 1,
        call_id: "c1",
        name: "research",
        args: {},
      },
      {
        type: "tool_finish",
        run_id: R,
        seq: 1,
        call_id: "c1",
        name: "research",
        content: "resumed",
        is_error: false,
      },
      { type: "step_start", run_id: R, index: 0 },
      {
        type: "tool_start",
        run_id: R,
        seq: 2,
        call_id: "c1",
        name: "research",
        args: {},
      },
      {
        type: "tool_finish",
        run_id: R,
        seq: 2,
        call_id: "c1",
        name: "research",
        content: "own",
        is_error: false,
      },
      {
        type: "step_finish",
        run_id: R,
        index: 0,
        reason: "tool_calls",
        usage: U,
      },
      { type: "run_finish", run_id: R, usage: U, steps: 1 },
    ]
    const base = subDoc.children[0]
    const resumed = {
      ...base,
      id: `${R}/resume/c1`,
      parent_run_id: R,
      parent_call_id: "c1",
    }
    const own = {
      ...base,
      id: `${R}/0/c1`,
      parent_run_id: R,
      parent_call_id: "c1",
    }
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    client.setQueryData(stepQuery(R, 0).queryKey, {
      children: [resumed, own].map((k) => ({
        id: k.id,
        call_id: "c1",
        agent: "researcher",
        status: "succeeded",
        usage: k.usage,
      })),
    } as unknown as StepDoc)
    const errors = vi.spyOn(console, "error").mockImplementation(() => {})
    try {
      // The run document lists neither: the step route's two c1
      // children are ambiguous, so no call draws one.
      const { unmount } = await renderWithRouter(
        <StepList
          events={events}
          folded={fold(events)}
          doc={{ ...subDoc, id: R, children: [] }}
        />,
        client
      )
      expect(
        document.querySelectorAll(`[data-step="0"] [data-child-row]`).length
      ).toBe(0)
      unmount()
      // The run document lists both: each call its own.
      await renderWithRouter(
        <StepList
          events={events}
          folded={fold(events)}
          doc={{ ...subDoc, id: R, children: [resumed, own] }}
        />,
        client
      )
      const rows = [
        ...document.querySelectorAll(`[data-step="0"] [data-child-row]`),
      ].map((r) => r.getAttribute("data-child-row"))
      expect(rows).toEqual([resumed.id, own.id])
      const dup = errors.mock.calls.filter((c) =>
        String(c[0]).includes("same key")
      )
      expect(dup).toEqual([])
    } finally {
      errors.mockRestore()
    }
  })
})

describe("the loop marks (UX-S1-07)", () => {
  /** One step calling poll({}) and its result. */
  const step = (i: number, content = "pending"): WireEvent[] => [
    { type: "step_start", run_id: "r_l", index: i },
    {
      type: "tool_start",
      run_id: "r_l",
      seq: 2 * i + 1,
      call_id: `c${i}`,
      name: "poll",
      args: { job: "j1" },
    },
    {
      type: "tool_finish",
      run_id: "r_l",
      seq: 2 * i + 2,
      call_id: `c${i}`,
      name: "poll",
      content,
      is_error: content !== "pending",
    },
    {
      type: "step_finish",
      run_id: "r_l",
      index: i,
      reason: "tool_calls",
      usage: { input_tokens: 1, output_tokens: 1 },
    },
  ]

  it("a loop_detected run's repeated steps link the first of the detector's set", async () => {
    // DetectLoops(3) failing at step 3: the set is steps 1–3, the count
    // and the step core's error names — step 0 is outside it, whatever
    // its args (the detector's streak, not a re-comparison).
    const events: WireEvent[] = [
      { type: "run_start", id: "r_l", model: { provider: "p", name: "m" } },
      ...step(0),
      ...step(1),
      ...step(2),
      ...step(3),
    ]
    const doc = {
      ...subDoc,
      id: "r_l",
      children: [],
      status: "failed",
      steps: 4,
      stopped_by: "loop_detected",
      err: "weft: run failed at step 3: weft: the model repeated the same tool calls too many times: 3 identical steps in a row",
    } as RunDoc
    await renderWithRouter(
      <StepList events={events} folded={fold(events)} doc={doc} />
    )
    const chip = (n: number) =>
      document.querySelector<HTMLAnchorElement>(
        `[data-step="${n}"] [data-loop-repeat]`
      )
    expect(chip(0)).toBeNull()
    expect(chip(1)).toBeNull() // the first of the set: what the others repeat
    for (const n of [2, 3]) {
      expect(chip(n)!.textContent).toBe("repeats step 1")
      expect(chip(n)!.getAttribute("href")).toBe("/runs/r_l?step=1&view=story")
      expect(chip(n)!.title).toContain("3 identical steps in a row")
    }
  })

  it("marks nothing on a run that did not stop on the detector", async () => {
    const events: WireEvent[] = [
      { type: "run_start", id: "r_l", model: { provider: "p", name: "m" } },
      ...step(0),
      ...step(1),
    ]
    const doc = {
      ...subDoc,
      id: "r_l",
      children: [],
      status: "failed",
      steps: 2,
      stopped_by: "max_steps",
    } as RunDoc
    await renderWithRouter(
      <StepList events={events} folded={fold(events)} doc={doc} />
    )
    expect(document.querySelector("[data-loop-repeat]")).toBeNull()
  })

  // UX-A-38: a long story folds to its step heads, or dims its quiet
  // steps so the problems stand out; the named step stays whole.
  it("collapse all steps and only problems (UX-A-38)", async () => {
    ;(Element.prototype as Partial<Element>).scrollIntoView ??= () => {}
    const events: WireEvent[] = [
      { type: "run_start", id: "r_l", model: { provider: "p", name: "m" } },
      ...step(0),
      ...step(1, "boom"),
      ...step(2),
    ]
    const doc = {
      ...subDoc,
      id: "r_l",
      children: [],
      status: "succeeded",
      steps: 3,
    } as RunDoc
    await renderWithRouter(
      <StepList events={events} folded={fold(events)} doc={doc} highlight={2} />
    )
    const card = (n: number) =>
      document.querySelector<HTMLElement>(`[data-step="${n}"]`)!
    fireEvent.click(screen.getByRole("button", { name: "collapse all steps" }))
    expect(card(0).dataset.folded).toBe("")
    expect(card(0).querySelector("[data-step-body]")!.className).toContain(
      "hidden"
    )
    expect(card(2).dataset.folded).toBeUndefined() // the named step
    fireEvent.click(screen.getByRole("button", { name: "expand all steps" }))
    expect(card(0).dataset.folded).toBeUndefined()

    fireEvent.click(screen.getByRole("button", { name: "only problems" }))
    expect(card(0).dataset.dimmed).toBe("")
    expect(card(1).dataset.dimmed).toBeUndefined() // its call failed
    expect(card(2).dataset.dimmed).toBeUndefined()
  })
})
