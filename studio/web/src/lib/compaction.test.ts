// originalOf (plan A9.2): a view's replaced range resolved by seq —
// the position in the growth records below the view's index,
// concatenated in index order — across multi-message batches and
// several views; an unreadable batch below the view is a gap, never a
// shifted range.
import { describe, expect, it } from "vitest"

import { asTranscript } from "./api"
import type { Message, RunCompaction } from "./api"
import { compactionLine, originalOf } from "./compaction"

const msg = (role: Message["role"], text: string): Message => ({ role, content: [{ type: "text", text }] })
const texts = (ms: Message[]) => ms.map((m) => (m.content[0] as { text: string }).text)
const view = (index: number, from: number, to: number, entries = 1): RunCompaction => ({
  scope: "run",
  index,
  step: index,
  from_seq: from,
  to_seq: to,
  hash: "h",
  replaced: to - from,
  entries,
})

// Growth: 0 = the input [h0, h1, u1], 1 = a1, 2 = t1, 4 = a2, 6 = a
// steer; views at 3 ([3,5): a1, t1) and 5 ([2,6): u1 … a2).
const transcript = asTranscript({
  batches: [
    { index: 0, step: 0, input: true, messages: [msg("user", "h0"), msg("assistant", "h1"), msg("user", "u1")] },
    { index: 1, step: 0, messages: [msg("assistant", "a1")] },
    { index: 2, step: 0, messages: [msg("tool", "t1")] },
    { index: 4, step: 1, messages: [msg("assistant", "a2")] },
    { index: 6, step: 1, messages: [msg("user", "steer")] },
  ],
})
const v1 = view(3, 3, 5)
const v2 = view(5, 2, 6)

describe("originalOf", () => {
  it("places each view's range by seq over multi-message batches and earlier views", () => {
    const o1 = originalOf(v1, transcript, [v1, v2])
    const o2 = originalOf(v2, transcript, [v1, v2])
    if (!("messages" in o1) || !("messages" in o2)) throw new Error(`unresolved: ${JSON.stringify([o1, o2])}`)
    expect(texts(o1.messages)).toEqual(["a1", "t1"])
    expect(texts(o2.messages)).toEqual(["u1", "a1", "t1", "a2"])
  })

  it("an unreadable body below the view is a gap, not a shifted range", () => {
    const t = asTranscript({
      batches: [
        { index: 0, step: 0, input: true, messages: "not json" },
        { index: 1, step: 0, messages: [msg("assistant", "a1")] },
        { index: 2, step: 0, messages: [msg("tool", "t1")] },
      ],
    })
    expect(t.batches[0].unreadable).toBe(true)
    const o = originalOf(view(3, 1, 3), t, [view(3, 1, 3)])
    expect("gap" in o && o.gap).toContain("messages record 0 before the view does not read as messages")
  })

  it("a body that lost a non-message entry is unreadable too", () => {
    const t = asTranscript({ batches: [{ index: 0, step: 0, messages: [null, msg("user", "u")] }] })
    expect(t.batches[0].unreadable).toBe(true)
    expect("gap" in originalOf(view(1, 0, 1), t, [])).toBe(true)
  })

  it("an insertion replaces nothing and says inserted", () => {
    const ins = view(3, 2, 2, 2)
    const o = originalOf(ins, transcript, [ins])
    expect("messages" in o && o.messages).toEqual([])
    expect(compactionLine(ins)).toBe("2 messages inserted by PrepareStep")
  })
})
