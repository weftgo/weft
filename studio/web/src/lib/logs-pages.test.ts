// lib/logs.ts (U11.2): every page of a run's lines up to the cap (a cut
// list says so), the WARN / ERROR ticks on the row a line landed on,
// "jump to span" read back by resolveRowSel (an ancestor's row when no
// row draws the span), the panel's hand-off link.
import { afterEach, describe, expect, it, vi } from "vitest"

import type { LogRow, Span as TimedSpan } from "./api"
import { logsLink } from "./links"
import { QueryClient } from "@tanstack/react-query"
import {
  allLogsQuery,
  fetchAllLogs,
  jumpSel,
  LOG_PAGES,
  LOGS,
  LOGS_POLL_MS,
  logTicks,
} from "./logs"
import type { AllLogs } from "./logs"
import { resolveRowSel } from "./run-tree"
import type { WaterfallRow } from "./waterfall"

const line = (index: number, sev: number, span?: string): LogRow => ({
  index,
  time: "2026-10-01T09:00:00Z",
  severity: sev >= 17 ? "ERROR" : sev >= 13 ? "WARN" : "DEBUG",
  severity_number: sev,
  body: `line ${index}`,
  attrs: {},
  ...(span ? { span_id: span } : {}),
})

const reply = (body: unknown) =>
  new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  })

describe("fetchAllLogs", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("follows next_from to the last page, merging lines and holes", async () => {
    const froms: (string | null)[] = []
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const u = new URL(String(input))
        froms.push(u.searchParams.get("from"))
        expect(u.searchParams.get("limit")).toBe("1000")
        expect(u.searchParams.get("severity")).toBe("warn")
        const from = Number(u.searchParams.get("from") ?? 0)
        return reply(
          from === 0
            ? {
                logs: [line(0, 13), line(1, 17)],
                next_from: 2,
                partial: true,
                partial_reason: "spans export when they end",
                holes: [{ hole: "gap", reason: "1 line" }],
              }
            : {
                logs: [line(2, 13)],
                holes: [{ hole: "gap", reason: "1 line" }],
              }
        )
      })
    )
    const all = await fetchAllLogs("r_a", "warn")
    expect(froms).toEqual([null, "2"])
    expect(all.logs.map((l) => l.index)).toEqual([0, 1, 2])
    expect(all.cut).toBe(false)
    expect(all.holes).toEqual([{ hole: "gap", reason: "1 line" }])
    expect(all.partial_reason).toBe("spans export when they end")
  })

  it("stops at the page cap and says the list is cut", async () => {
    let n = 0
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        const i = n++
        return reply({ logs: [line(i, 9)], next_from: i + 1 })
      })
    )
    const all = await fetchAllLogs("r_flood", undefined, 3)
    expect(n).toBe(3)
    expect(all.logs).toHaveLength(3)
    expect(all.cut).toBe(true)
    expect(LOGS.cutReason(10_000)).toContain("the first 10,000 lines are shown")
  })
})

describe("logTicks", () => {
  it("counts WARN and ERROR on the row the span (or its nearest drawn ancestor) is behind", () => {
    const rows = [
      { key: "s0", spanId: undefined },
      { key: "s0:m", spanId: "chat0" },
      { key: "c:0:c1", spanId: "tool0" },
    ]
    // app → tool0: a span no row draws, under the tool.
    const parents = new Map([
      ["app", "tool0"],
      ["tool0", "inv"],
      ["chat0", "inv"],
    ])
    const ticks = logTicks(
      [
        line(0, 5, "chat0"), // DEBUG: no tick
        line(1, 13, "chat0"),
        line(2, 17, "app"),
        line(3, 13, "app"),
        line(4, 13, "elsewhere"), // no row above it
        line(5, 17), // no span
      ],
      rows,
      parents
    )
    expect(Object.fromEntries(ticks)).toEqual({
      "s0:m": { warn: 1, error: 0 },
      "c:0:c1": { warn: 1, error: 1 },
    })
    expect(LOGS.tick({ warn: 1, error: 1 })).toBe("E1 W1")
    expect(LOGS.tickHint(1, 1)).toBe(
      "1 app log error, 1 app log warning under this row (its Logs tab)"
    )
  })

  // P8-fix-a finding 7: a cut warn read makes the counts floors.
  it("a cut read says its counts are floors: ≥ on the tick, at least in its hint", () => {
    expect(LOGS.tick({ warn: 2, error: 1 }, true)).toBe("≥E1 W2")
    expect(LOGS.tickHint(2, 1, true)).toBe(
      "at least 1 app log error, 2 app log warnings under this row (its Logs tab): the first 10,000 WARN+ lines were read"
    )
  })
})

