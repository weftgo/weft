// The engine per command (U17; UX-C-01, UX-D-04, UX-C-05, UX-C-08) on
// rw.deep-delegation's director run: two recorded steps, its model
// wefttest/script, step 1 the reply after ops_manager's call_1.
import { describe, expect, it } from "vitest"

import {
  COST_STEPS,
  engineChoice,
  engineHeld,
  engineOf,
  ENGINE_WORDS,
  factsOf,
  modelLabel,
  noEdit,
  runLabel,
  sameModel,
  stepsOf,
} from "./engine"
import type { EngineFacts, StepCost } from "./engine"
import { overridesOf, realEdits } from "./experiment-body"
import type { VariantFields } from "./experiment-body"
import { DRAWER_ORDER, DRAWER_TITLES } from "./drawer-spec"

const step = (input: number, output: number, usd: number): StepCost => ({
  usage: { input_tokens: input, output_tokens: output },
  cost_usd: usd,
  cost_known: true,
  cache: "exact",
})

/** A plain replay of the director from step 1 (rw.deep-delegation). */
const plain: EngineFacts = {
  source: true,
  edits: 0,
  prompt: false,
  model: "",
  input: false,
  fork: false,
  keyed: [],
  fromStep: 1,
  steps: 2,
  ownModel: "wefttest/script",
  costs: [step(900, 300, 0.0004)],
}

