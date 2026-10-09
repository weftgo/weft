// The "replay from here" fixture (plan F1, test-only; never imported by
// main.ts): the run routes/run-replay.test.tsx drives Studio's half
// with — four steps, step 2's refund call errored, a subagent child on
// step 1 — served to the panel's fake Studio, so both surfaces are
// tested on one shape.
import type { AgentView } from "./client"
import { golden } from "../test/fake-studio"
import { baseRoutes, META, page, runRow, T0, transcript } from "./testkit"
import type { Route } from "./testkit"

export const RUN = "s_01-t1"
export const CHILD = `${RUN}/1/c2`
export const ERR = "REFUND_FAILED: card declined"
export const AS_CALLED = "You are a support agent (as called)."
const usage = { input_tokens: 10, output_tokens: 4 }

export const childRow = runRow({
  id: CHILD,
  parent_run_id: RUN,
  parent_call_id: "c2",
  agent: "researcher",
  session_id: "",
  steps: 1,
})
export const runRowOf = () => runRow({ id: RUN, steps: 4 })

const call = (index: number, id: string, name: string, content: string, isError = false) => [
  { type: "step_start", run_id: RUN, index },
  { type: "tool_start", run_id: RUN, call_id: id, name, args: { q: id } },
  { type: "tool_finish", run_id: RUN, call_id: id, name, content, is_error: isError },
  { type: "step_finish", run_id: RUN, index, reason: "tool_calls", usage },
]

export const events = [
  { type: "run_start", id: RUN, model: { provider: "wefttest", name: "script" }, agent: "acme-support" },
  ...call(0, "c1", "lookup_order", "shipped"),
  ...call(1, "c2", "search_kb", "policy: refunds in 5 days"),
  ...call(2, "c3", "refund", ERR, true),
  { type: "step_start", run_id: RUN, index: 3 },
  { type: "step_finish", run_id: RUN, index: 3, reason: "stop", usage },
  { type: "run_finish", run_id: RUN, usage, steps: 4 },
]

const assistantCall = (id: string, name: string) => [
  { role: "assistant", content: [{ type: "tool_call", id, name, args: { q: id } }] },
]
const result = (id: string, name: string, content: string, isError = false) => [
  { role: "tool", content: [{ type: "tool_result", call_id: id, name, content, is_error: isError }] },
]
export const bodies = [
  [{ role: "user", content: [{ type: "text", text: "refund order 4411" }] }],
  assistantCall("c1", "lookup_order"),
  result("c1", "lookup_order", "shipped"),
  assistantCall("c2", "search_kb"),
  result("c2", "search_kb", "policy: refunds in 5 days"),
  assistantCall("c3", "refund"),
  result("c3", "refund", ERR, true),
  [{ role: "assistant", content: [{ type: "text", text: "Sorry, the card was declined." }] }],
]

const tool = (name: string, replay: string) => ({
  name,
  description: name,
  schema: { type: "object" },
  timeout_ms: 0,
  approval: false,
  replay,
  max_result_bytes: 65536,
  sequential: false,
  source: "local",
})

/** One request row per step: the catalog the step was offered (its
 * replay classes) and the prompt it was called with. tools overrides
 * the catalog (a hole ref, say). */
export function requestRow(step: number, tools: unknown = { hash: "t", tools: [tool("lookup_order", "never"), tool("refund", ""), tool("search_kb", "safe")], content: "", truncated_bytes: 0 }) {
  return {
    index: step,
    step,
    attempt: 1,
    time: T0,
    system_hash: "p",
    catalog_hash: "t",
    content: "",
    truncated_bytes: 0,
    body: {
      step,
      attempt: 1,
      system_hash: "p",
      messages_ref: { index: 0, count: 1 },
      tools: { catalog_hash: "t", names: ["lookup_order", "refund", "search_kb"] },
      sequential_tools: false,
      params: {},
      model: { provider: "wefttest", name: "script" },
      stream: true,
    },
    prompt: { hash: "p", text: AS_CALLED, content: "", truncated_bytes: 0 },
    tools,
  }
}

export const agentView: AgentView = {
  name: "acme-support",
  models: [],
  instructions: "You are a support agent.",
  tools: [
    { name: "lookup_order", side_effects: "never", allow: false },
    { name: "refund", side_effects: "never", allow: false },
    { name: "search_kb", side_effects: "safe", allow: false },
  ],
}
export const researcher: AgentView = {
  name: "researcher",
  models: [],
  instructions: "You research orders.",
  tools: [{ name: "search_kb", side_effects: "safe", allow: false }],
}

export const runtimeOf = (agents: AgentView[] = [agentView]) => ({
  id: "rt_1",
  host: "dev-box",
  pid: 42,
  service: "app",
  env: "dev",
  connected_since: T0,
  last_seen: T0,
  agents,
  breakpoints: [],
})

export const REPLAY_META = { ...META, capabilities: [...META.capabilities, "requests"] }

/** The fake Studio's routes for the fixture: the run, its records, its
 * child, the runtime and the command that finishes as pg_new. */
