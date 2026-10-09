// The honesty table against Go's (plan A3, ADR 0028 §11): the ten
// badges, in order, with obsdb.HoleNote's reason and fix word for word.
// studio/testdata/holes.golden.json is written by obsdb's
// TestHoleNotesGolden — one file both trees are checked against, so the
// two tables cannot drift.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"

import {
  CAUSES,
  HOLE_ORDER,
  HOLES,
  contentHoles,
  holeWords,
  isHole,
  statusHoles,
  kib,
  mergeHoles,
} from "./honesty"

interface GoHole {
  hole: string
  reason: string
  fix?: string
  causes?: Record<string, { reason: string; fix?: string }>
}

const golden = JSON.parse(
  readFileSync(resolve(process.cwd(), "../testdata/holes.golden.json"), "utf8")
) as GoHole[]

describe("the honesty table", () => {
  it("is Go's closed table: the same ten holes, in order", () => {
    expect(golden.map((g) => g.hole)).toEqual(HOLE_ORDER)
    expect(HOLE_ORDER).toEqual([
      "truncated",
      "stripped",
      "redacted",
      "max_tokens",
      "interrupted",
      "gap",
      "not_recorded",
      "derived",
      "hidden",
      "compacted",
    ])
  })

  it("words every hole as obsdb.HoleNote does, key by key", () => {
    for (const g of golden) {
      expect(isHole(g.hole)).toBe(true)
      if (!isHole(g.hole)) continue
      const note = HOLES[g.hole]
      expect(note.reason, g.hole).toBe(g.reason)
      expect(note.fix, g.hole).toBe(g.fix)
      expect(note.label, g.hole).toBeTruthy()
      expect(["loss", "note"]).toContain(note.tone)
    }
  })

  it("words every cause as obsdb.HoleNoteFor does, and a mark's cause wins over the badge's words (D5)", () => {
    for (const g of golden) {
      const mine = isHole(g.hole) ? (CAUSES[g.hole] ?? {}) : {}
      expect(Object.keys(mine).sort(), g.hole).toEqual(Object.keys(g.causes ?? {}).sort())
      for (const [cause, note] of Object.entries(g.causes ?? {})) {
        expect(mine[cause], `${g.hole}/${cause}`).toEqual(note)
        const w = holeWords({ hole: g.hole, cause })
        expect(w.reason).toBe(note.reason)
        expect(w.fix).toBe(note.fix)
      }
    }
    // A result cap's cut is the badge, not the recorder's words.
    expect(holeWords({ hole: "truncated", cause: "result_cap", bytes: 12 }).label).toBe("truncated")
    // An unknown cause reads the badge's own words.
    expect(holeWords({ hole: "gap", cause: "nope" }).reason).toBe(HOLES.gap.reason)
  })

  it("reads an event's attrs: the recorder's cut and the content-off mark", () => {
    expect(contentHoles(undefined)).toEqual([])
    expect(contentHoles({ "weft.content": "full" })).toEqual([])
    expect(contentHoles({ "weft.content": "stripped" })).toEqual([
      { hole: "stripped" },
    ])
    // The core's own capture-off mark reads the same.
    expect(contentHoles({ "weft.content": "none" })).toEqual([
      { hole: "stripped" },
    ])
    expect(
      contentHoles({ "weft.content.truncated_bytes": 12595 })
    ).toEqual([{ hole: "truncated", bytes: 12595 }])
  })

  it("says what the recorder cut, in bytes", () => {
    expect(kib(92)).toBe("92 B")
    expect(kib(12595)).toBe("12.3 KiB")
    const w = holeWords({ hole: "truncated", bytes: 12595 })
    expect(w.label).toBe("shortened by the recorder: 12.3 KiB cut")
    expect(w.reason).toBe(HOLES.truncated.reason)
    expect(w.fix).toBe(HOLES.truncated.fix)
    expect(holeWords({ hole: "stripped" }).label).toBe(
      "content not captured by this app"
    )
    // A response's words win; an unknown badge renders verbatim.
    expect(holeWords({ hole: "gap", reason: "mine" }).reason).toBe("mine")
    expect(holeWords({ hole: "brand_new" }).label).toBe("brand_new")
  })

  it("merges lists: one badge per hole, bytes summed, the table's order", () => {
    const merged = mergeHoles(
      [{ hole: "not_recorded" }, { hole: "truncated", bytes: 10 }],
      [{ hole: "truncated", bytes: 5 }, { hole: "stripped" }]
    )
    expect(merged).toEqual([
      { hole: "truncated", bytes: 15 },
      { hole: "stripped" },
      { hole: "not_recorded" },
    ])
  })
})

// One rule on both surfaces (the run page's header, the panel's turn).
describe("statusHoles", () => {
  it("interrupted, max_tokens from the stop reason, a gap only once the run is over", () => {
    expect(statusHoles({ status: "running", gaps: [4] })).toEqual([])
    expect(statusHoles({ gaps: [4] })).toEqual([])
    expect(statusHoles({ status: "failed", gaps: [4, 5] })).toEqual([
      { hole: "gap", reason: "2 events missing (positions 4, 5): a destination dropped a batch" },
    ])
    expect(statusHoles({ status: "succeeded", stop_reason: "max_tokens" })).toEqual([
      { hole: "max_tokens" },
    ])
    expect(statusHoles({ status: "interrupted" })).toEqual([{ hole: "interrupted" }])
  })
})
