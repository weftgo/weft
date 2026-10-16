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
    // A cancel before step 3 started names the unstarted step.
    {
      by: "canceled",
      err: `${P} 3: context canceled`,
      steps: 3,
      at: 2,
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
      expect(failedAtStep(run)).toBe(c.at)
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
      const v = stepVerdict(last, run)
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
