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
  dimRows,
  extent,
  instantMs,
  spanTree,
  timeDomain,
  took,
  waterfallRows,
} from "./waterfall"
import type { WaterfallRow } from "./waterfall"

const DIR = "r_dir"
const OPS = "r_dir/0/call_1"
const INV = "r_dir/0/call_1/0/call_1"

/** An instant `ms` after 09:00, microsecond digits kept. */
const at = (ms: number) =>
  `2026-10-01T09:00:00.${String(Math.round(ms * 1000)).padStart(6, "0")}Z`

function span(
  id: string,
  parent: string,
  name: string,
  from: number,
  to: number,
  attrs: Record<string, unknown>,
  status: Span["status"] = "ok"
): Span {
  return {
    trace_id: "t1",
    span_id: id,
    parent_span_id: parent,
    name,
    kind: "internal",
    start: at(from),
    end: at(to),
    status,
    status_message: "",
    service: "weft-arena-dev",
    attrs,
    events: [],
  }
}

const inv = (
  run: string,
  agent: string,
  extra: Record<string, unknown> = {}
) => ({
  "gen_ai.operation.name": "invoke_agent",
  "gen_ai.agent.name": agent,
  "gen_ai.request.model": "script",
  "weft.run.id": run,
  "weft.run.steps": 2,
  ...extra,
})
const chat = (run: string, step: number, finish: string, calls: number) => ({
  "gen_ai.operation.name": "chat",
  "gen_ai.request.model": "script",
  "gen_ai.response.model": "script",
  "gen_ai.response.finish_reasons": [finish],
  "weft.model.tool_calls": calls,
  "weft.ttft_ms": 0,
  "weft.run.id": run,
  "weft.step.index": step,
})
const tool = (run: string, step: number, name: string, bytes: number) => ({
  "gen_ai.operation.name": "execute_tool",
  "gen_ai.tool.name": name,
  "gen_ai.tool.call.id": "call_1",
  "weft.run.id": run,
  "weft.step.index": step,
  "weft.tool.seq": 1,
  "weft.tool.result_bytes": bytes,
})

// The director (14 ms), its child ops-manager under its one tool call,
// and the grandchild inventory-specialist under ops-manager's.
const SPANS: Span[] = [
  span("d_inv", "", "invoke_agent director", 0, 14, inv(DIR, "director")),
  span(
    "d_c0",
    "d_inv",
    "chat script",
    0.1,
    0.115,
    chat(DIR, 0, "tool_calls", 1)
  ),
  span(
    "d_t",
    "d_inv",
    "execute_tool ops_manager",
    1,
    10.5,
    tool(DIR, 0, "ops_manager", 48)
  ),
  span("d_http", "d_t", "http.get", 1.05, 1.08, {}),
  span(
    "o_inv",
    "d_t",
    "invoke_agent ops-manager",
    1.1,
    10.4,
    inv(OPS, "ops-manager", {
      "weft.parent.run.id": DIR,
      "weft.parent.call.id": "call_1",
    })
  ),
  span("o_c0", "o_inv", "chat script", 1.2, 1.3, chat(OPS, 0, "tool_calls", 1)),
  span(
    "o_t",
    "o_inv",
    "execute_tool inventory_specialist",
    1.5,
    9,
    tool(OPS, 0, "inventory_specialist", 35)
  ),
  span(
    "i_inv",
    "o_t",
    "invoke_agent inventory-specialist",
    1.6,
    8.9,
    inv(INV, "inventory-specialist", {
      "weft.parent.run.id": OPS,
      "weft.parent.call.id": "call_1",
    })
  ),
  span("i_c0", "i_inv", "chat script", 1.7, 1.8, chat(INV, 0, "tool_calls", 1)),
  span(
    "i_t",
    "i_inv",
    "execute_tool inventory_count",
    2,
    2.1,
    tool(INV, 0, "inventory_count", 24)
  ),
  span("i_db", "i_t", "db.query", 2.02, 2.05, {}),
  span("i_c1", "i_inv", "chat script", 2.2, 8.8, chat(INV, 1, "stop", 0)),
  span("o_c1", "o_inv", "chat script", 9.1, 10.3, chat(OPS, 1, "stop", 0)),
  span("d_c1", "d_inv", "chat script", 11, 12.2, chat(DIR, 1, "stop", 0)),
]

const ANSWER = "We have 42 units of A-17 in Lyon."
const REPORT = "The specialist reports 42 units of A-17 in Lyon."
const EVENTS = [
  {
    type: "run_start",
    id: DIR,
    agent: "director",
    model: { provider: "wefttest", name: "script" },
  },
  { type: "step_start", run_id: DIR, index: 0 },
  {
    type: "tool_start",
    run_id: DIR,
    seq: 1,
    call_id: "call_1",
    name: "ops_manager",
    args: { prompt: "How many units of SKU A-17 are in Lyon?" },
  },
  {
    type: "tool_finish",
    run_id: DIR,
    seq: 1,
    call_id: "call_1",
    name: "ops_manager",
    content: REPORT,
    is_error: false,
  },
  {
    type: "step_finish",
    run_id: DIR,
    index: 0,
    reason: "tool_calls",
    usage: { input_tokens: 30, output_tokens: 10 },
  },
  { type: "step_start", run_id: DIR, index: 1 },
  { type: "text_delta", run_id: DIR, text: ANSWER },
  {
    type: "step_finish",
    run_id: DIR,
    index: 1,
    reason: "stop",
    usage: { input_tokens: 30, output_tokens: 20 },
  },
  {
    type: "run_finish",
    run_id: DIR,
    usage: { input_tokens: 60, output_tokens: 30 },
    steps: 2,
  },
] as unknown as WireEvent[]

const FOLD = linkView(fold(EVENTS), [
  { id: OPS, parent_call_id: "call_1", parent_run_id: DIR },
])

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
    expect(took(by("s0:m").took!)).toBe("15µs")
    expect(took(by("c:0:call_1").took!)).toBe("9.5ms")
    expect(took(by("r:r_dir").took!)).toBe("14.0ms")
    expect(extent(by("s1"), "time")).toBe("1.2ms")
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
    expect(took(0.015)).toBe("15µs")
    expect(took(9.49)).toBe("9.5ms")
    expect(took(120)).toBe("120ms")
    expect(took(7737)).toBe("7.7s")
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
