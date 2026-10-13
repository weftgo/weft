// statusOf over every row shape (U1-fix): the stored four, the read-time
// cancelled and parked, the decided park, and the certainty bit —
// "awaiting a decision" only where the resume was looked for (a session
// turn read against its session's complete turn list).
import { describe, expect, it } from "vitest"

import { FILTER_KEYS, STATUS, statusOf } from "./status"
import type { StatusRow } from "./status"

const turn = (n: number, o: Partial<StatusRow> = {}): StatusRow => ({
  id: `s_01-t${n}`,
  status: "succeeded",
  session_id: "s_01",
  turn: n,
  ...o,
})

describe("statusOf (U1-fix)", () => {
  it("the stored four read as themselves", () => {
    for (const s of ["running", "succeeded", "failed", "interrupted"]) {
      const st = statusOf({ id: "r", status: s })
      expect(st.key).toBe(s)
      expect(st.word).toBe(s)
      expect(st.role).toBe(STATUS[s as keyof typeof STATUS].role)
    }
  })

  it("an unknown status is worded as itself, in the neutral role", () => {
    expect(statusOf({ id: "r", status: "exploded" })).toMatchObject({
      key: "exploded",
      word: "exploded",
      hint: "exploded",
      role: "resumed",
    })
  })

  it("failed + cancelled is cancelled, its cause in the hint; pending never makes it parked", () => {
    const st = statusOf({
      id: "r",
      status: "failed",
      error_type: "cancelled",
      cancel_cause: "studio.cancel",
      pending: 2,
    })
    expect(st.key).toBe("cancelled")
    expect(st.hint).toContain("cancelled · studio.cancel")
    // failed with pending calls (a run that failed after parking) and
    // no cancel: failed, never parked.
    expect(statusOf({ id: "r", status: "failed", pending: 1 }).key).toBe(
      "failed"
    )
  })

  it("interrupted and running with pending calls keep their words", () => {
    expect(statusOf({ id: "r", status: "interrupted", pending: 1 }).key).toBe(
      "interrupted"
    )
    expect(statusOf({ id: "r", status: "running", pending: 1 }).key).toBe(
      "running"
    )
  })

  it("awaiting a decision only with the session's complete turn list", () => {
    const t1 = turn(1, { pending: 1 })
    expect(statusOf(t1, [t1], true)).toMatchObject({
      key: "parked",
      word: "parked",
      detail: "1 call awaiting a decision",
    })
    // runs omitted: no claim.
    expect(statusOf(t1).detail).toBe("1 call pending · no resume recorded here")
    // runs given but not the complete list (a filtered or sorted runs
    // page): no claim either.
    expect(statusOf(turn(1, { pending: 2 }), [t1]).detail).toBe(
      "2 calls pending · no resume recorded here"
    )
    expect(statusOf(t1, null, true).key).toBe("parked")
  })

  it("a plain Generate (no session) never reads awaiting, complete or not", () => {
    const g = { id: "r_gen", status: "succeeded", pending: 1 }
    for (const st of [statusOf(g), statusOf(g, [g], true)]) {
      expect(st.key).toBe("parked")
      expect(st.detail).toBe(
        "1 call pending · a resume, if any, is not linked yet"
      )
      expect(st.hint).toBe(st.detail)
    }
  })

  it("a child or nested Generate that inherited the session is no turn: no claim", () => {
    const child = {
      ...turn(1, { pending: 1 }),
      id: "s_01-t1/0/call_1",
      parent_run_id: "s_01-t1",
    }
    expect(statusOf(child, [child], true).detail).toBe(
      "1 call pending · no resume recorded here"
    )
  })

  it("a turn-less row is never resumed by a later turn it cannot be ordered against", () => {
    const row = {
      id: "r_x",
      status: "succeeded",
      pending: 1,
      session_id: "s_01",
    }
    const st = statusOf(row, [row, turn(2)], true)
    expect(st.key).toBe("parked")
    expect(st.detail).toBe("1 call pending · no resume recorded here")
  })

  it("a later turn of the session is the resume (any list holding it)", () => {
    const t1 = turn(1, { pending: 1 })
    const t2 = turn(2)
    for (const complete of [true, false])
      expect(statusOf(t1, [t1, t2], complete)).toMatchObject({
        key: "resumed",
        word: "parked → t2",
        detail: "1 call decided · resumed in t2",
        resumedBy: "s_01-t2",
      })
    // Another session's later turn is not.
    const other = { ...turn(5), id: "s_02-t5", session_id: "s_02" }
    expect(statusOf(t1, [t1, other], true).key).toBe("parked")
  })

  it("resumed_by (U13.1's stored link) is read before any later turn", () => {
    const g = {
      id: "r_gen",
      status: "succeeded",
      pending: 1,
      resumed_by: "r_resume",
    }
    expect(statusOf(g)).toMatchObject({
      key: "resumed",
      word: "parked → r_resume",
      resumedBy: "r_resume",
    })
    // In a list that loaded it, its turn label.
    const t1 = turn(1, { pending: 1, resumed_by: "s_01-t3" })
    expect(statusOf(t1, [t1, turn(2), turn(3)]).word).toBe("parked → t3")
  })

  it("a long resumer id with no turn to name is cut to 8 characters in the word and detail, whole in the hint", () => {
    const id = "3f9a1c2b7d4e5f60718293a4b5c6d7e8"
    const s = statusOf({
      id: "r_gen",
      status: "succeeded",
      pending: 2,
      resumed_by: id,
    })
    expect(s).toMatchObject({
      key: "resumed",
      word: "parked → 3f9a1c2b…",
      detail: "2 calls decided · resumed in 3f9a1c2b…",
      hint: `2 calls decided · resumed in 3f9a1c2b… (${id})`,
      resumedBy: id,
    })
  })

  it("the panel's filter picks name every key a turn can read, the decided park included", () => {
    expect(FILTER_KEYS).toEqual([
      "running",
      "succeeded",
      "failed",
      "interrupted",
      "parked",
      "resumed",
      "cancelled",
    ])
  })
})
