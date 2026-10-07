// Experiments join their source turn on forked_from (S4.7).
import { afterEach, describe, expect, it, vi } from "vitest"

import type { RunRow } from "./api"
import { fetchSessionForks, forkSource, nestExperiments } from "./experiments"

const run = (id: string, forked_from = "", started = "2026-10-01T09:00:00Z") =>
  ({ id, forked_from, started, playground: Boolean(forked_from) }) as RunRow

describe("forkSource", () => {
  it("splits the run id from the step it continued at", () => {
    expect(forkSource(run("x", "s_1-t3#2"))).toEqual({ runID: "s_1-t3", fromStep: 2 })
    expect(forkSource(run("x", "odd#id#0"))).toEqual({ runID: "odd#id", fromStep: 0 })
    expect(forkSource(run("x", "no-step"))).toEqual({ runID: "no-step", fromStep: 0 })
    expect(forkSource(run("x"))).toBeNull()
  })
})

describe("nestExperiments", () => {
  it("groups the forks under their turn, oldest first, and drops strangers", () => {
    const turns = [run("s_1-t1"), run("s_1-t2")]
    const nested = nestExperiments(turns, [
      run("pg_b", "s_1-t2#0", "2026-10-01T09:05:00Z"),
      run("pg_a", "s_1-t2#1", "2026-10-01T09:01:00Z"),
      run("pg_c", "s_1-t1#0"),
      run("pg_other", "s_9-t1#0"),
      run("plain"),
    ])
    expect(nested.get("s_1-t2")?.map((r) => r.id)).toEqual(["pg_a", "pg_b"])
    expect(nested.get("s_1-t1")?.map((r) => r.id)).toEqual(["pg_c"])
    expect(nested.size).toBe(2)
  })
})

describe("fetchSessionForks", () => {
  afterEach(() => vi.unstubAllGlobals())
  const page = (runs: RunRow[], next_before: string | null) =>
    new Response(JSON.stringify({ total: runs.length, runs, next_before }), {
      status: 200,
    })

  it("scopes by public id, follows next_before, and stops at null", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const q = new URL(String(input)).searchParams
      expect(q.get("playground")).toBe("true")
      expect(q.get("public_id")).toBe("pub_1")
      expect(q.get("limit")).toBe("500")
      return q.get("before")
        ? page([run("pg_2", "t#0")], null)
        : page([run("pg_1", "t#0")], "2026-10-01T08:00:00Z")
    })
    vi.stubGlobal("fetch", fetchMock)
    const out = await fetchSessionForks({ public_id: "pub_1", agent: "orders" })
    expect(out.runs.map((r) => r.id)).toEqual(["pg_1", "pg_2"])
    expect(out.truncated).toBe(false)
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it("is bounded, and says so, when the cursor never ends", async () => {
    let n = 0
    const fetchMock = vi.fn(async (_input: RequestInfo | URL) =>
      page([run(`pg_${n}`, "t#0")], `2026-10-01T0${n++}:00:00Z`)
    )
    vi.stubGlobal("fetch", fetchMock)
    const out = await fetchSessionForks({ public_id: "", agent: "orders" })
    expect(out.truncated).toBe(true)
    expect(fetchMock.mock.calls.length).toBe(4)
    expect(new URL(String(fetchMock.mock.calls[0][0])).searchParams.get("agent")).toBe("orders")
  })

  it("stops on a cursor that does not advance", async () => {
    const fetchMock = vi.fn(async () => page([run("pg", "t#0")], "2026-10-01T08:00:00Z"))
    vi.stubGlobal("fetch", fetchMock)
    const out = await fetchSessionForks({ public_id: "pub_1", agent: "" })
    expect(fetchMock.mock.calls.length).toBe(2)
    expect(out.truncated).toBe(false)
  })

  it("sends the exact cursor back, and walks a group sharing one timestamp", async () => {
    const t = "2026-10-01T08:00:00Z"
    const exact = (id: string, next_before: string | null, next_before_id: string | null) =>
      new Response(
        JSON.stringify({ total: 3, runs: [run(id, "t#0")], next_before, next_before_id }),
        { status: 200 }
      )
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const q = new URL(String(input)).searchParams
      const id = q.get("before_id")
      if (!q.get("before")) return exact("pg_1", t, "pg_1")
      if (id === "pg_1") return exact("pg_2", t, "pg_2")
      return exact("pg_3", null, null)
    })
    vi.stubGlobal("fetch", fetchMock)
    const out = await fetchSessionForks({ public_id: "pub_1", agent: "" })
    expect(out.runs.map((r) => r.id)).toEqual(["pg_1", "pg_2", "pg_3"])
    expect(
      fetchMock.mock.calls.map((c) => new URL(String(c[0])).searchParams.get("before_id"))
    ).toEqual([null, "pg_1", "pg_2"])
  })
})
