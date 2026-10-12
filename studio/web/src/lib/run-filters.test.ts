// The runs list's filters (plan H1) as plain values: the URL read
// into chips and back, which list answers them, what the loaded rows
// answer when the Studio cannot search — and the trace list's TS
// mirror against the Go golden (traces.golden.json ⊆ TraceRow).
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { afterEach, describe, expect, it, vi } from "vitest"

import { fetchRuns, fetchSearch, fetchTraces } from "./api"
import type { RunRow, RunsPage, TraceRow, TracesPage } from "./api"
import {
  CLEARED,
  applyLocal,
  filtersFromSearch as filtersFromSearchOf,
  dedupeTraces,
  localKeys,
  rangeTime,
  removeChip,
  runChips,
  searchFiltersFrom,
  validateRunsSearch,
  parseWhen,
  viaSearch,
  whenNotes,
} from "./run-filters"
import { runsLink } from "./links"

const API = resolve(process.cwd(), "../testdata/api")
const golden = <T>(name: string): T =>
  JSON.parse(readFileSync(resolve(API, `${name}.golden.json`), "utf8")) as T

const ALL = { search: true, pricing: true }
const NONE = { search: false, pricing: false }

describe("the URL's filters", () => {
  const raw = {
    q: "refund failed",
    agent: "orders",
    status: "failed",
    tool: "lookup_order",
    model: "glm-4",
    error: "timeout",
    since: "2026-10-01",
    until: "2026-10-02T12:00:00Z",
    min_duration: 5,
    min_tokens: "1000",
    min_cost: 0.25,
    tag: ["tier=gold", "region=eu", "not-a-pair"],
    sort: "cost",
  }

  it("validates each key, drops what is malformed", () => {
    const s = validateRunsSearch(raw)
    expect(s).toMatchObject({
      q: "refund failed",
      status: "failed",
      since: "2026-10-01",
      until: "2026-10-02T12:00:00Z",
      min_duration: 5,
      min_tokens: 1000,
      min_cost: 0.25,
      tag: ["tier=gold", "region=eu"],
      sort: "cost",
    })
    expect(
      validateRunsSearch({
        status: "nope",
        min_tokens: -1,
        sort: "started",
        tag: "x",
      })
    ).toEqual(validateRunsSearch({}))
    // The server's RFC 3339 (time.Parse(time.RFC3339) → 400 otherwise):
    // a real day only — never V8's 2026-02-30 rolled to March 2 — and a
    // time with its zone; min_tokens a number.
    // Refused values stay in the search as typed (the router would drop
    // the key and the text with it) and are never sent or applied: a
    // not-applied chip and a note say so.
    const refused = validateRunsSearch({
      q: "x",
      since: "2026-02-30",
      until: "2026-10-02T12:00:00",
      min_tokens: "abc",
    })
    expect(refused.min_tokens).toBeUndefined()
    expect(searchFiltersFrom(refused, ALL)).toEqual({ q: "x" })
    expect(viaSearch({ since: "2026-02-30" }, ALL)).toBe(false)
    expect(localKeys({ since: "2026-02-30" }, NONE)).toEqual([])
    expect(
      runChips(refused, ALL).filter(
        (c) => c.key === "since" || c.key === "until"
      )
    ).toEqual([
      {
        key: "since",
        label: "since 2026-02-30",
        applied: false,
        note: "not a real day (YYYY-MM-DD) or an RFC 3339 time with a zone",
      },
      {
        key: "until",
        label: "until 2026-10-02T12:00:00",
        applied: false,
        note: "not a real day (YYYY-MM-DD) or an RFC 3339 time with a zone",
      },
    ])
    for (const bad of [
      "2026-13-01",
      "2026-10-01T24:00:00Z",
      "2026-10-01T12:00:00+2:00",
      "2026-10-01 12:00:00Z",
      "2026-02-30",
    ])
      expect(parseWhen(bad), bad).toBeUndefined()
    for (const good of [
      "2024-02-29",
      "2026-10-01T12:00:00Z",
      "2026-10-01T12:00:00.5+02:00",
    ])
      expect(parseWhen(good), good).toBe(good)
    expect(whenNotes({ since: "2026-02-30", until: "2026-10-02" })).toEqual([
      "since “2026-02-30” is ignored: not a real day (YYYY-MM-DD) or an RFC 3339 time with a zone",
    ])
    // A number the router parsed from digits is still the text.
    expect(validateRunsSearch({ q: 42 }).q).toBe("42")
  })

  it("draws one chip per filter, one per tag, and removing one keeps the rest", () => {
    const s = validateRunsSearch(raw)
    const chips = runChips(s, ALL)
    expect(chips.map((c) => c.label)).toEqual([
      "“refund failed”",
      "agent orders",
      "status failed",
      "tool lookup_order",
      "model glm-4",
      "error ~ timeout",
      "since 2026-10-01",
      "until 2026-10-02T12:00:00Z",
      "duration ≥ 5s",
      "tokens ≥ 1,000",
      "cost ≥ $0.25",
      "tag tier=gold",
      "tag region=eu",
    ])
    const gold = chips.find((c) => c.value === "tier=gold")!
    expect(removeChip(s, gold)).toEqual({ tag: "region=eu" })
    expect(
      removeChip(
        { ...s, tag: "region=eu" },
        chips.find((c) => c.value === "region=eu")!
      )
    ).toEqual({
      tag: undefined,
    })
    expect(removeChip(s, chips[0])).toEqual({ q: undefined })
    // The round trip: chips → link → the same search back.
    const link = runsLink({ ...s, ...removeChip(s, gold) })
    expect(validateRunsSearch(link.search as Record<string, unknown>)).toEqual({
      ...s,
      tag: "region=eu",
    })
    // Clear all keeps the sort (a view, not a filter).
    expect(runChips({ ...s, ...CLEARED }, ALL)).toEqual([])
    expect({ ...s, ...CLEARED }.sort).toBe("cost")
  })

  it("routes a text or search-only filter, or a sort, through /api/search", () => {
    expect(viaSearch({}, ALL)).toBe(false)
    expect(viaSearch({ agent: "orders", min_duration: 3 }, ALL)).toBe(false)
    expect(viaSearch({ q: "x" }, ALL)).toBe(true)
    expect(viaSearch({ sort: "duration" }, ALL)).toBe(true)
    expect(viaSearch({ q: "x" }, NONE)).toBe(false)
    // Cost needs pricing: without it neither sort=cost nor min_cost asks search.
    expect(
      viaSearch({ sort: "cost", min_cost: 1 }, { search: true, pricing: false })
    ).toBe(false)
    const f = searchFiltersFrom(
      validateRunsSearch({ ...raw, before: "2026-10-01T00:00:00Z" }),
      ALL
    )
    expect(f).toEqual({
      agent: "orders",
      status: "failed",
      tag: { tier: "gold", region: "eu" },
      q: "refund failed",
      tool: "lookup_order",
      model: "glm-4",
      error: "timeout",
      since: "2026-10-01T00:00:00Z",
      until: "2026-10-02T12:00:00Z",
      min_tokens: 1000,
      min_cost: 0.25,
      sort: "cost",
    })
  })

  it("a day is UTC midnight: since's own, until's next", () => {
    expect(rangeTime("2026-10-01", "since")).toBe("2026-10-01T00:00:00Z")
    expect(rangeTime("2026-10-31", "until")).toBe("2026-11-01T00:00:00Z")
    expect(rangeTime("2026-10-01T09:00:00+02:00", "until")).toBe(
      "2026-10-01T09:00:00+02:00"
    )
  })
})

