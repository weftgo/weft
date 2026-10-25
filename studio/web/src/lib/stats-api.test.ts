// The aggregates' TS mirrors (UX plan A4, A2): lib/api.ts's StatsPage,
// StatsGroup and SearchFacets against the Go goldens — a key the server
// sends that the mirror does not know fails here, not on a page.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"

import type {
  CostFields,
  SearchFacets,
  SearchPage,
  StatsBucket,
  StatsGroup,
  StatsPage,
  StatsPercentiles,
} from "./api"

const API = resolve(process.cwd(), "../testdata/api")
const golden = <T>(name: string): T =>
  JSON.parse(readFileSync(resolve(API, `${name}.golden.json`), "utf8")) as T

const PAGE_KEYS: Record<keyof StatsPage, true> = {
  by: true,
  since: true,
  until: true,
  buckets: true,
  bucket_ms: true,
  groups: true,
  groups_cut: true,
}
const COST_KEYS: Record<keyof CostFields, true> = {
  cost_usd: true,
  cost_known: true,
  cost_badge: true,
  cost_reason: true,
  unpriced_models: true,
  cache: true,
}
const GROUP_KEYS: Record<Exclude<keyof StatsGroup, keyof CostFields>, true> = {
  key: true,
  runs: true,
  failed: true,
  failure_rate: true,
  calls: true,
  failed_calls: true,
  input_tokens: true,
  output_tokens: true,
  duration_ms: true,
  ttft_ms: true,
  retried: true,
  retry_rate: true,
  denials: true,
  series: true,
}
const PCT_KEYS: Record<keyof StatsPercentiles, true> = {
  p50: true,
  p95: true,
  n: true,
}
const BUCKET_KEYS: Record<keyof StatsBucket, true> = {
  runs: true,
  failed: true,
  calls: true,
  failed_calls: true,
  input_tokens: true,
  output_tokens: true,
}
const FACET_KEYS: Record<keyof SearchFacets, true> = {
  error_class: true,
  error_class_capped: true,
}

const unknownKeys = (obj: object, known: Record<string, true>): string[] =>
  Object.keys(obj).filter((k) => !(k in known))

describe("GET /api/stats' mirror knows every key the goldens carry", () => {
  for (const by of ["agent", "model", "tool", "error-type", "step"]) {
    it(by, () => {
      const page = golden<StatsPage>(`stats-${by}`)
      expect(unknownKeys(page, PAGE_KEYS)).toEqual([])
      expect(page.buckets).toBe(24)
      expect(page.groups.length).toBeGreaterThan(0)
      for (const g of page.groups) {
        expect(unknownKeys(g, { ...GROUP_KEYS, ...COST_KEYS })).toEqual([])
        for (const p of [g.duration_ms, g.ttft_ms])
          if (p) expect(unknownKeys(p, PCT_KEYS)).toEqual([])
        expect(g.series).toHaveLength(page.buckets)
        for (const b of g.series)
          expect(unknownKeys(b, BUCKET_KEYS)).toEqual([])
      }
    })
  }
  it("an unpriced model is unknown, never $0", () => {
    const researcher = golden<StatsPage>("stats-agent").groups.find(
      (g) => g.key === "researcher"
    )!
    expect(researcher.cost_usd).toBeNull()
    expect(researcher.cost_known).toBe(false)
    expect(researcher.unpriced_models).toEqual(["zai/glm-5.3-flash"])
  })
})

describe("search's facets", () => {
  it("the error_class facet's mirror", () => {
    const page = golden<SearchPage>("search")
    expect(page.facets).toBeDefined()
    expect(unknownKeys(page.facets!, FACET_KEYS)).toEqual([])
    for (const c of page.facets!.error_class)
      expect(Object.keys(c).sort()).toEqual(["class", "count"])
  })
})
