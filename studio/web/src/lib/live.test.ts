// The live client (S4.5/S4.7, plan C5): one subscription that
// survives. Every connection is opened with a live grant (POST
// live-grant, the bearer in the header; the stream URL carries the
// sig, never the token). A dropped connection the browser retries is
// left to resume (Last-Event-ID) while the grant lasts; one after it is
// replaced by a new grant at once; a stream the server closed
// (overflow) or refused is reopened with backoff; a panel token's
// `expired` frame reopens only with a fresh bearer; every loss and
// every return tells the consumer to refetch pages; frames are deduped
// on (run, kind, pos); close() ends it all.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "./api"
import { freshBearer, openLive, panelTokenExp, throttle } from "./live"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio } from "@/test/fake-studio"

const record = (pos: number, kind = "event") => ({
  run_id: "r1",
  session_id: "",
  public_id: "",
  kind,
  pos,
  time: "2026-10-01T09:00:00Z",
  event: { type: "step_start", run_id: "r1", index: pos },
})

/** A panel token whose claims say scope and exp (the fake does not
 * check the signature; the wall compares the whole string). */
export const panelToken = (exp: number, scope = "read", publicId = "pub_1") =>
  `weft_pt.${btoa(JSON.stringify({ public_id: publicId, scope, exp: new Date(exp).toISOString() }))
    .replace(/=+$/, "")
    .replace(/\+/g, "-")
    .replace(/\//g, "_")}.c2ln`

/** flush lets the grant request travel (fetch, json) under fake timers. */
const flush = () => vi.advanceTimersByTimeAsync(0)

