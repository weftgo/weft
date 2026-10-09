// The transcript editor's shared half (plan F2): the wire, the edit
// list, the from_step the edits imply, the schema check (obsdb's
// TestCheckToolArgs table, case for case where JSON.parse can say the
// same) and the weft.edits mark read back.
import { describe, expect, it, vi } from "vitest"

import { checkArgs, editLine, editMarks, impliedFromStep, markedPart, putEdit, schemaOf, userMessagesOf, wireEdits } from "./edits"
import { compactedRefusal } from "./experiment-body"
import type { ReplayEdit } from "./edits"
import type { TranscriptBatch } from "./events"

describe("wireEdits", () => {
  it("sends kind always and each kind's own fields, args an object (studio/edits.go's editKindOf)", () => {
    const list: ReplayEdit[] = [
      { kind: "user", step: 0, content: "where is order 7?" },
      { kind: "tool_args", step: 0, callID: "c1", args: { order_id: "7" } },
      { kind: "tool_result", step: 1, callID: "c2", toolResult: "lost" },
      { kind: "reply", step: 2, content: "ok" },
      { kind: "insert", step: 2, content: "also check 43" },
      { kind: "user", step: 1, index: 1, content: "the second" },
      { step: 3, callID: "c4", toolResult: "pre-F2 shape" },
    ]
    expect(JSON.stringify(wireEdits(list))).toBe(
      JSON.stringify([
        { kind: "user", step: 0, content: "where is order 7?" },
        { kind: "tool_args", step: 0, call_id: "c1", args: { order_id: "7" } },
        { kind: "tool_result", step: 1, call_id: "c2", tool_result: "lost" },
        { kind: "reply", step: 2, content: "ok" },
        { kind: "insert", step: 2, content: "also check 43" },
        { kind: "user", step: 1, content: "the second", index: 1 },
        { kind: "tool_result", step: 3, call_id: "c4", tool_result: "pre-F2 shape" },
      ])
    )
  })

  it("one edit per target: put replaces, empty drops; an args edit and a result edit of one call are two", () => {
    let l: ReplayEdit[] = []
    l = putEdit(l, { kind: "tool_args", step: 0, callID: "c1", args: { a: 1 } })
    l = putEdit(l, { kind: "tool_result", step: 0, callID: "c1", toolResult: "x" })
    l = putEdit(l, { kind: "tool_args", step: 0, callID: "c1", args: { a: 2 } })
    expect(l).toEqual([
      { kind: "tool_args", step: 0, callID: "c1", args: { a: 2 } },
      { kind: "tool_result", step: 0, callID: "c1", toolResult: "x" },
    ])
    expect(putEdit(l, { kind: "tool_args", step: 0, callID: "c1" }, true)).toEqual([l[1]])
    expect(editLine(l[0])).toBe('tool_args · step 0 · c1 → {"a":2}')
    expect(editLine({ kind: "insert", step: 2, content: "hi" })).toBe("insert · before step 2 → hi")
  })

  it("the prefix keeps every edited step: an edit of step N needs from_step N+1, an insert at N from_step N (and 1 at least)", () => {
    expect(impliedFromStep([])).toBe(0)
    expect(impliedFromStep([{ kind: "user", step: 0, content: "x" }])).toBe(1)
    expect(impliedFromStep([{ kind: "insert", step: 0, content: "x" }])).toBe(1)
    expect(impliedFromStep([{ kind: "insert", step: 3, content: "x" }, { kind: "tool_result", step: 1, callID: "c", toolResult: "y" }])).toBe(3)
    expect(impliedFromStep([{ kind: "reply", step: 3, content: "x" }])).toBe(4)
  })
})

