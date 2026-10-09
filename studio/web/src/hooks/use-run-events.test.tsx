// The run page's events walk is the app's one raw fetch beside
// lib/api's get/post — and the only one that used to miss the bearer
// token, so under setups B/C (a token-walled Studio) every page of the
// walk 401'd and the run page showed the error instead of the story
// (programme audit P1-3). Pinned here: with a token stored the way the
// reader pastes it, every fetch the hook makes carries the header.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { act, cleanup, configure, renderHook, waitFor } from "@testing-library/react"

import { setStudioToken } from "@/lib/api"
import { FakeEventSource } from "@/test/fake-event-source"
import { withLiveGrant } from "@/test/fake-live-grant"
import { useRunEvents } from "./use-run-events"

// Waits are for conditions; the bound is a ceiling for a loaded machine.
configure({ asyncUtilTimeout: 10_000 })

const page = (pos: number, done: boolean) => ({
  events: [
    {
      pos,
      time: "2026-10-01T09:00:00Z",
      event: { type: "run_start", id: "r1", model: { provider: "p", name: "m" } },
    },
  ],
  next_after: null,
  done,
  gap_count: 0,
})

describe("useRunEvents fetch auth", () => {
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
    setStudioToken("")
  })
  beforeEach(() => {
    setStudioToken("")
  })

  it("sends the stored bearer token on every page fetch", async () => {
    setStudioToken("dev-secret")
    const fetchMock = vi.fn(
      async (_input: RequestInfo | URL, _init?: RequestInit) =>
        new Response(JSON.stringify(page(0, true)), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        })
    )
    vi.stubGlobal("fetch", withLiveGrant(fetchMock))

    const { result } = renderHook(() => useRunEvents("r1", "succeeded"))
    await waitFor(() => {
      expect(result.current.done).toBe(true)
      expect(result.current.error).toBeNull()
    })

    expect(fetchMock).toHaveBeenCalled()
    const headers = (fetchMock.mock.calls[0][1] as RequestInit).headers
    expect(new Headers(headers).get("Authorization")).toBe("Bearer dev-secret")
  })

  it("sends no Authorization header without a stored token (setup A)", async () => {
    const fetchMock = vi.fn(
      async (_input: RequestInfo | URL, _init?: RequestInit) =>
        new Response(JSON.stringify(page(0, true)), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        })
    )
    vi.stubGlobal("fetch", withLiveGrant(fetchMock))

    const { result } = renderHook(() => useRunEvents("r1", "succeeded"))
    await waitFor(() => expect(result.current.done).toBe(true))

    const headers = (fetchMock.mock.calls[0][1] as RequestInit).headers
    expect(new Headers(headers).get("Authorization")).toBeNull()
  })
})

// ── The walk, the live seam and the tail ───────────────────────────

const ev = (pos: number, type = "step_start") => ({
  pos,
  time: "2026-10-01T09:00:00Z",
  event:
    type === "run_start"
      ? { type, id: "r1", model: { provider: "p", name: "m" } }
      : { type, run_id: "r1", index: pos },
})
const pageOf = (
  positions: number[],
  next_after: number | null,
  done = false
) => ({ events: positions.map((p) => ev(p)), next_after, done, gaps: [] })
const json = (body: unknown) =>
  new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  })
const afterOf = (input: RequestInfo | URL) =>
  Number(new URL(String(input)).searchParams.get("after"))
const frame = (pos: number, kind = "event") => ({
  run_id: "r1",
  session_id: "",
  public_id: "",
  kind,
  pos,
  time: "2026-10-01T09:00:00Z",
  event:
    kind === "delta"
      ? { type: "text_delta", run_id: "r1", text: `d${pos}` }
      : { type: "step_start", run_id: "r1", index: pos },
})