describe("openLive", () => {
  let studio: FakeStudio
  beforeEach(() => {
    FakeEventSource.reset()
    vi.stubGlobal("EventSource", FakeEventSource)
    vi.useFakeTimers()
    studio = new FakeStudio().install()
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
    setStudioToken("")
  })

  it("asks for a grant with the bearer in the header, then opens the stream with the sig — no token in any URL", async () => {
    setStudioToken("tok +/=")
    studio.requireToken("tok +/=")
    const live = openLive({ selector: { run: "a/0/c 1" }, kinds: ["event", "delta"] })
    await flush()
    const grants = studio.calls("POST live-grant")
    expect(grants).toHaveLength(1)
    expect(grants[0].headers.get("Authorization")).toBe("Bearer tok +/=")
    expect(grants[0].body).toEqual({ run: "a/0/c 1", kinds: "event,delta" })
    expect(grants[0].query.toString()).toBe("")
    const es = FakeEventSource.nth(0)
    expect(es.refused).toBeNull()
    const url = new URL(es.url)
    expect(url.pathname).toBe("/api/live")
    expect(url.searchParams.get("run")).toBe("a/0/c 1")
    expect(url.searchParams.get("kinds")).toBe("event,delta")
    expect(url.searchParams.get("sig")).toMatch(/^weft_lg\./)
    expect(url.searchParams.has("token")).toBe(false)
    expect(es.url).not.toContain(encodeURIComponent("tok +/="))
    live.close()
  })

  it("setup A: the grant is asked for with no Authorization header", async () => {
    const live = openLive({ selector: { session: "s_1" } })
    await flush()
    const [grant] = studio.calls("POST live-grant")
    expect(grant.headers.has("Authorization")).toBe(false)
    expect(grant.body).toEqual({ session: "s_1", kinds: "event,run" })
    expect(FakeEventSource.nth(0).refused).toBeNull()
    live.close()
  })

  it("dedups record frames on (run, kind, pos)", async () => {
    const seen: number[] = []
    const live = openLive({ selector: { run: "r1" }, onRecord: (r) => seen.push(r.pos) })
    await flush()
    const es = FakeEventSource.nth(0)
    es.connect()
    es.emit("record", record(0), "10")
    es.emit("record", record(0)) // a resume's backfill of the same record
    es.emit("record", record(0, "delta")) // another kind is another record
    es.emit("record", record(1), "11")
    expect(seen).toEqual([0, 0, 1])
    live.close()
  })

  it("lets the browser resume a dropped connection while the grant lasts, then says refetch", async () => {
    const onOverflow = vi.fn()
    const live = openLive({ selector: { agent: "orders" }, onOverflow })
    await flush()
    const es = FakeEventSource.nth(0)
    es.connect()
    es.dropRetrying()
    // Not closed, not replaced: EventSource reconnects with
    // Last-Event-ID and the server backfills (S4.5).
    expect(es.closedByClient).toBe(false)
    expect(FakeEventSource.instances).toHaveLength(1)
    expect(live.overflowed()).toBe(true) // down: the poll fallback may run
    expect(onOverflow).toHaveBeenCalledTimes(1) // lost: refetch
    vi.advanceTimersByTime(3000) // the browser's retry delay, well inside 60 s
    es.retry() // the same sig, still good
    expect(live.overflowed()).toBe(false)
    expect(onOverflow).toHaveBeenCalledTimes(2) // back: refetch what the gap hid
    expect(studio.calls("POST live-grant")).toHaveLength(1)
    live.close()
  })

  it("a drop after the grant's 60 s asks for a new grant at once instead of reusing the spent sig", async () => {
    const onOverflow = vi.fn()
    const live = openLive({ selector: { run: "r1" }, onOverflow })
    await flush()
    const first = FakeEventSource.nth(0)
    first.connect()
    vi.advanceTimersByTime(61_000) // the stream runs on past its grant
    first.dropRetrying()
    // The browser's own retry would carry the spent sig: refused.
    expect(first.closedByClient).toBe(true)
    await flush()
    expect(studio.calls("POST live-grant")).toHaveLength(2)
    const second = FakeEventSource.nth(1)
    expect(second.refused).toBeNull()
    expect(second.url).not.toBe(first.url)
    second.connect()
    expect(live.overflowed()).toBe(false)
    expect(onOverflow).toHaveBeenCalledTimes(2) // lost, then back: refetch
    live.close()
  })

  it("the browser's retry refused after the grant's 60 s gets a new grant too, on the backoff", async () => {
    const live = openLive({ selector: { run: "r1" } })
    await flush()
    const first = FakeEventSource.nth(0)
    first.connect()
    vi.advanceTimersByTime(59_000)
    first.dropRetrying() // inside the grant: the browser retries
    expect(first.closedByClient).toBe(false)
    vi.advanceTimersByTime(3000)
    first.retry() // now past 60 s: the fake's /api/live answers 401
    expect(first.readyState).toBe(FakeEventSource.CLOSED)
    await vi.advanceTimersByTimeAsync(1000) // the first backoff step
    expect(studio.calls("POST live-grant")).toHaveLength(2)
    FakeEventSource.nth(1).connect()
    expect(live.overflowed()).toBe(false)
    live.close()
  })

  it("reopens after an overflow frame, with backoff and a new grant, and refetches twice", async () => {
    const onOverflow = vi.fn()
    const live = openLive({ selector: { run: "r1" }, onOverflow })
    await flush()
    const first = FakeEventSource.nth(0)
    first.connect()
    first.emit("overflow", {})
    expect(first.closedByClient).toBe(true)
    expect(onOverflow).toHaveBeenCalledTimes(1)
    expect(live.overflowed()).toBe(true)
    expect(FakeEventSource.instances).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(1000)
    expect(FakeEventSource.instances).toHaveLength(2)
    expect(studio.calls("POST live-grant")).toHaveLength(2)
    FakeEventSource.nth(1).connect()
    expect(live.overflowed()).toBe(false)
    expect(onOverflow).toHaveBeenCalledTimes(2)
    live.close()
  })

  it("backs off a refused stream and gives up instead of hammering", async () => {
    const onOverflow = vi.fn()
    const live = openLive({ selector: { run: "r1" }, onOverflow })
    await flush()
    // Every attempt is refused (the wall's 401 closes an EventSource
    // for good): 1 s, 2 s, 4 s… then no more.
    for (let i = 0; i < 20; i++) {
      FakeEventSource.instances.at(-1)!.fail()
      await vi.advanceTimersByTimeAsync(60_000)
    }
    expect(FakeEventSource.instances.length).toBeLessThanOrEqual(7)
    expect(live.overflowed()).toBe(true)
    live.close()
  })

  it("backs off a refused grant the same way (a token the wall refuses)", async () => {
    setStudioToken("wrong")
    studio.requireToken("dev-secret")
    const live = openLive({ selector: { run: "r1" } })
    await vi.advanceTimersByTimeAsync(10 * 60_000)
    expect(FakeEventSource.instances).toHaveLength(0)
    expect(studio.calls("POST live-grant").length).toBeLessThanOrEqual(7)
    expect(live.overflowed()).toBe(true)
    live.close()
  })

  it("an expired frame with no fresh bearer stops the stream quietly", async () => {
    const quiet = [vi.spyOn(console, "error"), vi.spyOn(console, "warn"), vi.spyOn(console, "log")]
    const tok = panelToken(Date.now() + 30_000)
    setStudioToken(tok)
    studio.requireToken(tok)
    const onOverflow = vi.fn()
    const live = openLive({ selector: { public_id: "pub_1" }, kinds: ["run"], onOverflow })
    await flush()
    const es = FakeEventSource.nth(0)
    es.connect()
    vi.advanceTimersByTime(30_000) // the token's own expiry
    es.expire()
    expect(es.closedByClient).toBe(true)
    await vi.advanceTimersByTimeAsync(10 * 60_000)
    expect(FakeEventSource.instances).toHaveLength(1)
    expect(studio.calls("POST live-grant")).toHaveLength(1)
    expect(live.overflowed()).toBe(true) // the existing end state: down, the fallback stays
    expect(onOverflow).toHaveBeenCalledTimes(1)
    for (const spy of quiet) expect(spy).not.toHaveBeenCalled()
    live.close()
  })

  it("an expired frame with a fresh bearer asks for a new grant with it and reopens", async () => {
    const old = panelToken(Date.now() + 30_000)
    setStudioToken(old)
    const live = openLive({ selector: { public_id: "pub_1" }, kinds: ["run"] })
    await flush()
    const es = FakeEventSource.nth(0)
    es.connect()
    vi.advanceTimersByTime(30_000)
    const fresh = panelToken(Date.now() + 3600_000)
    setStudioToken(fresh) // the host handed a new token over
    es.expire()
    await flush()
    const grants = studio.calls("POST live-grant")
    expect(grants).toHaveLength(2)
    expect(grants[1].headers.get("Authorization")).toBe(`Bearer ${fresh}`)
    FakeEventSource.nth(1).connect()
    expect(live.overflowed()).toBe(false)
    live.close()
  })

  it("close() stops everything: no reconnect, no late callbacks", async () => {
    const onOverflow = vi.fn()
    const onRecord = vi.fn()
    const live = openLive({ selector: { run: "r1" }, onOverflow, onRecord })
    await flush()
    const es = FakeEventSource.nth(0)
    es.connect()
    es.emit("overflow", {})
    live.close()
    await vi.advanceTimersByTimeAsync(120_000)
    expect(FakeEventSource.instances).toHaveLength(1)
    es.emit("record", record(5))
    expect(onRecord).not.toHaveBeenCalled()
    expect(onOverflow).toHaveBeenCalledTimes(1)
  })

  it("close() before the grant answers opens nothing", async () => {
    const live = openLive({ selector: { run: "r1" } })
    live.close()
    await flush()
    expect(FakeEventSource.instances).toHaveLength(0)
  })
})

describe("freshBearer", () => {
  it("a new server token or an unexpired panel token is fresh; none, the same one or an expired one is not", () => {
    const now = Date.parse("2026-10-08T12:00:00Z")
    const ahead = panelToken(now + 60_000)
    const behind = panelToken(now - 1)
    expect(freshBearer("", "x", now)).toBe(false)
    expect(freshBearer(ahead, ahead, now)).toBe(false)
    expect(freshBearer(behind, ahead, now)).toBe(false)
    expect(freshBearer(ahead, behind, now)).toBe(true)
    expect(freshBearer("server-token", ahead, now)).toBe(true)
    expect(freshBearer("weft_pt.not-claims.sig", ahead, now)).toBe(false)
    expect(panelTokenExp(ahead)).toBe(now + 60_000)
    expect(panelTokenExp("server-token")).toBeNull()
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
