// P9-trim's property lever (scripts/panel-mangle.ts): the member names
// panel.js renames are the bundle's own, proven from the source; every
// name a host, the npm entry, a response, the DOM or a second copy of
// panel.js reads stays verbatim in the bytes that ship.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { beforeAll, describe, expect, it } from "vitest"
import { analyzeMangle } from "../../scripts/panel-mangle"
import type { MangleAnalysis } from "../../scripts/panel-mangle"

let a: MangleAnalysis
beforeAll(() => {
  a = analyzeMangle()
}, 60_000)

const code = () =>
  readFileSync(resolve(process.cwd(), "../dist/panel/panel.js"), "utf8")

// What reaches panel.js from outside it, by name: the host API (C4), the
// npm entry's and main.ts's duck-typed calls (another copy may answer
// them), the custom element callbacks, the built bundle's own tests.
const VERBATIM = [
  "open",
  "close",
  "toggle",
  "isOpen",
  "scope",
  "select",
  "on",
  "studioLink",
  "api",
  "options",
  "npmEntry",
  "autoMounted",
  "rescan",
  "syncGlobal",
  "apiObjIs",
  "detectWord",
  "markerScopes",
  "connectedCallback",
  "disconnectedCallback",
  "attributeChangedCallback",
  "observedAttributes",
  "fire",
]

describe("the mangled member names (scripts/panel-mangle.ts)", () => {
  it("are the element's private members, the internal classes' and the internal types' — and nothing a host, a wire or a lib names", () => {
    expect(a.names.length).toBeGreaterThan(200)
    // A private member of the element, of PanelModel, of PanelState, of
    // ExperimentResult, of HeaderRungOptions.
    for (const n of [
      "lookupSession",
      "turnList",
      "rescopeSoon",
      "tooNew",
      "commandID",
      "isNativeFetch",
    ])
      expect(a.names).toContain(n)
    for (const n of VERBATIM) expect(a.names).not.toContain(n)
    // A private name that a response, an ES lib or a wire type also
    // spells stays: one occurrence elsewhere keeps it.
    for (const n of ["push", "keys", "state", "model", "body", "start"])
      expect(a.names).not.toContain(n)
    expect(a.kept.push).toMatch(/lib\.es5\.d\.ts/)
    expect(a.kept.fire).toMatch(/^reserved/)
    // Read off another element (LIVE's panels): another copy of panel.js
    // may name it differently, so it stays.
    expect(a.kept.dormant).toMatch(/another instance/)
  })

  it("never emits a short name the element inherits from HTMLElement", () => {
    expect(a.reserved).toEqual(expect.arrayContaining(["dir", "id"]))
    expect(a.reserved.every((n) => n.length <= 3)).toBe(true)
  })

  it("the committed panel.js carries none of them as a member and every verbatim one", () => {
    const js = code()
    const member = (n: string) =>
      new RegExp(`this\\.${n.replace(/\$/g, "\\$")}(?![\\w$])`).test(js)
    expect(a.names.filter((n) => n.length >= 4 && member(n))).toEqual([])
    for (const n of [
      "studioLink",
      "isOpen",
      "syncGlobal",
      "apiObjIs",
      "rescan",
    ])
      expect(js).toContain(n)
    expect(js).toContain(".fire(`run`,{")
  })
})
