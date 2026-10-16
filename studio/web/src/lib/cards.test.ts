// lib/cards.ts over the recorded shapes (studio/testdata/api/
// cards.golden.json, TestRunCardsGolden) and a few hand-made edges:
// the facts both renderers draw for U25's named runs.
import { describe, expect, it } from "vitest"

import type { RequestRow, WireEvent } from "./api"
import {
  attachmentsOf,
  attemptCause,
  attemptTag,
  BREAKPOINT,
  callFactsOf,
  childOutcome,
  execLine,
  fileWords,
  outputOf,
  parkWord,
  preparedChip,
  thinkingWords,
  tokenSplit,
} from "./cards"
import { messageLine } from "./compaction"
import { applyTranscript, fold, truncation } from "./events"
import { AWAITING } from "./status"
import { shape } from "@/test/cards-fixture"

const foldOf = (name: string) => {
  const s = shape(name)
  return applyTranscript(
    fold(s.events.events.map((e) => e.event as WireEvent)),
    s.transcript.batches,
    { replace: true }
  )
}
const factsOf = (name: string) => {
  const s = shape(name)
  return callFactsOf(s.spans.spans, s.run.id)
}
const rows = (name: string) =>
  (shape(name).requests as { requests: RequestRow[] }).requests

describe("the run cards' facts (U25)", () => {
  it("loop.max-tokens-tools: the cut call is placed on its step with its args as sent and the not-executed result", () => {
    const v = foldOf("loop.max-tokens-tools")
    const [call] = v.steps[0].toolCalls
    expect(call).toMatchObject({
      callId: "t1",
      name: "echo",
      unrun: true,
      args: '{"msg":"never',
    })
    expect(call.result?.isError).toBe(true)
    expect(truncation(call.result!.content)).toEqual({
      kind: "call",
      tool: "echo",
    })
    // The retry's call is its own step's, not a second copy on step 0.
    expect(v.steps[0].toolCalls).toHaveLength(1)
    expect(v.steps[1].toolCalls[0]).toMatchObject({
      callId: "t1",
      args: { msg: "retry" },
    })
    expect(v.steps[1].toolCalls[0].unrun).toBeUndefined()
  })

  it("out.invalid-repaired / out.no-output: the typed output, its repair count, the missing submission", () => {
    const s = shape("out.invalid-repaired")
    expect(
      outputOf(foldOf("out.invalid-repaired"), s.spans.spans, s.run.id)
    ).toEqual({
      args: { approved: true, reason: "fixed" },
      repairs: 1,
      none: false,
    })
    const n = shape("out.no-output")
    expect(
      outputOf(foldOf("out.no-output"), n.spans.spans, n.run.id)
    ).toMatchObject({
      args: undefined,
      none: true,
    })
    // A run of an agent without Output says nothing.
    const t = shape("tools.timeout")
    expect(
      outputOf(foldOf("tools.timeout"), t.spans.spans, t.run.id)
    ).toBeNull()
  })

  it("mw.retry: each attempt's status, cause and wait, from the step route and from the spans alike", () => {
    const att = shape("mw.retry").steps[0].attempts
    expect(att.map((a) => attemptCause(a))).toEqual([
      "503 · Service Unavailable: overloaded",
      "503 · Service Unavailable: overloaded · waited 1 ms",
      "waited 1 ms",
    ])
    const s = shape("mw.retry")
    expect(
      [1, 2, 3].map((n) => attemptTag(s.spans.spans, s.run.id, 0, n))
    ).toEqual([
      "attempt 1 · 503 · Service Unavailable: overloaded",
      "attempt 2 · 503 · Service Unavailable: overloaded · waited 1 ms",
      "attempt 3 · waited 1 ms",
    ])
  })

  it("tools.timeout: the call's limit beside its wall time", () => {
    const f = factsOf("tools.timeout").get("0:s1")
    expect(f).toMatchObject({ limitMs: 50, errorType: "timeout" })
    expect(execLine(f)).toBe("0 ms / 50 ms limit")
    expect(execLine({ ms: 12, limitMs: 5000, queueMs: 3 })).toBe(
      "12 ms / 5.0 s limit · queued 3 ms"
    )
    expect(execLine({})).toBeNull()
  })

  it("appr.park and the breakpoints: a gate awaits a decision, a breakpoint is paused — never never-completed", () => {
    expect(parkWord(factsOf("appr.park").get("0:d1"), "deploy")).toBe(AWAITING)
    expect(parkWord(factsOf("appr.breakpoint-own").get("0:b1"), "status")).toBe(
      BREAKPOINT
    )
    const pg = shape("appr.breakpoint-playground")
    expect(pg.run.meta["weft.playground.breakpoints"]).toBe("status")
    const f = factsOf("appr.breakpoint-playground").get("0:b2")
    expect(f?.park).toBe("park_on")
    expect(
      parkWord(f, "status", pg.run.meta["weft.playground.breakpoints"])
    ).toBe(BREAKPOINT)
    // A ParkOn the run does not name a breakpoint is the run's ParkOn.
    expect(parkWord(f, "status")).toBe(`${AWAITING} · ParkOn`)
    // A record from before U25 names no source.
    expect(parkWord(undefined, "x")).toBe(AWAITING)
  })

  it("sub.failed: the Subagent call's typed outcome; pending names the child's calls; a cycle; a timeout", () => {
    const f = factsOf("sub.failed").get("0:w1")
    expect(f?.errorType).toBe("SUBAGENT_FAILED")
    expect(childOutcome(f?.errorType, f, true)).toBe("child failed")
    expect(
      childOutcome("SUBAGENT_PENDING", { childPending: "refund_order" })
    ).toBe(`child ${AWAITING} · refund_order`)
    expect(childOutcome("SUBAGENT_CYCLE")).toBe("cycle refused")
    expect(childOutcome("timeout", { limitMs: 2000 }, true)).toBe(
      "child timed out · 2.0 s limit"
    )
    expect(childOutcome("timeout", { limitMs: 2000 }, false)).toBeNull()
  })

  it("opt.prepare-step: the parts PrepareStep changed and the thinking wire", () => {
    const r = rows("opt.prepare-step")
    expect(preparedChip(r[0].body.prepared)).toBe(
      "PrepareStep: system, tool_choice, params"
    )
    expect(preparedChip(r[1].body.prepared)).toBe("PrepareStep: system")
    expect(thinkingWords(r[0].body.thinking)).toBe("low → reasoning_effort")
    expect(thinkingWords(undefined)).toBe("adapter default")
  })

  it("msg.parts: the prompt's files as chips, in the request's message lines too", () => {
    const s = shape("msg.parts")
    expect(attachmentsOf(s.transcript.batches)).toEqual([
      "file text/plain · 14 B",
      "file image/png · url",
    ])
    expect(messageLine(s.transcript.batches[0].messages[0])).toBe(
      "user: What is in this image?  [file text/plain · 14 B] [file image/png · url]"
    )
    expect(fileWords({ media_type: "image/png" })).toBe(
      "file image/png · no data"
    )
  })

  it("the roll-up's own and delegated tokens", () => {
    expect(
      tokenSplit({ input_tokens: 60, output_tokens: 30 }, [
        { usage: { input_tokens: 40, output_tokens: 20 } },
      ])
    ).toBe("60 in = 20 own + 40 via 1 child")
    expect(tokenSplit({ input_tokens: 60, output_tokens: 30 }, [])).toBeNull()
  })
})
