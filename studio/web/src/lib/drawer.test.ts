// The drawer's derivations (U10.1, audit §6.3): lib/summarize.ts's
// answer lines (verdict · sent · replied · tools ran · then) and
// lib/run-tree.ts's landing rule, kind labels and tabs — on the
// opt.usage-limit run (failed at step 2) and the director run.
import { describe, expect, it } from "vitest"

import { DIR, FOLD, SPANS } from "@/test/director-fixture"
import { UL, UL_ERR, UL_EVENTS, UL_SPANS } from "@/test/usage-limit-fixture"
import { fold } from "./events"
import {
  defaultRowSel,
  defaultTab,
  drawerTabOf,
  KIND_LABEL,
  stepRowOf,
  treeSpans,
} from "./run-tree"
import {
  failedAtStep,
  landStep,
  repliedLine,
  sentLine,
  stepVerdict,
  thenLine,
  toolsLine,
} from "./summarize"
import { waterfallRows } from "./waterfall"

const ulRun = {
  status: "failed",
  err: UL_ERR,
  stopped_by: "usage_limit",
  steps: 3,
}
const ulFold = fold(UL_EVENTS)
const ulTree = (scale: "time" | "order", spans = UL_SPANS) =>
  waterfallRows(
    {
      runId: UL,
      status: "failed",
      spans: scale === "time" ? spans : undefined,
      fold: ulFold,
      total: UL_EVENTS.length,
    },
    scale
  )

describe("failedAtStep", () => {
  it("is the step core's RunError names, else the last, only when failed", () => {
    expect(failedAtStep(ulRun)).toBe(2)
    expect(failedAtStep({ status: "failed", err: "boom", steps: 4 })).toBe(3)
    expect(
      failedAtStep({ status: "succeeded", err: UL_ERR, steps: 3 })
    ).toBeUndefined()
    expect(failedAtStep({ status: "failed", steps: 0 })).toBeUndefined()
  })
})

describe("the answer lines", () => {
  it("step 2 of opt.usage-limit: the stop, its detail, what the loop did", () => {
    expect(stepVerdict(ulFold.steps[2], ulRun)).toEqual({
      text: "✗ usage limit reached (input 300/250, output 150/250) — the loop refused step 3 · tool_calls · 100 in · 50 out · 2 ms",
      bad: true,
    })
    expect(thenLine(ulFold.steps[2], ulRun, false)).toBe(
      "■ usage limit reached at step 2 (input 300/250, output 150/250)"
    )
    // The steps before it read plainly, and lead on.
    expect(stepVerdict(ulFold.steps[1], ulRun)).toEqual({
      text: "tool_calls · 100 in · 50 out · 2 ms",
      bad: false,
    })
    expect(thenLine(ulFold.steps[1], ulRun, true)).toBe("→ step 2")
    expect(repliedLine(ulFold.steps[2])).toBe("called more({})")
    expect(toolsLine(ulFold.steps[2])).toBe("1 ran")
  })

  it("a step that never finished: the run's error, or still running", () => {
    const open = { index: 1, toolCalls: [] }
    expect(
      stepVerdict(open, {
        status: "failed",
        err: "weft: run failed at step 1: provider 500",
      })
    ).toEqual({ text: "✗ failed: provider 500", bad: true })
    expect(stepVerdict(open, { status: "running" }, 12)).toEqual({
      text: "running… · 12 ms",
      bad: false,
    })
    expect(repliedLine(open, true)).toBe("— (not yet)")
    expect(repliedLine(open)).toBe("— (no reply recorded)")
    expect(thenLine(open, { status: "running" }, false)).toBe("running…")
  })

  it("the director's last step: its words, no tool, the run's end", () => {
    const last = FOLD.steps[1]
    expect(repliedLine(last)).toBe("“We have 42 units of A-17 in Lyon.”")
    expect(toolsLine(last)).toBe("none — the model called no tool")
    expect(
      thenLine(
        last,
        { status: "succeeded", stopped_by: "final_answer", steps: 2 },
        false
      )
    ).toBe("■ the model answered at step 1")
    expect(toolsLine(FOLD.steps[0], true)).toBe("1 ran")
  })

  it("sent: counts, the new ones, the offered tools and the marks", () => {
    expect(sentLine({ count: 5, prevCount: 3, tools: 1 })).toBe(
      "5 messages (2 new) · 1 tool offered · unchanged since the step before"
    )
    expect(sentLine({ count: 1, tools: 2 })).toBe("1 message · 2 tools offered")
    expect(
      sentLine({ count: 4, prevCount: 2, tools: 0, promptChanged: true })
    ).toBe("4 messages (2 new) · 0 tools offered · prompt changed at this step")
    expect(sentLine(undefined)).toBe("— no request record for this step")
  })
})

