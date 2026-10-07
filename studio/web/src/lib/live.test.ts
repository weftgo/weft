// The live client (S4.5): one subscription that survives. A dropped
// connection the browser retries is left to resume (Last-Event-ID); a
// stream the server closed (overflow) or refused is reopened with
// backoff; every loss and every return tells the consumer to refetch
// pages; frames are deduped on (run, kind, pos); close() ends it all.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "./api"
import { openLive, throttle } from "./live"
import { FakeEventSource } from "@/test/fake-event-source"

const record = (pos: number, kind = "event") => ({
  run_id: "r1",
  session_id: "",
  public_id: "",
  kind,
  pos,
  time: "2026-10-01T09:00:00Z",
  event: { type: "step_start", run_id: "r1", index: pos },
})

describe("openLive", () => {
  beforeEach(() => {
    FakeEventSource.reset()
    vi.stubGlobal("EventSource", FakeEventSource)
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
    setStudioToken("")
  })

  it("builds the URL under the API base with selector, kinds and token", () => {
    setStudioToken("tok +/=")
    const live = openLive({ selector: { run: "a/0/c 1" }, kinds: ["event", "delta"] })
    const url = new URL(FakeEventSource.instances[0].url)
    expect(url.pathname).toBe("/api/live")
    expect(url.searchParams.get("run")).toBe("a/0/c 1")
    expect(url.searchParams.get("kinds")).toBe("event,delta")
    expect(url.searchParams.get("token")).toBe("tok +/=")
    live.close()
  })

  it("dedups record frames on (run, kind, pos)", () => {
    const seen: number[] = []
    const live = openLive({ selector: { run: "r1" }, onRecord: (r) => seen.push(r.pos) })
    const es = FakeEventSource.instances[0]
    es.connect()
    es.emit("record", record(0), "10")
    es.emit("record", record(0)) // a resume's backfill of the same record
    es.emit("record", record(0, "delta")) // another kind is another record
    es.emit("record", record(1), "11")
    expect(seen).toEqual([0, 0, 1])
    live.close()
  })

  it("lets the browser resume a dropped connection, then says refetch", () => {
    const onOverflow = vi.fn()
    const live = openLive({ selector: { agent: "orders" }, onOverflow })
    const es = FakeEventSource.instances[0]
    es.connect()
    es.dropRetrying()
    // Not closed, not replaced: EventSource reconnects with
    // Last-Event-ID and the server backfills (S4.5).
    expect(es.closedByClient).toBe(false)
    expect(FakeEventSource.instances).toHaveLength(1)
    expect(live.overflowed()).toBe(true) // down: the poll fallback may run
    expect(onOverflow).toHaveBeenCalledTimes(1) // lost: refetch
    es.connect()
    expect(live.overflowed()).toBe(false)
    expect(onOverflow).toHaveBeenCalledTimes(2) // back: refetch what the gap hid
    live.close()
  })

  it("reopens after an overflow frame, with backoff, and refetches twice", () => {
    const onOverflow = vi.fn()
    const live = openLive({ selector: { run: "r1" }, onOverflow })
    const first = FakeEventSource.instances[0]
    first.connect()
    first.emit("overflow", {})
    expect(first.closedByClient).toBe(true)
    expect(onOverflow).toHaveBeenCalledTimes(1)
    expect(live.overflowed()).toBe(true)
    expect(FakeEventSource.instances).toHaveLength(1)
    vi.advanceTimersByTime(1000)
    expect(FakeEventSource.instances).toHaveLength(2)
    FakeEventSource.instances[1].connect()
    expect(live.overflowed()).toBe(false)
    expect(onOverflow).toHaveBeenCalledTimes(2)
    live.close()
  })

  it("backs off a refused stream and gives up instead of hammering", () => {
    const onOverflow = vi.fn()
    const live = openLive({ selector: { run: "r1" }, onOverflow })
    // Every attempt is refused (the wall's 401 closes an EventSource
    // for good): 1 s, 2 s, 4 s… then no more.
    for (let i = 0; i < 20; i++) {
      FakeEventSource.instances.at(-1)!.fail()
      vi.advanceTimersByTime(60_000)
    }
    expect(FakeEventSource.instances.length).toBeLessThanOrEqual(7)
    expect(live.overflowed()).toBe(true)
    live.close()
  })

  it("close() stops everything: no reconnect, no late callbacks", () => {
    const onOverflow = vi.fn()
    const onRecord = vi.fn()
    const live = openLive({ selector: { run: "r1" }, onOverflow, onRecord })
    const es = FakeEventSource.instances[0]
    es.connect()
    es.emit("overflow", {})
    live.close()
    vi.advanceTimersByTime(120_000)
    expect(FakeEventSource.instances).toHaveLength(1)
    es.emit("record", record(5))
    expect(onRecord).not.toHaveBeenCalled()
    expect(onOverflow).toHaveBeenCalledTimes(1)
  })
})

describe("throttle", () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it("runs at once, then once per window however many calls land", () => {
    const fn = vi.fn()
    const t = throttle(fn, 1000)
    t()
    expect(fn).toHaveBeenCalledTimes(1)
    for (let i = 0; i < 50; i++) t()
    expect(fn).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(1000)
    expect(fn).toHaveBeenCalledTimes(2)
    vi.advanceTimersByTime(5000)
    expect(fn).toHaveBeenCalledTimes(2)
  })

  it("cancel drops the pending trailing call", () => {
    const fn = vi.fn()
    const t = throttle(fn, 1000)
    t()
    t()
    t.cancel()
    vi.advanceTimersByTime(5000)
    expect(fn).toHaveBeenCalledTimes(1)
  })
})
