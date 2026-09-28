// The trace (spansFromFold): rows in document order, subagents nested
// inside their call with the parent's positions, open spans honest.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"

import type { EventsPage, WireEvent } from "./api"
import { fold } from "./events"
import { defaultSelection, flowFromFold, spansFromFold } from "./trace"

function golden(name: string): WireEvent[] {
  const path = resolve(process.cwd(), `../testdata/api/${name}`)
  return (JSON.parse(readFileSync(path, "utf8")) as EventsPage).events.map(
    (pe) => pe.event
  )
}

describe("spansFromFold", () => {
  it("nests the subagent under its call, on the parent's axis", () => {
    const events = golden("events-sub.golden.json")
    const spans = spansFromFold(fold(events), events.length, "succeeded")
    const kinds = spans.map((s) => `${s.depth}:${s.kind}:${s.label}`)
    expect(kinds).toEqual([
      "0:run:orders",
      "1:step:step 0",
      "2:tool:research",
      "3:subagent:researcher",
      "4:step:step 0",
      "1:step:step 1",
    ])
    const [run, step0, call, child, childStep, step1] = spans
    expect([run.from, run.to]).toEqual([0, 13])
    expect([step0.from, step0.to]).toEqual([1, 9])
    expect([call.from, call.to]).toEqual([2, 8]) // tool_start … tool_finish
    expect([child.from, child.to]).toEqual([3, 7]) // nested run_start … run_finish
    expect([childStep.from, childStep.to]).toEqual([4, 6])
    expect([step1.from, step1.to]).toEqual([10, 12])
    expect(call.tone).toBe("tool")
    expect(call.badge).toBe("ok")
    expect(call.sub).toBe("29 B")
    expect(child.parent).toBe(call.id)
    expect(step1.target).toEqual({ step: 1 })
    // Short, URL-safe keys; a child's step names its run.
    expect(spans.map((s) => s.key)).toEqual([
      "run",
      "s0",
      "c:call_1",
      "r:r_sub/0/call_1",
      "s:r_sub/0/call_1:0",
      "s1",
    ])
    // Nothing went wrong: start at the first step.
    expect(defaultSelection(spans)?.key).toBe("s0")
    // The flow strip: one pill per top-level step.
    expect(flowFromFold(fold(events), "succeeded")).toEqual([
      {
        key: "s0",
        index: 0,
        gist: 'research({"prompt":"status of order 42"})',
        finish: "tool_calls",
        bad: false,
        open: false,
      },
      {
        key: "s1",
        index: 1,
        gist: "Order 42 shipped.",
        finish: "stop",
        bad: false,
        open: false,
      },
    ])
  })

  it("draws an open call as running while live and never after", () => {
    const events: WireEvent[] = [
      { type: "run_start", id: "r", model: { provider: "p", name: "m" } },
      { type: "step_start", run_id: "r", index: 0 },
      {
        type: "tool_start",
        run_id: "r",
        seq: 1,
        call_id: "c1",
        name: "slow",
        args: {},
      },
    ]
    const live = spansFromFold(fold(events), events.length, "running")
    expect(live[2].to).toBeNull()
    expect(live[2].tone).toBe("running")
    const dead = spansFromFold(fold(events), events.length, "failed")
    expect(dead[2].tone).toBe("never")
    expect(dead[2].badge).toBe("never")
    expect(dead[1].tone).toBe("bad")
    expect(dead[1].sub).toBe("failed here")
    // Start where it went wrong.
    expect(defaultSelection(dead)?.key).toBe("s0")
  })

  it("is empty for an empty stream", () => {
    expect(spansFromFold(fold([]), 0, "running")).toHaveLength(1)
    expect(spansFromFold(fold([]), 0, "running")[0].kind).toBe("run")
  })
})
