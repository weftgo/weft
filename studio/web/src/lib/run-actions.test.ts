import { describe, expect, it } from "vitest"

import {
  DELETE_OFF,
  deleteConfirmWords,
  deleteOffWhy,
  exportFormatsFor,
} from "./run-actions"

const READ = `weft_pt.${btoa(JSON.stringify({ scope: "read" }))}.sig`
const PLAY = `weft_pt.${btoa(JSON.stringify({ scope: "playground" }))}.sig`

describe("exportFormatsFor", () => {
  it("json, jsonl and otlp under export; a read token no otlp; none without the capability", () => {
    expect(exportFormatsFor(["export"], "").map((f) => f.format)).toEqual([
      "json",
      "jsonl",
      "otlp",
    ])
    expect(exportFormatsFor(["export"], READ).map((f) => f.format)).toEqual([
      "json",
      "jsonl",
    ])
    expect(exportFormatsFor([], "")).toEqual([])
  })
})

describe("deleteOffWhy", () => {
  it("is mayDelete's rule: the capability, then never a panel token, then live_all", () => {
    expect(deleteOffWhy(["delete"], "", true)).toBeUndefined()
    expect(deleteOffWhy(["delete"], "dev", true)).toBeUndefined()
    expect(deleteOffWhy([], "", true)).toBe(DELETE_OFF.capability)
    expect(deleteOffWhy([], "", true, "flag --x")).toBe(
      `${DELETE_OFF.capability} — flag --x`
    )
    expect(deleteOffWhy(["delete"], READ, true)).toBe(DELETE_OFF.panel)
    expect(deleteOffWhy(["delete"], PLAY, true)).toBe(DELETE_OFF.panel)
    expect(deleteOffWhy(["delete"], "", false)).toBe(DELETE_OFF.host)
  })
  it("the confirm names the children and says what stays", () => {
    expect(deleteConfirmWords({ id: "r", children: [] }).children).toBe(
      "It has no subagent runs."
    )
    expect(
      deleteConfirmWords({ id: "r", children: [{ id: "a" }, { id: "b" }] })
        .children
    ).toBe("Its 2 subagent runs go with it:")
    expect(deleteConfirmWords({ id: "r", children: [] }).body).toMatch(
      /studio's database only/
    )
  })
})
