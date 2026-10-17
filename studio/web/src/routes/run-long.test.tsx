// H2's Done line: a 5,000-step run scrolls. The run page opens a
// synthetic run of 5,000 steps (each a tool call pair: 20,002 events)
// and the story, the waterfall and the raw table hold a bounded DOM —
// the rows in view plus an overscan — at any scroll position; the
// scrubber jumps to any step (?step=, G1) and rings its card; and the
// fold's live tail keeps its event window inside the ring's capacity,
// folding a frame of 100 appends without an O(n²).
import {
  act,
  cleanup,
  configure,
  fireEvent,
  renderHook,
  screen,
  waitFor,
} from "@testing-library/react"
import axe from "axe-core"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc, RunsPage, Span as TimedSpan, WireEvent } from "@/lib/api"
import { waterfallRows } from "@/lib/waterfall"
import { useRunEvents } from "@/hooks/use-run-events"
import {
  describeStep,
  nearestOrdinal,
  stepOffsets,
} from "@/components/studio/step-scrubber"
import type { FoldedStep } from "@/lib/events"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { withLiveGrant } from "@/test/fake-live-grant"
import { FakeStudio, golden, pagedEvents } from "@/test/fake-studio"
import { scrollTo, stubScroll } from "@/test/scroll"

configure({ asyncUtilTimeout: 30_000 })
vi.setConfig({ testTimeout: 120_000 })

const RUN = "r_long"
const STEPS = 5_000
const usage = { input_tokens: 10, output_tokens: 4 }

/** run_start, STEPS × (step_start, tool_start, tool_finish,
 * step_finish), run_finish. */
function longRun(): WireEvent[] {
  const evs: WireEvent[] = [
    {
      type: "run_start",
      id: RUN,
      model: { provider: "wefttest", name: "script" },
      agent: "long",
    },
  ]
  for (let i = 0; i < STEPS; i++) {
    evs.push(
      { type: "step_start", run_id: RUN, index: i },
      {
        type: "tool_start",
        run_id: RUN,
        seq: 2 * i + 1,
        call_id: `c${i}`,
        name: "lookup",
        args: { i },
      },
      {
        type: "tool_finish",
        run_id: RUN,
        seq: 2 * i + 2,
        call_id: `c${i}`,
        name: "lookup",
        content: `ok ${i}`,
        is_error: false,
      },
      {
        type: "step_finish",
        run_id: RUN,
        index: i,
        reason: "tool_calls",
        usage,
        latency_ms: 100,
      }
    )
  }
  evs.push({ type: "run_finish", run_id: RUN, usage, steps: STEPS })
  return evs
}

const steps = () => document.querySelectorAll("[data-step]")
const ordinals = () =>
  [...steps()].map((n) => Number(n.getAttribute("data-step")))

