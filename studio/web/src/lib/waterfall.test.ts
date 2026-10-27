// The run tree (U8.1): waterfallRows on rw.deep-delegation's director
// run — its spans and its descendants' (the ?tree=1 shape: every span
// carries its weft.run.id) joined with its folded events — and on the
// same run without spans (the order scale).
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"

import type { Span, WireEvent } from "./api"
import { fold, linkView } from "./events"
import {
  ANSWER,
  at,
  chat,
  DIR,
  EVENTS,
  FOLD,
  inv,
  OPS,
  REPORT,
  span,
  SPANS,
  tool,
} from "@/test/director-fixture"
import type { FoldedRun } from "./events"
import {
  axisTicks,
  dimRows,
  extent,
  UNTIMED_HOLE,
  instantMs,
  spanTree,
  timeDomain,
  took,
  waterfallRows as ownRows,
} from "./waterfall"
import type { WaterfallRow } from "./waterfall"
// The run tree as Studio's run page builds it: the descendant rules
// in (lib/waterfall-tree.ts). ownRows is the panel's (a run's own
// spans): the split's guard is at the end of this file.
import { treeRows as waterfallRows } from "./waterfall-tree"

const brief = (r: WaterfallRow) =>
  `${"  ".repeat(r.depth)}${r.kind} ${r.key} ${r.label}${r.gist ? ` | ${r.gist}` : ""}`

describe("waterfallRows: the director run on the time scale", () => {
  const wf = waterfallRows({
    runId: DIR,
    status: "succeeded",
    spans: SPANS,
    fold: FOLD,
    total: EVENTS.length,
  })

  it("draws run › step › model / tool › one collapsed child", () => {
    expect(wf.scale).toBe("time")
    expect(wf.rows.map(brief)).toEqual([
      "run r:r_dir director | 2 steps",
      "  step s0 step 0 | 1 tool: ops_manager",
      "    model s0:m model · script | → 1 call",
      `    tool c:0:call_1 ops_manager | ${REPORT}`,
      "      span t:d_http http.get",
      "      child r:r_dir/0/call_1 ops-manager | 2 steps",
      `  step s1 step 1 | “${ANSWER}”`,
      `    model s1:m model · script | “${ANSWER}”`,
    ])
    const child = wf.rows.find((r) => r.kind === "child")!
    expect(child.child).toEqual({ runId: OPS, collapsed: true, loaded: true })
    // The parent keys walk the tree.
    expect(child.parent).toBe("c:0:call_1")
    expect(wf.rows.find((r) => r.key === "c:0:call_1")!.parent).toBe("s0")
  })

  it("times rows as offset + took, sub-millisecond kept, humanised", () => {
    const by = (k: string) => wf.rows.find((r) => r.key === k)!
    expect(wf.domain).toEqual([0, 14])
    expect(by("s0").start).toBeCloseTo(0.1, 3)
    expect(by("s0").took).toBeCloseTo(10.4, 3)
    expect(took(by("s0:m").took!)).toBe("15 µs")
    expect(took(by("c:0:call_1").took!)).toBe("9.5 ms")
    expect(took(by("r:r_dir").took!)).toBe("14.0 ms")
    expect(extent(by("s1"), "time")).toBe("1.2 ms")
    expect(by("s0").finish).toBe("tool_calls")
    expect(by("s0:m").ttft).toBe(0)
    expect(by("c:0:call_1").lane).toBe(1)
    expect(by("s0").lanes).toBe(1)
  })

  it("marks the critical path: both serial steps, the tool over the model call, the serial span before the child", () => {
    expect(wf.rows.filter((r) => r.critical).map((r) => r.key)).toEqual([
      "r:r_dir",
      "s0",
      "s0:m",
      "c:0:call_1",
      "t:d_http",
      "r:r_dir/0/call_1",
      "s1",
      "s1:m",
    ])
  })

  it("expands a child in place from the same spans (the ?tree=1 shape)", () => {
    const open = waterfallRows({
      runId: DIR,
      spans: SPANS,
      fold: FOLD,
      expanded: new Set([OPS]),
    })
    const rows = open.rows.map(brief)
    expect(rows.slice(5, 12)).toEqual([
      "      child r:r_dir/0/call_1 ops-manager | 2 steps",
      "        step s:r_dir/0/call_1:0 step 0 | 1 tool: inventory_specialist",
      "          model s:r_dir/0/call_1:0:m model · script | → 1 call",
      "          tool c:r_dir/0/call_1:0:call_1 inventory_specialist | 35 B",
      "            child r:r_dir/0/call_1/0/call_1 inventory-specialist | 2 steps",
      "        step s:r_dir/0/call_1:1 step 1",
      "          model s:r_dir/0/call_1:1:m model · script",
    ])
    // The grandchild stays collapsed: its rows (and the db.query span
    // under its tool) are not drawn.
    expect(rows.some((r) => r.includes("db.query"))).toBe(false)
    expect(rows.some((r) => r.includes("inventory_count"))).toBe(false)
    const keys = open.rows.map((r) => r.key)
    expect(new Set(keys).size).toBe(keys.length)
  })

  it("counts the spans it cannot place", () => {
    const bad = { ...SPANS[1], span_id: "x", start: "yesterday" }
    expect(waterfallRows({ runId: DIR, spans: [...SPANS, bad] }).unplaced).toBe(
      1
    )
  })
})

