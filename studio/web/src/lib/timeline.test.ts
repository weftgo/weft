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
  gapReason,
  marksByTurn,
  PARKED_BEFORE_STEP,
  SESSION_LEVEL,
  turnGaps,
  unlistedRun,
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
        to: 2,
        text: "t2 · consumed by a steer · delivered into t1",
        mark: "e_a",
      },
      {
        turn: 4,
        to: 4,
        text: "t4 · consumed by a steer · dropped",
        mark: "e_c",
      },
    ])
  })
  it("says a skipped number no receipt names as a not_recorded hole", () => {
    expect(turnGaps([turn(1), turn(3)], [])).toEqual([
      { turn: 2, to: 2, text: "t2", reason: gapReason(2, 2) },
    ])
  })
  it("collapses consecutive unexplained numbers into one range: t1 + t1000 is one gap", () => {
    const far = { ...turn(1), run_id: `${S}-t1000`, turn: 1000 }
    const gaps = turnGaps([turn(1), far], [])
    expect(gaps).toEqual([
      { turn: 2, to: 999, text: "t2–t999", reason: gapReason(2, 999) },
    ])
    expect(gapReason(2, 999)).toMatch(/^no run of these turn numbers/)
    // A receipt in the middle splits the range around the number it took.
    const took = mark(
      "receipt",
      1,
      { input: "steer", receipt_id: "e_m", state: "queued", turn: 500 },
      { entry: "e_m" }
    )
    expect(
      turnGaps([turn(1), far], [took]).map((g) => [g.turn, g.to, g.text])
    ).toEqual([
      [2, 499, "t2–t499"],
      [500, 500, "t500 · consumed by a steer"],
      [501, 999, "t501–t999"],
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
      "↶ rolled back by t9",
    ])
    // A busy policy's end is no failure (UX-B-09): neutral, never red.
    expect(
      marks
        .filter((m) => m.kind === "interrupt")
        .map((m) => markLine(m, turns).tone)
    ).toEqual(["step", "step"])
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
  it("keys a mark by its entry, else kind:at:run; a true duplicate gains its occurrence", () => {
    expect(
      markKeys([
        mark("label", 1, {}, { entry: "e1" }),
        mark("interrupt", 1, {}),
        mark("pin", 1, {}, { entry: "e2" }),
        mark("pin", 1, {}, { entry: "e2" }),
        mark("interrupt", null, {}),
      ])
    ).toEqual([
      "e1",
      `interrupt:2026-10-19T12:00:01.100Z:${S}-t1`,
      "e2",
      "e2:1",
      "interrupt:2026-10-19T12:00:01.100Z:session",
    ])
  })
  it("keeps every key when a live session grows a mark earlier in the list", () => {
    const before = [
      mark("interrupt", 6, {}, { at: "2026-10-19T12:00:06.000Z" }),
      mark("label", 6, {}, { entry: "e9" }),
    ]
    const grown = [mark("pin", 1, {}, { entry: "e0" }), ...before]
    expect(markKeys(grown).slice(1)).toEqual(markKeys(before))
  })
})

describe("the parking step of a re-park (core's resolvePending)", () => {
  it("links the resume's pre-step card when it parked before step 0", () => {
    const ts = [
      turn(1, { pending: 1, resumed_by: `${S}-t2` }),
      turn(2, { steps: 0, pending: 1, resumed_from: `${S}-t1` }),
    ]
    const req = mark(
      "approval.request",
      2,
      { call_id: "r9", tool: "issue_refund" },
      { lane: "approvals", entry: "e_q" }
    )
    const { links } = markDetail(req, ts)
    expect(links[0].label).toBe(`parked in t2 · ${PARKED_BEFORE_STEP}`)
    expect(links[0].link.search).toMatchObject({ sel: "c:resume:r9" })
    expect(links[0].link.search).not.toHaveProperty("step")
    // A park in a step links that step.
    const step = markDetail({ ...req, run_id: `${S}-t1`, turn: 1 }, ts)
    expect(step.links[0].label).toBe("parked in t1 · step 0")
  })
})

describe("where a mark is filed (finding 7)", () => {
  it("tells a session-level mark from one under a run this page does not list", () => {
    const loose = mark("grant.revoke", null, { grant_id: "g1" })
    const gone = mark("label", 42, { name: "x" })
    const ts = [turn(1)]
    const row = (m: SessionMark) =>
      markDetail(m, ts).rows.find((r) => r.k === "filed under")?.v
    expect(row(loose)).toBe(SESSION_LEVEL)
    expect(row(gone)).toBe(unlistedRun(`${S}-t42`))
    expect(marksByTurn({ turns: ts, marks: [loose, gone] })).toMatchObject({
      loose: [0],
      unlisted: [1],
    })
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
  it("marks an estimate at the turn's model with ≈ and its reason", () => {
    const est = summarizerView({
      usage: { input_tokens: 1800, output_tokens: 60 },
      cost_usd: 0.0045,
      cost_known: true,
      cost_badge: "estimate",
      cost_reason:
        "priced at the turn's model: the summarizer's model was not recorded",
      cache: "not_reported",
    })
    expect(est?.text).toBe("+ summarizer ≈$0.0045")
    expect(est?.badge).toBe("estimate")
    expect(est?.title).toContain("an estimate: priced at the turn's model")
  })
})