describe("the landing selection (UX-A-03)", () => {
  it("a usage-limit failure lands on the step it failed at, on either scale", () => {
    expect(defaultRowSel(ulTree("time").rows, failedAtStep(ulRun))).toBe("s2")
    expect(defaultRowSel(ulTree("order").rows, failedAtStep(ulRun))).toBe("s2")
  })

  it("a run that went well lands on step 0", () => {
    const rows = waterfallRows({
      runId: DIR,
      spans: SPANS,
      fold: FOLD,
      total: 9,
    }).rows
    expect(defaultRowSel(rows)).toBe("s0")
  })

  it("the first failed row wins over the failed step; never the root", () => {
    const spans = UL_SPANS.map((s) =>
      s.span_id === "u_t1" ? { ...s, status: "error" as const } : s
    )
    const rows = ulTree("time", spans).rows
    expect(rows[0].status).toBe("error")
    expect(defaultRowSel(rows, 2)).toBe("c:1:c1")
  })
})

describe("each stop lands on the step every surface names (U10.1-fix #2, #3)", () => {
  // Core's error text as each stop writes it: max_steps names the step
  // it would have run next (fail(maxSteps, ErrMaxSteps)); the guards
  // the step they ran after; a cancel during the last step's tools
  // that step.
  const P = "weft: run failed at step"
  const cases = [
    {
      by: "max_steps",
      err: `${P} 3: weft: run exceeded the maximum number of steps`,
      steps: 3,
      at: 2,
      refused: true,
    },
    {
      by: "loop_detected",
      err: `${P} 4: weft: the model repeated the same tool calls too many times: 5 identical steps in a row`,
      steps: 5,
      at: 4,
      refused: true,
    },
    {
      by: "model_retries_exceeded",
      err: `${P} 1: weft: a tool asked the model to retry too many times: tool "x" retried 4 times`,
      steps: 2,
      at: 1,
      refused: true,
    },
    {
      by: "canceled",
      err: `${P} 2: context canceled`,
      steps: 3,
      at: 2,
      refused: false,
    },
    // A cancel before step 3 started names the unstarted step (core's
    // loop-top ctx check, fail(step, ctx.Err()), steps 3): no row draws
    // it, so the landing falls back to the last drawn (landStep).
    {
      by: "canceled",
      err: `${P} 3: context canceled`,
      steps: 3,
      at: 2,
      named: 3,
      refused: false,
    },
  ]
  for (const c of cases)
    it(`${c.by}: ${c.err.slice(P.length + 1, P.length + 3)}`, () => {
      const run = {
        status: "failed",
        err: c.err,
        stopped_by: c.by,
        steps: c.steps,
      }
      expect(failedAtStep(run)).toBe(c.named ?? c.at)
      const rows = [
        { key: "r:R", runId: "R", kind: "run", status: "error" },
        ...Array.from({ length: c.steps }, (_, i) => ({
          key: `s${i}`,
          runId: "R",
          parent: "r:R",
          kind: "step",
          status: "ok",
        })),
      ] as unknown as Parameters<typeof defaultRowSel>[0]
      expect(defaultRowSel(rows, failedAtStep(run))).toBe(`s${c.at}`)
      const last = {
        index: c.at,
        toolCalls: [{ name: "x", finishPos: 1 }],
        finish: { reason: "tool_calls" },
      }
      // The landed step is the last drawn: no step after it.
      const v = stepVerdict(last, run, undefined, true)
      expect(v.bad).toBe(true)
      expect(v.text.includes("the loop refused")).toBe(c.refused)
      if (c.refused)
        expect(v.text).toContain(`the loop refused step ${c.at + 1}`)
      // The end card names the same step the drawer landed on.
      expect(thenLine(last, run, false)).toContain(`at step ${c.at}`)
      // No earlier step reads as the failure.
      if (c.at > 0)
        expect(stepVerdict({ ...last, index: c.at - 1 }, run).bad).toBe(false)
    })

  it("a model error is no refusal either", () => {
    const run = {
      status: "failed",
      err: "weft: run failed at step 1: boom",
      steps: 2,
    }
    expect(failedAtStep(run)).toBe(1)
    expect(
      stepVerdict({ index: 1, toolCalls: [], finish: { reason: "stop" } }, run)
        .text
    ).toBe("✗ failed: boom · stop · 0 in · 0 out")
  })
})

