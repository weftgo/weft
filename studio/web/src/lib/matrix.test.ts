// The multi-source matrix (UX-C-37, UX-C-38): the failing step each row
// continues from, the client restatement of pricing.go's price (held to
// a figure the server priced), and the estimate before Run.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"

import type { ModelPrice, StatsPage } from "./api"
import {
  columnKey,
  estimateMatrix,
  failingStep,
  matrixDefinition,
  priceAt,
  sourceUsage,
  sourcesOf,
  sourcesParam,
} from "./matrix"

const TABLE = (
  JSON.parse(
    readFileSync(resolve(process.cwd(), "../pricing.json"), "utf8")
  ) as { models: ModelPrice[] }
).models
const STATS = JSON.parse(
  readFileSync(
    resolve(process.cwd(), "../testdata/api/stats-agent.golden.json"),
    "utf8"
  )
) as StatsPage

describe("failingStep", () => {
  it("reads the core's 'at step N', clamped to the recorded steps", () => {
    const err = (n: number) =>
      `weft: run failed at step ${n}: weft: run exceeded its usage limit (input 300/250, output 150/250)`
    expect(failingStep({ err: err(2), steps: 3 })).toBe(2)
    expect(failingStep({ err: err(5), steps: 3 })).toBe(2)
    expect(failingStep({ err: "boom", steps: 4 })).toBe(3)
    expect(failingStep({ err: "", steps: 0 })).toBe(0)
  })
})

describe("priceAt", () => {
  it("prices as the server does (director's haiku in the stats golden)", () => {
    const director = STATS.groups.find((g) => g.key === "director")!
    const p = priceAt(
      TABLE,
      { provider: "anthropic", name: "claude-haiku-4-5" },
      {
        input_tokens: director.input_tokens,
        output_tokens: director.output_tokens,
      }
    )
    expect(p).toEqual({ known: true, usd: director.cost_usd })
  })
  it("matches a glob row and says why for an unpriced model", () => {
    const p = priceAt(
      TABLE,
      { provider: "anthropic", name: "claude-haiku-4-5-20251001" },
      { input_tokens: 1_000_000, output_tokens: 0 }
    )
    expect(p.known).toBe(true)
    expect(
      priceAt(
        TABLE,
        { provider: "zai", name: "glm-5.3-flash" },
        { input_tokens: 1, output_tokens: 1 }
      )
    ).toEqual({ known: false, why: "zai/glm-5.3-flash has no price" })
    expect(
      priceAt(
        TABLE,
        { provider: "wefttest", name: "script" },
        { input_tokens: 9, output_tokens: 9 }
      )
    ).toEqual({ known: true, usd: 0, scripted: true })
  })
})

describe("estimateMatrix", () => {
  const row = {
    usage: { input_tokens: 1000, output_tokens: 200 },
    partial: false,
  }
  const rows = Array.from({ length: 7 }, () => row)
  const own = { provider: "zai", name: "glm-5.3-flash" }
  it("states cells, tokens and the bill before Run", () => {
    const e = estimateMatrix({
      columns: [
        { key: "haiku", model: "anthropic/claude-haiku-4-5", engine: "live" },
        { key: "sonnet", model: "anthropic/claude-sonnet-4-6", engine: "live" },
      ],
      rows,
      table: TABLE,
      own,
    })
    expect(e.cells).toBe(14)
    expect(e.tokens).toBe(14 * 1200)
    expect(e.line).toMatch(/^14 cells · ≈ 16\.8k tokens · ≈ \$0\.\d+$/)
    expect(e.columns[0].text).toBe("≈ $0.01")
  })
  it("says cost unknown for an unpriced model — never $0", () => {
    const e = estimateMatrix({
      columns: [
        { key: "glm", model: "", engine: "live" },
        { key: "haiku", model: "anthropic/claude-haiku-4-5", engine: "live" },
      ],
      rows,
      table: TABLE,
      own,
    })
    expect(e.usd).toBeNull()
    expect(e.line).toBe(
      "14 cells · ≈ 16.8k tokens · cost unknown: the agent's model: zai/glm-5.3-flash has no price"
    )
    expect(e.columns[1].usd).toBeCloseTo(0.014)
  })
  it("a scripted column is free; an unread row or table says so", () => {
    const e = estimateMatrix({
      columns: [{ key: "own", model: "", engine: "scripted" }],
      rows,
      table: null,
      own,
    })
    expect(e.line).toBe("7 cells · ≈ 0 tokens · ≈ $0.00")
    const u = estimateMatrix({
      columns: [
        { key: "h", model: "anthropic/claude-haiku-4-5", engine: "live" },
      ],
      rows: [row, { why: "reading the source's steps…" }],
      table: TABLE,
      own,
    })
    expect(u.line).toBe(
      "2 cells · cost unknown: anthropic/claude-haiku-4-5: reading the source's steps…"
    )
  })
})

describe("the matrix's pieces", () => {
  it("sums a row's steps from its failing step; a step past the cap is a floor", () => {
    const d = (i: number, o: number) => ({
      usage: { input_tokens: i, output_tokens: o },
    })
    expect(sourceUsage(2, [d(10, 5), d(20, 1)])).toMatchObject({
      usage: { input_tokens: 30, output_tokens: 6 },
      partial: false,
    })
    expect(sourceUsage(2, [d(10, 5), null])).toMatchObject({ partial: true })
    expect(sourceUsage(2, [])).toEqual({
      why: "the source never answered step 2",
    })
    expect(sourceUsage(2, [undefined])).toEqual({
      why: "reading the source's steps…",
    })
  })
  it("defines one input per source with its own step, the predicate verbatim", () => {
    const def = matrixDefinition({
      id: "ux-C-cheapest",
      name: "",
      agent: "researcher",
      columns: [
        { key: "haiku", model: "anthropic/claude-haiku-4-5", engine: "live" },
        { key: "own", model: "", engine: "scripted" },
      ],
      rows: [
        { runID: "r1", fromStep: 2 },
        { runID: "r2", fromStep: 0 },
      ],
      predicate: String.raw` status = succeeded AND answer ~ /\d+ units/ `,
    })
    expect(def.inputs).toEqual([
      { key: "1", source_run_id: "r1", from_step: 2 },
      { key: "2", source_run_id: "r2", from_step: 0 },
    ])
    expect(def.variants[0]).toMatchObject({
      overrides: { model: "anthropic/claude-haiku-4-5" },
      engine: "live",
    })
    expect(def.variants[1]).toMatchObject({ overrides: {}, engine: "scripted" })
    expect(def.pass_predicate).toBe(
      String.raw`status = succeeded AND answer ~ /\d+ units/`
    )
  })
  it("carries the sources in the URL, deduped and capped", () => {
    expect(sourcesOf(sourcesParam(["a", "b", "a"]))).toEqual(["a", "b"])
    expect(
      sourcesOf(sourcesParam(Array.from({ length: 30 }, (_, i) => `r${i}`)))
    ).toHaveLength(20)
    expect(columnKey("zai/glm-5.3-flash", ["glm-5.3-flash"])).toBe(
      "glm-5.3-flash-2"
    )
  })
})
