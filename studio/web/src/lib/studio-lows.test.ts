// U33.2's shared readers (the Studio / panel-side lows, batch 2): a
// GenAI log event read as a message (UX-H-10), a replay's provenance in
// words (UX-S5b-04) and an unknown set figure's known part (UX-H-07).
import { describe, expect, it } from "vitest"

import { partialView } from "./cost"
import { editsSummary, replayViewWords } from "./edits"
import { eventMessage } from "./genai"

describe("eventMessage (UX-H-10)", () => {
  it("reads gen_ai.<role>.message bodies as that role's message", () => {
    expect(
      eventMessage("gen_ai.user.message", `{"content":"where is my order?"}`)
    ).toEqual({
      role: "user",
      parts: [{ kind: "text", text: "where is my order?" }],
      side: "input",
    })
    expect(
      eventMessage("gen_ai.system.message", `{"content":"be brief"}`)?.role
    ).toBe("system")
  })

  it("reads an assistant's tool calls and a tool message's id", () => {
    const a = eventMessage(
      "gen_ai.assistant.message",
      JSON.stringify({
        tool_calls: [
          {
            id: "c1",
            type: "function",
            function: { name: "lookup", arguments: `{"q":"x"}` },
          },
        ],
      })
    )
    expect(a?.parts).toEqual([
      { kind: "tool_call", id: "c1", name: "lookup", arguments: { q: "x" } },
    ])
    const t = eventMessage(
      "gen_ai.tool.message",
      `{"content":"shipped","id":"c1"}`
    )
    expect(t).toMatchObject({
      role: "tool",
      parts: [{ kind: "tool_result", id: "c1", response: "shipped" }],
    })
  })

  it("reads gen_ai.choice as the output side, with its finish reason", () => {
    expect(
      eventMessage(
        "gen_ai.choice",
        `{"index":0,"finish_reason":"stop","message":{"content":"on its way"}}`
      )
    ).toEqual({
      role: "assistant",
      parts: [{ kind: "text", text: "on its way" }],
      side: "output",
      finishReason: "stop",
    })
  })

  it("a body that is not JSON is the text; a plain line is no message", () => {
    expect(eventMessage("gen_ai.user.message", "hi there")?.parts).toEqual([
      { kind: "text", text: "hi there" },
    ])
    expect(eventMessage(undefined, `{"content":"x"}`)).toBeUndefined()
    expect(eventMessage("app.started", `{"content":"x"}`)).toBeUndefined()
  })
})

describe("a replay's provenance in words (UX-S5b-04)", () => {
  const row = (edits: string) => ({
    id: "pg_1",
    meta: { "weft.edits": edits },
  })
  it("counts the marks and names each kind once, in order", () => {
    expect(
      editsSummary(row("0:user,0:c1:args,1:c2:result,2:insert,1:insert"))
    ).toBe("5 edits (user, args, result, insert)")
    expect(editsSummary(row("0:reply"))).toBe("1 edit (reply)")
  })
  it("counts what the mark's cap left out", () => {
    expect(editsSummary(row("0:user,+3 more"))).toBe("4 edits (user)")
  })
  it("says nothing without a mark, or on a child run", () => {
    expect(editsSummary({ id: "pg_1", meta: {} })).toBe("")
    expect(
      editsSummary({
        id: "pg_1/0/c1",
        parent_run_id: "pg_1",
        meta: { "weft.edits": "0:user" },
      })
    ).toBe("")
  })
  it("says what the model was fed at the step", () => {
    expect(replayViewWords("compacted:7")).toBe("fed the compacted view #7")
    expect(replayViewWords("derived")).toBe("prefix derived")
    expect(replayViewWords("unknown")).toBe("prefix view unknown")
    expect(replayViewWords("transcript")).toBe("")
    expect(replayViewWords(undefined)).toBe("")
  })
})

describe("partialView (UX-H-07)", () => {
  const reason =
    "1 of 2 turns' cost not known (the stock spans report no (or zero) usage)"
  it("draws the known part as the panel's header does", () => {
    const v = partialView({
      cost_usd: null,
      cost_known: false,
      cost_badge: "unknown",
      cost_reason: reason,
      cost_partial: { usd: 0.0029, priced: 1, of: 2 },
    })
    expect(v).toMatchObject({
      known: false,
      text: "≥ $0.0029 · 1 of 2 turns priced",
      title: reason,
      usd: 0.0029,
    })
    expect(
      partialView(
        {
          cost_known: false,
          cost_reason: reason,
          cost_partial: { usd: 0.00003, priced: 1, of: 2 },
        },
        "run"
      )?.text
    ).toBe("> $0 · 1 of 2 runs priced")
  })
  it("is undefined for a known figure or one with no priced part", () => {
    expect(partialView({ cost_usd: 1, cost_known: true })).toBeUndefined()
    expect(
      partialView({ cost_known: false, cost_reason: reason })
    ).toBeUndefined()
  })
})
