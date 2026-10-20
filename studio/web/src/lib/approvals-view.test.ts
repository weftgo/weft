// lib/approvals-view.ts (U15.2-fix; UX-B-14, UX-S3-11): the session's
// decisions and grants from thread's records, on the seed's shapes
// (oncall-sre's grants, treasury-ops' signed quorum), and a call's
// approval from its execute_tool span.
import { describe, expect, it } from "vitest"

import type { SessionMark, TimelineTurn } from "./api"
import {
  APPROVED_BY,
  approvalFactsOf,
  decisionRows,
  grantRows,
  grantState,
  rowOutcome,
  usesWord,
} from "./approvals-view"

const S = "s_t"
const turn = (n: number, over: Partial<TimelineTurn> = {}): TimelineTurn => ({
  run_id: `${S}-t${n}`,
  turn: n,
  agent: "treasury-ops",
  status: "succeeded",
  started: `2026-10-19T12:00:0${n}.000Z`,
  finished: `2026-10-19T12:00:0${n}.500Z`,
  last_seen: `2026-10-19T12:00:0${n}.500Z`,
  steps: 1,
  usage: { input_tokens: 10, output_tokens: 5 },
  marks: 0,
  ...over,
})
const mark = (
  kind: string,
  run: number,
  fields: Record<string, unknown>,
  content?: Record<string, unknown>
): SessionMark => ({
  at: `2026-10-19T12:00:0${run}.400Z`,
  record: "thread",
  kind,
  event: `weft.thread.${kind}`,
  lane: kind.startsWith("grant.") ? "grants" : "approvals",
  run_id: `${S}-t${run}`,
  turn: run,
  content_mark: content ? "full" : "none",
  fields,
  content,
})

describe("decisionRows", () => {
  it("a signed quorum: both signers with their keys, the refused signatures beside the call, the resume", () => {
    const turns = [
      turn(1, { pending: 1, resumed_by: `${S}-t2` }),
      turn(2, { resumed_from: `${S}-t1` }),
    ]
    const marks = [
      mark("approval.request", 1, {
        call_id: "w1",
        tool: "wire_transfer",
        quorum: 2,
      }),
      mark("approval.refused", 1, { call_id: "w1", reason: "bad_signature" }),
      mark("approval.refused", 1, {
        call_id: "w1",
        key_id: "treasury-2026",
        reason: "args_changed",
      }),
      mark(
        "approval.decision",
        1,
        {
          call_id: "w1",
          key_id: "treasury-2026",
          outcome: "approve",
          via: "signed",
        },
        { who: "alice" }
      ),
      mark(
        "approval.decision",
        1,
        {
          call_id: "w1",
          key_id: "treasury-2025",
          outcome: "approve",
          via: "signed",
        },
        { who: "bob" }
      ),
    ]
    const rows = decisionRows(marks, turns)
    expect(
      rows.map((r) => [r.call, r.tool, r.who, r.parked, r.resumed])
    ).toEqual([
      [
        "w1",
        "wire_transfer",
        "alice · via signed · key treasury-2026",
        `${S}-t1`,
        `${S}-t2`,
      ],
      [
        "w1",
        "wire_transfer",
        "bob · via signed · key treasury-2025",
        `${S}-t1`,
        `${S}-t2`,
      ],
    ])
    expect(rows[0].refused.map((r) => [r.reason, r.key])).toEqual([
      ["bad signature", ""],
      ["args changed", "treasury-2026"],
    ])
    expect(rowOutcome(rows[0]).word).toBe("approved")
  })

  it("a grant's decision names the grant's tool; an undecided request awaits one; an expiry reads expired", () => {
    const marks = [
      mark("grant.mint", 1, {
        grant_id: "g1",
        tool: "run_command",
        args: 1,
        scope: "session",
      }),
      mark("approval.decision", 1, {
        call_id: "c1",
        grant_id: "g1",
        outcome: "approve",
        via: "grant",
      }),
      mark("approval.decision", 1, {
        call_id: "p1",
        outcome: "deny",
        via: "expiry",
      }),
      mark("approval.request", 2, { call_id: "x9", tool: "publish" }),
    ]
    const rows = decisionRows(marks, [turn(1), turn(2)])
    expect(rows.map((r) => [r.call, r.tool, rowOutcome(r).word])).toEqual([
      ["c1", "run_command", "approved"],
      ["p1", "", "expired"],
      ["x9", "publish", "awaiting a decision"],
    ])
  })

  it("approve always is said on the outcome", () => {
    const [r] = decisionRows(
      [
        mark("approval.decision", 1, {
          call_id: "e1",
          outcome: "approve",
          via: "user",
          always: true,
        }),
      ],
      [turn(1)]
    )
    expect(rowOutcome(r).word).toBe("approved · always")
  })

  it("a user's decision no stored resume names says its decider is not recorded yet; a grant's and an expiry's need none (P9-pre-web-fix3 F1)", () => {
    const rows = decisionRows(
      [
        mark("approval.decision", 1, {
          call_id: "u1",
          outcome: "approve",
          via: "user",
        }),
        mark("approval.decision", 1, {
          call_id: "g1",
          outcome: "approve",
          via: "grant",
        }),
        mark("approval.decision", 1, {
          call_id: "p1",
          outcome: "deny",
          via: "expiry",
        }),
        mark(
          "approval.decision",
          1,
          { call_id: "w1", outcome: "approve", via: "user" },
          { who: "bob" }
        ),
      ],
      [turn(1, { pending: 1 })]
    )
    expect(rows.map((r) => [r.call, !!r.whoUnrecorded])).toEqual([
      ["u1", true],
      ["g1", false],
      ["p1", false],
      ["w1", false],
    ])
  })
})

