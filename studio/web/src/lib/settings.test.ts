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
  isDate,
  maskToken,
  pricingFile,
  pricingFileText,
  problems,
  tokenIdentity,
} from "./settings"

const served = golden<PricingSettings>("settings-pricing")

describe("the pricing draft", () => {
  it("round-trips the served table into the file shape, less source and path", () => {
    const f = pricingFile(draftOf(served), served.note)
    const { source: _s, path: _p, ...want } = served
    expect(f).toEqual(want)
    expect(pricingFileText(f!).endsWith("}\n")).toBe(true)
    expect(Object.keys(f!)).toEqual(["dated", "note", "models"])
    expect(Object.keys(f!.models[0])).toEqual(["provider", "model_glob", "input", "output", "cached_input"])
  })

  it("names every field the loader would refuse; no file until they are fixed", () => {
    const d = draftOf(served)
    d.dated = "2026-02-30"
    d.rows[0].input = "-1"
    d.rows[1].output = ""
    d.rows[1].reasoning = "abc"
    d.rows.push({ ...emptyRow(), provider: "x", model_glob: "[a", input: "1", output: "1" })
    expect(problems(d).map((p) => `${p.row}:${p.field}`)).toEqual([
      "-1:dated",
      "0:input",
      "1:output",
      "1:reasoning",
      "2:model_glob",
    ])
    expect(pricingFile(d)).toBeNull()
  })

  it("keys edits by the served table's source and dated", () => {
    expect(editsKey(served)).toBe("studio.pricing.edits:option:2026-10-01")
  })
})

describe("globError is path.Match's ErrBadPattern", () => {
  it.each(["*", "claude-*", "a/b?", "[abc]", "[^a-z]x", "\\*", "[\\]]"])("%s is good", (p) => {
    expect(globError(p)).toBe("")
  })
  it.each(["[", "[]", "[a", "[z-a]", "[-a]", "ab\\", "[a-]"])("%s is bad", (p) => {
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
  const claims = (o: object) => `weft_pt.${btoa(JSON.stringify(o)).replace(/=+$/, "")}.sig`
  const db = { kind: "sqlite" }
  it("reads a panel token's scope and public id from its claims", () => {
    const id = tokenIdentity(claims({ public_id: "pub_1", scope: "read", exp: "2026-10-11T00:00:00Z" }), {
      auth_required: true,
      db,
    })
    expect(id).toMatchObject({ kind: "panel-read", publicId: "pub_1", exp: "2026-10-11T00:00:00Z" })
    expect(tokenIdentity(claims({ scope: "playground" }), { auth_required: true, db }).kind).toBe("panel-playground")
  })
  it("any other bearer on a walled Studio is the server token; confirmed by db.path's rule", () => {
    expect(tokenIdentity("dev", { auth_required: true, live_all: true, db }).words).toContain("reads everything")
    expect(tokenIdentity("dev", { auth_required: true, db }).words).toContain("by its shape")
    expect(tokenIdentity("dev", { auth_required: false, db }).kind).toBe("unused")
    expect(tokenIdentity("", { auth_required: false, db }).kind).toBe("none")
  })
  it("masks without a character of the token", () => {
    expect(maskToken("dev-secret-1234")).not.toMatch(/[a-z0-9]/)
    expect(maskToken("")).toBe("")
  })
})
