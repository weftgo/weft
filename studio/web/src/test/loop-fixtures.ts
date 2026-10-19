// The loop graph's runs (U34; WEFT-LOOP-GRAPH.md §6): the arena's
// rw.flaky-infra, rw.budget-guards (the poller and the worker),
// rw.support-desk (the parked run and its resume), rw.tool-storm and
// rw.deep-delegation, mirrored from ../weft-arena/internal/suites/
// realworld.go in the shapes Studio serves them: the ?tree=1 spans
// (every span with its weft.run.id and run_id; execute_tool's
// attributes per docs/record-contract.md §3) and the root's events
// (tool_start with its arguments — what the fold matches a repeated
// call on). A synthetic builder, so the 1,000-step perf run is the
// same shape.
import type { Span, WireEvent } from "@/lib/api"
import { fold } from "@/lib/events"
import type { FoldedRun } from "@/lib/events"
import { span } from "./director-fixture"

export interface FxCall {
  name: string
  id?: string
  args?: unknown
  ms?: number
  /** error.type (an error result). */
  error?: string
  content?: string
  /** Parked for approval: weft.tool.pending, no tool_start. */
  pending?: boolean
  /** A call the resume approved (weft.tool.approved, before step 0). */
  approved?: boolean
  child?: FxRun
}
export interface FxStep {
  calls: FxCall[]
  finish?: string
  text?: string
}
export interface FxRun {
  id: string
  agent: string
  steps: FxStep[]
  stoppedBy?: string
  failed?: boolean
  /** Approved calls run before step 0 (a resume). */
  resumed?: FxCall[]
}

export interface Built {
  spans: Span[]
  events: WireEvent[]
  fold: FoldedRun
  pending: { type: "tool_call"; id: string; name: string; args?: unknown }[]
}

/** build lays a run out on a clock (ms): each step a chat span then its
 * calls side by side, a child run inside its call's span. */
export function build(run: FxRun): Built {
  const spans: Span[] = []
  const events: Record<string, unknown>[] = []
  const pending: Built["pending"] = []
  let sid = 0
  const id = (p: string) => `${p}${++sid}`
  const lay = (
    r: FxRun,
    t0: number,
    parentSpan: string,
    parentRun?: string,
    root = true
  ): number => {
    const inv = id("inv")
    const own: Span[] = []
    let t = t0 + 0.1
    let seq = 0
    const ev = (e: Record<string, unknown>) => {
      if (root) events.push({ run_id: r.id, ...e })
    }
    if (root)
      events.push({
        type: "run_start",
        id: r.id,
        agent: r.agent,
        model: { provider: "wefttest", name: "script" },
      })
    const call = (c: FxCall, step: number, start: number, resumed: boolean) => {
      const cid = c.id ?? id("call_")
      c.id = cid
      const ms = c.ms ?? 1
      const sp = id("t")
      if (c.pending) {
        pending.push({ type: "tool_call", id: cid, name: c.name, args: c.args })
        own.push(
          withRun(
            span(sp, inv, `execute_tool ${c.name}`, start, start, {
              "gen_ai.operation.name": "execute_tool",
              "gen_ai.tool.name": c.name,
              "gen_ai.tool.call.id": cid,
              "weft.run.id": r.id,
              "weft.step.index": step,
              "weft.tool.pending": true,
              "weft.tool.pending.source": "approval",
            }),
            r.id
          )
        )
        return start
      }
      seq++
      ev({ type: "tool_start", seq, call_id: cid, name: c.name, args: c.args })
      let end = start + ms
      if (c.child) {
        const childEnd = lay(c.child, start + 0.05, sp, r.id, false)
        end = Math.max(end, childEnd + 0.05)
      }
      own.push(
        withRun(
          span(
            sp,
            inv,
            `execute_tool ${c.name}`,
            start,
            end,
            {
              "gen_ai.operation.name": "execute_tool",
              "gen_ai.tool.name": c.name,
              "gen_ai.tool.call.id": cid,
              "weft.run.id": r.id,
              "weft.step.index": step,
              "weft.tool.seq": seq,
              ...(resumed ? { "weft.tool.approved": true } : {}),
              ...(c.error
                ? { "error.type": c.error }
                : { "weft.tool.result_bytes": (c.content ?? "ok").length }),
            },
            c.error ? "error" : "ok"
          ),
          r.id
        )
      )
      ev({
        type: "tool_finish",
        seq,
        call_id: cid,
        name: c.name,
        content: c.content ?? (c.error ? `${c.error}: failed` : "ok"),
        is_error: !!c.error,
      })
      return end
    }
    for (const c of r.resumed ?? []) t = Math.max(t, call(c, 0, t, true))
    r.steps.forEach((st, n) => {
      ev({ type: "step_start", index: n })
      const finish = st.finish ?? (st.calls.length ? "tool_calls" : "stop")
      own.push(
        withRun(
          span(id("c"), inv, "chat script", t, t + 0.5, {
            "gen_ai.operation.name": "chat",
            "gen_ai.request.model": "script",
            "gen_ai.response.model": "script",
            "gen_ai.response.finish_reasons": [finish],
            "weft.model.tool_calls": st.calls.length,
            "weft.run.id": r.id,
            "weft.step.index": n,
          }),
          r.id
        )
      )
      if (st.text) ev({ type: "text_delta", text: st.text })
      let end = t + 0.5
      for (const c of st.calls) end = Math.max(end, call(c, n, t + 0.6, false))
      ev({
        type: "step_finish",
        index: n,
        reason: finish,
        usage: { input_tokens: 10, output_tokens: 5 },
      })
      t = end + 0.1
    })
    const steps = r.steps.length
    ev({
      type: "run_finish",
      usage: { input_tokens: 10 * steps, output_tokens: 5 * steps },
      steps,
      ...(pending.length && root ? { pending } : {}),
      ...(r.stoppedBy ? { stopped_by: r.stoppedBy } : {}),
    })
    spans.push(
      withRun(
        span(
          inv,
          parentSpan,
          `invoke_agent ${r.agent}`,
          t0,
          t,
          {
            "gen_ai.operation.name": "invoke_agent",
            "gen_ai.agent.name": r.agent,
            "gen_ai.request.model": "script",
            "weft.run.id": r.id,
            "weft.run.steps": steps,
            "gen_ai.usage.input_tokens": 10 * steps,
            "gen_ai.usage.output_tokens": 5 * steps,
            ...(r.stoppedBy ? { "weft.run.stopped_by": r.stoppedBy } : {}),
            ...(parentRun
              ? {
                  "weft.parent.run.id": parentRun,
                  "weft.parent.call.id": r.id.split("/").at(-1),
                }
              : {}),
          },
          r.failed ? "error" : "ok"
        ),
        r.id
      ),
      ...own
    )
    return t
  }
  lay(run, 0, "")
  if (run.failed) events.pop() // a failed run's stream simply ends
  const wire = events as unknown as WireEvent[]
  return { spans, events: wire, fold: fold(wire), pending }
}

