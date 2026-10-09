// The transcript editor's shared half (plan F2): the wire, the edit
// list, the from_step the edits imply, the schema check (obsdb's
// TestCheckToolArgs table, case for case where JSON.parse can say the
// same) and the weft.edits mark read back.
import { describe, expect, it } from "vitest"

import { checkArgs, editLine, editMarks, impliedFromStep, markedPart, putEdit, userMessagesOf, wireEdits } from "./edits"
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
