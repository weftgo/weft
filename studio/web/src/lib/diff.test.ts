// The bounded diff the Request pane draws (plan E1.1 review): the
// common ends trimmed before the LCS, the middle capped.
import { describe, expect, it } from "vitest"

import { diffLines, diffLinesBounded } from "./diff"

describe("diffLinesBounded", () => {
  it("is diffLines' answer with the common ends trimmed", () => {
    const a = "keep 1\nkeep 2\nold\nkeep 3"
    const b = "keep 1\nkeep 2\nnew\nkeep 3"
    expect(diffLinesBounded(a, b)).toEqual({ rows: diffLines(a, b) })
    expect(diffLinesBounded("x\ny", "x")).toEqual({
      rows: [
        { kind: "same", text: "x" },
        { kind: "del", text: "y" },
      ],
    })
    expect(diffLinesBounded("x", "x\ny")).toEqual({
      rows: [
        { kind: "same", text: "x" },
        { kind: "add", text: "y" },
      ],
    })
  })

  it("a long prompt changed in one line diffs fast: only the middle meets the LCS", () => {
    const lines = Array.from({ length: 20_000 }, (_, i) => `line ${i}`)
    const b = [...lines]
    b[10_000] = "changed"
    const t0 = performance.now()
    const out = diffLinesBounded(lines.join("\n"), b.join("\n"))
    expect(performance.now() - t0).toBeLessThan(500)
    if (!("rows" in out)) throw new Error("expected rows")
    expect(out.rows.filter((r) => r.kind !== "same")).toEqual([
      { kind: "del", text: "line 10000" },
      { kind: "add", text: "changed" },
    ])
  })

  it("past the cap: the sizes, no table", () => {
    const big = (t: string) => Array.from({ length: 5000 }, (_, i) => `${t}${i}`).join("\n")
    const t0 = performance.now()
    expect(diffLinesBounded(big("a"), big("b"))).toEqual({ tooLarge: { before: 5000, after: 5000 } })
    expect(performance.now() - t0).toBeLessThan(500)
  })
})
