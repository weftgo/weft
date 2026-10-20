// lib/decisions.ts over studio/testdata/api/decisions.golden.json (the
// run documents' decided / resumes blocks, studio/decisions_test.go):
// the outcome line each surface draws, the resumed card's head, the
// honesty note, the one verb vocabulary, and step 0 without the
// decided calls.
import { describe, expect, it } from "vitest"

import type { DecisionDoc, SessionMark } from "./api"
import {
  decidedResult,
  decider,
  decisionDetail,
  decisionLine,
  decisionsNote,
  DONE,
  expiryNext,
  expiryText,
  marksCapped,
  OUTCOME,
  requestExpiries,
  runMarksPath,
  resumedHead,
  VERB,
  withoutDecided,
} from "./decisions"
import { appVerbWords } from "./honesty"
import { turnTag } from "./status"
import { golden } from "@/test/fake-studio"

const g = golden<Record<string, DecisionDoc>>("decisions")
const line = (k: string, i = 0) => {
  const d = g[k]
  return decisionLine(d.calls[i], d, turnTag(d.run))
}

describe("a decided call's line (U13.2)", () => {
  it("rw.support-desk's shape on a thread session: ✋ denied · agent-7 · 'need a photo of the damage' → t2", () => {
    expect(line("thread_t1_decided")).toBe(
      "✋ denied · agent-7 · 'need a photo of the damage' → t2"
    )
    expect(
      decisionDetail(g.thread_t1_decided.calls[0], g.thread_t1_decided)
    ).toBe("via user · 09:00:00 UTC")
  })

  it("a signed approval names its signer; a resolve and a grant theirs", () => {
    expect(line("thread_t3_decided")).toBe("✓ approved · alice → t4")
    expect(
      decisionDetail(g.thread_t3_decided.calls[0], g.thread_t3_decided)
    ).toMatch(/^via signed · key k_alice · /)
    expect(line("thread_t7_decided")).toBe("↩ resolved · agent-7 → t8")
    // A grant decides with nobody named: the grant id is the provenance.
    expect(line("grants_t1_decided")).toBe(
      `✓ approved → ${turnTag(g.grants_t1_decided.run)}`
    )
    expect(
      decisionDetail(g.grants_t1_decided.calls[0], g.grants_t1_decided)
    ).toMatch(/^via grant · grant e_thread_grants_0002 · /)
  })

  it("a read token: the actor (metadata) stays, who and the reason do not", () => {
    const d = g.read_token_t1_decided
    expect(d.calls[0].who).toBeUndefined()
    expect(d.calls[0].reason_badge).toBe("hidden")
    expect(line("read_token_t1_decided")).toBe("✋ denied · agent-7 → t2")
  })

  it("content off: the reason not_recorded", () => {
    expect(g.content_off_t1_decided.calls[0].reason_badge).toBe("not_recorded")
    expect(line("content_off_t1_decided")).toBe("✋ denied · agent-7 → t2")
  })

  it("a plain Generate: a deny's reason, an undecided call, no actor", () => {
    const d = g.approval_denied_decided
    expect(decider(d.calls[0], d)).toBe("")
    expect(decisionLine(d.calls[0], d)).toBe("✋ denied · 'over the limit'")
    expect(decisionLine(d.calls[1], d)).toBe("✋ denied: no decision")
    // The run's one actor names each call; several name none of them.
    const r = g.resumed_decided
    expect(decisionLine(r.calls[0], r)).toBe("✓ approved · op_42")
    expect(decisionLine(r.calls[1], r)).toBe("↩ resolved · op_42")
    expect(decider(r.calls[0], { ...r, actor: "a,b" })).toBe("")
  })

  it("every outcome the server sends has a word", () => {
    for (const k of Object.values(g))
      for (const c of k.calls)
        expect(OUTCOME[c.outcome], c.outcome).toBeTruthy()
    expect(OUTCOME.expired.word).toBe("expired")
  })
})