describe("a 5,000-step run scrolls (plan H2)", () => {
  const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!
  const events = longRun()
  const doc: RunDoc = {
    ...rOK,
    id: RUN,
    agent: "long",
    status: "succeeded",
    steps: STEPS,
    event_count: events.length,
    trace_id: "",
    children: [],
    holes: [],
  }

  let unstubScroll = () => {}
  beforeEach(() => {
    stubBrowser()
    unstubScroll = stubScroll()
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
    unstubScroll()
    vi.unstubAllGlobals()
  })

  it("the story mounts a bounded window of cards — top, middle, end — and the scrubber lands on step 4,999", async () => {
    const { router } = renderApp(`/runs/${RUN}?view=story`)
    const list = await screen.findByRole(
      "list",
      { name: /^steps, 5,000$/ },
      { timeout: 60_000 }
    )
    // (a) the top: a window of cards, never 5,000.
    expect(steps().length).toBeGreaterThan(0)
    expect(steps().length).toBeLessThan(20)
    expect(ordinals()).toContain(0)
    // The items say where they sit in the whole.
    const first = list.querySelector('[role="listitem"]')!
    expect(first.getAttribute("aria-setsize")).toBe("5000")
    expect(first.getAttribute("aria-posinset")).toBe("1")

    // (b) the middle and the end mount the cards there.
    const height = Number.parseFloat(
      (list.firstElementChild as HTMLElement).style.height
    )
    expect(height).toBeGreaterThan(STEPS * 100)
    scrollTo(list, height / 2)
    await waitFor(() => expect(ordinals()).toContain(2500))
    expect(steps().length).toBeLessThan(20)
    expect(ordinals()).not.toContain(0)
    scrollTo(list, height)
    await waitFor(() => expect(ordinals()).toContain(4999))
    expect(steps().length).toBeLessThan(20)

    // (c) back to the top, then the scrubber: 4,999 names the last
    // step in the URL, scrolls to it and rings it.
    scrollTo(list, 0)
    await waitFor(() => expect(ordinals()).toContain(0))
    const slider = screen.getByRole("slider", { name: "jump to step" })
    expect(slider.getAttribute("aria-valuemin")).toBe("0")
    expect(slider.getAttribute("aria-valuemax")).toBe("4999")
    fireEvent.change(slider, { target: { value: "4999" } })
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ step: 4999 })
    )
    await waitFor(() =>
      expect(
        document
          .querySelector('[data-step="4999"]')
          ?.hasAttribute("data-highlighted")
      ).toBe(true)
    )
    expect(document.querySelectorAll("[data-highlighted]")).toHaveLength(1)
    await waitFor(() => expect(list.scrollTop).toBeGreaterThan(height * 0.9))
    expect(steps().length).toBeLessThan(20)
    expect(slider.getAttribute("aria-valuenow")).toBe("4999")
    expect(slider.getAttribute("aria-valuetext")).toMatch(
      /^step 4999 of 5,000 · tool_calls · t\+499\.9 s model time$/
    )
    // Scrolled away, the same step named again brings its card back
    // (the URL does not change).
    scrollTo(list, 0)
    await waitFor(() => expect(ordinals()).toContain(0))
    const box = screen.getByRole("spinbutton", { name: "step number" })
    fireEvent.change(box, { target: { value: "4999" } })
    fireEvent.keyDown(box, { key: "Enter" })
    await waitFor(() => expect(list.scrollTop).toBeGreaterThan(height * 0.9))
    expect(ordinals()).toContain(4999)
  })

  it("a scrub is one history entry: pushed on the drag's first move, replaced for the rest; back returns to where it started", async () => {
    const { router } = renderApp(`/runs/${RUN}?view=story&step=10`)
    const slider = await screen.findByRole(
      "slider",
      { name: "jump to step" },
      { timeout: 60_000 }
    )
    const entries = router.history.length
    const capture = vi.fn()
    slider.setPointerCapture = capture
    fireEvent.pointerDown(slider, { pointerId: 7 })
    // The pointer is captured: a release outside the thumb still ends it.
    expect(capture).toHaveBeenCalledWith(7)
    expect(router.history.length).toBe(entries) // pressed, not moved: no entry
    fireEvent.change(slider, { target: { value: "100" } })
    await waitFor(() => expect(router.history.length).toBe(entries + 1))
    for (const v of ["2000", "3000"])
      fireEvent.change(slider, { target: { value: v } })
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ step: 3000 })
    )
    expect(router.history.length).toBe(entries + 1)
    // The drag is spoken once the thumb rests, by the live region.
    await waitFor(() =>
      expect(
        document.querySelector("[data-scrubber-live]")?.textContent
      ).toMatch(/^step 3000 of 5,000 · /)
    )
    fireEvent.pointerUp(slider, { pointerId: 7 })
    expect(router.history.length).toBe(entries + 1) // the release navigates nothing
    expect(router.state.location.search).toMatchObject({ step: 3000 })
    act(() => router.history.back())
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ step: 10 })
    )
    // A key step is its own entry, announced by the slider's
    // aria-valuetext only: the live region keeps the drag's words.
    const spoken = document.querySelector("[data-scrubber-live]")?.textContent
    fireEvent.change(slider, { target: { value: "11" } })
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ step: 11 })
    )
    expect(router.history.length).toBe(entries + 1)
    await new Promise((r) => setTimeout(r, 200))
    expect(document.querySelector("[data-scrubber-live]")?.textContent).toBe(
      spoken
    )
    expect(slider.getAttribute("aria-valuetext")).toMatch(
      /^step 11 of 5,000 · /
    )
    // n (kbd-help) puts the keyboard in the box; a number + enter names
    // that step.
    const box = screen.getByRole("spinbutton", { name: "step number" })
    act(() => {
      ;(document.activeElement as HTMLElement | null)?.blur()
    })
    fireEvent.keyDown(window, { key: "n" })
    await waitFor(() => expect(document.activeElement).toBe(box))
    fireEvent.change(box, { target: { value: "4242" } })
    fireEvent.keyDown(box, { key: "Enter" })
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ step: 4242 })
    )
    await waitFor(() =>
      expect(
        document
          .querySelector('[data-step="4242"]')
          ?.hasAttribute("data-highlighted")
      ).toBe(true)
    )
  })

  it("the scrubber and the virtualised story keep their roles: axe finds nothing", async () => {
    renderApp(`/runs/${RUN}?view=story`)
    const list = await screen.findByRole(
      "list",
      { name: /^steps, 5,000$/ },
      { timeout: 60_000 }
    )
    const scrubber = document.querySelector<HTMLElement>("[data-scrubber]")!
    for (const el of [scrubber, list]) {
      // Every rule on (P8-pre-web): the call row's toggle is a button
      // beside its verbs, no role=button row around them.
      const res = await axe.run(el)
      expect(
        res.violations.map(
          (v) =>
            `${v.id}: ${v.nodes.map((n) => n.html.slice(0, 120)).join(" | ")}`
        )
      ).toEqual([])
    }
    expect(
      scrubber.querySelector("[data-scrubber-readout]")?.textContent
    ).toMatch(/^step 0 of 5,000 · tool_calls · t\+0 ms model time$/)
  })

  it("a ?step= deep link into the long story mounts and rings that card", async () => {
    renderApp(`/runs/${RUN}?view=story&step=3210`)
    await waitFor(
      () =>
        expect(
          document
            .querySelector('[data-step="3210"]')
            ?.hasAttribute("data-highlighted")
        ).toBe(true),
      { timeout: 60_000 }
    )
    expect(steps().length).toBeLessThan(20)
  })

  it("the waterfall mounts a bounded window of its 15,001 rows", async () => {
    renderApp(`/runs/${RUN}`)
    // The run, and per step its row, its model call and its tool call.
    const tree = await screen.findByRole(
      "tree",
      { name: "waterfall" },
      { timeout: 60_000 }
    )
    // The scroller around the tree (its axis row outside the tree).
    const box = tree.closest<HTMLElement>(".waterfall")!
    await waitFor(() => expect(box.hasAttribute("data-virtual")).toBe(true), {
      timeout: 60_000,
    })
    const rows = () => tree.querySelectorAll('[role="treeitem"]')
    expect(rows().length).toBeGreaterThan(0)
    expect(rows().length).toBeLessThan(80)
    // The spacers keep the whole height: scrolled to the end, the last
    // step's call row is mounted.
    scrollTo(box, 15_001 * 25)
    await waitFor(() =>
      expect(tree.querySelector('[data-span="c:4999:c4999"]')).toBeTruthy()
    )
    expect(rows().length).toBeLessThan(80)
    const last = tree.querySelector('[data-span="s4999"]')!
    expect(last.getAttribute("aria-level")).toBe("2")
    expect(last.getAttribute("aria-posinset")).toBe("5000")
  })

  it("the raw table mounts a bounded window of its 20,002 rows", async () => {
    renderApp(`/runs/${RUN}?view=raw`)
    const table = await screen.findByRole(
      "list",
      { name: /^events, 20,002 rows$/ },
      { timeout: 60_000 }
    )
    const rows = () => table.querySelectorAll("[data-pos]")
    expect(rows().length).toBeGreaterThan(0)
    expect(rows().length).toBeLessThan(80)
    scrollTo(table, 10_000 * 29)
    await waitFor(() =>
      expect(table.querySelector('[data-pos="10000"]')).toBeTruthy()
    )
    expect(rows().length).toBeLessThan(80)
    const item = table
      .querySelector('[data-pos="10000"]')!
      .closest('[role="listitem"]')!
    expect(item.getAttribute("aria-posinset")).toBe("10001")
    expect(item.getAttribute("aria-setsize")).toBe("20002")
    // Every rule on: the event row's toggle is a button beside its copy
    // button, no longer a role=button row around it (U29.5).
    const res = await axe.run(table)
    expect(res.violations.map((v) => v.id)).toEqual([])
  })
})