describe("waterfallRows: the same run without spans (the order scale)", () => {
  const wf = waterfallRows({
    runId: DIR,
    status: "succeeded",
    spans: [],
    fold: FOLD,
    total: EVENTS.length,
  })

  it("places steps, model calls and tools by stream position", () => {
    expect(wf.scale).toBe("order")
    expect(wf.domain).toEqual([0, 8])
    expect(wf.rows.map((r) => `${brief(r)} ${extent(r, "order")}`)).toEqual([
      "run r:r_dir director | 2 steps #0–8",
      "  step s0 step 0 | 1 tool: ops_manager #1–4",
      "    model s0:m model · script | → 1 call #1–2",
      `    tool c:0:call_1 ops_manager | ${REPORT} #2–3`,
      "      child r:r_dir/0/call_1 ops_manager #2–3",
      `  step s1 step 1 | “${ANSWER}” #5–7`,
      `    model s1:m model · script | “${ANSWER}” #5–7`,
    ])
    expect(wf.rows.find((r) => r.kind === "child")!.child).toEqual({
      runId: OPS,
      collapsed: true,
      loaded: false,
    })
    expect(wf.rows.every((r) => r.critical)).toBe(true)
  })

  it("a running run leaves its open rows open", () => {
    const live = waterfallRows({
      runId: DIR,
      status: "running",
      fold: fold(EVENTS.slice(0, 3)),
      total: 3,
    })
    const by = (k: string) => live.rows.find((r) => r.key === k)!
    expect(by("r:r_dir").took).toBeNull()
    expect(by("s0").status).toBe("running")
    expect(by("c:0:call_1").took).toBeNull()
    expect(extent(by("c:0:call_1"), "order")).toBe("#2–")
  })

  it("draws nothing without events", () => {
    expect(waterfallRows({ runId: DIR }).rows).toEqual([])
  })
})

describe("lanes and the critical path over parallel calls", () => {
  const p = (id: string, name: string, from: number, to: number) =>
    span(id, "inv", `execute_tool ${name}`, from, to, {
      ...tool("r", 0, name, 1),
      "gen_ai.tool.call.id": id,
    })
  const wf = waterfallRows({
    runId: "r",
    spans: [
      span("inv", "", "invoke_agent a", 0, 100, inv("r", "a")),
      span("c0", "inv", "chat m", 0, 10, chat("r", 0, "tool_calls", 3)),
      p("a", "a", 10, 40),
      p("b", "b", 10, 30),
      p("c", "c", 31, 60),
    ],
  })
  const by = (k: string) => wf.rows.find((r) => r.key === k)!

  it("assigns lanes greedily and counts them on the step", () => {
    expect([by("c:0:a").lane, by("c:0:b").lane, by("c:0:c").lane]).toEqual([
      1, 2, 2,
    ])
    expect(by("s0").lanes).toBe(2)
  })

  it("marks the chain that set the step's end, not the overlapped call", () => {
    expect(by("c:0:c").critical).toBe(true)
    expect(by("c:0:b").critical).toBe(true)
    expect(by("c:0:a").critical).toBe(false)
    expect(by("s0:m").critical).toBe(true)
  })
})

