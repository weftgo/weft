// P9-trim's property lever (scripts/panel-mangle.ts): the member names
// panel.js renames are the bundle's own, proven from the source; every
// name a host, the npm entry, a response, the DOM or a second copy of
// panel.js reads stays verbatim in the bytes that ship.
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join, resolve } from "node:path"
import { beforeAll, describe, expect, it } from "vitest"
import { analyzeMangle } from "../../scripts/panel-mangle"
import type { MangleAnalysis } from "../../scripts/panel-mangle"

let a: MangleAnalysis
beforeAll(() => {
  a = analyzeMangle()
}, 60_000)

const code = () =>
  readFileSync(resolve(process.cwd(), "../dist/panel/panel.js"), "utf8")

// The stored layout, localStorage["weft.devtools"] (layout.ts readStore
// / writeStore: a document another panel.js version reads back), and the
// host events' details (weft:run, weft:parked, weft:error —
// element.ts DevtoolsEvents): keys the shipped bundle spells.
const STORED = [
  "v",
  "mode",
  "side",
  "open",
  "hidden",
  "x",
  "y",
  "w",
  "h",
  "d",
  "run",
  "theme",
  "raw",
  "tab",
  "debug",
]
const DETAILS = [
  "runId",
  "status",
  "publicId",
  "sessionId",
  "step",
  "callId",
  "ackId",
  "name",
  "message",
]

// What reaches panel.js from outside it, by name: the host API (C4), the
// npm entry's and main.ts's duck-typed calls (another copy may answer
// them), the custom element callbacks, the built bundle's own tests,
// the stored layout and the host events' details.
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
  ...STORED,
  ...DETAILS,
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
    // Each stored and each event-detail key, as an object literal's key.
    const key = (n: string) => new RegExp(`[{,]${n}:`).test(js)
    expect([...STORED, ...DETAILS].filter((n) => !key(n))).toEqual([])
  })
})

