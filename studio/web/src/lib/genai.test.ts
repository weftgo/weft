// H7: the chat view's reader over both GenAI key families, and the
// trace page's step-qualified selection keys.
import { describe, expect, it } from "vitest"

import type { Span } from "./api"
import { genaiChat, isModelCall } from "./genai"
import { traceSelectionLink } from "./links"
import { spansFromTimed } from "./trace"
import { keyRows, resolveSel, traceRuns } from "./trace-sel"

describe("genaiChat", () => {
  it("reads the current keys: system instructions, input and output, as strings or parsed", () => {
    const c = genaiChat({
      "gen_ai.system_instructions": JSON.stringify([{ type: "text", content: "be brief" }]),
      "gen_ai.input.messages": [
        { role: "user", parts: [{ type: "text", content: "hi" }] },
        { role: "tool", parts: [{ type: "tool_call_response", id: "call_1", response: "shipped" }] },
      ],
      "gen_ai.output.messages": JSON.stringify([
        {
          role: "assistant",
          parts: [{ type: "tool_call", id: "call_2", name: "refund", arguments: '{"id":"42"}' }, { type: "blob", mime_type: "image/png" }],
          finish_reason: "tool_call",
        },
      ]),
      "gen_ai.prompt": "ignored: the current family wins",
    })
    expect(c.family).toBe("semconv")
    expect(c.messages.map((m) => [m.role, m.side])).toEqual([
      ["system", "input"],
      ["user", "input"],
      ["tool", "input"],
      ["assistant", "output"],
    ])
    expect(c.messages[2].parts).toEqual([{ kind: "tool_result", id: "call_1", response: "shipped" }])
    expect(c.messages[3].parts[0]).toEqual({ kind: "tool_call", id: "call_2", name: "refund", arguments: { id: "42" } })
    expect(c.messages[3].parts[1]).toMatchObject({ kind: "other", type: "blob" })
    expect(c.messages[3].finishReason).toBe("tool_call")
    expect(c.problems).toEqual([])
  })

  it("says a value it cannot parse instead of dropping it silently", () => {
    const c = genaiChat({ "gen_ai.input.messages": "{not json" })
    expect(c.family).toBe("semconv")
    expect(c.messages).toEqual([])
    expect(c.problems).toEqual(["gen_ai.input.messages is not a JSON array of messages"])
  })

  it("reads the legacy keys: indexed with roles and tool calls, else plain", () => {
    const indexed = genaiChat({
      "gen_ai.prompt.1.content": "second",
      "gen_ai.prompt.0.role": "system",
      "gen_ai.prompt.0.content": "first",
      "gen_ai.completion.0.content": "done",
      "gen_ai.completion.0.tool_calls.0.id": "t1",
      "gen_ai.completion.0.tool_calls.0.name": "lookup",
      "gen_ai.completion.0.tool_calls.0.arguments": "not json",
    })
    expect(indexed.family).toBe("legacy")
    expect(indexed.messages.map((m) => m.role)).toEqual(["system", "user", "assistant"])
    expect(indexed.messages[2].parts).toEqual([
      { kind: "text", text: "done" },
      { kind: "tool_call", id: "t1", name: "lookup", arguments: "not json" },
    ])
    const plain = genaiChat({ "gen_ai.content.prompt": "q", "gen_ai.completion": "a" })
    expect(plain.messages.map((m) => [m.role, m.parts])).toEqual([
      ["user", [{ kind: "text", text: "q" }]],
      ["assistant", [{ kind: "text", text: "a" }]],
    ])
  })

  it("reads a legacy prompt's history: the assistant's tool calls and the tool result's call id", () => {
    const c = genaiChat({
      "gen_ai.prompt.0.role": "user",
      "gen_ai.prompt.0.content": "where is 42?",
      "gen_ai.prompt.1.role": "assistant",
      "gen_ai.prompt.1.tool_calls.0.id": "call_1",
      "gen_ai.prompt.1.tool_calls.0.name": "lookup_order",
      "gen_ai.prompt.1.tool_calls.0.arguments": '{"order_id":"42"}',
      "gen_ai.prompt.2.role": "tool",
      "gen_ai.prompt.2.tool_call_id": "call_1",
      "gen_ai.prompt.2.content": "shipped",
      "gen_ai.prompt.3.tool_call_id": "call_2",
      "gen_ai.prompt.3.content": "no role given",
      "gen_ai.completion.0.content": "it shipped",
    })
    expect(c.family).toBe("legacy")
    expect(c.messages.map((m) => [m.role, m.side])).toEqual([
      ["user", "input"],
      ["assistant", "input"],
      ["tool", "input"],
      ["tool", "input"],
      ["assistant", "output"],
    ])
    expect(c.messages[1].parts).toEqual([{ kind: "tool_call", id: "call_1", name: "lookup_order", arguments: { order_id: "42" } }])
    expect(c.messages[2].parts).toEqual([{ kind: "tool_result", id: "call_1", response: "shipped" }])
    expect(c.messages[3].parts).toEqual([{ kind: "tool_result", id: "call_2", response: "no role given" }])
    expect(c.keys).toContain("gen_ai.prompt.1.tool_calls.0.name")
    expect(c.keys).toContain("gen_ai.prompt.2.tool_call_id")
  })

  it("carries none: family null, nothing reconstructed", () => {
    expect(genaiChat({ "gen_ai.operation.name": "chat", "weft.run.id": "r" })).toEqual({
      family: null,
      messages: [],
      keys: [],
      problems: [],
    })
    expect(isModelCall({ "gen_ai.operation.name": "chat" })).toBe(true)
    expect(isModelCall({ "gen_ai.operation.name": "execute_tool" })).toBe(false)
  })
})