describe("the resumed card and its note", () => {
  it("resumed from t1 · 1 decided call", () => {
    const d = g.thread_t2_resumes
    expect(resumedHead(d, turnTag(d.run))).toBe(
      "resumed from t1 · 1 decided call"
    )
    expect(decisionsNote(d, "t2")).toBe("")
  })

  it("the cap, a derived list and a failed read are said", () => {
    const d = g.thread_t2_resumes
    expect(decisionsNote({ ...d, more: 3 }, "t2")).toBe(
      "3 more calls not listed"
    )
    expect(
      decisionsNote(
        {
          ...d,
          source: "transcript",
          calls: [{ ...d.calls[0], derived: true }],
        },
        "t2"
      )
    ).toContain("derived from t2's pre-step results")
    expect(decisionsNote({ ...d, error: "boom" }, "t2")).toBe(
      "the decisions could not be read: boom"
    )
    expect(resumedHead({ ...d, more: 2 }, "t1")).toBe(
      "resumed from t1 · 3 decided calls"
    )
  })

  it("step 0 drops the decided calls only once the card draws them, keeping a child run's", () => {
    const f = {
      steps: [
        {
          index: 0,
          toolCalls: [
            { callId: "r2", resumed: true },
            { callId: "sub", resumed: true, childRunId: "x/resume/sub" },
            { callId: "m1" },
          ],
        },
      ],
    }
    expect(withoutDecided(f, {}).steps[0].toolCalls).toHaveLength(3)
    // The card lists r1: r2 is no call of its, so step 0 keeps it.
    expect(
      withoutDecided(f, {
        resumes: g.thread_t2_resumes,
      }).steps[0].toolCalls.map((c) => c.callId)
    ).toEqual(["r2", "sub", "m1"])
    expect(
      withoutDecided(f, {
        resumes: {
          ...g.thread_t2_resumes,
          calls: [{ call_id: "r2", outcome: "approved" }],
        },
      }).steps[0].toolCalls.map((c) => c.callId)
    ).toEqual(["sub", "m1"])
  })

  // U13.2-fix 1: only the calls the block lists leave step 0.
  it("a block that lists none (a pre-U13.1 / content-off read, a failed read) or stops at its cap leaves the rest in step 0", () => {
    const f = {
      steps: [
        {
          index: 0,
          toolCalls: [
            { callId: "a", resumed: true },
            { callId: "b", resumed: true },
          ],
        },
      ],
    }
    const ids = (d: DecisionDoc) =>
      withoutDecided(f, { resumes: d }).steps[0].toolCalls.map((c) => c.callId)
    const base: DecisionDoc = {
      run: "t1",
      at: "2026-10-01T09:00:00Z",
      source: "none",
      calls: [],
      hole: "not_recorded",
    }
    // (a) no list: both calls stay, and the block says why (its hole).
    expect(ids(base)).toEqual(["a", "b"])
    // (b) a failed read: the error is said, the calls stay.
    expect(ids({ ...base, hole: undefined, error: "boom" })).toEqual(["a", "b"])
    expect(decisionsNote({ ...base, error: "boom" }, "t2")).toBe(
      "the decisions could not be read: boom"
    )
    // (c) past the cap: the listed call goes, the unlisted one stays.
    const capped = {
      ...base,
      hole: undefined,
      source: "resume_calls",
      calls: [{ call_id: "a", outcome: "approved" }],
      more: 1,
    }
    expect(ids(capped)).toEqual(["b"])
    expect(decisionsNote(capped, "t2")).toBe("1 more call not listed")
  })

  it("the server's notes, a derived outcome and a spans-derived list are said", () => {
    const d = g.thread_t2_resumes
    expect(
      decisionsNote(
        { ...d, notes: ["the parked run could not be read: gone"] },
        "t2"
      )
    ).toBe("the parked run could not be read: gone")
    // A transcript-sourced block whose every outcome a record named: no caveat.
    expect(decisionsNote({ ...d, source: "transcript" }, "t2")).toBe("")
    expect(
      decisionsNote(
        { ...d, source: "spans", calls: [{ ...d.calls[0], derived: true }] },
        "t2"
      )
    ).toBe("derived from t2's spans: a resolved call has none")
  })

  it("an expired row says expired once; Studio's clock is the caller's", () => {
    const d = g.thread_t1_decided
    const c = {
      ...d.calls[0],
      outcome: "expired",
      reason: "expired: no decision before 09:00",
    }
    expect(decisionLine(c, d)).toBe(
      "⌛ expired · agent-7 · 'no decision before 09:00'"
    )
    expect(decisionDetail(d.calls[0], d, () => "local")).toBe(
      "via user · local"
    )
  })

  it("an approved or resolved row carries what the call left; a denial none", () => {
    const calls = [
      { callId: "r1", result: { content: "refund of 249.90 issued" } },
    ]
    expect(decidedResult({ call_id: "r1", outcome: "approved" }, calls)).toBe(
      "refund of 249.90 issued"
    )
    expect(decidedResult({ call_id: "r1", outcome: "denied" }, calls)).toBe("")
    expect(decidedResult({ call_id: "zz", outcome: "resolved" }, calls)).toBe(
      ""
    )
  })

  // U13.2-fix2 2: core emits no tool event for a resolve — the fold
  // never holds it; the server serves its result on the decided call.
  it("a resolved row's result is the server's: the resumed fixture's add_note", () => {
    const d = g.resumed_resumes
    expect(decidedResult(d.calls[1], [])).toBe("noted by hand")
    expect(
      decidedResult(
        { call_id: "r1", outcome: "resolved", result_badge: "hidden" },
        [{ callId: "r1", result: { content: "leaked" } }]
      )
    ).toBe("")
  })
})