// P8-fix-a finding 1: the shapes core really writes (core/loop.go).
// A step's record is appended only after its model call and its tools,
// so a model error or a cancel during step N fails with "run failed at
// step N" (fail(step, streamErr) / fail(step, ctx.Err()) inside the
// call) while weft.run.steps is N: step N is the open one — its chat
// span carries the error, the fold holds its step_start — and step N − 1
// succeeded. The guard stops fail(step, err) after step N was appended
// (steps N + 1); max_steps fails at fail(maxSteps, ErrMaxSteps) with
// steps maxSteps; a cancel noticed at the loop's top, before step N's
// StepStart, fails "at step N" with steps N and no row for N.
describe("core's real failure shapes land on the failing step (P8-fix-a #1)", () => {
  const R = "r_core"
  const ev = (o: Record<string, unknown>) => ({ run_id: R, ...o })
  const finished = (i: number) => [
    ev({ type: "step_start", index: i }),
    ev({
      type: "tool_start",
      seq: i + 1,
      call_id: `c${i}`,
      name: "more",
      args: {},
    }),
    ev({
      type: "tool_finish",
      seq: i + 1,
      call_id: `c${i}`,
      name: "more",
      content: "ok",
      is_error: false,
    }),
    ev({
      type: "step_finish",
      index: i,
      reason: "tool_calls",
      usage: { input_tokens: 1, output_tokens: 1 },
    }),
  ]
  const start = {
    type: "run_start",
    id: R,
    agent: "a",
    model: { provider: "wefttest", name: "script" },
  }
  const span = (
    id: string,
    name: string,
    from: number,
    to: number,
    attrs: Record<string, unknown>,
    status = "ok"
  ) => ({
    trace_id: "t",
    span_id: id,
    parent_span_id: id === "inv" ? "" : "inv",
    name,
    start: new Date(Date.UTC(2026, 9, 1, 9, 0, 0, from)).toISOString(),
    end: new Date(Date.UTC(2026, 9, 1, 9, 0, 0, to)).toISOString(),
    status,
    attrs: { "weft.run.id": R, ...attrs },
    run_id: R,
  })
  const chatSpan = (i: number, status = "ok") =>
    span(
      `c${i}`,
      "chat script",
      i * 10 + 1,
      i * 10 + 4,
      { "gen_ai.operation.name": "chat", "weft.step.index": i },
      status
    )
  const toolSpan = (i: number) =>
    span(`t${i}`, "execute_tool more", i * 10 + 5, i * 10 + 8, {
      "gen_ai.operation.name": "execute_tool",
      "weft.step.index": i,
      "gen_ai.tool.name": "more",
      "gen_ai.tool.call.id": `c${i}`,
    })
  const cases: {
    name: string
    err: string
    by?: string
    steps: number
    /** Finished steps, then whether step `steps` has begun (open). */
    open: boolean
    named: number
    land: number
  }[] = [
    {
      name: "a model error at step 0",
      err: "weft: run failed at step 0: model stream: boom",
      steps: 0,
      open: true,
      named: 0,
      land: 0,
    },
    {
      name: "a model error at step 1",
      err: "weft: run failed at step 1: model stream: boom",
      steps: 1,
      open: true,
      named: 1,
      land: 1,
    },
    {
      name: "a cancel during step 1's model call",
      err: "weft: run failed at step 1: context canceled",
      by: "canceled",
      steps: 1,
      open: true,
      named: 1,
      land: 1,
    },
    {
      name: "a cancel before step 2 began",
      err: "weft: run failed at step 2: context canceled",
      by: "canceled",
      steps: 2,
      open: false,
      named: 2,
      land: 1,
    },
    {
      name: "max_steps after steps 0–2",
      err: "weft: run failed at step 3: weft: run exceeded the maximum number of steps",
      by: "max_steps",
      steps: 3,
      open: false,
      named: 2,
      land: 2,
    },
    {
      name: "usage_limit after step 1",
      err: "weft: run failed at step 1: weft: run exceeded its usage limit (input 2/1)",
      by: "usage_limit",
      steps: 2,
      open: false,
      named: 1,
      land: 1,
    },
  ]
  for (const c of cases)
    it(c.name, () => {
      const run = {
        status: "failed",
        err: c.err,
        stopped_by: c.by,
        steps: c.steps,
      }
      expect(failedAtStep(run)).toBe(c.named)
      const n = c.steps
      const events = [
        start,
        ...Array.from({ length: n }, (_, i) => finished(i)).flat(),
        ...(c.open ? [ev({ type: "step_start", index: n })] : []),
      ] as unknown as Parameters<typeof fold>[0]
      const f = fold(events)
      const spans = [
        span(
          "inv",
          "invoke_agent a",
          0,
          100,
          { "gen_ai.operation.name": "invoke_agent", "weft.run.steps": n },
          "error"
        ),
        ...Array.from({ length: n }, (_, i) => [
          chatSpan(i),
          toolSpan(i),
        ]).flat(),
        ...(c.open ? [chatSpan(n, "error")] : []),
      ] as unknown as Parameters<typeof waterfallRows>[0]["spans"]
      for (const scale of ["time", "order"] as const) {
        const rows = waterfallRows(
          {
            runId: R,
            status: "failed",
            spans: scale === "time" ? spans : undefined,
            fold: f,
            total: events.length,
          },
          scale
        ).rows
        const sel = defaultRowSel(rows, failedAtStep(run))
        // The step the selection is under (the drawer's answer).
        let r = rows.find((x) => x.key === sel)
        while (r && r.kind !== "step") r = rows.find((x) => x.key === r!.parent)
        expect(r?.key, `${scale}: ${sel}`).toBe(`s${c.land}`)
      }
      // The verdict row: the landed step (the last drawn) reads the
      // failure; the one before it (if any) does not.
      const steps = f.steps
      const landed = steps.find((s) => s.index === c.land)!
      expect(steps.at(-1)?.index).toBe(c.land)
      expect(stepVerdict(landed, run, undefined, true).bad).toBe(true)
      if (c.land > 0)
        expect(
          stepVerdict(
            steps.find((s) => s.index === c.land - 1)!,
            run,
            undefined,
            false
          ).bad
        ).toBe(false)
      expect(
        landStep(
          c.named,
          steps.map((s) => s.index)
        )
      ).toBe(c.land)
      // The then line ends the run at the landed step.
      if (c.by)
        expect(thenLine(landed, run, false)).toContain(`at step ${c.land}`)
    })

  it("landStep: the named step if drawn, else the last before it, else none", () => {
    expect(landStep(2, [0, 1, 2])).toBe(2)
    expect(landStep(2, [0, 1])).toBe(1)
    expect(landStep(0, [1, 2])).toBeUndefined()
    expect(landStep(undefined, [0])).toBeUndefined()
  })
})

