import { describe, expect, it } from "vitest"

import { absoluteTime, duration, relativeTime, tokens } from "./format"

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
      /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/
    )
  })

  it("computes run durations", () => {
    expect(duration("2026-09-28T09:00:00Z", "2026-09-28T09:00:01.4Z")).toBe(
      "1.4s"
    )
    expect(duration("2026-09-28T09:00:00Z", "2026-09-28T09:04:03Z")).toBe(
      "4m03s"
    )
    expect(duration("2026-09-28T09:00:00Z", null)).toBe("—")
  })
})

describe("tokens", () => {
  it("stays compact above a thousand", () => {
    expect(tokens(42)).toBe("42")
    expect(tokens(12400)).toBe("12.4k")
  })
})
