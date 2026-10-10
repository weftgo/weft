// The wire pair's client types and helpers over Go's golden
// (studio/testdata/api/requests-raw.golden.json, TestRequestsRawGolden):
// lib/api.ts's RawRecord and RawSide name exactly the fields obsdb's
// JSON tags write — a renamed or added Go field fails here.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"

import type { RawRecord, RawSide, RequestRow } from "./api"
import { hasWire } from "./requests"
import { headerRows, isJSONMedia, wireFileName, withheld } from "./wire"

const rows = (
  JSON.parse(readFileSync(resolve(process.cwd(), "../testdata/api/requests-raw.golden.json"), "utf8")) as {
    requests: RequestRow[]
  }
).requests

/** Every field the client types declare (obsdb.RawRecord / RawSide's tags). */
const RECORD_KEYS = [
  "index",
  "step",
  "attempt",
  "time",
  "request",
  "response",
  "redacted_headers",
  "redacted_query",
  "content",
  "truncated_bytes",
] as const satisfies readonly (keyof RawRecord)[]
const SIDE_KEYS = [
  "method",
  "url",
  "status",
  "headers",
  "media_type",
  "body",
  "encoding",
  "bytes",
  "truncated_bytes",
  "complete",
] as const satisfies readonly (keyof RawSide)[]

describe("RawRecord / RawSide pinned to the Go JSON tags", () => {
  const raws = rows.map((r) => r.raw).filter((r): r is RawRecord => r != null)

  it("the golden has three exchanges and a row without one", () => {
    expect(raws).toHaveLength(3)
    expect(rows.filter((r) => !hasWire(r)).map((r) => r.raw_badge)).toEqual(["not_recorded"])
  })

  it("every key the golden writes is one the types declare, and every declared key appears", () => {
    const rec = new Set(raws.flatMap((r) => Object.keys(r)))
    const side = new Set(raws.flatMap((r) => [r.request, ...(r.response ? [r.response] : [])]).flatMap((s) => Object.keys(s)))
    expect([...rec].filter((k) => !(RECORD_KEYS as readonly string[]).includes(k))).toEqual([])
    expect([...side].filter((k) => !(SIDE_KEYS as readonly string[]).includes(k))).toEqual([])
    expect(RECORD_KEYS.filter((k) => !rec.has(k))).toEqual([])
    expect(SIDE_KEYS.filter((k) => !side.has(k))).toEqual([])
  })

  it("the leaf types are the declared ones", () => {
    const [a, b, c] = raws
    expect(typeof a.request.bytes).toBe("number")
    expect(a.response?.complete).toBe(false)
    expect(b.response?.encoding).toBe("base64")
    expect(b.request.truncated_bytes).toBe(4096)
    expect(c.response).toBeNull()
    expect(Array.isArray(a.redacted_headers)).toBe(true)
  })
})

describe("the helpers", () => {
  it("wireFileName keeps a run id to a safe file name", () => {
    expect(wireFileName("r_1", { step: 2, attempt: 3 })).toBe("r_1.step2.attempt3.wire.json")
    expect(wireFileName("../a b/c?d#e", { step: 0, attempt: 1 })).toBe(".._a_b_c_d_e.step0.attempt1.wire.json")
  })

  it("headerRows sorts names and splits values; withheld counts and names", () => {
    expect(headerRows({ b: ["2", "3"], A: ["1"] })).toEqual([
      ["A", "1"],
      ["b", "2"],
      ["b", "3"],
    ])
    expect(headerRows(null)).toEqual([])
    expect(withheld(["x-api-key"], "header")).toBe("1 header withheld: x-api-key")
    expect(withheld([], "header")).toBeNull()
    expect(withheld(undefined, "query parameter")).toBeNull()
  })

  it("isJSONMedia: application/json, +json, with parameters", () => {
    expect(isJSONMedia("application/json; charset=utf-8")).toBe(true)
    expect(isJSONMedia("application/problem+json")).toBe(true)
    expect(isJSONMedia("text/event-stream")).toBe(false)
    expect(isJSONMedia(undefined)).toBe(false)
  })
})
