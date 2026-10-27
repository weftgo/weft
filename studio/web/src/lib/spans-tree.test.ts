// The run tree's client types over Go's golden
// (studio/testdata/api/spans-tree.golden.json, TestSpansTreeGolden):
// lib/api.ts's SpansDoc and TreeHole name exactly the fields api.go's
// treeSpansDoc / treeHole write — a renamed or added Go field fails here.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"

import type { Span, SpansDoc, TreeHole } from "./api"

const doc = JSON.parse(
  readFileSync(
    resolve(process.cwd(), "../testdata/api/spans-tree.golden.json"),
    "utf8"
  )
) as SpansDoc

const DOC_KEYS = [
  "spans",
  "holes",
] as const satisfies readonly (keyof SpansDoc)[]
const HOLE_KEYS = [
  "hole",
  "cause",
  "reason",
  "fix",
  "runs",
  "count",
] as const satisfies readonly (keyof TreeHole)[]
const TREE_SPAN_KEYS = [
  "run_id",
  "parent_run_id",
] as const satisfies readonly (keyof Span)[]

describe("SpansDoc / TreeHole pinned to the Go JSON tags", () => {
  const holes = doc.holes ?? []

  it("the golden carries every hole the tree answers", () => {
    expect(holes.map((h) => `${h.hole}:${h.cause ?? ""}`)).toEqual([
      "truncated:response_cap",
      "truncated:response_cap",
      "not_recorded:no_spans",
      "hidden:",
    ])
  })

  it("every key the golden writes is one the types declare, and every declared key appears", () => {
    expect(Object.keys(doc).sort()).toEqual([...DOC_KEYS].sort())
    const written = new Set(holes.flatMap((h) => Object.keys(h)))
    expect([...written].sort()).toEqual([...HOLE_KEYS].sort())
    const spanKeys = new Set(doc.spans.flatMap((s) => Object.keys(s)))
    for (const k of TREE_SPAN_KEYS) expect(spanKeys.has(k)).toBe(true)
  })

  it("no_spans names at most its runs and counts them all", () => {
    const missing = holes.find((h) => h.cause === "no_spans")
    expect(missing?.runs).toHaveLength(1)
    expect(missing?.count).toBe(101)
  })
})