// checkFlows: the value-level guard. Each case is a small module graph
// of its own, analyzed as the build analyzes src/panel/main.ts; the
// analysis must throw, naming what the case does.
describe("an internal type reached by value (checkFlows)", () => {
  const TYPES = `
export interface TreeState { applied: string; open: Set<string> }
export interface ExperimentResult { commandID: string; runs: string[] }
export interface PanelState { turnList: string[]; tooNew: boolean }
export interface TurnView { folded: number }
export interface Cursor { cursorAt: number }
export class PanelModel { rowOf(id: string) { return id } }
export function treeView(root: unknown, st: TreeState, name: string): string {
  return String(root) + st.applied + name
}
`
  // A wire shape that shares an internal type's name (P9-trim2-fix 1):
  // the lists name declarations, so this one is not internal.
  const WIRE = `export interface Cursor { before: string; beforeId: string }
`
  const HEAD = `import { treeView } from "./types"
import type { TreeState, ExperimentResult, PanelState, TurnView, PanelModel, Cursor } from "./types"
import type { Cursor as WireCursor } from "./wire"
declare const s: string
declare const state: PanelState
declare const tree: TreeState
declare const turn: TurnView
declare const model: PanelModel
declare const cur: Cursor
declare const el: HTMLElement
declare function fmt(v: unknown): string
declare function fmtAny(v: any): string
declare function fmtObj(v: object): string
declare function fmtEmpty(v: {}): string
declare function fmtGen<T>(v: T): string
declare function fmtRest(...vs: unknown[]): string
declare function panelGet<T>(path: string): Promise<T>
export {}
`
  const LISTS = {
    types: [
      "types.ts#TreeState",
      "types.ts#ExperimentResult",
      "types.ts#PanelState",
      "types.ts#TurnView",
      "types.ts#Cursor",
    ],
    classes: ["types.ts#PanelModel"],
  }
  const analyze = (body: string, lists = LISTS) => {
    const dir = mkdtempSync(join(tmpdir(), "panel-mangle-"))
    try {
      writeFileSync(join(dir, "types.ts"), TYPES)
      writeFileSync(join(dir, "wire.ts"), WIRE)
      writeFileSync(join(dir, "main.ts"), HEAD + body)
      return analyzeMangle(join(dir, "main.ts"), dir, { ...lists, base: dir })
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  }

  it.each([
    // (a) any into an internal type's place.
    ["const t: TreeState = JSON.parse(s)", /any fills TreeState/],
    [
      "export async function f(r: Response) { const x: ExperimentResult = await r.json(); return x }",
      /any fills ExperimentResult/,
    ],
    [
      "export async function f(r: Response): Promise<ExperimentResult> { return r.json() }",
      /any fills ExperimentResult/,
    ],
    [
      "export const g = (u: string) => fetch(u).then((r) => r.json()).then((x: ExperimentResult) => x)",
      /parameter typed ExperimentResult is given any/,
    ],
    ["let v: TurnView[] = []; v = JSON.parse(s); void v", /any fills TurnView/],
    [
      "const t = JSON.parse(s) as unknown as TreeState; void t",
      /asserts a value to TreeState/,
    ],
    [
      "const t = JSON.parse(s) as { st: PanelState }; void t",
      /asserts a value to PanelState/,
    ],
    // (b) reflection and serialization sinks.
    ["JSON.stringify(state)", /PanelState handed to JSON\.stringify/],
    [
      "JSON.stringify({ at: 1, turns: [turn] })",
      /TurnView \(via \.turns\) handed to JSON\.stringify/,
    ],
    ["structuredClone(tree)", /TreeState handed to structuredClone/],
    [
      'window.postMessage(state, "*")',
      /PanelState handed to window\.postMessage/,
    ],
    ["Object.keys(state)", /PanelState handed to Object\.keys/],
    ["Object.entries(tree)", /TreeState handed to Object\.entries/],
    ["Object.values(turn)", /TurnView handed to Object\.values/],
    ["Object.assign({}, state)", /PanelState handed to Object\.assign/],
    [
      'localStorage.setItem("k", state as unknown as string & PanelState)',
      /PanelState handed to localStorage\.setItem/,
    ],
    [
      'el.dispatchEvent(new CustomEvent("x", { detail: state }))',
      /PanelState \(via \.detail\) handed to new CustomEvent/,
    ],
    ['treeView(state, tree, "x")', /PanelState handed to treeView/],
    ["JSON.stringify(model)", /PanelModel handed to JSON\.stringify/],
    // (c) for…in, and a spread into a shape no internal type describes.
    ["for (const k in state) void k", /for…in over PanelState/],
    [
      "const o: Record<string, unknown> = { ...state }; void o",
      /PanelState spread into an object no internal type describes/,
    ],
    // (d) P9-trim2-fix 2a: an argument to an opaque parameter.
    ["fmt(state)", /PanelState passed to fmt's parameter typed unknown/],
    ["fmtAny(tree)", /TreeState passed to fmtAny's parameter typed any/],
    ["fmtObj(turn)", /TurnView passed to fmtObj's parameter typed object/],
    ["fmtEmpty(cur)", /Cursor passed to fmtEmpty's parameter typed \{\}/],
    [
      "fmtGen({ at: 1, views: [turn] })",
      /TurnView \(via \.views\) passed to fmtGen's parameter typed type parameter T/,
    ],
    [
      'fmtRest("x", model)',
      /PanelModel passed to fmtRest's parameter typed unknown/,
    ],
    [
      "console.log(state)",
      /PanelState passed to console\.log's parameter typed any/,
    ],
    ["String(cur)", /Cursor passed to String's parameter typed any/],
    // (e) 2b: a return through the callee's type parameter.
    [
      'export const f = () => panelGet<ExperimentResult>("x")',
      /ExperimentResult returned through panelGet's type parameter T/,
    ],
    [
      'export async function f() { const r: PanelState = await panelGet("x"); return r }',
      /PanelState returned through panelGet's type parameter T/,
    ],
    // (f) 2c: more reflection.
    ["Reflect.ownKeys(state)", /PanelState handed to Reflect\.ownKeys/],
    [
      "Object.getOwnPropertyNames(tree)",
      /TreeState handed to Object\.getOwnPropertyNames/,
    ],
    // (g) 2d: a type guard that makes one of an outside value.
    [
      "export function isState(v: unknown): v is PanelState { return !!v }",
      /type guard makes PanelState/,
    ],
    [
      "export function isCur(v: WireCursor): v is WireCursor & Cursor { return !!v }",
      /type guard makes Cursor/,
    ],
    [
      "export function must(v: unknown): asserts v is TurnView[] { if (!v) throw 0 }",
      /type guard makes TurnView/,
    ],
    // (h) P9-pre-web-fix 1: widened first, then sunk — an internal value
    // held in an opaque declared or contextual type.
    [
      "const u: unknown = state; JSON.stringify(u)",
      /PanelState widened to unknown/,
    ],
    ["let u: unknown; u = tree; void u", /TreeState widened to unknown/],
    [
      "export function f(): unknown { return turn }",
      /TurnView widened to unknown/,
    ],
    [
      "const o: Record<string, unknown> = { g: state }; void o",
      /PanelState \(via \.g\) widened to Record<string, unknown>/,
    ],
    ["const a: unknown[] = [cur]; void a", /Cursor widened to unknown/],
    ["const u: object = state; Object.keys(u)", /PanelState widened to object/],
    ["void `${tree as unknown}`", /TreeState widened to unknown/],
    ["void `${state}`", /PanelState interpolated into a template/],
    ["export const f = (): {} => model", /PanelModel widened to \{\}/],
  ])("%s", (body, want) => {
    expect(() => analyze(body)).toThrow(want)
  })

  it("names an internal type by its declaration: a same-named wire shape stays verbatim and may be parsed", () => {
    const r = analyze(`
const w: WireCursor = JSON.parse(s)
const n: number = cur.cursorAt
void w.before, w.beforeId, n
`)
    expect(r.names).toContain("cursorAt")
    expect(r.names).not.toContain("before")
    expect(r.names).not.toContain("beforeId")
  })

  it("fails on a listed declaration the graph does not hold (a moved or renamed type)", () => {
    expect(() =>
      analyze("void 0", { ...LISTS, types: [...LISTS.types, "wire.ts#Lane"] })
    ).toThrow(/does not hold[\s\S]*wire\.ts#Lane/)
    // A bare name is no key.
    expect(() =>
      analyze("void 0", { ...LISTS, classes: ["PanelModel"] })
    ).toThrow(/does not hold[\s\S]*PanelModel/)
  })

  it("lets the standard containers hold and return an internal value", () => {
    const r = analyze(`
export async function f() {
  const m = new Map<string, TurnView>()
  m.set("a", turn).set("b", turn)
  const got: TurnView | undefined = m.get("a")
  const set = new Set([turn])
  set.add(turn)
  const all: TurnView[] = await Promise.all([Promise.resolve(turn), turn])
  const xs: PanelState[] = [state].map((x) => x).filter((x) => !!x)
  const w = new WeakMap<object, TreeState>([[el, tree]])
  return [got, set, all, xs, w, Array.from(set)]
}
export function isTree(v: TreeState | TurnView): v is TreeState { return "applied" in v }
fmt(turn.folded)
fmt({ folded: turn.folded })
const held: TurnView[] = [turn]
const byID: Record<string, TurnView> = { a: turn }
void held, byID, \`\${turn.folded}\`
`)
    expect(r.names).toEqual(
      expect.arrayContaining(["cursorAt", "tooNew", "turnList"])
    )
  })

  it("passes code that builds the value and reads it by its own names", () => {
    const r = analyze(`
const next: PanelState = { ...state, tooNew: true }
const t: TreeState = { applied: "", open: new Set() }
treeView(JSON.parse(s), t, "raw")
treeView({ folded: turn.folded }, tree, "turn")
JSON.stringify({ folded: turn.folded })
for (const k in JSON.parse(s)) void k
void next, model.rowOf(s), el
`)
    expect(r.names).toEqual(
      expect.arrayContaining(["applied", "rowOf", "tooNew", "turnList"])
    )
  })

  it("the panel's own graph passes it (the build's analysis threw nothing)", () => {
    expect(a.names.length).toBeGreaterThan(200)
  })
})
