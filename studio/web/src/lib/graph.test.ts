// lib/graph.ts (U9.2, audit §6.2): the call graph over the run tree's
// spans — agents and MCP servers as nodes, edges with call counts and
// the slowest call — on rw.deep-delegation's director run.
import { describe, expect, it } from "vitest"

import { DIR, INV, OPS, SPANS, inv, span, tool } from "@/test/director-fixture"
import {
  callGraph,
  catalogSources,
  edgeLabel,
  graphUnsourced,
  loopLabel,
  nodeLine,
  nodeOfRun,
  nodeStatus,
  nodeStatusLine,
} from "./graph"

describe("callGraph", () => {
  const g = callGraph(SPANS, DIR)

  it("draws the three agents in call-depth columns, edges counted, the slowest call kept", () => {
    expect(g.nodes.map((n) => [n.id, n.depth, n.runs])).toEqual([
      ["a:director", 0, [DIR]],
      ["a:ops-manager", 1, [OPS]],
      ["a:inventory-specialist", 2, [INV]],
    ])
    expect(g.edges.map((e) => [e.from, e.to, edgeLabel(e)])).toEqual([
      ["a:director", "a:ops-manager", "1× · 9.3ms"],
      ["a:ops-manager", "a:inventory-specialist", "1× · 7.3ms"],
    ])
    expect(nodeLine(g.nodes[0])).toBe("1 run · slowest 14.0ms")
  })

  it("shares the tree's selection: a run's node", () => {
    expect(nodeOfRun(g, INV)?.id).toBe("a:inventory-specialist")
    expect(nodeOfRun(g, "nope")).toBeUndefined()
  })

  it("one node per agent: two runs of a worker are one node, one edge of 2×, failures counted", () => {
    const second = "r_dir/0/call_1/0/call_2"
    const more = [
      ...SPANS,
      span(
        "i2_inv",
        "o_t",
        "invoke_agent inventory-specialist",
        2,
        5,
        // No weft.parent.run.id: core's id rule names the parent.
        inv(second, "inventory-specialist"),
        "error"
      ),
    ]
    const g2 = callGraph(more, DIR)
    const w = g2.nodes.find((n) => n.id === "a:inventory-specialist")!
    expect(w.runs).toEqual([INV, second])
    expect(w.fails).toBe(1)
    expect(nodeLine(w)).toBe("2 runs · slowest 7.3ms · 1 failed")
    const e = g2.edges.find((x) => x.to === w.id)!
    expect(edgeLabel(e)).toBe("2× · 7.3ms · 1 failed")
    // Its status line (U34-fix4, UX-S3-14): the worst of its runs —
    // from the spans with no row, a row's words where the page read one.
    const st = nodeStatus(w)!
    expect([st.status.key, st.count, st.runs, st.steps]).toEqual([
      "failed",
      1,
      2,
      4,
    ])
    expect(nodeStatusLine(st)).toEqual({
      word: "failed 1 of 2",
      rest: " · 4 steps",
    })
    const parkedRow = { id: INV, status: "succeeded", pending: 1, steps: 3 }
    const viaRows = nodeStatus(w, (r) =>
      r === INV
        ? parkedRow
        : r === second
          ? { id: r, status: "failed" }
          : undefined
    )!
    // A row with no steps: the span's count stands in.
    expect([viaRows.status.key, viaRows.steps]).toEqual(["failed", 5])
    expect(
      nodeStatus(w, (r) =>
        r === INV ? parkedRow : { id: r, status: "succeeded" }
      )!.status.key
    ).toBe("parked")
  })

  it("a run parked on weft.run.pending reads parked from its span alone; an MCP node has no status", () => {
    const parked = callGraph(
      SPANS.map((s) => {
        const a = s.attrs
        return a["weft.run.id"] === INV &&
          a["gen_ai.operation.name"] === "invoke_agent"
          ? { ...s, attrs: { ...a, "weft.run.pending": 1 } }
          : s
      }),
      DIR
    )
    const n = parked.nodes.find((x) => x.id === "a:inventory-specialist")!
    expect(nodeStatus(n)?.status.key).toBe("parked")
    expect(nodeStatusLine(nodeStatus(n)!).word).toBe("parked")
    expect(nodeStatus({ ...n, kind: "mcp", facts: [] })).toBeUndefined()
  })

  it("MCP calls are edges to their server's node, from the tools record's source", () => {
    const more = [
      ...SPANS,
      span("m1", "d_inv", "execute_tool billing_lookup", 12.3, 12.35, {
        ...tool(DIR, 1, "billing_lookup", 9),
      }),
      span("m2", "d_inv", "execute_tool billing_lookup", 12.4, 12.5, {
        ...tool(DIR, 1, "billing_lookup", 9),
      }),
    ]
    const src = (run: string, name: string) =>
      run === DIR && name === "billing_lookup" ? "mcp:billing" : "local"
    const g2 = callGraph(more, DIR, src)
    const m = g2.nodes.find((n) => n.kind === "mcp")!
    expect(m).toMatchObject({
      id: "m:mcp:billing",
      label: "mcp:billing",
      depth: 1,
    })
    expect(nodeLine(m)).toBe("2 calls · slowest 100µs")
    expect(g2.edges.find((e) => e.to === m.id)).toMatchObject({
      from: "a:director",
      count: 2,
    })
    // A local tool is not a node.
    expect(g2.nodes).toHaveLength(4)
  })

  it("a self-call is a loop edge; a resumed call's child finds its parent by core's id rule (U9.2-fix)", () => {
    const spans = [
      span("a", "", "invoke_agent director", 0, 30, inv("R", "director")),
      span("b", "a", "invoke_agent director", 2, 20, {
        ...inv("R/0/c1", "director"),
        "weft.parent.run.id": "R",
      }),
      // No weft.parent.run.id: <parent>/resume/<call> names R.
      span(
        "c",
        "a",
        "invoke_agent worker",
        21,
        25,
        inv("R/resume/c2", "worker")
      ),
    ]
    const g2 = callGraph(spans, "R")
    expect(g2.nodes.map((n) => [n.id, n.depth, n.runs])).toEqual([
      ["a:director", 0, ["R", "R/0/c1"]],
      ["a:worker", 1, ["R/resume/c2"]],
    ])
    expect(g2.edges.map((e) => [e.from, e.to, e.count])).toEqual([
      ["a:director", "a:director", 1],
      ["a:director", "a:worker", 1],
    ])
    expect(loopLabel(g2.edges[0])).toBe("↻ 1× · 18.0ms")
  })

  it("a child run's tools whose catalog the page did not read are listed unsourced, never guessed", () => {
    const src = () => "mcp:billing"
    const g2 = callGraph(SPANS, DIR, src, (r) => r === DIR)
    expect(g2.unsourced).toEqual([OPS, INV].sort())
    expect(g2.nodes.some((n) => n.kind === "mcp")).toBe(true)
    expect(graphUnsourced(2)).toMatch(/^MCP calls of 2 child runs not shown/)
    expect(callGraph(SPANS, DIR).unsourced).toEqual([])
  })

  it("reads the sources from a run's request catalogs", () => {
    const steps = new Map([
      [
        0,
        {
          rows: [
            {
              tools: {
                tools: [
                  { name: "a", source: "local" },
                  { name: "b", source: "mcp" },
                ],
              },
            },
          ],
        },
      ],
    ])
    expect([...catalogSources(steps)]).toEqual([
      ["a", "local"],
      ["b", "mcp"],
    ])
    expect(catalogSources(undefined).size).toBe(0)
  })
})
