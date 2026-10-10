// The playground's form as a value (plan F5): the share link's two
// codecs and its cap, the reader's refusals, a cell's command id, a
// saved experiment read back as the form, and the one-cell re-run.
import { describe, expect, it, vi } from "vitest"

import type { AgentView } from "./api"
import { emptyLab, overridesOf } from "./experiment-body"
import {
  cellBody,
  cellCommandID,
  cellKey,
  decodeForm,
  encodeForm,
  FORM_VERSION,
  formFromExperiment,
  formOfFields,
  parseCellCommandID,
  placeRuns,
  SHARE_CAP,
  stateFromHash,
  validateForm,
  variantFromOverrides,
} from "./form-state"
import type { FormState } from "./form-state"
import { deleteDraft, listDrafts, loadDraft, resetDrafts, saveDraft } from "./drafts"
import { experiment } from "@/test/experiment-fixture"

const form: FormState = {
  v: FORM_VERSION,
  agent: "orders",
  run: "r_ok",
  step: 2,
  edits: [{ kind: "tool_result", step: 1, callID: "c2", toolResult: "none" }],
  variants: [
    { key: "A", instructions: "Be brief.", tools_off: ["refund"], engine: "scripted" },
    { key: "terse", model: "glm", side_effects: "park", lab: { temperature: "0.5", park_on: ["refund"] } },
  ],
  matrix: { name: "exp_tone", from_step: 1, inputs: [{ key: "1", text: "where is 42?" }, { key: "2", text: "" }] },
}

describe("the share link (#state=)", () => {
  it("deflated where the platform can, and read back to the same form", async () => {
    const enc = await encodeForm(form)
    expect(enc.codec).toBe("z")
    expect(enc.value).toMatch(/^z[A-Za-z0-9_-]+$/)
    expect(enc.tooLong).toBe(false)
    expect(await decodeForm(enc.value)).toEqual(form)
  })

  it("without CompressionStream it is the JSON, base64url — still read back", async () => {
    vi.stubGlobal("CompressionStream", undefined)
    try {
      const enc = await encodeForm(form)
      expect(enc.codec).toBe("j")
      expect(enc.value.startsWith("j")).toBe(true)
      expect(await decodeForm(enc.value)).toEqual(form)
    } finally {
      vi.unstubAllGlobals()
    }
    // A link made that way opens on any browser.
    expect(await decodeForm((await encodeForm(form, { codec: "j" })).value)).toEqual(form)
  })

  it("a form past the cap is said to be too long, never cut", async () => {
    // Random words deflate badly: well past SHARE_CAP once encoded.
    const words = Array.from({ length: 4000 }, (_, i) => `${i.toString(36)}${Math.random().toString(36).slice(2, 6)}`)
    const big: FormState = { ...form, variants: [{ key: "A", instructions: words.join(" ") }] }
    const enc = await encodeForm(big)
    expect(enc.value.length).toBeGreaterThan(SHARE_CAP)
    expect(enc.tooLong).toBe(true)
  })

  it("a damaged, cut, foreign or newer value reads as nothing", async () => {
    const enc = await encodeForm(form)
    expect(await decodeForm(enc.value.slice(0, enc.value.length / 2))).toBeNull()
    expect(await decodeForm("x" + enc.value.slice(1))).toBeNull()
    expect(await decodeForm("z!!!")).toBeNull()
    expect(await decodeForm("")).toBeNull()
    const newer = (await encodeForm({ ...form, v: 2 } as unknown as FormState, { codec: "j" })).value
    expect(await decodeForm(newer)).toBeNull()
  })

  it("is the fragment's state= key", () => {
    expect(stateFromHash("#state=zAbc")).toBe("zAbc")
    expect(stateFromHash("run=r_1&state=jXy")).toBe("jXy")
    expect(stateFromHash("#run=r_1")).toBeUndefined()
    expect(stateFromHash("")).toBeUndefined()
  })
})

describe("validateForm", () => {
  it("keeps known keys of the right shape, drops the rest, refuses a form without a variant", () => {
    expect(
      validateForm({
        v: 1,
        agent: 7,
        variants: [
          { key: "A", engine: "warp", tools_off: [1], lab: { temperature: "0.3", bogus: "x" }, extra: true },
          { key: "A" },
          { nokey: true },
        ],
        matrix: { inputs: [{ key: "1", text: "hi" }, { key: "1", text: "dup" }], from_step: -1 },
      })
    ).toEqual({ v: 1, variants: [{ key: "A", lab: { temperature: "0.3" } }], matrix: { inputs: [{ key: "1", text: "hi" }] } })
    expect(validateForm({ v: 1, variants: [] })).toBeNull()
    expect(validateForm({ v: 2, variants: [{ key: "A" }] })).toBeNull()
    expect(validateForm(null)).toBeNull()
  })
})

describe("formOfFields", () => {
  it("writes only what differs from the defaults; a prompt equal to the registered one is left out", () => {
    const f = formOfFields({
      registered: "You are support.",
      variants: [
        {
          key: "A",
          instructions: "You are support.",
          toolsOff: new Set(),
          model: "",
          thinking: "",
          input: "",
          engine: "live",
          sideEffects: "substitute",
          thread: "ephemeral",
          lab: emptyLab(),
        },
      ],
      matrix: { inputs: [{ key: "1", text: "" }] },
    })
    expect(f).toEqual({ v: 1, variants: [{ key: "A" }], matrix: { inputs: [{ key: "1", text: "" }] } })
  })
})