describe("engineChoice", () => {
  it("a plain replay opens on scripted, Run names it", () => {
    const c = engineChoice(plain)
    expect(c.default).toBe("scripted")
    expect(c.scripted).toEqual({ enabled: true, reason: "" })
    expect(runLabel(c)).toBe("Run on scripted · 0 tokens")
    expect(engineHeld(c)).toBe("")
  })

  it("live names the model and the estimate from the steps' usage × price", () => {
    const c = engineChoice(plain)
    expect(c.live).toEqual({
      model: "wefttest/script",
      estimate: "~1.2k tokens ≈ $0.0004 for step 1",
      usd: "≈ $0.0004",
    })
    expect(runLabel(c, "live")).toBe("Run live on wefttest/script · ≈ $0.0004")
    expect(ENGINE_WORDS.live(c.live.model, c.live.estimate)).toBe(
      "live · calls wefttest/script · ~1.2k tokens ≈ $0.0004 for step 1"
    )
  })

  it("a live replay runs every step after from_step: their recorded costs summed; unread later steps said, the figure a floor", () => {
    const all = engineChoice({
      ...plain,
      fromStep: 0,
      steps: 2,
      costs: [step(100, 50, 0.0001), step(900, 300, 0.0004)],
    })
    expect(all.live.estimate).toBe("~1.4k tokens ≈ $0.0005 for steps 0–1")
    expect(all.live.usd).toBe("≈ $0.0005")
    const one = engineChoice({
      ...plain,
      fromStep: 0,
      costs: [step(100, 50, 0.0001), null],
    })
    expect(one.live.estimate).toBe(
      "~150 tokens ≈ $0.0001 for step 0 · later steps also run live"
    )
    expect(runLabel(one, "live")).toBe(
      "Run live on wefttest/script · ≥ $0.0001"
    )
    // stepsOf: from_step to the step count, capped, -1 marking the rest.
    expect(stepsOf(1, 3)).toEqual([1, 2])
    expect(stepsOf(2, 2)).toEqual([])
    expect(stepsOf(1)).toEqual([1, -1])
    expect(stepsOf(0, COST_STEPS + 5)).toHaveLength(COST_STEPS + 1)
  })

  it("an edit disables scripted with its reason, defaults live, and a pick of scripted is held, not switched", () => {
    const c = engineChoice({ ...plain, edits: 1 })
    expect(c.default).toBe("live")
    expect(c.scripted.enabled).toBe(false)
    expect(c.scripted.reason).toBe(
      "your edit changes step 1's request: the recording answered the original"
    )
    expect(engineOf(c)).toBe("live")
    expect(engineOf(c, "scripted")).toBe("scripted")
    expect(engineHeld(c, "scripted")).toBe(
      "scripted is refused: your edit changes step 1's request: the recording answered the original — pick live"
    )
    expect(runLabel(c)).toBe("Run live on wefttest/script · ≈ $0.0004")
  })

  it("each change that cannot hit names itself (the server's refusals, then the key's misses)", () => {
    const T = ": the recording answered the original"
    const why = (f: Partial<EngineFacts>) =>
      engineChoice({ ...plain, ...f }).scripted.reason
    expect(why({ source: false })).toBe("no source run to replay")
    expect(why({ prompt: true })).toBe(`a changed prompt${T}`)
    expect(why({ model: "zai/glm-5.3-flash" })).toBe(`a model override${T}`)
    expect(why({ fromStep: 2 })).toBe("the source never answered step 2")
    expect(why({ fork: true })).toBe(`a fork's new message${T}`)
    expect(why({ input: true })).toBe(`a new input${T}`)
    expect(why({ keyed: ["tools", "thinking"] })).toBe(
      `tools, thinking changed${T}`
    )
    // The preview's key check (U18.1): the server's sentence, verbatim.
    const msg =
      "scripted has no recorded answer for step 2: the tool list differs from the recording — run on live"
    expect(
      why({
        keyMatch: {
          ok: false,
          because: ["tools"],
          step: 2,
          message: msg,
        },
      })
    ).toBe(msg)
    // No sentence: the step.
    expect(why({ keyMatch: { ok: false, because: ["tools"], step: 2 } })).toBe(
      "no recorded answer for step 2"
    )
    expect(why({ keyMatch: null })).toBe("")
    expect(why({ keyMatch: { ok: true } })).toBe("")
    expect(why({ steps: undefined, fromStep: 5 })).toBe("")
  })

  it("cost unknown says why, never $0 for an unknown; a scripted source's $0 is the bare figure", () => {
    const est = (f: Partial<EngineFacts>) =>
      engineChoice({ ...plain, ...f }).live
    expect(est({ costs: null, costWhy: "no step route" })).toEqual({
      model: "wefttest/script",
      estimate: "cost unknown: no step route",
      usd: "",
    })
    expect(est({ costs: [] }).estimate).toBe(
      "cost unknown: the source never answered step 1"
    )
    expect(est({ costs: [step(0, 0, 0)] }).estimate).toBe(
      "cost unknown: step 1 recorded no usage"
    )
    expect(
      est({
        costs: [
          {
            usage: { input_tokens: 10, output_tokens: 5 },
            cost_usd: null,
            cost_known: false,
            cost_badge: "unpriced",
            cost_reason: "no price for zai/glm-5.3-flash",
          },
        ],
      }).estimate
    ).toBe("cost unknown: no price for zai/glm-5.3-flash")
    const over = engineChoice({ ...plain, model: "zai/glm-5.3-flash" })
    expect(over.live.model).toBe("zai/glm-5.3-flash")
    expect(over.live.estimate).toBe(
      "cost unknown: the steps were priced on wefttest/script"
    )
    expect(runLabel(over)).toBe("Run live on zai/glm-5.3-flash · cost unknown")
    // An offline agent's scripted model: an exact $0, no chip suffix.
    const off = est({
      costs: [
        {
          usage: { input_tokens: 10, output_tokens: 5 },
          cost_usd: 0,
          cost_known: true,
          cost_badge: "scripted",
        },
      ],
    })
    expect(off.estimate).toBe("~15 tokens ≈ $0.00 for step 1")
    expect(off.usd).toBe("≈ $0.00")
  })
})

