import { describe, expect, it } from "vitest"

import {
  childrenUnder,
  curlFor,
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
      "No child run is known under it; any recorded under it goes with it."
    )
    expect(
      deleteConfirmWords({ id: "r", children: [{ id: "a" }, { id: "b" }] })
        .children
    ).toBe("It goes with every child run under it, at any depth (2 known):")
    // P8-fix-b #2: the server deletes the whole closure — a child's own
    // children count, as a floor (their children are not read here).
    expect(
      deleteConfirmWords({
        id: "r",
        children: [{ id: "a", children_count: 3 }, { id: "b" }],
      }).children
    ).toBe("It goes with every child run under it, at any depth (5+ known):")
    expect(deleteConfirmWords({ id: "r", children: [] }).derived).toMatch(
      /Derived runs read from its tools' spans/
    )
    expect(childrenUnder(1)).toBe("(+1 child run under it)")
    expect(childrenUnder(3)).toBe("(+3 child runs under it)")
    expect(deleteConfirmWords({ id: "r", children: [] }).body).toMatch(
      /studio's database only/
    )
  })
})

describe("curlFor (U30.2, UX-A-15)", () => {
  it("names $WEFT_STUDIO_TOKEN where the API is walled, never a token; the route and tree=1 as the page fetches them", () => {
    expect(
      curlFor({
        apiBase: "http://127.0.0.1:7331/studio/api/",
        runID: "r/0/call 1",
        format: "jsonl",
        tree: true,
        authRequired: true,
      })
    ).toBe(
      `curl -fsSL -OJ -H "Authorization: Bearer $WEFT_STUDIO_TOKEN" 'http://127.0.0.1:7331/studio/api/runs/r%2F0%2Fcall%201/export?format=jsonl&tree=1'`
    )
    expect(
      curlFor({ apiBase: "http://h/api/", runID: "r'x", format: "json" })
    ).toBe(`curl -fsSL -OJ 'http://h/api/runs/r%27x/export?format=json'`)
  })
})