describe("the loaded rows answer what the list cannot", () => {
  const runs = golden<RunsPage>("runs").runs
  const now = Date.parse("2020-01-01T09:03:10.123Z")

  it("duration always, client-side: the API has no duration filter", () => {
    const s = validateRunsSearch({ min_duration: 3 })
    expect(localKeys(s, ALL)).toEqual(["min_duration"])
    // r_stale has run 10 s so far; r_sub took 5 s; the rest 2 s.
    expect(applyLocal(runs, s, ALL, now).map((r) => r.id)).toEqual([
      "r_stale",
      "r_sub",
    ])
    expect(runChips(s, ALL)[0].note).toBe("the loaded rows only")
  })

  it("experiment_id (plan F5, F5.1): a URL chip the API applies, playground runs asked of both lists", () => {
    const s = validateRunsSearch({ experiment_id: "exp_tone" })
    expect(s.experiment_id).toBe("exp_tone")
    const chips = runChips(s, ALL)
    expect(chips).toEqual([
      { key: "experiment_id", label: "experiment exp_tone", applied: true },
    ])
    expect(removeChip(s, chips[0])).toEqual({ experiment_id: undefined })
    expect(CLEARED).toHaveProperty("experiment_id", undefined)
    // Both lists are asked for the experiment's playground runs.
    expect(filtersFromSearchOf(s)).toMatchObject({
      experiment_id: "exp_tone",
      playground: true,
    })
    expect(searchFiltersFrom({ ...s, q: "x" }, ALL)).toMatchObject({
      experiment_id: "exp_tone",
      playground: true,
    })
    // The API filters (F5.1): the loaded rows are not narrowed again.
    const mine = { ...runs[0], id: "pg_1", experiment_id: "exp_tone" }
    expect(applyLocal([mine, ...runs], s, ALL, now)).toHaveLength(
      runs.length + 1
    )
    expect(localKeys(s, NONE)).toEqual([])
    expect(runsLink({ experiment_id: "exp_tone" })).toEqual({
      to: "/runs",
      search: { experiment_id: "exp_tone" },
    })
  })

  it("without search: model, error, range and tokens filter the loaded rows; text and tool cannot", () => {
    const s = validateRunsSearch({
      q: "refund",
      tool: "lookup",
      error: "INJECTED",
      since: "2020-01-01T09:00:30Z",
    })
    expect(localKeys(s, NONE)).toEqual(["error", "since"])
    expect(applyLocal(runs, s, NONE, now).map((r) => r.id)).toEqual(["r_fail"])
    const chips = runChips(s, NONE)
    expect(chips.find((c) => c.key === "q")).toMatchObject({
      applied: false,
      note: "needs the search capability",
    })
    expect(chips.find((c) => c.key === "tool")).toMatchObject({
      applied: false,
    })
    expect(chips.find((c) => c.key === "error")).toMatchObject({
      applied: true,
      note: "the loaded rows only",
    })
    // min_cost without pricing applies nowhere, and says so.
    expect(runChips({ min_cost: 1 }, NONE)[0]).toMatchObject({
      applied: false,
      note: "needs the pricing capability",
    })
  })

  it("without search, a sort orders the loaded rows; with it, the API's order stands", () => {
    const byTokens = (rs: RunRow[]) =>
      rs.map((r) => r.usage.input_tokens + r.usage.output_tokens)
    const sorted = byTokens(applyLocal(runs, { sort: "tokens" }, NONE, now))
    expect(sorted).toEqual([...sorted].sort((a, b) => b - a))
    expect(applyLocal(runs, { sort: "tokens" }, ALL, now)).toEqual(runs)
  })
})

