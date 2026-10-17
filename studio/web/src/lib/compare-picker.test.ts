// lib/compare-picker.ts (H9; P8-fix-b #13): ?b= read in every form, an
// array's elements kept whole; the find box's queries; the order the
// picker offers — found, replay source, same conversation, same agent.
import { describe, expect, it } from "vitest"

import type { RunRow } from "./api"
import {
  WHY_AGENT,
  WHY_FOUND,
  WHY_PUBLIC,
  candidates,
  findQueries,
  splitRuns,
} from "./compare-picker"

const run = (id: string, over: Partial<RunRow> = {}) =>
  ({ id, agent: "a", public_id: "", ...over }) as RunRow

describe("splitRuns", () => {
  it("keeps an array's elements whole, parses a JSON array, splits the comma form", () => {
    expect(splitRuns(["r,1", "r2"])).toEqual(["r,1", "r2"])
    expect(splitRuns('["r,1","r2"]')).toEqual(["r,1", "r2"])
    expect(splitRuns("r1,r2")).toEqual(["r1", "r2"])
    expect(splitRuns("r1")).toEqual(["r1"])
    expect(splitRuns("[not json")).toEqual(["[not json"])
    expect(splitRuns(undefined)).toEqual([])
    expect(splitRuns(7)).toEqual([7])
  })
})

describe("findQueries", () => {
  it("asks by id prefix and agent from two characters, never with a space", () => {
    expect(findQueries("1c")).toEqual({ id_prefix: "1c", agent: "1c" })
    expect(findQueries("1")).toBeNull()
    expect(findQueries("two words")).toBeNull()
  })
})

describe("candidates", () => {
  it("found first, then the conversation's runs, then the agent's, never the base", () => {
    const base = run("b", { agent: "x", public_id: "pub_c" })
    const picks = candidates(
      [run("n1"), run("n2", { agent: "x" }), base],
      base,
      [],
      { found: [run("deep")], samePublic: [run("t2", { public_id: "pub_c" })] }
    )
    expect(picks.map((p) => [p.run.id, p.why])).toEqual([
      ["deep", WHY_FOUND],
      ["t2", WHY_PUBLIC],
      ["n2", WHY_AGENT],
      ["n1", undefined],
    ])
    expect(
      candidates([run("n1")], undefined, [], { found: [run("deep")] }).map(
        (p) => p.run.id
      )
    ).toEqual(["deep", "n1"])
  })
})
