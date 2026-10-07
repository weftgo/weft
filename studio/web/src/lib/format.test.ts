import { describe, expect, it } from "vitest"

import {
  absoluteTime,
  duration,
  elapsed,
  relativeTime,
  spanMs,
  tokens,
  usageSummary,
  zoneLabel,
} from "./format"

// A fixed "now" so the tests never flake: 2026-09-28T12:00:00Z.
const NOW = Date.parse("2026-09-28T12:00:00Z")

describe("relativeTime", () => {
  it("renders recent, minutes, hours, days, and dates", () => {
    expect(relativeTime(new Date(NOW - 5_000).toISOString(), NOW)).toBe(
      "just now"
    )
    expect(relativeTime(new Date(NOW - 4 * 60_000).toISOString(), NOW)).toBe(
      "4m ago"
    )
    expect(relativeTime(new Date(NOW - 2 * 3600_000).toISOString(), NOW)).toBe(
      "2h ago"
    )
    expect(relativeTime(new Date(NOW - 3 * 86400_000).toISOString(), NOW)).toBe(
      "3d ago"
    )
    expect(relativeTime("2026-08-20T12:00:00Z", NOW)).toBe("Aug 20")
  })

  it("degrades on garbage", () => {
    expect(relativeTime("not-a-date", NOW)).toBe("—")
  })
})

describe("absoluteTime and duration", () => {
  it("formats a local stamp", () => {
    expect(absoluteTime("2026-09-28T09:00:12Z")).toMatch(
      /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} UTC/
    )
  })

  it("computes run durations", () => {
    expect(duration("2026-09-28T09:00:00Z", "2026-09-28T09:00:01.4Z")).toBe(
      "1.4s"
    )
    expect(duration("2026-09-28T09:00:00Z", "2026-09-28T09:04:03Z")).toBe(
      "4m03s"
    )
    expect(duration("2026-09-28T09:00:00Z", "2026-09-28T09:00:00.012Z")).toBe(
      "12ms"
    )
    expect(duration("2026-09-28T09:00:00Z", "2026-09-28T11:05:00Z")).toBe(
      "2h05m"
    )
    expect(duration("2026-09-28T09:00:00Z", null)).toBe("—")
    expect(elapsed("2026-09-28T09:00:00Z", NOW)).toBe("3h00m")
  })
})

describe("tokens", () => {
  it("stays compact above a thousand", () => {
    expect(tokens(42)).toBe("42")
    expect(tokens(12400)).toBe("12.4k")
  })
})

// A local wall-clock stamp with no zone reads as UTC to anyone
// comparing it with a server log — the stamp names its offset.
describe("absoluteTime's zone label", () => {
  it("labels the stamp with the viewer's UTC offset", () => {
    expect(absoluteTime("2026-09-28T09:00:12Z")).toMatch(
      /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} UTC([+-]\d{1,2}(:\d{2})?)?$/
    )
    expect(zoneLabel(0)).toBe("UTC")
    expect(zoneLabel(-120)).toBe("UTC+2")
    expect(zoneLabel(330)).toBe("UTC-5:30")
    expect(zoneLabel(-345)).toBe("UTC+5:45")
  })
})

describe("formatting edges", () => {
  it("never renders NaN, undefined or a 60-second remainder", () => {
    expect(spanMs(Number.NaN)).toBe("—")
    expect(spanMs(299_700)).toBe("5m00s") // was "4m60s"
    expect(spanMs(59_960)).toBe("1m00s") // was "60.0s"
    expect(tokens(undefined as unknown as number)).toBe("—")
    expect(tokens(1_234_567)).toBe("1.2M")
    expect(tokens(999_999)).toBe("1.0M") // was "1000.0k"
    expect(
      usageSummary({} as unknown as { input_tokens: number; output_tokens: number })
    ).toBe("0 in / 0 out")
  })
})
