// lib/settings.ts (plan H4): the pricing draft checked against
// studio.LoadPricing's rules, the override file's exact shape, and
// what the stored bearer is.
import { describe, expect, it } from "vitest"

import { golden } from "@/test/fake-studio"
import type { PricingSettings } from "./api"
import {
  draftOf,
  editsKey,
  emptyRow,
  globError,
  globEscape,
  isDate,
  maskToken,
  pricingFile,
  pricingFileText,
  problems,
  tokenIdentity,
  withPriceRow,
} from "./settings"

const served = golden<PricingSettings>("settings-pricing")

describe("the pricing draft", () => {
  it("round-trips the served table into the file shape, less source and path", () => {
    const f = pricingFile(draftOf(served), served.note)
    const { source: _s, path: _p, ...want } = served
    expect(f).toEqual(want)
    expect(pricingFileText(f!).endsWith("}\n")).toBe(true)
    expect(Object.keys(f!)).toEqual(["dated", "note", "models"])
    expect(Object.keys(f!.models[0])).toEqual([
      "provider",
      "model_glob",
      "input",
      "output",
      "cached_input",
    ])
  })

  it("names every field the loader would refuse; no file until they are fixed", () => {
    const d = draftOf(served)
    d.dated = "2026-02-30"
    d.rows[0].input = "-1"
    d.rows[1].output = ""
    d.rows[1].reasoning = "abc"
    d.rows.push({
      ...emptyRow(),
      provider: "x",
      model_glob: "[a",
      input: "1",
      output: "1",
    })
    expect(problems(d).map((p) => `${p.row}:${p.field}`)).toEqual([
      "-1:dated",
      "0:input",
      "1:output",
      "1:reasoning",
      "2:model_glob",
    ])
    expect(pricingFile(d)).toBeNull()
  })

  it("keys edits by the served table's source, file path and dated", () => {
    expect(editsKey(served)).toBe(
      `studio.pricing.edits:option:${served.path ?? ""}:2026-10-01`
    )
    // Two --pricing files with the same dated never share a draft.
    const a = editsKey({
      source: "file",
      path: "/etc/a.json",
      dated: "2026-10-01",
    })
    const b = editsKey({
      source: "file",
      path: "/etc/b.json",
      dated: "2026-10-01",
    })
    expect(a).not.toBe(b)
    expect(editsKey({ source: "default", dated: "2026-10-01" })).toBe(
      "studio.pricing.edits:default::2026-10-01"
    )
  })

  it("a rate is a decimal: JS's 0x10, 0b11, Infinity and 1_000 forms are refused, never rewritten", () => {
    for (const bad of [
      "0x10",
      "0b11",
      "0o7",
      "Infinity",
      "1_000",
      ".5",
      "5.",
      " 1e",
    ]) {
      const d = draftOf(served)
      d.rows[0].input = bad
      expect(
        problems(d).map((p) => `${p.row}:${p.field}`),
        bad
      ).toEqual(["0:input"])
    }
    for (const good of ["0", "3", "0.25", "1.5e-1", "2E3"]) {
      const d = draftOf(served)
      d.rows[0].input = good
      expect(problems(d), good).toEqual([])
      expect(pricingFile(d)!.models[0].input).toBe(Number(good))
    }
  })
})

describe("globError is path.Match's ErrBadPattern", () => {
  // [z-a]: Go's path.Match("openai/[z-a]", "") is nil — out of order
  // matches nothing, and is no ErrBadPattern.
  it.each([
    "*",
    "claude-*",
    "a/b?",
    "[abc]",
    "[^a-z]x",
    "\\*",
    "[\\]]",
    "[z-a]",
  ])("%s is good", (p) => {
    expect(globError(p)).toBe("")
  })
  it.each(["[", "[]", "[a", "[-a]", "ab\\", "[a-]"])("%s is bad", (p) => {
    expect(globError(p)).not.toBe("")
  })
})

describe("isDate", () => {
  it("is YYYY-MM-DD and a real day", () => {
    expect(isDate("2026-10-10")).toBe(true)
    expect(isDate("2024-02-29")).toBe(true)
    expect(isDate("2026-02-29")).toBe(false)
    expect(isDate("2026-1-1")).toBe(false)
  })
})

describe("tokenIdentity", () => {
  const claims = (o: object) =>
    `weft_pt.${btoa(JSON.stringify(o)).replace(/=+$/, "")}.sig`
  const db = { kind: "sqlite" }
  it("reads a panel token's scope and public id from its claims", () => {
    const id = tokenIdentity(
      claims({
        public_id: "pub_1",
        scope: "read",
        exp: "2026-10-11T00:00:00Z",
      }),
      {
        auth_required: true,
        db,
      }
    )
    expect(id).toMatchObject({
      kind: "panel-read",
      publicId: "pub_1",
      exp: "2026-10-11T00:00:00Z",
    })
    expect(
      tokenIdentity(claims({ scope: "playground" }), {
        auth_required: true,
        db,
      }).kind
    ).toBe("panel-playground")
  })
  it("any other bearer on a walled Studio is the server token; confirmed by db.path's rule", () => {
    expect(
      tokenIdentity("dev", { auth_required: true, live_all: true, db }).words
    ).toContain("reads everything")
    expect(tokenIdentity("dev", { auth_required: true, db }).words).toContain(
      "by its shape"
    )
    expect(tokenIdentity("dev", { auth_required: false, db }).kind).toBe(
      "unused"
    )
    expect(tokenIdentity("", { auth_required: false, db }).kind).toBe("none")
  })
  it("masks without a character of the token", () => {
    expect(maskToken("dev-secret-1234")).not.toMatch(/[a-z0-9]/)
    expect(maskToken("")).toBe("")
  })
})

describe("the ?price= prefill (U5U7-fix 8d)", () => {
  it("escapes path.Match's metacharacters: the row prices the name alone, a good pattern", () => {
    expect(globEscape("a*b?c[d]\\e")).toBe("a\\*b\\?c\\[d]\\\\e")
    const d = withPriceRow(draftOf(served), "acme/model[v2]*")
    expect(d.rows[0]).toMatchObject({
      provider: "acme",
      model_glob: "model\\[v2]\\*",
    })
    expect(
      problems(d).filter((p) => p.row === 0 && p.field === "model_glob")
    ).toEqual([])
    expect(globError(`acme/${d.rows[0].model_glob}`)).toBe("")
    // A plain name is unchanged, and a second prefill adds nothing.
    const plain = withPriceRow(draftOf(served), "openai/kimi-k2.7-code")
    expect(plain.rows[0]).toMatchObject({
      provider: "openai",
      model_glob: "kimi-k2.7-code",
    })
    expect(withPriceRow(plain, "openai/kimi-k2.7-code").rows.length).toBe(
      plain.rows.length
    )
  })
})
