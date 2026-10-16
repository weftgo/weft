// rw.deep-delegation's director run (U8.1, U8.2): its spans and its
// descendants' (the ?tree=1 shape — every span carries its weft.run.id
// and, as the API serves them, its run_id) and its folded events. The
// run tree's model tests (lib/waterfall.test.ts) and the run page's
// (routes/run-tree.test.tsx) read the same run.
import type { Span, WireEvent } from "@/lib/api"
import { fold, linkView } from "@/lib/events"

export const DIR = "r_dir"
export const OPS = "r_dir/0/call_1"
export const INV = "r_dir/0/call_1/0/call_1"

/** An instant `ms` after 09:00, microsecond digits kept. */
export const at = (ms: number) =>
  `2026-10-01T09:00:00.${String(Math.round(ms * 1000)).padStart(6, "0")}Z`

export function span(
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

export const inv = (
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
export const chat = (
  run: string,
  step: number,
  finish: string,
  calls: number
) => ({
  "gen_ai.operation.name": "chat",
  "gen_ai.request.model": "script",
  "gen_ai.response.model": "script",
  "gen_ai.response.finish_reasons": [finish],
  "weft.model.tool_calls": calls,
  "weft.ttft_ms": 0,
  "weft.run.id": run,
  "weft.step.index": step,
})
export const tool = (
  run: string,
  step: number,
  name: string,
  bytes: number
) => ({
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
export const SPANS: Span[] = [
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

export const ANSWER = "We have 42 units of A-17 in Lyon."
export const REPORT = "The specialist reports 42 units of A-17 in Lyon."
export const EVENTS = [
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

export const FOLD = linkView(fold(EVENTS), [
  { id: OPS, parent_call_id: "call_1", parent_run_id: DIR },
])
