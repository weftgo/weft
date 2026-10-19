// lib/loopgraph.ts (U34; WEFT-LOOP-GRAPH.md §6) over the arena's runs
// (src/test/loop-fixtures.ts): the counts, failures, parks and ending
// per tool node, the insight strip, the call stepper's index, the
// scrubber's lit edges, the span-less fold, and the 1,000-step budget.
import { describe, expect, it } from "vitest"

import {
  callOf,
  catalogTools,
  END,
  litBy,
  loopChips,
  loopGraph,
  loopInsights,
  nodeOfKey,
  runChain,
  toolMarks,
} from "./loopgraph"
import type { LoopOptions } from "./loopgraph"
import { ALL_RUNS } from "./run-tree"
import { waterfallRows } from "./waterfall"
import { treeRows } from "./waterfall-tree"
import {
  build,
  DESK_PARKED,
  DESK_RESUME,
  DIRECTOR,
  FLAKY,
  longRun,
  POLLER,
  STORM,
  WORKER,
} from "../test/loop-fixtures"
import type { FxRun } from "../test/loop-fixtures"

function graphOf(r: FxRun, o: LoopOptions = {}, run = r.id) {
  const b = build(r)
  const rows = treeRows(
    {
      runId: r.id,
      status: r.failed ? "failed" : "succeeded",
      spans: b.spans,
      fold: b.fold,
      expanded: ALL_RUNS,
    },
    "time"
  ).rows
  const args = new Map<string, unknown>()
  for (const s of b.fold.steps)
    for (const c of s.toolCalls) args.set(c.callId, c.args)
  return {
    b,
    rows,
    g: loopGraph(rows, run, {
      pending: run === r.id ? b.pending : undefined,
      argsOf: (id) => args.get(id),
      ...o,
    }),
  }
}

const counts = (g: ReturnType<typeof loopGraph>) =>
  g.tools.map((t) => [t.name, toolMarks(t)])

describe("rw.flaky-infra", () => {
  const { g } = graphOf(FLAKY, { stoppedBy: "final_answer" })
  it("one node per tool: inventory_api twice (the retry), the three failing tools once", () => {
    expect(counts(g)).toEqual([
      ["inventory_api", "×2 ✗"],
      ["shipping_quote", "×1 ✗"],
      ["legacy_billing", "×1 ✗"],
      ["tax_service", "×1 ✗"],
    ])
    expect(g.tools[1].calls[0].error).toBe("timeout")
    expect(g.tools[2].calls[0].error).toBe("panic")
    expect(g.tools[3].calls[0].error).toBe("RATE_LIMITED")
    expect(g.end.word).toBe("the model answered")
    expect(g.end.tone).toBe("ok")
  })
  it("the insight strip names the failing tools, three at most", () => {
    expect(loopInsights(g).map((i) => [i.kind, i.text, i.node])).toEqual([
      ["fails", "shipping_quote failed 1 of 1", "t:shipping_quote"],
      ["fails", "legacy_billing failed 1 of 1", "t:legacy_billing"],
      ["fails", "tax_service failed 1 of 1", "t:tax_service"],
    ])
  })
  it("the stepper walks inventory_api's two calls", () => {
    const inv = g.tools[0]
    expect(inv.calls.map((c) => c.key)).toEqual([
      expect.stringMatching(/^c:0:/),
      expect.stringMatching(/^c:1:/),
    ])
    expect(callOf(g, inv.calls[1].key)).toEqual({ tool: inv, i: 1 })
    expect(nodeOfKey(g, inv.calls[0].key)).toBe("t:inventory_api")
  })
})

describe("rw.budget-guards", () => {
  it("the poller: poll_job ×3 with the same args, the end loop_detected", () => {
    const { g } = graphOf(POLLER, {
      stoppedBy: "loop_detected",
      status: "failed",
    })
    expect(counts(g)).toEqual([["poll_job", "×3"]])
    expect(g.end.by).toBe("loop_detected")
    expect(g.end.word).toBe("loop detected")
    expect(g.end.tone).toBe("bad")
    expect(loopInsights(g).map((i) => i.text)).toEqual([
      "ended by loop detected",
      "poll_job ×3 same args",
    ])
    expect(loopInsights(g)[0].node).toBe(END)
  })
  it("the worker ends max_steps; different args are no repeat", () => {
    const { g } = graphOf(WORKER, { stoppedBy: "max_steps", status: "failed" })
    expect(g.end.word).toBe("max steps reached")
    expect(loopInsights(g).map((i) => i.kind)).toEqual(["end"])
  })
  it("content-off (no args recorded): no repeat is claimed", () => {
    const { g } = graphOf(POLLER, {
      stoppedBy: "loop_detected",
      argsOf: () => undefined,
    })
    expect(loopInsights(g).map((i) => i.kind)).toEqual(["end"])
  })
})

