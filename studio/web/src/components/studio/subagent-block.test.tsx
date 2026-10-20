// A subagent block opened while its child runs: the block's own reads
// (the child's document and transcript) were fetched once, on expand,
// and never again — so a child that finished afterwards kept reading
// "running" and showed no words (deltas are not stored: a finished
// run's text comes from its transcript). The parent's row of the
// child is refreshed by the run page; the block follows it.
import {
  act,
  cleanup,
  configure,
  fireEvent,
  screen,
  waitFor,
} from "@testing-library/react"
import { useState } from "react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import type { RunRow, RunsPage } from "@/lib/api"
import { SubagentBlock } from "@/components/studio/subagent-block"
import {
  RequestSection,
  useRunRequests,
} from "@/components/studio/step-request"
import { FakeEventSource } from "@/test/fake-event-source"
import {
  FakeStudio,
  golden,
  pagedEvents,
  pagedRequests,
  transcriptOf,
} from "@/test/fake-studio"
import { renderWithRouter } from "@/test/render"

// Waits are for conditions; the bound is a ceiling for a loaded machine.
configure({ asyncUtilTimeout: 10_000 })

const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!
const child: RunRow = {
  ...rOK,
  id: "p/0/call_1",
  parent_run_id: "p",
  parent_call_id: "call_1",
  status: "running",
  finished: null,
}

let setChild: (r: RunRow) => void = () => {}
function Harness() {
  const [c, set] = useState(child)
  setChild = set
  return <SubagentBlock child={c} defaultOpen />
}

