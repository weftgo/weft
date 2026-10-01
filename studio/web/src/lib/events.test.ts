// Event folding tests (plan §4.4 / §7): the golden fixtures recorded
// by the Go API tests plus hand-written edge cases, and the replay
// prefix property — folding events[0,k) never throws and agrees with
// the full fold on everything it has seen.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"

import type { EventsPage, WireEvent } from "./api"
import {
  applyTranscript,
  callState,
  fold,
  foldMore,
  linkView,
  newFold,
  truncation,
} from "./events"

function golden(name: string): WireEvent[] {
  const path = resolve(process.cwd(), `../testdata/api/${name}`)
  const page = JSON.parse(readFileSync(path, "utf8")) as EventsPage
  return page.events.map((pe) => pe.event)
}

const okEvents = golden("events-ok.golden.json")
const subEvents = golden("events-sub.golden.json")

describe("fold on the goldens", () => {
  it("folds the success-with-tool-call run", () => {
    const run = fold(okEvents)
    expect(run.runId).toBe("r_ok")
    expect(run.agent).toBe("orders")
    expect(run.finished).toBe(true)
    // The stored stream is durable events only: one step, its tool
    // call closed, no deltas (the finished text comes from the
    // transcript — see applyTranscript below).
    expect(run.steps).toHaveLength(1)

    const [first] = run.steps
    expect(first.toolCalls).toHaveLength(1)
    const call = first.toolCalls[0]
    expect(call.name).toBe("lookup_order")
    expect(call.state).toBe("done")
    expect(call.result).toEqual({
      content: "order 42: shipped",
      isError: false,
    })
    expect((call.args as { order_id: string }).order_id).toBe("42")
    expect(first.finish?.reason).toBe("end_turn")
    expect(first.text).toBe("")
    expect(run.usage?.input_tokens).toBe(10)
  })

  it("folds the subagent run; the child joins by parent_call_id (S4.3)", () => {
    const feed = newFold()
    foldMore(feed, subEvents)
    // The child is its own run: the parent's stream has no nested
    // envelopes, and the link is data (parent_call_id), not events.
    const view = linkView(feed.result(), [
      { id: "r_sub/0/call_3", parent_call_id: "call_3" },
      { id: "r_other", parent_call_id: "call_none" }, // links nothing
    ])
    const call = view.steps[0].toolCalls[0]
    expect(call.name).toBe("research")
    expect(call.childRunId).toBe("r_sub/0/call_3")
    expect(call.result!.content).toBe("order 42 shipped this morning")
    // An unlinked call stays unlinked.
    const none = view.steps.flatMap((s) => s.toolCalls).find(
      (c) => c.callId === "call_none"
    )
    expect(none).toBeUndefined()
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

  it("applies the transcript over a history-loaded run (S4.3)", () => {
    // A run loaded from history has no deltas: the events fold with
    // empty text, and the messages records supply the final words.
    const events: WireEvent[] = [
      { type: "run_start", id: "r_t", model: { provider: "p", name: "m" } },
      { type: "step_start", run_id: "r_t", index: 0 },
      {
        type: "tool_start",
        run_id: "r_t",
        seq: 1,
        call_id: "c1",
        name: "lookup",
      },
      {
        type: "tool_finish",
        run_id: "r_t",
        seq: 1,
        call_id: "c1",
        name: "lookup",
        content: "found",
        is_error: false,
      },
      {
        type: "step_finish",
        run_id: "r_t",
        index: 0,
        reason: "tool_calls",
        usage: { input_tokens: 1, output_tokens: 1 },
      },
      { type: "step_start", run_id: "r_t", index: 1 },
      {
        type: "step_finish",
        run_id: "r_t",
        index: 1,
        reason: "end_turn",
        usage: { input_tokens: 1, output_tokens: 2 },
      },
      {
        type: "run_finish",
        run_id: "r_t",
        usage: { input_tokens: 2, output_tokens: 3 },
        steps: 2,
      },
    ]
    const view = applyTranscript(fold(events), [
      {
        messages: [{ role: "user", content: [{ type: "text", text: "hi" }] }],
      },
      {
        messages: [
          {
            role: "assistant",
            content: [
              { type: "tool_call", id: "c1", name: "lookup", args: { q: 1 } },
            ],
          },
        ],
      },
      {
        messages: [
          {
            role: "assistant",
            content: [{ type: "text", text: "the answer" }],
          },
        ],
      },
    ])
    expect(view.steps[0].toolCalls[0].args).toEqual({ q: 1 })
    expect(view.steps[1].text).toBe("the answer")
  })

  it("applyTranscript never overwrites streamed text (live wins)", () => {
    const events: WireEvent[] = [
      { type: "run_start", id: "r_l", model: { provider: "p", name: "m" } },
      { type: "text_delta", run_id: "r_l", text: "streamed " },
      { type: "text_delta", run_id: "r_l", text: "words" },
    ]
    const view = applyTranscript(fold(events), [
      {
        messages: [
          {
            role: "assistant",
            content: [{ type: "text", text: "different words" }],
          },
        ],
      },
    ])
    expect(view.steps[0].text).toBe("streamed words")
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

describe("steered events fold as user turns (ADR 0019)", () => {
  const steered: WireEvent[] = [
    { type: "run_start", id: "r_s", model: { provider: "p", name: "m" } },
    { type: "step_start", run_id: "r_s", index: 0 },
    { type: "text_delta", run_id: "r_s", text: "checking" },
    {
      type: "step_finish",
      run_id: "r_s",
      index: 0,
      reason: "stop",
      usage: { input_tokens: 1, output_tokens: 1 },
    },
    {
      type: "steered",
      run_id: "r_s",
      seq: 1,
      step: 0,
      messages: [
        {
          role: "user",
          content: [
            { type: "text", text: "wait — " },
            { type: "text", text: "metric units" },
          ],
        },
      ],
    },
    { type: "step_start", run_id: "r_s", index: 1 },
    { type: "text_delta", run_id: "r_s", text: "done in metres" },
    {
      type: "step_finish",
      run_id: "r_s",
      index: 1,
      reason: "stop",
      usage: { input_tokens: 1, output_tokens: 1 },
    },
    {
      type: "run_finish",
      run_id: "r_s",
      usage: { input_tokens: 2, output_tokens: 2 },
      steps: 2,
    },
  ]

  it("attaches the delivered turn to the step it followed", () => {
    const run = fold(steered)
    expect(run.steps).toHaveLength(2)
    expect(run.steps[0].steer).toEqual({
      text: "wait — metric units",
      pos: 4,
    })
    expect(run.steps[1].steer).toBeUndefined()
    // The step's event range spans through the steer.
    expect(run.steps[0].to).toBe(4)
  })

  it("keeps the prefix property across the steer", () => {
    for (let k = 0; k <= steered.length; k++) {
      const prefix = fold(steered, k)
      const full = fold(steered)
      for (const s of prefix.steps) {
        const match = full.steps.find((x) => x.index === s.index)
        expect(match).toBeDefined()
        // A steer the prefix has seen matches the full fold; before
        // the event it is simply absent (that is the prefix property).
        if (s.steer) expect(s.steer).toEqual(match?.steer)
      }
    }
  })

  it("folds a steered event with null messages as an empty turn", () => {
    const run = fold([
      ...steered.slice(0, 4),
      { type: "steered", run_id: "r_s", seq: 1, step: 0, messages: null },
    ])
    expect(run.steps[0].steer).toEqual({ text: "", pos: 4 })
  })
})