describe("checkArgs mirrors obsdb.CheckToolArgs", () => {
  // The schema Go's reflector writes for TestCheckToolArgs' input.
  const schema = {
    type: "object",
    properties: {
      order_id: { type: "string" },
      days: { type: "integer" },
      items: { type: "array", items: { type: "object", properties: { sku: { type: "string" }, qty: { type: "integer" } }, required: ["sku", "qty"], additionalProperties: false } },
      tags: { type: "object", additionalProperties: { type: "string" } },
    },
    required: ["order_id"],
    additionalProperties: false,
  }
  const closed = { type: "object", properties: { mode: { type: "string", enum: ["fast", "slow"] }, n: { type: ["integer", "null"] } }, additionalProperties: false }
  const P = 'INVALID_INPUT: tool "lookup_order": '
  const cases: [unknown, string, string][] = [
    [schema, '{"order_id":"42"}', ""],
    [schema, "{}", `${P}missing required field "order_id"`],
    [schema, '{"order_id":"42","days":"3"}', `${P}field "days": expected integer, got string`],
    [schema, '{"order_id":"42","days":1.5}', `${P}field "days": expected integer, got number 1.5`],
    [schema, '{"order_id":"42","days":3.0}', `${P}field "days": expected integer, got number 3.0`],
    [schema, '{"order_id":"42","days":3e0}', `${P}field "days": expected integer, got number 3e0`],
    [schema, '{"order_id":null,"days":null,"items":null,"tags":null}', ""],
    [schema, '{"order_id":"42","items":[{"sku":null,"qty":1}]}', ""],
    [schema, '{"order_id":"42","items":[{"sku":"a","qty":1},{"sku":"b","qty":"2"}]}', `${P}field "items.1.qty": expected integer, got string`],
    [schema, '{"order_id":"42","tags":{"k":1}}', `${P}field "tags.k": expected string, got number`],
    [schema, '["42"]', `${P}expected object at the top level, got array`],
    [closed, '{"mode":"fast","n":null}', ""],
    [closed, '{"mode":"medium"}', `${P}field "mode": "medium" is not one of the schema's values`],
    [closed, '{"n":"1"}', `${P}field "n": expected integer or null, got string`],
    [closed, '{"x":1}', `${P}unknown field "x": not in the schema`],
    [undefined, '{"anything":[1]}', ""],
    [undefined, '"x"', `${P}expected object at the top level, got string`],
  ]
  for (const [s, args, want] of cases)
    it(`${args} → ${want || "accepted"}`, () => {
      const r = checkArgs("lookup_order", s, args)
      expect(r.error ?? "").toBe(want)
      if (!want) expect(r.args).toEqual(JSON.parse(args))
    })

  it("bad JSON is refused in the loop's prefix (the parser's own words after it)", () => {
    expect(checkArgs("lookup_order", undefined, '{"a":}').error).toMatch(/^INVALID_INPUT: tool "lookup_order": invalid JSON: /)
    expect(checkArgs("lookup_order", schema, '{"order_id":"42"} {}').error).toMatch(/^INVALID_INPUT: tool "lookup_order": invalid JSON: /)
    expect(checkArgs("lookup_order", undefined, "null").error).toBe(`${P}expected object at the top level, got null`)
  })
})

describe("the weft.edits mark", () => {
  it("reads the tokens in source coordinates, the cap's +N more, and nothing on a child run", () => {
    const meta = { "weft.edits": "0:user,0:c1:args,1:c2:result,2:reply,1:user:2,2:insert,+3 more" }
    expect(editMarks({ meta })).toEqual({
      marks: [
        { step: 0, what: "user", index: 0 },
        { step: 0, what: "args", callID: "c1" },
        { step: 1, what: "result", callID: "c2" },
        { step: 2, what: "reply" },
        { step: 1, what: "user", index: 2 },
        { step: 2, what: "insert" },
      ],
      more: 3,
    })
    expect(editMarks({ meta, parent_run_id: "r_1" })).toEqual({ marks: [], more: 0 })
    expect(editMarks({ meta: {} })).toEqual({ marks: [], more: 0 })
  })

  it("maps a call token onto the replay's input by its call id (the kept prefix is positional)", () => {
    const batches: TranscriptBatch[] = [
      {
        step: 0,
        input: true,
        messages: [
          { role: "user", content: [{ type: "text", text: "go" }] },
          { role: "assistant", content: [{ type: "tool_call", id: "c1", name: "lookup_order", args: { order_id: "7" } }] },
          { role: "tool", content: [{ type: "tool_result", call_id: "c1", name: "lookup_order", content: "lost", is_error: false }] },
        ],
      },
    ]
    expect(markedPart(batches, { step: 0, what: "args", callID: "c1" })).toBe('lookup_order({"order_id":"7"})')
    expect(markedPart(batches, { step: 0, what: "result", callID: "c1" })).toBe("lost")
    expect(markedPart(batches, { step: 0, what: "reply" })).toBe("")
  })
})

