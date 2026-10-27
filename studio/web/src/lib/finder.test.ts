import { describe, expect, it } from "vitest"

import {
  acceptSelection,
  EXACT_VALUE,
  exactRows,
  exactSessions,
  exactTraces,
  lookupsOf,
  metaKeywords,
  movesSelection,
  paletteFilter,
  TAG_VALUE_HINT,
  tagHintFor,
} from "./finder"

describe("lookupsOf", () => {
  it("reads an id prefix, a public id and a tag; spaces and short text ask nothing", () => {
    expect(lookupsOf("1c06797f")).toEqual({
      idPrefix: "1c06797f",
      publicId: "1c06797f",
    })
    // An app-chosen public id (no pub_): looked up as one too.
    expect(lookupsOf("arena:run-1:rw.deep-delegation")).toEqual({
      idPrefix: "arena:run-1:rw.deep-delegation",
      publicId: "arena:run-1:rw.deep-delegation",
    })
    expect(lookupsOf("pub_")).toEqual({ idPrefix: "pub_", publicId: "pub_" })
    expect(lookupsOf("a=")).toEqual({})
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

describe("acceptSelection and the tag hint (P8-fix-b)", () => {
  it("takes cmdk's pick only for an empty box, an exact match, or the user's own move", () => {
    expect(acceptSelection({ text: "", exactCount: 0, moved: false })).toBe(
      true
    )
    expect(
      acceptSelection({ text: "director", exactCount: 0, moved: false })
    ).toBe(false)
    expect(
      acceptSelection({ text: "director", exactCount: 0, moved: true })
    ).toBe(true)
    expect(
      acceptSelection({ text: "1c06797f", exactCount: 1, moved: false })
    ).toBe(true)
    expect(movesSelection({ key: "ArrowDown" })).toBe(true)
    expect(movesSelection({ key: "n", ctrlKey: true })).toBe(true)
    expect(movesSelection({ key: "n" })).toBe(false)
    expect(movesSelection({ key: "Enter" })).toBe(false)
  })
  it("says tags need key=value under a bare word nothing exact answers", () => {
    expect(tagHintFor(lookupsOf("rw.deep-delegation"), 0, true)).toBe(
      TAG_VALUE_HINT
    )
    expect(tagHintFor(lookupsOf("rw.deep-delegation"), 1, true)).toBeUndefined()
    expect(
      tagHintFor(lookupsOf("rw.deep-delegation"), 0, false)
    ).toBeUndefined()
    expect(tagHintFor(lookupsOf("k=v"), 0, true)).toBeUndefined()
    expect(tagHintFor(lookupsOf("two words"), 0, true)).toBeUndefined()
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

describe("exactSessions and exactTraces (U30.2, UX-A-13)", () => {
  it("keeps a session whose id starts with the text or whose public id is it, the exact id first", () => {
    const rows = [
      { id: "aa5e55", public_id: "" },
      { id: "5e55a0b1", public_id: "" },
      { id: "5e55", public_id: "" },
      { id: "s_x", public_id: "5e55" },
    ]
    expect(exactSessions(lookupsOf("5e55"), rows).map((r) => r.id)).toEqual([
      "5e55",
      "5e55a0b1",
      "s_x",
    ])
    expect(exactSessions(lookupsOf("5e5"), rows)).toEqual([])
  })
  it("keeps a trace only when its id starts with the text", () => {
    const rows = [{ trace_id: "7ace0d1e" }, { trace_id: "d7ace0d1" }]
    expect(exactTraces(lookupsOf("7ace"), rows)).toEqual([rows[0]])
    expect(exactTraces(lookupsOf("a=b"), rows)).toEqual([])
  })
})