describe("who decided, honestly (U13.2-fix2)", () => {
  it("two actors on a read token: no decider, the hidden badge", () => {
    const d = g.read_token_two_actors_t1_decided
    expect(d.calls[0].who_hidden).toBe(true)
    expect(line("read_token_two_actors_t1_decided")).toBe("✋ denied → t2")
  })

  it("a split verdict names its denier and the approvers as such", () => {
    const d = g.thread_t1_decided
    const c = { ...d.calls[0], who: "bob", approved_by: "alice", reason: "" }
    expect(decisionLine(c, d)).toBe("✋ denied by bob · approved by alice")
    // A hidden split names neither side, nor the actor in their place.
    expect(
      decisionLine(
        { call_id: "x", outcome: "denied", via: "quorum", who_hidden: true },
        d
      )
    ).toBe("✋ denied")
  })
})

describe("rw.support-desk's shape (U13.2-fix 6)", () => {
  const sd = golden<Record<string, DecisionDoc>>("decisions_support_desk")
  it("a plain Generate resumed with Approve, no actor: ✓ approved → <run>", () => {
    const d = sd.support_desk_decided
    expect(decisionLine(d.calls[0], d, turnTag(d.run))).toBe(
      `✓ approved → ${turnTag(d.run)}`
    )
    expect(decisionLine(d.calls[1], d)).toBe("↩ resolved")
  })
})

describe("one verb vocabulary (UX-H-20)", () => {
  it("approve · deny · resolve…, and a finished decision reads its outcome", () => {
    expect(Object.values(VERB)).toEqual(["approve", "deny", "resolve…"])
    for (const [d, w] of Object.entries(DONE))
      expect(appVerbWords({ state: "finished", decision: d }, "r").text).toBe(w)
  })
})

// UX-B-41's request expiry, one rule for both surfaces (P9-pre-web-fix6).
describe("a request expiry's rule, words and clock", () => {
  const AT = "2026-10-01T09:00:41.000Z"
  const at = Date.parse(AT)
  const mark = (expiry: unknown, call = "c1"): SessionMark => ({
    at: "2026-10-01T09:00:01Z",
    record: "thread",
    kind: "approval.request",
    event: "weft.thread.approval.request",
    lane: "approvals",
    run_id: "r1",
    turn: 1,
    content_mark: "none",
    fields: { call_id: call, expiry },
  })

  it("lapses past the stamp, never at it: thread's now.After(expiry)", () => {
    expect(expiryText(AT, at).lapsed).toBe(false)
    expect(expiryText(AT, at).text).toMatch(/\(in 0ms\)$/)
    expect(expiryText(AT, at + 1).lapsed).toBe(true)
    // The clock runs at the stamp (Studio's ticking, the panel's tick)
    // and stops past it.
    expect(expiryNext(AT, at)).toBe(1)
    expect(expiryNext(AT, at + 1)).toBe(0)
  })

  it("drops an unparsable stamp in requestExpiries, for both surfaces", () => {
    const exp = requestExpiries(
      [
        mark("not a time", "bad"),
        mark("", "empty"),
        mark(42, "num"),
        mark(AT, "c1"),
      ],
      "r1"
    )
    expect([...exp]).toEqual([["c1", AT]])
  })

  it("schedules the next redraw for when the words next change", () => {
    const words = (now: number) => expiryText(AT, now).text
    const check = (r: number) => {
      const now = at - r
      const d = expiryNext(AT, now)
      expect(d).toBeGreaterThan(0)
      // From an hour on nothing changes before the tick lands; below,
      // the clock draws once a second (Studio's cadence).
      if (r >= 3_600_000) expect(words(now + d - 1)).toBe(words(now))
      expect(words(now + d)).not.toBe(words(now))
      return d
    }
    // h:mm: the next minute the rounded seconds show.
    expect(check(7_000_000)).toBe(40_501)
    expect(check(3_600_000)).toBe(501)
    expect(words(at - 3_600_000 + 501)).toMatch(/\(in 59m59s\)$/)
    // m:ss: the rounding edge of each second.
    expect(check(120_000)).toBe(501)
    // Under a minute, tenths: the whole tenth of the next second.
    expect(check(40_000)).toBe(951)
    expect(words(at - 40_000 + 951)).toMatch(/\(in 39\.0s\)$/)
    expect(check(2_500)).toBe(451)
    // The lapse instant itself, when it comes first.
    expect(expiryNext(AT, at - 40)).toBe(41)
  })

  it("shares the run-marks read's path and cap rule", () => {
    expect(runMarksPath("s 1", "r/1")).toBe(
      "sessions/s%201/entries?run=r%2F1&limit=1000"
    )
    expect(marksCapped({ total: 2, entries: [1] })).toBe(true)
    expect(marksCapped({ total: 1, entries: [1] })).toBe(false)
  })
})
