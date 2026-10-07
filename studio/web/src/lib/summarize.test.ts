// Event summaries and the gutter's bucketing rule.
import { describe, expect, it } from "vitest"

import type { WireEvent } from "./api"
import {
  bytes,
  dominantKind,
  eventKind,
  eventSummary,
  eventType,
  isBoundary,
} from "./summarize"

const start: WireEvent = {
  type: "tool_start",
  run_id: "r",
  seq: 1,
  call_id: "c1",
  name: "lookup_order",
  args: { order_id: "42" },
}
const finishErr: WireEvent = {
  type: "tool_finish",
  run_id: "r",
  seq: 2,
  call_id: "c1",
  name: "lookup_order",
  content: "ORDER_NOT_FOUND: order 42 does not exist",
  is_error: true,
}
const delta: WireEvent = { type: "text_delta", run_id: "r", text: "hi there" }
describe("summarize", () => {
  it("classifies kinds and boundaries", () => {
    expect(eventKind(start)).toBe("tool")
    expect(eventKind(finishErr)).toBe("error")
    expect(eventKind(delta)).toBe("delta")
    expect(isBoundary(start)).toBe(true)
    expect(isBoundary(delta)).toBe(false)
  })
  it("names the event's type as the wire spells it", () => {
    expect(eventType(start)).toBe("tool_start")
    expect(eventType(delta)).toBe("text_delta")
  })
  it("summarizes in one line", () => {
    expect(eventSummary(start)).toBe('lookup_order({"order_id":"42"})')
    expect(eventSummary(finishErr)).toMatch(/^lookup_order → error · 40 B · /)
    expect(eventSummary(delta)).toBe("“hi there”")
    expect(eventSummary({ ...delta, text: "x".repeat(200) }, 20)).toHaveLength(
      22
    ) // 19 chars + … in quotes
  })
  it("picks the loudest kind for a bucket", () => {
    expect(dominantKind(["delta", "delta", "tool"])).toBe("tool")
    expect(dominantKind(["result", "error"])).toBe("error")
    expect(dominantKind([])).toBe("delta")
  })
  it("formats byte sizes", () => {
    expect(bytes(12)).toBe("12 B")
    expect(bytes(2048)).toBe("2.0 KB")
  })
})

// The raw explorer and the replay bar summarize whatever the stream
// holds — bodies are stored as ingested, so a malformed or unknown
// event must read as a quiet row, never throw (one bad record would
// otherwise take the whole run page down).
describe("summaries of malformed and unknown events", () => {
  const bad = (v: unknown) => v as WireEvent

  it("never throws on a non-object body", () => {
    for (const v of [null, "text", 7, [], {}]) {
      expect(() => eventKind(bad(v))).not.toThrow()
      expect(() => eventSummary(bad(v))).not.toThrow()
      expect(() => isBoundary(bad(v))).not.toThrow()
      expect(typeof eventType(bad(v))).toBe("string")
      expect(typeof eventSummary(bad(v))).toBe("string")
    }
  })

  it("names an unknown type and shows its JSON", () => {
    const ev = bad({ type: "from_the_future", run_id: "r", n: 1 })
    expect(eventType(ev)).toBe("from_the_future")
    expect(eventKind(ev)).toBe("step")
    expect(eventSummary(ev)).toContain('"n":1')
  })

  it("summarizes known types with missing fields", () => {
    expect(() =>
      eventSummary(bad({ type: "run_start", id: "r" }))
    ).not.toThrow()
    expect(() =>
      eventSummary(bad({ type: "step_finish", run_id: "r", index: 0 }))
    ).not.toThrow()
    expect(() =>
      eventSummary(bad({ type: "tool_finish", run_id: "r", name: "t" }))
    ).not.toThrow()
    expect(() =>
      eventSummary(
        bad({ type: "steered", step: 0, messages: [{ role: "user", content: null }] })
      )
    ).not.toThrow()
  })
})
