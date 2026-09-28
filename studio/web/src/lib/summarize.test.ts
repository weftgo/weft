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
  unwrap,
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
const nested: WireEvent = {
  type: "nested",
  run_id: "r",
  seq: 3,
  call_id: "c1",
  event: {
    type: "nested",
    run_id: "r/0/c1",
    seq: 1,
    call_id: "c2",
    event: start,
  },
}

describe("summarize", () => {
  it("classifies kinds and boundaries", () => {
    expect(eventKind(start)).toBe("tool")
    expect(eventKind(finishErr)).toBe("error")
    expect(eventKind(delta)).toBe("delta")
    expect(isBoundary(start)).toBe(true)
    expect(isBoundary(delta)).toBe(false)
  })
  it("unwraps nested envelopes to the inner event with a depth", () => {
    expect(unwrap(nested)).toEqual({ inner: start, depth: 2 })
    expect(eventType(nested)).toBe("tool_start")
    expect(eventKind(nested)).toBe("tool")
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
