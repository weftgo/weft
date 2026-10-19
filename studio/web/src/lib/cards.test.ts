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
  BY_SOURCE,
  callFactsOf,
  childOutcome,
  execLine,
  fileWords,
  outputOf,
  ownBreakpoints,
  paramsOverride,
  parkWord,
  preparedChip,
  thinkingWords,
  tokenSplit,
} from "./cards"
import { messageLine } from "./compaction"
import { applyTranscript, fold, truncation } from "./events"
import { AWAITING } from "./status"
import { NOT_EXECUTED, toolsLine } from "./summarize"
import { UNTIMED_NOTE, waterfallRows } from "./waterfall"
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
    // The answer's tools line counts it apart: not executed, never
    // "never finished".
    expect(toolsLine(v.steps[0])).toBe(`1 ${NOT_EXECUTED}`)
    expect(toolsLine(v.steps[1])).toBe("1 ran")
  })

  it("loop.max-tokens-tools: the call never run is drawn 'not executed' in the run tree, never a loss hole (P8-gate-fix)", () => {
    const s = shape("loop.max-tokens-tools")
    const wf = waterfallRows(
      {
        runId: s.run.id,
        status: s.run.status,
        spans: s.spans.spans,
        fold: foldOf("loop.max-tokens-tools"),
      },
      "time"
    )
    const row = wf.rows.find((r) => r.key === "c:0:t1")!
    expect(row.untimed).toBe("unrun")
    expect(UNTIMED_NOTE.unrun[0]).toBe(NOT_EXECUTED)
    // No row of the run is a loss: every other row has its span.
    expect(wf.rows.filter((r) => r.untimed === "missing")).toEqual([])
  })

  it("appr.park: the tools-ran line counts a parked call as awaiting a decision, never 'never finished' (P8-gate-fix)", () => {
    const v = foldOf("appr.park")
    const step = v.steps.find((st) =>
      st.toolCalls.some((c) => c.callId === "d1")
    )!
    const ids = v.pending.map((c) => c.id)
    expect(ids).toContain("d1")
    const line = toolsLine(step, false, v.pending)
    expect(line).toContain(`1 ${AWAITING}`)
    expect(line).not.toContain("never finished")
    // Without the run's pending ids the line cannot know.
    expect(toolsLine(step)).toContain("never finished")
  })

  it("out.invalid-repaired / out.no-output: the typed output, its repair count, the missing submission", () => {
    const s = shape("out.invalid-repaired")
    expect(
      outputOf(foldOf("out.invalid-repaired"), s.spans.spans, s.run.id)
    ).toEqual({
      args: { approved: true, reason: "fixed" },
      repairs: 1,
      none: false,
      stripped: false,
    })
    // Content off (or a read token): the submission's args are not in
    // the record — the run still submitted: typed + stripped, never
    // "no output submitted".
    const off = fold(
      s.events.events.map((e) => {
        const ev = e.event as WireEvent & { args?: unknown }
        return ev.type === "tool_start" ? { ...ev, args: null } : ev
      })
    )
    expect(outputOf(off, s.spans.spans, s.run.id)).toEqual({
      args: undefined,
      repairs: 1,
      none: false,
      stripped: true,
    })
    // The field absent altogether (a read token's strip): the same.
    const absent = fold(
      s.events.events.map((e) => {
        const { args: _drop, ...ev } = e.event as WireEvent & { args?: unknown }
        return (
          (e.event as WireEvent).type === "tool_start" ? ev : e.event
        ) as WireEvent
      })
    )
    expect(outputOf(absent, s.spans.spans, s.run.id)).toMatchObject({
      none: false,
      stripped: true,
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
    const pg = shape("appr.breakpoint-playground")
    expect(pg.run.meta["weft.playground.breakpoints"]).toBe("status")
    const f = factsOf("appr.breakpoint-playground").get("0:b2")
    expect(f?.park).toBe("park_on")
    expect(parkWord(f, "status", pg.run)).toBe(BREAKPOINT)
    // A ParkOn the run does not name a breakpoint is the run's ParkOn.
    expect(parkWord(f, "status")).toBe(`${AWAITING} · ParkOn`)
    // No breakpoints named: the tool "undefined" is no breakpoint.
    expect(parkWord(f, "undefined", { meta: {} })).toBe(`${AWAITING} · ParkOn`)
    // A record from before U25 names no source.
    expect(parkWord(undefined, "x")).toBe(AWAITING)
    expect(parkWord({ park: "park_all_except" }, "x")).toBe(
      `${AWAITING} · ParkAllExcept`
    )
  })

  it("a decided parked call reads its outcome and the turn it resumed in, never awaiting", () => {
    const decided = {
      run: "s_a-t2",
      at: "2026-10-01T09:00:00Z",
      source: "resume_calls",
      calls: [{ call_id: "r1", outcome: "denied" }],
    }
    const f = factsOf("appr.park").get("0:d1")
    expect(parkWord(f, "deploy", { decided }, "r1")).toBe("✋ denied → t2")
    // Another call of the same park, or no id: the park's own word.
    expect(parkWord(f, "deploy", { decided }, "r2")).toBe(AWAITING)
    expect(parkWord(f, "deploy", { decided })).toBe(AWAITING)
    // summarize's tools line leaves the decided call out of its held count.
    const step = {
      toolCalls: [
        { callId: "r1", name: "deploy" },
        { callId: "r2", name: "deploy" },
      ],
    } as unknown as Parameters<typeof toolsLine>[0]
    const parked = [{ id: "r1" }, { id: "r2" }]
    expect(toolsLine(step, false, parked)).toBe(`2 ${AWAITING}`)
    expect(toolsLine(step, false, parked, decided)).toBe(
      `1 ${AWAITING} · 1 decided`
    )
    // A decided call the pending list does not name (a span-derived
    // run's): counted with neither, never "-1 awaiting" (P9-pre-web-fix2 8).
    const line = toolsLine(step, false, [{ id: "r2" }], decided)
    expect(line).toBe(`1 ${AWAITING} · 1 never finished`)
    expect(line).not.toMatch(/-\d/)
    expect(toolsLine(step, false, [], decided)).not.toMatch(/-\d|decided/)
  })

  it("appr.breakpoint-own: a ParkSource park is a breakpoint only on a playground run or a breakpoints_own runtime's", () => {
    const own = shape("appr.breakpoint-own")
    const f = factsOf("appr.breakpoint-own").get("0:b1")
    expect(f?.park).toBe("park_source")
    // ParkSource is a general option (ADR 0030): no debugger is known.
    expect(parkWord(f, "status", own.run)).toBe(BY_SOURCE)
    const rts = [{ service: own.run.service, breakpoints_own: true }]
    expect(ownBreakpoints(rts, own.run.service)).toBe(true)
    expect(ownBreakpoints(rts, "other")).toBe(false)
    expect(
      ownBreakpoints([{ service: own.run.service }], own.run.service)
    ).toBe(false)
    expect(parkWord(f, "status", own.run, undefined, true)).toBe(BREAKPOINT)
    expect(parkWord(f, "status", { ...own.run, playground: true })).toBe(
      BREAKPOINT
    )
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

  it("the roll-up's own and delegated tokens: core Subagent children only", () => {
    const usage = { input_tokens: 60, output_tokens: 30 }
    const sub = {
      id: "r1/0/c1",
      usage: { input_tokens: 40, output_tokens: 20 },
    }
    expect(tokenSplit({ id: "r1", usage, children: [sub] })).toBe(
      "60 in = 20 own + 40 via 1 child"
    )
    expect(tokenSplit({ id: "r1", usage, children: [] })).toBeNull()
    // A pool child session or a Generate inside a tool handler is
    // linked to the run, not in its usage (pricing.go's rule): never
    // "100 in = 20 own + 80 via 1 child", never a negative own.
    const pool = { id: "r9", usage: { input_tokens: 80, output_tokens: 1 } }
    expect(tokenSplit({ id: "r1", usage, children: [pool] })).toBeNull()
    expect(tokenSplit({ id: "r1", usage, children: [sub, pool] })).toBe(
      "60 in = 20 own + 40 via 1 child"
    )
    // A sibling whose id merely starts with the run's is not its child.
    expect(
      tokenSplit({ id: "r1", usage, children: [{ ...sub, id: "r10/0/c1" }] })
    ).toBeNull()
  })

  it("weft.override.params in every stored shape reads as a whole replacement", () => {
    // U25-fix: set knobs, the agent's knobs the replacement clears null.
    expect(paramsOverride('{"temperature":0.4,"top_p":null}')).toBe(
      "replaced whole: temperature 0.4, top_p cleared"
    )
    // U25.1: the set knobs alone.
    expect(
      paramsOverride(
        '{"temperature":0.4,"top_p":0.9,"max_tokens":64,"stop":["END"],"seed":7}'
      )
    ).toBe(
      'replaced whole: temperature 0.4, top_p 0.9, max_tokens 64, stop ["END"], seed 7'
    )
    // Before U25: the Go struct, every field, unset ones null — says
    // nothing of the agent's, so no "cleared".
    expect(
      paramsOverride(
        '{"Temperature":0.4,"TopP":null,"MaxTokens":null,"Stop":null,"Seed":null}'
      )
    ).toBe("replaced whole: temperature 0.4")
    expect(paramsOverride("{}")).toBe(
      "replaced whole: every knob the adapter's default"
    )
    expect(paramsOverride("not json")).toBeNull()
    expect(paramsOverride(undefined)).toBeNull()
  })
})
