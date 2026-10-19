// lib/timeline.ts (U15.1): the timeline's grouping, words and links,
// on the shapes the seed's sessions answer GET /api/sessions/{id}/
// timeline with — incident-analyst's busy turns (a steer that took t2
// and was delivered into t1, one that took t4 and was dropped, an
// interrupt and a rollback), research-notebook's priced compaction.
import { describe, expect, it } from "vitest"

import type { SessionMark, TimelineTurn } from "./api"
import { summarizerView } from "./cost"
import {
  busyEndSpans,
  compactionStarts,
  contextRows,
  decisionOf,
  endedByPolicy,
  endLine,
  isoCompare,
  startedFrom,
  startedWords,
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
  it("names a decision's own actor, never the resume's several (P9-pre-web-fix2 1)", () => {
    const parked = [
      turn(1, { pending: 2, stopped_by: "parked", resumed_by: `${S}-t2` }),
      turn(2, { resumed_from: `${S}-t1` }),
    ]
    const own = mark(
      "approval.decision",
      1,
      { call_id: "r1", outcome: "approve", key_id: "k_dave" },
      { lane: "approvals", content_hidden: true, actor: "k_dave" }
    )
    expect(markLine(own, parked).text).toBe("approved · k_dave → t2")
    const list = mark(
      "approval.decision",
      1,
      { call_id: "r2", outcome: "deny" },
      {
        lane: "approvals",
        content_hidden: true,
        actor: "bob,k_dave",
        actor_of: "resume",
      }
    )
    expect(decisionOf(list).c.who_hidden).toBe(true)
    expect(markLine(list, parked).text).not.toContain("bob")
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

// ── U15.2-fix ───────────────────────────────────────────────────────

const comp = (run: number, at: string, reason: string, hash: string) =>
  mark(
    "compaction",
    run,
    {
      scope: "session",
      hash,
      reason,
      replaced: 3,
      entries: 1,
      tokens_before: 9000,
      tokens_after: 4000,
    },
    { at, lane: "compaction", event: "weft.compaction" }
  )

describe("compactionStarts (U15.2-fix: the started-on turn by time)", () => {
  it("names every compaction since the turn before, the next turn to start after each", () => {
    // t4 (12:00:04–04.5) leaves a trim and a manual one; t5 starts at
    // 05 on both — t3's earlier trim is t4's.
    const ts = [3, 4, 5].map((n) => turn(n))
    const ms = [
      comp(3, "2026-10-19T12:00:03.700Z", "trim", "h1"),
      comp(4, "2026-10-19T12:00:04.600Z", "trim", "h2"),
      comp(4, "2026-10-19T12:00:04.900Z", "manual", "h3"),
    ]
    const starts = compactionStarts(ts, ms)
    expect(starts.map((s) => [s.mark.hash, s.on, s.held])).toEqual([
      ["h1", `${S}-t4`, false],
      ["h2", `${S}-t5`, false],
      ["h3", `${S}-t5`, false],
    ])
    const rows = contextRows(ts, ms)
    expect(rows[2].started.map((s) => s.mark.hash)).toEqual(["h2", "h3"])
    expect(startedWords(rows[2].started, (r) => r.slice(-2))).toBe(
      "started on a compacted context · compacted after t4: ⌗ trim 9.0k → 4.0k · ⌗ manual 9.0k → 4.0k"
    )
    // A marker past the newest turn: no turn started on it yet.
    expect(
      compactionStarts(ts, [
        comp(5, "2026-10-19T12:00:05.900Z", "trim", "h4"),
      ])[0].on
    ).toBeUndefined()
  })

  it("after a Branch back, the marker filed under an older turn on the line opens the next turn to start — not the next turn number", () => {
    // t1, t2; Branch back to t1's answer; a compaction filed under t1
    // (the last run on the current path) lands after t2 ended; t3 is
    // the turn that started on it, never t2.
    const ts = [1, 2, 3].map((n) => turn(n))
    const s = compactionStarts(ts, [
      comp(1, "2026-10-19T12:00:02.800Z", "manual", "hb"),
    ])
    expect(s[0].on).toBe(`${S}-t3`)
    expect(s[0].held).toBe(false)
    expect(
      contextRows(ts, [
        comp(1, "2026-10-19T12:00:02.800Z", "manual", "hb"),
      ]).map((r) => r.started.length)
    ).toEqual([0, 0, 1])
  })

  it("a held marker (filed under the run that started on it) opens its own run", () => {
    // thread held the marker (no run produced the context) and emitted
    // it as t2 reported: it landed inside t2's span.
    const ts = [1, 2].map((n) => turn(n))
    const held = comp(2, "2026-10-19T12:00:02.000300Z", "manual", "hh")
    const [s] = compactionStarts(ts, [held])
    expect(s.held).toBe(true)
    expect(s.on).toBe(`${S}-t2`)
    expect(startedFrom(s, (r) => r)).toBe("compacted before this turn")
    // The lane: t2 started on it; it is not one of t2's after-marks.
    const rows = contextRows(ts, [held])
    expect(rows[1].started).toHaveLength(1)
    expect(rows[1].marks).toEqual([])
    // A marker recorded before its own run started (the run it was
    // held for started after the compaction): that run started on it.
    const early = comp(2, "2026-10-19T12:00:01.800Z", "manual", "he")
    expect(compactionStarts(ts, [early])[0]).toMatchObject({
      held: true,
      on: `${S}-t2`,
    })
  })

  it("orders times to the nanosecond: a marker 0.4 ms after its run's finish is after it", () => {
    const t = turn(1, { finished: "2026-10-19T12:00:01.500100Z" })
    const [s] = compactionStarts(
      [t, turn(2)],
      [comp(1, "2026-10-19T12:00:01.500500Z", "trim", "hn")]
    )
    expect(s.held).toBe(false)
    expect(s.on).toBe(`${S}-t2`)
    expect(
      isoCompare("2026-10-19T12:00:01.5001Z", "2026-10-19T12:00:01.500100001Z")
    ).toBeLessThan(0)
  })
})

describe("the branch mark's words (U15.2-fix)", () => {
  it("a Branch to the entry the leaf was on says no line was left", () => {
    const b = (from: string, to: string, summarized = false) =>
      markLine(
        mark(
          "branch",
          3,
          { from_entry: from, to_entry: to, summarized },
          { lane: "branch_fork" }
        ),
        turns
      ).text
    expect(b("e7", "e4", true)).toBe(
      "branched · the leaf moved off this line · the left branch summarized"
    )
    expect(b("e14", "e14")).toBe(
      "branched to the entry the leaf was on · no line was left"
    )
  })
})

describe("busy ends on the run page (U15.2-fix, UX-B-09)", () => {
  it("knows thread's cause, words the end line and redraws the run's rows neutral", () => {
    expect(endedByPolicy({ cancel_cause: "thread.interrupted" })).toBe(true)
    expect(endedByPolicy({ cancel_cause: "app.shutdown" })).toBe(false)
    const end = {
      kind: "rolled_back" as const,
      text: "rolled back by t9 · off path",
      title: "",
      mark: "k",
    }
    expect(endLine(end, 0)).toBe("■ rolled back by t9 · off path at step 0")
    const rows = busyEndSpans(
      [
        { runId: "r", kind: "run", tone: "bad", badge: "error" },
        { runId: "r", kind: "step", tone: "bad", badge: "failed" },
        { runId: "r", kind: "tool", tone: "never", badge: "cancelled" },
        { runId: "r", kind: "tool", tone: "tool", badge: "ok" },
        { runId: "other", kind: "run", tone: "bad", badge: "error" },
      ],
      "r",
      end
    )
    expect(rows.map((r) => [r.tone, r.badge])).toEqual([
      ["step", "rolled back"],
      ["step", "rolled back"],
      ["step", "cut off"],
      ["tool", "ok"],
      ["bad", "error"],
    ])
  })
})
