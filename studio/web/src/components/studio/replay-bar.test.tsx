// Replay tests (plan §7): the cadence table, and — the property that
// matters — at playhead k the page renders exactly the folded prefix
// (pinned in step-list.test.tsx); here that playback advances the
// playhead event by event and writes seeks, but not ticks, through.
import { act, fireEvent, screen } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { useState } from "react"

import type { WireEvent } from "@/lib/api"
import { cadenceMs, ReplayBar } from "@/components/studio/replay-bar"
import { renderWithRouter } from "@/test/render"

/** Feeds ticks and seeks back into the bar, the way the run page does. */
function Harness({ initial }: { initial: number | null }) {
  const [ph, setPh] = useState<number | null>(initial)
  return (
    <>
      <ReplayBar events={events} playhead={ph} onTick={setPh} onSeek={setPh} />
      <ReplayLog playhead={ph} />
    </>
  )
}

function ReplayLog({ playhead }: { playhead: number | null }) {
  return (
    <span data-testid="ph">
      {playhead === null ? events.length : playhead}/{events.length}
    </span>
  )
}

const events: WireEvent[] = [
  { type: "run_start", id: "r", model: { provider: "p", name: "m" } },
  { type: "step_start", run_id: "r", index: 0 },
  { type: "text_delta", run_id: "r", text: "a" },
  { type: "text_delta", run_id: "r", text: "b" },
  {
    type: "tool_start",
    run_id: "r",
    seq: 1,
    call_id: "c",
    name: "t",
    args: {},
  },
  {
    type: "run_finish",
    run_id: "r",
    usage: { input_tokens: 1, output_tokens: 1 },
    steps: 1,
  },
]

describe("cadenceMs", () => {
  it("streams deltas and lands boundaries", () => {
    expect(cadenceMs(events[2])).toBe(60)
    expect(cadenceMs(events[3])).toBe(60)
    expect(cadenceMs(events[0])).toBe(400)
    expect(cadenceMs(events[4])).toBe(400)
    expect(cadenceMs(events[5])).toBe(400)
  })
})

describe("ReplayBar", () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it("advances the playhead event by event while playing", async () => {
    await renderWithRouter(<Harness initial={0} />)
    fireEvent.click(screen.getByRole("button", { name: "play" }))
    expect(screen.getByTestId("ph").textContent).toBe("0/6")

    // run_start and step_start reveals are boundaries: 400 ms each.
    act(() => vi.advanceTimersByTime(400))
    expect(screen.getByTestId("ph").textContent).toBe("1/6")
    act(() => vi.advanceTimersByTime(400))
    expect(screen.getByTestId("ph").textContent).toBe("2/6")
    // two deltas at 60 ms each — one act per event so each next
    // timer is registered after the re-render flushes.
    act(() => vi.advanceTimersByTime(60))
    expect(screen.getByTestId("ph").textContent).toBe("3/6")
    act(() => vi.advanceTimersByTime(60))
    expect(screen.getByTestId("ph").textContent).toBe("4/6")
    // the tool_start boundary and the run_finish land it at the end.
    act(() => vi.advanceTimersByTime(400))
    expect(screen.getByTestId("ph").textContent).toBe("5/6")
    act(() => vi.advanceTimersByTime(400))
    expect(screen.getByTestId("ph").textContent).toBe("6/6")
  })

  it("pauses and seeks through the controls", async () => {
    await renderWithRouter(<Harness initial={null} />)

    // Live (null) shows the total; stepping back seeks to 5.
    expect(screen.getByTestId("ph").textContent).toBe("6/6")
    fireEvent.keyDown(window, { key: "[" })
    expect(screen.getByTestId("ph").textContent).toBe("5/6")
    fireEvent.keyDown(window, { key: "[" })
    expect(screen.getByTestId("ph").textContent).toBe("4/6")
    fireEvent.keyDown(window, { key: "]" })
    expect(screen.getByTestId("ph").textContent).toBe("5/6")
    // Space mid-stream plays from here; from the end it restarts.
    fireEvent.keyDown(window, { key: "]" })
    expect(screen.getByTestId("ph").textContent).toBe("6/6")
    fireEvent.keyDown(window, { key: " " })
    expect(screen.getByTestId("ph").textContent).toBe("0/6")
    // Back to live.
    fireEvent.click(screen.getByRole("button", { name: "back to live" }))
    expect(screen.getByTestId("ph").textContent).toBe("6/6")
  })
})
