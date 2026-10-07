// Event folding tests (plan §4.4 / §7): the golden fixtures recorded
// by the Go API tests plus hand-written edge cases, and the replay
// prefix property — folding events[0,k) never throws and agrees with
// the full fold on everything it has seen.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"

import type { EventsPage, Message, WireEvent } from "./api"
import {
  applyTranscript,
  callState,
  fold,
  foldMore,
  linkView,
  newFold,
  placeBatches,
  producedText,
  runHoles,
  splitTranscript,
  stepHoles,
  truncation,
  turnPrompt,
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
    expect(first.finish?.reason).toBe("stop")
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
        reason: "stop",
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

// The input record (ADR 0024 D1): a run's first messages record is
// everything it was FED — for turn 2+ of a thread that is the whole
// conversation so far, earlier assistant replies included (the real
// shape: batch 0 = [user, assistant, user], then one message per
// record). The overlay must map only the run's OWN assistant messages
// onto its steps; before this, step 0 of every later turn showed the
// previous turn's reply.
describe("applyTranscript and the input record", () => {
  const user = (text: string): Message => ({
    role: "user",
    content: [{ type: "text", text }],
  })
  const assistant = (text: string): Message => ({
    role: "assistant",
    content: [{ type: "text", text }],
  })
  const oneStep: WireEvent[] = [
    { type: "run_start", id: "s_1-t2", model: { provider: "p", name: "m" } },
    { type: "step_start", run_id: "s_1-t2", index: 0 },
    {
      type: "step_finish",
      run_id: "s_1-t2",
      index: 0,
      reason: "stop",
      usage: { input_tokens: 1, output_tokens: 1 },
    },
    {
      type: "run_finish",
      run_id: "s_1-t2",
      usage: { input_tokens: 1, output_tokens: 1 },
      steps: 1,
    },
  ]
  // Turn 2 of a session, as the local sink stores it.
  const turn2 = [
    { messages: [user("hi"), assistant("turn one's reply"), user("and now?")] },
    { messages: [assistant("turn two's reply")] },
  ]

  it("overlays the run's own reply, never the history it was fed", () => {
    const view = applyTranscript(fold(oneStep), turn2)
    expect(view.steps[0].text).toBe("turn two's reply")
  })

  it("splits the transcript into what was fed and what was produced", () => {
    const { input, produced } = splitTranscript(turn2)
    expect(input.map((m) => m.role)).toEqual(["user", "assistant", "user"])
    expect(produced.map((m) => m.role)).toEqual(["assistant"])
    expect(turnPrompt(turn2)).toBe("and now?")
    expect(producedText(turn2)).toBe("turn two's reply")
  })

  it("treats a lone assistant first batch as produced (a run with no input)", () => {
    const batches = [{ messages: [assistant("unprompted")] }]
    expect(splitTranscript(batches).input).toEqual([])
    expect(applyTranscript(fold(oneStep), batches).steps[0].text).toBe(
      "unprompted"
    )
  })

  it("trusts the server's input flag over the shape when it is present", () => {
    const batches = [
      { input: false, messages: [user("steered in"), user("twice")] },
      { messages: [assistant("ok")] },
    ]
    expect(splitTranscript(batches).input).toEqual([])
    const flagged = [
      { input: true, messages: [assistant("a fed-back reply")] },
      { messages: [assistant("the new one")] },
    ]
    expect(splitTranscript(flagged).produced).toEqual([assistant("the new one")])
  })

  it("survives malformed bodies (anything OTLP ingest stored)", () => {
    const junk = [
      { messages: "a bare string" },
      { messages: null },
      { messages: [null, 7, { role: "assistant" }, { role: "assistant", content: null }] },
      { messages: [assistant("still here")] },
    ] as unknown as { messages: Message[] }[]
    expect(() => applyTranscript(fold(oneStep), junk)).not.toThrow()
    expect(() => turnPrompt(junk)).not.toThrow()
    expect(producedText(junk)).toBe("still here")
  })
})

// Event bodies are stored as ingested (obsdb does not validate them),
// so the fold meets whatever reached POST /v1/logs: a null body, a
// bare string, an object with no type, a type from a newer core, a
// content-stripped delta. None of them may throw or corrupt the view.
describe("fold on malformed and unknown events", () => {
  const base: WireEvent[] = [
    { type: "run_start", id: "r_m", model: { provider: "p", name: "m" } },
    { type: "step_start", run_id: "r_m", index: 0 },
  ]
  const bad = (v: unknown) => v as WireEvent

  it("skips non-object and untyped events without losing position", () => {
    const run = fold([
      ...base,
      bad(null),
      bad("a string body"),
      bad(42),
      bad({}),
      bad({ type: "from_the_future", run_id: "r_m" }),
      bad({ type: "nested", run_id: "r_m", seq: 1, call_id: "c", event: {} }),
      {
        type: "tool_start",
        run_id: "r_m",
        seq: 1,
        call_id: "c1",
        name: "lookup",
        args: {},
      },
    ])
    expect(run.steps).toHaveLength(1)
    // Positions stay the stream's own: the call opened at index 8.
    expect(run.steps[0].toolCalls[0].startPos).toBe(8)
  })

  it("never renders 'undefined' for a delta without text", () => {
    const run = fold([
      ...base,
      bad({ type: "text_delta", run_id: "r_m" }),
      bad({ type: "reasoning_delta", run_id: "r_m" }),
      { type: "text_delta", run_id: "r_m", text: "ok" },
    ])
    expect(run.steps[0].text).toBe("ok")
    expect(run.steps[0].reasoning).toBe("")
  })

  it("defaults a step_finish without usage and a steer without content", () => {
    const run = fold([
      ...base,
      bad({ type: "step_finish", run_id: "r_m", index: 0, reason: "stop" }),
      bad({
        type: "steered",
        run_id: "r_m",
        seq: 1,
        step: 0,
        messages: [{ role: "user", content: null }, null],
      }),
      bad({ type: "run_finish", run_id: "r_m", steps: 1, pending: "nope" }),
    ])
    expect(run.steps[0].finish?.usage).toEqual({
      input_tokens: 0,
      output_tokens: 0,
    })
    expect(run.steps[0].steer?.text).toBe("")
    expect(run.pending).toEqual([])
    expect(run.finished).toBe(true)
  })
})

// ADR 0028 §8: each batch carries the step it joined, as the core
// stamped it — the steered batch the step that just finished, a resumed
// run's rebuilt tool message step 0. The client places batches by that
// value, never by counting assistant messages; only a batch with no
// stored step (-1, badge not_recorded) is placed by inference, and that
// placement is marked derived.
describe("transcript placement by the stored step", () => {
  const msg = (role: Message["role"], text: string): Message => ({
    role,
    content: [{ type: "text", text }],
  })
  const call = (id: string, name: string): Message => ({
    role: "assistant",
    content: [{ type: "tool_call", id, name, args: { order_id: "42" } }],
  })
  const result = (id: string, name: string): Message => ({
    role: "tool",
    content: [
      {
        type: "tool_result",
        call_id: id,
        name,
        content: "ok",
        is_error: false,
      },
    ],
  })
  const twoSteps = (run: string, calls: [string, string][]): WireEvent[] => [
    { type: "run_start", id: run, model: { provider: "p", name: "m" } },
    ...calls.flatMap(([id, name], index): WireEvent[] => [
      { type: "step_start", run_id: run, index },
      ...(id
        ? ([
            {
              type: "tool_start",
              run_id: run,
              seq: index + 1,
              call_id: id,
              name,
              args: null,
            },
          ] as WireEvent[])
        : []),
      {
        type: "step_finish",
        run_id: run,
        index,
        reason: "stop",
        usage: { input_tokens: 1, output_tokens: 1 },
      },
    ]),
  ]

  // Run 1: step 0 looks the order up, a steer arrives after it, step 1
  // asks for the refund, which parks.
  const steered = [
    { index: 0, step: 0, input: true, messages: [msg("user", "where is 42?")] },
    {
      index: 1,
      step: 0,
      input: false,
      messages: [call("c_lookup", "lookup_order")],
    },
    {
      index: 2,
      step: 0,
      input: false,
      messages: [result("c_lookup", "lookup_order")],
    },
    {
      index: 3,
      step: 0,
      input: false,
      messages: [msg("user", "and refund it")],
    },
    {
      index: 4,
      step: 1,
      input: false,
      messages: [
        {
          role: "assistant",
          content: [
            { type: "text", text: "refunding" },
            ...call("c_refund", "refund").content,
          ],
        } as Message,
      ],
    },
  ]
  // Run 2 resumes it: the approved call's result joins at step 0, then
  // step 0 calls again and step 1 answers. Batch 3 (step 0's result) is
  // lost in transit: the stored steps still place step 1's answer.
  const resumed = [
    {
      index: 0,
      step: 0,
      input: true,
      messages: [msg("user", "where is 42?"), call("c_refund", "refund")],
    },
    {
      index: 1,
      step: 0,
      input: false,
      messages: [result("c_refund", "refund")],
    },
    {
      index: 2,
      step: 0,
      input: false,
      messages: [call("c_again", "lookup_order")],
    },
    {
      index: 4,
      step: 1,
      input: false,
      messages: [msg("assistant", "Refunded order 42.")],
    },
  ]

  it("places every batch under the step it joined, steer and resume included", () => {
    expect(
      placeBatches(steered).map((b) => [b.step, b.input, b.derived])
    ).toEqual([
      [0, true, false],
      [0, false, false],
      [0, false, false],
      [0, false, false],
      [1, false, false],
    ])
    const view = applyTranscript(
      fold(
        twoSteps("r1", [
          ["c_lookup", "lookup_order"],
          ["c_refund", "refund"],
        ])
      ),
      steered
    )
    expect(view.steps[0].toolCalls[0].args).toEqual({ order_id: "42" })
    expect(view.steps[1].text).toBe("refunding")
    expect(view.steps[1].toolCalls[0].args).toEqual({ order_id: "42" })
    expect(view.steps.some((s) => s.derived)).toBe(false)

    const placed = placeBatches(resumed)
    expect(placed.map((b) => [b.step, b.input, b.derived])).toEqual([
      [0, true, false],
      [0, false, false],
      [0, false, false],
      [1, false, false],
    ])
    const again = applyTranscript(
      fold(
        twoSteps("r2", [
          ["c_again", "lookup_order"],
          ["", ""],
        ])
      ),
      resumed
    )
    expect(again.steps[0].text).toBe("")
    expect(again.steps[0].toolCalls[0].args).toEqual({ order_id: "42" })
    expect(again.steps[1].text).toBe("Refunded order 42.")
    expect(again.steps.some((s) => s.derived)).toBe(false)
  })

  it("believes the stored step over the batches' order", () => {
    // Step 0's assistant batch is missing: an order walk would put
    // step 1's words on step 0.
    const view = applyTranscript(
      fold(
        twoSteps("r3", [
          ["", ""],
          ["", ""],
        ])
      ),
      [
        { step: 0, input: true, messages: [msg("user", "hi")] },
        { step: 1, input: false, messages: [msg("assistant", "step one")] },
      ]
    )
    expect(view.steps[0].text).toBe("")
    expect(view.steps[1].text).toBe("step one")
  })

  it("places a step -1 batch by inference and marks it derived", () => {
    const old = [
      {
        step: -1,
        badge: "not_recorded",
        input: true,
        messages: [msg("user", "hi")],
      },
      {
        step: -1,
        badge: "not_recorded",
        input: false,
        messages: [msg("assistant", "a0")],
      },
      {
        step: -1,
        badge: "not_recorded",
        input: false,
        messages: [msg("user", "steer")],
      },
      {
        step: -1,
        badge: "not_recorded",
        input: false,
        messages: [msg("assistant", "a1")],
      },
    ]
    expect(placeBatches(old).map((b) => [b.step, b.input, b.derived])).toEqual([
      [0, true, true],
      [0, false, true],
      [0, false, true],
      [1, false, true],
    ])
    const view = applyTranscript(
      fold(
        twoSteps("r4", [
          ["", ""],
          ["", ""],
        ])
      ),
      old
    )
    expect(view.steps.map((s) => [s.text, s.derived])).toEqual([
      ["a0", true],
      ["a1", true],
    ])
  })

  it("keeps a batch whose step the fold does not hold on view.unplaced", () => {
    const view = applyTranscript(fold(twoSteps("r5", [["", ""]])), [
      { step: 0, input: true, messages: [msg("user", "hi")] },
      { step: 0, input: false, messages: [msg("assistant", "a0")] },
      { step: 3, input: false, messages: [msg("assistant", "from a lost step")] },
    ])
    expect(view.steps[0].text).toBe("a0")
    expect(view.unplaced).toEqual([
      {
        step: 3,
        input: false,
        derived: false,
        messages: [msg("assistant", "from a lost step")],
      },
    ])
  })

  it("places a row whose input the backend inferred as derived", () => {
    const placed = placeBatches([
      { step: 0, input: true, badge: "derived", messages: [msg("user", "q")] },
      { step: 0, input: false, messages: [msg("assistant", "a")] },
    ])
    expect(placed.map((b) => [b.step, b.input, b.derived])).toEqual([
      [0, true, true],
      [0, false, false],
    ])
  })
})

// The recorder's own cuts (plan A3, ADR 0028 §11): the events route's
// attrs, as Go writes them (events-capped / events-stripped goldens),
// fold into badges on the event, its step and call, and the run.
describe("content attrs → badges", () => {
  function page(name: string): EventsPage {
    return JSON.parse(
      readFileSync(resolve(process.cwd(), `../testdata/api/${name}`), "utf8")
    ) as EventsPage
  }

  it("a capped tool result is truncated on the event, the call, the step and the run", () => {
    const p = page("events-capped.golden.json")
    const view = fold(
      p.events.map((pe) => pe.event),
      undefined,
      p.events.map((pe) => pe.attrs)
    )
    const finish = p.events.find((pe) => pe.event.type === "tool_finish")!
    const cut = finish.attrs?.["weft.content.truncated_bytes"] ?? 0
    expect(cut).toBeGreaterThan(0)
    expect(view.eventHoles?.[finish.pos]).toEqual([{ hole: "truncated", bytes: cut }])
    const step = view.steps[0]
    expect(step.toolCalls[0].holes).toEqual([{ hole: "truncated", bytes: cut }])
    expect(step.holes?.[0].hole).toBe("truncated")
    expect(view.holes?.map((h) => h.hole)).toEqual(["truncated"])
    // The run's bytes are every event's, summed.
    const total = p.events.reduce(
      (n, pe) => n + (pe.attrs?.["weft.content.truncated_bytes"] ?? 0),
      0
    )
    expect(view.holes?.[0].bytes).toBe(total)
    // run_start was not cut: no badge of its own.
    expect(view.eventHoles?.[0]).toBeUndefined()
  })

  it("a content-off run is stripped on every event, every step and the run, once", () => {
    const p = page("events-stripped.golden.json")
    const view = fold(
      p.events.map((pe) => pe.event),
      undefined,
      p.events.map((pe) => pe.attrs)
    )
    expect(Object.keys(view.eventHoles ?? {}).length).toBe(p.events.length)
    for (const s of view.steps)
      expect(s.holes).toEqual([{ hole: "stripped" }])
    expect(view.holes).toEqual([{ hole: "stripped" }])
    expect(runHoles({ holes: [{ hole: "stripped", reason: "doc's" }] }, view)).toEqual([
      { hole: "stripped", reason: "doc's" },
    ])
  })

  it("a prefix fold keeps the badges it has seen (replay)", () => {
    const p = page("events-capped.golden.json")
    const evs = p.events.map((pe) => pe.event)
    const attrs = p.events.map((pe) => pe.attrs)
    const finish = p.events.findIndex((pe) => pe.event.type === "tool_finish")
    expect(fold(evs, finish, attrs).holes).toBeUndefined()
    expect(fold(evs, finish + 1, attrs).holes?.[0].hole).toBe("truncated")
    // foldMore carries them the same way.
    const feed = newFold()
    foldMore(feed, evs, attrs)
    expect(feed.result().holes).toEqual(fold(evs, undefined, attrs).holes)
  })

  it("a step card's holes: its own, a derived placement, max_tokens, and the run's that hold for every step", () => {
    const view = fold([
      { type: "run_start", id: "r", model: { provider: "p", name: "m" } },
      { type: "step_start", run_id: "r", index: 0 },
      {
        type: "step_finish",
        run_id: "r",
        index: 0,
        reason: "max_tokens",
        usage: { input_tokens: 1, output_tokens: 1 },
      },
    ] as WireEvent[])
    const step = { ...view.steps[0], derived: true }
    const run = [
      { hole: "not_recorded" },
      { hole: "interrupted" },
      { hole: "gap", reason: "run-level" },
    ]
    expect(stepHoles(step, run).map((h) => h.hole)).toEqual([
      "max_tokens",
      "not_recorded",
      "derived",
    ])
    // With the step route's holes cached: the union — its holes (its
    // words first), the fold's and the run's.
    expect(
      stepHoles(step, run, {
        holes: [
          { hole: "compacted", reason: "r" },
          { hole: "not_recorded", reason: "step's" },
        ],
      })
    ).toEqual([
      { hole: "max_tokens" },
      { hole: "not_recorded", reason: "step's" },
      {
        hole: "derived",
        reason:
          "this step's words come from a transcript batch whose step was inferred, not stored",
      },
      { hole: "compacted", reason: "r" },
    ])
  })
})

// A10: a resumed run's step 0 may hold two calls with one id — the
// approved call resumed before the loop's first model call (its child
// <run>/resume/<id>) and the model's own call (<run>/0/<id>). Each
// child links to its own call, whichever order the run document lists
// them in, and no call is linked twice; each finish closes its own call.
describe("linkView on a repeated call id inside a resumed step", () => {
  const R = "r_res"
  const U = { input_tokens: 1, output_tokens: 1 }
  const events = [
    { type: "run_start", id: R, model: { provider: "p", name: "m" } },
    { type: "tool_start", run_id: R, seq: 1, call_id: "c1", name: "research", args: {} },
    { type: "tool_finish", run_id: R, seq: 1, call_id: "c1", name: "research", content: "resumed", is_error: false },
    { type: "step_start", run_id: R, index: 0 },
    { type: "tool_start", run_id: R, seq: 2, call_id: "c1", name: "research", args: {} },
    { type: "tool_finish", run_id: R, seq: 2, call_id: "c1", name: "research", content: "own", is_error: false },
    { type: "step_finish", run_id: R, index: 0, reason: "tool_calls", usage: U },
  ] as WireEvent[]
  const resumed = { id: `${R}/resume/c1`, parent_run_id: R, parent_call_id: "c1" }
  const own = { id: `${R}/0/c1`, parent_run_id: R, parent_call_id: "c1" }
  for (const [label, kids] of [
    ["resumed child listed first", [resumed, own]],
    ["named child listed first", [own, resumed]],
  ] as const) {
    it(`each child links to its own call (${label})`, () => {
      const view = linkView(fold(events), [...kids])
      const calls = view.steps[0].toolCalls
      expect(calls.map((c) => [c.result?.content, c.resumed ?? false, c.childRunId])).toEqual([
        ["resumed", true, resumed.id],
        ["own", false, own.id],
      ])
    })
  }
  it("links each child once: a second pass changes nothing", () => {
    const view = linkView(linkView(fold(events), [own, resumed]), [resumed, own])
    expect(view.steps[0].toolCalls.map((c) => c.childRunId)).toEqual([resumed.id, own.id])
  })
})