export function replayRoutes(opts: { agents?: AgentView[]; compactions?: unknown[] } = {}): Record<string, Route> {
  const r = baseRoutes()
  const row = runRowOf()
  r["runs?public_id=pub_orders&limit=50"] = { total: 1, runs: [row], next_before: null }
  r[`runs/${RUN}`] = { ...row, children: [childRow], holes: [], compactions: opts.compactions ?? [] }
  r[`runs/${RUN}/events?after=0&limit=500`] = page(events)
  r[`runs/${RUN}/transcript`] = transcript(...bodies)
  r[`runs/${RUN}/requests?limit=1000`] = { requests: [0, 1, 2, 3].map((n) => requestRow(n)) }
  const childEvents = [
    { type: "run_start", id: CHILD, model: { provider: "wefttest", name: "script" }, agent: "researcher" },
    { type: "step_start", run_id: CHILD, index: 0 },
    { type: "tool_start", run_id: CHILD, call_id: "k1", name: "search_kb", args: { q: "k1" } },
    { type: "tool_finish", run_id: CHILD, call_id: "k1", name: "search_kb", content: "found", is_error: false },
    { type: "step_finish", run_id: CHILD, index: 0, reason: "tool_calls", usage },
    { type: "step_start", run_id: CHILD, index: 1 },
    { type: "step_finish", run_id: CHILD, index: 1, reason: "stop", usage },
    { type: "run_finish", run_id: CHILD, usage, steps: 2 },
  ]
  r[`runs/${CHILD}`] = { ...childRow, children: [], holes: [] }
  r[`runs/${CHILD}/events?after=0&limit=500`] = page(childEvents)
  r[`runs/${CHILD}/transcript`] = transcript(
    [{ role: "user", content: [{ type: "text", text: "dig" }] }],
    [{ role: "assistant", content: [{ type: "tool_call", id: "k1", name: "search_kb", args: { q: "k1" } }] }],
    [{ role: "tool", content: [{ type: "tool_result", call_id: "k1", name: "search_kb", content: "found" }] }],
    [{ role: "assistant", content: [{ type: "text", text: "the policy says 5 days" }] }]
  )
  r[`runs/${CHILD}/requests?limit=1000`] = { requests: [] }
  r[`runs/${CHILD}/spans`] = { spans: [] }
  r.runtimes = { runtimes: [runtimeOf(opts.agents)] }
  r["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
  r["playground/commands/cmd_1"] = {
    command_id: "cmd_1",
    state: "finished",
    status: "succeeded",
    run_id: "pg_new",
    error: null,
    created: T0,
    updated: T0,
  }
  const pg = runRow({ id: "pg_new", playground: true, session_id: "", forked_from: `${RUN}#3`, steps: 1 })
  r["runs/pg_new"] = { ...pg, children: [] }
  r["runs/pg_new/events?after=0&limit=500"] = page([
    { type: "run_start", id: "pg_new", model: { provider: "wefttest", name: "script" }, agent: "acme-support" },
    { type: "step_start", run_id: "pg_new", index: 3 },
    { type: "step_finish", run_id: "pg_new", index: 3, reason: "stop", usage },
    { type: "run_finish", run_id: "pg_new", usage, steps: 1 },
  ])
  r["runs/pg_new/transcript"] = transcript([{ role: "user", content: [{ type: "text", text: "refund order 4411" }] }], [
    { role: "assistant", content: [{ type: "text", text: "Refunded." }] },
  ])
  return r
}

/** The transcript cut after its first n batches (5: steps 0–1, each
 * call answered): the fold's later steps hold no reply. */
export const transcriptCut = (n: number) => transcript(...bodies.slice(0, n))

// ── The transcript editor's fixture (plan F2) ─────────────────────

/** lookup_order's recorded input schema: q a string, nothing else. */
export const ARGS_SCHEMA = { type: "object", properties: { q: { type: "string" } }, required: ["q"], additionalProperties: false }

/** The steps' catalog with lookup_order's schema recorded. */
export const editCatalog = {
  hash: "t",
  content: "",
  truncated_bytes: 0,
  tools: [{ ...tool("lookup_order", "never"), schema: ARGS_SCHEMA }, tool("refund", ""), tool("search_kb", "safe")],
}

/** The command the Done line's edits make on either surface, in order:
 * step 0's prompt, c1's args, c2's result, a message before step 2. */
export const DONE_EDITS = [
  { kind: "user", step: 0, content: "refund order 7" },
  { kind: "tool_args", step: 0, call_id: "c1", args: { q: "c9" } },
  { kind: "tool_result", step: 1, call_id: "c2", tool_result: "policy: no refunds" },
  { kind: "insert", step: 2, content: "and check 43" },
]

/** META with the replay's records and the preview served. */
export const EDIT_META = { ...REPLAY_META, capabilities: [...REPLAY_META.capabilities, "preview"] }

/** The replay fixture's routes with lookup_order's schema in every
 * step's catalog and POST /api/playground/preview answering preview
 * (default: the playground-preview golden). */
export function editRoutes(preview: Route = golden("playground-preview")): Record<string, Route> {
  const r = replayRoutes()
  r[`runs/${RUN}/requests?limit=1000`] = { requests: [0, 1, 2, 3].map((n) => requestRow(n, editCatalog)) }
  r["POST playground/preview"] = preview
  return r
}
