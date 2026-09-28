// Event folding tests (plan §4.4 / §7): the golden fixtures recorded
// by the Go API tests plus hand-written edge cases, and the replay
// prefix property — folding events[0,k) never throws and agrees with
// the full fold on everything it has seen.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"

import type { EventsPage, ResultDoc, WireEvent } from "./api"
import {
  callState,
  crossCheck,
  fold,
  foldMore,
  newFold,
  truncation,
} from "./events"

function golden(name: string): WireEvent[] {
  const path = resolve(process.cwd(), `../testdata/api/${name}`)
  const page = JSON.parse(readFileSync(path, "utf8")) as EventsPage
  return page.events.map((pe) => pe.event)
}

/** Wrap one event as the wire would: a nested envelope. */
function nest(
  runId: string,
  seq: number,
  callId: string,
  event: WireEvent
): WireEvent {
  return { type: "nested", run_id: runId, seq, call_id: callId, event }
}

const okEvents = golden("events-ok.golden.json")
const subEvents = golden("events-sub.golden.json")

describe("fold on the goldens", () => {
  it("folds the success-with-tool-call run", () => {
    const run = fold(okEvents)
    expect(run.runId).toBe("r_ok")
    expect(run.agent).toBe("orders")
    expect(run.finished).toBe(true)
    expect(run.steps).toHaveLength(2)

    const [first, second] = run.steps
    expect(first.toolCalls).toHaveLength(1)
    const call = first.toolCalls[0]
    expect(call.name).toBe("lookup_order")
    expect(call.state).toBe("done")
    expect(call.result).toEqual({
      content: "order 42: shipped",
      isError: false,
    })
    expect((call.args as { order_id: string }).order_id).toBe("42")
    expect(first.finish?.reason).toBe("tool_calls")
    expect(second.text).toBe("Order 42 shipped this morning.")
    expect(second.finish?.reason).toBe("stop")
    expect(run.usage?.input_tokens).toBe(20)
  })

  it("folds the subagent run with its nested child (B7)", () => {
    const run = fold(subEvents)
    const call = run.steps[0].toolCalls[0]
    expect(call.name).toBe("research")
    const child = call.child
    expect(child).toBeDefined()
    expect(child!.runId).toBe("r_sub/0/call_1")
    expect(child!.agent).toBe("researcher")
    expect(child!.finished).toBe(true)
    expect(child!.steps[0].text).toBe("order 42 shipped this morning")
    expect(call.result!.content).toBe("order 42 shipped this morning")
    expect(run.steps[1].text).toBe("Order 42 shipped.")
  })

  it("cross-checks the fold against the store's result document", () => {
    const run = fold(okEvents)
    // The recorded result of r_ok (run-ok's steps), hand-mirrored from
    // the run document golden's shape.
    const result: ResultDoc = {
      steps: [
        {
          index: 0,
          stop_reason: "tool_calls",
          usage: { input_tokens: 10, output_tokens: 5 },
          tool_calls: [
            { type: "tool_call", id: "call_1", name: "lookup_order", args: {} },
          ],
        },
        {
          index: 1,
          stop_reason: "stop",
          usage: { input_tokens: 10, output_tokens: 5 },
          text: "Order 42 shipped this morning.",
        },
      ],
      usage: { input_tokens: 20, output_tokens: 10 },
    }
    expect(crossCheck(run, result)).toEqual([])
    const altered: ResultDoc = {
      ...result,
      steps: result.steps?.map((s, i) =>
        i === 1 ? { ...s, text: "different" } : s
      ),
    }
    expect(crossCheck(run, altered)).not.toEqual([])
  })
})

