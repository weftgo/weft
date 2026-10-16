import { describe, expect, it } from "vitest"

import {
  EXACT_VALUE,
  exactRows,
  lookupsOf,
  metaKeywords,
  paletteFilter,
} from "./finder"

describe("lookupsOf", () => {
  it("reads an id prefix, a public id and a tag; spaces and short text ask nothing", () => {
    expect(lookupsOf("1c06797f")).toEqual({ idPrefix: "1c06797f" })
    expect(lookupsOf("pub_acme")).toEqual({
      idPrefix: "pub_acme",
      publicId: "pub_acme",
    })
    expect(lookupsOf("scenario=rw.deep-delegation")).toEqual({
      tag: { key: "scenario", value: "rw.deep-delegation" },
    })
    expect(lookupsOf("1c0")).toEqual({})
    expect(lookupsOf("refund failed")).toEqual({})
    expect(lookupsOf("=x")).toEqual({})
  })
})

describe("exactRows", () => {
  it("keeps only what was asked, the exact id first, once each", () => {
    const rows = [{ id: "1c06797fab" }, { id: "zz" }, { id: "1c06797f" }]
    expect(
      exactRows({ idPrefix: "1c06797f" }, { idPrefix: rows }).map((r) => r.id)
    ).toEqual(["1c06797f", "1c06797fab"])
    expect(
      exactRows(
        { publicId: "pub_a" },
        {
          publicId: [
            { id: "a", public_id: "pub_a" },
            { id: "b", public_id: "pub_b" },
          ],
        }
      ).map((r) => r.id)
    ).toEqual(["a"])
    expect(
      exactRows(
        { tag: { key: "k", value: "v" } },
        {
          tag: [
            { id: "a", meta: { k: "v" } },
            { id: "b", meta: { k: "w" } },
          ],
        }
      ).map((r) => r.id)
    ).toEqual(["a"])
  })
})

describe("paletteFilter", () => {
  it("matches contiguous substrings only, never a subsequence", () => {
    expect(paletteFilter("trace:d1a5c0b06c7d9e7f", "1c06797f")).toBe(0)
    expect(paletteFilter("run:abc1c06797fdef x", "1c06797f")).toBeGreaterThan(0)
    expect(paletteFilter("run:1c06797f x", "1c06797f")).toBe(0.9)
    expect(paletteFilter(`${EXACT_VALUE}run:x`, "anything")).toBe(1)
    expect(
      paletteFilter("session:s1", "lagos refund", ["Lagos refund escalation"])
    ).toBeGreaterThan(0)
    expect(paletteFilter("session:s1", "lagos zz", ["Lagos refund"])).toBe(0)
  })
  it("a run's metadata pairs are keywords", () => {
    expect(metaKeywords({ scenario: "rw.x" })).toEqual(["scenario=rw.x"])
    expect(
      paletteFilter("run:r1", "rw.x", metaKeywords({ scenario: "rw.x" }))
    ).toBeGreaterThan(0)
  })
})