describe("grantRows", () => {
  it("minted, used n of max, revoked or lapsed; a shared store's grant minted elsewhere", () => {
    const marks = [
      mark(
        "grant.mint",
        1,
        {
          grant_id: "g1",
          tool: "run_command",
          args: 2,
          max_uses: 2,
          scope: "session",
        },
        { reason: "two restarts" }
      ),
      mark("grant.use", 1, {
        grant_id: "g1",
        call_id: "x1",
        outcome: "approved",
        uses: 1,
        max_uses: 2,
      }),
      mark("grant.use", 2, {
        grant_id: "g1",
        call_id: "x2",
        outcome: "approved",
        uses: 2,
        max_uses: 2,
      }),
      mark("grant.expire", 2, {
        grant_id: "g1",
        reason: "max_uses",
        tool: "run_command",
      }),
      mark("grant.use", 1, {
        grant_id: "team-df",
        call_id: "c4",
        outcome: "approved",
        scope: "shared",
      }),
      mark("grant.mint", 2, {
        grant_id: "g2",
        tool: "run_command",
        args: 1,
        deny: true,
        scope: "session",
      }),
      mark("grant.revoke", 2, { grant_id: "g2", tool: "run_command" }),
    ]
    const g = grantRows(marks)
    expect(
      g.map((x) => [x.id, x.rule, usesWord(x), grantState(x), x.shared])
    ).toEqual([
      ["g1", "allow · 2 args globs", "2 of 2", "expired · max uses", false],
      ["team-df", "", "1", "live", true],
      ["g2", "deny · 1 args glob", "0", "revoked", false],
    ])
    expect(g[0].reason).toBe("two restarts")
    expect(g[0].uses.map((u) => [u.call, u.run])).toEqual([
      ["x1", `${S}-t1`],
      ["x2", `${S}-t2`],
    ])
  })

  it("an expiry that passed with no use after it says so, never live", () => {
    const [g] = grantRows([
      mark("grant.mint", 1, {
        grant_id: "g3",
        tool: "run_command",
        expiry: "2026-10-19T12:00:05.000Z",
      }),
    ])
    expect(grantState(g, Date.parse("2026-10-19T12:00:04.000Z"))).toMatch(
      /^live · expires /
    )
    expect(grantState(g, Date.parse("2026-10-19T12:00:06.000Z"))).toMatch(
      /^expiry passed .* · no use after$/
    )
  })
})

describe("approvalFactsOf", () => {
  it("indexes the run's own approved execute_tool spans by call id", () => {
    const f = approvalFactsOf(
      [
        {
          attrs: {
            "gen_ai.operation.name": "execute_tool",
            "weft.run.id": "r",
            "gen_ai.tool.call.id": "c1",
            "weft.tool.approved_by": "grant",
            "weft.grant.id": "g1",
          },
        },
        {
          attrs: {
            "gen_ai.operation.name": "execute_tool",
            "weft.run.id": "r",
            "gen_ai.tool.call.id": "c2",
            "weft.tool.approved_by": "approver",
          },
        },
        {
          attrs: {
            "gen_ai.operation.name": "execute_tool",
            "weft.run.id": "r",
            "gen_ai.tool.call.id": "c3",
          },
        },
        {
          attrs: {
            "gen_ai.operation.name": "execute_tool",
            "weft.run.id": "other",
            "gen_ai.tool.call.id": "c4",
            "weft.tool.approved_by": "user",
          },
        },
      ],
      "r"
    )
    expect([...f.keys()]).toEqual(["c1", "c2"])
    expect(APPROVED_BY.chip(f.get("c1")!)).toBe("✓ approved · by grant g1")
    expect(APPROVED_BY.chip(f.get("c2")!)).toBe("✓ approved · by approver")
  })
})