describe("hand-written edge cases", () => {
  it("folds an empty stream", () => {
    const run = fold([])
    expect(run.steps).toEqual([])
    expect(run.finished).toBe(false)
    expect(run.runId).toBe("")
  })

  it("folds a failure mid-step: no run_finish, calls stay open", () => {
    const events: WireEvent[] = [
      { type: "run_start", id: "r_f", model: { provider: "p", name: "m" } },
      { type: "step_start", run_id: "r_f", index: 0 },
      { type: "text_delta", run_id: "r_f", text: "partial" },
      {
        type: "tool_start",
        run_id: "r_f",
        seq: 1,
        call_id: "c1",
        name: "t",
        args: {},
      },
    ]
    const run = fold(events)
    expect(run.finished).toBe(false)
    expect(run.usage).toBeUndefined()
    expect(run.steps[0].text).toBe("partial")
    expect(callState(run.steps[0].toolCalls[0], "failed")).toBe("never")
    expect(callState(run.steps[0].toolCalls[0], "running")).toBe("running")
  })

  it("keys interleaved parallel calls by call_id", () => {
    const events: WireEvent[] = [
      { type: "run_start", id: "r_p", model: { provider: "p", name: "m" } },
      { type: "step_start", run_id: "r_p", index: 0 },
      {
        type: "tool_start",
        run_id: "r_p",
        seq: 1,
        call_id: "a",
        name: "slow",
        args: {},
      },
      {
        type: "tool_start",
        run_id: "r_p",
        seq: 2,
        call_id: "b",
        name: "fast",
        args: {},
      },
      {
        type: "tool_finish",
        run_id: "r_p",
        seq: 3,
        call_id: "b",
        name: "fast",
        content: "b-out",
        is_error: false,
      },
      {
        type: "tool_finish",
        run_id: "r_p",
        seq: 4,
        call_id: "a",
        name: "slow",
        content: "a-out",
        is_error: false,
      },
    ]
    const run = fold(events)
    const calls = run.steps[0].toolCalls
    expect(calls.map((c) => c.callId)).toEqual(["a", "b"]) // start order
    expect(calls[0].result!.content).toBe("a-out")
    expect(calls[1].result!.content).toBe("b-out")
  })

  it("accumulates tool_args_delta by name and hands it to the call", () => {
    const events: WireEvent[] = [
      { type: "run_start", id: "r_a", model: { provider: "p", name: "m" } },
      { type: "step_start", run_id: "r_a", index: 0 },
      { type: "tool_args_delta", run_id: "r_a", name: "write", args: '{"pa' },
      {
        type: "tool_args_delta",
        run_id: "r_a",
        name: "write",
        args: 'th":"/tmp"}',
      },
      {
        type: "tool_start",
        run_id: "r_a",
        seq: 1,
        call_id: "c",
        name: "write",
        args: { path: "/tmp" },
      },
    ]
    const run = fold(events)
    expect(run.steps[0].toolCalls[0].streamedArgs).toBe('{"path":"/tmp"}')
  })

  it("folds nested within nested (a grandchild run)", () => {
    // The parent delegates to `outer`; outer's own run calls `gc`
    // (itself a subagent), so gc's events arrive nested INSIDE
    // outer's stream (ADR 0014's late-event rule keeps them there).
    const events: WireEvent[] = [
      { type: "run_start", id: "r_n", model: { provider: "p", name: "m" } },
      { type: "step_start", run_id: "r_n", index: 0 },
      {
        type: "tool_start",
        run_id: "r_n",
        seq: 1,
        call_id: "outer",
        name: "outer",
        args: {},
      },
      nest("r_n", 2, "outer", {
        type: "run_start",
        id: "child",
        model: { provider: "p", name: "m" },
        agent: "outer-agent",
      }),
      nest("r_n", 3, "outer", {
        type: "step_start",
        run_id: "child",
        index: 0,
      }),
      nest("r_n", 4, "outer", {
        type: "tool_start",
        run_id: "child",
        seq: 1,
        call_id: "gc",
        name: "gc",
        args: {},
      }),
      nest(
        "r_n",
        5,
        "outer",
        nest("child", 1, "gc", {
          type: "run_start",
          id: "gc",
          model: { provider: "p", name: "m" },
          agent: "gc-agent",
        })
      ),
      nest(
        "r_n",
        6,
        "outer",
        nest("child", 2, "gc", {
          type: "text_delta",
          run_id: "gc",
          text: "deep",
        })
      ),
      nest("r_n", 7, "outer", {
        type: "tool_finish",
        run_id: "child",
        seq: 2,
        call_id: "gc",
        name: "gc",
        content: "deep",
        is_error: false,
      }),
      nest("r_n", 8, "outer", {
        type: "run_finish",
        run_id: "child",
        usage: { input_tokens: 1, output_tokens: 1 },
        steps: 1,
      }),
      {
        type: "tool_finish",
        run_id: "r_n",
        seq: 9,
        call_id: "outer",
        name: "outer",
        content: "deep",
        is_error: false,
      },
    ]
    const run = fold(events)
    const outer = run.steps[0].toolCalls.find((c) => c.callId === "outer")
    expect(outer!.child!.runId).toBe("child")
    expect(outer!.child!.agent).toBe("outer-agent")
    const gc = outer!.child!.steps[0].toolCalls.find((c) => c.callId === "gc")
    expect(gc!.child!.agent).toBe("gc-agent")
    expect(gc!.child!.steps[0].text).toBe("deep")
  })
})

