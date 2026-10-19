// lib/timeline.ts (U15.1): the timeline's grouping, words and links,
// on the shapes the seed's sessions answer GET /api/sessions/{id}/
// timeline with — incident-analyst's busy turns (a steer that took t2
// and was delivered into t1, one that took t4 and was dropped, an
// interrupt and a rollback), research-notebook's priced compaction.
import { describe, expect, it } from "vitest"

import type { SessionMark, TimelineTurn } from "./api"
import { summarizerView } from "./cost"
import {
  exchanges,
  exchangesWords,
  markDetail,
  markKeys,
  markLine,
  resumeOfTurn,
  turnGaps,
  turnSiblings,
} from "./timeline"

const S = "s_war"
const turn = (n: number, over: Partial<TimelineTurn> = {}): TimelineTurn => ({
  run_id: `${S}-t${n}`,
  turn: n,
  agent: "incident-analyst",
  status: "succeeded",
  started: `2026-10-19T12:00:0${n}.000Z`,
  finished: `2026-10-19T12:00:0${n}.500Z`,
  last_seen: `2026-10-19T12:00:0${n}.500Z`,
  steps: 1,
  usage: { input_tokens: 10, output_tokens: 5 },
  marks: 0,
  prompt_preview: `prompt ${n}`,
  ...over,
})
const mark = (
  kind: string,
  run: number | null,
  fields: Record<string, unknown>,
  over: Partial<SessionMark> = {}
): SessionMark => ({
  at: "2026-10-19T12:00:01.100Z",
  record: kind === "compaction" ? "compaction" : "thread",
  kind,
  event: `weft.thread.${kind}`,
  lane: "steer_queue",
  run_id: run === null ? null : `${S}-t${run}`,
  turn: run,
  content_mark: "none",
  fields,
  ...over,
})

const turns = [1, 3, 5, 6, 7, 8, 9].map((n) =>
  turn(
    n,
    n === 6 || n === 8 ? { status: "failed", error_type: "cancelled" } : {}
  )
)
const marks: SessionMark[] = [
  mark(
    "receipt",
    1,
    {
      input: "steer",
      minted_run: `${S}-t2`,
      receipt_id: "e_a",
      state: "queued",
      turn: 2,
    },
    {
      entry: "e_a",
      content: { text: "Focus only on the EU region." },
      content_mark: "full",
    }
  ),
  mark(
    "receipt",
    1,
    { input: "steer", receipt_id: "e_a", state: "delivered" },
    { entry: "e_b" }
  ),
  mark(
    "receipt",
    3,
    {
      input: "steer",
      minted_run: `${S}-t4`,
      receipt_id: "e_c",
      state: "queued",
      turn: 4,
    },
    { entry: "e_c" }
  ),
  mark(
    "receipt",
    3,
    { input: "steer", receipt_id: "e_c", state: "dropped" },
    { entry: "e_d" }
  ),
  mark(
    "receipt",
    3,
    {
      input: "send",
      minted_run: `${S}-t5`,
      receipt_id: "e_e",
      state: "accepted",
      turn: 5,
    },
    { entry: "e_e" }
  ),
  mark("interrupt", 6, {
    by_run: `${S}-t7`,
    by_turn: 7,
    policy: "interrupt",
    target: "run",
  }),
  mark("interrupt", 8, {
    by_run: `${S}-t9`,
    by_turn: 9,
    policy: "rollback",
    target: "run",
  }),
]

describe("turnGaps (UX-E-19)", () => {
  it("says which receipt took each skipped turn number and what became of it", () => {
    expect(turnGaps(turns, marks)).toEqual([
      {
        turn: 2,
        text: "t2 · consumed by a steer · delivered into t1",
        mark: "e_a",
      },
      { turn: 4, text: "t4 · consumed by a steer · dropped", mark: "e_c" },
    ])
  })
  it("says a skipped number no receipt names", () => {
    expect(turnGaps([turn(1), turn(3)], [])).toEqual([
      { turn: 2, text: "t2 · no run of this turn number was recorded" },
    ])
  })
})