function sp(id: string, attrs: Record<string, unknown>, parent = ""): Span {
  return {
    trace_id: "t",
    span_id: id,
    parent_span_id: parent,
    name: id,
    kind: "internal",
    start: "2026-10-01T09:00:00.000Z",
    end: "2026-10-01T09:00:01.000Z",
    status: "ok",
    status_message: "",
    service: "",
    attrs,
    events: [],
  }
}

describe("the trace page's selection keys", () => {
  it("a second span wanting a taken key keeps its span id key; resolveSel reads keys, span ids and old call keys", () => {
    const spans = [
      sp("a", { "gen_ai.operation.name": "invoke_agent", "weft.run.id": "r" }),
      sp("b", { "gen_ai.operation.name": "chat", "weft.run.id": "r", "weft.step.index": 0 }, "a"),
      sp("b2", { "gen_ai.operation.name": "chat", "weft.run.id": "r", "weft.step.index": "0" }, "a"),
      sp("c", { "gen_ai.operation.name": "execute_tool", "weft.run.id": "r", "weft.step.index": 0, "gen_ai.tool.call.id": "x:y" }, "a"),
    ]
    const rows = keyRows(spansFromTimed(spans))
    expect(rows.map((r) => r.key)).toEqual(["r:r", "s0", "t:b2", "c:0:x:y"])
    expect(rows.map((r) => r.id)).toEqual(["t:a", "t:b", "t:b2", "t:c"])
    expect(resolveSel(rows, "c:0:x:y")).toEqual({ id: "t:c", key: "c:0:x:y" })
    expect(resolveSel(rows, "t:c")).toEqual({ id: "t:c", key: "c:0:x:y" })
    // A call id holding ":" still reads as an old bare key.
    expect(resolveSel(rows, "c:x:y")).toEqual({ id: "t:c", key: "c:0:x:y" })
    expect(resolveSel(rows, "c:nope")).toEqual({})
    expect(resolveSel(rows, undefined)).toEqual({})
    expect(traceRuns(spans)).toEqual(["r"])
  })

  it("a resumed (approved) call keys c:resume:<call> as the run page does, apart from step 0's call of the same id", () => {
    const tool = (id: string, run: string, approved: boolean) =>
      sp(id, {
        "gen_ai.operation.name": "execute_tool",
        "weft.run.id": run,
        "weft.step.index": 0,
        "gen_ai.tool.call.id": "call_1",
        ...(approved ? { "weft.tool.approved": true } : {}),
      })
    const one = keyRows(spansFromTimed([tool("r1", "r", true), tool("s1", "r", false)]))
    expect(one.map((r) => r.key)).toEqual(["c:resume:call_1", "c:0:call_1"])
    // A run page's key pasted into the trace page lands on the resumed call.
    expect(resolveSel(one, "c:resume:call_1")).toEqual({ id: "t:r1", key: "c:resume:call_1" })
    expect(resolveSel(one, "c:0:call_1")).toEqual({ id: "t:s1", key: "c:0:call_1" })
    // An old bare key names both: the first, ambiguous.
    expect(resolveSel(one, "c:call_1")).toEqual({ id: "t:r1", key: "c:resume:call_1", ambiguous: true })
    const two = keyRows(spansFromTimed([tool("r1", "ra", true), tool("x1", "rb", false)]))
    expect(two.map((r) => r.key)).toEqual(["c:ra:resume:call_1", "c:rb:0:call_1"])
    expect(resolveSel(two, "c:ra:resume:call_1").id).toBe("t:r1")
  })

  it("traceSelectionLink writes sel before view, and nothing for no selection", () => {
    expect(traceSelectionLink("T", { sel: "c:1:call_1", view: "chat" })).toEqual({
      to: "/traces/$id",
      params: { id: "T" },
      search: { sel: "c:1:call_1", view: "chat" },
    })
    expect(traceSelectionLink("T")).toEqual({ to: "/traces/$id", params: { id: "T" }, search: {} })
  })
})