describe("the replay prefix property", () => {
  const cases: Array<[string, WireEvent[]]> = [
    ["goldens: r_ok", okEvents],
    ["goldens: r_sub (nested)", subEvents],
  ]

  for (const [name, events] of cases) {
    it(`folding every prefix of ${name} never throws and stays consistent`, () => {
      const full = fold(events)
      for (let k = 0; k <= events.length; k++) {
        const part = fold(events, k)
        expect(part.steps.length).toBeLessThanOrEqual(full.steps.length)
        for (const step of part.steps) {
          const fullStep = full.steps.find((s) => s.index === step.index)
          expect(fullStep).toBeDefined()
          // Partial text is a prefix of the full text; finished steps
          // agree exactly.
          expect(fullStep!.text.startsWith(step.text)).toBe(true)
          expect(fullStep!.reasoning.startsWith(step.reasoning)).toBe(true)
          expect(step.toolCalls.length).toBeLessThanOrEqual(
            fullStep!.toolCalls.length
          )
          for (const call of step.toolCalls) {
            const fullCall = fullStep!.toolCalls.find(
              (c) => c.callId === call.callId
            )
            expect(fullCall).toBeDefined()
            if (call.result) {
              expect(fullCall!.result).toEqual(call.result)
            }
          }
        }
        expect(part.finished).toBe(k === events.length ? full.finished : false)
      }
    })
  }
})

describe("incremental folding (foldMore, plan §4.4)", () => {
  const cases: Array<[string, WireEvent[]]> = [
    ["goldens: r_ok", okEvents],
    ["goldens: r_sub (nested)", subEvents],
  ]

  for (const [name, events] of cases) {
    it(`folding ${name} page by page equals the one-shot fold at every split`, () => {
      for (const size of [1, 2, 3, 5]) {
        const feed = newFold()
        const collected: WireEvent[] = []
        for (let i = 0; i < events.length; i += size) {
          const page = events.slice(i, i + size)
          foldMore(feed, page)
          collected.push(...page)
          // The view after each page is exactly the one-shot fold of
          // everything seen so far (toEqual is insertion-order blind —
          // an earlier result() may have attached a key sooner).
          expect(feed.result()).toEqual(fold(collected))
        }
      }
    })
  }

  it("foldMore on an empty feed of empty pages stays empty and usable", () => {
    const feed = foldMore(newFold(), [])
    expect(feed.result().steps).toEqual([])
    foldMore(feed, okEvents)
    expect(feed.result().runId).toBe("r_ok")
  })
})

describe("truncation markers (B9)", () => {
  // The literals below are copied from weft/loop.go — the Go side is
  // the source; if these tests break after a core change, the regexes
  // above must follow.
  it("detects the result cap marker", () => {
    const capped = "…first 65536 bytes…\n…[truncated 130000 bytes]"
    expect(truncation(capped)).toEqual({ kind: "bytes", bytes: 130000 })
  })

  it("detects the fail-truncated call marker", () => {
    const content =
      "tool call lookup_order was not executed: the response hit the output token limit"
    expect(truncation(content)).toEqual({ kind: "call", tool: "lookup_order" })
  })

  it("returns null for complete results", () => {
    expect(truncation("order 42: shipped")).toBeNull()
    expect(truncation("")).toBeNull()
  })
})
