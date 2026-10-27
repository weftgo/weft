// The Live page's row fold (plan H6): what the all stream's frames make
// of a run — H6.1's step / tokens / TTFT rule, the grace, the resume's
// unreadable marks, the ?peek= validation.
import { describe, expect, it } from "vitest"

import type { RunRow } from "./api"
import type { LiveRecord } from "./live"
import {
  LIVE_GRACE_MS,
  RUNNING_COST_REASON,
  applyBackfillError,
  applyRecord,
  applyRun,
  applySeed,
  costFieldsOf,
  emptyLiveRows,
  matchesFilter,
  prune,
  rowsOf,
  stepText,
  tokensOf,
  validPeek,
} from "./live-rows"

const run = (id: string, over: Partial<RunRow> = {}): RunRow =>
  ({
    id,
    agent: "orders",
    status: "running",
    pending: 0,
    steps: 0,
    usage: { input_tokens: 0, output_tokens: 0 },
    started: "2026-10-01T09:00:00Z",
    last_seen: "2026-10-01T09:00:01Z",
    finished: null,
    ...over,
  }) as RunRow
const rec = (run_id: string, pos: number, event: unknown): LiveRecord =>
  ({
    seq: 0,
    run_id,
    session_id: "",
    public_id: "",
    kind: "event",
    pos,
    time: "",
    event,
  }) as LiveRecord

describe("the row fold", () => {
  it("step, tokens and TTFT from step_start / step_finish", () => {
    let s = applyRun(emptyLiveRows, run("r1"), 0)
    s = applyRecord(s, rec("r1", 0, { type: "run_start", id: "r1" }))
    s = applyRecord(s, rec("r1", 1, { type: "step_start", index: 0 }))
    let v = rowsOf(s)[0]
    expect(stepText(v).text).toBe("1…")
    expect(tokensOf(v)).toBeNull()
    s = applyRecord(
      s,
      rec("r1", 2, {
        type: "step_finish",
        index: 0,
        usage: { input_tokens: 7, output_tokens: 3 },
        ttft_ms: 90,
      })
    )
    v = rowsOf(s)[0]
    expect(stepText(v).text).toBe("1")
    expect(tokensOf(v)).toEqual({ n: 10, input: 7, output: 3, partial: false })
    expect(v.stats?.ttftMs).toBe(90)
  })

  it("a re-delivered step_finish (a resume's backfill past the dedup window) does not change the total", () => {
    const fin = (pos: number, index: number, i: number, o: number) =>
      ({
        run_id: "r",
        session_id: "",
        public_id: "",
        kind: "event",
        pos,
        time: "",
        event: {
          type: "step_finish",
          run_id: "r",
          index,
          usage: { input_tokens: i, output_tokens: o },
        },
      }) as unknown as LiveRecord
    let s = applyRecord(emptyLiveRows, {
      ...fin(0, 0, 0, 0),
      event: { type: "run_start", id: "r" },
    } as LiveRecord)
    s = applyRecord(s, fin(1, 0, 10, 2))
    s = applyRecord(s, fin(2, 1, 20, 4))
    expect([s.stats.r.input, s.stats.r.output]).toEqual([30, 6])
    s = applyRecord(s, fin(1, 0, 10, 2))
    s = applyRecord(s, fin(2, 1, 20, 4))
    expect([s.stats.r.input, s.stats.r.output]).toEqual([30, 6])
  })

  it("a run joined mid-way counts only what it saw, and says so", () => {
    let s = applyRun(emptyLiveRows, run("r1"))
    s = applyRecord(
      s,
      rec("r1", 9, {
        type: "step_finish",
        index: 4,
        usage: { input_tokens: 1, output_tokens: 1 },
      })
    )
    expect(tokensOf(rowsOf(s)[0])?.partial).toBe(true)
  })

  it("the final row's usage and steps win once the run ended", () => {
    let s = applyRun(emptyLiveRows, run("r1"))
    s = applyRecord(
      s,
      rec("r1", 3, {
        type: "step_finish",
        index: 1,
        usage: { input_tokens: 1, output_tokens: 1 },
      })
    )
    s = applyRun(
      s,
      run("r1", {
        status: "succeeded",
        steps: 2,
        usage: { input_tokens: 50, output_tokens: 5 },
        last_seen: "2026-10-01T09:00:09Z",
      })
    )
    const v = rowsOf(s)[0]
    expect(stepText(v).text).toBe("2 / 2")
    expect(tokensOf(v)?.n).toBe(55)
  })

  it("a seed never overwrites a fresher frame", () => {
    let s = applyRun(
      emptyLiveRows,
      run("r1", { status: "failed", last_seen: "2026-10-01T09:00:09Z" })
    )
    s = applySeed(s, [run("r1")])
    expect(rowsOf(s)[0].run.status).toBe("failed")
  })

  it("an ended row stays the grace, then goes — the peeked one stays", () => {
    let s = applyRun(emptyLiveRows, run("r1", { status: "succeeded" }), 1000)
    s = applyRun(s, run("r2", { status: "failed" }), 1000)
    s = applyRun(s, run("r3"), 1000)
    expect(prune(s, 1000 + LIVE_GRACE_MS)).toBe(s)
    const next = prune(s, 1001 + LIVE_GRACE_MS, "r2")
    expect(
      rowsOf(next)
        .map((v) => v.run.id)
        .sort()
    ).toEqual(["r2", "r3"])
  })

  it("a parked run counts as ended", () => {
    const s = applyRun(
      emptyLiveRows,
      run("r1", { status: "succeeded", pending: 1 }),
      5
    )
    expect(s.entries.r1.endedAt).toBe(5)
  })

  it("an unreadable mark waits for its row", () => {
    let s = applyBackfillError(emptyLiveRows, { run_id: "r1", kind: "event" })
    s = applyBackfillError(s, { run_id: "r1", kind: "event" })
    s = applyRun(s, run("r1"))
    expect(rowsOf(s)[0].unreadable).toEqual(["event"])
  })

  it("a running row without a reason gets A5.1's", () => {
    expect(costFieldsOf(run("r1")).cost_reason).toBe(RUNNING_COST_REASON)
    expect(
      costFieldsOf(
        run("r1", { status: "succeeded", cost_known: true, cost_usd: 1 })
      ).cost_usd
    ).toBe(1)
  })

  it("filters on agent and status words", () => {
    expect(matchesFilter(run("r1"), "ord run")).toBe(true)
    expect(matchesFilter(run("r1"), "failed")).toBe(false)
    expect(
      matchesFilter(run("r1", { pending: 1, status: "succeeded" }), "parked")
    ).toBe(true)
  })

  it("?peek= takes an id-shaped string only", () => {
    expect(validPeek("01JABCDEF_x-1")).toBe("01JABCDEF_x-1")
    expect(validPeek("<script>")).toBeUndefined()
    expect(validPeek("")).toBeUndefined()
    expect(validPeek(42)).toBeUndefined()
    expect(validPeek("x".repeat(200))).toBeUndefined()
  })
})

describe("a derived row folded away (I6-fix2)", () => {
  it("a removed frame drops the row; one for a row the page never had changes nothing", () => {
    const id = "otel-6c0c0ffee0000000000000000000c001-6c00000000000004"
    let s = applyRun(emptyLiveRows, run(id, { derived: true }), 1_000)
    expect(s.entries[id]).toBeTruthy()
    s = applyRun(s, run(id, { derived: true, removed: true }), 2_000)
    expect(s.entries[id]).toBeUndefined()
    expect(applyRun(s, run("otel-x", { removed: true }), 3_000)).toBe(s)
  })
})
