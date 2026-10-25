// The engine per command (U17; UX-C-01, UX-D-04, UX-C-05, UX-C-08) on
// rw.deep-delegation's director run: two recorded steps, its model
// wefttest/script, step 1 the reply after ops_manager's call_1.
import { describe, expect, it } from "vitest"

import {
  engineChoice,
  engineHeld,
  engineOf,
  ENGINE_WORDS,
  keyedKnobs,
  noEdit,
  runLabel,
} from "./engine"
import type { EngineFacts } from "./engine"
import { realEdits } from "./experiment-body"
import { DRAWER_ORDER, DRAWER_TITLES } from "./drawer-spec"

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
  cost: {
    usage: { input_tokens: 900, output_tokens: 300 },
    cost_usd: 0.0004,
    cost_known: true,
    cache: "exact",
  },
}

describe("engineChoice", () => {
  it("a plain replay opens on scripted, Run names it", () => {
    const c = engineChoice(plain)
    expect(c.default).toBe("scripted")
    expect(c.scripted).toEqual({ enabled: true, reason: "" })
    expect(runLabel(c)).toBe("Run on scripted · 0 tokens")
    expect(engineHeld(c)).toBe("")
  })

  it("live names the model and the estimate from the step's usage × price", () => {
    const c = engineChoice(plain)
    expect(c.live).toEqual({
      model: "wefttest/script",
      estimate: "~1.2k tok ≈ $0.0004",
      usd: "$0.0004",
    })
    expect(runLabel(c, "live")).toBe("Run live on wefttest/script · ≈ $0.0004")
    expect(ENGINE_WORDS.live(c.live.model, c.live.estimate)).toBe(
      "live · calls wefttest/script · ~1.2k tok ≈ $0.0004"
    )
  })

  it("an edit disables scripted with its reason, defaults live, and a pick of scripted is held, not switched", () => {
    const c = engineChoice({ ...plain, edits: 1 })
    expect(c.default).toBe("live")
    expect(c.scripted.enabled).toBe(false)
    expect(c.scripted.reason).toBe(
      "your edit changes step 1's request: the recording answered the original"
    )
    expect(engineOf(c)).toBe("live")
    // The user's pick stays theirs: scripted is held with the reason.
    expect(engineOf(c, "scripted")).toBe("scripted")
    expect(engineHeld(c, "scripted")).toBe(
      "scripted is refused: your edit changes step 1's request: the recording answered the original — pick live"
    )
    expect(runLabel(c)).toBe("Run live on wefttest/script · ≈ $0.0004")
  })

  it("each change that cannot hit names itself (the server's refusals, then the key's misses)", () => {
    const why = (f: Partial<EngineFacts>) =>
      engineChoice({ ...plain, ...f }).scripted.reason
    expect(why({ source: false })).toBe("no source run to replay")
    expect(why({ prompt: true })).toBe(
      "a changed prompt: the recording answered the original"
    )
    expect(why({ model: "zai/glm-5.3-flash" })).toBe(
      "a model override: the recording answered the original"
    )
    expect(why({ fromStep: 2 })).toBe("the source never answered step 2")
    expect(why({ fork: true })).toBe(
      "a fork's new message: the recording answered the original"
    )
    expect(why({ input: true })).toBe(
      "a new input: the recording answered the original"
    )
    expect(why({ keyed: keyedKnobs({ tools: true, thinking: "high" }) })).toBe(
      "tools, thinking changed: the recording answered the original"
    )
    // The preview's key check (U18.1) when the server sends one.
    expect(
      why({ keyMatch: { ok: false, because: ["the tool list changed"] } })
    ).toBe(
      "the key misses (the tool list changed): the recording answered the original"
    )
    expect(why({ hit: false })).toBe("no recorded answer for this request")
    // Absent is unknown: it disables nothing.
    expect(why({ keyMatch: null, hit: null })).toBe("")
    expect(why({ keyMatch: { ok: true } })).toBe("")
    // Steps not read yet: unknown, not a refusal.
    expect(why({ steps: undefined, fromStep: 5 })).toBe("")
  })

  it("cost unknown says why, never $0 for an unknown", () => {
    const est = (f: Partial<EngineFacts>) =>
      engineChoice({ ...plain, ...f }).live
    expect(est({ cost: null, costWhy: "no step route" })).toEqual({
      model: "wefttest/script",
      estimate: "cost unknown: no step route",
      usd: "",
    })
    expect(
      est({ cost: { usage: { input_tokens: 0, output_tokens: 0 } } }).estimate
    ).toBe("cost unknown: the step recorded no usage")
    expect(
      est({
        cost: {
          usage: { input_tokens: 10, output_tokens: 5 },
          cost_usd: null,
          cost_known: false,
          cost_badge: "unpriced",
          cost_reason: "no price for zai/glm-5.3-flash",
        },
      }).estimate
    ).toBe("cost unknown: no price for zai/glm-5.3-flash")
    const over = est({ model: "zai/glm-5.3-flash" })
    expect(over.model).toBe("zai/glm-5.3-flash")
    expect(over.estimate).toBe(
      "cost unknown: the step was priced on wefttest/script"
    )
    expect(
      runLabel(engineChoice({ ...plain, model: "zai/glm-5.3-flash" }))
    ).toBe("Run live on zai/glm-5.3-flash · cost unknown")
    // A scripted source's own model is an exact $0, said as such.
    expect(
      est({
        cost: {
          usage: { input_tokens: 10, output_tokens: 5 },
          cost_usd: 0,
          cost_known: true,
          cost_badge: "scripted",
        },
      }).estimate
    ).toBe("~15 tok ≈ $0 · scripted")
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
    // Not read: every draft counts.
    expect(realEdits([same], null)).toEqual([same])
  })
})

describe("the drawer's order (lib/drawer-spec.ts)", () => {
  it("source · target · engine · what changes · will be sent · tools · advanced · run", () => {
    expect(DRAWER_ORDER.map((k) => DRAWER_TITLES[k])).toEqual([
      "source",
      "target",
      "engine",
      "1 what changes",
      "2 will be sent",
      "3 tools during the replay",
      "advanced: side effects · thread · options",
      "",
    ])
  })
})