describe("SubagentBlock", () => {
  beforeEach(() => {
    FakeEventSource.reset()
    vi.stubGlobal("EventSource", FakeEventSource)
  })
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
  })

  it("re-reads the child's words once the parent says it finished", async () => {
    let finished = false
    const say = (role: string, text: string) => ({
      role,
      content: [{ type: "text", text }],
    })
    const events = [
      {
        type: "run_start",
        id: child.id,
        model: child.model,
        agent: "research",
      },
      { type: "step_start", run_id: child.id, index: 0 },
      {
        type: "step_finish",
        run_id: child.id,
        index: 0,
        reason: "stop",
        usage: { input_tokens: 1, output_tokens: 1 },
      },
      {
        type: "run_finish",
        run_id: child.id,
        usage: { input_tokens: 1, output_tokens: 1 },
        steps: 1,
      },
    ].map((event, pos) => ({ pos, time: rOK.started, event }))
    const studio = new FakeStudio()
      .on(`GET runs/${child.id}`, () => ({
        ...child,
        children: [],
        ...(finished ? { status: "succeeded" } : {}),
      }))
      .on(`GET runs/${child.id}/events`, (req) =>
        pagedEvents(finished ? events : events.slice(0, 3), {
          done: () => finished,
        })(req)
      )
      .on(`GET runs/${child.id}/transcript`, () =>
        transcriptOf(
          finished
            ? [[say("user", "dig")], [say("assistant", "the finding")]]
            : [[say("user", "dig")]]
        )
      )
      .install()
    await renderWithRouter(<Harness />)
    await waitFor(() =>
      expect(studio.calls(`GET runs/${child.id}/transcript`)).toHaveLength(1)
    )
    expect(screen.queryByText("the finding")).toBeNull()
    finished = true
    act(() =>
      setChild({ ...child, status: "succeeded", finished: rOK.finished })
    )
    expect(await screen.findByText("the finding")).toBeTruthy()
  })

  // Parity with the panel's nested row (panel/subagents.test.ts, the
  // same rows): statusOf's word, never the stored one (W7-prep).
  it.each<[string, Partial<RunRow>]>([
    ["parked", { status: "succeeded", pending: 1 }],
    [
      "cancelled",
      {
        status: "failed",
        error_type: "cancelled",
        cancel_cause: "studio.cancel",
      },
    ],
  ])("reads a %s child in statusOf's word", async (word, over) => {
    new FakeStudio().install()
    const { container } = await renderWithRouter(
      <SubagentBlock child={{ ...child, ...over, finished: rOK.finished }} />
    )
    await waitFor(() =>
      expect(container.querySelector("[data-child-status]")?.textContent).toBe(
        word
      )
    )
  })

  // P9-pre-web-fix4 F2: the child's inline request pane draws the same
  // step numbers as the parent's; its tab ids must not repeat them, and
  // each tab and tabpanel must name its partner inside its own run.
  it("a parent's request pane and its inline child's have unique ids, each aria reference resolving inside its own run", async () => {
    const done: RunRow = {
      ...child,
      status: "succeeded",
      finished: rOK.finished,
    }
    const events = [
      {
        type: "run_start",
        id: child.id,
        model: child.model,
        agent: "research",
      },
      { type: "step_start", run_id: child.id, index: 0 },
      {
        type: "step_finish",
        run_id: child.id,
        index: 0,
        reason: "stop",
        usage: { input_tokens: 1, output_tokens: 1 },
      },
      {
        type: "run_finish",
        run_id: child.id,
        usage: { input_tokens: 1, output_tokens: 1 },
        steps: 1,
      },
    ].map((event, pos) => ({ pos, time: rOK.started, event }))
    const reqs = () =>
      pagedRequests(golden<Parameters<typeof pagedRequests>[0]>("requests-ok"))
    new FakeStudio()
      .on("GET meta", {
        ...golden<Record<string, unknown>>("meta"),
        capabilities: ["requests"],
      })
      .on(`GET runs/${child.id}`, { ...done, children: [] })
      .on(`GET runs/${child.id}/events`, pagedEvents(events))
      .on(`GET runs/${child.id}/transcript`, transcriptOf([]))
      .on(`GET runs/${child.id}/requests`, reqs())
      .on("GET runs/p/requests", reqs())
      .install()
    function Parent() {
      const req = useRunRequests("p", { enabled: true, running: false })
      return (
        <div data-run="p">
          {req ? <RequestSection req={req} step={0} open runId="p" /> : null}
          <div data-run={child.id}>
            <SubagentBlock child={done} defaultOpen />
          </div>
        </div>
      )
    }
    const { container } = await renderWithRouter(<Parent />)
    const childReq = () =>
      container.querySelector<HTMLElement>('[data-child-request="0"]')
    await waitFor(() =>
      expect(childReq()?.querySelector("[aria-expanded]")).toBeTruthy()
    )
    fireEvent.click(childReq()!.querySelector("[aria-expanded]")!)
    await waitFor(() =>
      expect(container.querySelectorAll('[role="tab"]').length).toBe(4)
    )
    const ids = Array.from(container.querySelectorAll("[id]")).map((e) => e.id)
    expect(ids.length).toBeGreaterThanOrEqual(6)
    expect(new Set(ids).size).toBe(ids.length)
    // The run an element is drawn in: its nearest data-run.
    const runOf = (e: Element) =>
      e.closest("[data-run]")?.getAttribute("data-run")
    const refs = container.querySelectorAll(
      "[aria-labelledby], [aria-controls]"
    )
    expect(refs.length).toBeGreaterThanOrEqual(6)
    for (const e of Array.from(refs)) {
      for (const attr of ["aria-labelledby", "aria-controls"]) {
        const v = e.getAttribute(attr)
        if (!v) continue
        for (const id of v.split(/\s+/)) {
          const to = document.getElementById(id)
          // A tab's aria-controls names the panel only while it is drawn.
          if (!to && attr === "aria-controls") continue
          expect(to, `${attr}=${id}`).toBeTruthy()
          expect(runOf(to!)).toBe(runOf(e))
        }
      }
    }
    const labelled = container.querySelectorAll('[role="tabpanel"]')
    expect(labelled).toHaveLength(2)
  })
})
