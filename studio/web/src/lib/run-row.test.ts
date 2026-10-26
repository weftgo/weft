// A run row's lineage and fields (U21.1, UX-F-06): one set of words for
// Studio's runs list and the panel's turn chips (parity.test.ts draws
// both).
import { describe, expect, it } from "vitest"

import { forkSource, lineageOf, sourceLabel } from "./run-row"

const row = (o: {
  forked_from?: string
  experiment_id?: string
  meta?: Record<string, string>
}) => ({
  forked_from: "",
  experiment_id: "",
  meta: {},
  ...o,
})

describe("lineageOf", () => {
  it("a replay names its source's turn and step; a thread fork its entry", () => {
    const runs = [{ id: "r_src", turn: 3 }]
    expect(
      lineageOf(row({ forked_from: "r_src#1", experiment_id: "x1" }), runs)
    ).toMatchObject([
      {
        kind: "replay",
        text: "↻ replay of t3 · step 1",
        src: "r_src",
        step: 1,
      },
    ])
    expect(
      lineageOf(row({ meta: { "weft.session.forked_from": "s_01#e_12" } })).map(
        (l) => l.text
      )
    ).toEqual(["⑃ fork of s_01#e_12"])
    expect(lineageOf(row({ experiment_id: "x2" }))).toMatchObject([
      { kind: "experiment", text: "experiment" },
    ])
    expect(lineageOf(row({}))).toEqual([])
  })

  it("sourceLabel: t<n> when known, a thread turn's id, else the short id", () => {
    expect(sourceLabel("s_01-t4")).toBe("t4")
    expect(sourceLabel("r_1", [{ id: "r_1", turn: 2 }])).toBe("t2")
    expect(sourceLabel("1c06797f5df3b4e908d846af2227721d")).toBe(
      "1c06797f…721d"
    )
  })

  it("forkSource reads the step after the last #", () => {
    expect(forkSource({ forked_from: "a#b#2" })).toEqual({
      runID: "a#b",
      fromStep: 2,
    })
    expect(forkSource({ forked_from: "a" })).toEqual({
      runID: "a",
      fromStep: 0,
    })
    expect(forkSource({ forked_from: "" })).toBeNull()
  })
})
