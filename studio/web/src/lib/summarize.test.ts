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