describe("the fold's live tail is a bounded ring (plan H2)", () => {
  beforeEach(() => {
    FakeEventSource.reset()
    vi.stubGlobal("EventSource", FakeEventSource)
  })
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
  })

  const frame = (pos: number) => ({
    run_id: "r1",
    session_id: "",
    public_id: "",
    kind: "event",
    pos,
    time: "2026-10-01T09:00:00Z",
    event: { type: "step_start", run_id: "r1", index: pos },
  })
  const delta = (pos: number) => ({
    run_id: "r1",
    session_id: "",
    public_id: "",
    kind: "delta",
    pos,
    time: "2026-10-01T09:00:00Z",
    event: { type: "text_delta", run_id: "r1", text: "w" },
  })

  it("5,000 live appends keep the window within capacity; a frame of 100 folds in under 10 ms", async () => {
    const CAP = 1_000
    const fetchMock = vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            events: [
              {
                pos: 0,
                time: "2026-10-01T09:00:00Z",
                event: { type: "run_start", id: "r1" },
              },
            ],
            next_after: null,
            done: false,
            gaps: [],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } }
        )
    )
    vi.stubGlobal("fetch", withLiveGrant(fetchMock))
    const { result } = renderHook(() =>
      useRunEvents("r1", "running", { live: true, capacity: CAP })
    )
    await waitFor(() => expect(result.current.lastPos).toBe(0))
    const es = await waitFor(() => FakeEventSource.nth(0))
    es.connect()
    // Let the open's catch-up read settle: frames then fold straight in.
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0))
    })

    let pos = 1
    let dpos = 0 // deltas carry their own counter
    // Each step streams three deltas: folded into its words, never
    // kept in the window — base and total count positioned events.
    for (; pos <= 4_900; pos++) {
      es.emit("record", frame(pos), String(pos))
      for (let d = 0; d < 3; d++) es.emit("record", delta(dpos++))
    }
    // A frame of 100 appends on top of 4,900 steps: O(1) each.
    const t0 = performance.now()
    for (; pos <= 5_000; pos++) es.emit("record", frame(pos), String(pos))
    const took = performance.now() - t0
    expect(took).toBeLessThan(10)

    await waitFor(() => expect(result.current.lastPos).toBe(5_000))
    const s = result.current
    expect(s.events.length).toBe(CAP)
    expect(s.attrs.length).toBe(CAP)
    expect(s.total).toBe(5_001)
    expect(s.base).toBe(5_001 - CAP)
    // The window is the stream's tail; the fold still holds every step.
    expect((s.events[0] as { index: number }).index).toBe(s.base)
    expect((s.events.at(-1) as { index: number }).index).toBe(5_000)
    expect(s.folded.steps).toHaveLength(5_000)
    expect(s.folded.steps.find((st) => st.index === 4_900)?.text).toBe("www")
    expect(s.events.every((e) => e.type === "step_start")).toBe(true)
  })
})

