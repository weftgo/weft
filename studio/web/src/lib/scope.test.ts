// The Scope marker's one string form (plan §13.3, C1): public id
// first, then session=, flow=, run= in that order. The cases live in
// studio/testdata/scope.golden.json, which the Go side
// (scope/scope_test.go, C3.1) reads too — one fixture, two
// serialisers, no drift.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"
import { parseScope, serializeScope } from "./scope"
import type { Scope } from "./scope"

interface Case {
  scope: Scope
  string: string
}
const golden = JSON.parse(
  readFileSync(resolve(process.cwd(), "../testdata/scope.golden.json"), "utf8")
) as { roundtrip: Case[]; serialize: Case[]; parse: Case[] }

describe("the shared golden (studio/testdata/scope.golden.json)", () => {
  it("has every section, and a round-trip case with a flow field", () => {
    expect(golden.roundtrip.length).toBeGreaterThan(0)
    expect(golden.serialize.length).toBeGreaterThan(0)
    expect(golden.parse.length).toBeGreaterThan(0)
    expect(golden.roundtrip.some((c) => c.scope.flow)).toBe(true)
  })

  it.each(golden.roundtrip)("round-trips $string", (c) => {
    expect(serializeScope(c.scope)).toBe(c.string)
    expect(parseScope(c.string)).toEqual(c.scope)
  })

  it.each(golden.serialize)(
    "serialises to $string (empty fields are unset)",
    (c) => {
      expect(serializeScope(c.scope)).toBe(c.string)
    }
  )

  it.each(golden.parse)("parses $string leniently", (c) => {
    expect(parseScope(c.string)).toEqual(c.scope)
  })
})

// A lone surrogate cannot cross the golden (Go decodes it to U+FFFD);
// the Go side pins its analogue, invalid UTF-8, in its own test.
describe("serializeScope", () => {
  it("never throws on a string encodeURIComponent refuses (a lone surrogate): the delimiters are still encoded", () => {
    expect(() => serializeScope({ publicId: "\uD800" })).not.toThrow()
    expect(serializeScope({ publicId: "a;\uD800", run: "=%" })).toBe(
      "a%3B\uD800;run=%3D%25"
    )
    expect(
      parseScope(serializeScope({ publicId: "a;\uD800", run: "=%" }))
    ).toEqual({ publicId: "a;\uD800", run: "=%" })
  })
  it("encodes control characters in that fallback too (no raw CR/LF in a header), and still round-trips", () => {
    const s = { publicId: "pub\uD800\r\nX: y", run: "\u0000\t\u007f\uDFFF" }
    expect(serializeScope(s)).toBe("pub\uD800%0D%0AX: y;run=%00%09%7F\uDFFF")
    expect(parseScope(serializeScope(s))).toEqual(s)
  })
})