describe("factsOf: the command's own reading (what the body sends)", () => {
  const agent = {
    name: "director",
    models: ["script", "glm"],
    instructions: "You direct.",
    tools: [
      { name: "ops_manager", side_effects: "safe" as const, allow: false },
    ],
    defaults: {
      max_steps: 10,
      parallelism: 4,
      thinking: "low",
      tool_choice: { mode: "auto" as const },
    },
  }
  const own = { provider: "wefttest", name: "script" }
  const variant = (over: Partial<VariantFields> = {}): VariantFields => ({
    instructions: "",
    toolsOff: new Set(),
    model: "",
    thinking: "",
    input: "",
    engine: "live",
    sideEffects: "substitute",
    thread: "ephemeral",
    ...over,
  })
  const lab = (p: Record<string, unknown>) =>
    ({
      max_steps: "",
      parallelism: "",
      temperature: "",
      top_p: "",
      max_tokens: "",
      seed: "",
      stop: "",
      tool_choice: "",
      tool_choice_name: "",
      only_tools: [],
      park_on: [],
      ...p,
    }) as VariantFields["lab"]
  const reason = (v: VariantFields, extra: Record<string, unknown> = {}) =>
    engineChoice(
      factsOf({
        overrides: overridesOf(v, agent, own),
        defaults: agent.defaults,
        edits: 0,
        input: v.input,
        sourceInput: "how many units of A-17 in Lyon?",
        fork: false,
        fromStep: 0,
        steps: 2,
        own,
        ...extra,
      })
    ).scripted.reason

  it("a knob equal to the agent's own is no change: the body leaves it out", () => {
    expect(sameModel("script", own)).toBe(true)
    expect(sameModel("wefttest/script", own)).toBe(true)
    expect(sameModel("glm", own)).toBe(false)
    // UX-D-04: a model the source never ran on is a live call.
    expect(modelLabel("glm", own)).toBe("glm · live call")
    expect(modelLabel("script", own)).toBe("script")
    expect(overridesOf(variant({ model: "script" }), agent, own)).toEqual({})
    expect(reason(variant({ model: "script" }))).toBe("")
    expect(reason(variant({ lab: lab({ tool_choice: "auto" }) }))).toBe("")
    expect(reason(variant({ thinking: "low" }))).toBe("")
    expect(reason(variant({ input: "how many units of A-17 in Lyon?" }))).toBe(
      ""
    )
    expect(reason(variant({ instructions: "You direct." }))).toBe("")
  })

  it("a knob that changes the key is a change, parallelism 1 included (SequentialTools)", () => {
    const T = ": the recording answered the original"
    expect(reason(variant({ model: "glm" }))).toBe(`a model override${T}`)
    expect(reason(variant({ lab: lab({ tool_choice: "any" }) }))).toBe(
      `tool choice changed${T}`
    )
    expect(reason(variant({ lab: lab({ parallelism: "1" }) }))).toBe(
      `parallelism 1 (sequential tools) changed${T}`
    )
    expect(reason(variant({ input: "and in Paris?" }))).toBe(`a new input${T}`)
    expect(reason(variant({ thinking: "high" }))).toBe(`thinking changed${T}`)
  })
})

describe("an edit equal to the recording is not an edit", () => {
  it("noEdit and realEdits", () => {
    expect(noEdit("", "x")).toBe(true)
    expect(noEdit("x", "x")).toBe(true)
    expect(noEdit("y", "x")).toBe(false)
    const fields = [
      {
        step: 0,
        callID: "call_1",
        name: "ops_manager",
        placeholder: "42 units",
      },
      { step: 1, name: "reply", placeholder: "We have 42 units." },
    ]
    const same = { step: 0, callID: "call_1", toolResult: "42 units" }
    const changed = { step: 0, callID: "call_1", toolResult: "0 units" }
    const reply = { step: 1, content: "We have 42 units." }
    expect(realEdits([same, reply], fields)).toEqual([])
    expect(realEdits([changed, reply], fields)).toEqual([changed])
    expect(realEdits([same], null)).toEqual([same])
  })
})

describe("the drawer's order and target (lib/drawer-spec.ts)", () => {
  it("source · target · engine · what changes · will be sent · tools · advanced · run", () => {
    expect(DRAWER_ORDER.map((k) => DRAWER_TITLES[k])).toEqual([
      "source",
      "target",
      "engine",
      "1 what changes",
      "2 will be sent",
      "3 tools during the replay",
      "advanced: side effects · where it writes · options",
      "",
    ])
  })
})