// P8-fix-a finding 6: a running run's lines are read on from the last
// line held, never from 0 again; a cut read is never re-read; the
// finished run's read is whole, once.
describe("allLogsQuery pages on while the run runs", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("reads on from the line after the last held, keeps a cut read as it is", async () => {
    const froms: (string | null)[] = []
    let total = 2
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const u = new URL(String(input))
        froms.push(u.searchParams.get("from"))
        const from = Number(u.searchParams.get("from") ?? 0)
        const logs = []
        for (let i = from; i < total; i++) logs.push(line(i, 13))
        return reply({ logs })
      })
    )
    const first = await fetchAllLogs("r_live", "warn")
    expect(first.logs.map((l) => l.index)).toEqual([0, 1])
    total = 3
    const next = await fetchAllLogs("r_live", "warn", LOG_PAGES, first)
    expect(froms).toEqual([null, "2"])
    expect(next.logs.map((l) => l.index)).toEqual([0, 1, 2])
    // A cut read is the cap's: nothing more is read.
    const cut = { ...next, cut: true }
    expect(await fetchAllLogs("r_live", "warn", LOG_PAGES, cut)).toBe(cut)
    expect(froms).toHaveLength(2)
  })

  it("through the query: one read per poll from the held tail, none once cut; the finished run's key reads whole", async () => {
    const froms: (string | null)[] = []
    let cutNow = false
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const u = new URL(String(input))
        froms.push(u.searchParams.get("from"))
        const from = Number(u.searchParams.get("from") ?? 0)
        return reply(
          cutNow
            ? { logs: [line(from, 13)], next_from: from + 1 }
            : { logs: [line(from, 13)] }
        )
      })
    )
    const qc = new QueryClient()
    const live = allLogsQuery("r_q", "warn", true)
    await qc.fetchQuery(live)
    await qc.refetchQueries({ queryKey: live.queryKey })
    expect(froms).toEqual([null, "1"])
    const held = qc.getQueryData<AllLogs>(live.queryKey)!
    expect(held.logs.map((l) => l.index)).toEqual([0, 1])
    const interval = live.refetchInterval as (q: unknown) => number | false
    expect(interval({ state: { data: held } })).toBe(LOGS_POLL_MS)
    expect(interval({ state: { data: { ...held, cut: true } } })).toBe(false)
    // The finished run: its own key, read from 0, not polled.
    const done = allLogsQuery("r_q", "warn", false)
    expect(done.queryKey).not.toEqual(live.queryKey)
    await qc.fetchQuery(done)
    expect(froms).toEqual([null, "1", null])
    const doneInterval = done.refetchInterval as (q: unknown) => number | false
    expect(doneInterval({ state: { data: held } })).toBe(false)
    // A flood while running: the cap cuts it, and the cut read stops.
    froms.length = 0
    cutNow = true
    const flood = allLogsQuery("r_flood", undefined, true)
    const page = await qc.fetchQuery(flood)
    expect(page.cut).toBe(true)
    expect(froms).toHaveLength(LOG_PAGES)
    await qc.refetchQueries({ queryKey: flood.queryKey })
    expect(froms).toHaveLength(LOG_PAGES)
  })
})

describe("jump to span", () => {
  const timed = (id: string): TimedSpan =>
    ({ span_id: id, trace_id: "t", attrs: {} }) as unknown as TimedSpan
  const row = (key: string, span?: string, runId = "r_1") =>
    ({ key, runId, span: span ? timed(span) : undefined }) as WaterfallRow
  const rows = [
    row("r:r_1", "inv"),
    row("s0"),
    row("s0:m", "chat0"),
    row("c:0:c1", "tool0"),
    row("r:r_1/0/c9", "child_inv", "r_1/0/c9"),
  ]

  it("selects the row its span is behind, else its nearest drawn ancestor's", () => {
    const parents = new Map([
      ["app", "tool0"],
      ["deeper", "app"],
    ])
    expect(jumpSel("chat0")).toBe("t:chat0")
    expect(resolveRowSel(rows, jumpSel("chat0"), "r_1")).toBe("s0:m")
    expect(
      resolveRowSel(rows, jumpSel("deeper"), "r_1", undefined, parents)
    ).toBe("c:0:c1")
    // A child run's span: the row that draws it.
    expect(resolveRowSel(rows, jumpSel("child_inv"), "r_1")).toBe("r:r_1/0/c9")
    // No ancestor drawn: nothing.
    expect(resolveRowSel(rows, jumpSel("nope"), "r_1")).toBeUndefined()
  })

  it("the panel's hand-off lands on the step's row, Logs tab open", () => {
    expect(logsLink("r/1", 2)).toEqual({
      to: "/runs/$id",
      params: { id: "r/1" },
      search: { sel: "s2", tab: "logs" },
    })
  })
})
