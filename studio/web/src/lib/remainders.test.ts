// U30.3's lib rules (the Studio remainders, part 2): each remainder's
// words, pinned where both a route test and a reader would look.
import { describe, expect, it } from "vitest"

import type { RequestRow } from "./api"
import { giveUpLine, msText } from "./attempts"
import {
  childTokenChips,
  knobsBase,
  outputType,
  overrideTag,
  paramOverrideTags,
  variantParamLines,
} from "./cards"
import { BARRIER, concurrencyMarks } from "./concurrency"
import { attemptError } from "./error-kind"
import { duration, spanMs } from "./format"
import { OUTPUT_MARK, toolPolicyChips } from "./policy"
import { concurrencyWords } from "./request-pane"
import { offsetText, took } from "./waterfall"

describe("one duration format (UX-F-16)", () => {
  it("spaces every unit word, on both surfaces' one helper", () => {
    expect(spanMs(11)).toBe("11 ms")
    expect(spanMs(1234)).toBe("1.2 s")
    expect(spanMs(63_000)).toBe("1m03s")
    expect(msText).toBe(spanMs)
    expect(took(0.015)).toBe("15 µs")
    expect(took(9.49)).toBe("9.5 ms")
    expect(offsetText(4)).toBe("+4.0 ms")
    expect(
      duration("2026-10-01T09:00:00.000Z", "2026-10-01T09:00:00.017Z")
    ).toBe("17 ms")
  })
})

describe("an attempt's cause and the give-up (UX-S2-02)", () => {
  it("words each classified error.type, model_error kept as the fallback", () => {
    expect(attemptError("rate_limit").word).toBe("rate limited (429)")
    expect(attemptError("overloaded").word).toBe("overloaded")
    expect(attemptError("auth").word).toBe("auth refused")
    expect(attemptError("model_error").word).toBe(
      "provider error (see the status)"
    )
  })
  it("says why the retrying layer stopped, after the last failed attempt only", () => {
    const fail = { outcome: "error" }
    expect(giveUpLine([fail, { ...fail, giveup_reason: "budget" }])).toBe(
      "gave up: retries spent (2 attempts)"
    )
    expect(giveUpLine([{ ...fail, giveup_reason: "not_retryable" }])).toBe(
      "gave up: not retryable (1 attempt)"
    )
    // A record from before it: several failures, no reason.
    expect(giveUpLine([fail, fail, fail])).toBe("gave up after 3 attempts")
    expect(giveUpLine([fail])).toBeNull()
    expect(giveUpLine([fail, { outcome: "ok" }])).toBeNull()
    expect(giveUpLine(undefined)).toBeNull()
    // The run's context ended: nothing gave up.
    for (const error_type of ["context.Canceled", "context.DeadlineExceeded"])
      expect(giveUpLine([fail, fail, { ...fail, error_type }])).toBeNull()
  })
})

describe("a step's parallelism and a barrier (UX-S2-07)", () => {
  it("the run header's concurrency prefers the request record's dial", () => {
    expect(
      concurrencyWords({ recorded: 2, override: 3, manifest: 4 })?.text
    ).toBe("parallelism: 2")
    expect(concurrencyWords({ recorded: 1 })?.text).toBe("sequential")
    // A record before the dial: the override, as before.
    expect(concurrencyWords({ override: 3, manifest: 4 })?.text).toBe(
      "parallelism: 3"
    )
  })
  const req = (step: number, parallelism?: number, seq: string[] = []) =>
    ({
      step,
      attempt: 1,
      body: { parallelism },
      tools: {
        hash: "h",
        content: "",
        truncated_bytes: 0,
        tools: ["a", "barrier"].map((name) => ({
          name,
          sequential: seq.includes(name),
        })),
      },
    }) as unknown as RequestRow
  const rows = [
    { key: "s0", runId: "r", kind: "step", label: "step 0", sub: "tool_calls" },
    { key: "c:0:p1", runId: "r", kind: "tool", label: "a" },
    { key: "c:0:p2", runId: "r", kind: "tool", label: "barrier" },
    { key: "s1", runId: "r", kind: "step", label: "step 1" },
    { key: "s0", runId: "child", kind: "step", label: "step 0" },
  ]
  it("adds the dial to a step that ran tools and marks a Sequential tool", () => {
    const out = concurrencyMarks(rows, [req(0, 2, ["barrier"]), req(1, 2)], "r")
    expect(out[0].sub).toBe("tool_calls · parallelism 2")
    expect(out.map((r) => !!(r as { barrier?: boolean }).barrier)).toEqual([
      false,
      false,
      true,
      false,
      false,
    ])
    // No tool rows under step 1, another run's step 0: untouched.
    expect(out[3]).toBe(rows[3])
    expect(out[4]).toBe(rows[4])
    expect(BARRIER).toBe("▮ sequential")
  })
  it("says nothing from a record written before the dial", () => {
    expect(concurrencyMarks(rows, [req(0)], "r")[0].sub).toBe("tool_calls")
    expect(concurrencyMarks(rows, undefined, "r")).toBe(rows)
  })
})