describe("the scrubber's read-out (plan H2)", () => {
  const st = (
    index: number,
    latencyMs?: number,
    finished = true
  ): FoldedStep => ({
    index,
    text: "",
    reasoning: "",
    toolCalls: [],
    from: index * 4,
    to: index * 4 + 3,
    finish: finished
      ? {
          reason: "stop",
          usage: { input_tokens: 0, output_tokens: 0 },
          ...(latencyMs ? { latencyMs } : {}),
        }
      : undefined,
  })

  it("snaps to the nearest ordinal a resumed run has", () => {
    expect(nearestOrdinal([0, 1, 5, 9], 3)).toBe(1)
    expect(nearestOrdinal([0, 1, 5, 9], 4)).toBe(5)
    expect(nearestOrdinal([0, 1, 5, 9], 99)).toBe(9)
    expect(nearestOrdinal([0, 1, 5, 9], -3)).toBe(0)
    expect(nearestOrdinal([], 3)).toBeUndefined()
  })

  it("offsets are the model time before a step; unknown once a latency is missing", () => {
    const three = [st(0, 1500), st(1), st(2, 200)]
    expect(stepOffsets(three)).toEqual([0, 1500, null])
    expect(describeStep(three, 1, "succeeded", stepOffsets(three))).toBe(
      "step 1 of 3 · stop · t+1.5 s model time"
    )
    expect(describeStep(three, 2, "succeeded", stepOffsets(three))).toBe(
      "step 2 of 3 · stop · events 8–11"
    )
    const live = [st(0, 10), st(1, undefined, false)]
    expect(describeStep(live, 1, "running", stepOffsets(live))).toBe(
      "step 1 of 2 · in flight · t+10 ms model time"
    )
  })
})