describe("a cell's command id", () => {
  it("carries the cell's address and side-effect mode, within the server's id rule", () => {
    const id = cellCommandID("A", "1", "park", "n0nce")!
    expect(id).toMatch(/^[A-Za-z0-9._:-]{1,128}$/)
    expect(parseCellCommandID(id)).toEqual({ variant: "A", input: "1", sideEffects: "park" })
    // Any key text survives (base64url).
    const odd = cellCommandID("tone ✓/x", "row.2", "allow")!
    expect(parseCellCommandID(odd)).toEqual({ variant: "tone ✓/x", input: "row.2", sideEffects: "allow" })
    // Too long for the id: none (the server mints one).
    expect(cellCommandID("V".repeat(100), "1", "substitute")).toBeUndefined()
    expect(parseCellCommandID("cmd_123")).toBeNull()
    expect(parseCellCommandID(undefined)).toBeNull()
  })
})

const orders: AgentView = {
  name: "orders",
  models: ["glm"],
  tools: [
    { name: "lookup_order", side_effects: "safe", allow: false },
    { name: "refund", side_effects: "never", allow: false },
  ],
  instructions: "You are support.",
}

describe("a saved experiment read back", () => {
  it("variantFromOverrides is overridesOf's inverse", () => {
    const v = variantFromOverrides("B", experiment.variants[1].overrides)
    expect(v).toEqual({ key: "B", instructions: "Be brief.", tools_on: ["lookup_order"], lab: { temperature: "0.5" } })
    const back = overridesOf(
      {
        instructions: v.instructions!,
        toolsOff: new Set(["refund"]),
        model: "",
        thinking: "",
        lab: { ...emptyLab(), ...v.lab },
      },
      orders
    )
    expect(back).toEqual(experiment.variants[1].overrides)
  })

  it("places runs by their command ids; a pre-F5 grid by issue order; the rest are loose", () => {
    const p = placeRuns(experiment)
    expect([...p.cells.entries()].map(([k, l]) => [k, l.map((r) => r.id)])).toEqual([
      [cellKey("A", "1"), ["pg_a1"]],
      [cellKey("A", "2"), ["pg_a2"]],
      [cellKey("B", "1"), ["pg_b1"]],
      [cellKey("B", "2"), ["pg_b2"]],
    ])
    expect(p.byOrder).toBe(false)
    const old = { ...experiment, runs: experiment.runs!.map((r) => ({ ...r, meta: {} })) }
    const q = placeRuns(old)
    expect(q.byOrder).toBe(true)
    expect(q.cells.get(cellKey("B", "1"))?.[0].id).toBe("pg_b1")
    const partial = { ...old, runs: old.runs.slice(0, 3) }
    expect(placeRuns(partial).loose.map((r) => r.id)).toEqual(["pg_a1", "pg_a2", "pg_b1"])
  })

  it("formFromExperiment: the variants, the rows, the source, the matrix's step and each variant's modes", () => {
    expect(formFromExperiment(experiment)).toEqual({
      v: 1,
      agent: "orders",
      run: "r_ok",
      variants: [
        { key: "A" },
        { key: "B", instructions: "Be brief.", tools_on: ["lookup_order"], lab: { temperature: "0.5" }, engine: "scripted", side_effects: "park" },
      ],
      matrix: {
        name: "exp_tone",
        from_step: 1,
        inputs: [
          { key: "1", text: "where is 42?" },
          { key: "2", text: "" },
        ],
      },
    })
  })

  it("cellBody re-runs one cell: its overrides as saved, its source, the same experiment id, a cell command id", () => {
    const f = formFromExperiment(experiment)
    const body = cellBody(experiment, f, "B", "2", { runtime: "rt_1", publicID: "pub_1" })
    expect(body).toMatchObject({
      runtime: "rt_1",
      agent: "orders",
      source: { run_id: "r_ok", from_step: 1 },
      overrides: experiment.variants[1].overrides,
      engine: "scripted",
      side_effects: "park",
      thread: "ephemeral",
      experiment_id: "exp_tone",
      public_id: "pub_1",
    })
    // Past step 0 the cell continues the kept prefix: no input.
    expect(body).not.toHaveProperty("input")
    expect(parseCellCommandID(body.command_id)).toEqual({ variant: "B", input: "2", sideEffects: "park" })
    const at0 = cellBody(experiment, { ...f, matrix: { ...f.matrix!, from_step: 0 } }, "A", "1", { runtime: "rt_1" })
    expect(at0.input).toBe("where is 42?")
    expect(() => cellBody(experiment, f, "Z", "1", { runtime: "rt_1" })).toThrow(/no cell Z×1/)
  })
})

describe("drafts (no IndexedDB here: the in-memory stand-in)", () => {
  it("save, load, list newest first by kind, delete", async () => {
    resetDrafts()
    await saveDraft("d_1", form, { label: "one" })
    await new Promise((r) => setTimeout(r, 5))
    await saveDraft("d_2", { ...form, agent: "planner" })
    await saveDraft("exp:exp_tone", form, { kind: "experiment" })
    expect((await loadDraft("d_1"))?.form).toEqual(form)
    expect((await listDrafts()).map((d) => d.id)).toEqual(["d_2", "d_1"])
    expect((await listDrafts("experiment")).map((d) => d.id)).toEqual(["exp:exp_tone"])
    await deleteDraft("d_1")
    expect(await loadDraft("d_1")).toBeNull()
    resetDrafts()
  })
})
