// The aggregates drawn (UX-E-25, UX-A-14): a group's strip from the Go
// golden, a runs_query read into the runs list's URL that asks the same
// server query back, and the "vs typical" chips' n ≥ 5 rule.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"

import type { StatsGroup, StatsPage } from "./api"
import { searchFiltersFrom } from "./run-filters"
import {
  groupMetrics,
  pct,
  statsRunsSearch,
  statsWindowOf,
  typicalChips,
} from "./stats"

const page = JSON.parse(
  readFileSync(
    resolve(process.cwd(), "../testdata/api/stats-agent.golden.json"),
    "utf8"
  )
) as StatsPage
const group = (k: string) => page.groups.find((g) => g.key === k)!

describe("groupMetrics", () => {
  it("draws the seeded agents' numbers; unpriced never $0", () => {
    const m = Object.fromEntries(
      groupMetrics(group("researcher")).map((x) => [x.key, x])
    )
    expect(m.runs.value).toBe("5")
    expect(m.failed.value).toBe("60%")
    expect(m.failed.bad).toBe(true)
    expect(m.duration.value).toBe("p50 3.0s · p95 5.0s")
    expect(m.ttft.value).toBe("p50 600ms")
    expect(m.tokens.value).toBe("176 → 58")
    expect(m.cost.cost?.known).toBe(false)
    expect(m.cost.cost?.badge).toBe("unpriced")
    expect(m.runs.series).toHaveLength(24)
    const d = Object.fromEntries(
      groupMetrics(group("director")).map((x) => [x.key, x])
    )
    expect(d.cost.value).toBe("$0.0020")
    expect(d.retries.value).toBe("67%")
    const o = Object.fromEntries(
      groupMetrics(group("orders")).map((x) => [x.key, x])
    )
    expect(o.cost.value).toBe("$0 · scripted")
    expect(o.denials.value).toBe("1")
  })
  it("words rates", () => {
    expect(pct(null)).toBe("—")
    expect(pct(0.004)).toBe("0.4%")
    expect(statsWindowOf("7d")).toBe("7d")
    expect(statsWindowOf("1y")).toBe("24h")
  })
})

describe("statsRunsSearch", () => {
  it("lands on the runs the group counts: the same query back", () => {
    const g = group("director")
    const s = statsRunsSearch(g.runs_query!)
    expect(s).toMatchObject({
      agent: "director",
      subagents: "all",
      experiments: "off",
    })
    const back = searchFiltersFrom(s, {
      search: true,
      pricing: true,
      stats: true,
    })
    const want = new URLSearchParams(g.runs_query)
    expect(back.agent).toBe(want.get("agent"))
    expect(back.all).toBe(true)
    expect(back.playground).toBe(false)
    expect(back.since).toBe(want.get("since"))
    expect(back.until).toBe(want.get("until"))
  })
  it("reads an error_type group's kind and status", () => {
    const s = statsRunsSearch(
      "error_type=usage_limit&status=failed&all=1&tag.env=prod&since=2020-01-01T00%3A00%3A00Z"
    )
    expect(s).toMatchObject({
      error_type: "usage_limit",
      status: "failed",
      tag: "env=prod",
    })
  })
})

describe("typicalChips", () => {
  const doc = {
    started: "2020-01-01T10:00:00Z",
    finished: "2020-01-01T10:00:09Z",
    usage: { input_tokens: 900, output_tokens: 100 },
    children: [],
    cost_usd: 0.004,
    cost_known: true,
  }
  it("says too few under 5 runs", () => {
    expect(typicalChips(doc, group("director"))).toEqual({ few: 3 })
    expect(typicalChips(doc, undefined)).toEqual({ few: 0 })
  })
  it("compares duration with the p50 and tokens / cost with the mean", () => {
    const g: StatsGroup = {
      ...group("director"),
      runs: 6,
      input_tokens: 3000,
      output_tokens: 600,
      cost_usd: 0.012,
      duration_ms: { p50: 3000, p95: 7000, n: 6 },
    }
    const chips = typicalChips(doc, g)
    expect(chips).toEqual([
      expect.objectContaining({
        key: "duration",
        text: "duration 3.0× p50",
        high: true,
      }),
      expect.objectContaining({
        key: "tokens",
        text: "tokens 1.7× mean",
        high: true,
      }),
      expect.objectContaining({ key: "cost", text: "cost 2.0× mean" }),
    ])
    // An unpriced group: no cost chip, never a $0 comparison.
    const un = typicalChips(doc, { ...g, cost_known: false, cost_usd: null })
    expect(Array.isArray(un) && un.map((c) => c.key)).toEqual([
      "duration",
      "tokens",
    ])
  })
})