describe("the typed output's type (UX-S1-06)", () => {
  it("reads the root's weft.output.type, and marks the Output tool on the Agents card", () => {
    const spans = [
      {
        name: "invoke_agent billing",
        start: "",
        end: "",
        attrs: {
          "gen_ai.operation.name": "invoke_agent",
          "weft.run.id": "r",
          "weft.output.type": "main.Invoice",
        },
      },
    ]
    expect(outputType(spans, "r")).toBe("main.Invoice")
    expect(outputType(spans, "other")).toBeUndefined()
    const chips = toolPolicyChips({
      name: "submit_output",
      output_type: "main.Invoice",
    })
    expect(chips[0].key).toBe("output_type")
    expect(chips[0].text).toBe(`${OUTPUT_MARK} · main.Invoice`)
    expect(toolPolicyChips({ name: "lookup" })).toEqual([])
  })
})

describe("per-child token chips (UX-S3-13)", () => {
  const u = (i: number, o: number) => ({ input_tokens: i, output_tokens: o })
  const doc = {
    id: "r",
    delegated_usage: u(40, 20),
    children: [
      { id: "r/0/c1", agent: "ops-manager", usage: u(40, 20) },
      { id: "pool-child", agent: "analyst", usage: u(5, 5) },
    ],
  }
  it("one chip per Subagent child, its own tokens", () => {
    expect(childTokenChips(doc).map((c) => c.text)).toEqual([
      "ops-manager 40 in · 20 out",
    ])
  })
  it("none where the token cannot see every child (no delegated_usage)", () => {
    expect(childTokenChips({ ...doc, delegated_usage: undefined })).toEqual([])
  })
})

describe("the per-param override tag (UX-S5b-03)", () => {
  const agent = { knobs: { temperature: 0.7 }, from: "agent" as const }
  it("tags each knob the override set or cleared, with the agent's configured value when known", () => {
    const v = '{"temperature":0.4,"top_p":null}'
    expect([...paramOverrideTags(v)]).toEqual([
      ["temperature", "override"],
      ["top_p", "cleared by override"],
    ])
    expect(paramOverrideTags(v, agent).get("temperature")).toBe(
      "override (agent: 0.7)"
    )
    expect(overrideTag("seed", agent)).toBe("override (agent: default)")
    // The pre-U25 Go struct: its nulls say nothing of the agent's.
    expect([...paramOverrideTags({ Temperature: 0.4, TopP: null })]).toEqual([
      ["temperature", "override"],
    ])
  })
  it("reads the manifest's agent params first, else the source run's request, said as such", () => {
    expect(
      knobsBase({ params: { temperature: 0.7 } }, { temperature: 1 })
    ).toEqual(agent)
    // No params in the manifest (none set, or a manifest before the
    // field): the source run's knobs, named as the source run's.
    const src = knobsBase({}, { temperature: 1 })
    expect(src).toEqual({ knobs: { temperature: 1 }, from: "source run" })
    expect(overrideTag("temperature", src)).toBe("override (source run: 1)")
    expect(overrideTag("seed", src)).toBe("override (source run: default)")
    expect(knobsBase(undefined, undefined)).toBeUndefined()
  })
  it("lists an experiment variant's knobs against the comparison", () => {
    expect(
      variantParamLines(
        {
          options: { temperature: 0.4, max_steps: 3 },
          params: { top_p: 0.9 },
          tool_choice: "none",
        },
        agent
      )
    ).toEqual([
      "top_p 0.9 ◂ override (agent: default)",
      "temperature 0.4 ◂ override (agent: 0.7)",
      "tool choice none ◂ override",
    ])
    expect(
      variantParamLines(
        { options: { temperature: 0.4 } },
        knobsBase(undefined, { temperature: 0.9 })
      )
    ).toEqual(["temperature 0.4 ◂ override (source run: 0.9)"])
    expect(variantParamLines({ instructions: "x" })).toEqual([])
  })
})