describe("the trace list's mirror (traces.golden.json ⊆ TraceRow)", () => {
  const ROW_KEYS: Record<keyof TraceRow, true> = {
    trace_id: true,
    root: true,
    service: true,
    start: true,
    end: true,
    duration_ms: true,
    span_count: true,
    error: true,
    run_ids: true,
    badge: true,
    reason: true,
    fix: true,
  }
  const PAGE_KEYS: Record<keyof TracesPage, true> = {
    total: true,
    traces: true,
    next_before: true,
    next_before_id: true,
  }
  const page = golden<TracesPage>("traces")

  it("knows every key the Go golden carries", () => {
    expect(Object.keys(page).filter((k) => !(k in PAGE_KEYS))).toEqual([])
    expect(page.traces.length).toBeGreaterThan(0)
    for (const t of page.traces) {
      expect(Object.keys(t).filter((k) => !(k in ROW_KEYS))).toEqual([])
      if (t.root)
        expect(Object.keys(t.root).sort()).toEqual(["name", "span_id"])
    }
    // The rootless trace carries the gap badge with its words.
    const rootless = page.traces.find((t) => t.root === null)!
    expect(rootless).toMatchObject({ badge: "gap" })
    expect(rootless.reason).toBeTruthy()
  })

  it("dedupes a trace listed again on a later page, first row kept", () => {
    const [a, b] = page.traces
    expect(
      dedupeTraces([a, b, { ...a, span_count: 99 }]).map((t) => [
        t.trace_id,
        t.span_count,
      ])
    ).toEqual([
      [a.trace_id, a.span_count],
      [b.trace_id, b.span_count],
    ])
  })
})