describe("markLine", () => {
  it("words the busy policies' marks", () => {
    const words = marks.map((m) => {
      const l = markLine(m, turns)
      return `${l.icon} ${l.text}`
    })
    expect(words).toEqual([
      "⏭ steer queued · took t2 'Focus only on the EU region.'",
      "⇢ steer delivered into t1",
      "⏭ steer queued · took t4",
      "🗑 steer dropped",
      "⏭ send accepted → ran as t5",
      "✗ interrupted by t7",
      "✗ rolled back by t9",
    ])
  })
  it("words a compaction with its counts", () => {
    const c = mark(
      "compaction",
      4,
      {
        entries: 2,
        replaced: 11,
        reason: "manual",
        tokens_before: 6054,
        tokens_after: 2739,
      },
      { lane: "compaction" }
    )
    expect(markLine(c, turns).text).toBe(
      "compaction · 11 messages compacted into 2 · 6.1k → 2.7k tokens · manual"
    )
  })
  it("words a decision as the run page's cards do, with the resume it led to", () => {
    const parked = [
      turn(1, { pending: 1, stopped_by: "parked", resumed_by: `${S}-t2` }),
      turn(2, { resumed_from: `${S}-t1`, prompt_preview: "prompt 1" }),
    ]
    const d = mark(
      "approval.decision",
      1,
      { call_id: "r3", outcome: "resolve", via: "user", request_entry: "e_r" },
      { lane: "approvals", content: { content: '{"status":"queued"}' } }
    )
    expect(markLine(d, parked).text).toBe("resolved → t2")
    const deny = mark(
      "approval.decision",
      1,
      { call_id: "r1", outcome: "deny", via: "expiry" },
      { lane: "approvals", content: { reason: "expired: no decision in 10m" } }
    )
    expect(markLine(deny, parked).text).toBe(
      "expired · 'no decision in 10m' → t2"
    )
  })
})

describe("markDetail", () => {
  it("links an interrupt's interrupting turn and names the run it is filed under", () => {
    const { rows, links } = markDetail(marks[5], turns)
    expect(rows.find((r) => r.k === "filed under")?.v).toBe("t6")
    expect(links.map((l) => l.label)).toEqual(["filed under t6", "by t7"])
  })
  it("names a summarizer call's tokens on a compaction", () => {
    const c = mark(
      "compaction",
      4,
      {
        entries: 2,
        replaced: 11,
        summarizer_usage: { input_tokens: 1800, output_tokens: 60 },
      },
      { lane: "compaction", entry: "e_k" }
    )
    expect(markDetail(c, turns).rows.find((r) => r.k === "summarizer")?.v).toBe(
      "1.8k in / 60 out"
    )
  })
})

describe("exchanges (UX-S3-05)", () => {
  it("groups a resume under its park by the stored link, or by the resume rule without one", () => {
    const ts = [
      turn(1, { pending: 1 }),
      turn(2, { prompt_preview: "prompt 1" }), // no link: the rule
      turn(3, { pending: 1, resumed_by: `${S}-t4` }),
      turn(4, { resumed_from: `${S}-t3`, prompt_preview: "new words" }),
      turn(5),
    ]
    expect(
      exchanges(ts).map((e) => [e.lead.turn, ...e.resumes.map((r) => r.turn)])
    ).toEqual([[1, 2], [3, 4], [5]])
    expect(exchangesWords(ts)).toBe("3 exchanges · 5 turns")
    expect(exchangesWords([turn(1)])).toBe("1 turn")
    expect(resumeOfTurn(`${S}-t1`, ts)?.turn).toBe(2)
    expect(resumeOfTurn(`${S}-t3`, ts)?.turn).toBe(4)
  })
})

describe("markKeys", () => {
  it("keys a mark by its entry, by its position when it has none or shares one", () => {
    expect(
      markKeys([
        mark("label", 1, {}, { entry: "e1" }),
        mark("interrupt", 1, {}),
        mark("pin", 1, {}, { entry: "e2" }),
        mark("pin", 1, {}, { entry: "e2" }),
      ])
    ).toEqual(["e1", "m1", "m2", "m3"])
  })
})

describe("turnSiblings (UX-B-21)", () => {
  it("places a turn among its session's turns", () => {
    const rows = [3, 1, 2].map((n) => ({ id: `${S}-t${n}`, turn: n }))
    const at = turnSiblings({ id: `${S}-t2` }, rows)
    expect(at).toMatchObject({ index: 2, total: 3 })
    expect(at?.prev?.turn).toBe(1)
    expect(at?.next?.turn).toBe(3)
    expect(turnSiblings({ id: "other" }, rows)).toBeNull()
  })
})

describe("summarizerView (UX-B-13)", () => {
  it("reads '+ summarizer $x', its tokens in the title; unknown as —", () => {
    const known = summarizerView({
      usage: { input_tokens: 1800, output_tokens: 60 },
      cost_usd: 0.000306,
      cost_known: true,
      cache: "not_reported",
    })
    expect(known?.text).toBe("+ summarizer $0.0003")
    expect(known?.title).toMatch(/1,800 in \/ 60 out tokens/)
    const unpriced = summarizerView({
      usage: { input_tokens: 10, output_tokens: 5 },
      cost_usd: null,
      cost_known: false,
      cost_badge: "unpriced",
      cost_reason: "acme/x has no price",
    })
    expect(unpriced?.text).toBe("+ summarizer —")
    expect(unpriced?.title).toContain("acme/x has no price")
    expect(summarizerView(undefined)).toBeNull()
  })
})
