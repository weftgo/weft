// lib/decisions.ts over studio/testdata/api/decisions.golden.json (the
// run documents' decided / resumes blocks, studio/decisions_test.go):
// the outcome line each surface draws, the resumed card's head, the
// honesty note, the one verb vocabulary, and step 0 without the
// decided calls.
import { describe, expect, it } from "vitest"

import type { DecisionDoc } from "./api"
import {
  decider,
  decisionDetail,
  decisionLine,
  decisionsNote,
  DONE,
  OUTCOME,
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
    expect(decisionsNote({ ...d, source: "transcript" }, "t2")).toContain(
      "derived from t2's pre-step results"
    )
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
    expect(
      withoutDecided(f, {
        resumes: g.thread_t2_resumes,
      }).steps[0].toolCalls.map((c) => c.callId)
    ).toEqual(["sub", "m1"])
  })
})

describe("one verb vocabulary (UX-H-20)", () => {
  it("approve · deny · resolve…, and a finished decision reads its outcome", () => {
    expect(Object.values(VERB)).toEqual(["approve", "deny", "resolve…"])
    for (const [d, w] of Object.entries(DONE))
      expect(appVerbWords({ state: "finished", decision: d }, "r").text).toBe(w)
  })
})