describe("the queries", () => {
  afterEach(() => vi.unstubAllGlobals())
  const reply = (body: unknown) =>
    new Response(JSON.stringify(body), {
      headers: { "Content-Type": "application/json" },
    })

  it("fetchSearch sends search's own filters beside /api/runs'", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input))
      expect(url.pathname.endsWith("/api/search")).toBe(true)
      expect(url.search).toBe(
        "?agent=orders&tag.tier=gold&q=refund+failed&tool=lookup&model=m&error=boom&since=2026-10-01T00%3A00%3A00Z&until=2026-10-02T00%3A00%3A00Z&min_tokens=10&min_cost=0.5&sort=cost&offset=50"
      )
      return reply({ total: 0, runs: [], sort: "cost", next_offset: null })
    })
    vi.stubGlobal("fetch", fetchMock)
    await fetchSearch({
      agent: "orders",
      tag: { tier: "gold" },
      q: "refund failed",
      tool: "lookup",
      model: "m",
      error: "boom",
      since: "2026-10-01T00:00:00Z",
      until: "2026-10-02T00:00:00Z",
      min_tokens: 10,
      min_cost: 0.5,
      sort: "cost",
      offset: 50,
    })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it("experiment_id rides both routes' queries (plan F5)", async () => {
    const seen: string[] = []
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = new URL(String(input))
        seen.push(`${url.pathname.split("/api/")[1]}${url.search}`)
        return reply({
          total: 0,
          runs: [],
          sort: "started",
          next_offset: null,
          next_before: null,
        })
      })
    )
    await fetchSearch({ playground: true, experiment_id: "exp_tone", q: "x" })
    await fetchRuns({ playground: true, experiment_id: "exp_tone" })
    expect(seen).toEqual([
      "search?playground=true&experiment_id=exp_tone&q=x",
      "runs?playground=true&experiment_id=exp_tone",
    ])
  })

  it("fetchTraces sends the filters and the exact cursor", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input))
      expect(url.pathname.endsWith("/api/traces")).toBe(true)
      expect(Object.fromEntries(url.searchParams)).toEqual({
        since: "2026-10-01T00:00:00Z",
        service: "edge-svc",
        before: "2026-10-02T00:00:00Z",
        before_id: "2b",
        limit: "50",
      })
      return reply(golden("traces"))
    })
    vi.stubGlobal("fetch", fetchMock)
    const page = await fetchTraces({
      since: "2026-10-01T00:00:00Z",
      service: "edge-svc",
      before: "2026-10-02T00:00:00Z",
      before_id: "2b",
      limit: 50,
    })
    expect(page.traces).toHaveLength(4)
  })
})
