// H9 on the Live page's fold: the attempt frames (O17) — parsed,
// deduped, the newest kept, worded for the step cell — the tree order
// a subagent is drawn in, and the minute a run that ended stays,
// a reload included (applyRecent).
import { describe, expect, it } from "vitest"

import type { RunRow } from "./api"
import type { LiveAttempt } from "./live-attempts"
import { attemptFrames, attemptNote, newerAttempt } from "./live-attempts"
import {
  LIVE_GRACE_MS,
  applyAttempt,
  applyRecent,
  applyRun,
  emptyLiveRows,
  prune,
  rowsOf,
} from "./live-rows"

const run = (id: string, over: Partial<RunRow> = {}): RunRow =>
  ({
    id,
    parent_run_id: "",
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
const att = (over: Partial<LiveAttempt> = {}): LiveAttempt => ({
  seq: 1,
  run_id: "r1",
  session_id: "",
  public_id: "",
  step: 0,
  attempt: 1,
  status: "error",
  error_type: "rate_limited",
  start: "",
  end: "",
  ...over,
})

describe("attempt frames", () => {
  it("are parsed and deduped on (run, step, attempt); a malformed one is dropped", () => {
    const got: LiveAttempt[] = []
    const { attempt } = attemptFrames((a) => got.push(a))
    const frame = JSON.stringify({ ...att(), seq: undefined })
    attempt(frame, 7)
    attempt(frame, 8) // ingest and the database both published it
    attempt("{not json", 9)
    attempt(JSON.stringify({ run_id: "r1" }), 10)
    attempt(JSON.stringify({ ...att({ attempt: 2, status: "ok" }) }), 11)
    expect(got.map((a) => [a.attempt, a.status, a.seq])).toEqual([
      [1, "error", 7],
      [2, "ok", 11],
    ])
  })

  it("keep each run's newest attempt", () => {
    let s = applyRun(emptyLiveRows, run("r1"))
    s = applyAttempt(s, att({ step: 1, attempt: 1 }))
    s = applyAttempt(s, att({ step: 0, attempt: 3 })) // an older step
    expect(rowsOf(s)[0].attempt).toMatchObject({ step: 1, attempt: 1 })
    expect(newerAttempt(att({ attempt: 1 }), att({ attempt: 2 })).attempt).toBe(
      2
    )
  })

  it("word a failed try as retrying while the run runs, past tense once it ended", () => {
    const n = attemptNote(att({ wait_ms: 4000 }), true)!
    expect(n.text).toBe("↻ attempt 1 failed")
    expect(n.retrying).toBe(true)
    expect(n.title).toContain("step 1 attempt 1 failed (rate_limited)")
    expect(n.title).toContain("the provider asked to wait 4.0s")
    expect(attemptNote(att(), false)!.retrying).toBe(false)
    expect(
      attemptNote(
        att({ attempt: 2, status: "ok", error_type: undefined }),
        true
      )!.text
    ).toBe("ok on attempt 2")
    expect(
      attemptNote(att({ attempt: 2, status: "ok", layer: "fallback" }), true)!
        .text
    ).toBe("ok on attempt 2 (fallback)")
    // A first try that answered says nothing.
    expect(attemptNote(att({ status: "ok" }), true)).toBeNull()
    // P8-fix-b #13: an earlier step's attempt says nothing on a later
    // step — "ok on attempt 2" from step 1 is gone on step 6.
    const ok2 = att({
      step: 0,
      attempt: 2,
      status: "ok",
      error_type: undefined,
    })
    expect(attemptNote(ok2, true, 0)?.text).toBe("ok on attempt 2")
    expect(attemptNote(ok2, true, 5)).toBeNull()
    expect(attemptNote(att({ step: 0 }), true, 5)).toBeNull()
    expect(attemptNote(ok2, true, null)?.text).toBe("ok on attempt 2")
    expect(attemptNote(undefined, true)).toBeNull()
  })
})

describe("the Live rows' tree and their minute", () => {
  it("draws a subagent right under its parent, one level deeper", () => {
    let s = applyRun(
      emptyLiveRows,
      run("d", { started: "2026-10-01T09:00:00Z" })
    )
    s = applyRun(s, run("other", { started: "2026-10-01T09:00:01Z" }))
    s = applyRun(
      s,
      run("d/0/c1", { parent_run_id: "d", started: "2026-10-01T09:00:02Z" })
    )
    expect(rowsOf(s).map((v) => [v.run.id, v.depth])).toEqual([
      ["other", 0],
      ["d", 0],
      ["d/0/c1", 1],
    ])
  })

  it("keeps a run that ended for a minute", () => {
    const s = applyRun(emptyLiveRows, run("r1", { status: "succeeded" }), 1000)
    expect(LIVE_GRACE_MS).toBe(60_000)
    expect(prune(s, 1000 + 59_000)).toBe(s)
    expect(rowsOf(prune(s, 1001 + LIVE_GRACE_MS))).toHaveLength(0)
  })

  it("lists on load the runs that ended in the last minute, their minute counted from their end", () => {
    const now = Date.parse("2026-10-01T09:10:00Z")
    const s = applyRecent(
      emptyLiveRows,
      [
        run("fresh", { status: "succeeded", finished: "2026-10-01T09:09:30Z" }),
        run("old", { status: "failed", finished: "2026-10-01T09:05:00Z" }),
        run("still", { status: "running" }), // the running seed's
      ],
      now
    )
    expect(rowsOf(s).map((v) => v.run.id)).toEqual(["fresh"])
    expect(rowsOf(s)[0].endedAt).toBe(Date.parse("2026-10-01T09:09:30Z"))
    expect(rowsOf(prune(s, now + 31_000))).toHaveLength(0)
  })
})