describe("userMessagesOf counts a step's user messages as studio/edits.go's userSeqs does", () => {
  it("step 0's prompt first (the input's last message), then each step's own user messages", () => {
    const batches: TranscriptBatch[] = [
      { step: 0, input: true, messages: [{ role: "user", content: [{ type: "text", text: "earlier" }] }, { role: "assistant", content: [{ type: "text", text: "a" }] }, { role: "user", content: [{ type: "text", text: "now" }] }] },
      { step: 0, input: false, messages: [{ role: "assistant", content: [{ type: "tool_call", id: "c1", name: "x", args: {} }] }] },
      { step: 0, input: false, messages: [{ role: "tool", content: [{ type: "tool_result", call_id: "c1", name: "x", content: "r", is_error: false }] }, { role: "user", content: [{ type: "text", text: "steer" }] }] },
    ]
    expect(userMessagesOf(batches)).toEqual([
      { step: 0, index: 0, text: "now" },
      { step: 0, index: 1, text: "steer" },
    ])
  })
})

// ── F2.2 review fixes ────────────────────────────────────────────────

describe("checkArgs: numbers the browser carries (review 5)", () => {
  const P = 'INVALID_INPUT: tool "t": '
  const int = { type: "object", properties: { n: { type: "integer" } } }
  const rows: [unknown, string, string][] = [
    // int64's max is not a double: sent from here it would be …808.
    [int, '{"n":9223372036854775807}', `${P}field "n": the number 9223372036854775807 would be sent as 9223372036854775808 (past the exact integers JSON keeps here): write it as a string or a smaller number`],
    [int, '{"n":-9223372036854775808}', ""],
    [int, '{"n":9223372036854775808}', `${P}field "n": expected integer, got number 9223372036854775808`],
    [int, '{"n":1e2}', `${P}field "n": expected integer, got number 1e2`],
    [undefined, '{"n":9007199254740993}', `${P}field "n": the number 9007199254740993 would be sent as 9007199254740992 (past the exact integers JSON keeps here): write it as a string or a smaller number`],
    [undefined, '{"a":{"b":[1e400]}}', `${P}field "a.b.0": the number 1e400 is out of range here: write it as a string`],
    [undefined, '{"n":9007199254740992}', ""],
  ]
  for (const [s, args, want] of rows)
    it(`${args} → ${want || "accepted"}`, () => expect(checkArgs("t", s, args).error ?? "").toBe(want))

  it("an engine without number literals: the integer check is unchecked, never a pass", () => {
    const parse = JSON.parse.bind(JSON)
    const spy = vi.spyOn(JSON, "parse").mockImplementation((t: string, r?: (this: unknown, k: string, v: unknown) => unknown) =>
      parse(t, r ? function (this: unknown, k: string, v: unknown) { return r.call(this, k, v) } : undefined)
    )
    try {
      for (const lit of ["3.0", "1e2"]) {
        const r = checkArgs("t", int, `{"n":${lit}}`)
        expect(r.error).toBeUndefined()
        expect(r.unchecked).toBe(true)
      }
    } finally {
      spy.mockRestore()
    }
    expect(checkArgs("t", int, '{"n":3}').unchecked).toBeUndefined()
  })
})

describe("schemaOf: the run's last catalog per name, as the server checks (review 6)", () => {
  it("a tool whose schema changed between steps is checked against the last one", () => {
    const cat = (type: string) => ({ tools: { tools: [{ name: "lookup_order", schema: { type: "object", properties: { q: { type } } } }] } })
    const steps = new Map([
      [2, { rows: [cat("integer")] }],
      [0, { rows: [cat("string")] }],
      [1, { rows: [{ tools: { tools: [{ name: "other" }] } }] }],
    ])
    const s = schemaOf(steps, "lookup_order")
    expect(s).toEqual(cat("integer").tools.tools[0].schema)
    expect(checkArgs("lookup_order", s, '{"q":"7"}').error).toBe('INVALID_INPUT: tool "lookup_order": field "q": expected integer, got string')
    expect(schemaOf(steps, "nope")).toBeUndefined()
  })
})

