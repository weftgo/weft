// lib/stepdiff.ts over the E3.1 goldens (studio/testdata/api/diff*.golden.json):
// the rows, cells, marks, markers and words both surfaces draw.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"

import {
  CELL_WORDS,
  DIFF_COLUMNS,
  ROW_WORDS,
  cellHole,
  cellText,
  diffPath,
  markChip,
  markerWords,
  nWayView,
  sideView,
  stepDiffView,
  summaryWords,
  systemHidden,
} from "./stepdiff"
import type { DiffDoc, DiffRowDoc } from "./stepdiff"
import { HOLES, holeWords } from "./honesty"

const golden = (name: string) =>
  JSON.parse(
    readFileSync(
      resolve(process.cwd(), `../testdata/api/${name}.golden.json`),
      "utf8"
    )
  ) as DiffDoc

const SAME = Object.fromEntries(DIFF_COLUMNS.map((c) => [c, "same"]))

describe("stepDiffView over diff.golden.json (a tool result differs at step 3)", () => {
  const v = stepDiffView(golden("diff"))

  it("aligns the rows by ordinal and marks step 3 alone, on tool_results", () => {
    expect(v.rows.map((r) => r.step)).toEqual([0, 1, 2, 3, 4])
    expect(v.markers).toEqual([3])
    for (const r of v.rows) {
      if (r.step === 3)
        expect(r.cells).toEqual({ ...SAME, tool_results: "changed" })
      else expect(r.cells).toEqual(SAME)
    }
    expect(markerWords(v.rows[3])).toBe("changed at step 3 · tool results")
    expect(summaryWords(v)).toBe("1 of 5 steps changed · first at step 3")
    expect(v.unknown).toBe(0)
    expect(v.holes).toEqual([])
  })

  it("prints each side's values the same way on every surface", () => {
    const r = v.rows[3]
    expect(cellText(r.a.side, "tool_calls")).toBe(
      'lookup_order({"order_id":"3"})'
    )
    expect(cellText(r.a.side, "tool_results")).toBe("order 3 shipped")
    expect(cellText(r.b.side, "tool_results")).toBe("order 3 lost")
    expect(cellText(r.a.side, "system")).toBe("You are a support agent.")
    expect(cellText(r.a.side, "text")).toBe("(none)")
    expect(cellText(r.a.side, "usage")).toBe("10 in · 5 out")
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
      expect(r.a.holes.map((h) => h.hole)).toEqual([
        "gap",
        "not_recorded",
        "derived",
      ])
    }
    expect(CELL_WORDS.unknown).toBe("not comparable")
    expect(v.unknown).toBe(10)
    // "no step changed" says it covers only what both recorded
    expect(summaryWords(v)).toBe(
      "no step changed of 2 · 10 cells not comparable"
    )
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
    expect(markerWords(v.rows[4])).toBe(
      "changed at step 4 · only in the base run"
    )
    expect(summaryWords(v)).toBe(
      "1 of 5 steps changed (1 only in one run) · first at step 4"
    )
  })

  it("the response's truncated hole is the response_cap cause", () => {
    expect(stepDiffView(short).holes).toEqual([
      { hole: "truncated", reason: "r", fix: "f", cause: "response_cap" },
    ])
  })

  it("a mark the side's holes already carry is drawn once, as the hole", () => {
    const side = {
      ...base.steps[0].a!,
      marks: ["max_tokens", "compacted", "subagent"],
      holes: [{ hole: "max_tokens", reason: "cut" }],
    }
    const sv = sideView(side)
    expect(sv.holes.map((h) => h.hole)).toEqual(["max_tokens"])
    expect(sv.marks.map((m) => [m.mark, m.hole])).toEqual([
      ["compacted", "compacted"],
      ["subagent", undefined],
    ])
  })

  it("marks take the table's words where they are holes, their own else", () => {
    expect(markChip("compacted")).toMatchObject({
      label: HOLES.compacted.label,
      hole: "compacted",
    })
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
    expect(n.rows[3].others.map((o) => o.cells.tool_results)).toEqual([
      "changed",
      "same",
    ])
    expect(n.rows[4].others[1].cells.text).toBe("missing")
    expect(n.rows[4].base.side?.text).toBe("all looked up")
    // Not one base: no throw, an empty view with its one line.
    const bad = nWayView([
      base,
      { ...short, a: { ...short.a, run_id: "other" } },
    ])
    expect(bad.rows).toEqual([])
    expect(bad.error).toMatch(/one base run: r_da and other/)
    expect(nWayView([]).error).toMatch(/no run was compared/)
    expect(n.error).toBeUndefined()
  })

  it("the path is the route's", () => {
    expect(diffPath("r a", "r#b")).toBe("diff?a=r+a&b=r%23b")
  })
})

