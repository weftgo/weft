import { describe, expect, it } from "vitest"

import {
  CHILDREN_COUNT_UNKNOWN,
  childrenChip,
  moreChildren,
  nest,
} from "./delegation"

const r = (id: string, parent_run_id = "") => ({ id, parent_run_id })

describe("childrenChip", () => {
  it("is null for a run that started none, or an older Studio's row", () => {
    expect(childrenChip({})).toBeNull()
    expect(childrenChip({ children_count: 0 })).toBeNull()
  })
  it("counts the children and names the failed ones", () => {
    expect(childrenChip({ children_count: 1 })).toEqual({
      text: "⑂ 1",
      failed: "",
      title: "1 child run (its subagents and any run it started)",
    })
    const c = childrenChip({ children_count: 3, children_failed: 2 })
    expect(c?.text).toBe("⑂ 3")
    expect(c?.failed).toBe("2 failed")
    expect(c?.title).toContain("3 child runs")
    expect(c?.title).toContain("2 failed")
  })
})

describe("the children read's edges (P8-fix-b)", () => {
  it("an unknown count is ⑂ ?", () => {
    expect(childrenChip({ children_unknown: true })).toEqual({
      text: "⑂ ?",
      failed: "",
      title: CHILDREN_COUNT_UNKNOWN,
    })
  })
  it("+N more past the page read; + more when only the server's cursor says so", () => {
    expect(moreChildren(100, 250, true)).toBe("+150 more · open the list")
    expect(moreChildren(100, 100, false)).toBeUndefined()
    expect(moreChildren(100, 90, true)).toBe("+ more · open the list")
    expect(moreChildren(3, undefined, false)).toBeUndefined()
  })
})

describe("nest", () => {
  it("puts a child under its parent, the roots in their own order", () => {
    // The list's order: newest first — a child starts after its parent
    // and so is listed above it.
    const rows = [r("d/0/c1", "d"), r("x"), r("d"), r("d/0/c1/0/c2", "d/0/c1")]
    expect(nest(rows).map((n) => [n.row.id, n.depth])).toEqual([
      ["x", 0],
      ["d", 0],
      ["d/0/c1", 1],
      ["d/0/c1/0/c2", 2],
    ])
  })
  it("keeps a child whose parent is not listed as a root", () => {
    expect(nest([r("k", "gone"), r("a")]).map((n) => n.depth)).toEqual([0, 0])
    expect(nest([r("k", "gone"), r("a")]).map((n) => n.row.id)).toEqual([
      "k",
      "a",
    ])
  })
  it("places every row once, a cycle included", () => {
    const out = nest([r("a", "b"), r("b", "a"), r("c")])
    expect(out.map((n) => n.row.id).sort()).toEqual(["a", "b", "c"])
  })
})
