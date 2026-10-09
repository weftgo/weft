// The replay safety rules as data (plan F1, lib/replay.ts): the ack
// preview's verdict per tool, mirroring runtime/registry.go's
// allowedTools and executor.go's substitute chain — pinned over every
// mode × class × allow × breakpoint × approval combination that
// changes the answer — and the verbs' pre-filled drafts, which both
// surfaces turn into the same command.
import { describe, expect, it } from "vitest"

import {
  breakpointsFor,
  canReplay,
  continueHere,
  editPromptAndReplay,
  editResultAndReplay,
  prefixLine,
  replayFromStep,
  replayVerdicts,
  rerun,
  stepPositionOf,
  tokenScopeOf,
} from "./replay"
import type { CatalogTool, ReplayVerdict, SideEffectsMode } from "./replay"
import type { AgentView } from "./api"

const catalog: CatalogTool[] = [
  { name: "lookup", replay: "never", approval: false }, // explicit never
  { name: "search", replay: "safe", approval: false }, // vouched safe
  { name: "refund", replay: "", approval: false }, // unannotated, opted in
  { name: "email", replay: "", approval: false }, // unannotated, not opted in
  { name: "submit_output", replay: "", approval: false }, // the Output submission
  { name: "transfer", replay: "never", approval: true }, // RequireApproval
  { name: "check", replay: "safe", approval: true }, // safe + RequireApproval
]

const agent: Pick<AgentView, "tools"> = {
  tools: [
    { name: "lookup", side_effects: "never", allow: false },
    { name: "search", side_effects: "safe", allow: false },
    { name: "refund", side_effects: "never", allow: true },
    { name: "email", side_effects: "never", allow: false },
    { name: "submit_output", side_effects: "never", allow: false },
    { name: "transfer", side_effects: "never", allow: false },
    { name: "check", side_effects: "safe", allow: false },
  ],
}

const SUB = "substituted from the recorded result when the call repeats, else parked"

type Row = [string, ReplayVerdict, string, boolean?]

const TABLE: Record<"substitute" | "park" | "allow", Row[]> = {
  substitute: [
    ["lookup", "substituted", `replay never · ${SUB}`],
    ["search", "runs", "replay safe · runs"],
    // In substitute mode an opted-in tool is a side effect like any
    // other: AllowSideEffects only counts under "allow".
    ["refund", "substituted", `replay never (unannotated) · ${SUB}`],
    ["email", "substituted", `replay never (unannotated) · ${SUB}`],
    ["submit_output", "runs", "structured output · runs"],
    ["transfer", "substituted", `requires approval · ${SUB}`],
    ["check", "substituted", `requires approval · ${SUB}`],
  ],
  park: [
    ["lookup", "parked", "replay never · parks (side effects: park)"],
    ["search", "runs", "replay safe · runs"],
    ["refund", "parked", "replay never (unannotated) · parks (side effects: park)"],
    ["email", "parked", "replay never (unannotated) · parks (side effects: park)"],
    ["submit_output", "runs", "structured output · runs"],
    ["transfer", "parked", "requires approval · parks at the approval boundary"],
    ["check", "parked", "requires approval · parks at the approval boundary"],
  ],
  allow: [
    [
      "lookup",
      "parked",
      "replay never · not opted in by AllowSideEffects: side effects allow is refused while it is on",
      true,
    ],
    ["search", "runs", "replay safe · runs"],
    ["refund", "runs", "opted in by AllowSideEffects · runs for real"],
    [
      "email",
      "parked",
      "replay never (unannotated) · not opted in by AllowSideEffects: side effects allow is refused while it is on",
      true,
    ],
    ["submit_output", "runs", "structured output · runs"],
    ["transfer", "parked", "requires approval · parks at the approval boundary", true],
    ["check", "parked", "requires approval · parks at the approval boundary"],
  ],
}

const shape = (rows: Row[]) =>
  rows.map(([name, verdict, why, refuses]) => ({ name, verdict, why, ...(refuses ? { refuses } : {}) }))

