// A subagent block opened while its child runs: the block's own reads
// (the child's document and transcript) were fetched once, on expand,
// and never again — so a child that finished afterwards kept reading
// "running" and showed no words (deltas are not stored: a finished
// run's text comes from its transcript). The parent's row of the
// child is refreshed by the run page; the block follows it.
import { act, cleanup, configure, screen, waitFor } from "@testing-library/react"
import { useState } from "react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import type { RunRow, RunsPage } from "@/lib/api"
import { SubagentBlock } from "@/components/studio/subagent-block"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden, pagedEvents, transcriptOf } from "@/test/fake-studio"
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
    const say = (role: string, text: string) => ({ role, content: [{ type: "text", text }] })
    const events = [
      { type: "run_start", id: child.id, model: child.model, agent: "research" },
      { type: "step_start", run_id: child.id, index: 0 },
      { type: "step_finish", run_id: child.id, index: 0, reason: "stop", usage: { input_tokens: 1, output_tokens: 1 } },
      { type: "run_finish", run_id: child.id, usage: { input_tokens: 1, output_tokens: 1 }, steps: 1 },
    ].map((event, pos) => ({ pos, time: rOK.started, event }))
    const studio = new FakeStudio()
      .on(`GET runs/${child.id}`, () => ({ ...child, children: [], ...(finished ? { status: "succeeded" } : {}) }))
      .on(
        `GET runs/${child.id}/events`,
        (req) => pagedEvents(finished ? events : events.slice(0, 3), { done: () => finished })(req)
      )
      .on(`GET runs/${child.id}/transcript`, () =>
        transcriptOf(finished ? [[say("user", "dig")], [say("assistant", "the finding")]] : [[say("user", "dig")]])
      )
      .install()
    await renderWithRouter(<Harness />)
    await waitFor(() => expect(studio.calls(`GET runs/${child.id}/transcript`)).toHaveLength(1))
    expect(screen.queryByText("the finding")).toBeNull()
    finished = true
    act(() => setChild({ ...child, status: "succeeded", finished: rOK.finished }))
    expect(await screen.findByText("the finding")).toBeTruthy()
  })
})
