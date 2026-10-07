// The live page holds one stream per agent, never one per run: a
// browser allows six connections per origin over HTTP/1.1, and a
// stream per running run (plus one per agent, as it was) meant the
// seventh run left every later request of the tab queued forever.
import { cleanup, configure, screen, waitFor, act } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import type { RunRow } from "@/lib/api"
import { LiveRuns, MAX_AGENT_STREAMS } from "@/components/studio/live-runs"
import { FakeEventSource } from "@/test/fake-event-source"
import { renderWithRouter } from "@/test/render"

// Waits are for conditions; the bound is a ceiling for a loaded machine.
configure({ asyncUtilTimeout: 10_000 })

const run = (id: string, agent: string, over: Partial<RunRow> = {}): RunRow =>
  ({
    id,
    agent,
    model: { provider: "p", name: "m" },
    status: "running",
    started: "2026-10-01T09:00:00Z",
    last_seen: "2026-10-01T09:00:01Z",
    ...over,
  }) as RunRow

describe("LiveRuns", () => {
  beforeEach(() => {
    FakeEventSource.reset()
    vi.stubGlobal("EventSource", FakeEventSource)
  })
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
  })

  it("opens one stream per agent, whatever the number of runs", async () => {
    const runs = Array.from({ length: 12 }, (_, i) =>
      run(`r${i}`, i % 2 ? "orders" : "research")
    )
    await renderWithRouter(<LiveRuns runs={runs} onStale={() => {}} />)
    expect(FakeEventSource.open()).toHaveLength(2)
    const selectors = FakeEventSource.open().map((es) =>
      new URL(es.url).searchParams.get("agent")
    )
    expect(selectors.sort()).toEqual(["orders", "research"])
    // Run frames only: an agent stream that asked for events too is
    // backfilled, on every browser reconnect (Last-Event-ID), with
    // every stored event of every run that agent ever made (live.go's
    // backfill pages the whole selector) — a long-lived agent's
    // history poured into the tab, one render per frame.
    for (const es of FakeEventSource.open())
      expect(new URL(es.url).searchParams.get("kinds")).toBe("run")
  })

  it("caps the streams of a large fleet and says which are live", async () => {
    const runs = Array.from({ length: 9 }, (_, i) => run(`r${i}`, `agent-${i}`))
    await renderWithRouter(<LiveRuns runs={runs} onStale={() => {}} />)
    expect(FakeEventSource.open()).toHaveLength(MAX_AGENT_STREAMS)
    expect(screen.getByText(/streaming 4 of 9 agents live/)).toBeTruthy()
  })

  it("updates a row in place from its run frame and refetches only on a change of membership", async () => {
    const onStale = vi.fn()
    await renderWithRouter(
      <LiveRuns runs={[run("r1", "orders")]} onStale={onStale} />
    )
    const es = FakeEventSource.instances[0]
    act(() => {
      es.connect()
      // A heartbeat-driven frame of a run already listed: no refetch.
      es.emit("run", { run: run("r1", "orders", { last_seen: "2026-10-01T09:00:05Z", event_count: 7 }) }, "1")
    })
    expect(onStale).not.toHaveBeenCalled()
    // The count is the row's own (the run frame carries it).
    await waitFor(() => expect(screen.getByText(/7 evt/)).toBeTruthy())
    act(() => {
      // It ended: the row says so at once, and the list is refetched.
      es.emit("run", { run: run("r1", "orders", { status: "succeeded", last_seen: "2026-10-01T09:00:09Z" }) }, "3")
      // A run the list has not seen started.
      es.emit("run", { run: run("r2", "orders") }, "4")
    })
    await waitFor(() => expect(screen.getByText("ended")).toBeTruthy())
    expect(onStale).toHaveBeenCalledTimes(1) // throttled: one refetch
  })

  it("closes every stream on unmount", async () => {
    const { unmount } = await renderWithRouter(
      <LiveRuns runs={[run("r1", "orders"), run("r2", "research")]} onStale={() => {}} />
    )
    unmount()
    expect(FakeEventSource.open()).toHaveLength(0)
  })
})