describe("dimRows (dim, never hide)", () => {
  const rows = waterfallRows({
    runId: DIR,
    spans: SPANS.map((s) =>
      s.span_id === "d_c1"
        ? {
            ...s,
            status: "error" as const,
            attrs: { ...s.attrs, "error.type": "timeout" },
          }
        : s
    ),
    fold: FOLD,
  }).rows

  it("dims nothing with no filter on", () => {
    expect(dimRows(rows, {}).size).toBe(0)
  })

  it("errors: the failing row and the rows above it stay lit", () => {
    const dim = dimRows(rows, { errors: true })
    const lit = rows.filter((r) => !dim.has(r.key)).map((r) => r.key)
    expect(lit).toEqual(["r:r_dir", "s1", "s1:m"])
    expect(rows.find((r) => r.key === "s1:m")!.error).toBe("timeout")
  })

  it("tools: tool and child rows, with their path", () => {
    const dim = dimRows(rows, { tools: true })
    const lit = rows.filter((r) => !dim.has(r.key)).map((r) => r.key)
    expect(lit).toEqual(["r:r_dir", "s0", "c:0:call_1", "r:r_dir/0/call_1"])
  })

  it("slow: rows of at least a fifth of the run", () => {
    const dim = dimRows(rows, { slow: true })
    expect(dim.has("c:0:call_1")).toBe(false)
    expect(dim.has("s0:m")).toBe(true)
  })
})

describe("a derived run (stock GenAI spans, no weft.run.id)", () => {
  it("reads the spans as the run's own: steps, model calls, the tool", () => {
    const spans = (
      JSON.parse(
        readFileSync(
          resolve(process.cwd(), "src/test/derived/spans.json"),
          "utf8"
        )
      ) as { spans: Span[] }
    ).spans
    const wf = waterfallRows({ runId: "otel-x", spans })
    expect(wf.rows.map((r) => `${r.depth} ${r.kind} ${r.label}`)).toEqual([
      "0 run support",
      "1 step step 0",
      "2 model model · gpt-5-2025-08-07", // the model that answered
      "2 tool lookup_order",
      "1 step step 1",
      "2 model model · gpt-5-2025-08-07", // the model that answered
    ])
  })

  it("its root row counts the steps the header does: two model calls, 2 steps — with or without Studio's step index (P020.1)", () => {
    const spans = (
      JSON.parse(
        readFileSync(
          resolve(process.cwd(), "src/test/derived/spans.json"),
          "utf8"
        )
      ) as { spans: Span[] }
    ).spans
    // No events: the fold is empty, as the run page's is for a derived run.
    const empty = { steps: [] } as unknown as FoldedRun
    const root = (s: Span[]) =>
      waterfallRows({ runId: "otel-x", spans: s, fold: empty }).rows[0]
    expect(root(spans).gist).toBe("2 steps")
    const bare = spans.map((s) => {
      const {
        ["weft.step.index"]: _,
        ["weft.tool.seq"]: __,
        ...attrs
      } = s.attrs
      return { ...s, attrs }
    })
    expect(root(bare).gist).toBe("2 steps")
  })
})

describe("hostile input", () => {
  it("a parent link that loops, and a call id stored twice, end and stay unique", () => {
    const wf = waterfallRows({
      runId: "r",
      expanded: new Set(["r"]),
      spans: [
        span("inv", "", "invoke_agent a", 0, 10, {
          ...inv("r", "a"),
          "weft.parent.run.id": "r",
          "weft.parent.call.id": "call_1",
        }),
        span("t1", "inv", "execute_tool x", 1, 2, tool("r", 0, "x", 1)),
        span("t2", "inv", "execute_tool x", 3, 4, tool("r", 0, "x", 1)),
      ],
    })
    expect(wf.rows.map((r) => r.key)).toEqual([
      "r:r",
      "s0",
      "c:0:call_1",
      "t:t2",
    ])
    expect(wf.rows[3].parent).toBe("s0")
  })
})

describe("the shared helpers", () => {
  it("keeps sub-millisecond digits Date.parse drops", () => {
    expect(instantMs(at(0.015)) - instantMs(at(0))).toBeCloseTo(0.015, 3)
    expect(instantMs("2026-10-01T09:00:00Z")).toBe(
      Date.parse("2026-10-01T09:00:00Z")
    )
    expect(Number.isNaN(instantMs("nope"))).toBe(true)
  })

  it("humanises a duration: µs, ms, s", () => {
    expect(took(0.015)).toBe("15 µs")
    expect(took(9.49)).toBe("9.5 ms")
    expect(took(120)).toBe("120 ms")
    expect(took(7737)).toBe("7.7 s")
    expect(took(-1)).toBe("—")
  })

  it("orders a span tree and gives the time domain", () => {
    expect(
      spanTree(SPANS)
        .map((n) => n.span.span_id)
        .slice(0, 4)
    ).toEqual(["d_inv", "d_c0", "d_t", "d_http"])
    expect(timeDomain(SPANS)).toEqual([0, 14])
  })
})