describe("the run tree's time scale over a long run (U8.2-fix)", () => {
  // N steps, each a chat span with one attempt under it and a tool span:
  // the tree reads each span's attributes a bounded number of times —
  // never once per span per model call (the O(n²) the review measured:
  // 5,000 steps with an attempt each took 1.5 s).
  const iso = (ms: number) => new Date(Date.UTC(2026, 0, 1) + ms).toISOString()
  function longSpans(n: number) {
    let reads = 0
    const sp = (
      id: string,
      parent: string,
      name: string,
      a: number,
      b: number,
      attrs: Record<string, unknown>
    ) => {
      const s = {
        trace_id: "t",
        span_id: id,
        parent_span_id: parent,
        name,
        start: iso(a),
        end: iso(b),
        status: "ok",
      }
      Object.defineProperty(s, "attrs", {
        get: () => (reads++, attrs),
        enumerable: true,
      })
      return s as unknown as TimedSpan
    }
    const run = { "weft.run.id": RUN }
    const spans = [
      sp("root", "", "invoke_agent long", 0, n * 10 + 5, {
        ...run,
        "gen_ai.operation.name": "invoke_agent",
      }),
    ]
    for (let i = 0; i < n; i++)
      spans.push(
        sp(`c${i}`, "root", "chat m", i * 10, i * 10 + 3, {
          ...run,
          "gen_ai.operation.name": "chat",
          "weft.step.index": i,
        }),
        sp(`a${i}`, `c${i}`, "attempt", i * 10, i * 10 + 3, {
          ...run,
          "gen_ai.operation.name": "attempt",
          "weft.step.index": i,
          "weft.attempt.index": 1,
        }),
        sp(`x${i}`, "root", "execute_tool lookup", i * 10 + 4, i * 10 + 8, {
          ...run,
          "gen_ai.operation.name": "execute_tool",
          "weft.step.index": i,
          "gen_ai.tool.call.id": `c${i}`,
          "gen_ai.tool.name": "lookup",
        })
      )
    return { spans, reads: () => reads }
  }

  it("reads each span's attributes a bounded number of times, linear in the run", () => {
    const perSpan = (n: number) => {
      const { spans, reads } = longSpans(n)
      const before = reads()
      const wf = waterfallRows(
        { runId: RUN, status: "succeeded", spans },
        "time"
      )
      expect(wf.rows).toHaveLength(1 + 4 * n)
      return (reads() - before) / spans.length
    }
    const small = perSpan(200)
    const large = perSpan(2_000)
    // Linear: the per-span count does not grow with the run (the
    // quadratic build grew it tenfold here).
    expect(large).toBeLessThan(small * 1.5)
    expect(large).toBeLessThan(40)
  })

  it("draws 10,000 steps with an attempt each well inside a second", () => {
    const { spans } = longSpans(10_000)
    const t = performance.now()
    const wf = waterfallRows({ runId: RUN, status: "running", spans }, "time")
    const ms = performance.now() - t
    expect(wf.rows).toHaveLength(40_001)
    // Measured ~130 ms (bun); the quadratic build took ~6 s. The bound
    // is generous: the machine may be shared.
    expect(ms).toBeLessThan(2_000)
  })
})
