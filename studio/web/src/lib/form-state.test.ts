// The playground's form as a value (plan F5): the share link's two
// codecs and its cap, the reader's refusals, a cell's command id, a
// saved experiment read back as the form, and the one-cell re-run.
import { describe, expect, it, vi } from "vitest"

import type { AgentView, ExperimentRow } from "./api"
import { golden } from "@/test/fake-studio"
import { emptyLab, overridesOf } from "./experiment-body"
import {
  cellBody,
  cellCommandID,
  cellKey,
  DECODE_CAP,
  decodeForm,
  decodeFormOutcome,
  draftRefusedWords,
  encodeForm,
  FORM_VERSION,
  LINK_REFUSED,
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
import {
  deleteDraft,
  listDrafts,
  loadDraft,
  loadDraftOutcome,
  resetDrafts,
  saveDraft,
} from "./drafts"
import { experiment } from "@/test/experiment-fixture"

const form: FormState = {
  v: FORM_VERSION,
  agent: "orders",
  run: "r_ok",
  step: 2,
  edits: [{ kind: "tool_result", step: 1, callID: "c2", toolResult: "none" }],
  variants: [
    {
      key: "A",
      instructions: "Be brief.",
      tools_off: ["refund"],
      engine: "scripted",
    },
    {
      key: "terse",
      model: "glm",
      side_effects: "park",
      lab: { temperature: "0.5", park_on: ["refund"] },
    },
  ],
  matrix: {
    name: "exp_tone",
    inputs: [
      { key: "1", text: "where is 42?" },
      { key: "2", text: "" },
    ],
  },
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
    expect(
      await decodeForm((await encodeForm(form, { codec: "j" })).value)
    ).toEqual(form)
  })

  it("a form past the cap is said to be too long, never cut", async () => {
    // Random words deflate badly: well past SHARE_CAP once encoded.
    const words = Array.from(
      { length: 4000 },
      (_, i) => `${i.toString(36)}${Math.random().toString(36).slice(2, 6)}`
    )
    const big: FormState = {
      ...form,
      variants: [{ key: "A", instructions: words.join(" ") }],
    }
    const enc = await encodeForm(big)
    expect(enc.value.length).toBeGreaterThan(SHARE_CAP)
    expect(enc.tooLong).toBe(true)
  })

  it("a value longer than a share link carries is refused unread", async () => {
    expect(await decodeForm("j" + "A".repeat(SHARE_CAP))).toBeNull()
  })

  it("a deflate bomb is cut off past DECODE_CAP, before it is parsed", async () => {
    // A long run of zeros deflates ~1000:1: a short value, a huge body.
    const zeros = new Uint8Array(DECODE_CAP * 4)
    const cs = new CompressionStream("deflate-raw")
    const z = new Uint8Array(
      await new Response(
        new Response(zeros).body!.pipeThrough(cs)
      ).arrayBuffer()
    )
    let bin = ""
    for (const b of z) bin += String.fromCharCode(b)
    const value =
      "z" + btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
    expect(value.length).toBeLessThan(SHARE_CAP)
    const parse = vi.spyOn(JSON, "parse")
    expect(await decodeForm(value)).toBeNull()
    // Nothing the size of the inflated body reached the parser.
    expect(
      parse.mock.calls.some(
        ([t]) => typeof t === "string" && t.length >= DECODE_CAP
      )
    ).toBe(false)
    parse.mockRestore()
  })

  it("a damaged, cut, foreign or newer value reads as nothing", async () => {
    const enc = await encodeForm(form)
    expect(
      await decodeForm(enc.value.slice(0, enc.value.length / 2))
    ).toBeNull()
    expect(await decodeForm("x" + enc.value.slice(1))).toBeNull()
    expect(await decodeForm("z!!!")).toBeNull()
    expect(await decodeForm("")).toBeNull()
    const newer = (
      await encodeForm({ ...form, v: 3 } as unknown as FormState, {
        codec: "j",
      })
    ).value
    expect(await decodeForm(newer)).toBeNull()
  })

  it("says why it read nothing: an older (v1) form names the way out, a newer one and a damaged one their own (P8-fix-b #3)", async () => {
    const v1 = { v: 1, step: 2, variants: [{ key: "A" }] }
    const older = (await encodeForm(v1 as unknown as FormState, { codec: "j" }))
      .value
    expect(await decodeFormOutcome(older)).toEqual({ refused: "older" })
    expect(await decodeForm(older)).toBeNull()
    const newer = (
      await encodeForm({ ...form, v: 3 } as unknown as FormState, {
        codec: "j",
      })
    ).value
    expect(await decodeFormOutcome(newer)).toEqual({ refused: "newer" })
    expect(await decodeFormOutcome("z!!!")).toEqual({ refused: "unreadable" })
    const ok = (await encodeForm(form)).value
    expect(await decodeFormOutcome(ok)).toEqual({ form })
    expect(LINK_REFUSED.older).toMatch(/re-create it from the experiment/)
    expect(LINK_REFUSED.newer).toMatch(/newer Studio/)
    expect(LINK_REFUSED.unreadable).not.toMatch(/newer/)
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
        v: 2,
        agent: 7,
        variants: [
          {
            key: "A",
            engine: "warp",
            tools_off: [1],
            lab: { temperature: "0.3", bogus: "x" },
            extra: true,
          },
          { key: "A" },
          { nokey: true },
        ],
        matrix: {
          inputs: [
            { key: "1", text: "hi" },
            { key: "1", text: "dup" },
          ],
          from_step: -1,
        },
      })
    ).toEqual({
      v: 2,
      variants: [{ key: "A", lab: { temperature: "0.3" } }],
      matrix: { inputs: [{ key: "1", text: "hi" }] },
    })
    expect(validateForm({ v: 2, variants: [] })).toBeNull()
    expect(validateForm({ v: 3, variants: [{ key: "A" }] })).toBeNull()
    // Form v1 carried two steps (Run's and the matrix's): refused, never
    // read with one of them dropped (U26; UX-C-21).
    expect(
      validateForm({
        v: 1,
        step: 2,
        variants: [{ key: "A" }],
        matrix: { from_step: 0, inputs: [{ key: "1", text: "" }] },
      })
    ).toBeNull()
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
          // No engine picked: the per-command choice (U17), not written.
          sideEffects: "substitute",
          thread: "ephemeral",
          lab: emptyLab(),
        },
      ],
      matrix: { inputs: [{ key: "1", text: "" }] },
    })
    expect(f).toEqual({
      v: 2,
      variants: [{ key: "A" }],
      matrix: { inputs: [{ key: "1", text: "" }] },
    })
  })
})