// ── U8.1-fix: the review's findings, one test each ─────────────────

describe("a call id two steps repeat (core numbers call_1 per step)", () => {
  // P delegates with call_1 in step 0 and again with call_1 in step 1:
  // each child hangs under the tool span its invoke_agent span names.
  const C0 = "P/0/call_1"
  const C1 = "P/1/call_1"
  const parent = { "weft.parent.run.id": "P", "weft.parent.call.id": "call_1" }
  const spans = [
    span("p", "", "invoke_agent p", 0, 30, inv("P", "p")),
    span("p_c0", "p", "chat m", 0, 1, chat("P", 0, "tool_calls", 1)),
    span("p_t0", "p", "execute_tool a", 1, 10, tool("P", 0, "a", 1)),
    span("k0", "p_t0", "invoke_agent a", 2, 9, inv(C0, "a", parent)),
    span("k0_c", "k0", "chat m", 2, 3, chat(C0, 0, "stop", 0)),
    span("p_c1", "p", "chat m", 11, 12, chat("P", 1, "tool_calls", 1)),
    span("p_t1", "p", "execute_tool b", 12, 20, tool("P", 1, "b", 1)),
    span("k1", "p_t1", "invoke_agent b", 13, 19, inv(C1, "b", parent)),
    span("k1_c", "k1", "chat m", 13, 14, chat(C1, 0, "stop", 0)),
  ]

  it("each tool gets its own child, and no child span is left as a raw row", () => {
    const wf = waterfallRows({
      runId: "P",
      spans,
      expanded: new Set([C0, C1]),
    })
    const by = (k: string) => wf.rows.find((r) => r.key === k)!
    expect(by(`r:${C0}`).parent).toBe("c:0:call_1")
    expect(by(`r:${C1}`).parent).toBe("c:1:call_1")
    expect(by(`s:${C0}:0:m`).parent).toBe(`s:${C0}:0`)
    expect(by(`s:${C1}:0:m`).parent).toBe(`s:${C1}:0`)
    expect(wf.rows.filter((r) => r.kind === "span")).toEqual([])
    // The child starts inside its tool.
    expect(by(`r:${C0}`).start).toBeGreaterThanOrEqual(by("c:0:call_1").start)
  })

  it("without the tool span, (parent run, call) is the fallback — a step's own child each, never overwritten", () => {
    // The tool spans arrive, the span each invoke_agent names as parent
    // does not: the (run, call) bucket holds both children, and each
    // tool takes the one that starts inside it.
    const cut = spans.map((s) =>
      s.span_id === "k0"
        ? { ...s, parent_span_id: "gone0" }
        : s.span_id === "k1"
          ? { ...s, parent_span_id: "gone1" }
          : s
    )
    const wf = waterfallRows({ runId: "P", spans: cut })
    const by = (k: string) => wf.rows.find((r) => r.key === k)!
    expect(by(`r:${C0}`).parent).toBe("c:0:call_1")
    expect(by(`r:${C1}`).parent).toBe("c:1:call_1")
    expect(wf.rows.filter((r) => r.kind === "span")).toEqual([])
  })

  it("one candidate under (run, call) is taken only by the tool it starts inside (U10.1-fix #9)", () => {
    // Step 0's child never exported a span; step 1's names a parent
    // that is gone: the bucket holds step 1's child alone, and step
    // 0's tool (which comes first) must not claim it.
    const cut = spans
      .filter((s) => !s.span_id.startsWith("k0"))
      .map((s) => (s.span_id === "k1" ? { ...s, parent_span_id: "gone1" } : s))
    const wf = waterfallRows({ runId: "P", spans: cut })
    const by = (k: string) => wf.rows.find((r) => r.key === k)
    expect(by(`r:${C1}`)?.parent).toBe("c:1:call_1")
    expect(wf.rows.filter((r) => r.parent === "c:0:call_1")).toEqual([])
  })
})