describe("replayVerdicts (allowedTools' rule, as the ack preview words it)", () => {
  for (const mode of ["substitute", "park", "allow"] as const) {
    it(`side effects ${mode}`, () => {
      expect(replayVerdicts({ catalog, agent, mode })).toEqual(shape(TABLE[mode]))
    })
  }

  it('the default mode "" is substitute', () => {
    expect(replayVerdicts({ catalog, agent, mode: "" })).toEqual(shape(TABLE.substitute))
  })

  it("a breakpoint parks whatever the mode and the class", () => {
    for (const mode of ["", "substitute", "park", "allow"] as SideEffectsMode[]) {
      const out = replayVerdicts({ catalog, agent, mode, breakpoints: ["search", "lookup", "refund"] })
      for (const name of ["search", "lookup", "refund"])
        expect(out.find((v) => v.name === name)).toEqual({ name, verdict: "parked", why: "breakpoint · parks" })
    }
  })

  it("a breakpoint the agent does not have cannot fire (breakpointTools restricts it)", () => {
    const out = replayVerdicts({
      catalog: [{ name: "child_tool", replay: "safe", approval: false }],
      agent,
      mode: "park",
      breakpoints: ["child_tool"],
    })
    expect(out[0].verdict).toBe("runs")
  })

  it("a tool switched off in tools_enabled is off", () => {
    const out = replayVerdicts({ catalog, agent, mode: "allow", toolsEnabled: ["search", "refund"] })
    expect(out.filter((v) => v.verdict !== "off").map((v) => v.name)).toEqual(["search", "refund"])
    expect(out.find((v) => v.name === "email")).toEqual({ name: "email", verdict: "off", why: "off" })
    // Nothing left on is refused: allow is acceptable now.
    expect(out.some((v) => v.refuses)).toBe(false)
  })

  it("an empty tools_enabled is not an override (the wire reads it so): every tool on", () => {
    expect(replayVerdicts({ catalog, agent, mode: "park", toolsEnabled: [] })).toEqual(shape(TABLE.park))
  })

  it("the runtime's registration decides the class; without one the catalog's stands", () => {
    // Registered never although the catalog said safe: parks.
    const reg = { tools: [{ name: "search", side_effects: "never", allow: false }] }
    expect(
      replayVerdicts({ catalog: [catalog[1]], agent: reg, mode: "park" })[0].verdict
    ).toBe("parked")
    // No registration: the catalog's safe runs, its never parks.
    expect(replayVerdicts({ catalog, mode: "park" }).map((v) => v.verdict)).toEqual([
      "parked",
      "runs",
      "parked",
      "parked",
      "runs",
      "parked",
      "parked",
    ])
  })

  it("a catalog tool the runtime does not register (a ToolSource's, a child's) parks, and says so", () => {
    const out = replayVerdicts({
      catalog: [{ name: "dynamic", replay: "", approval: false }],
      agent,
      mode: "substitute",
    })
    expect(out[0]).toEqual({
      name: "dynamic",
      verdict: "substituted",
      why: `replay never (unannotated) · not registered on the runtime · ${SUB}`,
    })
  })

  it("breakpointsFor keeps the stored set the agent can fire", () => {
    expect(breakpointsFor({ breakpoints: ["lookup", "elsewhere"] }, agent)).toEqual(["lookup"])
    expect(breakpointsFor(undefined, agent)).toEqual([])
  })
})

describe("the verbs' drafts (one command on both surfaces)", () => {
  it("replay from this step keeps steps 0..n-1", () => {
    expect(replayFromStep(2)).toEqual({ verb: "from_step", fromStep: 2, edits: [], thread: "ephemeral", input: "" })
  })

  it("edit this result and replay runs the NEXT step fresh against the edit", () => {
    expect(editResultAndReplay(2, "c7", "boom")).toEqual({
      verb: "edit_result",
      fromStep: 3,
      edits: [{ step: 2, callID: "c7", toolResult: "boom" }],
      thread: "ephemeral",
      input: "",
      focus: "edit",
    })
  })

  it("edit the prompt and replay opens on the prompt, pre-filled when the surface has the text", () => {
    expect(editPromptAndReplay(1, "You are terse.")).toEqual({
      verb: "edit_prompt",
      fromStep: 1,
      edits: [],
      instructions: "You are terse.",
      thread: "ephemeral",
      input: "",
      focus: "prompt",
    })
    expect(editPromptAndReplay(1).instructions).toBeUndefined()
  })

  it("re-run is from_step 0; continue here is a fork with an empty input to type", () => {
    expect(rerun()).toEqual({ verb: "rerun", fromStep: 0, edits: [], thread: "ephemeral", input: "" })
    expect(continueHere()).toEqual({
      verb: "continue",
      fromStep: 0,
      edits: [],
      thread: "fork",
      input: "",
      focus: "input",
    })
  })

  it("a step number that is not a whole position becomes 0, never a negative from_step", () => {
    expect(replayFromStep(-1).fromStep).toBe(0)
    expect(replayFromStep(1.5).fromStep).toBe(0)
  })

  it("stepPositionOf maps an ordinal to its position; -1 for one the run lacks", () => {
    // A fold that lacks step 1 (its events were lost): ordinal 2 is
    // position 1 — from_step counts positions.
    expect(stepPositionOf([0, 2, 3], 2)).toBe(1)
    expect(stepPositionOf([0, 2, 3], 1)).toBe(-1)
    expect(stepPositionOf([0, 1, 2], 2)).toBe(2)
  })
})

describe("prefixLine", () => {
  it("says what is kept, and when the kept prefix is the compacted one", () => {
    expect(prefixLine(0, false)).toBe("nothing kept · the whole turn runs again")
    expect(prefixLine(1, false)).toBe("step 0 kept")
    expect(prefixLine(3, false)).toBe("steps 0–2 kept")
    expect(prefixLine(3, true)).toBe("steps 0–2 kept · the compacted prefix (what the model saw at step 3)")
  })
})

describe("canReplay (the panel's canAct rule)", () => {
  const panelToken = (scope: string) =>
    `weft_pt.${btoa(JSON.stringify({ public_id: "pub_1", scope }))
      .replace(/=+$/, "")
      .replace(/\+/g, "-")
      .replace(/\//g, "_")}.sig`

  it("needs the playground capability and a bearer that may act", () => {
    expect(canReplay(["playground"], "")).toBe(true)
    expect(canReplay(["playground"], "dev-secret")).toBe(true)
    expect(canReplay(["playground"], panelToken("playground"))).toBe(true)
    expect(canReplay(["playground"], panelToken("read"))).toBe(false)
    expect(canReplay(["live"], "")).toBe(false)
  })

  it("an unreadable panel token is the default mint: read", () => {
    expect(tokenScopeOf("weft_pt.!!!.sig")).toBe("read")
    expect(tokenScopeOf("plain")).toBe("")
  })
})
