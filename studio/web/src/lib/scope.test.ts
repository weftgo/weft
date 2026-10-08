// The Scope marker's one string form (plan §13.3, C1): public id
// first, then session=, flow=, run= in that order — the format C3.1's
// Go side must read and write the same way.
import { describe, expect, it } from "vitest"
import { parseScope, serializeScope } from "./scope"

describe("serializeScope", () => {
  it("writes the public id bare, then session, flow, run in that order", () => {
    expect(serializeScope({ publicId: "pub_1" })).toBe("pub_1")
    expect(serializeScope({ run: "r_9", flow: "f_3", session: "s_2", publicId: "pub_1" })).toBe(
      "pub_1;session=s_2;flow=f_3;run=r_9"
    )
    expect(serializeScope({ publicId: "pub_1", run: "r_9" })).toBe("pub_1;run=r_9")
  })

  it("an empty optional field is unset; an empty public id stays first", () => {
    expect(serializeScope({ publicId: "pub_1", session: "" })).toBe("pub_1")
    expect(serializeScope({ publicId: "", session: "s_2" })).toBe(";session=s_2")
  })

  it("percent-encodes what would break the marker, and only that kind of thing", () => {
    expect(serializeScope({ publicId: "a;b", session: "x=y" })).toBe("a%3Bb;session=x%3Dy")
    expect(serializeScope({ publicId: "pub_01J-x.y~z" })).toBe("pub_01J-x.y~z")
  })

  it("never throws on a string encodeURIComponent refuses (a lone surrogate): the delimiters are still encoded", () => {
    expect(() => serializeScope({ publicId: "\uD800" })).not.toThrow()
    expect(serializeScope({ publicId: "a;\uD800", run: "=%" })).toBe("a%3B\uD800;run=%3D%25")
    expect(parseScope(serializeScope({ publicId: "a;\uD800", run: "=%" }))).toEqual({ publicId: "a;\uD800", run: "=%" })
  })
})

describe("parseScope", () => {
  it("round-trips every shape serializeScope writes", () => {
    for (const s of [
      { publicId: "pub_1" },
      { publicId: "pub_1", session: "s_2" },
      { publicId: "pub_1", session: "s_2", flow: "f_3", run: "r_9" },
      { publicId: "", run: "r_9" },
      { publicId: "a;b=c", flow: "f;=" },
    ])
      expect(parseScope(serializeScope(s))).toEqual(s)
  })

  it("ignores unknown keys, repeats and segments without '=', and never throws", () => {
    expect(parseScope("pub_1;tenant=acme;session=s_2;session=s_3;junk;run=")).toEqual({
      publicId: "pub_1",
      session: "s_2",
    })
    expect(parseScope("")).toEqual({ publicId: "" })
    expect(parseScope("%E0%A4%A")).toEqual({ publicId: "%E0%A4%A" })
  })
})
