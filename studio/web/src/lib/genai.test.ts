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

  it("traceSelectionLink writes sel before view, and nothing for no selection", () => {
    expect(traceSelectionLink("T", { sel: "c:1:call_1", view: "chat" })).toEqual({
      to: "/traces/$id",
      params: { id: "T" },
      search: { sel: "c:1:call_1", view: "chat" },
    })
    expect(traceSelectionLink("T")).toEqual({ to: "/traces/$id", params: { id: "T" }, search: {} })
  })
})
