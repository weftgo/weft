// U15.2: the session tree's placement rule (lib/session-tree.ts) and
// the compaction words off the story (lib/compaction.ts).
import { describe, expect, it } from "vitest"

import type { RunCompaction, TimelineTurn } from "./api"
import { compactionBadges, compactionChip } from "./compaction-studio"
import { sessionTree, treeRows } from "./session-tree"

const turn = (n: number): TimelineTurn => ({
  run_id: `s-t${n}`,
  turn: n,
  agent: "a",
  status: "succeeded",
  started: "2026-10-19T00:00:00Z",
  finished: null,
  last_seen: "2026-10-19T00:00:01Z",
  steps: 1,
  usage: { input_tokens: 1, output_tokens: 1 },
  marks: 0,
})

describe("sessionTree", () => {
  it("places a turn on the line weft.turn.parent names, the newest line current", () => {
    const turns = [1, 2, 3, 4].map(turn)
    const rows: { id: string; meta: Record<string, string> }[] = [
      { id: "s-t1", meta: {} },
      { id: "s-t2", meta: {} },
      { id: "s-t3", meta: { "weft.turn.parent": "1" } },
      { id: "s-t4", meta: {} },
    ]
    const flat = treeRows(sessionTree(turns, rows, []))
    expect(
      flat.map((r) => [r.node.turn.turn, r.depth, r.node.onPath, r.fork])
    ).toEqual([
      [1, 0, true, false],
      [2, 1, false, true],
      [3, 0, true, false],
      [4, 0, true, false],
    ])
  })
  it("reads a parent of 0, or one at or after the turn, as a root", () => {
    const turns = [1, 2, 3].map(turn)
    const rows: { id: string; meta: Record<string, string> }[] = [
      { id: "s-t1", meta: {} },
      { id: "s-t2", meta: { "weft.turn.parent": "0" } },
      { id: "s-t3", meta: { "weft.turn.parent": "9" } },
    ]
    const roots = sessionTree(turns, rows, [])
    // t3's parent is no recorded turn: a root, and the current line.
    expect(roots.map((r) => [r.turn.turn, r.onPath])).toEqual([
      [1, false],
      [2, false],
      [3, true],
    ])
  })
  it("draws a long linear session as one column without recursion", () => {
    const turns = Array.from({ length: 5000 }, (_, i) => turn(i + 1))
    const flat = treeRows(sessionTree(turns, [], []))
    expect(flat).toHaveLength(5000)
    expect(flat.every((r) => r.depth === 0 && r.node.onPath)).toBe(true)
  })
})

describe("compaction off the story", () => {
  const view: RunCompaction = {
    scope: "run",
    step: 3,
    index: 7,
    hash: "h",
    replaced: 4,
    entries: 1,
  }
  const marker: RunCompaction = {
    scope: "session",
    hash: "m",
    replaced: 11,
    entries: 2,
  }
  it("words the header's chips", () => {
    expect(compactionChip(view)).toBe("⌗ compacted at step 3 (4 → 1)")
    expect(compactionChip(marker)).toBe(
      "⌗ session compacted after this run (11 → 2)"
    )
  })
  it("marks the root run's step row whose request saw a view", () => {
    const rows: { key: string; runId: string; badge?: string }[] = [
      { key: "s2", runId: "r" },
      { key: "s3", runId: "r", badge: "retried" },
      { key: "s3", runId: "child" },
    ]
    expect(
      compactionBadges(rows, "r", [view, marker]).map((r) => r.badge)
    ).toEqual([undefined, "retried · ⌗ compacted", undefined])
  })
})
