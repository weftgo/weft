// The Studio playground's pure halves (step 8b review fixes 4a/4b):
// the transcript-edit fields a continued run renders (4b — the panel's
// per-step drawer fields, derived here from the source transcript),
// and §5.1's wire shape they post. The thread select (4a) rides the
// same PlaygroundRunBody the panel posts — pinned on the panel side.
import { describe, expect, it } from "vitest"

import { editFieldsOf, wireEdits } from "./playground"
import type { Message } from "@/lib/api"

const batch = (step: number, ...messages: Message[]) => ({ step, messages })
const user = (text: string): Message => ({ role: "user", content: [{ type: "text", text }] })
const assistantText = (text: string): Message => ({
  role: "assistant",
  content: [{ type: "text", text }],
})
const assistantCall = (id: string, name: string): Message => ({
  role: "assistant",
  content: [{ type: "tool_call", id, name, args: {} }],
})
const toolResult = (callID: string, name: string, content: string): Message => ({
  role: "tool",
  content: [{ type: "tool_result", call_id: callID, name, content, is_error: false }],
})

describe("editFieldsOf", () => {
  it("derives the kept steps' tool results and call-free replies", () => {
    const batches = [
      batch(
        0,
        user("refund order #4411 please"),
        assistantCall("c1", "lookup_order"),
        toolResult("c1", "lookup_order", "shipped")
      ),
      batch(1, assistantCall("c2", "refund"), toolResult("c2", "refund", "refunded")),
      batch(2, assistantText("Refunded — anything else?")),
    ]
    // from_step 2 keeps steps 0..1: both steps' results are patchable;
    // no reply is (step 2's reply is the fresh step, not kept).
    expect(editFieldsOf(batches, 2)).toEqual([
      { step: 0, callID: "c1", name: "lookup_order", placeholder: "shipped" },
      { step: 1, callID: "c2", name: "refund", placeholder: "refunded" },
    ])
    // from_step 3 keeps the reply too: step 2's assistant message is
    // call-free, so its text is rewritable (a reply rewrite may not
    // drop a step's calls — a call-carrying reply never renders).
    expect(editFieldsOf(batches, 3)).toEqual([
      { step: 0, callID: "c1", name: "lookup_order", placeholder: "shipped" },
      { step: 1, callID: "c2", name: "refund", placeholder: "refunded" },
      { step: 2, name: "reply", placeholder: "Refunded — anything else?" },
    ])
  })

  it("keeps nothing when the whole turn re-runs (from_step 0)", () => {
    const batches = [batch(0, user("hi"), assistantText("hello"))]
    expect(editFieldsOf(batches, 0)).toEqual([])
  })
})

describe("wireEdits", () => {
  it("maps drafts to §5.1's flattened shape — the panel's buildRunBody mapping", () => {
    expect(
      wireEdits([
        { step: 1, callID: "c2", toolResult: "429 Too Many Requests" },
        { step: 0, content: "a rewritten reply" },
      ])
    ).toEqual([
      { step: 1, call_id: "c2", tool_result: "429 Too Many Requests" },
      { step: 0, content: "a rewritten reply" },
    ])
  })
})
