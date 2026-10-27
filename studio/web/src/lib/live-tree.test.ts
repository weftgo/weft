import { describe, expect, it } from "vitest"

import type { RunRow } from "./api"
import type { LiveRowView } from "./live-rows"
import { LIVE_TREE, foldLive, liveWindow, miniBar } from "./live-tree"

const T0 = Date.parse("2026-10-01T09:00:00.000Z")
const at = (ms: number) => new Date(T0 + ms).toISOString()
function view(
  id: string,
  depth: number,
  over: Partial<RunRow> = {}
): LiveRowView {
  return {
    run: {
      id,
      status: "succeeded",
      started: at(0),
      finished: at(1000),
      ...over,
    } as RunRow,
    stats: null,
    unreadable: [],
    endedAt: null,
    depth,
  }
}

describe("foldLive (U30.2, UX-A-18)", () => {
  it("opens a parent while a child runs, folds it once every child ended; a toggle wins", () => {
    const rows = [
      view("dad", 0),
      view("kid", 1, { status: "running", finished: null }),
      view("grandkid", 2, { status: "failed" }),
      view("other", 0),
    ]
    const live = foldLive(rows, {})
    // The grandkid ended: the kid's fold is shut, the dad's open.
    expect(live.rows.map((v) => v.run.id)).toEqual(["dad", "kid", "other"])
    expect(live.folds.dad).toEqual({ n: 2, running: 1, failed: 1, open: true })
    expect(live.folds.kid).toEqual({ n: 1, running: 0, failed: 1, open: false })
    expect(live.folds.other).toBeUndefined()
    expect(foldLive(rows, { kid: true }).rows.map((v) => v.run.id)).toEqual([
      "dad",
      "kid",
      "grandkid",
      "other",
    ])
    expect(foldLive(rows, { dad: false }).rows.map((v) => v.run.id)).toEqual([
      "dad",
      "other",
    ])
    const done = rows.map((v) => ({
      ...v,
      run: { ...v.run, status: "succeeded" as const, finished: at(1000) },
    }))
    expect(foldLive(done, {}).rows.map((v) => v.run.id)).toEqual([
      "dad",
      "other",
    ])
    expect(LIVE_TREE.more(2)).toBe("+2 subagents")
  })
})

describe("miniBar (U30.2, UX-A-18)", () => {
  it("places each run on one axis: earliest start to latest end, now for a running run", () => {
    const rows = [
      view("dad", 0, { started: at(0), finished: at(4000) }),
      view("kid", 1, { started: at(1000), finished: at(2000) }),
      view("live", 0, { started: at(3000), status: "running", finished: null }),
    ]
    const now = T0 + 5000
    const axis = liveWindow(rows, now)
    expect(axis).toEqual([T0, T0 + 5000])
    expect(miniBar(rows[0], axis, now)).toEqual({ left: 0, width: 80 })
    expect(miniBar(rows[1], axis, now)).toEqual({ left: 20, width: 20 })
    expect(miniBar(rows[2], axis, now)).toEqual({ left: 60, width: 40 })
    expect(
      miniBar(view("x", 0, { started: "nope" }), axis, now)
    ).toBeUndefined()
  })
})