describe("a running delegation (the ?tree=1 shape, mid-flight)", () => {
  const P = "P"
  const C = "P/0/call_1"
  const events = [
    {
      type: "run_start",
      id: P,
      agent: "p",
      model: { provider: "x", name: "m" },
    },
    { type: "step_start", run_id: P, index: 0 },
    {
      type: "tool_start",
      run_id: P,
      seq: 1,
      call_id: "call_1",
      name: "ops",
      args: {},
    },
  ] as unknown as WireEvent[]
  // Only the finished spans arrive: the root's step-0 chat and the
  // child's step-0 chat — neither invoke_agent nor the tool has ended.
  const spans = [
    span("p_c0", "p", "chat m", 0, 1, chat(P, 0, "tool_calls", 1)),
    span("c_c0", "c_inv", "chat m", 2, 3, chat(C, 0, "tool_calls", 1)),
  ]

  it("a child row no span names reads its agent (the run document's), on both scales (U8.3-fix)", () => {
    const input = {
      runId: P,
      status: "running",
      spans,
      fold: linkView(fold(events), [
        { id: C, parent_call_id: "call_1", parent_run_id: P },
      ]),
      agents: new Map([[C, "researcher"]]),
    }
    const label = (scale?: "order") =>
      waterfallRows(input, scale).rows.find((r) => r.key === `r:${C}`)?.label
    expect(label()).toBe("researcher")
    expect(label("order")).toBe("researcher")
    // Without it, both fall back to the call's tool name.
    const bare = { ...input, agents: undefined }
    expect(
      waterfallRows(bare, "order").rows.find((r) => r.key === `r:${C}`)?.label
    ).toBe("ops")
  })

  it("the open call is a row, its child a running row under it, never a foreign span", () => {
    const wf = waterfallRows({
      runId: P,
      status: "running",
      spans,
      fold: linkView(fold(events), [
        { id: C, parent_call_id: "call_1", parent_run_id: P },
      ]),
    })
    expect(wf.scale).toBe("time")
    expect(wf.rows.map(brief)).toEqual([
      "run r:P p | 1 step",
      "  step s0 step 0",
      "    model s0:m model · script | → 1 call",
      "    tool c:0:call_1 ops",
      // No agent known: the label rule falls back to its tool's name.
      "      child r:P/0/call_1 ops",
    ])
    const by = (k: string) => wf.rows.find((r) => r.key === k)!
    for (const k of ["r:P", "s0", "c:0:call_1", `r:${C}`]) {
      expect(by(k).took).toBeNull()
      expect(by(k).status).toBe("running")
    }
    expect(by(`r:${C}`).child).toEqual({
      runId: C,
      collapsed: true,
      loaded: true,
    })
    expect(by("c:0:call_1").start).toBe(1)
  })

  it("with no fold link, the child is a running row under the root", () => {
    const wf = waterfallRows({ runId: P, status: "running", spans })
    const child = wf.rows.find((r) => r.key === `r:${C}`)!
    expect(child.kind).toBe("child")
    expect(child.parent).toBe("r:P")
    expect(child.took).toBeNull()
    expect(child.status).toBe("running")
    expect(wf.rows.filter((r) => r.kind === "span")).toEqual([])
  })

  it("a step the model is still answering is an open step with an open model call", () => {
    const wf = waterfallRows({
      runId: DIR,
      status: "running",
      spans: SPANS.filter((s) => s.span_id !== "d_c1" && s.span_id !== "d_inv"),
      fold: linkView(fold(EVENTS.slice(0, 7)), [
        { id: OPS, parent_call_id: "call_1", parent_run_id: DIR },
      ]),
    })
    const s1 = wf.rows.find((r) => r.key === "s1")!
    const m1 = wf.rows.find((r) => r.key === "s1:m")!
    expect([s1.took, s1.status, m1.took, m1.status]).toEqual([
      null,
      "running",
      null,
      "running",
    ])
    expect(m1.parent).toBe("s1")
    // From the run's latest end: step 0's tool.
    const t = wf.rows.find((r) => r.key === "c:0:call_1")!
    expect(s1.start).toBeCloseTo(t.start + t.took!, 3)
  })
})