describe("useRunEvents walk and tail", () => {
  beforeEach(() => {
    FakeEventSource.reset()
    vi.stubGlobal("EventSource", FakeEventSource)
  })
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  // A collapsed subagent block mounts the hook with no id: it used to
  // GET runs//events (a 404) on mount and again every 2 s while the
  // child was running.
  it("does nothing without a run id", async () => {
    const fetchMock = vi.fn(async () => json(pageOf([], null)))
    vi.stubGlobal("fetch", withLiveGrant(fetchMock))
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const { result } = renderHook(() => useRunEvents("", "running", { live: true }))
    // Past the 2 s poll: still nothing read.
    await vi.advanceTimersByTimeAsync(2500)
    expect(fetchMock).not.toHaveBeenCalled()
    expect(FakeEventSource.instances).toHaveLength(0)
    expect(result.current.loading).toBe(false)
  })

  // The seam: a frame that lands while the first walk is still paging
  // used to be dropped — and with it the event, for good, because the
  // next frame moved the cursor past it.
  it("keeps live frames that arrive during the initial walk", async () => {
    let release: (r: Response) => void = () => {}
    // The first page is held; any later read (the open's catch-up)
    // finds nothing new.
    const fetchMock = vi.fn((_i: RequestInfo | URL, _o?: RequestInit) =>
      fetchMock.mock.calls.length === 1
        ? new Promise<Response>((res) => {
            release = res
          })
        : Promise.resolve(json(pageOf([], null)))
    )
    vi.stubGlobal("fetch", withLiveGrant(fetchMock))
    const { result } = renderHook(() => useRunEvents("r1", "running", { live: true }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    const es = await waitFor(() => FakeEventSource.nth(0))
    es.connect()
    // The page was read at positions 0..1; 2 and 3 are published while
    // its response is still in flight.
    es.emit("record", frame(2), "7")
    es.emit("record", frame(3), "8")
    release(json(pageOf([0, 1], null)))
    await waitFor(() => expect(result.current.lastPos).toBe(3))
    expect(result.current.events).toHaveLength(4)
    expect(result.current.folded.steps.map((s) => s.index)).toEqual([0, 1, 2, 3])
  })

  // A frame whose position skips ahead means an event was missed (it
  // was published before this stream subscribed): the pages have it.
  it("refetches pages when a live frame skips a position", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) =>
      afterOf(input) === 0
        ? json(pageOf([0, 1], null))
        : json(pageOf([2, 3], null))
    )
    vi.stubGlobal("fetch", withLiveGrant(fetchMock))
    const { result } = renderHook(() => useRunEvents("r1", "running", { live: true }))
    await waitFor(() => expect(result.current.lastPos).toBe(1))
    const es = await waitFor(() => FakeEventSource.nth(0))
    es.connect()
    es.emit("record", frame(3), "9") // 2 was never delivered live
    await waitFor(() => expect(result.current.lastPos).toBe(3))
    expect(result.current.folded.steps.map((s) => s.index)).toEqual([0, 1, 2, 3])
    expect(fetchMock.mock.calls.map((c) => afterOf(c[0]))).toEqual([0, 2])
  })

  // The stream opens once its grant answers (plan C5) — after the first
  // page was read. An event published in between is in neither: the
  // open reads the pages past the walk, without waiting for the next
  // frame or the status flip.
  it("reads the pages past the walk when the stream opens after it", async () => {
    let stored = [0, 1]
    const fetchMock = vi.fn(async (input: RequestInfo | URL) =>
      json(pageOf(stored.filter((p) => p >= afterOf(input)), null))
    )
    const granted = withLiveGrant(fetchMock)
    let answerGrant = () => {}
    vi.stubGlobal("fetch", (input: RequestInfo | URL, init?: RequestInit) =>
      String(input).endsWith("/live-grant")
        ? new Promise<Response>((res) => {
            answerGrant = () => res(granted(input, init))
          })
        : granted(input, init)
    )
    const { result } = renderHook(() => useRunEvents("r1", "running", { live: true }))
    await waitFor(() => expect(result.current.lastPos).toBe(1))
    stored = [0, 1, 2] // published after the page, before the subscription
    answerGrant()
    const es = await waitFor(() => FakeEventSource.nth(0))
    es.connect()
    await waitFor(() => expect(result.current.lastPos).toBe(2))
    expect(fetchMock.mock.calls.map((c) => afterOf(c[0]))).toEqual([0, 2])
  })

  it("folds deltas and in-order events straight from the stream", async () => {
    const fetchMock = vi.fn(async () => json(pageOf([0], null)))
    vi.stubGlobal("fetch", withLiveGrant(fetchMock))
    const { result } = renderHook(() => useRunEvents("r1", "running", { live: true }))
    await waitFor(() => expect(result.current.lastPos).toBe(0))
    const es = await waitFor(() => FakeEventSource.nth(0))
    es.connect()
    es.emit("record", frame(0, "delta"))
    es.emit("record", frame(1), "5")
    es.emit("record", frame(1, "delta"))
    await waitFor(() => expect(result.current.events).toHaveLength(4))
    expect(result.current.lastPos).toBe(1)
    // The first walk and the open's catch-up (plan C5: the stream opens
    // after the walk began); no refetch for the frames: nothing skipped.
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  // A cursor that does not advance (a server bug, a proxy replaying a
  // page) must end the walk, not spin on the API.
  it("stops on a cursor that does not advance", async () => {
    const fetchMock = vi.fn(async () => json(pageOf([0], 1)))
    vi.stubGlobal("fetch", withLiveGrant(fetchMock))
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const { result } = renderHook(() => useRunEvents("r1", "succeeded"))
    await waitFor(() => expect(result.current.loading).toBe(false))
    // Past the 2 s poll: the walk does not spin.
    await vi.advanceTimersByTimeAsync(2500)
    expect(fetchMock.mock.calls.length).toBeLessThanOrEqual(4)
  })

  // done is "terminal", even on a partial page (api.go's eventsPage):
  // the walk follows next_after, and the stream only reads done once
  // drained.
  it("follows next_after past a done page and reports done when drained", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) =>
      afterOf(input) === 0
        ? json(pageOf([0, 1], 2, true))
        : json(pageOf([2], null, true))
    )
    vi.stubGlobal("fetch", withLiveGrant(fetchMock))
    const seenDone: [boolean, number][] = []
    const { result } = renderHook(() => {
      const s = useRunEvents("r1", "succeeded")
      seenDone.push([s.done, s.lastPos])
      return s
    })
    await waitFor(() => expect(result.current.lastPos).toBe(2))
    expect(result.current.done).toBe(true)
    // Never done while events remained.
    expect(seenDone.some(([done, pos]) => done && pos < 2)).toBe(false)
  })

  // One stream per run page: run frames ride the events stream (the
  // page used to open a second EventSource for them — two of a
  // browser's six connections per origin for one tab).
  it("hands run frames to onRun on the same stream", async () => {
    vi.stubGlobal("fetch", withLiveGrant(vi.fn(async () => json(pageOf([0], null)))))
    const onRun = vi.fn()
    renderHook(() => useRunEvents("r1", "running", { live: true, onRun }))
    await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1))
    const es = await waitFor(() => FakeEventSource.nth(0))
    expect(new URL(es.url).searchParams.get("kinds")).toBe("event,delta,run")
    es.connect()
    es.emit("run", { run: { id: "r1", status: "succeeded" } }, "12")
    expect(onRun).toHaveBeenCalledWith({ id: "r1", status: "succeeded" })
  })

  it("closes its stream and stops polling on unmount", async () => {
    const fetchMock = vi.fn(async () => json(pageOf([0], null)))
    vi.stubGlobal("fetch", withLiveGrant(fetchMock))
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const { unmount } = renderHook(() => useRunEvents("r1", "running", { live: true }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    unmount()
    expect(FakeEventSource.open()).toHaveLength(0)
    const calls = fetchMock.mock.calls.length
    // Past two 2 s polls: none ran.
    await vi.advanceTimersByTimeAsync(4500)
    expect(fetchMock.mock.calls.length).toBe(calls)
  })
})