const withRun = (s: Span, run: string): Span => ({ ...s, run_id: run })

// ── The arena's runs ──────────────────────────────────────────────

export const FLAKY: FxRun = {
  id: "r_flaky",
  agent: "ops-probe",
  stoppedBy: "final_answer",
  steps: [
    {
      calls: [
        { name: "inventory_api", args: { sku: "a-17" }, error: "RETRY" },
        {
          name: "shipping_quote",
          args: { city: "Lima" },
          ms: 300,
          error: "timeout",
        },
        { name: "legacy_billing", args: { account: 9 }, error: "panic" },
        { name: "tax_service", args: { region: "EU" }, error: "RATE_LIMITED" },
      ],
    },
    { calls: [{ name: "inventory_api", args: { sku: "A-17" } }] },
    { calls: [], text: "Inventory: 42 units of A-17." },
  ],
}

export const POLLER: FxRun = {
  id: "r_poller",
  agent: "poller",
  stoppedBy: "loop_detected",
  failed: true,
  steps: [0, 1, 2].map(() => ({
    calls: [{ name: "poll_job", args: { job_id: "J-7" } }],
  })),
}

export const WORKER: FxRun = {
  id: "r_worker",
  agent: "worker",
  stoppedBy: "max_steps",
  failed: true,
  steps: [1, 2].map((n) => ({ calls: [{ name: "record_step", args: { n } }] })),
}

export const DESK_PARKED: FxRun = {
  id: "r_desk1",
  agent: "support-desk",
  stoppedBy: "parked",
  steps: [
    { calls: [{ name: "lookup_order", args: { order_id: "1002" } }] },
    {
      calls: [
        {
          name: "issue_refund",
          id: "rf1",
          args: { order_id: "1002", amount: 249.9 },
          pending: true,
        },
      ],
    },
  ],
}

export const DESK_RESUME: FxRun = {
  id: "r_desk2",
  agent: "support-desk",
  stoppedBy: "has_tool_call:submit_output",
  resumed: [{ name: "issue_refund", id: "rf1", args: { order_id: "1002" } }],
  steps: [
    {
      calls: [{ name: "submit_output", args: { action: "refunded" } }],
    },
  ],
}

const CITIES = ["Paris", "Tokyo", "Lima", "Cairo", "Oslo", "Sydney"]
export const STORM: FxRun = {
  id: "r_storm",
  agent: "weather-desk",
  stoppedBy: "final_answer",
  steps: [
    {
      calls: [
        ...CITIES.map((city) => ({
          name: "get_weather",
          args: { city },
          ms: 150,
        })),
        { name: "ledger_append", args: { entry: "weather sweep" }, ms: 50 },
        { name: "forecast_report", args: { city: "Oslo" } },
      ],
    },
    { calls: [], text: "Paris 18°C, Tokyo 24°C…" },
  ],
}

export const DIRECTOR: FxRun = {
  id: "r_dir",
  agent: "director",
  stoppedBy: "final_answer",
  steps: [
    {
      calls: [
        {
          name: "ops_manager",
          id: "call_1",
          args: { prompt: "How many units?" },
          child: {
            id: "r_dir/0/call_1",
            agent: "ops-manager",
            stoppedBy: "final_answer",
            steps: [
              {
                calls: [
                  {
                    name: "inventory_specialist",
                    id: "call_1",
                    child: {
                      id: "r_dir/0/call_1/0/call_1",
                      agent: "inventory-specialist",
                      stoppedBy: "final_answer",
                      steps: [
                        {
                          calls: [
                            {
                              name: "inventory_count",
                              args: { sku: "A-17", warehouse: "Lyon" },
                            },
                          ],
                        },
                        { calls: [], text: "42 units." },
                      ],
                    },
                  },
                ],
              },
              { calls: [], text: "The specialist reports 42 units." },
            ],
          },
        },
      ],
    },
    { calls: [], text: "We have 42 units of A-17 in Lyon." },
  ],
}

/** A synthetic run of `n` steps, each calling one of four tools (the
 * perf case: the model aggregates, nodes are tools). */
export function longRun(n: number): FxRun {
  const tools = ["search", "fetch", "parse", "store"]
  return {
    id: "r_long",
    agent: "long",
    stoppedBy: "max_steps",
    failed: true,
    steps: Array.from({ length: n }, (_, i) => ({
      calls: [
        {
          name: tools[i % 4],
          args: { i },
          ...(i % 7 === 0 ? { error: "tool_error" } : {}),
        },
      ],
    })),
  }
}
