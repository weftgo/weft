// lib/run-tree.ts (U8.2): the run page's projection of the run tree,
// its ?sel= reader and the detail pane's choice of span, on the
// director run.
import { describe, expect, it } from "vitest"

import { DIR, FOLD, INV, OPS, SPANS } from "@/test/director-fixture"
import {
  ALL_RUNS,
  detailSpan,
  expandFor,
  qualifyKey,
  resolveRowSel,
  treeSpans,
} from "./run-tree"
import { spansFromFold } from "./trace"
import { rowBadge, waterfallRows } from "./waterfall"

const time = waterfallRows({ runId: DIR, spans: SPANS, fold: FOLD, total: 9 })
const order = waterfallRows({ runId: DIR, fold: FOLD, total: 9 }, "order")
const folded = spansFromFold(FOLD, 9, "succeeded")

describe("treeSpans", () => {
  it("keys, labels, gists, badges and marks from the rows", () => {
    const spans = treeSpans(time)
    const by = (k: string) => spans.find((s) => s.key === k)!
    expect(by("s0")).toMatchObject({
      id: "s0",
      kind: "step",
      label: "step 0",
      sub: "1 tool: ops_manager",
      badge: "tool_calls",
      lanes: 1,
      critical: true,
    })
    expect(by("s0:m")).toMatchObject({
      kind: "model",
      label: "model · script",
      sub: "→ 1 call",
    })
    expect(by("s0:m").timed?.span.span_id).toBe("d_c0")
    expect(by("c:0:call_1")).toMatchObject({
      kind: "tool",
      badge: "ok",
      lane: 1,
      tone: "tool",
    })
    expect(by(`r:${OPS}`)).toMatchObject({
      kind: "subagent",
      parent: "c:0:call_1",
      childRun: OPS,
    })
    // The order scale carries no timed span: its extents are positions.
    expect(treeSpans(order).some((s) => s.timed)).toBe(false)
  })

  it("rowBadge: an error's kind, an open row's state, a finish, ok", () => {
    const base = time.rows.find((r) => r.key === "s0")!
    expect(rowBadge({ ...base, status: "error", error: "timeout" })).toBe(
      "timeout"
    )
    expect(rowBadge({ ...base, status: "running" })).toBe("running")
    expect(rowBadge(base)).toBe("tool_calls")
    expect(rowBadge(time.rows[0])).toBeUndefined()
  })
})

describe("resolveRowSel", () => {
  it("a key, and the old forms as the key they are now", () => {
    const rows = time.rows
    expect(resolveRowSel(rows, "s1", DIR)).toBe("s1")
    expect(resolveRowSel(rows, "run", DIR)).toBe(`r:${DIR}`)
    expect(resolveRowSel(rows, `s:${DIR}:1`, DIR)).toBe("s1")
    expect(resolveRowSel(rows, `s:${DIR}:1:m`, DIR)).toBe("s1:m")
    expect(resolveRowSel(rows, `c:${DIR}:0:call_1`, DIR)).toBe("c:0:call_1")
    // A pre-U8.2 time-axis key: the row its span is behind.
    expect(resolveRowSel(rows, "t:d_c1", DIR)).toBe("s1:m")
    // A pre-G1 bare call id.
    expect(resolveRowSel(rows, "c:call_1", DIR, 0)).toBe("c:0:call_1")
    expect(resolveRowSel(rows, "c:gone", DIR)).toBeUndefined()
    expect(resolveRowSel(rows, undefined, DIR)).toBeUndefined()
  })
})

describe("detailSpan", () => {
  const spans = treeSpans(time)
  it("the fold's span where it has one; the child's tool call; a model call's chat span", () => {
    expect(detailSpan(`r:${DIR}`, spans, folded)?.key).toBe("run")
    expect(detailSpan("s0", spans, folded)?.node).toBeTruthy()
    expect(detailSpan("c:0:call_1", spans, folded)?.node).toBeTruthy()
    expect(detailSpan(`r:${OPS}`, spans, folded)?.key).toBe("c:0:call_1")
    expect(detailSpan("s0:m", spans, folded)?.timed?.span.span_id).toBe("d_c0")
    expect(detailSpan("t:d_http", spans, folded)?.timed?.span.name).toBe(
      "http.get"
    )
  })

  it("without a fold (a derived run), the spans: a step reads its model call's", () => {
    expect(detailSpan("s0", spans, [])?.timed?.span.span_id).toBe("d_c0")
    expect(detailSpan(`r:${DIR}`, spans, [])?.timed?.span.span_id).toBe("d_inv")
  })

  it("on the order scale, a model call reads its step", () => {
    expect(detailSpan("s1:m", treeSpans(order), folded)?.key).toBe("s1")
  })
})

describe("the child subtree in place (U9.2)", () => {
  const full = waterfallRows({
    runId: DIR,
    spans: SPANS,
    fold: FOLD,
    expanded: ALL_RUNS,
  })

  it("expandFor: the children above a ?sel= row, a child row itself left as drawn", () => {
    expect(expandFor(full.rows, `c:${INV}:0:call_1`, DIR)).toEqual([INV, OPS])
    expect(expandFor(full.rows, `s:${OPS}:1`, DIR)).toEqual([OPS])
    expect(expandFor(full.rows, `r:${INV}`, DIR)).toEqual([OPS])
    expect(expandFor(full.rows, `r:${OPS}`, DIR)).toEqual([])
    expect(expandFor(full.rows, "s1", DIR)).toEqual([])
    expect(expandFor(full.rows, "nope", DIR)).toEqual([])
    expect(expandFor(full.rows, undefined, DIR)).toEqual([])
  })

  it("resolveRowSel reads <child run>:<span id>", () => {
    expect(resolveRowSel(full.rows, `${INV}:i_t`, DIR)).toBe(
      `c:${INV}:0:call_1`
    )
    expect(resolveRowSel(full.rows, `${OPS}:o_inv`, DIR)).toBe(`r:${OPS}`)
    expect(resolveRowSel(full.rows, `${OPS}:i_t`, DIR)).toBeUndefined()
  })

  it("treeSpans marks a child row collapsed or expanded", () => {
    const one = treeSpans(
      waterfallRows({ runId: DIR, spans: SPANS, expanded: new Set([OPS]) })
    )
    expect(one.find((s) => s.key === `r:${OPS}`)!.collapsed).toBe(false)
    expect(one.find((s) => s.key === `r:${INV}`)!.collapsed).toBe(true)
  })

  it("qualifyKey writes a run's own keys as the trace page keys them", () => {
    expect(qualifyKey("s0", OPS)).toBe(`s:${OPS}:0`)
    expect(qualifyKey("s1:m", OPS)).toBe(`s:${OPS}:1:m`)
    expect(qualifyKey("s0:a2", OPS)).toBe(`s:${OPS}:0:a2`)
    expect(qualifyKey("c:0:call_1", OPS)).toBe(`c:${OPS}:0:call_1`)
    expect(qualifyKey("c:resume:call_1", OPS)).toBe(`c:${OPS}:resume:call_1`)
    expect(qualifyKey("t:ab", OPS)).toBe(`t:${OPS}:ab`)
    expect(qualifyKey(`r:${OPS}`, OPS)).toBe(`r:${OPS}`)
  })
})