// ── Second-pass review: the seam's holes ───────────────────────────

describe("useRunEvents seam holes", () => {
  beforeEach(() => {
    FakeEventSource.reset()
    vi.stubGlobal("EventSource", FakeEventSource)
  })
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  // The stream subscribes while the first page is in flight: what was
  // published between the page's read and the subscription never comes
  // live. A frame past that hole, held during the walk, must send the
  // reader back to the pages — folding it over the hole loses 2 and 3
  // for good (the cursor moves past them).
  it("re-reads the pages for a held frame that lands past a hole", async () => {
    let release: (r: Response) => void = () => {}
    const fetchMock = vi.fn((input: RequestInfo | URL) =>
      afterOf(input) === 0
        ? new Promise<Response>((res) => {
            release = res
          })
        : Promise.resolve(json(pageOf([2, 3, 4], null)))
    )
    vi.stubGlobal("fetch", withLiveGrant(fetchMock))
    const { result } = renderHook(() => useRunEvents("r1", "running", { live: true }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    const es = await waitFor(() => FakeEventSource.nth(0))
    es.connect()
    es.emit("record", frame(4), "9") // 2 and 3 were published before the subscription
    release(json(pageOf([0, 1], null)))
    await waitFor(() => expect(result.current.lastPos).toBe(4))
    expect(result.current.folded.steps.map((s) => s.index)).toEqual([0, 1, 2, 3, 4])
  })

  // A hole the pages do not fill either (the server reports it in
  // gaps: a lost batch) must not hold the tail back forever.
  it("folds past a hole the pages cannot fill", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) =>
      afterOf(input) === 0 ? json(pageOf([0, 1], null)) : json(pageOf([], null))
    )
    vi.stubGlobal("fetch", withLiveGrant(fetchMock))
    const { result } = renderHook(() => useRunEvents("r1", "running", { live: true }))
    await waitFor(() => expect(result.current.lastPos).toBe(1))
    const es = await waitFor(() => FakeEventSource.nth(0))
    es.connect()
    es.emit("record", frame(3), "9")
    await waitFor(() => expect(result.current.lastPos).toBe(3))
    // The walk, the open's catch-up, the read for the hole.
    expect(fetchMock.mock.calls.map((c) => afterOf(c[0]))).toEqual([0, 2, 2])
  })
})

