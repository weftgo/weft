// weft-arena's opt.usage-limit run (U10.1's Done line): each scripted
// step bills 100 in / 50 out and calls `more`; UsageLimit(input 250)
// is breached after step 2 finishes, so the loop's guard refuses the
// third model call — "weft: run failed at step 2: …". Three finished
// steps, no run_finish, an Error invoke_agent span; its spans, its
// transcript, its request record and two app log lines.
import type { RequestRow, RunDoc, RunsPage, Span, WireEvent } from "@/lib/api"
import { chat, span, tool } from "@/test/director-fixture"
import { golden } from "@/test/fake-studio"

export const UL = "r_ul"
export const UL_ERR =
  "weft: run failed at step 2: weft: run exceeded its usage limit (input 300/250, output 150/250)"

const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!
const usage = { input_tokens: 100, output_tokens: 50 }

export function ulDoc(over: Partial<RunDoc> = {}): RunDoc {
  return {
    ...rOK,
    id: UL,
    agent: "researcher",
    status: "failed",
    err: UL_ERR,
    stopped_by: "usage_limit",
    error_type: "usage_limit",
    steps: 3,
    stop_reason: "tool_calls",
    trace_id: "t1",
    forked_from: "",
    experiment_id: "",
    playground: false,
    children: [],
    holes: [],
    compactions: [],
    usage: { input_tokens: 300, output_tokens: 150 },
    ...over,
  }
}

const stepEvents = (i: number) => [
  { type: "step_start", run_id: UL, index: i },
  {
    type: "tool_start",
    run_id: UL,
    seq: i + 1,
    call_id: `c${i}`,
    name: "more",
    args: {},
  },
  {
    type: "tool_finish",
    run_id: UL,
    seq: i + 1,
    call_id: `c${i}`,
    name: "more",
    content: "ok",
    is_error: false,
  },
  {
    type: "step_finish",
    run_id: UL,
    index: i,
    reason: "tool_calls",
    usage,
    latency_ms: 2,
  },
]

export const UL_EVENTS = [
  {
    type: "run_start",
    id: UL,
    agent: "researcher",
    model: { provider: "wefttest", name: "script" },
  },
  ...stepEvents(0),
  ...stepEvents(1),
  ...stepEvents(2),
] as unknown as WireEvent[]

const call = (id: string) => [
  {
    role: "assistant",
    content: [{ type: "tool_call", id, name: "more", args: {} }],
  },
]
const result = (id: string) => [
  {
    role: "tool",
    content: [
      {
        type: "tool_result",
        call_id: id,
        name: "more",
        content: "ok",
        is_error: false,
      },
    ],
  },
]
export const UL_BODIES = [
  [{ role: "user", content: [{ type: "text", text: "keep going" }] }],
  call("c0"),
  result("c0"),
  call("c1"),
  result("c1"),
  call("c2"),
  result("c2"),
]

const tl = (run: string, step: number) => ({
  ...tool(run, step, "more", 2),
  "gen_ai.tool.call.id": `c${step}`,
})

export const UL_SPANS: Span[] = [
  span(
    "u_inv",
    "",
    "invoke_agent researcher",
    0,
    9,
    {
      "gen_ai.operation.name": "invoke_agent",
      "gen_ai.agent.name": "researcher",
      "weft.run.id": UL,
      "weft.run.steps": 3,
      "weft.run.stopped_by": "usage_limit",
      "error.type": "usage_limit",
    },
    "error"
  ),
  span("u_c0", "u_inv", "chat script", 0.1, 1, chat(UL, 0, "tool_calls", 1)),
  span("u_t0", "u_inv", "execute_tool more", 1.1, 2, tl(UL, 0)),
  span("u_c1", "u_inv", "chat script", 3, 4, chat(UL, 1, "tool_calls", 1)),
  span("u_t1", "u_inv", "execute_tool more", 4.1, 5, tl(UL, 1)),
  span("u_c2", "u_inv", "chat script", 6, 7, chat(UL, 2, "tool_calls", 1)),
  span("u_t2", "u_inv", "execute_tool more", 7.1, 8, tl(UL, 2)),
].map((s) => ({ ...s, run_id: UL }))

const req = (index: number, step: number, count: number): RequestRow => ({
  index,
  step,
  attempt: 1,
  time: rOK.started,
  system_hash: "sys",
  catalog_hash: "cat",
  content: "",
  truncated_bytes: 0,
  body: {
    step,
    attempt: 1,
    system_hash: "sys",
    messages_ref: { index, count },
    tools: { catalog_hash: "cat", names: ["more"] },
    sequential_tools: false,
    params: {},
    model: { provider: "wefttest", name: "script" },
    stream: false,
  },
})

/** Each step's request: 1, 3 and 5 messages (the input, then each
 * step's call and result appended), the one tool offered. */
export const UL_REQUESTS = {
  requests: [req(0, 0, 1), req(2, 1, 3), req(4, 2, 5)],
}

/** Two app log lines: one under step 2's tool, one under step 0's
 * model call. */
export const UL_LOGS = {
  logs: [
    {
      index: 0,
      time: rOK.started,
      severity: "INFO",
      severity_number: 9,
      body: "step 0 model call",
      attrs: {},
      span_id: "u_c0",
    },
    {
      index: 1,
      time: rOK.started,
      severity: "WARN",
      severity_number: 13,
      body: "mw.log: budget 300/250 exceeded",
      attrs: {},
      span_id: "u_t2",
    },
  ],
}