describe("string-typed attributes (the ClickHouse fallback map)", () => {
  const wf = waterfallRows({
    runId: "r",
    spans: [
      span("inv", "", "invoke_agent a", 0, 10, inv("r", "a")),
      span("res", "inv", "execute_tool x", 0.5, 1, {
        ...tool("r", 0, "x", 1),
        "weft.tool.approved": "true",
      }),
      span("c0", "inv", "chat m", 1, 2, {
        ...chat("r", 0, "tool_calls", 1),
        "gen_ai.response.finish_reasons": '["tool_calls"]',
      }),
      span("t0", "inv", "execute_tool x", 2, 3, {
        ...tool("r", 0, "x", 1),
        "weft.tool.pending": "true",
      }),
    ],
  })
  const by = (k: string) => wf.rows.find((r) => r.key === k)!

  it('"true" reads true: the resumed call apart from step 0\'s, a pending call pending', () => {
    expect(by("c:resume:call_1").parent).toBe("r:r")
    expect(by("c:0:call_1").parent).toBe("s0")
    expect(by("c:0:call_1").status).toBe("pending")
  })

  it("a finish list stored as a JSON string reads its first reason", () => {
    expect(by("s0").finish).toBe("tool_calls")
    expect(by("s0:m").finish).toBe("tool_calls")
  })
})

describe("span ids are unique within a trace only", () => {
  it("a nested Generate's run in its own trace nests by trace and span id; its rows run-qualified", () => {
    const other = (s: Span): Span => ({ ...s, trace_id: "t2" })
    const wf = waterfallRows({
      runId: "r",
      expanded: new Set(["n"]),
      spans: [
        span("a", "", "invoke_agent a", 0, 10, inv("r", "a")),
        span("x", "a", "http.get", 1, 2, {}),
        // The same span ids in another trace: a nested Generate (no
        // weft.parent.*, no fold link).
        other(span("a", "", "invoke_agent n", 3, 9, inv("n", "n"))),
        other(span("x", "a", "db.query", 4, 5, { "weft.run.id": "n" })),
      ],
    })
    expect(wf.rows.map(brief)).toEqual([
      "run r:r a | 2 steps",
      "  span t:x http.get",
      "  child r:n n | 2 steps",
      "    span t:n:x db.query",
    ])
  })

  it("a nested Generate under a tool span is that tool's collapsed child", () => {
    const wf = waterfallRows({
      runId: "r",
      spans: [
        span("a", "", "invoke_agent a", 0, 10, inv("r", "a")),
        span("c", "a", "chat m", 0, 1, chat("r", 0, "tool_calls", 1)),
        span("t", "a", "execute_tool x", 1, 9, tool("r", 0, "x", 1)),
        span("na", "t", "invoke_agent n", 2, 8, inv("n", "n")),
        span("nc", "na", "chat m", 2, 3, chat("n", 0, "stop", 0)),
      ],
    })
    const n = wf.rows.find((r) => r.key === "r:n")!
    expect(n.parent).toBe("c:0:call_1")
    expect(n.child).toEqual({ runId: "n", collapsed: true, loaded: true })
    expect(wf.rows.map((r) => r.kind)).not.toContain("span")
  })
})

describe("two model calls in one step (re-run after an error)", () => {
  // Given out of start order: the error came first.
  const wf = waterfallRows({
    runId: "r",
    spans: [
      span("a", "", "invoke_agent a", 0, 10, inv("r", "a")),
      span("c2", "a", "chat m", 4, 5, chat("r", 0, "stop", 0)),
      span(
        "c1",
        "a",
        "chat m",
        1,
        2,
        { ...chat("r", 0, "error", 0), "error.type": "timeout" },
        "error"
      ),
      span("x", "c2", "attempt", 4, 5, {
        "gen_ai.operation.name": "attempt",
        "weft.run.id": "r",
        "weft.attempt.index": 1,
      }),
    ],
  })
  const by = (k: string) => wf.rows.find((r) => r.key === k)!

  it("keys by start (:m the first, :m2 the next, its attempts under it); the step reads the last", () => {
    expect(by("s0:m").span!.span_id).toBe("c1")
    expect(by("s0:m2").span!.span_id).toBe("c2")
    expect(by("s0:m2:a1").parent).toBe("s0:m2")
    expect(by("s0").status).toBe("ok")
    expect(by("s0").finish).toBe("stop")
    expect(by("s0:m").error).toBe("timeout")
  })
})