describe("the mark's tokens (reviews 7, 8)", () => {
  it("a call id holding ':' keeps it: the first segment is the step, the last the kind", () => {
    expect(editMarks({ meta: { "weft.edits": "3:ns:c1:args,2:a:b:c:result,1:user:2,0:user" } }).marks).toEqual([
      { step: 3, what: "args", callID: "ns:c1" },
      { step: 2, what: "result", callID: "a:b:c" },
      { step: 1, what: "user", index: 2 },
      { step: 0, what: "user", index: 0 },
    ])
  })

  it("a reused call id: the token's step ordinal picks its pair; without from_step, no value rather than the wrong one", () => {
    const call = (q: string) => ({ role: "assistant" as const, content: [{ type: "tool_call" as const, id: "c1", name: "lookup_order", args: { q } }] })
    const res = (c: string) => ({ role: "tool" as const, content: [{ type: "tool_result" as const, call_id: "c1", name: "lookup_order", content: c, is_error: false }] })
    const batches: TranscriptBatch[] = [
      { step: 0, input: true, messages: [{ role: "user", content: [{ type: "text", text: "go" }] }, call("a"), res("first"), call("b"), res("second")] },
    ]
    expect(markedPart(batches, { step: 1, what: "args", callID: "c1" }, 2)).toBe('lookup_order({"q":"b"})')
    expect(markedPart(batches, { step: 0, what: "result", callID: "c1" }, 2)).toBe("first")
    expect(markedPart(batches, { step: 1, what: "result", callID: "c1" }, 2)).toBe("second")
    expect(markedPart(batches, { step: 1, what: "args", callID: "c1" })).toBe("")
  })
})

describe("compactedRefusal: the server's refusal of an edit inside a view (review 10)", () => {
  const b = transcriptOfBodies()
  const note = { from_seq: 1, to_seq: 3, entries: 1 }
  it("an edit of a message in [from_seq, to_seq) is refused in F1.1's words; one outside is not", () => {
    expect(compactedRefusal(b, [{ kind: "tool_result", step: 0, callID: "c1", toolResult: "x" }], 2, note)).toBe(
      'call "c1" of step 0 was compacted away before step 2\'s request (messages [1, 3) replaced by 1): the model never saw it there; edit from an earlier from_step'
    )
    expect(compactedRefusal(b, [{ kind: "tool_args", step: 0, callID: "c1", args: {} }], 2, note)).toMatch(/^call "c1" of step 0 was compacted away/)
    expect(compactedRefusal(b, [{ kind: "tool_result", step: 1, callID: "c2", toolResult: "x" }], 2, note)).toBe("")
    expect(compactedRefusal(b, [{ kind: "user", step: 0, content: "x" }], 2, note)).toBe("")
    expect(compactedRefusal(b, [{ kind: "tool_result", step: 0, callID: "c1", toolResult: "x" }], 2, null)).toBe("")
  })
  it("an insert at a boundary strictly inside the range is refused", () => {
    expect(compactedRefusal(b, [{ kind: "insert", step: 1, content: "x" }], 2, { from_seq: 1, to_seq: 4, entries: 1 })).toBe(
      "the boundary before step 1 was compacted away before step 2's request (messages [1, 4) replaced by 1): the model never saw it there; insert outside the range"
    )
    expect(compactedRefusal(b, [{ kind: "insert", step: 1, content: "x" }], 2, note)).toBe("")
  })
})

function transcriptOfBodies(): TranscriptBatch[] {
  const m = (role: "user" | "assistant" | "tool", content: unknown[]) => ({ role, content }) as never
  return [
    { step: 0, input: true, messages: [m("user", [{ type: "text", text: "go" }])] },
    { step: 0, input: false, messages: [m("assistant", [{ type: "tool_call", id: "c1", name: "x", args: {} }])] },
    { step: 0, input: false, messages: [m("tool", [{ type: "tool_result", call_id: "c1", name: "x", content: "r", is_error: false }])] },
    { step: 1, input: false, messages: [m("assistant", [{ type: "tool_call", id: "c2", name: "x", args: {} }])] },
    { step: 1, input: false, messages: [m("tool", [{ type: "tool_result", call_id: "c2", name: "x", content: "r", is_error: false }])] },
  ]
}