describe("a hidden children hole hides no system prompt (P8-pre-web #7)", () => {
  const kids = {
    hole: "hidden",
    reason:
      "2 child runs carry another public id, or none: a panel token reads its own public id's runs, so they are left out",
    count: 2,
  }
  it("a side whose child runs were left out keeps its system cell", () => {
    const doc = golden("diff")
    const side = { ...doc.steps[0].a!, holes: [kids] }
    expect(systemHidden(side)).toBe(false)
    expect(cellHole("system", side, doc.steps[0].b)).toBeUndefined()
    // Its prompt null for another reason: still not "hidden".
    expect(systemHidden({ ...side, system: null })).toBe(false)
  })
  it("the read token's side says system_hidden: it hides it", () => {
    const side = golden("diff-hidden").steps[0].a!
    expect(side.system_hidden).toBe(true)
    expect(systemHidden(side)).toBe(true)
    // The explicit fact, never the hole's count: a counted hidden hole
    // beside it still hides the prompt.
    expect(systemHidden({ ...side, holes: [kids] })).toBe(true)
  })
})

describe("diff-replay.golden.json (A8: a scripted replay of a two-step run from step 1)", () => {
  const doc = golden("diff-replay")
  const v = stepDiffView(doc)

  it("reads kept · same: step 0 the source's, not re-run; step 1 the same, usage not counted", () => {
    expect(doc.aligned).toEqual({
      replay: "b",
      source: "r_src",
      a_from: 0,
      b_from: 1,
    })
    expect(v.rows.map((r) => r.state)).toEqual(["kept", "same"])
    expect(v.rows.map((r) => ROW_WORDS[r.state])).toEqual(["kept", "same"])
    expect(v.rows[0].cells).toEqual(
      Object.fromEntries(DIFF_COLUMNS.map((c) => [c, "kept"]))
    )
    expect(v.rows[1].cells).toEqual({ ...SAME, usage: "differs" })
    expect(v.markers).toEqual([])
    // each side's own ordinal: the replay's step 0 is the source's step 1
    expect(v.rows.map((r) => [r.aStep, r.bStep])).toEqual([
      [0, null],
      [1, 0],
    ])
    expect(cellText(v.rows[1].b.side, "usage")).toBe("0 in · 0 out")
    expect(summaryWords(v)).toBe(
      "no step changed of 1 · 1 kept (not re-run) · usage differs (0 tokens)"
    )
  })

  it("N-way keeps each compared run's own ordinal beside the aligned one", () => {
    const n = nWayView([doc])
    expect(n.aligned).toBe(true)
    expect(n.rows.map((r) => [r.step, r.baseStep, r.others[0].step])).toEqual([
      [0, 0, null],
      [1, 1, 0],
    ])
    expect(n.rows.map((r) => r.others[0].state)).toEqual(["kept", "same"])
  })

  it("a Studio before A8 (no a_step, b_step, kept, differs) reads as before", () => {
    const old = golden("diff")
    for (const r of old.steps) {
      delete r.a_step
      delete r.b_step
      delete r.kept
      delete r.differs
    }
    const w = stepDiffView(old)
    expect(w.rows.map((r) => r.state)).toEqual([
      "same",
      "same",
      "same",
      "changed",
      "same",
    ])
    expect(w.rows[3].bStep).toBe(3)
    expect(nWayView([old]).aligned).toBe(false)
  })
})

describe("nWayView: a compared run absent at an ordinal (U19-fix 5)", () => {
  it("reads missing, from its cells — never same", () => {
    const one = golden("diff")
    const short: DiffDoc = {
      ...one,
      b: { ...one.b, run_id: `${one.b.run_id}-short` },
      steps: one.steps.slice(0, 3),
    }
    const n = nWayView([one, short])
    const last = n.rows.find((r) => r.step === 4)!
    expect(last.others[1].state).toBe("missing")
    expect(ROW_WORDS[last.others[1].state]).toBe("missing")
    expect(last.others[0].state).toBe("same")
  })
})

describe("a sibling row (kept_by)", () => {
  it("is compared, never kept; an unread source is not compared", () => {
    const doc = golden("diff-siblings")
    const v = stepDiffView(doc)
    expect(v.kept).toBe(0)
    expect(v.markers).toEqual([1])
    expect(v.rows[0].note).toBe(
      "b kept the source's step; a vs source: changed"
    )
    const unread = {
      ...doc,
      steps: doc.steps.map((r: DiffRowDoc, i: number) =>
        i === 0
          ? {
              ...r,
              b: null,
              changed: false,
              changes: [],
              unknown: [
                "system",
                "tool_calls",
                "tool_results",
                "text",
                "usage",
              ],
            }
          : r
      ),
    }
    const u = stepDiffView(unread)
    expect(u.rows[0].cells.text).toBe("unknown")
    expect(u.rows[0].note).toContain("not compared")
    expect(u.kept).toBe(0)
  })
})
