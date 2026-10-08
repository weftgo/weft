// GET /api/runs/{id}/logs through fetchLogs (plan A7): the query it
// sends, the page it returns, and the read-scoped token's refusal read
// as a hole — {logs: [], badge: "hidden"} — the way fetchRequests reads
// its own, never as an error.
import { readdirSync, readFileSync } from "node:fs"
import { resolve } from "node:path"
import { afterEach, describe, expect, it, vi } from "vitest"

import { ApiError, fetchLogs, setStudioToken } from "./api"
import type { LogRow, LogsPage, StepAttempt, StepDoc } from "./api"

const API = resolve(process.cwd(), "../testdata/api")
const golden = (name: string): Record<string, unknown> =>
  JSON.parse(readFileSync(resolve(API, name), "utf8")) as Record<string, unknown>

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

// The goldens Go writes (studio/testdata/api, TestLogsRoute): fetchLogs
// hands each page through as the server wrote it.
describe("fetchLogs over the Go goldens", () => {
  afterEach(() => vi.unstubAllGlobals())
  for (const name of ["logs-ok", "logs-ok-paged", "logs-not-recorded"]) {
    it(`returns ${name} as written`, async () => {
      const page = golden(`${name}.golden.json`)
      vi.stubGlobal("fetch", vi.fn(async () => reply(page)))
      const got = await fetchLogs("r_ok")
      expect(got).toEqual(page)
      expect(Array.isArray(got.logs)).toBe(true)
    })
  }

  it("logs-ok carries its lines; logs-not-recorded its badge beside none", async () => {
    const ok = golden("logs-ok.golden.json") as unknown as LogsPage
    expect(ok.logs.length).toBeGreaterThan(0)
    expect(ok.badge).toBeUndefined()
    const none = golden("logs-not-recorded.golden.json") as unknown as LogsPage
    expect(none.logs).toEqual([])
    expect(none.badge).toBe("not_recorded")
    expect(none.reason).toBeTruthy()
  })

  it("a running run's page says partial, with its reason, and no badge", async () => {
    const page: LogsPage = {
      ...(golden("logs-ok.golden.json") as unknown as LogsPage),
      partial: true,
      partial_reason: "spans are exported when they end: lines under in-flight spans appear once they do, and indexes may shift",
    }
    vi.stubGlobal("fetch", vi.fn(async () => reply(page)))
    const got = await fetchLogs("r_live")
    expect(got.partial).toBe(true)
    expect(got.partial_reason).toBe("spans are exported when they end: lines under in-flight spans appear once they do, and indexes may shift")
    expect(got.badge).toBeUndefined()
  })
})

// A drift guard: every key a Go golden carries is one the TypeScript
// mirror declares. The key lists are typed against the interfaces
// (Record<keyof T, true>), so a field added on either side without the
// other fails here or in tsc.
const STEP_DOC_KEYS: Record<keyof StepDoc, true> = {
  run_id: true,
  step: true,
  status: true,
  reason: true,
  started: true,
  finished: true,
  latency_ms: true,
  ttft_ms: true,
  model: true,
  request: true,
  attempts: true,
  attempts_badge: true,
  messages_in: true,
  events: true,
  tool_calls: true,
  children: true,
  usage: true,
  compaction: true,
  holes: true,
}
const STEP_ATTEMPT_KEYS: Record<keyof StepAttempt, true> = {
  attempt: true,
  model: true,
  provider: true,
  outcome: true,
  error_type: true,
  retry_after_ms: true,
  started: true,
  finished: true,
  span_id: true,
  request_index: true,
  badge: true,
}
const LOGS_PAGE_KEYS: Record<keyof LogsPage, true> = {
  logs: true,
  next_from: true,
  partial: true,
  partial_reason: true,
  holes: true,
  badge: true,
  reason: true,
  fix: true,
}
const LOG_ROW_KEYS: Record<keyof LogRow, true> = {
  index: true,
  time: true,
  severity: true,
  severity_number: true,
  body: true,
  attrs: true,
  span_id: true,
}

function unknownKeys(obj: unknown, known: Record<string, true>): string[] {
  if (typeof obj !== "object" || obj === null) return []
  return Object.keys(obj).filter((k) => !(k in known))
}

describe("the TS mirrors know every key the Go goldens carry", () => {
  const files = readdirSync(API)
  const steps = files.filter((f) => /^step-.*\.golden\.json$/.test(f))
  const logs = files.filter((f) => /^logs-.*\.golden\.json$/.test(f))

  it("finds the goldens", () => {
    expect(steps.length).toBeGreaterThan(0)
    expect(logs.length).toBeGreaterThan(0)
  })

  for (const f of steps)
    it(`${f} ⊆ StepDoc`, () => {
      const doc = golden(f)
      expect(unknownKeys(doc, STEP_DOC_KEYS)).toEqual([])
      for (const a of (doc.attempts as unknown[] | undefined) ?? [])
        expect(unknownKeys(a, STEP_ATTEMPT_KEYS)).toEqual([])
    })

  for (const f of logs)
    it(`${f} ⊆ LogsPage`, () => {
      const page = golden(f)
      expect(unknownKeys(page, LOGS_PAGE_KEYS)).toEqual([])
      for (const row of (page.logs as unknown[] | undefined) ?? [])
        expect(unknownKeys(row, LOG_ROW_KEYS)).toEqual([])
    })
})
