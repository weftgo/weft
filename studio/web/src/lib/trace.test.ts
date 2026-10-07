// The trace (spansFromFold): rows in document order, subagents nested
// inside their call with the parent's positions, open spans honest.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"

import type { EventsPage, WireEvent, Span as TimedSpan  } from "./api"
import { fold } from "./events"
import {
  defaultSelection,
  flowFromFold,
  isGenAISpan,
  spansFromFold,
  spansFromTimed,
  timeDomain,
} from "./trace"

function golden(name: string): WireEvent[] {
  const path = resolve(process.cwd(), `../testdata/api/${name}`)
  return (JSON.parse(readFileSync(path, "utf8")) as EventsPage).events.map(
    (pe) => pe.event
  )
}

describe("spansFromFold", () => {
  it("lays the durable stream out: run, steps, calls (no nested rows)", () => {
    const events = golden("events-sub.golden.json")
    const spans = spansFromFold(fold(events), events.length, "succeeded")
    // A subagent's child run is NOT in the parent's stream (S4.3):
    // the rows are the parent's own; the child joins by
    // parent_call_id on the page, not in the fold.
    const kinds = spans.map((s) => `${s.depth}:${s.kind}:${s.label}`)
    expect(kinds).toEqual(["0:run:orders", "1:step:step 0", "2:tool:research"])
    const [run, step0, call] = spans
    expect([run.from, run.to]).toEqual([0, 5])
    expect([step0.from, step0.to]).toEqual([1, 4])
    expect([call.from, call.to]).toEqual([2, 3]) // tool_start … tool_finish
    expect(call.tone).toBe("tool")
    expect(call.badge).toBe("ok")
    expect(spans.map((s) => s.key)).toEqual(["run", "s0", "c:call_3"])
    // Nothing went wrong: start at the first step.
    expect(defaultSelection(spans)?.key).toBe("s0")
    // The flow strip: one pill per top-level step; no stored text, so
    // the pill is the call.
    expect(flowFromFold(fold(events), "succeeded")).toEqual([
      {
        key: "s0",
        index: 0,
        gist: 'research({"prompt":"status of order 42"})',
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

describe("spansFromTimed (the time axis, S4.7)", () => {
  const t0 = Date.parse("2026-10-01T09:00:00Z")
  const span = (
    id: string,
    parent: string,
    name: string,
    startMs: number,
    endMs: number,
    attrs: Record<string, unknown> = {}
  ): TimedSpan => ({
    trace_id: "tr",
    span_id: id,
    parent_span_id: parent,
    name,
    kind: "internal",
    start: new Date(t0 + startMs).toISOString(),
    end: new Date(t0 + endMs).toISOString(),
    status: "ok",
    status_message: "",
    service: "svc",
    attrs,
    events: [],
  })

  it("nests by parent_span_id on a millisecond domain", () => {
    const timed = [
      span("a", "", "invoke_agent", 0, 100, { "weft.run.id": "r" }),
      span("b", "a", "chat glm", 5, 40, {
        "gen_ai.operation.name": "chat",
      }),
      span("c", "b", "execute_tool", 10, 20),
    ]
    const spans = spansFromTimed(timed)
    expect(spans.map((s) => `${s.depth}:${s.label}`)).toEqual([
      "0:invoke_agent",
      "1:chat glm",
      "2:execute_tool",
    ])
    expect([spans[0].from, spans[0].to]).toEqual([0, 100])
    expect([spans[1].from, spans[1].to]).toEqual([5, 40])
    expect(spans[1].timed?.span.attrs["gen_ai.operation.name"]).toBe("chat")
    expect(timeDomain(timed)).toEqual([0, 100])
    expect(isGenAISpan(timed[1])).toBe(true)
    expect(isGenAISpan(timed[0])).toBe(false)
  })

  // The API returns spans in start order, not tree order: two parallel
  // tools start before either one's children. Rows indent by depth, so
  // they must come out parent-then-subtree or a child reads as nested
  // under the wrong sibling.
  it("orders rows as a tree: each parent, then its whole subtree", () => {
    const timed = [
      span("root", "", "invoke_agent orders", 0, 100),
      span("toolA", "root", "execute_tool a", 10, 60),
      span("toolB", "root", "execute_tool b", 11, 70),
      span("a1", "toolA", "invoke_agent child-a", 12, 50),
      span("b1", "toolB", "invoke_agent child-b", 13, 55),
    ]
    expect(spansFromTimed(timed).map((s) => `${s.depth}:${s.label}`)).toEqual([
      "0:invoke_agent orders",
      "1:execute_tool a",
      "2:invoke_agent child-a",
      "1:execute_tool b",
      "2:invoke_agent child-b",
    ])
  })

  // The core names its spans "<operation> <name>" ("invoke_agent
  // planner", observe.go's spanName), never the bare operation the
  // golden fixtures carry: the run row is told by the operation.
  it("tells an agent span by its operation, not an exact name", () => {
    const [run, chat] = spansFromTimed([
      span("a", "", "invoke_agent planner", 0, 10, {
        "gen_ai.operation.name": "invoke_agent",
      }),
      span("b", "a", "chat glm", 1, 5, { "gen_ai.operation.name": "chat" }),
    ])
    expect(run.kind).toBe("run")
    expect(chat.kind).toBe("tool")
  })

  // Ingest stores what it is sent: a trace can name a parent that is
  // missing, itself, or a cycle. Every span still gets one row, cycles
  // are cut, and nothing loops.
  it("survives missing parents, self-parents and cycles", () => {
    const timed = [
      span("orphan", "gone", "orphan", 0, 5),
      span("self", "self", "self", 1, 6),
      span("x", "y", "x", 2, 7),
      span("y", "x", "y", 3, 8),
    ]
    const rows = spansFromTimed(timed)
    expect(rows.map((r) => r.label).sort()).toEqual(["orphan", "self", "x", "y"])
    const byId = new Map(rows.map((r) => [r.id, r]))
    for (const r of rows) {
      // Walking up from any row terminates.
      let p = r.parent
      let hops = 0
      while (p && hops < 10) {
        p = byId.get(p)?.parent
        hops++
      }
      expect(hops).toBeLessThan(10)
    }
  })

  it("handles a trace too large to spread into Math.min", () => {
    const timed = Array.from({ length: 200_000 }, (_, i) =>
      span(`s${i}`, "", "work", i, i + 1)
    )
    expect(timeDomain(timed)).toEqual([0, 200_000])
    expect(spansFromTimed(timed)).toHaveLength(200_000)
  })

  it("keeps unparseable times off the axis instead of NaN", () => {
    const bad = { ...span("a", "", "a", 0, 10), start: "nope", end: "nope" }
    const [row] = spansFromTimed([bad])
    expect(row.from).toBe(0)
    expect(row.to).toBeNull()
    expect(timeDomain([bad])).toEqual([0, 1])
  })

  it("is empty without spans", () => {
    expect(spansFromTimed([])).toEqual([])
    expect(timeDomain([])).toEqual([0, 1])
  })
})