describe("useRunEvents live publish cost", () => {
  beforeEach(() => {
    FakeEventSource.reset()
    vi.stubGlobal("EventSource", FakeEventSource)
  })
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  // Every publish re-renders the run page over the whole fold: a delta
  // stream published frame by frame re-rendered a 10k-event page tens
  // of times a second and locked the tab. A burst is one publish.
  it("coalesces a burst of live frames into one publish", async () => {
    vi.stubGlobal("fetch", withLiveGrant(vi.fn(async () => json(pageOf([0], null)))))
    const published = new Set<unknown>()
    const { result } = renderHook(() => {
      const s = useRunEvents("r1", "running", { live: true })
      published.add(s)
      return s
    })
    await waitFor(() => expect(result.current.lastPos).toBe(0))
    const es = await waitFor(() => FakeEventSource.nth(0))
    es.connect()
    const before = published.size
    // Each SSE frame is its own browser task — no batching for free:
    // act() flushes whatever a frame scheduled before the next one.
    for (let i = 0; i < 30; i++) act(() => es.emit("record", frame(i, "delta")))
    act(() => es.emit("record", frame(1), "5"))
    await waitFor(() => expect(result.current.events).toHaveLength(32))
    expect(published.size - before).toBe(1)
    expect(result.current.lastPos).toBe(1)
  })
})
