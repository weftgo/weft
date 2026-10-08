// The Request pane's readings (plan E1.1): the override fingerprint off
// the invoke_agent span, the first step's composition check against
// the instructions hash, the diff baselines and the messages a request
// sent, resolved against the transcript.
import { describe, expect, it } from "vitest"

import type { RequestRow, Span, Transcript } from "./api"
import {
  composedFromInstructions,
  composeSystem,
  EMPTY_SHA256,
  messagesSent,
  overrideOf,
  previousRows,
  sha256Hex,
} from "./request-pane"

function row(over: Partial<RequestRow> & { text?: string; names?: string[] }): RequestRow {
  const { text, names, ...rest } = over
  return {
    index: 0,
    step: 0,
    attempt: 1,
    time: "",
    system_hash: "",
    catalog_hash: "",
    content: "",
    truncated_bytes: 0,
    body: {
      step: 0,
      attempt: 1,
      system_hash: "",
      messages_ref: { index: 0, count: 1 },
      tools: { catalog_hash: "", names: names ?? [] },
      sequential_tools: false,
      params: {},
      model: {},
      stream: false,
    },
    prompt:
      text === undefined ? undefined : { hash: rest.system_hash ?? "", text, content: "", truncated_bytes: 0 },
    ...rest,
  }
}

function span(attrs: Record<string, unknown>): Span {
  return {
    trace_id: "t",
    span_id: "s",
    parent_span_id: "",
    name: "invoke_agent orders",
    kind: "internal",
    start: "",
    end: "",
    status: "ok",
    status_message: "",
    service: "",
    attrs: { "gen_ai.operation.name": "invoke_agent", ...attrs },
    events: [],
  }
}

describe("sha256Hex", () => {
  it("is ADR 0028's text hash", async () => {
    expect(await sha256Hex("")).toBe(EMPTY_SHA256)
    expect(await sha256Hex("You are a support agent.")).toBe(
      "57e8f485cbb5aab3a684a972d8cc3981c066cbf52ef8706d75f2bc0a831c78f7"
    )
  })
})

describe("overrideOf", () => {
  it("reads the run's own invoke_agent span, never a child's", () => {
    const spans = [
      span({ "weft.run.id": "child", "weft.override.hash": "x", "weft.override.instructions": true }),
      span({ "weft.run.id": "r", "weft.override.hash": "h", "weft.override.model": "a/b" }),
    ]
    expect(overrideOf(spans, "r")).toEqual({ hash: "h", instructions: false, fields: ["model"] })
    expect(overrideOf(spans, "child")?.instructions).toBe(true)
  })
  it("is undefined on a plain run", () => {
    expect(overrideOf([span({ "weft.run.id": "r" })], "r")).toBeUndefined()
    expect(overrideOf(undefined, "r")).toBeUndefined()
  })
})

describe("composedFromInstructions", () => {
  const ins = "You are a support agent."
  it("the instructions plus the offered tools' snippets are the loop's composition", async () => {
    const text = composeSystem(ins, ["Look orders up by id.", ""])
    expect(text).toBe("You are a support agent.\n\nLook orders up by id.")
    const r = row({ system_hash: (await sha256Hex(text))!, text, names: ["lookup", "refund"] })
    expect(await composedFromInstructions(r, (await sha256Hex(ins))!, ["Look orders up by id.", ""])).toBe(true)
  })
  it("a rewritten text is not", async () => {
    const text = "You are a support agent. Refunds need a reason."
    const r = row({ system_hash: (await sha256Hex(text))!, text })
    expect(await composedFromInstructions(r, (await sha256Hex(ins))!, [])).toBe(false)
    // No tools offered: no snippets to compose, the manifest unneeded.
    expect(await composedFromInstructions(r, (await sha256Hex(ins))!, undefined)).toBe(false)
  })
  it("cannot decide without the text, or without the snippets of offered tools", async () => {
    const text = "You are a support agent.\n\nsomething"
    const h = (await sha256Hex(ins))!
    expect(
      await composedFromInstructions(row({ system_hash: "abc", names: ["lookup"] }), h, [])
    ).toBeUndefined()
    expect(
      await composedFromInstructions(
        row({ system_hash: (await sha256Hex(text))!, text, names: ["lookup"] }),
        h,
        undefined
      )
    ).toBeUndefined()
  })
  it("equal hashes decide alone, the stripped text not needed", async () => {
    expect(await composedFromInstructions(row({ system_hash: "h" }), "h", undefined)).toBe(true)
    expect(await composedFromInstructions(row({ system_hash: "" }), EMPTY_SHA256, undefined)).toBe(true)
  })
})

describe("previousRows", () => {
  it("names each later step's previous recorded step's last attempt", () => {
    const rows = [
      row({ index: 0, step: 0 }),
      row({ index: 1, step: 1 }),
      row({ index: 2, step: 1, attempt: 2 }),
      row({ index: 3, step: 3 }),
    ]
    const prev = previousRows(rows)
    expect(prev.has(0)).toBe(false)
    expect(prev.get(1)?.index).toBe(0)
    expect(prev.get(3)?.index).toBe(2)
  })
})

describe("messagesSent", () => {
  const m = (role: "user" | "assistant" | "tool", text: string) => ({
    role,
    content: [{ type: "text" as const, text }],
  })
  const transcript: Transcript = {
    batches: [
      { index: 0, step: 0, input: true, messages: [m("user", "a")] },
      { index: 1, step: 0, messages: [m("assistant", "b")] },
      { index: 2, step: 1, messages: [m("tool", "c"), m("user", "d")] },
    ],
  }
  it("is the growth records up to the ref's index: count, bytes, the last three", () => {
    const r = row({})
    r.body.messages_ref = { index: 2, count: 4 }
    const out = messagesSent(r, transcript)
    const all = transcript.batches.flatMap((b) => b.messages)
    expect(out.count).toBe(4)
    expect(out.bytes).toBe(new TextEncoder().encode(JSON.stringify(all)).length)
    expect(out.last).toEqual(all.slice(1))
    expect(out.earlier).toBe(1)
  })
  it("says why only the count is known", () => {
    const r = row({})
    r.body.messages_ref = { count: 4 }
    expect(messagesSent(r, transcript).hole).toBe("no_index")
    r.body.messages_ref = { index: 2, count: 9 }
    expect(messagesSent(r, transcript).hole).toBe("gap")
    r.body.messages_ref = { index: 3, count: 4 }
    expect(messagesSent(r, transcript, [{ scope: "run", index: 3, hash: "", replaced: 1, entries: 1 }]).hole).toBe(
      "compacted"
    )
    expect(messagesSent(r, null).hole).toBe("no_transcript")
  })
})