describe("the words (cosmetic)", () => {
  it("chooses the unit on the rounded value", () => {
    expect(took(999.6)).toBe("1.0 s")
    expect(took(999.4)).toBe("999 ms")
    expect(took(99.96)).toBe("100 ms")
    expect(took(0.9996)).toBe("1.0 ms")
    expect(took(0)).toBe("0 µs")
  })

  it("an expanded child's scaled positions keep one decimal", () => {
    const child = fold([
      { type: "run_start", id: OPS, agent: "ops-manager" },
      { type: "step_start", run_id: OPS, index: 0 },
      { type: "text_delta", run_id: OPS, text: "x" },
      { type: "step_finish", run_id: OPS, index: 0, reason: "stop" },
      { type: "step_start", run_id: OPS, index: 1 },
      { type: "text_delta", run_id: OPS, text: "y" },
      { type: "step_finish", run_id: OPS, index: 1, reason: "stop" },
      { type: "run_finish", run_id: OPS, steps: 2 },
    ] as unknown as WireEvent[])
    const wf = waterfallRows({
      runId: DIR,
      status: "succeeded",
      fold: FOLD,
      total: EVENTS.length,
      children: new Map([[OPS, child]]),
      expanded: new Set([OPS]),
    })
    const texts = wf.rows.map((r) => extent(r, "order"))
    for (const t of texts) expect(t).toMatch(/^#\d+(\.\d)?–(\d+(\.\d)?)?$/)
    expect(texts.some((t) => t.includes("."))).toBe(true)
  })
})

describe("fold rows no span covers (U8.2-fix: truncation is visible)", () => {
  const R = "r1"
  // Step 0's spans were read; step 1 finished in the fold, its spans
  // not (yet) read; step 2 is open.
  const spans = [
    span("c0", "root", "chat m", 0, 3, chat(R, 0, "tool_calls", 1)),
    span("x0", "root", "execute_tool lookup", 4, 8, tool(R, 0, "lookup", 1)),
  ]
  const call = (i: number, done: boolean) => ({
    callId: "call_1",
    name: "lookup",
    startPos: 4 * i + 2,
    ...(done
      ? { finishPos: 4 * i + 3, result: { content: "ok", isError: false } }
      : {}),
  })
  const step = (i: number, done: boolean) => ({
    index: i,
    text: "",
    reasoning: "",
    toolCalls: done ? [call(i, true)] : [],
    ...(done ? { finish: { reason: "tool_calls", usage: {} } } : {}),
    from: 4 * i + 1,
    to: 4 * i + 4,
  })
  const foldOf = (finished: boolean, steps: unknown[]) =>
    ({
      runId: R,
      steps,
      pending: [],
      finished,
      startPos: 0,
      agent: "a",
    }) as unknown as FoldedRun

  it("live: a just-finished step is drawn at the edge, pending its spans, never gone", () => {
    const wf = waterfallRows(
      {
        runId: R,
        status: "running",
        spans,
        fold: foldOf(false, [step(0, true), step(1, true), step(2, false)]),
      },
      "time"
    )
    const by = (k: string) => wf.rows.find((r) => r.key === k)!
    expect(wf.rows.filter((r) => r.kind === "step").map((r) => r.key)).toEqual([
      "s0",
      "s1",
      "s2",
    ])
    expect(by(`r:${R}`).gist).toBe("3 steps")
    for (const k of ["s1", "s1:m", "c:1:call_1"]) {
      expect(by(k).untimed).toBe("pending")
      expect(by(k).took).toBe(0)
      expect(by(k).status).toBe("ok")
      expect(extent(by(k), "time")).toBe("—")
    }
    // After the row before it: step 0's end.
    expect(by("s1").start).toBe(by("s0").start + by("s0").took!)
    expect(by("c:1:call_1").parent).toBe("s1")
    // The open step stays open, no mark.
    expect([by("s2").took, by("s2").status, by("s2").untimed]).toEqual([
      null,
      "running",
      undefined,
    ])
    expect(by("s0").untimed).toBeUndefined()
  })

  it("finished: a step whose spans never came is drawn missing, in its place", () => {
    const wf = waterfallRows(
      {
        runId: R,
        status: "succeeded",
        spans,
        fold: foldOf(true, [step(0, true), step(1, true)]),
      },
      "time"
    )
    const by = (k: string) => wf.rows.find((r) => r.key === k)!
    expect(wf.rows.map((r) => r.key)).toEqual([
      `r:${R}`,
      "s0",
      "s0:m",
      "c:0:call_1",
      "s1",
      "s1:m",
      "c:1:call_1",
    ])
    for (const k of ["s1", "s1:m", "c:1:call_1"])
      expect(by(k).untimed).toBe("missing")
    expect(UNTIMED_HOLE.hole).toBe("not_recorded")
    expect(UNTIMED_HOLE.reason).toMatch(/no span covers this row/)
  })

  it("content-off: a tool row's gist is its result size, a string count read too", () => {
    const wf = waterfallRows(
      {
        runId: R,
        status: "succeeded",
        spans: [
          span("x0", "", "execute_tool lookup", 0, 4, {
            ...tool(R, 0, "lookup", 1),
            "weft.tool.result_bytes": "55",
          }),
        ],
        fold: foldOf(true, [
          {
            ...step(0, true),
            toolCalls: [
              { ...call(0, true), result: { content: "", isError: false } },
            ],
          },
        ]),
      },
      "time"
    )
    expect(wf.rows.find((r) => r.key === "c:0:call_1")!.gist).toBe("55 B")
  })
})

// P8-fix-a: the descendant rules live in lib/waterfall-tree.ts, which
// panel.js does not bundle. The panel reads a run's own spans (no
// ?tree=1): on that input both builds draw the same rows; only a
// descendant's spans tell them apart.
describe("the run tree's descendant half is split out (P8-fix-a)", () => {
  const own = SPANS.filter((s) => s.attrs["weft.run.id"] === DIR)
  it("a run's own spans: the panel's build and the run page's agree, row for row", () => {
    expect(own.length).toBeGreaterThan(0)
    expect(own.length).toBeLessThan(SPANS.length)
    for (const status of ["succeeded", "running"])
      expect(
        ownRows({ runId: DIR, status, spans: own, fold: FOLD, total: 9 }).rows
      ).toEqual(
        waterfallRows({ runId: DIR, status, spans: own, fold: FOLD, total: 9 })
          .rows
      )
    // The order scale too (no expanded child, no descendant folds).
    expect(ownRows({ runId: DIR, fold: FOLD, total: 9 }, "order").rows).toEqual(
      waterfallRows({ runId: DIR, fold: FOLD, total: 9 }, "order").rows
    )
  })

  it("a descendant's spans need the run page's build: without it they are not claimed by its row", () => {
    const tree = waterfallRows({ runId: DIR, spans: SPANS, fold: FOLD })
    const bare = ownRows({ runId: DIR, spans: SPANS, fold: FOLD })
    expect(tree.rows.filter((r) => r.runId === OPS)).toHaveLength(1)
    expect(bare.rows.filter((r) => r.runId === OPS).length).toBeGreaterThan(1)
  })
})

describe("axisTicks (P020.1: the time axis never overlaps)", () => {
  const label = (t: number) => (t ? took(t) : "0")
  // Each label's box as the waterfall draws it: the first from the left
  // edge, the last to the right edge, the rest centred.
  const boxes = (ticks: number[], lo: number, hi: number, w: number) =>
    ticks.map((t, i) => {
      const lw = label(t).length * 6.5
      const x = ((t - lo) / (hi - lo + 1)) * w
      return i === ticks.length - 1
        ? [w - lw, w]
        : i === 0
          ? [0, lw]
          : [x - lw / 2, x + lw / 2]
    })
  it("a wide track keeps five ticks", () => {
    expect(axisTicks(0, 301, 600, label)).toHaveLength(5)
  })
  it("the run page's default track (~200 px) thins them until none overlaps", () => {
    for (const [hi, w] of [
      [301, 204],
      [3200, 204],
      [10, 204],
      [25, 170],
      [301, 120],
    ]) {
      const t = axisTicks(0, hi, w, label)
      expect(t.length).toBeLessThan(5)
      expect(t.at(-1)).toBe(hi)
      const b = boxes(t, 0, hi, w)
      for (let i = 1; i < b.length; i++)
        expect(b[i][0]).toBeGreaterThanOrEqual(b[i - 1][1])
    }
  })
  it("too narrow for two labels: the end alone; unmeasured: the two ends", () => {
    expect(axisTicks(0, 3200, 40, label)).toEqual([3200])
    expect(axisTicks(0, 301, 0, label)).toEqual([0, 301])
  })
})