describe("rw.support-desk", () => {
  it("the parked run: lookup_order above issue_refund ⏸, the end parked", () => {
    const { g } = graphOf(DESK_PARKED, { stoppedBy: "parked" })
    expect(counts(g)).toEqual([
      ["lookup_order", "×1"],
      ["issue_refund", "×1 ⏸"],
    ])
    expect(g.tools[1].calls[0].outcome).toBe("parked")
    expect(g.end.tone).toBe("parked")
    expect(g.end.word).toBe("parked on an approval")
  })
  it("the fold alone (no spans): the park from run_finish.pending, the same counts", () => {
    const b = build(DESK_PARKED)
    const rows = waterfallRows({ runId: DESK_PARKED.id, fold: b.fold }).rows
    const g = loopGraph(rows, DESK_PARKED.id, {
      pending: b.pending,
      stoppedBy: "parked",
    })
    expect(counts(g)).toEqual([
      ["lookup_order", "×1"],
      ["issue_refund", "×1 ⏸"],
    ])
  })
  it("the resume: issue_refund ran, the end has_tool_call:submit_output", () => {
    const { g } = graphOf(DESK_RESUME, {
      stoppedBy: "has_tool_call:submit_output",
    })
    expect(counts(g)).toEqual([
      ["issue_refund", "×1"],
      ["submit_output", "×1"],
    ])
    expect(g.end.by).toBe("has_tool_call:submit_output")
    expect(g.end.word).toBe("stopped by the tool call submit_output")
  })
})

describe("rw.tool-storm", () => {
  it("the fan-out step lights its six parallel calls; the others are dim", () => {
    const { g } = graphOf(STORM)
    expect(counts(g)[0]).toEqual(["get_weather", "×6"])
    const lit = litBy(g, 0)
    expect([...lit].sort()).toEqual([
      "t:forecast_report",
      "t:get_weather",
      "t:ledger_append",
    ])
    expect(g.steps[0].tools.filter((t) => t === "t:get_weather")).toHaveLength(
      6
    )
    expect(litBy(g, 1).size).toBe(0)
  })
})

describe("rw.deep-delegation", () => {
  it("a subagent call names its child run; the child's own loop builds from the same rows", () => {
    const { g, rows } = graphOf(DIRECTOR)
    expect(g.tools[0]).toMatchObject({
      name: "ops_manager",
      agent: "ops-manager",
    })
    const ops = g.tools[0].calls[0].child!
    expect(ops).toBe("r_dir/0/call_1")
    const child = loopGraph(rows, ops)
    expect(child.agent).toBe("ops-manager")
    expect(child.tools[0]).toMatchObject({
      name: "inventory_specialist",
      agent: "inventory-specialist",
    })
    expect(child.end.word).toBe("the model answered")
    expect(runChain("r_dir", "r_dir/0/call_1/0/call_1")).toEqual([
      "r_dir",
      "r_dir/0/call_1",
      "r_dir/0/call_1/0/call_1",
    ])
    expect(runChain("r_dir", "r_dir")).toEqual(["r_dir"])
  })
})

describe("the catalog", () => {
  it("offered and never called; unread is null, never none", () => {
    const steps = new Map([
      [
        0,
        {
          rows: [
            {
              tools: {
                tools: [
                  { name: "lookup_order" },
                  { name: "search_docs" },
                  { name: "gh_issue", source: "mcp:github" },
                ],
              },
            },
          ],
        },
      ],
    ])
    const cat = catalogTools(steps)
    expect(cat).toEqual([
      { name: "lookup_order" },
      { name: "search_docs" },
      { name: "gh_issue", source: "mcp:github" },
    ])
    const { g } = graphOf(DESK_PARKED, { catalog: cat })
    expect(g.offered).toEqual(["search_docs", "gh_issue"])
    expect(loopInsights(g).at(-1)!.text).toBe(
      "search_docs, gh_issue never called"
    )
    expect(catalogTools(new Map())).toBeNull()
    expect(graphOf(DESK_PARKED).g.offered).toBeNull()
  })
})

describe("the panel's line", () => {
  it("chips: each tool with its marks and first step, then the end", () => {
    const { g } = graphOf(FLAKY, { stoppedBy: "final_answer" })
    expect(loopChips(g)).toEqual([
      { text: "inventory_api ×2 ✗", step: 0, tone: "error" },
      { text: "shipping_quote ×1 ✗", step: 0, tone: "error" },
      { text: "legacy_billing ×1 ✗", step: 0, tone: "error" },
      { text: "tax_service ×1 ✗", step: 0, tone: "error" },
      { text: "→ the model answered", step: 2, tone: "ok" },
    ])
  })
})

describe("the budget (§3.5)", () => {
  it("a 1,000-step run builds in under 50 ms", () => {
    const r = longRun(1000)
    const b = build(r)
    const rows = treeRows(
      { runId: r.id, status: "failed", spans: b.spans, fold: b.fold },
      "time"
    ).rows
    // Warm once, then measure the model alone (the rows are the page's).
    loopGraph(rows, r.id)
    const t0 = performance.now()
    const g = loopGraph(rows, r.id, { stoppedBy: "max_steps" })
    loopInsights(g)
    const ms = performance.now() - t0
    expect(g.steps).toHaveLength(1000)
    expect(g.tools.map((t) => t.calls.length)).toEqual([250, 250, 250, 250])
    expect(ms).toBeLessThan(50)
  })
})
