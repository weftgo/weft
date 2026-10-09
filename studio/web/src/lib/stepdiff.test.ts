// lib/stepdiff.ts over the E3.1 goldens (studio/testdata/api/diff*.golden.json):
// the rows, cells, marks, markers and words both surfaces draw.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"

import {
  CELL_WORDS,
  DIFF_COLUMNS,
  cellHole,
  cellText,
  diffPath,
  markChip,
  markerWords,
  nWayView,
  sideView,
  stepDiffView,
  summaryWords,
} from "./stepdiff"
import type { DiffDoc, DiffRowDoc } from "./stepdiff"
import { HOLES, holeWords } from "./honesty"

const golden = (name: string) =>
  JSON.parse(readFileSync(resolve(process.cwd(), `../testdata/api/${name}.golden.json`), "utf8")) as DiffDoc

const SAME = Object.fromEntries(DIFF_COLUMNS.map((c) => [c, "same"]))

describe("stepDiffView over diff.golden.json (a tool result differs at step 3)", () => {
  const v = stepDiffView(golden("diff"))

  it("aligns the rows by ordinal and marks step 3 alone, on tool_results", () => {
    expect(v.rows.map((r) => r.step)).toEqual([0, 1, 2, 3, 4])
    expect(v.markers).toEqual([3])
    for (const r of v.rows) {
      if (r.step === 3) expect(r.cells).toEqual({ ...SAME, tool_results: "changed" })
      else expect(r.cells).toEqual(SAME)
    }
    expect(markerWords(v.rows[3])).toBe("changed at step 3 · tool results")
    expect(summaryWords(v)).toBe("1 of 5 steps changed · first at step 3")
    expect(v.unknown).toBe(0)
    expect(v.holes).toEqual([])
  })

  it("prints each side's values the same way on every surface", () => {
    const r = v.rows[3]
    expect(cellText(r.a.side, "tool_calls")).toBe('lookup_order({"order_id":"3"})')
    expect(cellText(r.a.side, "tool_results")).toBe("order 3 shipped")
    expect(cellText(r.b.side, "tool_results")).toBe("order 3 lost")
    expect(cellText(r.a.side, "system")).toBe("You are a support agent.")
    expect(cellText(r.a.side, "text")).toBe("(none)")
    expect(cellText(r.a.side, "usage")).toBe("10→5")
    expect(cellText(v.rows[4].b.side, "text")).toBe("all looked up")
    expect(cellText(null, "text")).toBe("—")
  })
})

describe("the hidden and not-recorded goldens", () => {
  it("a read-scoped token's diff: the system prompt hidden (a badge per side), the change still found", () => {
    const v = stepDiffView(golden("diff-hidden"))
    expect(v.markers).toEqual([3])
    for (const r of v.rows) {
      expect(r.a.holes.map((h) => h.hole)).toEqual(["hidden"])
      expect(r.b.holes.map((h) => h.hole)).toEqual(["hidden"])
      // system compared by hash: the same, though its words are hidden
      expect(r.cells.system).toBe("same")
      // the words withheld: the table's hidden label, never the hash as if read
      expect(cellText(r.a.side, "system")).toBe(HOLES.hidden.label)
      expect(cellHole("system", r.a.side, r.b.side)).toBe("hidden")
      expect(cellHole("text", r.a.side, r.b.side)).toBeUndefined()
    }
    expect(holeWords(v.rows[0].a.holes[0]).label).toBe(HOLES.hidden.label)
  })

  it("a pre-record run's diff: every column not comparable — never same — and no marker", () => {
    const v = stepDiffView(golden("diff-not-recorded"))
    expect(v.markers).toEqual([])
    for (const r of v.rows) {
      for (const c of DIFF_COLUMNS) expect(r.cells[c]).toBe("unknown")
      expect(r.a.holes.map((h) => h.hole)).toEqual(["gap", "not_recorded", "derived"])
    }
    expect(CELL_WORDS.unknown).toBe("not comparable")
    expect(v.unknown).toBe(10)
    // "no step changed" says it covers only what both recorded
    expect(summaryWords(v)).toBe("no step changed of 2 · 10 cells not comparable")
  })
})

describe("missing steps, marks and the response's holes", () => {
  const base = golden("diff")
  const short: DiffDoc = {
    ...base,
    b: { ...base.b, run_id: "r_dc", steps: 4 },
    steps: base.steps.map((s): DiffRowDoc =>
      s.step === 4
        ? { ...s, changed: true, b: null, changes: ["missing"], unknown: [] }
        : s.step === 3
          ? { ...s, changed: false, b: s.a, changes: [] }
          : s
    ),
    summary: { changed_steps: [4], first_changed: 4 },
    holes: [{ hole: "truncated", reason: "r", fix: "f" }],
  }

  it("a step one run lacks is missing in every column, said by its marker", () => {
    const v = stepDiffView(short)
    expect(v.markers).toEqual([4])
    for (const c of DIFF_COLUMNS) expect(v.rows[4].cells[c]).toBe("missing")
    expect(markerWords(v.rows[4])).toBe("changed at step 4 · only in the base run")
    expect(summaryWords(v)).toBe("1 of 5 steps changed (1 only in one run) · first at step 4")
  })

  it("the response's truncated hole is the response_cap cause", () => {
    expect(stepDiffView(short).holes).toEqual([{ hole: "truncated", reason: "r", fix: "f", cause: "response_cap" }])
  })

  it("a mark the side's holes already carry is drawn once, as the hole", () => {
    const side = { ...base.steps[0].a!, marks: ["max_tokens", "compacted", "subagent"], holes: [{ hole: "max_tokens", reason: "cut" }] }
    const sv = sideView(side)
    expect(sv.holes.map((h) => h.hole)).toEqual(["max_tokens"])
    expect(sv.marks.map((m) => [m.mark, m.hole])).toEqual([["compacted", "compacted"], ["subagent", undefined]])
  })

  it("marks take the table's words where they are holes, their own else", () => {
    expect(markChip("compacted")).toMatchObject({ label: HOLES.compacted.label, hole: "compacted" })
    expect(markChip("max_tokens").hole).toBe("max_tokens")
    expect(markChip("interrupted").hole).toBe("interrupted")
    for (const m of ["subagent", "parked", "running", "error"]) {
      expect(markChip(m).hole).toBeUndefined()
      expect(markChip(m).label).toBe(m)
      expect(markChip(m).title).not.toBe(m)
    }
  })

  it("N-way: N−1 responses against one base, side by side", () => {
    const n = nWayView([base, short])
    expect(n.base.run_id).toBe("r_da")
    expect(n.others.map((o) => o.run_id)).toEqual(["r_db", "r_dc"])
    expect(n.markers).toEqual([[3], [4]])
    expect(n.rows.filter((r) => r.changed).map((r) => r.step)).toEqual([3, 4])
    expect(n.rows[3].others.map((o) => o.cells.tool_results)).toEqual(["changed", "same"])
    expect(n.rows[4].others[1].cells.text).toBe("missing")
    expect(n.rows[4].base.side?.text).toBe("all looked up")
    // Not one base: no throw, an empty view with its one line.
    const bad = nWayView([base, { ...short, a: { ...short.a, run_id: "other" } }])
    expect(bad.rows).toEqual([])
    expect(bad.error).toMatch(/one base run: r_da and other/)
    expect(nWayView([]).error).toMatch(/no run was compared/)
    expect(n.error).toBeUndefined()
  })

  it("the path is the route's", () => {
    expect(diffPath("r a", "r#b")).toBe("diff?a=r+a&b=r%23b")
  })
})