describe("a cell's command id", () => {
  it("carries the cell's address and side-effect mode, within the server's id rule", () => {
    const id = cellCommandID("A", "1", "park", "n0nce")!
    expect(id).toMatch(/^[A-Za-z0-9._:-]{1,128}$/)
    expect(parseCellCommandID(id)).toEqual({
      variant: "A",
      input: "1",
      sideEffects: "park",
    })
    // Any key text survives (base64url).
    const odd = cellCommandID("tone ✓/x", "row.2", "allow")!
    expect(parseCellCommandID(odd)).toEqual({
      variant: "tone ✓/x",
      input: "row.2",
      sideEffects: "allow",
    })
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
    expect(v).toEqual({
      key: "B",
      instructions: "Be brief.",
      tools_on: ["lookup_order"],
      lab: { temperature: "0.5" },
    })
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
    expect(
      [...p.cells.entries()].map(([k, l]) => [k, l.map((r) => r.id)])
    ).toEqual([
      [cellKey("A", "1"), ["pg_a1"]],
      [cellKey("A", "2"), ["pg_a2"]],
      [cellKey("B", "1"), ["pg_b1"]],
      [cellKey("B", "2"), ["pg_b2"]],
    ])
    expect(p.byOrder).toBe(false)
    const old = {
      ...experiment,
      runs: experiment.runs!.map((r) => ({ ...r, meta: {} })),
    }
    const q = placeRuns(old)
    expect(q.byOrder).toBe(true)
    expect(q.cells.get(cellKey("B", "1"))?.[0].id).toBe("pg_b1")
    const partial = { ...old, runs: old.runs.slice(0, 3) }
    expect(placeRuns(partial).loose.map((r) => r.id)).toEqual([
      "pg_a1",
      "pg_a2",
      "pg_b1",
    ])
  })

  it("formFromExperiment: the variants, the rows, the source, the page's one step and each variant's modes", () => {
    expect(formFromExperiment(experiment)).toEqual({
      v: 2,
      agent: "orders",
      run: "r_ok",
      step: 1,
      variants: [
        // A's runs ran live: a matrix saved before U17 stays live
        // (U18.2-fix), never the per-command choice.
        { key: "A", engine: "live" },
        {
          key: "B",
          instructions: "Be brief.",
          tools_on: ["lookup_order"],
          lab: { temperature: "0.5" },
          engine: "scripted",
          side_effects: "park",
        },
      ],
      matrix: {
        name: "exp_tone",
        id: "exp_tone",
        inputs: [
          { key: "1", text: "where is 42?" },
          { key: "2", text: "" },
        ],
      },
    })
  })

  it("prefers the saved command (F5.1) over what the runs say, per field", () => {
    const saved = golden<ExperimentRow>("experiment")
    const f = formFromExperiment(saved)
    // The golden's runs say nothing a heuristic could read; the command does.
    expect(f.matrix).toMatchObject({
      name: "tracking link",
      id: "exp_g",
    })
    expect(f.step).toBe(1)
    expect(f.run).toBe("r_ok")
    // A saved engine is a pick (U17): kept, live included.
    expect(f.variants.find((v) => v.key === "A")).toEqual({
      key: "A",
      engine: "live",
    })
    expect(f.variants.find((v) => v.key === "B")).toMatchObject({
      engine: "scripted",
      side_effects: "park",
    })
    expect(f.edits).toEqual([
      {
        kind: "tool_result",
        step: 0,
        callID: "call_1",
        toolResult: "order 42: lost",
      },
    ])
    // A saved field wins over the runs' reading: the fixture's runs say
    // B ran scripted and parked — the saved command says live, allow.
    const over = formFromExperiment({
      ...experiment,
      engine: "live",
      side_effects: "allow",
      from_step: 0,
      variants: experiment.variants.map((v) => ({ ...v, engine: "live" })),
    })
    expect(over.variants.find((v) => v.key === "B")).toMatchObject({
      side_effects: "allow",
    })
    // A saved engine is a pick (U17), live included.
    expect(over.variants.find((v) => v.key === "B")?.engine).toBe("live")
    expect(over.step).toBeUndefined()
    // Absent fields fall back to the runs (a definition before F5.1).
    expect(formFromExperiment(experiment).variants[1]).toMatchObject({
      engine: "scripted",
      side_effects: "park",
    })
  })

  it("keeps an id that fits command_id's rule and drops one that does not (re-minted)", () => {
    const bad = formFromExperiment({
      ...experiment,
      id: "my experiment/1",
      name: "my experiment/1",
    })
    expect(bad.matrix?.name).toBe("my experiment/1")
    expect(bad.matrix?.id).toBeUndefined()
    expect(
      validateForm({
        ...form,
        matrix: { inputs: [{ key: "1", text: "" }], name: "a b", id: "a b" },
      })?.matrix
    ).toEqual({
      inputs: [{ key: "1", text: "" }],
      name: "a b",
    })
  })

  it("cellBody with no saved engine takes the cell's own choice (U17): scripted when it can answer, live otherwise", () => {
    // The API golden: A sends no override, row 1 is the source's own
    // turn, row 2 a new input, B's overrides change the key.
    const g = golden<ExperimentRow>("experiment")
    const bare: ExperimentRow = {
      ...g,
      engine: undefined,
      transcript_edits: undefined,
      from_step: 0,
      variants: g.variants.map((v) => ({ ...v, engine: undefined })),
      runs: [],
    }
    const f = formFromExperiment(bare)
    // A: no override, the source's own turn (row 1 has no text).
    expect(
      cellBody(bare, f, "A", "1", { runtime: "rt_1", steps: 2 }).engine
    ).toBe("scripted")
    // A new input: live.
    expect(
      cellBody(bare, f, "A", "2", { runtime: "rt_1", steps: 2 }).engine
    ).toBe("live")
    // B's overrides change the key: live.
    expect(
      cellBody(bare, f, "B", "1", { runtime: "rt_1", steps: 2 }).engine
    ).toBe("live")
  })

  it("cellBody re-runs one cell: its overrides as saved, its source, the same experiment id, a cell command id", () => {
    const f = formFromExperiment(experiment)
    const body = cellBody(experiment, f, "B", "2", {
      runtime: "rt_1",
      publicID: "pub_1",
    })
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
    expect(parseCellCommandID(body.command_id)).toEqual({
      variant: "B",
      input: "2",
      sideEffects: "park",
    })
    const at0 = cellBody(experiment, { ...f, step: 0 }, "A", "1", {
      runtime: "rt_1",
    })
    expect(at0.input).toBe("where is 42?")
    // The page's edits travel with every cell past step 0 (U26).
    const edited = cellBody(
      experiment,
      {
        ...f,
        edits: [
          { kind: "tool_result", step: 0, callID: "c1", toolResult: "x" },
        ],
      },
      "A",
      "1",
      { runtime: "rt_1" }
    )
    expect(edited.transcript_edits).toHaveLength(1)
    expect(at0.transcript_edits).toBeUndefined()
    expect(() =>
      cellBody(experiment, f, "Z", "1", { runtime: "rt_1" })
    ).toThrow(/no cell Z×1/)
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
    expect((await listDrafts("experiment")).map((d) => d.id)).toEqual([
      "exp:exp_tone",
    ])
    await deleteDraft("d_1")
    expect(await loadDraft("d_1")).toBeNull()
    resetDrafts()
  })
})

describe("loadDraftOutcome (P8-fix-b #3)", () => {
  it("a draft kept in form v1 reads older, with the way out; none here reads missing", async () => {
    resetDrafts()
    await saveDraft("d_old", {
      v: 1,
      step: 2,
      variants: [{ key: "A" }],
    } as unknown as FormState)
    expect(await loadDraftOutcome("d_old")).toEqual({ refused: "older" })
    expect(await loadDraft("d_old")).toBeNull()
    expect(await loadDraftOutcome("d_none")).toEqual({ refused: "missing" })
    expect(draftRefusedWords("d_old", "older")).toMatch(
      /^draft d_old is from an older Studio.*re-create it from the experiment/
    )
    expect(draftRefusedWords("d_none", "missing")).toMatch(
      /^draft d_none is not in this browser/
    )
    resetDrafts()
  })
})
