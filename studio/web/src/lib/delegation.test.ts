import { describe, expect, it } from "vitest"

import {
  CHILDREN_COUNT_UNKNOWN,
  childKind,
  delegatedHoles,
  linkedHoles,
  lineageOfRun,
  lineageOfSession,
  storyChildren,
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

describe("childKind (U14, UX-B-06)", () => {
  it("tells a subagent run, a delegated session's turn and a run a call started apart", () => {
    expect(childKind({ id: "p/0/c1", parent_run_id: "p" })).toBe("subagent")
    expect(
      childKind({
        id: "s_c-t1",
        session_id: "s_c",
        caller_run_id: "s_p-t1",
        meta: { "weft.session.parent": "s_p" },
      } as never)
    ).toBe("delegated")
    // Recorded before weft 0.18: the pool child named its caller as parent.
    expect(
      childKind({
        id: "s_c-t1",
        session_id: "s_c",
        parent_run_id: "s_p-t1",
        meta: { "weft.session.parent": "s_p" },
      })
    ).toBe("delegated")
    // A Generate under a pool child's turn inherits weft.session.parent:
    // not its own session's turn.
    expect(
      childKind({
        id: "r_9",
        session_id: "s_c",
        meta: { "weft.session.parent": "s_p" },
      })
    ).toBe("started")
    expect(childKind({ id: "r_9" })).toBe("started")
  })
})

describe("lineage (U14, UX-B-05)", () => {
  it("reads a run's lineage from its meta and caller link", () => {
    expect(lineageOfRun({ meta: {} })).toBeNull()
    expect(
      lineageOfRun({
        caller_run_id: "s_p-t3",
        meta: {
          "weft.session.parent": "s_p",
          "weft.session.parent_call": "d1",
        },
      })
    ).toEqual({ session: "s_p", turn: 3, call: "d1", run: "s_p-t3" })
    expect(
      lineageOfRun({
        parent_run_id: "s_p-t1",
        meta: { "weft.session.parent": "s_p" },
      })
    ).toEqual({ session: "s_p", turn: 1, call: undefined, run: "s_p-t1" })
    // p.Submit outside a run: no caller, no turn.
    expect(lineageOfRun({ meta: { "weft.session.parent": "s_p" } })).toEqual({
      session: "s_p",
      turn: undefined,
      call: undefined,
      run: undefined,
    })
  })
  it("reads a session row's lineage", () => {
    expect(lineageOfSession({})).toBeNull()
    expect(
      lineageOfSession({
        parent_session: "s_p",
        parent_turn: 1,
        parent_call: "d1",
        parent_run: "s_p-t1",
      })
    ).toEqual({ session: "s_p", turn: 1, call: "d1", run: "s_p-t1" })
  })
})

describe("storyChildren", () => {
  it("joins the caller-linked runs on their call", () => {
    const kids = storyChildren({
      children: [{ id: "p/0/c1", parent_call_id: "c1" }],
      caller_linked: [
        { id: "s_c-t1", parent_call_id: "", caller_call_id: "d1" },
      ],
    })
    expect(kids.map((k) => [k.id, k.parent_call_id])).toEqual([
      ["p/0/c1", "c1"],
      ["s_c-t1", "d1"],
    ])
    expect(storyChildren({ children: [] })).toEqual([])
  })
})

describe("linkedHoles / delegatedHoles (U14-fix)", () => {
  it("are the shared table's badges in their own words, no fix", () => {
    expect(linkedHoles({})).toEqual([])
    const h = linkedHoles({
      caller_linked_more: true,
      caller_linked_hidden: 1,
      caller_linked_unknown: true,
    })
    expect(h.map((m) => m.hole)).toEqual([
      "truncated",
      "hidden",
      "not_recorded",
    ])
    expect(h.every((m) => m.fix === "" && !!m.reason)).toBe(true)
    expect(h[1].reason).toBe("1 run its calls started carry another public id")
    expect(
      delegatedHoles({ delegated_more: true, delegated_unknown: true }).map(
        (m) => m.hole
      )
    ).toEqual(["truncated", "not_recorded"])
    expect(delegatedHoles({})).toEqual([])
  })
})
