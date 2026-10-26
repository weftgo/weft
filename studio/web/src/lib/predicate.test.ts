// The pass predicate (UX plan D8): the language parsed, a cell judged —
// an unknown fact is "unknown", never a pass — and the grid's summary.
import { describe, expect, it } from "vitest"

import {
  DEFAULT_PREDICATE,
  judge,
  parsePredicate,
  passRate,
  summarize,
} from "./predicate"
import type { ColumnTally, Predicate } from "./predicate"

const p = (s: string): Predicate => {
  const r = parsePredicate(s)
  if (!r.ok) throw new Error(r.error)
  return r.pred
}

describe("parsePredicate", () => {
  it("reads the audit's predicate and the default", () => {
    const pred = p(String.raw`status = succeeded AND answer ~ /\d+ units/`)
    expect(pred.clauses).toHaveLength(2)
    expect(pred.needsAnswer).toBe(true)
    expect(p("").text).toBe(DEFAULT_PREDICATE)
    expect(p("  ").needsAnswer).toBe(false)
  })
  it("keeps AND inside a regex or a quoted text", () => {
    expect(p("answer ~ /cats and dogs/i").clauses).toHaveLength(1)
    expect(p('answer = "a and b" and steps <= 3').clauses).toHaveLength(2)
  })
  it("says what is wrong", () => {
    for (const [src, want] of [
      ["status = done", "status is one of"],
      ["answer ~ units", "answer ~ takes /regex/flags"],
      ["answer ~ /(/", "answer ~ /(/"],
      ["cost ~ 3", "cost takes"],
      ["steps < many", "steps takes"],
      ["latency < 3", "is not a clause"],
    ]) {
      const r = parsePredicate(src)
      expect(r.ok).toBe(false)
      if (!r.ok) expect(r.error).toContain(want)
    }
  })
})

describe("judge", () => {
  const pred = p(String.raw`status = succeeded AND answer ~ /\d+ units/`)
  it("passes, fails, or does not judge", () => {
    expect(
      judge(pred, { status: "succeeded", answer: "We have 42 units." })
    ).toBe("pass")
    expect(judge(pred, { status: "succeeded", answer: "none left" })).toBe(
      "fail"
    )
    // A known fail wins over an unknown answer.
    expect(judge(pred, { status: "failed", answer: null })).toBe("fail")
    // The answer not read: not judged, never a pass.
    expect(judge(pred, { status: "succeeded", answer: null })).toBe("unknown")
    expect(judge(pred, { status: "running", answer: "42 units" })).toBe(
      "unknown"
    )
  })
  it("reads numbers, and an unpriced cost is unknown", () => {
    const q = p("status = succeeded AND cost < 0.01 AND tokens <= 2000")
    expect(judge(q, { status: "succeeded", cost: 0.002, tokens: 1500 })).toBe(
      "pass"
    )
    expect(judge(q, { status: "succeeded", cost: 0.02, tokens: 1500 })).toBe(
      "fail"
    )
    expect(judge(q, { status: "succeeded", cost: null, tokens: 1500 })).toBe(
      "unknown"
    )
    expect(judge(p("status != failed"), { status: "succeeded" })).toBe("pass")
  })
})

describe("summarize", () => {
  const col = (o: Partial<ColumnTally> & { key: string }): ColumnTally => ({
    label: o.key,
    pass: 0,
    fail: 0,
    unknown: 0,
    missing: 0,
    usd: 0,
    ...o,
  })
  it("names the cheapest column passing every row and the cheapest overall", () => {
    const glm = col({ key: "glm", pass: 5, fail: 2, usd: 0.0021 })
    const kimi = col({ key: "kimi", pass: 7, usd: 0.0108 })
    const opus = col({ key: "opus", pass: 7, usd: 0.4 })
    const s = summarize([glm, kimi, opus])
    expect(s.cheapestPassing?.key).toBe("kimi")
    expect(s.cheapestOverall?.key).toBe("glm")
    expect(passRate(glm)).toBe("5/7")
  })
  it("never counts an unpriced column as $0, nor an unjudged one as passing", () => {
    const free = col({ key: "free", pass: 7, usd: null })
    const pending = col({ key: "pending", pass: 6, unknown: 1, usd: 0.001 })
    const s = summarize([free, pending])
    expect(s.cheapestPassing).toBeNull()
    expect(s.why).toContain("unpriced")
    expect(s.cheapestOverall?.key).toBe("pending")
  })
})