describe("kind labels, tabs and the step a row is under", () => {
  it("a model call is MODEL, never TOOL", () => {
    expect(KIND_LABEL.model).toBe("MODEL")
    expect(KIND_LABEL.attempt).toBe("ATTEMPT")
    expect(KIND_LABEL.run).toBe("RUN")
    expect(KIND_LABEL.tool).toBe("TOOL")
  })

  it("?tab= values: the five tabs, anything else dropped; defaults by kind", () => {
    for (const t of ["request", "response", "tools", "logs", "raw"])
      expect(drawerTabOf(t)).toBe(t)
    expect(drawerTabOf("events")).toBeUndefined()
    expect(defaultTab("step")).toBe("response")
    expect(defaultTab("model")).toBe("request")
    expect(defaultTab("tool")).toBe("tools")
    expect(defaultTab("run")).toBe("response")
    expect(defaultTab("span")).toBe("raw")
  })

  it("stepRowOf walks a model call or a tool up to its step", () => {
    const rows = treeSpans(ulTree("time"))
    expect(stepRowOf("s2:m", rows)?.key).toBe("s2")
    expect(stepRowOf("c:1:c1", rows)?.key).toBe("s1")
    expect(stepRowOf(`r:${UL}`, rows)).toBeUndefined()
  })
})
