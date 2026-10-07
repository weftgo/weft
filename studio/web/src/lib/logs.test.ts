// GET /api/runs/{id}/logs through fetchLogs (plan A7): the query it
// sends, the page it returns, and the read-scoped token's refusal read
// as a hole — {logs: [], badge: "hidden"} — the way fetchRequests reads
// its own, never as an error.
import { afterEach, describe, expect, it, vi } from "vitest"

import { ApiError, fetchLogs, setStudioToken } from "./api"

const reply = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })

describe("fetchLogs", () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    setStudioToken("")
  })

  it("sends from, limit and severity and returns the page", async () => {
    const page = {
      logs: [
        {
          index: 3,
          time: "2026-10-01T09:00:00Z",
          severity: "WARN",
          severity_number: 13,
          body: "orders cache miss",
          attrs: { cache: "orders" },
          span_id: "0102030405060708",
        },
      ],
      next_from: 4,
    }
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input))
      expect(url.pathname.endsWith("/api/runs/r%2F0%2Fc1/logs")).toBe(true)
      expect(url.searchParams.get("from")).toBe("3")
      expect(url.searchParams.get("limit")).toBe("1")
      expect(url.searchParams.get("severity")).toBe("warn")
      return reply(page)
    })
    vi.stubGlobal("fetch", fetchMock)
    expect(
      await fetchLogs("r/0/c1", { from: 3, limit: 1, severity: "warn" })
    ).toEqual(page)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it("reads a read-scoped token's 403 as the hidden hole", async () => {
    const refusal = {
      error: {
        code: "forbidden",
        message: "a read-scoped panel token does not read them",
      },
      badge: "hidden",
      reason: "your token's scope may not read this",
      fix: "use a playground-scoped token",
    }
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => reply(refusal, 403))
    )
    expect(await fetchLogs("r_a")).toEqual({
      logs: [],
      badge: "hidden",
      reason: "your token's scope may not read this",
      fix: "use a playground-scoped token",
    })
  })

  it("throws any other refusal", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        reply({ error: { code: "forbidden", message: "not yours" } }, 403)
      )
    )
    await expect(fetchLogs("r_b")).rejects.toBeInstanceOf(ApiError)
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        reply({ error: { code: "not_found", message: "no run r_c" } }, 404)
      )
    )
    await expect(fetchLogs("r_c")).rejects.toMatchObject({ status: 404 })
  })
})
