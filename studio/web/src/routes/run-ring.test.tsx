// H2 review: the run page past the ring's capacity. The capacity is
// made small here (1,000) so a 300-step run (1,202 events) evicts its
// head: the page says which positions it shows and where the rest are,
// and replay is off — no replay bar, and a ?t= playhead is ignored.
import { cleanup, configure, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc, RunsPage, WireEvent } from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden, pagedEvents } from "@/test/fake-studio"
import type * as Ring from "@/lib/ring"

vi.mock("@/lib/ring", async (importOriginal) => ({
  ...(await importOriginal<typeof Ring>()),
  EVENT_RING_CAPACITY: 1_000,
}))

configure({ asyncUtilTimeout: 30_000 })
vi.setConfig({ testTimeout: 60_000 })

const RUN = "r_ring"
const STEPS = 300
const usage = { input_tokens: 1, output_tokens: 1 }

function run(): WireEvent[] {
  const evs: WireEvent[] = [
    { type: "run_start", id: RUN, model: { provider: "p", name: "m" } },
  ]
  for (let i = 0; i < STEPS; i++)
    evs.push(
      { type: "step_start", run_id: RUN, index: i },
      {
        type: "tool_start",
        run_id: RUN,
        seq: 2 * i + 1,
        call_id: `c${i}`,
        name: "t",
        args: {},
      },
      {
        type: "tool_finish",
        run_id: RUN,
        seq: 2 * i + 2,
        call_id: `c${i}`,
        name: "t",
        content: "ok",
        is_error: false,
      },
      {
        type: "step_finish",
        run_id: RUN,
        index: i,
        reason: "tool_calls",
        usage,
      }
    )
  evs.push({ type: "run_finish", run_id: RUN, usage, steps: STEPS })
  return evs
}

describe("a run past the ring's capacity (plan H2)", () => {
  const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!
  const events = run()
  const doc: RunDoc = {
    ...rOK,
    id: RUN,
    status: "succeeded",
    steps: STEPS,
    event_count: events.length,
    trace_id: "",
    children: [],
    holes: [],
  }
  beforeEach(() => {
    stubBrowser()
    FakeEventSource.reset()
    vi.stubGlobal("EventSource", FakeEventSource)
    setStudioToken("")
    new FakeStudio()
      .on("GET meta", {
        ...golden<Record<string, unknown>>("meta"),
        capabilities: ["ingest"],
      })
      .on(`GET runs/${RUN}`, doc)
      .on(
        `GET runs/${RUN}/events`,
        pagedEvents(
          events.map((event, pos) => ({ pos, time: rOK.started, event })),
          { done: true }
        )
      )
      .on(`GET runs/${RUN}/transcript`, { run_id: RUN, batches: [] })
      .on(`GET runs/${RUN}/spans`, { spans: [] })
      .install()
  })
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
  })

  it("says the window is partial, turns replay off and ignores ?t=", async () => {
    renderApp(`/runs/${RUN}?view=story&t=5`)
    const note = await waitFor(() => {
      const n = document.querySelector("[data-ring-note]")
      expect(n).toBeTruthy()
      return n!
    })
    expect(note.textContent).toMatch(
      /showing the last 1,000 events \(positions 202–1,201\); the 202 before them are on the API/
    )
    // No replay bar, no playhead: the story reads the whole fold.
    expect(document.querySelector("[data-replay]")).toBeNull()
    expect(screen.queryByText(/^replay · /)).toBeNull()
    expect(document.querySelector('[data-step="0"]')).toBeTruthy()
    // No jump-to-here buttons: there is no prefix to replay.
    expect(
      screen.queryByRole("button", { name: /^jump to this step/ })
    ).toBeNull()
  })

  it("the raw view numbers the kept window from its base and says copy covers it", async () => {
    renderApp(`/runs/${RUN}?view=raw`)
    await waitFor(() =>
      expect(document.body.textContent).toMatch(
        /positions 202…1201 \(the last 1,000 kept; 0…201 on the API — copy and download cover the kept window\)/
      )
    )
    expect(document.querySelector('[data-pos="202"]')).toBeTruthy()
    expect(document.querySelector('[data-pos="0"]')).toBeNull()
  })
})
